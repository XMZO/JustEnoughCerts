package onepanel

import (
	"cmp"
	"context"
	"errors"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

func panelType() providers.Type {
	return providers.Type{
		ID: "onepanel.panel", Kind: providers.Target, Name: i18n.OnepanelPanelName, Description: i18n.OnepanelPanelDescription,
		Tags: []string{"china", "self_hosted"}, Testable: true,
		Fields: []providers.Field{
			{Key: "url", Label: i18n.OnepanelFieldUrl, Help: i18n.OnepanelFieldPanelUrlHelp, Kind: providers.URL, Required: true},
			{Key: "api_key", Label: i18n.OnepanelFieldApiKey, Help: i18n.OnepanelFieldApiKeyHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "api_version", Label: i18n.OnepanelFieldApiVersion, Kind: providers.Select, Required: true, Default: apiV2, Options: []providers.Option{
				{Value: apiV2, Label: i18n.OnepanelApiV2},
				{Value: apiV1, Label: i18n.OnepanelApiV1},
			}},
			{Key: "restart", Label: i18n.OnepanelFieldRestart, Help: i18n.OnepanelFieldRestartHelp, Kind: providers.Switch, Default: "true"},
			{Key: "fingerprint", Label: i18n.OnepanelFieldFingerprint, Help: i18n.OnepanelFieldFingerprintHelp, Kind: providers.Text, Advanced: true},
			{Key: "allow_private", Label: i18n.OnepanelFieldAllowPrivate, Help: i18n.OnepanelFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true},
		},
	}
}

// panelSSL is a 1Panel whose own HTTPS certificate JEC keeps.
type panelSSL struct {
	*panel
	version string
	restart bool
}

var (
	_ contracts.Target = (*panelSSL)(nil)
	_ contracts.Tester = (*panelSSL)(nil)
)

// panelFactory reads the settings of in, as factory does.
func panelFactory(ctx context.Context, in providers.Instance) (any, error) {
	p, err := factory(ctx, in)
	if err != nil {
		return nil, err
	}
	return &panelSSL{panel: p.(*panel), version: cmp.Or(in.Config["api_version"], apiV2), restart: in.Config["restart"] != "false"}, nil
}

// paths are those of the API that set and tell the panel's certificate.
func (s *panelSSL) paths() (update, info string) {
	if s.version == apiV1 {
		return "/settings/ssl/update", "/settings/ssl/info"
	}
	return "/core/settings/ssl/update", "/core/settings/ssl/info"
}

// Test tells the panel's certificate, changing nothing.
func (s *panelSSL) Test(ctx context.Context) (contracts.Message, error) {
	var info struct {
		Domain  string `json:"domain"`
		Timeout string `json:"timeout"`
	}
	_, path := s.paths()
	if err := s.call(ctx, path, nil, &info); err != nil {
		if s.plainToHTTPS(err) {
			return contracts.Message{}, retry.Permanent(&contracts.Problem{Key: i18n.OnepanelErrorHttpsNow, Params: map[string]any{"address": "https://" + s.host}})
		}
		return contracts.Message{}, s.explain(err)
	}
	if info.Domain == "" {
		return contracts.Message{Key: i18n.OnepanelTestPlain}, nil
	}
	return contracts.Message{Key: i18n.OnepanelTestPanel, Params: map[string]any{"domain": info.Domain, "until": info.Timeout}}, nil
}

// Deploy makes the certificate the panel's own, turning its HTTPS on if
// it was off, and has the panel restart to serve it, if so set. There is
// one, overwritten, so nothing to remove afterwards.
func (s *panelSSL) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("onepanel: no certificate to deploy"))
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	ssl := "Enable"
	if s.version == apiV1 {
		ssl = "enable"
	}
	body := map[string]any{
		"cert": string(cryptox.EncodeCertificates(req.Chain...)), "key": string(key),
		"sslType": "import-paste", "ssl": ssl, "autoRestart": strconv.FormatBool(s.restart),
	}
	names := cryptox.Names(req.Chain[0])
	path, info := s.paths()
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.OnepanelLogPanel})
	target := s.panel
	if s.opts.Pin != "" {
		// The panel may serve an earlier version, for the same names, even
		// when reached by its address.
		if target, err = s.trusting(s.base.Scheme, s.opts, names); err != nil {
			return none, retry.Permanent(err)
		}
		defer target.client.CloseIdleConnections()
	}
	// Reached over HTTP, it may have turned its HTTPS on, as at an earlier
	// deployment: then over HTTPS, trusting the certificate JEC deployed.
	// A request with a body would see the connection cut before the panel
	// says so, hence the look first.
	if s.base.Scheme == "http" && s.plainToHTTPS(target.call(ctx, info, nil, nil)) {
		log(contracts.LogLine{Level: contracts.Warn, Key: i18n.OnepanelLogHttps, Params: map[string]any{"address": addressFor(s.host, names)}})
		if target, err = s.trusting("https", httpx.Options{Timeout: time.Minute, PrivateOnly: true, Roots: s.opts.Roots}, names); err != nil {
			return none, retry.Permanent(err)
		}
		defer target.client.CloseIdleConnections()
	}
	if err = target.call(ctx, path, body, nil); err != nil {
		return none, s.explain(err)
	}
	if s.restart {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.OnepanelLogRestart})
	}
	if s.base.Scheme == "http" {
		log(contracts.LogLine{Level: contracts.Warn, Key: i18n.OnepanelLogUseHttps, Params: map[string]any{"address": addressFor(s.host, names)}})
	}
	return none, nil
}

// trusting returns the panel over scheme, with a client of opts that
// trusts a certificate a CA vouches for one of names, or for the host.
func (s *panelSSL) trusting(scheme string, opts httpx.Options, names []string) (*panel, error) {
	opts.Names = append(slices.Clone(opts.Names), names...)
	if !slices.Contains(opts.Names, s.base.Hostname()) {
		opts.Names = append(opts.Names, s.base.Hostname())
	}
	client, err := httpx.Client(opts)
	if err != nil {
		return nil, err
	}
	client.CheckRedirect = s.client.CheckRedirect
	p := *s.panel
	p.client, p.opts = client, opts
	base := *s.base
	base.Scheme = scheme
	p.base = &base
	return &p, nil
}

// plainToHTTPS reports whether err is the answer of a panel serving HTTPS
// to a request in plain HTTP.
func (s *panelSSL) plainToHTTPS(err error) bool {
	var se *statusError
	return s.base.Scheme == "http" && errors.As(err, &se) && se.status == http.StatusBadRequest && strings.Contains(se.text, "HTTPS server")
}

// addressFor is the address of the panel at host, by the first of names
// that is no wildcard, with host's port.
func addressFor(host string, names []string) string {
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	for _, n := range names {
		if !strings.HasPrefix(n, "*.") {
			name = n
			break
		}
	}
	if _, port, err := net.SplitHostPort(host); err == nil {
		return "https://" + net.JoinHostPort(name, port)
	}
	return "https://" + name
}
