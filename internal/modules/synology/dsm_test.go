package synology

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

const (
	username = "jec"
	password = "s3cret password"
	sid      = "the-sid"
	token    = "the-token"
	// secret is a two-factor secret key, as DSM shows it.
	secret = "JBSWY3DPEHPK3PXP"
)

// imported is a certificate the fake NAS imported, by field.
type imported map[string]string

// fakeNAS answers as DSM 7 does, for what JEC uses.
type fakeNAS struct {
	t     *testing.T
	mu    sync.Mutex
	certs []map[string]any
	// otp has the account need a two-factor code.
	otp       bool
	imports   []imported
	logouts   int
	loginCode int // an error code for the login, if not 0
	// reply, when it gives a status, answers instead for the api.
	reply func(api string) (int, string)
}

func (f *fakeNAS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			f.t.Error(err)
		}
	} else if err := r.ParseForm(); err != nil {
		f.t.Error(err)
	}
	api := r.Form.Get("api")
	if f.reply != nil {
		if status, answer := f.reply(api); status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, answer)
			return
		}
	}
	fail := func(code int) { _, _ = fmt.Fprintf(w, `{"error": {"code": %d}, "success": false}`, code) }
	switch {
	case r.URL.Path == "/webapi/query.cgi" && api == "SYNO.API.Info":
		_, _ = io.WriteString(w, `{"data": {"SYNO.API.Auth": {"maxVersion": 7, "minVersion": 1, "path": "entry.cgi"}}, "success": true}`)
	case r.URL.Path != "/webapi/entry.cgi":
		w.WriteHeader(http.StatusNotFound)
	case api == "SYNO.API.Auth" && r.Form.Get("method") == "login":
		if r.Method != http.MethodPost || r.URL.Query().Get("passwd") != "" || r.URL.Query().Get("enable_syno_token") != "yes" || r.Form.Get("version") != "7" {
			f.t.Errorf("signed in with %s %v", r.Method, r.URL.Query())
		}
		switch {
		case f.loginCode != 0:
			fail(f.loginCode)
		case r.Form.Get("account") != username || r.Form.Get("passwd") != password:
			fail(400)
		case f.otp && r.Form.Get("otp_code") == "":
			fail(403)
		case f.otp && !totp.Validate(r.Form.Get("otp_code"), secret):
			fail(404)
		default:
			_, _ = io.WriteString(w, `{"data": {"sid": "`+sid+`", "synotoken": "`+token+`", "is_portal_port": false}, "success": true}`)
		}
	case api == "SYNO.API.Auth" && r.Form.Get("method") == "logout":
		f.logouts++
		_, _ = io.WriteString(w, `{"success": true}`)
	case r.Form.Get("_sid") != sid || r.Header.Get("X-SYNO-TOKEN") != token:
		fail(119)
	case api == "SYNO.Core.Certificate.CRT" && r.Form.Get("method") == "list":
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"certificates": f.certs}, "success": true})
	case api == "SYNO.Core.Certificate" && r.Form.Get("method") == "import":
		if r.URL.Query().Get("SynoToken") != token {
			f.t.Errorf("SynoToken %q", r.URL.Query().Get("SynoToken"))
		}
		in := imported{}
		for name, values := range r.MultipartForm.Value {
			in[name] = values[0]
		}
		for name, files := range r.MultipartForm.File {
			file, _ := files[0].Open()
			data, _ := io.ReadAll(file)
			in[name] = string(data)
		}
		f.imports = append(f.imports, in)
		id := in["id"]
		if id == "" {
			id = fmt.Sprintf("new%d", len(f.imports))
			f.certs = append(f.certs, map[string]any{"id": id, "desc": in["desc"], "is_default": in["as_default"] == "true"})
		}
		_, _ = fmt.Fprintf(w, `{"data": {"id": %q, "restart_httpd": true}, "success": true}`, id)
	default:
		fail(103)
	}
}

func serve(t *testing.T, f *fakeNAS, config map[string]string) *nas {
	t.Helper()
	f.t = t
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfg := map[string]string{"url": srv.URL + "/", "username": username}
	for k, v := range config {
		cfg[k] = v
	}
	secrets := map[string]string{"password": password}
	if f.otp {
		secrets["otp_secret"] = "jbsw y3dp ehpk 3pxp"
	}
	n, err := factory(t.Context(), providers.Instance{Config: cfg, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	return n.(*nas)
}

// request makes a certificate for example.com, logging into lines.
func request(t *testing.T, lines *[]contracts.LogLine) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "example.com")
	return contracts.DeployRequest{CertificateID: 7, Domains: []string{"example.com"}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key,
		Log: func(l contracts.LogLine) { *lines = append(*lines, l) }}
}

// checkImport checks that in holds the certificate of req, with its key in
// the traditional form, the leaf apart from the intermediate certificates.
func checkImport(t *testing.T, in imported, req contracts.DeployRequest) {
	t.Helper()
	if !strings.HasPrefix(in["key"], "-----BEGIN EC PRIVATE KEY-----") {
		t.Errorf("key %.40q", in["key"])
	}
	b, err := cryptox.ParseBundle([]byte(in["cert"]+in["inter_cert"]), []byte(in["key"]))
	if err != nil || !b.Leaf.Equal(req.Chain[0]) || len(b.Chain) != 1 {
		t.Errorf("imported %v: %v", in, err)
	}
	if leaf, err := cryptox.ParseCertificates([]byte(in["cert"])); err != nil || len(leaf) != 1 {
		t.Errorf("cert holds %d certificates: %v", len(leaf), err)
	}
}

func TestDeployOwn(t *testing.T) {
	f := &fakeNAS{certs: []map[string]any{{"id": "sys", "desc": "", "is_default": true, "subject": map[string]any{"common_name": "synology"}}}}
	n := serve(t, f, nil)
	var lines []contracts.LogLine
	req := request(t, &lines)
	res, err := n.Deploy(t.Context(), req)
	if err != nil || res.Ref != "new1" {
		t.Fatalf("first: %+v %v", res, err)
	}
	in := f.imports[0]
	checkImport(t, in, req)
	if in["id"] != "" || in["desc"] != "example.com (JEC #7)" || in["as_default"] != "" {
		t.Errorf("added %v", in)
	}
	if len(lines) != 2 || lines[0].Key != i18n.SynologyLogAdd || lines[1].Key != i18n.SynologyLogRestart {
		t.Errorf("logged %+v", lines)
	}

	// The next version replaces it, under the description people gave it.
	f.certs[1]["desc"] = "renamed"
	next := request(t, &lines)
	next.Previous = []string{"gone", "new1"}
	if res, err = n.Deploy(t.Context(), next); err != nil || res.Ref != "new1" || len(f.certs) != 2 {
		t.Fatalf("second: %+v %v, %d certificates", res, err, len(f.certs))
	}
	if in = f.imports[1]; in["id"] != "new1" || in["desc"] != "renamed" {
		t.Errorf("replaced %v", in)
	}
	checkImport(t, in, next)
	if f.logouts != 2 {
		t.Errorf("%d sign-outs", f.logouts)
	}
}

func TestDeployDescription(t *testing.T) {
	f := &fakeNAS{certs: []map[string]any{
		{"id": "sys", "desc": "", "is_default": false},
		{"id": "abc", "desc": "NAS", "is_default": true},
	}}
	var lines []contracts.LogLine
	req := request(t, &lines)
	req.Previous = []string{"sys"}
	res, err := serve(t, f, map[string]string{"description": "NAS"}).Deploy(t.Context(), req)
	if err != nil || res.Ref != "abc" {
		t.Fatalf("%+v %v", res, err)
	}
	if in := f.imports[0]; in["id"] != "abc" || in["desc"] != "NAS" || in["as_default"] != "true" {
		t.Errorf("imported %v", in)
	}
	if lines[0].Key != i18n.SynologyLogReplace || lines[0].Params["certificate"] != "NAS" {
		t.Errorf("logged %+v", lines)
	}

	// A description no certificate has adds one so described, the default
	// when set so.
	res, err = serve(t, f, map[string]string{"description": "Other", "default": "true"}).Deploy(t.Context(), req)
	if err != nil || res.Ref != "new2" {
		t.Fatalf("%+v %v", res, err)
	}
	if in := f.imports[1]; in["id"] != "" || in["desc"] != "Other" || in["as_default"] != "true" {
		t.Errorf("imported %v", in)
	}
}

func TestTwoFactor(t *testing.T) {
	f := &fakeNAS{otp: true}
	var lines []contracts.LogLine
	if _, err := serve(t, f, nil).Deploy(t.Context(), request(t, &lines)); err != nil {
		t.Fatal(err)
	}
	// Without the secret, DSM asks for a code.
	n := serve(t, f, nil)
	n.otpSecret = ""
	_, err := n.Test(t.Context())
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.SynologyErrorOtpNeeded || !retry.IsPermanent(err) {
		t.Errorf("no code: %v", err)
	}
	// A code of another time is refused, which may pass.
	n = serve(t, f, nil)
	n.now = func() time.Time { return time.Now().Add(-time.Hour) }
	_, err = n.Test(t.Context())
	if !errors.As(err, &pr) || pr.Key != i18n.SynologyErrorOtp || retry.IsPermanent(err) {
		t.Errorf("old code: %v", err)
	}
}

func TestSignIn(t *testing.T) {
	for code, key := range map[int]i18n.Key{
		400: i18n.SynologyErrorCredentials,
		401: i18n.SynologyErrorDisabled,
		406: i18n.SynologyErrorOtpEnforced,
		407: i18n.SynologyErrorBlocked,
		409: i18n.SynologyErrorExpired,
		499: i18n.SynologyErrorSignIn,
	} {
		_, err := serve(t, &fakeNAS{loginCode: code}, nil).Test(t.Context())
		var pr *contracts.Problem
		if !errors.As(err, &pr) || pr.Key != key || !retry.IsPermanent(err) {
			t.Errorf("%d: %v", code, err)
		}
	}
	n := serve(t, &fakeNAS{}, nil)
	n.password = "wrong"
	_, err := n.Test(t.Context())
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.SynologyErrorCredentials {
		t.Errorf("wrong password: %v", err)
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		api       string
		status    int
		body      string
		key       i18n.Key
		permanent bool
	}{
		{"SYNO.Core.Certificate.CRT", 200, `{"error": {"code": 105}, "success": false}`, i18n.SynologyErrorAdmin, true},
		{"SYNO.Core.Certificate", 200, `{"error": {"code": 119}, "success": false}`, i18n.SynologyErrorSession, false},
		{"SYNO.Core.Certificate", 200, `{"error": {"code": 5520}, "success": false}`, i18n.SynologyErrorApi, true},
		{"SYNO.Core.Certificate", 502, `<html>bad gateway</html>`, i18n.SynologyErrorStatus, false},
		{"SYNO.API.Info", 200, `<html>a router</html>`, i18n.SynologyErrorStatus, true},
		{"SYNO.API.Info", 200, `{"data": {}, "success": true}`, i18n.SynologyErrorStatus, true},
	} {
		f := &fakeNAS{reply: func(api string) (int, string) {
			if api == c.api {
				return c.status, c.body
			}
			return 0, ""
		}}
		var lines []contracts.LogLine
		_, err := serve(t, f, nil).Deploy(t.Context(), request(t, &lines))
		var pr *contracts.Problem
		if !errors.As(err, &pr) || pr.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%s %d %s: %v", c.api, c.status, c.body, err)
		}
	}
}

func TestTest(t *testing.T) {
	f := &fakeNAS{certs: []map[string]any{
		{"id": "sys", "desc": "", "subject": map[string]any{"common_name": "synology"}},
		{"id": "abc", "desc": "NAS"},
	}}
	msg, err := serve(t, f, nil).Test(t.Context())
	if err != nil || msg.Key != i18n.SynologyTestList || msg.Params["certificates"] != "synology, NAS" {
		t.Errorf("listed: %+v %v", msg, err)
	}
	msg, err = serve(t, f, map[string]string{"description": "NAS"}).Test(t.Context())
	if err != nil || msg.Key != i18n.SynologyTestFound || msg.Params["certificate"] != "NAS" {
		t.Errorf("found: %+v %v", msg, err)
	}
	msg, err = serve(t, f, map[string]string{"description": "Other"}).Test(t.Context())
	if err != nil || msg.Key != i18n.SynologyTestNew || msg.Params["description"] != "Other" {
		t.Errorf("new: %+v %v", msg, err)
	}
	if f.logouts != 3 {
		t.Errorf("%d sign-outs", f.logouts)
	}
}

func TestSettings(t *testing.T) {
	for _, in := range []providers.Instance{
		{Config: map[string]string{"url": "192.168.1.2:5001"}},
		{Config: map[string]string{"url": "https://203.0.113.5:5001", "fingerprint": "not one"}},
		{Config: map[string]string{"url": "https://203.0.113.5:5001"}, Secrets: map[string]string{"otp_secret": "not base32!"}},
	} {
		if _, err := factory(t.Context(), in); err == nil || !retry.IsPermanent(err) {
			t.Errorf("%v: %v", in.Config, err)
		}
	}
	n, err := factory(t.Context(), providers.Instance{Config: map[string]string{"url": "http://93.184.215.14:5000", "username": username}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = n.(*nas).Test(t.Context())
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.SynologyErrorPlain {
		t.Errorf("plain HTTP to a public address: %v", err)
	}
}
