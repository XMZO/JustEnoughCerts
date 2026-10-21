package apisix

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// TestLive deploys to a real APISIX and checks what it serves. CI does not
// run it: JEC_TEST_APISIX is its Admin API, such as http://127.0.0.1:9180,
// JEC_TEST_APISIX_KEY an admin key, and JEC_TEST_APISIX_TLS the address it
// serves HTTPS on, such as 127.0.0.1:9443.
func TestLive(t *testing.T) {
	base, addr := os.Getenv("JEC_TEST_APISIX"), os.Getenv("JEC_TEST_APISIX_TLS")
	if base == "" || addr == "" {
		t.Skip("JEC_TEST_APISIX and JEC_TEST_APISIX_TLS are not set")
	}
	ctx := t.Context()
	open := func(key string) *gateway {
		t.Helper()
		g, err := factory(ctx, providers.Instance{Config: map[string]string{"url": base}, Secrets: map[string]string{"admin_key": key}})
		if err != nil {
			t.Fatal(err)
		}
		return g.(*gateway)
	}
	var p *contracts.Problem
	if _, err := open("not the key").Test(ctx); !errors.As(err, &p) || p.Key != i18n.ApisixErrorKey {
		t.Errorf("a wrong key: %v", err)
	}
	g := open(os.Getenv("JEC_TEST_APISIX_KEY"))
	if msg, err := g.Test(ctx); err != nil {
		t.Fatal(err)
	} else {
		t.Logf("test: %s %v", msg.Key, msg.Params)
	}

	domain := fmt.Sprintf("jec-live-%d.example.com", time.Now().UnixNano())
	id := time.Now().Unix()
	ca := cryptoxtest.NewCA(t)
	issue := func() contracts.DeployRequest {
		key := cryptoxtest.NewKey(t)
		leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), domain, "*."+domain)
		return contracts.DeployRequest{CertificateID: id, Domains: []string{domain, "*." + domain}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key,
			Log: func(l contracts.LogLine) { t.Logf("%s %v", l.Key, l.Params) }}
	}
	served := func(name string, want *x509.Certificate) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: name, InsecureSkipVerify: true})
			if err == nil {
				got := conn.ConnectionState().PeerCertificates[0]
				_ = conn.Close()
				if got.Equal(want) {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s is not served the certificate deployed: %v", name, err)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}

	first := issue()
	if _, err := g.Deploy(ctx, first); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := g.call(context.Background(), http.MethodDelete, "ssls/"+g.objectFor(id), nil, nil); err != nil {
			t.Errorf("removing the SSL object: %v", err)
		}
	})
	served(domain, first.Chain[0])
	served("api."+domain, first.Chain[0]) // through the wildcard
	second := issue()
	if _, err := g.Deploy(ctx, second); err != nil {
		t.Fatal(err)
	}
	served(domain, second.Chain[0])

}
