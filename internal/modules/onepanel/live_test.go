package onepanel

import (
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// TestLive deploys to a real 1Panel. CI does not run it: JEC_TEST_ONEPANEL
// is the panel's address, such as http://127.0.0.1:10086, with its API
// on, JEC_TEST_ONEPANEL_KEY the API key, and JEC_TEST_ONEPANEL_VERSION v1
// or v2, v2 when left out.
func TestLive(t *testing.T) {
	base := os.Getenv("JEC_TEST_ONEPANEL")
	if base == "" {
		t.Skip("JEC_TEST_ONEPANEL is not set")
	}
	ctx := t.Context()
	version := os.Getenv("JEC_TEST_ONEPANEL_VERSION")
	if version == "" {
		version = apiV2
	}
	open := func(config map[string]string) *panel {
		t.Helper()
		config["url"], config["api_version"] = base, version
		p, err := factory(ctx, providers.Instance{Config: config, Secrets: map[string]string{"api_key": os.Getenv("JEC_TEST_ONEPANEL_KEY")}})
		if err != nil {
			t.Fatal(err)
		}
		return p.(*panel)
	}
	domain := fmt.Sprintf("jec-live-%d.example.com", time.Now().UnixNano())
	ca := cryptoxtest.NewCA(t)
	issue := func() contracts.DeployRequest {
		key := cryptoxtest.NewKey(t)
		leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), domain)
		return contracts.DeployRequest{CertificateID: 1, Domains: []string{domain}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key,
			Log: func(l contracts.LogLine) { t.Logf("%s %v", l.Key, l.Params) }}
	}

	// A certificate of the panel's, as people upload one first.
	first := issue()
	key, err := cryptox.EncodeKey(first.Key)
	if err != nil {
		t.Fatal(err)
	}
	bare := open(map[string]string{})
	err = bare.call(ctx, "/websites/ssl/upload", map[string]any{
		"type": "paste", "certificate": string(cryptox.EncodeCertificates(first.Chain...)), "privateKey": string(key), "description": "JEC live test",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	list, err := bare.certificates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, c := range list {
		if c.PrimaryDomain == domain {
			id = c.ID
		}
	}
	if id == 0 {
		t.Fatalf("the uploaded certificate is not listed: %+v", list)
	}
	t.Cleanup(func() {
		if err := bare.call(context.Background(), "/websites/ssl/del", map[string]any{"ids": []int64{id}}, nil); err != nil {
			t.Logf("removing the certificate: %v", err)
		}
	})
	if msg, err := bare.Test(ctx); err != nil || msg.Key != i18n.OnepanelTestList {
		t.Errorf("test without an ID: %v %v", msg, err)
	}

	p := open(map[string]string{"certificate": strconv.FormatInt(id, 10)})
	if msg, err := p.Test(ctx); err != nil || msg.Key != i18n.OnepanelTestOk {
		t.Fatalf("test: %v %v", msg, err)
	}
	second := issue()
	if _, err := p.Deploy(ctx, second); err != nil {
		t.Fatal(err)
	}
	var detail struct {
		Pem         string `json:"pem"`
		Description string `json:"description"`
	}
	if err := p.call(ctx, "/websites/ssl/"+strconv.FormatInt(id, 10), nil, &detail); err != nil {
		t.Fatal(err)
	}
	got, err := cryptox.ParseCertificates([]byte(detail.Pem))
	if err != nil || len(got) == 0 || !got[0].Equal(second.Chain[0]) || detail.Description != "JEC live test" {
		t.Errorf("the panel holds %d certificates, the first the new one: %v, description %q: %v", len(got), len(got) > 0 && got[0].Equal(second.Chain[0]), detail.Description, err)
	}
}

// TestLivePanel deploys the panel's own certificate to a real 1Panel that
// starts without HTTPS. CI does not run it: as TestLive, with
// JEC_TEST_ONEPANEL an http:// address by IP, and JEC_TEST_ONEPANEL_RESTART
// a shell command that restarts the panel, for an install that cannot
// restart itself, such as one in a container.
func TestLivePanel(t *testing.T) {
	base := os.Getenv("JEC_TEST_ONEPANEL")
	if base == "" {
		t.Skip("JEC_TEST_ONEPANEL is not set")
	}
	ctx := t.Context()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	ca := cryptoxtest.NewCA(t)
	roots := x509.NewCertPool()
	roots.AddCert(ca.Root)
	open := func(address string) *panelSSL {
		t.Helper()
		p, err := panelFactory(ctx, providers.Instance{
			Config:  map[string]string{"url": address, "api_version": cmp.Or(os.Getenv("JEC_TEST_ONEPANEL_VERSION"), apiV2), "allow_private": "true"},
			Secrets: map[string]string{"api_key": os.Getenv("JEC_TEST_ONEPANEL_KEY")},
		})
		if err != nil {
			t.Fatal(err)
		}
		s := p.(*panelSSL)
		// The test's CA, as the system's would trust a real one.
		s.opts.Roots = roots
		if s.client, err = httpx.Client(s.opts); err != nil {
			t.Fatal(err)
		}
		return s
	}
	restart := func() {
		t.Helper()
		if cmd := os.Getenv("JEC_TEST_ONEPANEL_RESTART"); cmd != "" {
			if out, err := exec.CommandContext(ctx, "sh", "-c", cmd).CombinedOutput(); err != nil {
				t.Fatalf("restarting the panel: %v %s", err, out)
			}
		}
	}
	issue := func() contracts.DeployRequest {
		key := cryptoxtest.NewKey(t)
		leaf := ca.Sign(t, key, &x509.Certificate{
			Subject: pkix.Name{CommonName: "panel.example"}, DNSNames: []string{"panel.example"}, IPAddresses: []net.IP{net.ParseIP(u.Hostname())},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		return contracts.DeployRequest{CertificateID: 1, Domains: []string{"panel.example"}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key,
			Log: func(l contracts.LogLine) { t.Logf("%s %v", l.Key, l.Params) }}
	}
	served := func(want *x509.Certificate) {
		t.Helper()
		conn, err := tls.Dial("tcp", u.Host, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		if !conn.ConnectionState().PeerCertificates[0].Equal(want) {
			t.Fatal("the panel does not serve the certificate deployed")
		}
	}

	plain := open(base)
	if msg, err := plain.Test(ctx); err != nil || msg.Key != i18n.OnepanelTestPlain {
		t.Fatalf("test of a panel without HTTPS: %v %v", msg, err)
	}
	first := issue()
	if _, err := plain.Deploy(ctx, first); err != nil {
		t.Fatal(err)
	}
	restart()
	served(first.Chain[0])

	// Its HTTPS on, a test over HTTP says to change the address; a
	// deployment goes over HTTPS on its own.
	var p *contracts.Problem
	if _, err := open(base).Test(ctx); !errors.As(err, &p) || p.Key != i18n.OnepanelErrorHttpsNow {
		t.Errorf("test over HTTP: %v", err)
	}
	second := issue()
	if _, err := open(base).Deploy(ctx, second); err != nil {
		t.Fatal(err)
	}
	restart()
	served(second.Chain[0])
	secure := "https://" + u.Host
	if msg, err := open(secure).Test(ctx); err != nil || msg.Key != i18n.OnepanelTestPanel {
		t.Errorf("test over HTTPS: %v %v", msg, err)
	} else {
		t.Logf("test: %v", msg.Params)
	}
}
