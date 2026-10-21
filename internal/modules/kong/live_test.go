package kong

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

// TestLive deploys to a real Kong and checks what it serves. CI does not
// run it: JEC_TEST_KONG is its Admin API, such as http://127.0.0.1:8001,
// and JEC_TEST_KONG_TLS the address it proxies HTTPS on, such as
// 127.0.0.1:8443.
func TestLive(t *testing.T) {
	base, addr := os.Getenv("JEC_TEST_KONG"), os.Getenv("JEC_TEST_KONG_TLS")
	if base == "" || addr == "" {
		t.Skip("JEC_TEST_KONG and JEC_TEST_KONG_TLS are not set")
	}
	ctx := t.Context()
	open := func(config map[string]string) *gateway {
		t.Helper()
		config["url"] = base
		g, err := factory(ctx, providers.Instance{Config: config})
		if err != nil {
			t.Fatal(err)
		}
		return g.(*gateway)
	}
	g := open(map[string]string{})
	if msg, err := g.Test(ctx); err != nil {
		t.Fatal(err)
	} else {
		t.Logf("test: %s %v", msg.Key, msg.Params)
	}

	domain := fmt.Sprintf("jec-live-%d.example.com", time.Now().UnixNano())
	id := time.Now().Unix()
	ca := cryptoxtest.NewCA(t)
	issue := func(names ...string) contracts.DeployRequest {
		key := cryptoxtest.NewKey(t)
		leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), names...)
		return contracts.DeployRequest{CertificateID: id, Domains: names, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key,
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
	remove := func(object string) {
		if err := g.call(context.Background(), http.MethodDelete, "certificates/"+object, nil, nil); err != nil {
			t.Errorf("removing the certificate object: %v", err)
		}
	}

	first := issue(domain, "*."+domain)
	if _, err := g.Deploy(ctx, first); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { remove(g.objectFor(id)) })
	served(domain, first.Chain[0])
	served("api."+domain, first.Chain[0]) // through the wildcard
	// Replaced, with a name less: its SNI goes with it.
	second := issue(domain)
	if _, err := g.Deploy(ctx, second); err != nil {
		t.Fatal(err)
	}
	served(domain, second.Chain[0])
	var snis struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := g.call(ctx, http.MethodGet, "certificates/"+g.objectFor(id)+"/snis", nil, &snis); err != nil || len(snis.Data) != 1 || snis.Data[0].Name != domain {
		t.Errorf("SNIs %+v: %v", snis, err)
	}

	// A name another object holds is refused, and said so.
	other := open(map[string]string{"id": nameUUID("someone else")})
	theirs := issue("other." + domain)
	theirs.CertificateID = id + 1
	if _, err := other.Deploy(ctx, theirs); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { remove(nameUUID("someone else")) })
	clash := issue(domain, "other."+domain)
	var p *contracts.Problem
	if _, err := g.Deploy(ctx, clash); !errors.As(err, &p) || p.Key != i18n.KongErrorConflict {
		t.Errorf("a name held by another object: %v", err)
	} else {
		t.Logf("conflict: %v", p.Params)
	}
}
