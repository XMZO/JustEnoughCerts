package synology

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

	"github.com/pquerna/otp/totp"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// maxAuthVersion bounds the version of SYNO.API.Auth JEC asks for, as DSM
// 7 offers it; DSM 6 offers 6.
const maxAuthVersion = 7

// maxListed bounds the certificates a message names.
const maxListed = 20

func dsmType() providers.Type {
	return providers.Type{
		ID: "synology.dsm", Kind: providers.Target, Name: i18n.SynologyDsmName, Description: i18n.SynologyDsmDescription,
		Tags: []string{"self_hosted"}, Testable: true,
		Fields: []providers.Field{
			{Key: "url", Label: i18n.SynologyFieldUrl, Help: i18n.SynologyFieldUrlHelp, Kind: providers.URL, Required: true},
			{Key: "username", Label: i18n.SynologyFieldUsername, Help: i18n.SynologyFieldUsernameHelp, Kind: providers.Text, Required: true},
			{Key: "password", Label: i18n.SynologyFieldPassword, Kind: providers.Text, Secret: true, Required: true},
			{Key: "otp_secret", Label: i18n.SynologyFieldOtpSecret, Help: i18n.SynologyFieldOtpSecretHelp, Kind: providers.Text, Secret: true},
			{Key: "description", Label: i18n.SynologyFieldDescription, Help: i18n.SynologyFieldDescriptionHelp, Kind: providers.Text},
			{Key: "default", Label: i18n.SynologyFieldDefault, Help: i18n.SynologyFieldDefaultHelp, Kind: providers.Switch, Default: "false"},
			{Key: "fingerprint", Label: i18n.SynologyFieldFingerprint, Help: i18n.SynologyFieldFingerprintHelp, Kind: providers.Text, Advanced: true},
			{Key: "allow_private", Label: i18n.SynologyFieldAllowPrivate, Help: i18n.SynologyFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true},
		},
	}
}

// nas is a Synology NAS with a certificate to keep up to date.
type nas struct {
	client      *http.Client
	base        *url.URL
	host        string
	username    string
	password    string
	otpSecret   string // Base32, for an account with two-factor authentication
	description string // of the certificate to replace, or empty for JEC's own
	asDefault   bool
	now         func() time.Time
}

var (
	_ contracts.Target = (*nas)(nil)
	_ contracts.Tester = (*nas)(nil)
)

// factory reads the settings of in. Over plain HTTP, which carries the
// private key in the clear, JEC reaches private addresses only; over
// HTTPS, public ones unless private ones are allowed, and a certificate of
// DSM's own making when its fingerprint is pinned.
func factory(_ context.Context, in providers.Instance) (any, error) {
	raw := strings.TrimSpace(in.Config["url"])
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.SynologyErrorUrl, Params: map[string]any{"url": raw}})
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
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.SynologyErrorFingerprint, Params: map[string]any{"fingerprint": opts.Pin}})
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	n := &nas{client: client, base: u, host: u.Host, username: strings.TrimSpace(in.Config["username"]), password: in.Value("password"),
		description: strings.TrimSpace(in.Config["description"]), asDefault: in.Config["default"] == "true", now: time.Now}
	n.otpSecret = strings.ToUpper(strings.Join(strings.Fields(in.Value("otp_secret")), ""))
	if n.otpSecret != "" {
		if _, err := totp.GenerateCode(n.otpSecret, time.Now()); err != nil {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.SynologyErrorOtpSecret})
		}
	}
	return n, nil
}

// Close lets go of idle connections.
func (n *nas) Close() error {
	n.client.CloseIdleConnections()
	return nil
}

// certificate is a certificate of the NAS, as DSM lists them.
type certificate struct {
	ID        string `json:"id"`
	Desc      string `json:"desc"`
	IsDefault bool   `json:"is_default"`
	Subject   struct {
		CommonName string `json:"common_name"`
	} `json:"subject"`
}

// name is what people know the certificate by.
func (c certificate) name() string { return cmp.Or(c.Desc, c.Subject.CommonName, c.ID) }

// listed names the first certificates of list, for a message.
func listed(list []certificate) string {
	var names []string
	for _, c := range list[:min(len(list), maxListed)] {
		names = append(names, c.name())
	}
	return strings.Join(names, ", ")
}

// pick finds the certificate to replace: the one with the description
// set, else the one JEC deployed to before, by the newest of refs.
func pick(list []certificate, description string, refs []string) (certificate, bool) {
	if description != "" {
		i := slices.IndexFunc(list, func(c certificate) bool { return c.Desc == description })
		if i < 0 {
			return certificate{}, false
		}
		return list[i], true
	}
	for _, ref := range refs {
		if i := slices.IndexFunc(list, func(c certificate) bool { return c.ID == ref }); i >= 0 {
			return list[i], true
		}
	}
	return certificate{}, false
}

// session is a use of the Web API, signed in.
type session struct {
	*nas
	authPath string // of SYNO.API.Auth, such as entry.cgi
	version  int    // of SYNO.API.Auth
	sid      string
	token    string // the SynoToken, which DSM checks against forged requests
}

// form encodes v as a form, for a POST.
func form(v url.Values) (string, []byte) {
	return "application/x-www-form-urlencoded", []byte(v.Encode())
}

// signIn finds the version of the sign-in API, then signs in with the
// username and password, the code of the two-factor secret added, for a
// session ID and a SynoToken. The password goes in the body of a POST, out
// of the URLs that servers log.
func (n *nas) signIn(ctx context.Context) (*session, error) {
	s := &session{nas: n}
	var info map[string]struct {
		Path       string `json:"path"`
		MaxVersion int    `json:"maxVersion"`
	}
	err := s.call(ctx, "query.cgi", url.Values{"api": {"SYNO.API.Info"}, "version": {"1"}, "method": {"query"}, "query": {"SYNO.API.Auth"}}, "", nil, &info)
	if err != nil {
		return nil, n.explain(err)
	}
	auth := info["SYNO.API.Auth"]
	if auth.Path == "" || strings.ContainsAny(auth.Path, "/?#") || auth.MaxVersion < 1 {
		return nil, n.explain(&statusError{status: http.StatusOK})
	}
	s.authPath, s.version = auth.Path, min(auth.MaxVersion, maxAuthVersion)
	params := url.Values{"api": {"SYNO.API.Auth"}, "version": {strconv.Itoa(s.version)}, "method": {"login"}, "format": {"sid"},
		"account": {n.username}, "passwd": {n.password}, "enable_syno_token": {"yes"}}
	if n.otpSecret != "" {
		code, err := totp.GenerateCode(n.otpSecret, n.now())
		if err != nil {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.SynologyErrorOtpSecret})
		}
		params.Set("otp_code", code)
	}
	var answer struct {
		Sid   string `json:"sid"`
		Token string `json:"synotoken"`
	}
	contentType, body := form(params)
	err = s.call(ctx, s.authPath, url.Values{"enable_syno_token": {"yes"}}, contentType, body, &answer)
	var ae *apiError
	switch {
	case errors.As(err, &ae):
		return nil, signInError(ae)
	case err != nil:
		return nil, n.explain(err)
	case answer.Sid == "":
		return nil, n.explain(&statusError{status: http.StatusOK})
	}
	s.sid, s.token = answer.Sid, answer.Token
	return s, nil
}

// signInError explains the codes of SYNO.API.Auth's login. Only a refused
// two-factor code may be a matter of timing, such as a code used twice.
func signInError(e *apiError) error {
	var key i18n.Key
	switch e.code {
	case 400:
		key = i18n.SynologyErrorCredentials
	case 401:
		key = i18n.SynologyErrorDisabled
	case 403:
		key = i18n.SynologyErrorOtpNeeded
	case 404:
		return &contracts.Problem{Key: i18n.SynologyErrorOtp, Err: e}
	case 406:
		key = i18n.SynologyErrorOtpEnforced
	case 407:
		key = i18n.SynologyErrorBlocked
	case 408, 409, 410:
		key = i18n.SynologyErrorExpired
	default:
		return retry.Permanent(&contracts.Problem{Key: i18n.SynologyErrorSignIn, Params: map[string]any{"code": e.code}, Err: e})
	}
	return retry.Permanent(&contracts.Problem{Key: key, Err: e})
}

// signOut ends the session, so that sessions do not pile up in DSM, even
// when the context of the work is done.
func (s *session) signOut() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = s.call(ctx, s.authPath, url.Values{"api": {"SYNO.API.Auth"}, "version": {strconv.Itoa(s.version)}, "method": {"logout"}, "_sid": {s.sid}}, "", nil, nil)
}

// certificates lists the certificates of the NAS.
func (s *session) certificates(ctx context.Context) ([]certificate, error) {
	var data struct {
		Certificates []certificate `json:"certificates"`
	}
	contentType, body := form(url.Values{"api": {"SYNO.Core.Certificate.CRT"}, "method": {"list"}, "version": {"1"}, "_sid": {s.sid}})
	err := s.call(ctx, "entry.cgi", nil, contentType, body, &data)
	return data.Certificates, err
}

// Test signs in, and finds the certificate with the description set or
// lists the certificates of the NAS, without changing anything.
func (n *nas) Test(ctx context.Context) (contracts.Message, error) {
	s, err := n.signIn(ctx)
	if err != nil {
		return contracts.Message{}, err
	}
	defer s.signOut()
	list, err := s.certificates(ctx)
	switch {
	case err != nil:
		return contracts.Message{}, n.explain(err)
	case n.description == "":
		return contracts.Message{Key: i18n.SynologyTestList, Params: map[string]any{"certificates": listed(list)}}, nil
	}
	if c, found := pick(list, n.description, nil); found {
		return contracts.Message{Key: i18n.SynologyTestFound, Params: map[string]any{"certificate": c.name()}}, nil
	}
	return contracts.Message{Key: i18n.SynologyTestNew, Params: map[string]any{"description": n.description}}, nil
}

// Deploy imports the new version over the certificate with the description
// set, or over the one JEC deployed to before, or as a new certificate.
// DSM keeps the certificate's ID, so the services bound to it serve the
// new version, and restarts its web server when it serves it. Nothing is
// left to remove; the ref names the certificate, for the next deployment.
func (n *nas) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("synology: no certificate to deploy"))
	}
	s, err := n.signIn(ctx)
	if err != nil {
		return none, err
	}
	defer s.signOut()
	list, err := s.certificates(ctx)
	if err != nil {
		return none, n.explain(err)
	}
	c, found := pick(list, n.description, req.Previous)
	desc := n.description
	if found {
		desc = cmp.Or(desc, c.Desc)
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.SynologyLogReplace, Params: map[string]any{"certificate": c.name()}})
	} else {
		domain := cmp.Or(slices.Concat(req.Domains, cryptox.Names(req.Chain[0]))...)
		desc = cmp.Or(desc, fmt.Sprintf("%s (JEC #%d)", domain, req.CertificateID))
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.SynologyLogAdd, Params: map[string]any{"name": desc}})
	}
	id, restarted, err := s.upload(ctx, c.ID, desc, n.asDefault || c.IsDefault, req)
	if err != nil {
		return none, n.explain(err)
	}
	if restarted {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.SynologyLogRestart})
	}
	return contracts.DeployResult{Ref: id}, nil
}

// upload imports the certificate of req over the certificate id, or as a
// new one when id is empty, with the description desc. The leaf goes
// apart from the intermediate certificates, and the key in the PKCS #1 or
// SEC 1 form that tools for DSM have always sent. It returns the ID of the
// certificate, and whether DSM restarts its web server.
func (s *session) upload(ctx context.Context, id, desc string, asDefault bool, req contracts.DeployRequest) (string, bool, error) {
	key, err := cryptox.EncodeKeyTraditional(req.Key)
	if err != nil {
		return "", false, retry.Permanent(err)
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	files := [][2]string{{"key", string(key)}, {"cert", string(cryptox.EncodeCertificates(req.Chain[0]))}}
	if len(req.Chain) > 1 {
		files = append(files, [2]string{"inter_cert", string(cryptox.EncodeCertificates(req.Chain[1:]...))})
	}
	for _, f := range files {
		part, err := w.CreateFormFile(f[0], f[0]+".pem")
		if err != nil {
			return "", false, err
		}
		if _, err := io.WriteString(part, f[1]); err != nil {
			return "", false, err
		}
	}
	fields := [][2]string{{"id", id}, {"desc", desc}}
	if asDefault {
		fields = append(fields, [2]string{"as_default", "true"})
	}
	for _, f := range fields {
		if err := w.WriteField(f[0], f[1]); err != nil {
			return "", false, err
		}
	}
	if err := w.Close(); err != nil {
		return "", false, err
	}
	var data struct {
		ID           string `json:"id"`
		RestartHTTPD bool   `json:"restart_httpd"`
	}
	query := url.Values{"api": {"SYNO.Core.Certificate"}, "method": {"import"}, "version": {"1"}, "SynoToken": {s.token}, "_sid": {s.sid}}
	err = s.call(ctx, "entry.cgi", query, w.FormDataContentType(), body.Bytes(), &data)
	return cmp.Or(data.ID, id), data.RestartHTTPD, err
}

// apiError is an answer of DSM that says it failed, with its code.
type apiError struct{ code int }

func (e *apiError) Error() string { return fmt.Sprintf("synology: error %d", e.code) }

// statusError is an answer that is no answer of the Web API.
type statusError struct{ status int }

func (e *statusError) Error() string { return fmt.Sprintf("synology: HTTP %d", e.status) }

// call sends a request to the Web API at path, under /webapi, with query:
// a GET, or a POST of body of type contentType. It decodes the data of
// the answer into out. DSM answers {"success": false, "error": {"code"}}
// when it fails.
func (s *session) call(ctx context.Context, path string, query url.Values, contentType string, body []byte, out any) error {
	u := s.base.JoinPath("webapi", path)
	u.RawQuery = query.Encode()
	method, r := http.MethodGet, io.Reader(nil)
	if body != nil {
		method, r = http.MethodPost, bytes.NewReader(body)
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
		req.Header.Set("X-SYNO-TOKEN", s.token)
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
	var answer struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &answer) != nil {
		return &statusError{status: resp.StatusCode}
	}
	switch {
	case !answer.Success && answer.Error != nil:
		return &apiError{code: answer.Error.Code}
	case !answer.Success:
		return &statusError{status: resp.StatusCode}
	case out != nil && len(answer.Data) > 0 && json.Unmarshal(answer.Data, out) != nil:
		return &statusError{status: resp.StatusCode}
	}
	return nil
}

// explain turns an error of a call into a Problem: one that trying again
// cannot fix, such as an account that is no administrator, ends the
// attempts.
func (n *nas) explain(err error) error {
	var ae *apiError
	var se *statusError
	switch fp, unverified := httpx.Fingerprint(err); {
	case errors.As(err, &ae) && ae.code == 105:
		return retry.Permanent(&contracts.Problem{Key: i18n.SynologyErrorAdmin, Err: err})
	case errors.As(err, &ae) && (ae.code == 106 || ae.code == 107 || ae.code == 119):
		return &contracts.Problem{Key: i18n.SynologyErrorSession, Params: map[string]any{"code": ae.code}, Err: err}
	case errors.As(err, &ae):
		return retry.Permanent(&contracts.Problem{Key: i18n.SynologyErrorApi, Params: map[string]any{"code": ae.code}, Err: err})
	case errors.As(err, &se):
		problem := &contracts.Problem{Key: i18n.SynologyErrorStatus, Params: map[string]any{"status": se.status}, Err: err}
		if se.status >= 500 {
			return problem
		}
		return retry.Permanent(problem)
	case errors.Is(err, httpx.ErrNotPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.SynologyErrorPrivate, Params: map[string]any{"host": n.host}})
	case errors.Is(err, httpx.ErrPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.SynologyErrorPlain, Params: map[string]any{"host": n.host}})
	case errors.Is(err, httpx.ErrPin):
		return retry.Permanent(&contracts.Problem{Key: i18n.SynologyErrorPin, Err: httpx.Cause(err)})
	case unverified:
		return retry.Permanent(&contracts.Problem{Key: i18n.SynologyErrorUnverified, Params: map[string]any{"fingerprint": fp}})
	}
	return &contracts.Problem{Key: i18n.SynologyErrorNetwork, Params: map[string]any{"host": n.host}, Err: httpx.Cause(err)}
}
