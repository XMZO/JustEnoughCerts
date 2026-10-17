package namecheap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/buildinfo"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// The vendor's API, as the official client v1.15.10 uses it (PLAN.md 5.6).
// Every call is a POST, with a JSON body if any, signed with a bearer
// token: the one-time token from Namecheap's panel to register, then the
// access token registering gives. The calls live under the user API's
// address, but for refreshing the token. A call fails on a status of 300
// or more, or with a z-error-code header. The API is not public: it may
// change at any time.
const (
	defaultURL = "https://externalautossl.service.spaceship.com/api/v1/externalautossl/user"
	// tool is the name the API knows the client by, and compatible the
	// version of the official client JEC speaks as.
	tool       = "ssl-manager"
	compatible = "v1.15.10"
	maxBody    = 1 << 20 // bytes of a response read
	maxSnippet = 300     // bytes of a failed response kept for the log
)

// States of a certificate at the vendor that the official client knows;
// the others, such as a CA reviewing the order, it waits through.
const (
	stateAwaitingCSR        = "Awaiting CSR"
	stateAwaitingChallenges = "Awaiting Challenges Installation"
	stateIssued             = "Certificate Issued"
	stateRevoked            = "Certificate Revoked"
	stateFailed             = "Order Failed"
)

// apiError is a call the API answered with a failure.
type apiError struct {
	Status int
	Code   string // the z-error-code header, if any
	Body   string // the start of the answer
	// RetryAfter is what a 429 asked to wait, if it said.
	RetryAfter time.Duration
}

func (e *apiError) Error() string {
	s := fmt.Sprintf("namecheap: status %d", e.Status)
	if e.Code != "" {
		s += ", error " + e.Code
	}
	if e.Body != "" {
		s += ": " + e.Body
	}
	return s
}

// client calls the API at base, the user API's address.
type client struct {
	base string
	http *http.Client
}

// userAgent is the official client's, which the API may check, with JEC
// named after it.
func userAgent() string {
	return fmt.Sprintf("sslManager/%s (%s; %s; %s) JustEnoughCerts/%s",
		compatible, runtime.Version(), runtime.GOOS, runtime.GOARCH, buildinfo.Get().Version)
}

// user and vendor return the URL of a call under the user API, or under
// the vendor API next to it, which refreshes tokens.
func (c *client) user(path string) string { return strings.TrimRight(c.base, "/") + path }

func (c *client) vendor(path string) string {
	return strings.TrimSuffix(strings.TrimRight(c.base, "/"), "/user") + path
}

// call POSTs body, if not nil, as JSON to address with token, and decodes
// the answer into result, if not nil.
func (c *client) call(ctx context.Context, token, address string, body, result any) error {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent())
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 || resp.Header.Get("z-error-code") != "" {
		e := &apiError{Status: resp.StatusCode, Code: resp.Header.Get("z-error-code"), Body: snippet(data)}
		if resp.StatusCode == http.StatusTooManyRequests {
			e.RetryAfter, _ = retry.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		}
		return e
	}
	if result == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("namecheap: the answer of %s: %w", address, err)
	}
	return nil
}

// snippet keeps the start of a failed answer, on one line.
func snippet(data []byte) string {
	s := strings.Join(strings.Fields(string(data)), " ")
	if len(s) > maxSnippet {
		s = s[:maxSnippet] + "…"
	}
	return s
}

// certificate is a certificate at the vendor, as the API shows it: an
// order's next certificate, waiting for a CSR, being validated, issued.
type certificate struct {
	ID           string   `json:"id"`
	Domains      []string `json:"domains"`
	State        string   `json:"state"`
	SerialNumber string   `json:"serialNumber"`
	// ModulusSHA1 is the SHA-1 of the RSA modulus of the certificate's
	// key, in hexadecimal: which key it is for.
	ModulusSHA1    string `json:"modulusSha1"`
	InstallationID string `json:"installationId"`
	Wildcard       bool   `json:"wildcard"`
}

// challenge is a domain validation of a certificate.
type challenge struct {
	ID     string `json:"id"`
	Domain string `json:"domain"`
	Method string `json:"method"` // HTTP, DNS or Email
	State  string `json:"state"`
	// Meta holds the file of an HTTP challenge, or the record of a DNS one.
	Meta         json.RawMessage `json:"meta"`
	Instructions *struct {
		Files []webFile `json:"files"`
	} `json:"instructions"`
}

// Methods of challenges.
const methodHTTP = "HTTP"

// webFile is what an HTTP challenge has the website answer: the content at
// a URL path of the domain.
type webFile struct {
	FilePath string `json:"filePath"`
	Content  string `json:"content"`
}

// files returns the files of an HTTP challenge, from its meta or else its
// instructions.
func (ch challenge) files() []webFile {
	var f webFile
	if json.Unmarshal(ch.Meta, &f) == nil && f.FilePath != "" {
		return []webFile{f}
	}
	if ch.Instructions != nil {
		return ch.Instructions.Files
	}
	return nil
}

type page[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
}

// register signs this client up with a one-time token, and returns the
// access token and the integration hash, if the API gave one.
func (c *client) register(ctx context.Context, oneTime string) (access, hash string, err error) {
	var out struct {
		AccessToken     string `json:"accessToken"`
		IntegrationHash string `json:"integrationHash"`
	}
	if err := c.call(ctx, oneTime, c.user("/integrations/create"), map[string]string{"tool": tool}, &out); err != nil {
		return "", "", err
	}
	if out.AccessToken == "" {
		return "", "", errors.New("namecheap: registered, but no access token came back")
	}
	return out.AccessToken, out.IntegrationHash, nil
}

// hash returns the integration hash, which status calls identify it by.
func (c *client) hash(ctx context.Context, token string) (string, error) {
	var out struct {
		IntegrationHash string `json:"integrationHash"`
	}
	if err := c.call(ctx, token, c.user("/integrations/hash"), nil, &out); err != nil {
		return "", err
	}
	if out.IntegrationHash == "" {
		return "", errors.New("namecheap: no integration hash came back")
	}
	return out.IntegrationHash, nil
}

// meta tells the API what runs the client, as the official client does
// after registering.
func (c *client) meta(ctx context.Context, token string) error {
	version := buildinfo.Get().Version
	body := map[string]any{
		"tool": tool,
		"meta": map[string]any{
			"tool":         map[string]string{"name": tool, "version": compatible},
			"system":       map[string]any{"os": runtime.GOOS, "distro": "JustEnoughCerts", "version": version, "arch": runtime.GOARCH, "goVersion": runtime.Version(), "privileged": false},
			"controlPanel": map[string]string{"type": "manual_interactive", "version": version},
		},
	}
	return c.call(ctx, token, c.user("/integrations/updateMeta"), body, nil)
}

// status returns the integration's fingerprint, which changes when
// something of its certificates does.
func (c *client) status(ctx context.Context, token, hash string) (string, error) {
	var out struct {
		Fingerprint string `json:"fingerprint"`
	}
	address := c.user("/integrations/status") + "?" + url.Values{"integrationHash": {hash}}.Encode()
	if err := c.call(ctx, token, address, nil, &out); err != nil {
		return "", err
	}
	return out.Fingerprint, nil
}

// certificates lists the certificates of domain, the newest first, or all
// of them for "".
func (c *client) certificates(ctx context.Context, token, domain string) ([]certificate, error) {
	var body any
	if domain != "" {
		body = struct {
			Filters map[string]any `json:"filters"`
			Skip    *int           `json:"skip"`
			Take    *int           `json:"take"`
		}{Filters: map[string]any{"order.domains.fqdn": map[string]string{"$eq": domain}}}
	}
	var out page[certificate]
	if err := c.call(ctx, token, c.user("/certificates/list"), body, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// request sends the CSR, in PEM form, for certificate id.
func (c *client) request(ctx context.Context, token, id string, csr []byte) (certificate, error) {
	var out certificate
	body := map[string]string{"id": id, "content": base64.StdEncoding.EncodeToString(csr)}
	err := c.call(ctx, token, c.user("/certificates/request"), body, &out)
	return out, err
}

// challenges lists the domain validations of certificate id.
func (c *client) challenges(ctx context.Context, token, id string) ([]challenge, error) {
	var out page[challenge]
	if err := c.call(ctx, token, c.user("/certificates/listChallenges"), map[string]string{"id": id}, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// installed reports that the answer of challenge id is in place.
func (c *client) installed(ctx context.Context, token, id string) error {
	return c.call(ctx, token, c.user("/certificates/markChallengeInstalled"), map[string]string{"id": id}, nil)
}

// pem returns certificate id with its chain, in PEM form.
func (c *client) pem(ctx context.Context, token, id string) ([]byte, error) {
	var out struct {
		Content string `json:"content"`
	}
	if err := c.call(ctx, token, c.user("/certificates/readPEMFormatted"), map[string]string{"id": id}, &out); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(out.Content)
	if err != nil {
		return nil, fmt.Errorf("namecheap: the certificate is not base64: %w", err)
	}
	return data, nil
}

// confirm reports that certificate id is installed, under a name for the
// installation.
func (c *client) confirm(ctx context.Context, token, id, installation string) error {
	return c.call(ctx, token, c.user("/certificates/confirmInstallation"), map[string]string{"id": id, "installationId": installation}, nil)
}

// refresh trades the access token for a new one, with the Unix times it was
// issued at and expires at.
func (c *client) refresh(ctx context.Context, token string) (access string, issued, expires int64, err error) {
	var out struct {
		AccessToken string `json:"accessToken"`
		IssuedAt    int64  `json:"issuedAt"`
		ExpiresAt   int64  `json:"expiresAt"`
	}
	if err := c.call(ctx, token, c.vendor("/vendor/integration-token/refresh"), nil, &out); err != nil {
		return "", 0, 0, err
	}
	if out.AccessToken == "" || out.IssuedAt <= 0 || out.ExpiresAt <= 0 {
		return "", 0, 0, errors.New("namecheap: a refreshed token without its token or times")
	}
	return out.AccessToken, out.IssuedAt, out.ExpiresAt, nil
}
