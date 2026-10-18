package oidc

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/auth"
)

// callbackPath ends the address the provider sends people back to.
const callbackPath = "api/v1/oidc/callback"

// settings are those of the sign-on, the client secret opened.
type settings struct {
	Enabled     bool
	Label       string
	Issuer      string
	ClientID    string
	Secret      string
	Allowed     []string
	RedirectURI string
}

// load returns the settings, the zero value before any were saved.
func (m *Module) load(ctx context.Context) (settings, error) {
	var s settings
	var enabled int
	var sealed []byte
	var allowed string
	err := m.db.QueryRowContext(ctx, `SELECT enabled, label, issuer, client_id, client_secret, allowed, redirect_uri FROM oidc_settings WHERE id = 1`).
		Scan(&enabled, &s.Label, &s.Issuer, &s.ClientID, &sealed, &allowed, &s.RedirectURI)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return settings{}, nil
	case err != nil:
		return s, err
	}
	s.Enabled = enabled == 1
	if allowed != "" {
		s.Allowed = strings.Split(allowed, "\n")
	}
	if sealed != nil {
		secret, err := m.secrets.Open(sealed, "oidc client secret")
		if err != nil {
			return s, err
		}
		s.Secret = string(secret)
	}
	return s, nil
}

// OIDCSettingsBody is the sign-on's settings, as the API shows them; the
// name is the schema's in the OpenAPI document, beside other modules'.
type OIDCSettingsBody struct { //nolint:revive // the schema's name
	Enabled     bool     `json:"enabled"`
	Label       string   `json:"label" maxLength:"64" doc:"The provider's name on the sign-in page"`
	Issuer      string   `json:"issuer" maxLength:"512" doc:"The provider's issuer URL, as its discovery document names it"`
	ClientID    string   `json:"clientId" maxLength:"512"`
	HasSecret   bool     `json:"hasSecret" doc:"A client secret is set; it is never shown"`
	Allowed     []string `json:"allowed" maxItems:"100" doc:"Who may sign in: email addresses, or the provider's subject identifiers"`
	RedirectURI string   `json:"redirectUri" maxLength:"1024" doc:"Where the provider sends people back, as the provider has it"`
}

type settingsOutput struct {
	Body OIDCSettingsBody
}

type oidcSettingsInput struct {
	Body struct {
		Enabled      bool     `json:"enabled"`
		Label        string   `json:"label" maxLength:"64"`
		Issuer       string   `json:"issuer" maxLength:"512"`
		ClientID     string   `json:"clientId" maxLength:"512"`
		ClientSecret *string  `json:"clientSecret,omitempty" maxLength:"1024" doc:"Replaces the client secret when given; empty removes it"`
		Allowed      []string `json:"allowed" maxItems:"100"`
		RedirectURI  string   `json:"redirectUri" maxLength:"1024"`
	}
}

// OIDCStatusBody tells the sign-in page whether to offer single sign-on.
type OIDCStatusBody struct { //nolint:revive // the schema's name
	Enabled bool   `json:"enabled"`
	Label   string `json:"label" doc:"The provider's name, for the sign-in button"`
}

type statusOutput struct {
	Body OIDCStatusBody
}

func (s settings) body() OIDCSettingsBody {
	return OIDCSettingsBody{
		Enabled: s.Enabled, Label: s.Label, Issuer: s.Issuer, ClientID: s.ClientID, HasSecret: s.Secret != "",
		Allowed: append([]string{}, s.Allowed...), RedirectURI: s.RedirectURI,
	}
}

// label is the name the sign-in button shows: the one set, or else the
// issuer's host.
func (s settings) label() string {
	if s.Label != "" {
		return s.Label
	}
	if u, err := url.Parse(s.Issuer); err == nil {
		return u.Hostname()
	}
	return s.Issuer
}

func (m *Module) register(a huma.API) {
	tags := []string{"oidc"}
	only := map[string]any{auth.SessionOnly: true}
	huma.Register(a, huma.Operation{
		OperationID: "oidc-settings", Method: http.MethodGet, Path: "/settings", Tags: tags, Metadata: only,
		Summary: "Get the settings of single sign-on",
	}, func(ctx context.Context, _ *struct{}) (*settingsOutput, error) {
		s, err := m.load(ctx)
		if err != nil {
			return nil, err
		}
		return &settingsOutput{Body: s.body()}, nil
	})
	huma.Register(a, huma.Operation{
		OperationID: "oidc-save", Method: http.MethodPut, Path: "/settings", Tags: tags, Metadata: only,
		Summary:     "Save the settings of single sign-on",
		Description: "Switched on, the settings are checked against the provider's discovery document first.",
	}, m.save)
	huma.Register(a, huma.Operation{
		OperationID: "oidc-status", Method: http.MethodGet, Path: "/status", Tags: tags, Security: api.Public,
		Summary: "Tell whether the sign-in page offers single sign-on",
	}, func(ctx context.Context, _ *struct{}) (*statusOutput, error) {
		s, err := m.load(ctx)
		if err != nil {
			return nil, err
		}
		return &statusOutput{Body: OIDCStatusBody{Enabled: s.Enabled, Label: s.label()}}, nil
	})
	m.registerFlow(a, tags)
}

// save checks and stores the settings: switched on, they must name a
// provider whose discovery document answers, a client, someone allowed,
// and the address the provider sends people back to.
func (m *Module) save(ctx context.Context, in *oidcSettingsInput) (*settingsOutput, error) {
	old, err := m.load(ctx)
	if err != nil {
		return nil, err
	}
	b := in.Body
	s := settings{
		Enabled: b.Enabled, Label: strings.TrimSpace(b.Label), Issuer: strings.TrimSpace(b.Issuer), ClientID: strings.TrimSpace(b.ClientID),
		Secret: old.Secret, RedirectURI: strings.TrimSpace(b.RedirectURI),
	}
	if b.ClientSecret != nil {
		s.Secret = strings.TrimSpace(*b.ClientSecret)
	}
	for _, a := range b.Allowed {
		if a = strings.TrimSpace(a); a != "" && !slices.Contains(s.Allowed, a) {
			s.Allowed = append(s.Allowed, a)
		}
	}
	if s.Enabled {
		if err := m.check(ctx, s); err != nil {
			return nil, err
		}
	}
	var sealed []byte
	if s.Secret != "" {
		sealed = m.secrets.Seal([]byte(s.Secret), "oidc client secret")
	}
	enabled := 0
	if s.Enabled {
		enabled = 1
	}
	_, err = m.db.ExecContext(ctx, `INSERT INTO oidc_settings (id, enabled, label, issuer, client_id, client_secret, allowed, redirect_uri, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (id) DO UPDATE SET enabled = excluded.enabled, label = excluded.label, issuer = excluded.issuer,
		client_id = excluded.client_id, client_secret = excluded.client_secret, allowed = excluded.allowed, redirect_uri = excluded.redirect_uri,
		updated_at = excluded.updated_at`,
		enabled, s.Label, s.Issuer, s.ClientID, sealed, strings.Join(s.Allowed, "\n"), s.RedirectURI, m.now().UnixMilli())
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.cached = nil
	m.mu.Unlock()
	return &settingsOutput{Body: s.body()}, nil
}

// check tells what is wrong with settings to switch on, field by field.
func (m *Module) check(ctx context.Context, s settings) error {
	invalid := api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil)
	bad := false
	field := func(name string, key i18n.Key) {
		invalid = invalid.WithField("body."+name, key)
		bad = true
	}
	if !issuerOK(s.Issuer) {
		field("issuer", i18n.OidcErrorIssuer)
	}
	if s.ClientID == "" {
		field("clientId", i18n.KernelErrorFieldRequired)
	}
	if len(s.Allowed) == 0 {
		field("allowed", i18n.KernelErrorFieldRequired)
	}
	if u, err := url.Parse(s.RedirectURI); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || !strings.HasSuffix(u.Path, "/"+callbackPath) {
		field("redirectUri", i18n.OidcErrorRedirect)
	}
	if bad {
		return invalid
	}
	if _, err := discover(ctx, m.client, s.Issuer); err != nil {
		m.log.Warn("oidc discovery", "issuer", s.Issuer, "err", err)
		return api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil).WithField("body.issuer", i18n.OidcErrorDiscovery)
	}
	return nil
}

// issuerOK tells whether issuer is an address JEC sends a client secret to:
// over HTTPS, or plain HTTP to this machine or an address of the local
// network.
func issuerOK(issuer string) bool {
	u, err := url.Parse(issuer)
	if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		if u.Hostname() == "localhost" {
			return true
		}
		ip := net.ParseIP(u.Hostname())
		return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
	}
	return false
}
