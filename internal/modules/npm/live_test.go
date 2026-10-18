package npm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// TestLive deploys to a real Nginx Proxy Manager and checks what its nginx
// serves. CI does not run it: JEC_TEST_NPM is the panel's admin address,
// such as http://127.0.0.1:81, JEC_TEST_NPM_EMAIL and
// JEC_TEST_NPM_PASSWORD sign in an administrator, and JEC_TEST_NPM_TLS is
// the address of its HTTPS port, such as 127.0.0.1:443.
func TestLive(t *testing.T) {
	base, addr := os.Getenv("JEC_TEST_NPM"), os.Getenv("JEC_TEST_NPM_TLS")
	if base == "" || addr == "" {
		t.Skip("JEC_TEST_NPM and JEC_TEST_NPM_TLS are not set")
	}
	ctx := t.Context()
	p, err := factory(ctx, providers.Instance{
		Config:  map[string]string{"url": base, "email": os.Getenv("JEC_TEST_NPM_EMAIL")},
		Secrets: map[string]string{"password": os.Getenv("JEC_TEST_NPM_PASSWORD")},
	})
	if err != nil {
		t.Fatal(err)
	}
	pl := p.(*panel)
	if msg, err := pl.Test(ctx); err != nil {
		t.Fatal(err)
	} else {
		t.Logf("test: %s %v", msg.Key, msg.Params)
	}
	s, err := pl.signIn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// remove runs in cleanups too, once the test's context is canceled.
	remove := func(path ...string) {
		if err := s.call(context.Background(), http.MethodDelete, nil, nil, nil, path...); err != nil {
			t.Errorf("deleting %v: %v", path, err)
		}
	}

	domain := fmt.Sprintf("jec-live-%d.example.com", time.Now().UnixNano())
	ca := cryptoxtest.NewCA(t)
	var applied []any
	issue := func(previous ...string) contracts.DeployRequest {
		key := cryptoxtest.NewKey(t)
		leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), domain)
		return contracts.DeployRequest{CertificateID: 1, Domains: []string{domain}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key,
			Previous: previous, Log: func(l contracts.LogLine) {
				t.Logf("%s %v", l.Key, l.Params)
				if l.Key == i18n.NpmLogApply {
					applied = append(applied, l.Params["kind"])
				}
			}}
	}
	deploy := func(req contracts.DeployRequest) string {
		t.Helper()
		res, err := pl.Deploy(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		return res.Ref
	}

	first := issue()
	ref := deploy(first)
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil {
		t.Fatalf("ref %q", ref)
	}
	t.Cleanup(func() { remove("nginx", "certificates", ref) })

	// A proxy host takes it: nginx serves the first version.
	var proxy struct {
		ID int64 `json:"id"`
	}
	err = s.call(ctx, http.MethodPost, nil, map[string]any{
		"domain_names": []string{domain}, "forward_scheme": "http", "forward_host": "127.0.0.1", "forward_port": 81,
		"certificate_id": id, "access_list_id": 0, "meta": map[string]any{}, "locations": []any{},
	}, &proxy, "nginx", "proxy-hosts")
	if err != nil {
		t.Fatal(err)
	}
	served(t, addr, domain, first.Chain[0])

	// The next version goes into the same certificate and replaces the
	// first, once JEC saves the host again.
	second := issue(ref)
	if again := deploy(second); again != ref {
		t.Errorf("ref %q, then %q", ref, again)
	}
	served(t, addr, domain, second.Chain[0])

	// With a 404 host and a stream in its place, the 404 host applies it.
	remove("nginx", "proxy-hosts", strconv.FormatInt(proxy.ID, 10))
	var dead, stream struct {
		ID int64 `json:"id"`
	}
	err = s.call(ctx, http.MethodPost, nil, map[string]any{"domain_names": []string{domain}, "certificate_id": id, "meta": map[string]any{}}, &dead, "nginx", "dead-hosts")
	if err != nil {
		t.Fatal(err)
	}
	err = s.call(ctx, http.MethodPost, nil, map[string]any{
		"incoming_port": 30000 + time.Now().Nanosecond()%20000, "forwarding_host": "127.0.0.1", "forwarding_port": 81,
		"tcp_forwarding": true, "udp_forwarding": false, "certificate_id": id, "meta": map[string]any{},
	}, &stream, "nginx", "streams")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { remove("nginx", "streams", strconv.FormatInt(stream.ID, 10)) })
	third := issue(ref)
	deploy(third)
	served(t, addr, domain, third.Chain[0])

	// The stream alone applies it too.
	remove("nginx", "dead-hosts", strconv.FormatInt(dead.ID, 10))
	deploy(issue(ref))
	if fmt.Sprint(applied) != "[proxy dead stream]" {
		t.Errorf("applied through %v", applied)
	}
	if msg, err := (&panel{client: pl.client, base: pl.base, host: pl.host, email: pl.email, password: pl.password, id: id}).Test(ctx); err != nil ||
		msg.Key != i18n.NpmTestOk || msg.Params["count"] != 1 {
		t.Errorf("test: %+v %v", msg, err)
	}
}

// served waits for nginx at addr to serve want for domain.
func served(t *testing.T, addr, domain string, want *x509.Certificate) {
	t.Helper()
	var got *x509.Certificate
	for range 50 {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, &tls.Config{ServerName: domain, InsecureSkipVerify: true})
		if err == nil {
			got = conn.ConnectionState().PeerCertificates[0]
			_ = conn.Close()
			if got.Equal(want) {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if got == nil {
		t.Fatalf("no certificate served for %s", domain)
	}
	t.Fatalf("serving %s, not %s", got.SerialNumber, want.SerialNumber)
}
