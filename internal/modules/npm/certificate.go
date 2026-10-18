package npm

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
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

// custom is the provider of the certificates people upload to the panel,
// the only ones it lets JEC replace; the others are its Let's Encrypt
// certificates, which it renews itself.
const custom = "other"

// maxListed bounds the certificates a message names.
const maxListed = 20

// hostKind is a kind of the panel's hosts, all of which can serve a
// certificate.
type hostKind struct {
	path   string // of its API, under /api/nginx
	expand string // of the hosts using a certificate, when getting it
	name   string // for messages
}

var kinds = []hostKind{
	{path: "proxy-hosts", expand: "proxy_hosts", name: "proxy"},
	{path: "redirection-hosts", expand: "redirection_hosts", name: "redirection"},
	{path: "dead-hosts", expand: "dead_hosts", name: "dead"},
	{path: "streams", expand: "streams", name: "stream"},
}

func certificateType() providers.Type {
	return providers.Type{
		ID: "npm.certificate", Kind: providers.Target, Name: i18n.NpmCertificateName, Description: i18n.NpmCertificateDescription,
		Tags: []string{"self_hosted"}, Testable: true,
		Fields: []providers.Field{
			{Key: "url", Label: i18n.NpmFieldUrl, Help: i18n.NpmFieldUrlHelp, Kind: providers.URL, Required: true},
			{Key: "email", Label: i18n.NpmFieldEmail, Help: i18n.NpmFieldEmailHelp, Kind: providers.Email, Required: true},
			{Key: "password", Label: i18n.NpmFieldPassword, Kind: providers.Text, Secret: true, Required: true},
			{Key: "certificate", Label: i18n.NpmFieldCertificate, Help: i18n.NpmFieldCertificateHelp, Kind: providers.Number},
			{Key: "fingerprint", Label: i18n.NpmFieldFingerprint, Help: i18n.NpmFieldFingerprintHelp, Kind: providers.Text, Advanced: true},
			{Key: "allow_private", Label: i18n.NpmFieldAllowPrivate, Help: i18n.NpmFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true},
		},
	}
}

// panel is a Nginx Proxy Manager with a certificate to keep up to date.
type panel struct {
	client   *http.Client
	base     *url.URL
	host     string
	email    string
	password string
	id       int64 // the certificate set, or 0 for one of JEC's own
}

var (
	_ contracts.Target = (*panel)(nil)
	_ contracts.Tester = (*panel)(nil)
)

// factory reads the settings of in. Over plain HTTP, which carries the
// private key in the clear, JEC reaches private addresses only; over
// HTTPS, public ones unless private ones are allowed, and a certificate of
// the panel's own making when its fingerprint is pinned.
func factory(_ context.Context, in providers.Instance) (any, error) {
	raw := strings.TrimSpace(in.Config["url"])
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.NpmErrorUrl, Params: map[string]any{"url": raw}})
	}
	opts := httpx.Options{Timeout: time.Minute}
	if u.Scheme == "http" {
		opts.PrivateOnly = true
	} else {
		opts.PublicOnly = in.Config["allow_private"] != "true"
		opts.Pin = strings.TrimSpace(in.Config["fingerprint"])
	}
	client, err := httpx.Client(opts)
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.NpmErrorFingerprint, Params: map[string]any{"fingerprint": opts.Pin}})
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	p := &panel{client: client, base: u, host: u.Host, email: strings.TrimSpace(in.Config["email"]), password: in.Value("password")}
	if s := strings.TrimSpace(in.Config["certificate"]); s != "" {
		if p.id, err = strconv.ParseInt(s, 10, 64); err != nil || p.id <= 0 {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.NpmErrorCertificateId, Params: map[string]any{"id": s}})
		}
	}
	return p, nil
}

// Close lets go of idle connections.
func (p *panel) Close() error {
	p.client.CloseIdleConnections()
	return nil
}

// certificate is a certificate of the panel, with the hosts using it when
// got one by one.
type certificate struct {
	ID               int64    `json:"id"`
	Provider         string   `json:"provider"`
	NiceName         string   `json:"nice_name"`
	DomainNames      []string `json:"domain_names"`
	ProxyHosts       []host   `json:"proxy_hosts"`
	RedirectionHosts []host   `json:"redirection_hosts"`
	DeadHosts        []host   `json:"dead_hosts"`
	Streams          []host   `json:"streams"`
}

// name is what people know the certificate by.
func (c certificate) name() string {
	return "#" + strconv.FormatInt(c.ID, 10) + " " + cmp.Or(c.NiceName, strings.Join(c.DomainNames, ", "))
}

// hosts returns the hosts of kind k using c.
func (c certificate) hosts(k hostKind) []host {
	switch k.expand {
	case "proxy_hosts":
		return c.ProxyHosts
	case "redirection_hosts":
		return c.RedirectionHosts
	case "dead_hosts":
		return c.DeadHosts
	}
	return c.Streams
}

// host is a host of the panel, or a stream, which forwards a port.
type host struct {
	ID           int64    `json:"id"`
	DomainNames  []string `json:"domain_names"`
	IncomingPort int      `json:"incoming_port"`
	Enabled      bool     `json:"enabled"`
	Meta         hostMeta `json:"meta"`
}

// hostMeta is what the panel notes about a host's configuration.
type hostMeta struct {
	// NginxOnline is false when nginx refused the configuration and the
	// panel took the host out.
	NginxOnline *bool  `json:"nginx_online"`
	NginxErr    string `json:"nginx_err"`
}

func (m hostMeta) online() bool { return m.NginxOnline == nil || *m.NginxOnline }

// label is what people know the host by: its domains, or a stream's port.
func (h host) label() string {
	if len(h.DomainNames) > 0 {
		return strings.Join(h.DomainNames, ", ")
	}
	return ":" + strconv.Itoa(h.IncomingPort)
}

// listed names the first certificates of list, for a message.
func listed(list []certificate) string {
	var names []string
	for _, c := range list[:min(len(list), maxListed)] {
		names = append(names, c.name())
	}
	return strings.Join(names, ", ")
}

// session is a use of the panel's API, signed in.
type session struct {
	*panel
	token string
}

// signIn trades the email and password for a token, which the panel makes
// valid for a day. A user with two-factor authentication gets a challenge
// instead, which JEC cannot answer.
func (p *panel) signIn(ctx context.Context) (*session, error) {
	var answer struct {
		Token       string `json:"token"`
		Requires2FA bool   `json:"requires_2fa"`
	}
	s := &session{panel: p}
	err := s.call(ctx, http.MethodPost, nil, map[string]string{"identity": p.email, "secret": p.password}, &answer, "tokens")
	var ae *apiError
	switch {
	case errors.As(err, &ae) && ae.status/100 == 4:
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.NpmErrorSignIn, Params: map[string]any{"message": ae.message}, Err: err})
	case err != nil:
		return nil, p.explain(err)
	case answer.Requires2FA:
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.NpmErrorTwoFactor})
	case answer.Token == "":
		return nil, p.explain(&statusError{status: http.StatusOK})
	}
	s.token = answer.Token
	return s, nil
}

// certificates lists the panel's certificates.
func (s *session) certificates(ctx context.Context) ([]certificate, error) {
	var list []certificate
	return list, s.call(ctx, http.MethodGet, nil, nil, &list, "nginx", "certificates")
}

// customs lists the panel's custom certificates.
func (s *session) customs(ctx context.Context) ([]certificate, error) {
	list, err := s.certificates(ctx)
	var out []certificate
	for _, c := range list {
		if c.Provider == custom {
			out = append(out, c)
		}
	}
	return out, err
}

// certificate gets the certificate id with the hosts using it, and tells
// whether the panel has it.
func (s *session) certificate(ctx context.Context, id int64) (certificate, bool, error) {
	var expand []string
	for _, k := range kinds {
		expand = append(expand, k.expand)
	}
	var c certificate
	err := s.call(ctx, http.MethodGet, url.Values{"expand": {strings.Join(expand, ",")}}, nil, &c, "nginx", "certificates", strconv.FormatInt(id, 10))
	var ae *apiError
	if errors.As(err, &ae) && ae.status == http.StatusNotFound {
		return c, false, nil
	}
	return c, err == nil, err
}

// Test signs in, and finds the certificate set or lists the panel's custom
// certificates, without changing anything.
func (p *panel) Test(ctx context.Context) (contracts.Message, error) {
	s, err := p.signIn(ctx)
	if err != nil {
		return contracts.Message{}, err
	}
	if p.id == 0 {
		list, err := s.customs(ctx)
		switch {
		case err != nil:
			return contracts.Message{}, p.explain(err)
		case len(list) == 0:
			return contracts.Message{Key: i18n.NpmTestNew}, nil
		}
		return contracts.Message{Key: i18n.NpmTestList, Params: map[string]any{"certificates": listed(list)}}, nil
	}
	c, err := s.set(ctx)
	if err != nil {
		return contracts.Message{}, err
	}
	count := 0
	for _, k := range kinds {
		count += len(c.hosts(k))
	}
	return contracts.Message{Key: i18n.NpmTestOk, Params: map[string]any{"certificate": c.name(), "count": count}}, nil
}

// set gets the certificate set, which has to be a custom one.
func (s *session) set(ctx context.Context) (certificate, error) {
	c, found, err := s.certificate(ctx, s.id)
	switch {
	case err != nil:
		return c, s.explain(err)
	case !found:
		list, err := s.customs(ctx)
		switch {
		case err != nil:
			return c, s.explain(err)
		case len(list) == 0:
			return c, retry.Permanent(&contracts.Problem{Key: i18n.NpmErrorNoCertificates, Params: map[string]any{"id": s.id}})
		}
		return c, retry.Permanent(&contracts.Problem{Key: i18n.NpmErrorNoCertificate, Params: map[string]any{"id": s.id, "certificates": listed(list)}})
	case c.Provider != custom:
		return c, retry.Permanent(&contracts.Problem{Key: i18n.NpmErrorProvider, Params: map[string]any{"certificate": c.name()}})
	}
	return c, nil
}

// Deploy uploads the new version to the certificate set, or to the one JEC
// added at an earlier deployment, adding it the first time, and has nginx
// load it. The panel's certificate is overwritten, so there is nothing to
// remove afterwards; the ref names JEC's own, for the next deployment.
func (p *panel) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("npm: no certificate to deploy"))
	}
	s, err := p.signIn(ctx)
	if err != nil {
		return none, err
	}
	var c certificate
	if p.id != 0 {
		c, err = s.set(ctx)
	} else {
		c, err = s.own(ctx, req, log)
	}
	if err != nil {
		return none, err
	}
	if err := s.upload(ctx, c, req, log); err != nil {
		return none, err
	}
	if err := s.apply(ctx, c, log); err != nil {
		return none, err
	}
	if p.id != 0 {
		return none, nil
	}
	return contracts.DeployResult{Ref: strconv.FormatInt(c.ID, 10)}, nil
}

// own finds the certificate JEC added at an earlier deployment, or adds
// one, named after the primary domain and the certificate in JEC.
func (s *session) own(ctx context.Context, req contracts.DeployRequest, log func(contracts.LogLine)) (certificate, error) {
	if id := ownID(req.Previous); id != 0 {
		c, found, err := s.certificate(ctx, id)
		switch {
		case err != nil:
			return c, s.explain(err)
		case found && c.Provider == custom:
			return c, nil
		}
		log(contracts.LogLine{Level: contracts.Warn, Key: i18n.NpmLogGone, Params: map[string]any{"id": id}})
	}
	domain := cmp.Or(slices.Concat(req.Domains, cryptox.Names(req.Chain[0]))...)
	name := fmt.Sprintf("%s (JEC #%d)", domain, req.CertificateID)
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.NpmLogCreate, Params: map[string]any{"name": name}})
	var c certificate
	if err := s.call(ctx, http.MethodPost, nil, map[string]string{"provider": custom, "nice_name": name}, &c, "nginx", "certificates"); err != nil {
		return c, s.explain(err)
	}
	return c, nil
}

// ownID is the ID of the certificate JEC added, by the newest of refs.
func ownID(refs []string) int64 {
	for _, ref := range refs {
		if id, err := strconv.ParseInt(ref, 10, 64); err == nil && id > 0 {
			return id
		}
	}
	return 0
}

// upload replaces the files of c: the leaf, its key and the intermediate
// certificates, which the panel puts after the leaf. The panel answers
// with what it stored, the key included, which JEC leaves unread.
func (s *session) upload(ctx context.Context, c certificate, req contracts.DeployRequest, log func(contracts.LogLine)) error {
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return retry.Permanent(err)
	}
	type file struct {
		field string
		data  []byte
	}
	files := []file{{"certificate", cryptox.EncodeCertificates(req.Chain[0])}, {"certificate_key", key}}
	if len(req.Chain) > 1 {
		files = append(files, file{"intermediate_certificate", cryptox.EncodeCertificates(req.Chain[1:]...)})
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for _, f := range files {
		w, err := form.CreateFormFile(f.field, f.field+".pem")
		if err != nil {
			return err
		}
		if _, err := w.Write(f.data); err != nil {
			return err
		}
	}
	if err := form.Close(); err != nil {
		return err
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.NpmLogUpload, Params: map[string]any{"certificate": c.name()}})
	err = s.send(ctx, http.MethodPost, nil, form.FormDataContentType(), body.Bytes(), nil, "nginx", "certificates", strconv.FormatInt(c.ID, 10), "upload")
	if err != nil {
		return s.explain(err)
	}
	return nil
}

// apply has nginx load the new version. The panel only writes the files
// of an upload, but saving a host again rewrites its configuration and
// reloads nginx, which then reads every certificate anew; so JEC saves one
// of the hosts using c, as it is. Hosts taken offline, after nginx refused
// them, are left alone. Nginx reloads only when it accepts the whole
// configuration, so a broken host or certificate elsewhere in the panel
// fails the save, and the panel says no more than "Internal Error".
func (s *session) apply(ctx context.Context, c certificate, log func(contracts.LogLine)) error {
	for _, k := range kinds {
		for _, h := range c.hosts(k) {
			if !h.Enabled || !h.Meta.online() {
				continue
			}
			log(contracts.LogLine{Level: contracts.Info, Key: i18n.NpmLogApply, Params: map[string]any{"kind": k.name, "host": h.label()}})
			var saved struct {
				Meta hostMeta `json:"meta"`
			}
			err := s.call(ctx, http.MethodPut, nil, map[string]int64{"certificate_id": c.ID}, &saved, "nginx", k.path, strconv.FormatInt(h.ID, 10))
			var ae *apiError
			switch {
			case errors.As(err, &ae) && ae.status >= 500:
				return &contracts.Problem{Key: i18n.NpmErrorReload, Params: map[string]any{"message": ae.message, "status": ae.status}, Err: err}
			case err != nil:
				return s.explain(err)
			case !saved.Meta.online():
				return retry.Permanent(&contracts.Problem{Key: i18n.NpmErrorNginx,
					Params: map[string]any{"kind": k.name, "host": h.label(), "error": strings.TrimSpace(saved.Meta.NginxErr)}})
			}
			return nil
		}
	}
	log(contracts.LogLine{Level: contracts.Warn, Key: i18n.NpmLogUnused, Params: map[string]any{"certificate": c.name()}})
	return nil
}

// apiError is an answer of the panel that says it failed.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return fmt.Sprintf("npm: HTTP %d: %s", e.status, e.message) }

// statusError is an answer that is no answer of the API.
type statusError struct{ status int }

func (e *statusError) Error() string { return fmt.Sprintf("npm: HTTP %d", e.status) }

// call sends body as JSON, when not nil, to the API at path, and decodes
// the answer into out.
func (s *session) call(ctx context.Context, method string, query url.Values, body, out any, path ...string) error {
	if body == nil {
		return s.send(ctx, method, query, "", nil, out, path...)
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return s.send(ctx, method, query, "application/json", data, out, path...)
}

// send sends body of type contentType to the API at path, with the token
// once signed in, and decodes the answer into out. The panel tells why it
// failed in {"error": {"code", "message"}}.
func (s *session) send(ctx context.Context, method string, query url.Values, contentType string, body []byte, out any, path ...string) error {
	u := s.base.JoinPath(append([]string{"api"}, path...)...)
	u.RawQuery = query.Encode()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), r)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var answer struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &answer) == nil && answer.Error.Message != "" {
			return &apiError{status: resp.StatusCode, message: answer.Error.Message}
		}
		return &statusError{status: resp.StatusCode}
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return &statusError{status: resp.StatusCode}
	}
	return nil
}

// explain turns an error of a call into a Problem: one that trying again
// cannot fix, such as a refused permission, ends the attempts.
func (p *panel) explain(err error) error {
	var ae *apiError
	var se *statusError
	switch fp, unverified := httpx.Fingerprint(err); {
	case errors.As(err, &ae) && (ae.status == http.StatusUnauthorized || ae.status == http.StatusForbidden):
		return retry.Permanent(&contracts.Problem{Key: i18n.NpmErrorDenied, Params: map[string]any{"message": ae.message}, Err: err})
	case errors.As(err, &ae):
		problem := &contracts.Problem{Key: i18n.NpmErrorApi, Params: map[string]any{"status": ae.status, "message": ae.message}, Err: err}
		if ae.status >= 500 || ae.status == http.StatusRequestTimeout || ae.status == http.StatusTooManyRequests {
			return problem
		}
		return retry.Permanent(problem)
	case errors.As(err, &se):
		problem := &contracts.Problem{Key: i18n.NpmErrorStatus, Params: map[string]any{"status": se.status}, Err: err}
		if se.status >= 500 {
			return problem
		}
		return retry.Permanent(problem)
	case errors.Is(err, httpx.ErrNotPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.NpmErrorPrivate, Params: map[string]any{"host": p.host}})
	case errors.Is(err, httpx.ErrPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.NpmErrorPlain, Params: map[string]any{"host": p.host}})
	case errors.Is(err, httpx.ErrPin):
		return retry.Permanent(&contracts.Problem{Key: i18n.NpmErrorPin, Err: httpx.Cause(err)})
	case unverified:
		return retry.Permanent(&contracts.Problem{Key: i18n.NpmErrorUnverified, Params: map[string]any{"fingerprint": fp}})
	}
	return &contracts.Problem{Key: i18n.NpmErrorNetwork, Params: map[string]any{"host": p.host}, Err: httpx.Cause(err)}
}
