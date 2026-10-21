package portainer

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// TestLive deploys to a real Portainer and checks what it serves. CI does
// not run it: JEC_TEST_PORTAINER is its address, such as
// https://portainer:9443, under a name, on a certificate of its own making,
// and JEC_TEST_PORTAINER_KEY an administrator's access token.
func TestLive(t *testing.T) {
	base := os.Getenv("JEC_TEST_PORTAINER")
	if base == "" {
		t.Skip("JEC_TEST_PORTAINER is not set")
	}
	ctx := t.Context()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	ca := cryptoxtest.NewCA(t)
	roots := x509.NewCertPool()
	roots.AddCert(ca.Root)
	// open trusts the test's CA, as the system's would a real one.
	open := func(address string, config map[string]string) *panel {
		t.Helper()
		config["url"], config["allow_private"] = address, "true"
		p, err := factory(ctx, providers.Instance{Config: config, Secrets: map[string]string{"api_key": os.Getenv("JEC_TEST_PORTAINER_KEY")}})
		if err != nil {
			t.Fatal(err)
		}
		pl := p.(*panel)
		if pl.opts.Pin != "" {
			pl.opts.Roots = roots
			if pl.client, err = httpx.Client(pl.opts); err != nil {
				t.Fatal(err)
			}
		}
		return pl
	}

	var p *contracts.Problem
	_, err = open(base, map[string]string{}).Test(ctx)
	if !errors.As(err, &p) || p.Key != i18n.PortainerErrorUnverified {
		t.Fatalf("unpinned: %v", err)
	}
	pin := p.Params["fingerprint"].(string)
	pl := open(base, map[string]string{"fingerprint": pin})
	if msg, err := pl.Test(ctx); err != nil {
		t.Fatal(err)
	} else {
		t.Logf("test: %s", msg.Key)
	}

	issue := func() contracts.DeployRequest {
		key := cryptoxtest.NewKey(t)
		leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), u.Hostname(), "www."+u.Hostname())
		return contracts.DeployRequest{CertificateID: 1, Domains: []string{u.Hostname()}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key,
			Log: func(l contracts.LogLine) { t.Logf("%s %v", l.Key, l.Params) }}
	}
	served := func(want *x509.Certificate) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			conn, err := tls.Dial("tcp", u.Host, &tls.Config{InsecureSkipVerify: true})
			if err == nil {
				got := conn.ConnectionState().PeerCertificates[0]
				_ = conn.Close()
				if got.Equal(want) {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("Portainer does not serve the certificate deployed: %v", err)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}

	first := issue()
	if _, err := pl.Deploy(ctx, first); err != nil {
		t.Fatal(err)
	}
	served(first.Chain[0])
	// No longer the certificate pinned, Portainer is trusted for its name.
	if msg, err := open(base, map[string]string{"fingerprint": pin}).Test(ctx); err != nil || msg.Key != i18n.PortainerTestOk {
		t.Errorf("test after deploying: %v %v", msg, err)
	}
	second := issue()
	if _, err := open(base, map[string]string{"fingerprint": pin}).Deploy(ctx, second); err != nil {
		t.Fatal(err)
	}
	served(second.Chain[0])

	// Reached by its address, a test cannot tell it; a deployment of the
	// certificate it serves can.
	addrs, err := net.LookupHost(u.Hostname())
	if err != nil {
		t.Fatal(err)
	}
	byAddress := "https://" + net.JoinHostPort(addrs[0], u.Port())
	if _, err := open(byAddress, map[string]string{"fingerprint": pin}).Test(ctx); !errors.As(err, &p) || p.Key != i18n.PortainerErrorPin {
		t.Errorf("test by address: %v", err)
	}
	third := issue()
	if _, err := open(byAddress, map[string]string{"fingerprint": pin}).Deploy(ctx, third); err != nil {
		t.Fatal(err)
	}
	served(third.Chain[0])
}
