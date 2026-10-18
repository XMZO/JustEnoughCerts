package upyun

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// fakeConsole answers as UPYUN's console does, for what JEC uses: the
// sign-in sets a cookie, which every other call needs.
type fakeConsole struct {
	t     *testing.T
	mu    sync.Mutex
	certs []string            // the chains uploaded; the ID of each is crt-<index+1>
	sites map[string][]served // the certificates of each domain
	calls []string
	reply func(path string) (int, string)
}

func (f *fakeConsole) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	write := func(status int, data any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}
	fail := func(status, code int, message string) {
		write(status, map[string]any{"error_code": code, "message": message})
	}
	if f.reply != nil {
		if status, answer := f.reply(r.URL.Path); status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, answer)
			return
		}
	}
	var in map[string]any
	if r.Method == http.MethodPost {
		if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&in) != nil {
			fail(http.StatusBadRequest, 10001, "bad body")
			return
		}
	}
	str := func(k string) string { s, _ := in[k].(string); return s }
	if r.URL.Path == "/accounts/signin/" {
		if str("username") != "jec" || str("password") != "s3cret" {
			fail(http.StatusBadRequest, 10010, "username or password error")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "s", Value: "session-1", Path: "/", HttpOnly: true})
		write(http.StatusOK, map[string]any{"result": true})
		return
	}
	if c, err := r.Cookie("s"); err != nil || c.Value != "session-1" {
		fail(http.StatusUnauthorized, 10030, "please sign in")
		return
	}
	known := func(id string) bool {
		n, err := strconv.Atoi(strings.TrimPrefix(id, "crt-"))
		return err == nil && strings.HasPrefix(id, "crt-") && n >= 1 && n <= len(f.certs)
	}
	switch path := r.URL.Path; {
	case r.Method == http.MethodPost && path == "/api/https/certificate/":
		chain := str("certificate")
		if _, err := cryptox.ParseBundle([]byte(chain), []byte(str("private_key"))); err != nil {
			fail(http.StatusBadRequest, 40001, "bad certificate")
			return
		}
		// The same certificate again is the one stored before.
		i := slices.Index(f.certs, chain)
		if i < 0 {
			f.certs, i = append(f.certs, chain), len(f.certs)
		}
		write(http.StatusOK, map[string]any{"status": 0, "result": map[string]any{"certificate_id": "crt-" + strconv.Itoa(i+1)}})
	case r.Method == http.MethodGet && path == "/api/https/services/manager":
		list, ok := f.sites[r.URL.Query().Get("domain")]
		if !ok {
			fail(http.StatusBadRequest, 20020, "domain not found")
			return
		}
		write(http.StatusOK, map[string]any{"status": 0, "result": list})
	case r.Method == http.MethodPost && path == "/api/https/certificate/manager":
		d, id := str("domain"), str("certificate_id")
		if _, ok := f.sites[d]; !ok || !known(id) || in["https"] != true {
			fail(http.StatusBadRequest, 20021, "cannot set "+id+" on "+d)
			return
		}
		force, _ := in["force_https"].(bool)
		f.sites[d] = append(f.sites[d], served{CertificateID: id, HTTPS: true, ForceHTTPS: force})
		write(http.StatusOK, map[string]any{"status": true})
	case r.Method == http.MethodPost && path == "/api/https/migrate/domain":
		d, id := str("domain_name"), str("crt_id")
		i := slices.IndexFunc(f.sites[d], func(s served) bool { return s.HTTPS })
		switch {
		case i < 0 || !known(id):
			fail(http.StatusBadRequest, 20022, "cannot move "+d+" to "+id)
		case f.sites[d][i].CertificateID == id:
			fail(http.StatusBadRequest, 20023, "domain has been bound to this certificate")
		default:
			f.sites[d][i].CertificateID = id
			write(http.StatusOK, map[string]any{"status": true})
		}
	default:
		fail(http.StatusNotFound, 404, "no such API")
	}
}

// called is the calls the console got, but the sign-ins.
func (f *fakeConsole) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(f.calls), func(c string) bool { return strings.HasSuffix(c, "/accounts/signin/") })
}

func newConsole(t *testing.T, config map[string]string) (*fakeConsole, *cdn) {
	t.Helper()
	f := &fakeConsole{t: t, sites: map[string][]served{
		"www.example.com": {{CertificateID: "crt-old", HTTPS: true, ForceHTTPS: true}},
		"cdn.example.com": {},
	}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	secrets := map[string]string{"password": "s3cret"}
	if pw, ok := config["password"]; ok {
		secrets["password"] = pw
	}
	p, err := factory(t.Context(), providers.Instance{Config: config, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	c := p.(*cdn)
	c.client, c.base = srv.Client(), srv.URL
	t.Cleanup(func() { _ = c.Close() })
	return f, c
}

// deployRequest is a version of the tests, with its log.
func deployRequest(t *testing.T, names ...string) (contracts.DeployRequest, *[]i18n.Key) {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().AddDate(0, 3, 0), names...)
	keys := &[]i18n.Key{}
	return contracts.DeployRequest{
		CertificateID: 3, Domains: names, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key, Previous: []string{"crt-old"},
		Log: func(l contracts.LogLine) { *keys = append(*keys, l.Key) },
	}, keys
}

func TestDeploy(t *testing.T) {
	f, c := newConsole(t, map[string]string{"username": " jec ", "force_https": "true"})
	req, keys := deployRequest(t, "www.example.com", "cdn.example.com", "*.example.com")
	res, err := c.Deploy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "crt-1" || len(res.Removed) != 0 {
		t.Errorf("result %+v", res)
	}
	want := []string{
		"POST /api/https/certificate/",
		"GET /api/https/services/manager", "POST /api/https/migrate/domain",
		"GET /api/https/services/manager", "POST /api/https/certificate/manager",
	}
	if got := f.called(); !slices.Equal(got, want) {
		t.Errorf("calls %v", got)
	}
	if f.certs[0] != string(cryptox.EncodeCertificates(req.Chain...)) {
		t.Error("uploaded another chain")
	}
	www, cdn := f.sites["www.example.com"], f.sites["cdn.example.com"]
	if len(www) != 1 || www[0] != (served{CertificateID: "crt-1", HTTPS: true, ForceHTTPS: true}) ||
		len(cdn) != 1 || cdn[0] != (served{CertificateID: "crt-1", HTTPS: true, ForceHTTPS: true}) {
		t.Errorf("www %+v, cdn %+v", www, cdn)
	}
	if want := []i18n.Key{i18n.UpyunLogUpload, i18n.UpyunLogSwitch, i18n.UpyunLogEnable, i18n.UpyunLogKept}; !slices.Equal(*keys, want) {
		t.Errorf("log %v", *keys)
	}

	// Again: the console has the certificate, and the domains use it.
	*keys = nil
	if res, err := c.Deploy(t.Context(), req); err != nil || res.Ref != "crt-1" {
		t.Fatalf("again: %+v %v", res, err)
	}
	if want := []i18n.Key{i18n.UpyunLogUpload, i18n.UpyunLogCurrent, i18n.UpyunLogCurrent, i18n.UpyunLogKept}; !slices.Equal(*keys, want) {
		t.Errorf("log %v", *keys)
	}
}

func TestDeployDomains(t *testing.T) {
	// The domains set, rather than the certificate's; a domain the console
	// reports bound to the certificate already is done.
	f, c := newConsole(t, map[string]string{"username": "jec", "domains": "WWW.example.com."})
	req, _ := deployRequest(t, "*.example.com")
	req.Previous = nil
	f.reply = func(path string) (int, string) {
		if path == "/api/https/migrate/domain" {
			return http.StatusBadRequest, `{"data": {"error_code": 20023, "message": "domain has been bound to this certificate"}}`
		}
		return 0, ""
	}
	if _, err := c.Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if got := f.called(); !slices.Contains(got, "POST /api/https/migrate/domain") || slices.Contains(got, "POST /api/https/certificate/manager") {
		t.Errorf("calls %v", got)
	}

	// Only a wildcard, and no domains set.
	_, c = newConsole(t, map[string]string{"username": "jec"})
	if _, err := c.Deploy(t.Context(), req); problemKey(err) != i18n.UpyunErrorNoDomains || !retry.IsPermanent(err) {
		t.Errorf("no domains: %v", err)
	}
}

func problemKey(err error) i18n.Key {
	var p *contracts.Problem
	if errors.As(err, &p) {
		return p.Key
	}
	return ""
}

func TestDeployFailures(t *testing.T) {
	for _, c := range []struct {
		name      string
		config    map[string]string
		reply     func(path string) (int, string)
		key       i18n.Key
		permanent bool
	}{
		{"a wrong password", map[string]string{"username": "jec", "password": "wrong"}, nil, i18n.UpyunErrorLogin, true},
		{"a domain of another account", map[string]string{"username": "jec", "domains": "other.example.org"}, nil, i18n.UpyunErrorDomain, true},
		{"a busy console", map[string]string{"username": "jec"}, func(path string) (int, string) {
			if path == "/api/https/certificate/" {
				return http.StatusServiceUnavailable, `{"data": {"error_code": 50000, "message": "busy"}}`
			}
			return 0, ""
		}, i18n.UpyunErrorApi, false},
		{"a page that is no answer", map[string]string{"username": "jec"}, func(path string) (int, string) {
			if path == "/api/https/certificate/" {
				return http.StatusOK, "<html>maintenance</html>"
			}
			return 0, ""
		}, i18n.UpyunErrorStatus, true},
		{"a sign-in that says no", map[string]string{"username": "jec"}, func(path string) (int, string) {
			if path == "/accounts/signin/" {
				return http.StatusOK, `{"data": {"result": false}}`
			}
			return 0, ""
		}, i18n.UpyunErrorLogin, true},
		{"a sign-in refused at once", map[string]string{"username": "jec"}, func(path string) (int, string) {
			if path == "/accounts/signin/" {
				return http.StatusBadRequest, `{"errors": [{"field": "password"}], "msg": "wrong password"}`
			}
			return 0, ""
		}, i18n.UpyunErrorLogin, true},
		{"a session refused", map[string]string{"username": "jec"}, func(path string) (int, string) {
			if path == "/api/https/certificate/" {
				return http.StatusUnauthorized, `{"data": {"error_code": 10030, "message": "please sign in"}}`
			}
			return 0, ""
		}, i18n.UpyunErrorLogin, true},
	} {
		f, cdn := newConsole(t, c.config)
		f.reply = c.reply
		req, _ := deployRequest(t, "www.example.com")
		_, err := cdn.Deploy(t.Context(), req)
		if problemKey(err) != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%s: %v", c.name, err)
		}
	}

	// The console cannot be reached.
	_, cdn := newConsole(t, map[string]string{"username": "jec"})
	cdn.base = "http://127.0.0.1:1"
	req, _ := deployRequest(t, "www.example.com")
	if _, err := cdn.Deploy(t.Context(), req); problemKey(err) != i18n.UpyunErrorNetwork || retry.IsPermanent(err) {
		t.Errorf("unreachable: %v", err)
	}
}

func TestTest(t *testing.T) {
	f, c := newConsole(t, map[string]string{"username": "jec"})
	if msg, err := c.Test(t.Context()); err != nil || msg.Key != i18n.UpyunTestLogin {
		t.Errorf("signed in: %+v %v", msg, err)
	}
	_, c = newConsole(t, map[string]string{"username": "jec", "domains": "www.example.com, cdn.example.com"})
	if msg, err := c.Test(t.Context()); err != nil || msg.Key != i18n.UpyunTestOk || msg.Params["domains"] != "www.example.com, cdn.example.com" {
		t.Errorf("domains: %+v %v", msg, err)
	}
	_, c = newConsole(t, map[string]string{"username": "jec", "domains": "other.example.org"})
	if _, err := c.Test(t.Context()); problemKey(err) != i18n.UpyunErrorDomain {
		t.Errorf("another account's domain: %v", err)
	}
	if got := f.called(); len(got) != 0 {
		t.Errorf("a test changed something: %v", got)
	}
}
