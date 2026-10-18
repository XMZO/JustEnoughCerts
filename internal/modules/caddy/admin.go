package caddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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

// The address of the admin API in a Docker network next to JEC, and how
// long a request may take.
const (
	defaultURL     = "http://caddy:2019"
	requestTimeout = 30 * time.Second
)

// errURL is the error of an address that is no address of the admin API.
var errURL = errors.New("caddy: not an address of the admin API")

func adminType() providers.Type {
	return providers.Type{
		ID: "caddy.admin", Kind: providers.Target, Name: i18n.CaddyAdminName, Description: i18n.CaddyAdminDescription,
		Tags: []string{"self_hosted"}, Testable: true,
		Fields: []providers.Field{
			{Key: "url", Label: i18n.CaddyFieldUrl, Help: i18n.CaddyFieldUrlHelp, Kind: providers.Text, Required: true, Default: defaultURL},
		},
	}
}

// admin is the admin API of a Caddy.
type admin struct {
	client *http.Client
	base   string // such as http://caddy:2019, or http://caddy over a socket
	url    string // as set, for messages
}

var (
	_ contracts.Target = (*admin)(nil)
	_ contracts.Tester = (*admin)(nil)
)

func factory(_ context.Context, in providers.Instance) (any, error) {
	raw := strings.TrimSpace(in.Config["url"])
	a, err := open(raw)
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.CaddyErrorUrl, Params: map[string]any{"url": raw}, Err: err})
	}
	return a, nil
}

// open reaches the admin API at raw: http:// and a host and port, of this
// machine or its network only, as the private key goes over plain HTTP; or
// unix:// and the path of a socket.
func open(raw string) (*admin, error) {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, errURL
	}
	switch u.Scheme {
	case "unix":
		if u.Host != "" || u.Path == "" {
			return nil, errURL
		}
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		t := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", u.Path)
			},
			MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second,
		}
		return &admin{client: &http.Client{Transport: t, Timeout: requestTimeout, CheckRedirect: noRedirect}, base: "http://caddy", url: raw}, nil
	case "http":
		if u.Host == "" || (u.Path != "" && u.Path != "/") {
			return nil, errURL
		}
		client, err := httpx.Client(httpx.Options{Timeout: requestTimeout, PrivateOnly: true})
		if err != nil {
			return nil, err
		}
		client.CheckRedirect = noRedirect
		return &admin{client: client, base: "http://" + u.Host, url: raw}, nil
	}
	return nil, errURL
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// Close has nothing to release.
func (*admin) Close() error { return nil }

// apiError is an answer of the admin API that is no success, with the
// error Caddy gives.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return fmt.Sprintf("caddy: HTTP %d: %s", e.status, e.message) }

// do sends a request, with body as JSON unless nil, and decodes the answer
// of a success into out, unless nil. It sends no Origin, which Caddy
// checks when there is one.
func (a *admin) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		msg := http.StatusText(resp.StatusCode)
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return &apiError{status: resp.StatusCode, message: msg}
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

// explain turns a failed request into a Problem: a refused change, which
// trying again cannot fix, ends the attempts.
func (a *admin) explain(err error) error {
	var ae *apiError
	switch {
	case errors.Is(err, httpx.ErrPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.CaddyErrorPlain, Params: map[string]any{"url": a.url}, Err: err})
	case errors.As(err, &ae) && ae.status >= 502:
		return &contracts.Problem{Key: i18n.CaddyErrorApi, Params: map[string]any{"status": ae.status, "error": ae.message}, Err: err}
	case errors.As(err, &ae):
		return retry.Permanent(&contracts.Problem{Key: i18n.CaddyErrorApi, Params: map[string]any{"status": ae.status, "error": ae.message}, Err: err})
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	}
	return &contracts.Problem{Key: i18n.CaddyErrorConnect, Params: map[string]any{"url": a.url}, Err: httpx.Cause(err)}
}

// Test reads the admin config of Caddy, which tells that it answers.
func (a *admin) Test(ctx context.Context) (contracts.Message, error) {
	var v json.RawMessage
	if err := a.do(ctx, http.MethodGet, "/config/admin", nil, &v); err != nil {
		return contracts.Message{}, a.explain(err)
	}
	return contracts.Message{Key: i18n.CaddyTestOk, Params: map[string]any{"url": a.url}}, nil
}

// Deploy replaces the entry of the certificate with the new version, or
// adds it the first time.
func (a *admin) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("caddy: no certificate to deploy"))
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	id := "jec-" + strconv.FormatInt(req.CertificateID, 10)
	entry := map[string]any{"@id": id, "certificate": string(cryptox.EncodeCertificates(req.Chain...)), "key": string(key), "tags": []string{"jec"}}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.CaddyLogReplace, Params: map[string]any{"id": id}})
	err = a.do(ctx, http.MethodPatch, "/id/"+id, entry, nil)
	var ae *apiError
	if errors.As(err, &ae) && ae.status == http.StatusNotFound {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.CaddyLogAdd, Params: map[string]any{"id": id}})
		err = a.add(ctx, entry)
	}
	if err != nil {
		return none, a.explain(err)
	}
	log(contracts.LogLine{Level: contracts.OK, Key: i18n.CaddyLogLoaded})
	return contracts.DeployResult{Ref: id}, nil
}

// loadPEM is where Caddy keeps the certificates it loads from PEM, as the
// keys down to it.
var loadPEM = []string{"apps", "tls", "certificates", "load_pem"}

// errNoConfig is the error of a Caddy with no config at all, which JEC
// does not write from nothing.
var errNoConfig = &apiError{status: http.StatusConflict, message: "Caddy has no config to add a certificate to"}

// add appends entry to load_pem, creating the keys down to it that are
// missing: GET answers null for a key missing from an object, and an
// error for one under a missing key.
func (a *admin) add(ctx context.Context, entry any) error {
	for i := len(loadPEM) - 1; i >= 0; i-- {
		path := "/config/" + strings.Join(loadPEM[:i+1], "/")
		var v json.RawMessage
		err := a.do(ctx, http.MethodGet, path, nil, &v)
		var ae *apiError
		switch {
		case errors.As(err, &ae) && ae.status < 500 && i > 0:
			continue // its parent is missing too
		case errors.As(err, &ae) && ae.status < 500:
			return errNoConfig
		case err != nil:
			return err
		case len(v) == 0 || string(v) == "null":
			return a.do(ctx, http.MethodPost, path, below(i, entry), nil)
		case i == len(loadPEM)-1:
			return a.do(ctx, http.MethodPost, path, entry, nil) // appends
		}
		return a.do(ctx, http.MethodPost, path+"/"+loadPEM[i+1], below(i+1, entry), nil)
	}
	return errNoConfig
}

// below is the value of key i of loadPEM that holds entry alone.
func below(i int, entry any) any {
	v := any([]any{entry})
	for j := len(loadPEM) - 1; j > i; j-- {
		v = map[string]any{loadPEM[j]: v}
	}
	return v
}
