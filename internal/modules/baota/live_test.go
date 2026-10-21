package baota

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// TestLive deploys to a website of a real BaoTa panel and checks what its
// web server serves. CI does not run it: JEC_TEST_BAOTA is the panel's
// address, such as http://127.0.0.1:8888, with its API on,
// JEC_TEST_BAOTA_KEY the API key, JEC_TEST_BAOTA_SITE a website's name,
// which is its domain too, and JEC_TEST_BAOTA_TLS the address its web
// server serves HTTPS on, such as 127.0.0.1:443.
func TestLive(t *testing.T) {
	base, site, addr := os.Getenv("JEC_TEST_BAOTA"), os.Getenv("JEC_TEST_BAOTA_SITE"), os.Getenv("JEC_TEST_BAOTA_TLS")
	if base == "" || site == "" || addr == "" {
		t.Skip("JEC_TEST_BAOTA, JEC_TEST_BAOTA_SITE and JEC_TEST_BAOTA_TLS are not set")
	}
	ctx := t.Context()
	p, err := factory(ctx, providers.Instance{Config: map[string]string{"url": base, "sites": site}, Secrets: map[string]string{"api_key": os.Getenv("JEC_TEST_BAOTA_KEY")}})
	if err != nil {
		t.Fatal(err)
	}
	pl := p.(*panel)
	if msg, err := pl.Test(ctx); err != nil {
		t.Fatal(err)
	} else {
		t.Logf("test: %s %v", msg.Key, msg.Params)
	}
	ca := cryptoxtest.NewCA(t)
	issue := func() contracts.DeployRequest {
		key := cryptoxtest.NewKey(t)
		leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), site)
		return contracts.DeployRequest{CertificateID: 1, Domains: []string{site}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key,
			Log: func(l contracts.LogLine) { t.Logf("%s %v", l.Key, l.Params) }}
	}
	served := func(want *x509.Certificate) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: site, InsecureSkipVerify: true})
			if err == nil {
				got := conn.ConnectionState().PeerCertificates[0]
				_ = conn.Close()
				if got.Equal(want) {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s is not served the certificate deployed: %v", site, err)
			}
			time.Sleep(time.Second)
		}
	}
	for range 2 {
		req := issue()
		if _, err := pl.Deploy(ctx, req); err != nil {
			t.Fatal(err)
		}
		served(req.Chain[0])
	}
}
