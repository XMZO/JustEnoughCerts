package npm

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

const (
	email    = "jec@example.com"
	password = "s3cret password"
	token    = "the-token"
)

// fakeCert is a certificate of the fake panel, with the hosts using it by
// the kind's expand name.
type fakeCert struct {
	provider, name string
	files          map[string]string
	hosts          map[string][]map[string]any
}

// fakePanel answers as Nginx Proxy Manager 2.16 does, for what JEC uses.
type fakePanel struct {
	t     *testing.T
	mu    sync.Mutex
	certs map[int64]*fakeCert
	next  int64
	// saved lists the hosts saved again, as kind path and ID.
	saved []string
	// offline has the panel take saved hosts offline, as when nginx refuses
	// their configuration.
	offline bool
	twoFA   bool
	// reply, when it gives a status, answers instead for the API path.
	reply func(path string) (int, string)
}

func (f *fakePanel) fail(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":{"code":%d,"message":%q}}`, status, message)
}

func (f *fakePanel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/api")
	if f.reply != nil {
		if status, answer := f.reply(path); status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, answer)
			return
		}
	}
	if path == "/tokens" && r.Method == http.MethodPost {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["identity"] != email || body["secret"] != password {
			f.fail(w, http.StatusBadRequest, "Invalid email or password")
			return
		}
		if f.twoFA {
			_, _ = io.WriteString(w, `{"requires_2fa": true, "challenge_token": "c"}`)
			return
		}
		_, _ = io.WriteString(w, `{"token": "`+token+`", "expires": "2026-10-19T10:00:00.000Z"}`)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+token {
		f.fail(w, http.StatusUnauthorized, "Permission Denied")
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/nginx/"), "/")
	switch {
	case path == "/nginx/certificates" && r.Method == http.MethodGet:
		var list []map[string]any
		for id, c := range f.certs {
			list = append(list, map[string]any{"id": id, "provider": c.provider, "nice_name": c.name, "domain_names": []string{}})
		}
		_ = json.NewEncoder(w).Encode(list)
	case path == "/nginx/certificates" && r.Method == http.MethodPost:
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.next++
		f.certs[f.next] = &fakeCert{provider: body["provider"], name: body["nice_name"], files: map[string]string{}}
		_, _ = fmt.Fprintf(w, `{"id": %d, "provider": %q, "nice_name": %q, "domain_names": [], "meta": {}}`, f.next, body["provider"], body["nice_name"])
	case len(parts) >= 2 && parts[0] == "certificates":
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		c := f.certs[id]
		if c == nil {
			f.fail(w, http.StatusNotFound, "Not Found - "+parts[1])
			return
		}
		if len(parts) == 3 && parts[2] == "upload" && r.Method == http.MethodPost {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				f.fail(w, http.StatusBadRequest, "No files were uploaded")
				return
			}
			for name, files := range r.MultipartForm.File {
				file, _ := files[0].Open()
				data, _ := io.ReadAll(file)
				c.files[name] = string(data)
			}
			_ = json.NewEncoder(w).Encode(c.files)
			return
		}
		if r.URL.Query().Get("expand") != "proxy_hosts,redirection_hosts,dead_hosts,streams" {
			f.t.Errorf("expand %q", r.URL.Query().Get("expand"))
		}
		answer := map[string]any{"id": id, "provider": c.provider, "nice_name": c.name, "domain_names": []string{"example.com"}}
		for _, k := range kinds {
			answer[k.expand] = append([]map[string]any{}, c.hosts[k.expand]...)
		}
		_ = json.NewEncoder(w).Encode(answer)
	case len(parts) == 2 && r.Method == http.MethodPut:
		var body map[string]int64
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.saved = append(f.saved, fmt.Sprintf("%s/%s:%d", parts[0], parts[1], body["certificate_id"]))
		meta := map[string]any{"nginx_online": !f.offline, "nginx_err": nil}
		if f.offline {
			meta["nginx_err"] = "nginx: [emerg] cannot load certificate key\n"
		}
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "certificate_id": body["certificate_id"], "meta": meta})
	default:
		f.fail(w, http.StatusNotFound, "Not Found")
	}
}

func serve(t *testing.T, f *fakePanel, config map[string]string) *panel {
	t.Helper()
	f.t = t
	if f.certs == nil {
		f.certs = map[int64]*fakeCert{}
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfg := map[string]string{"url": srv.URL + "/", "email": email}
	for k, v := range config {
		cfg[k] = v
	}
	p, err := factory(t.Context(), providers.Instance{Config: cfg, Secrets: map[string]string{"password": password}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*panel)
}

// request makes a certificate for example.com, logging into lines.
func request(t *testing.T, lines *[]contracts.LogLine) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "example.com", "www.example.com")
	return contracts.DeployRequest{CertificateID: 7, Domains: []string{"example.com", "www.example.com"},
		Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key,
		Log: func(l contracts.LogLine) { *lines = append(*lines, l) }}
}

func logged(lines []contracts.LogLine) []i18n.Key {
	var keys []i18n.Key
	for _, l := range lines {
		keys = append(keys, l.Key)
	}
	return keys
}

// checkFiles checks that c holds the leaf of req and its key, and the rest
// of its chain as the intermediate certificates.
func checkFiles(t *testing.T, c *fakeCert, req contracts.DeployRequest) {
	t.Helper()
	b, err := cryptox.ParseBundle([]byte(c.files["certificate"]+c.files["intermediate_certificate"]), []byte(c.files["certificate_key"]))
	if err != nil || !b.Leaf.Equal(req.Chain[0]) || len(b.Chain) != 1 || !b.Chain[0].Equal(req.Chain[1]) {
		t.Errorf("files %v: %v", c.files, err)
	}
	if leaf, err := cryptox.ParseCertificates([]byte(c.files["certificate"])); err != nil || len(leaf) != 1 {
		t.Errorf("certificate holds %d certificates: %v", len(leaf), err)
	}
}

func TestDeployOwn(t *testing.T) {
	f := &fakePanel{}
	p := serve(t, f, nil)
	var lines []contracts.LogLine
	req := request(t, &lines)
	res, err := p.Deploy(t.Context(), req)
	if err != nil || res.Ref != "1" || len(res.Removed) != 0 {
		t.Fatalf("first: %+v %v", res, err)
	}
	c := f.certs[1]
	if c.provider != custom || c.name != "example.com (JEC #7)" {
		t.Errorf("added %+v", c)
	}
	checkFiles(t, c, req)
	if got := logged(lines); len(got) != 3 || got[0] != i18n.NpmLogCreate || got[1] != i18n.NpmLogUpload || got[2] != i18n.NpmLogUnused {
		t.Errorf("logged %v", got)
	}
	if lines[2].Level != contracts.Warn || lines[2].Params["certificate"] != "#1 example.com (JEC #7)" {
		t.Errorf("unused: %+v", lines[2])
	}

	// Once people pick it on hosts, a new version goes into the same
	// certificate, and saving the first host serving it applies it.
	c.hosts = map[string][]map[string]any{
		"proxy_hosts": {
			{"id": 4, "domain_names": []string{"off.example.com"}, "enabled": false},
			{"id": 5, "domain_names": []string{"down.example.com"}, "enabled": true, "meta": map[string]any{"nginx_online": false}},
			{"id": 6, "domain_names": []string{"example.com", "www.example.com"}, "enabled": true, "meta": map[string]any{"nginx_online": true}},
		},
		"streams": {{"id": 2, "incoming_port": 993, "enabled": true}},
	}
	lines = nil
	next := request(t, &lines)
	next.Log = req.Log
	next.Previous = []string{"1", "1"}
	if res, err = p.Deploy(t.Context(), next); err != nil || res.Ref != "1" || len(f.certs) != 1 {
		t.Fatalf("second: %+v %v, %d certificates", res, err, len(f.certs))
	}
	checkFiles(t, c, next)
	if len(f.saved) != 1 || f.saved[0] != "proxy-hosts/6:1" {
		t.Errorf("saved %v", f.saved)
	}
	if got := logged(lines); len(got) != 2 || got[1] != i18n.NpmLogApply || lines[1].Params["kind"] != "proxy" || lines[1].Params["host"] != "example.com, www.example.com" {
		t.Errorf("logged %v %+v", got, lines)
	}
}

func TestDeployGone(t *testing.T) {
	f := &fakePanel{next: 9}
	var lines []contracts.LogLine
	req := request(t, &lines)
	req.Previous = []string{"", "nonsense", "3"}
	res, err := serve(t, f, nil).Deploy(t.Context(), req)
	if err != nil || res.Ref != "10" || f.certs[10] == nil {
		t.Fatalf("%+v %v", res, err)
	}
	if got := logged(lines); len(got) != 4 || got[0] != i18n.NpmLogGone || lines[0].Params["id"] != int64(3) || got[1] != i18n.NpmLogCreate {
		t.Errorf("logged %v", got)
	}
}

func TestDeploySet(t *testing.T) {
	f := &fakePanel{certs: map[int64]*fakeCert{
		3: {provider: custom, name: "mine", files: map[string]string{}, hosts: map[string][]map[string]any{
			"redirection_hosts": {{"id": 8, "domain_names": []string{"old.example.com"}, "enabled": true}},
		}},
		4: {provider: "letsencrypt", name: "example.com", files: map[string]string{}},
	}}
	var lines []contracts.LogLine
	req := request(t, &lines)
	req.Previous = []string{"1"}
	res, err := serve(t, f, map[string]string{"certificate": "3"}).Deploy(t.Context(), req)
	if err != nil || res.Ref != "" {
		t.Fatalf("%+v %v", res, err)
	}
	checkFiles(t, f.certs[3], req)
	if len(f.saved) != 1 || f.saved[0] != "redirection-hosts/8:3" {
		t.Errorf("saved %v", f.saved)
	}

	_, err = serve(t, f, map[string]string{"certificate": "4"}).Deploy(t.Context(), req)
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.NpmErrorProvider || pr.Params["certificate"] != "#4 example.com" || !retry.IsPermanent(err) {
		t.Errorf("Let's Encrypt: %v", err)
	}
	_, err = serve(t, f, map[string]string{"certificate": "9"}).Deploy(t.Context(), req)
	if !errors.As(err, &pr) || pr.Key != i18n.NpmErrorNoCertificate || pr.Params["certificates"] != "#3 mine" {
		t.Errorf("missing: %v", err)
	}
	_, err = serve(t, &fakePanel{}, map[string]string{"certificate": "9"}).Deploy(t.Context(), req)
	if !errors.As(err, &pr) || pr.Key != i18n.NpmErrorNoCertificates {
		t.Errorf("none: %v", err)
	}
}

func TestNginxRefused(t *testing.T) {
	f := &fakePanel{offline: true, certs: map[int64]*fakeCert{
		3: {provider: custom, name: "mine", files: map[string]string{}, hosts: map[string][]map[string]any{
			"dead_hosts": {{"id": 2, "domain_names": []string{"gone.example.com"}, "enabled": true}},
		}},
	}}
	var lines []contracts.LogLine
	_, err := serve(t, f, map[string]string{"certificate": "3"}).Deploy(t.Context(), request(t, &lines))
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.NpmErrorNginx || pr.Params["kind"] != "dead" ||
		pr.Params["error"] != "nginx: [emerg] cannot load certificate key" || !retry.IsPermanent(err) {
		t.Errorf("%v %+v", err, pr)
	}
}

func TestReload(t *testing.T) {
	f := &fakePanel{certs: map[int64]*fakeCert{
		3: {provider: custom, name: "mine", files: map[string]string{}, hosts: map[string][]map[string]any{
			"proxy_hosts": {{"id": 2, "domain_names": []string{"example.com"}, "enabled": true}},
		}},
	}, reply: func(path string) (int, string) {
		if path == "/nginx/proxy-hosts/2" {
			return 500, `{"error":{"code":500,"message":"Internal Error"}}`
		}
		return 0, ""
	}}
	var lines []contracts.LogLine
	_, err := serve(t, f, map[string]string{"certificate": "3"}).Deploy(t.Context(), request(t, &lines))
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.NpmErrorReload || pr.Params["message"] != "Internal Error" || retry.IsPermanent(err) {
		t.Errorf("%v", err)
	}
}

func TestTest(t *testing.T) {
	msg, err := serve(t, &fakePanel{}, nil).Test(t.Context())
	if err != nil || msg.Key != i18n.NpmTestNew {
		t.Errorf("none: %+v %v", msg, err)
	}
	f := &fakePanel{certs: map[int64]*fakeCert{
		3: {provider: custom, name: "mine", hosts: map[string][]map[string]any{
			"proxy_hosts": {{"id": 1}, {"id": 2}}, "streams": {{"id": 1}},
		}},
		4: {provider: "letsencrypt", name: "theirs"},
	}}
	msg, err = serve(t, f, nil).Test(t.Context())
	if err != nil || msg.Key != i18n.NpmTestList || msg.Params["certificates"] != "#3 mine" {
		t.Errorf("listed: %+v %v", msg, err)
	}
	msg, err = serve(t, f, map[string]string{"certificate": "3"}).Test(t.Context())
	if err != nil || msg.Key != i18n.NpmTestOk || msg.Params["certificate"] != "#3 mine" || msg.Params["count"] != 3 {
		t.Errorf("found: %+v %v", msg, err)
	}
}

func TestSignIn(t *testing.T) {
	var pr *contracts.Problem
	_, err := serve(t, &fakePanel{}, nil).Test(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	p := serve(t, &fakePanel{}, nil)
	p.password = "wrong"
	_, err = p.Test(t.Context())
	if !errors.As(err, &pr) || pr.Key != i18n.NpmErrorSignIn || pr.Params["message"] != "Invalid email or password" || !retry.IsPermanent(err) {
		t.Errorf("wrong password: %v", err)
	}
	_, err = serve(t, &fakePanel{twoFA: true}, nil).Test(t.Context())
	if !errors.As(err, &pr) || pr.Key != i18n.NpmErrorTwoFactor || !retry.IsPermanent(err) {
		t.Errorf("two-factor: %v", err)
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		status    int
		body      string
		key       i18n.Key
		permanent bool
	}{
		{403, `{"error":{"code":403,"message":"Permission Denied"}}`, i18n.NpmErrorDenied, true},
		{500, `{"error":{"code":500,"message":"Internal Error"}}`, i18n.NpmErrorApi, false},
		{400, `{"error":{"code":400,"message":"Certificate Key is not valid"}}`, i18n.NpmErrorApi, true},
		{502, `<html>bad gateway</html>`, i18n.NpmErrorStatus, false},
		{404, `<html>not found</html>`, i18n.NpmErrorStatus, true},
	} {
		f := &fakePanel{reply: func(path string) (int, string) {
			if strings.HasSuffix(path, "/upload") {
				return c.status, c.body
			}
			return 0, ""
		}}
		var lines []contracts.LogLine
		_, err := serve(t, f, nil).Deploy(t.Context(), request(t, &lines))
		var pr *contracts.Problem
		if !errors.As(err, &pr) || pr.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
	}
	// A server that is not the panel answers its sign-in with a page.
	_, err := serve(t, &fakePanel{reply: func(string) (int, string) { return 200, "<html>hello</html>" }}, nil).Test(t.Context())
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.NpmErrorStatus || !retry.IsPermanent(err) {
		t.Errorf("not the panel: %v", err)
	}
}

func TestSettings(t *testing.T) {
	for _, config := range []map[string]string{
		{"url": "192.168.1.10:81"},
		{"url": "ftp://192.168.1.10"},
		{"url": "https://203.0.113.5:81", "fingerprint": "not one"},
		{"url": "http://192.168.1.10:81", "certificate": "three"},
		{"url": "http://192.168.1.10:81", "certificate": "0"},
	} {
		if _, err := factory(t.Context(), providers.Instance{Config: config}); err == nil || !retry.IsPermanent(err) {
			t.Errorf("%v: %v", config, err)
		}
	}
	p, err := factory(t.Context(), providers.Instance{Config: map[string]string{"url": "http://93.184.215.14:81", "email": email}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.(*panel).Test(t.Context())
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.NpmErrorPlain {
		t.Errorf("plain HTTP to a public address: %v", err)
	}
}
