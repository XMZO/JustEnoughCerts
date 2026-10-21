package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// nodePattern is what the name of a node is made of.
var nodePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,62}$`)

func nodeType() providers.Type {
	return providers.Type{
		ID: "proxmox.node", Kind: providers.Target, Name: i18n.ProxmoxNodeName, Description: i18n.ProxmoxNodeDescription, Untested: true,
		Tags: []string{"self_hosted"}, Testable: true,
		Fields: []providers.Field{
			{Key: "url", Label: i18n.ProxmoxFieldUrl, Help: i18n.ProxmoxFieldUrlHelp, Kind: providers.URL, Required: true},
			{Key: "node", Label: i18n.ProxmoxFieldNode, Help: i18n.ProxmoxFieldNodeHelp, Kind: providers.Text, Required: true},
			{Key: "token_id", Label: i18n.ProxmoxFieldTokenId, Help: i18n.ProxmoxFieldTokenIdHelp, Kind: providers.Text, Required: true},
			{Key: "token_secret", Label: i18n.ProxmoxFieldTokenSecret, Kind: providers.Text, Secret: true, Required: true},
			{Key: "restart", Label: i18n.ProxmoxFieldRestart, Help: i18n.ProxmoxFieldRestartHelp, Kind: providers.Switch, Default: "true"},
			{Key: "fingerprint", Label: i18n.ProxmoxFieldFingerprint, Help: i18n.ProxmoxFieldFingerprintHelp, Kind: providers.Text, Advanced: true},
			{Key: "allow_private", Label: i18n.ProxmoxFieldAllowPrivate, Help: i18n.ProxmoxFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true},
		},
	}
}

// node is a node of Proxmox VE.
type node struct {
	client  *http.Client
	opts    httpx.Options // of client
	api     string        // the URL of the node's API, such as https://pve:8006/api2/json/nodes/pve
	host    string
	name    string
	token   string // the value of the Authorization header
	restart bool
}

var (
	_ contracts.Target = (*node)(nil)
	_ contracts.Tester = (*node)(nil)
)

// factory reads the settings of in. Over plain HTTP, which carries the
// private key in the clear, JEC reaches private addresses only; over
// HTTPS, public ones unless private ones are allowed, and the certificate
// Proxmox VE makes itself when its fingerprint is pinned.
func factory(_ context.Context, in providers.Instance) (any, error) {
	raw := strings.TrimSpace(in.Config["url"])
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.ProxmoxErrorUrl, Params: map[string]any{"url": raw}})
	}
	name := strings.TrimSpace(in.Config["node"])
	if !nodePattern.MatchString(name) {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.ProxmoxErrorNode, Params: map[string]any{"node": name}})
	}
	opts := httpx.Options{Timeout: time.Minute}
	if u.Scheme == "http" {
		opts.PrivateOnly = true
	} else {
		opts.PublicOnly = in.Config["allow_private"] != "true"
		opts.Pin = strings.TrimSpace(in.Config["fingerprint"])
		// Once the node serves the certificate JEC deployed, in place of
		// the one pinned, it is trusted for the node's name.
		opts.Names = []string{u.Hostname()}
	}
	client, err := httpx.Client(opts)
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.ProxmoxErrorFingerprint, Params: map[string]any{"fingerprint": opts.Pin}})
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &node{
		client: client, opts: opts, api: u.JoinPath("api2", "json", "nodes", name).String(), host: u.Host, name: name,
		token:   "PVEAPIToken=" + strings.TrimSpace(in.Value("token_id")) + "=" + strings.TrimSpace(in.Value("token_secret")),
		restart: in.Config["restart"] != "false",
	}, nil
}

// Close lets go of idle connections.
func (n *node) Close() error {
	n.client.CloseIdleConnections()
	return nil
}

// Test reads the certificates of the node, without changing them.
func (n *node) Test(ctx context.Context) (contracts.Message, error) {
	if err := n.call(ctx, n.client, http.MethodGet, "/certificates/info", nil); err != nil {
		return contracts.Message{}, n.explain(err)
	}
	return contracts.Message{Key: i18n.ProxmoxTestOk, Params: map[string]any{"node": n.name}}, nil
}

// Deploy makes the version the custom certificate of the node, in place
// of the one before, and restarts its web server so that it serves it, if
// so set. There is one custom certificate, overwritten, so nothing to
// remove afterwards.
func (n *node) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("proxmox: no certificate to deploy"))
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	form := url.Values{
		"certificates": {string(cryptox.EncodeCertificates(req.Chain...))},
		"key":          {string(key)},
		"force":        {"1"},
		"restart":      {"0"},
	}
	if n.restart {
		form.Set("restart", "1")
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.ProxmoxLogUpload, Params: map[string]any{"node": n.name}})
	client := n.client
	if n.opts.Pin != "" {
		// The node may serve an earlier version, for the same names, even
		// when reached by its address.
		opts := n.opts
		opts.Names = append(slices.Clone(opts.Names), cryptox.Names(req.Chain[0])...)
		if client, err = httpx.Client(opts); err != nil {
			return none, retry.Permanent(err)
		}
		defer client.CloseIdleConnections()
		client.CheckRedirect = n.client.CheckRedirect
	}
	if err := n.call(ctx, client, http.MethodPost, "/certificates/custom", form); err != nil {
		return none, n.explain(err)
	}
	if n.restart {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.ProxmoxLogRestart, Params: map[string]any{"node": n.name}})
	}
	return none, nil
}

// statusError is an answer of the API that says it failed: Proxmox VE
// puts the reason in the status line.
type statusError struct {
	code   int
	reason string
}

func (e *statusError) Error() string { return fmt.Sprintf("proxmox: %d %s", e.code, e.reason) }

// call sends a request to path of the node's API through client, with
// form as its body.
func (n *node) call(ctx context.Context, client *http.Client, method, path string, form url.Values) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, n.api+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", n.token)
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 == 2 {
		return nil
	}
	reason := strings.TrimSpace(strings.TrimPrefix(resp.Status, fmt.Sprint(resp.StatusCode)))
	// Parameters that do not verify come back as errors by name.
	var answer struct {
		Errors map[string]string `json:"errors"`
	}
	if json.Unmarshal(data, &answer) == nil {
		for name, problem := range answer.Errors {
			reason += "; " + name + ": " + strings.TrimSpace(problem)
		}
	}
	return &statusError{code: resp.StatusCode, reason: reason}
}

// explain turns an error of a call into a Problem: one that trying again
// cannot fix, such as a refused token, ends the attempts.
func (n *node) explain(err error) error {
	var se *statusError
	switch fp, unverified := httpx.Fingerprint(err); {
	case errors.As(err, &se) && se.code == http.StatusUnauthorized:
		return retry.Permanent(&contracts.Problem{Key: i18n.ProxmoxErrorToken, Err: err})
	case errors.As(err, &se) && se.code == http.StatusForbidden:
		return retry.Permanent(&contracts.Problem{Key: i18n.ProxmoxErrorPermission, Params: map[string]any{"node": n.name}, Err: err})
	case errors.As(err, &se):
		problem := &contracts.Problem{Key: i18n.ProxmoxErrorApi, Params: map[string]any{"status": se.code, "reason": se.reason}, Err: err}
		if se.code >= 500 && se.code != http.StatusNotImplemented {
			return problem
		}
		return retry.Permanent(problem)
	case errors.Is(err, httpx.ErrNotPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.ProxmoxErrorPrivate, Params: map[string]any{"host": n.host}})
	case errors.Is(err, httpx.ErrPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.ProxmoxErrorPlain, Params: map[string]any{"host": n.host}})
	case errors.Is(err, httpx.ErrPin):
		return retry.Permanent(&contracts.Problem{Key: i18n.ProxmoxErrorPin, Err: httpx.Cause(err)})
	case unverified:
		return retry.Permanent(&contracts.Problem{Key: i18n.ProxmoxErrorUnverified, Params: map[string]any{"fingerprint": fp}})
	}
	return &contracts.Problem{Key: i18n.ProxmoxErrorNetwork, Params: map[string]any{"host": n.host}, Err: httpx.Cause(err)}
}
