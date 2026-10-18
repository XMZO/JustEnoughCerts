package oidc

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// discovery is what JEC reads of a provider's discovery document
// (OpenID Connect Discovery 1.0).
type discovery struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	TokenAuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
}

// algorithms are those an ID token may be signed with: asymmetric ones
// only, never none, nor HS256, whose key is the client secret.
var algorithms = []jose.SignatureAlgorithm{jose.RS256, jose.RS384, jose.RS512, jose.PS256, jose.PS384, jose.PS512, jose.ES256, jose.ES384, jose.ES512, jose.EdDSA}

// getJSON fetches url and decodes the JSON it answers into out. The
// addresses are the provider's, which people set themselves.
func getJSON(ctx context.Context, client *http.Client, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil) //nolint:gosec // G704: the provider people set
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req) //nolint:gosec // G704: the provider people set
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oidc: GET %s: HTTP %d", u, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("oidc: GET %s: %w", u, err)
	}
	return nil
}

// discover reads the discovery document of issuer, which must name itself
// the same and give the endpoints a sign-in needs.
func discover(ctx context.Context, client *http.Client, issuer string) (discovery, error) {
	var d discovery
	err := getJSON(ctx, client, strings.TrimSuffix(issuer, "/")+"/.well-known/openid-configuration", &d)
	switch {
	case err != nil:
		return d, err
	case d.Issuer != issuer:
		return d, fmt.Errorf("oidc: the provider names itself %q, not %q", d.Issuer, issuer)
	case d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.JWKSURI == "":
		return d, errors.New("oidc: the discovery document lacks an endpoint")
	}
	return d, nil
}

// challenge is the PKCE code challenge of verifier (RFC 7636, S256).
func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// authURL is where a browser goes to sign in at the provider.
func authURL(d discovery, clientID, redirect, state, nonce, verifier string) string {
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect}, "scope": {"openid email profile"},
		"state": {state}, "nonce": {nonce}, "code_challenge": {challenge(verifier)}, "code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(d.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return d.AuthorizationEndpoint + sep + q.Encode()
}

// exchange trades code for the provider's ID token, authenticating with
// the client secret, if there is one, as the provider prefers.
func exchange(ctx context.Context, client *http.Client, d discovery, clientID, secret, redirect, code, verifier string) (string, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}}
	basic := secret != "" && (len(d.TokenAuthMethods) == 0 || slices.Contains(d.TokenAuthMethods, "client_secret_basic"))
	if !basic {
		form.Set("client_id", clientID)
		if secret != "" {
			form.Set("client_secret", secret)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if basic {
		req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(secret))
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		IDToken     string `json:"id_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("oidc: token endpoint: HTTP %d: %w", resp.StatusCode, err)
	}
	switch {
	case out.Error != "":
		return "", fmt.Errorf("oidc: token endpoint: %s: %s", out.Error, out.Description)
	case resp.StatusCode != http.StatusOK || out.IDToken == "":
		return "", fmt.Errorf("oidc: token endpoint: HTTP %d without an ID token", resp.StatusCode)
	}
	return out.IDToken, nil
}

// identity is who an ID token says signed in.
type identity struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified *bool  `json:"email_verified"`
	Nonce         string `json:"nonce"`
	AuthorizedBy  string `json:"azp"`
}

// verify checks the signature of an ID token by the keys of the provider,
// its issuer, its audience, its times and its nonce, and returns who it
// says signed in. The keys are fetched again once when none has the ID the
// token names, as after the provider rotated them.
func verify(ctx context.Context, client *http.Client, d discovery, keys *jose.JSONWebKeySet, raw, clientID, nonce string, now time.Time) (identity, *jose.JSONWebKeySet, error) {
	var who identity
	tok, err := jwt.ParseSigned(raw, algorithms)
	if err != nil {
		return who, keys, err
	}
	if len(tok.Headers) != 1 {
		return who, keys, errors.New("oidc: an ID token with more than one signature")
	}
	kid := tok.Headers[0].KeyID
	key := keyFor(keys, kid)
	if key == nil {
		keys = &jose.JSONWebKeySet{}
		if err := getJSON(ctx, client, d.JWKSURI, keys); err != nil {
			return who, nil, err
		}
		if key = keyFor(keys, kid); key == nil {
			return who, keys, fmt.Errorf("oidc: no key %q to check the ID token with", kid)
		}
	}
	var claims jwt.Claims
	if err := tok.Claims(key.Key, &claims, &who); err != nil {
		return who, keys, err
	}
	if err := claims.ValidateWithLeeway(jwt.Expected{Issuer: d.Issuer, AnyAudience: jwt.Audience{clientID}, Time: now}, time.Minute); err != nil {
		return who, keys, err
	}
	switch {
	case claims.Expiry == nil || claims.IssuedAt == nil:
		return who, keys, errors.New("oidc: an ID token without exp or iat")
	case len(claims.Audience) > 1 && who.AuthorizedBy != clientID:
		return who, keys, errors.New("oidc: an ID token for another party")
	case who.Nonce == "" || who.Nonce != nonce:
		return who, keys, errors.New("oidc: the nonce of the ID token is not the sign-in's")
	case who.Subject == "":
		return who, keys, errors.New("oidc: an ID token without sub")
	}
	return who, keys, nil
}

// keyFor returns the key of keys with ID kid, or the one signing key when
// kid is empty and there is one.
func keyFor(keys *jose.JSONWebKeySet, kid string) *jose.JSONWebKey {
	if keys == nil {
		return nil
	}
	var found []jose.JSONWebKey
	for _, k := range keys.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		if kid == "" || k.KeyID == kid {
			found = append(found, k)
		}
	}
	if len(found) != 1 || !found[0].Valid() || !found[0].IsPublic() {
		return nil
	}
	return &found[0]
}
