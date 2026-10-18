package proxmox

import (
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// fakeNode answers as the API of Proxmox VE does, for the node pve, with
// the status line fail says when it is set.
type fakeNode struct {
	t    *testing.T
	mu   sync.Mutex
	got  []url.Values
	fail string // a status line, such as "403 Permission check failed"
	body string
}

func (f *fakeNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "PVEAPIToken=root@pam!jec=0e7c-uuid" {
		f.t.Errorf("authorized as %q", r.Header.Get("Authorization"))
	}
	if err := r.ParseForm(); err != nil {
		f.t.Error(err)
	}
	f.mu.Lock()
	f.got = append(f.got, r.PostForm)
	f.mu.Unlock()
	if f.fail != "" {
		code, reason, _ := strings.Cut(f.fail, " ")
		// Proxmox VE puts the reason in the status line, which the server
		// of the tests writes from the code alone: a hijacked connection
		// writes it whole.
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			f.t.Fatal(err)
		}
		_, _ = buf.WriteString("HTTP/1.1 " + code + " " + reason + "\r\nContent-Type: application/json\r\nContent-Length: " +
			strconv.Itoa(len(f.body)) + "\r\nConnection: close\r\n\r\n" + f.body)
		_ = buf.Flush()
		_ = conn.Close()
		return
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /api2/json/nodes/pve/certificates/info", "POST /api2/json/nodes/pve/certificates/custom":
		_, _ = io.WriteString(w, `{"data": [{"filename": "pveproxy-ssl.pem"}]}`)
	default:
		w.WriteHeader(http.StatusNotImplemented)
	}
}

func open(t *testing.T, config map[string]string) *node {
	t.Helper()
	cfg := map[string]string{"node": "pve", "token_id": "root@pam!jec", "allow_private": "true"}
	for k, v := range config {
		cfg[k] = v
	}
	p, err := factory(t.Context(), providers.Instance{Config: cfg, Secrets: map[string]string{"token_secret": "0e7c-uuid"}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*node)
}

// serve starts a node over HTTPS, with a certificate of its own making as
// Proxmox VE has, and returns it with the settings that pin it.
func serve(t *testing.T) (*fakeNode, map[string]string) {
	t.Helper()
	f := &fakeNode{t: t}
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	_, err := open(t, map[string]string{"url": srv.URL}).Test(t.Context())
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.ProxmoxErrorUnverified {
		t.Fatalf("unpinned: %v", err)
	}
	return f, map[string]string{"url": srv.URL + "/", "fingerprint": p.Params["fingerprint"].(string)}
}

func request(t *testing.T) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "pve.example.com")
	return contracts.DeployRequest{CertificateID: 1, Domains: []string{"pve.example.com"}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key}
}

func TestDeploy(t *testing.T) {
	f, config := serve(t)
	msg, err := open(t, config).Test(t.Context())
	if err != nil || msg.Key != i18n.ProxmoxTestOk {
		t.Fatalf("test %+v %v", msg, err)
	}
	req := request(t)
	if _, err := open(t, config).Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	form := f.got[len(f.got)-1]
	b, err := cryptox.ParseBundle([]byte(form.Get("certificates")), []byte(form.Get("key")))
	if err != nil || !b.Leaf.Equal(req.Chain[0]) || len(b.Chain) != 1 || form.Get("force") != "1" || form.Get("restart") != "1" {
		t.Errorf("sent %v: %v", form, err)
	}
	config["restart"] = "false"
	if _, err := open(t, config).Deploy(t.Context(), request(t)); err != nil || f.got[len(f.got)-1].Get("restart") != "0" {
		t.Errorf("no restart: %v", err)
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		fail, body string
		key        i18n.Key
		permanent  bool
		reason     string
	}{
		{"401 authentication failure", ``, i18n.ProxmoxErrorToken, true, ""},
		{"403 Permission check failed (/nodes/pve, Sys.Modify)", ``, i18n.ProxmoxErrorPermission, true, ""},
		{"400 Parameter verification failed.", `{"data": null, "errors": {"certificates": "invalid PEM"}}`, i18n.ProxmoxErrorApi, true, "Parameter verification failed.; certificates: invalid PEM"},
		{"595 Connection refused", ``, i18n.ProxmoxErrorApi, false, "Connection refused"},
	} {
		f, config := serve(t)
		f.fail, f.body = c.fail, c.body
		_, err := open(t, config).Deploy(t.Context(), request(t))
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || retry.IsPermanent(err) != c.permanent || (c.reason != "" && p.Params["reason"] != c.reason) {
			t.Errorf("%s: %v %v", c.fail, err, p)
		}
	}
}

func TestSettings(t *testing.T) {
	for _, config := range []map[string]string{
		{"url": "192.168.1.2:8006", "node": "pve"},
		{"url": "https://192.168.1.2:8006", "node": "../pve"},
		{"url": "https://192.168.1.2:8006", "node": "pve", "fingerprint": "not one"},
	} {
		if _, err := factory(t.Context(), providers.Instance{Config: config}); err == nil || !retry.IsPermanent(err) {
			t.Errorf("%v: %v", config, err)
		}
	}
	for _, c := range []struct {
		config map[string]string
		key    i18n.Key
	}{
		{map[string]string{"url": "https://127.0.0.1:1", "allow_private": "false"}, i18n.ProxmoxErrorPrivate},
		{map[string]string{"url": "http://93.184.215.14:8006"}, i18n.ProxmoxErrorPlain},
	} {
		_, err := open(t, c.config).Test(t.Context())
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || !retry.IsPermanent(err) {
			t.Errorf("%v: %v", c.config, err)
		}
	}
}
