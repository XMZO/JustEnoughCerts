package safeline

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// TestLive deploys to a real SafeLine. CI does not run it: JEC_TEST_SAFELINE
// is its management address, such as https://127.0.0.1:9443, on a
// certificate of its own making, and JEC_TEST_SAFELINE_TOKEN an API token.
// With JEC_TEST_SAFELINE_TENGINE, the host its tengine listens on, a site
// on port 8443 shows that the certificate served follows.
func TestLive(t *testing.T) {
	base := os.Getenv("JEC_TEST_SAFELINE")
	if base == "" {
		t.Skip("JEC_TEST_SAFELINE is not set")
	}
	ctx := t.Context()
	open := func(config map[string]string) *firewall {
		t.Helper()
		config["url"], config["allow_private"] = base, "true"
		f, err := factory(ctx, providers.Instance{Config: config, Secrets: map[string]string{"api_token": os.Getenv("JEC_TEST_SAFELINE_TOKEN")}})
		if err != nil {
			t.Fatal(err)
		}
		return f.(*firewall)
	}

	// Its certificate is its own: the test tells the fingerprint to pin.
	_, err := open(map[string]string{}).Test(ctx)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.SafelineErrorUnverified {
		t.Fatalf("unpinned: %v", err)
	}
	pin := p.Params["fingerprint"].(string)
	f := open(map[string]string{"fingerprint": pin})
	if msg, err := f.Test(ctx); err != nil {
		t.Fatal(err)
	} else {
		t.Logf("test: %s %v", msg.Key, msg.Params)
	}
	if _, err := open(map[string]string{"fingerprint": pin, "api_token": "x"}).Test(ctx); err != nil {
		t.Logf("a wrong token: %v", err) // the token is a secret, so not this one
	}

	domain := fmt.Sprintf("jec-live-%d.example.com", time.Now().UnixNano())
	ca := cryptoxtest.NewCA(t)
	issue := func() contracts.DeployRequest {
		key := cryptoxtest.NewKey(t)
		leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), domain, "www."+domain)
		return contracts.DeployRequest{CertificateID: 1, Domains: []string{domain, "www." + domain}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key,
			Log: func(l contracts.LogLine) { t.Logf("%s %v", l.Key, l.Params) }}
	}
	ours := func() []certificate {
		t.Helper()
		list, err := f.certificates(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return slices.DeleteFunc(list, func(c certificate) bool { return !sameNames(c.Domains, []string{domain, "www." + domain}) })
	}
	content := func(id int64) string {
		t.Helper()
		var detail struct {
			Manual struct {
				Crt string `json:"crt"`
			} `json:"manual"`
		}
		if err := f.call(ctx, http.MethodGet, "cert/"+strconv.FormatInt(id, 10), nil, &detail); err != nil {
			t.Fatal(err)
		}
		return detail.Manual.Crt
	}

	// The first time, a new certificate; then the same one, replaced.
	first := issue()
	if _, err := f.Deploy(ctx, first); err != nil {
		t.Fatal(err)
	}
	added := ours()
	if len(added) != 1 {
		t.Fatalf("after the first deployment: %+v", added)
	}
	t.Cleanup(func() {
		if err := f.call(context.Background(), http.MethodDelete, "cert/"+strconv.FormatInt(added[0].ID, 10), nil, nil); err != nil {
			t.Errorf("removing the certificate: %v", err)
		}
	})
	served := func(*x509.Certificate) {}
	if host := os.Getenv("JEC_TEST_SAFELINE_TENGINE"); host != "" {
		var site int64
		err := f.call(ctx, http.MethodPost, "site", map[string]any{
			"ports": []string{"8443_ssl"}, "server_names": []string{domain}, "cert_id": added[0].ID, "upstreams": []string{"http://127.0.0.1:9"},
			"comment": "JEC", "load_balance": map[string]int{"balance_type": 1},
		}, &site)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := f.call(context.Background(), http.MethodDelete, "site", map[string]any{"ids": []int64{site}}, nil); err != nil {
				t.Errorf("removing the site: %v", err)
			}
		})
		served = func(want *x509.Certificate) {
			t.Helper()
			deadline := time.Now().Add(30 * time.Second)
			for {
				conn, err := tls.Dial("tcp", host+":8443", &tls.Config{ServerName: domain, InsecureSkipVerify: true})
				if err == nil {
					got := conn.ConnectionState().PeerCertificates[0]
					_ = conn.Close()
					if got.Equal(want) {
						return
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("the site does not serve the certificate deployed: %v", err)
				}
				time.Sleep(time.Second)
			}
		}
		served(first.Chain[0])
	}
	second := issue()
	if _, err := f.Deploy(ctx, second); err != nil {
		t.Fatal(err)
	}
	served(second.Chain[0])
	if again := ours(); len(again) != 1 || again[0].ID != added[0].ID {
		t.Fatalf("after the second deployment: %+v, first %+v", again, added)
	}
	if got, want := content(added[0].ID), string(cryptox.EncodeCertificates(second.Chain...)); got != want {
		t.Errorf("SafeLine holds\n%s\nwant\n%s", got, want)
	}

	// Set by its ID, the certificate is found and replaced too.
	byID := open(map[string]string{"fingerprint": pin, "certificate": strconv.FormatInt(added[0].ID, 10)})
	if msg, err := byID.Test(ctx); err != nil || msg.Key != i18n.SafelineTestOk {
		t.Errorf("test by ID: %v %v", msg, err)
	}
	if _, err := byID.Deploy(ctx, first); err != nil {
		t.Fatal(err)
	}
	if got := content(added[0].ID); got != string(cryptox.EncodeCertificates(first.Chain...)) {
		t.Error("not replaced by ID")
	}
	// A key that is not the certificate's is refused for good.
	bad := issue()
	bad.Key = cryptoxtest.NewKey(t)
	if _, err := f.Deploy(ctx, bad); !errors.As(err, &p) || p.Key != i18n.SafelineErrorApi {
		t.Errorf("a mismatched key: %v", err)
	} else {
		t.Logf("a mismatched key: %v", p.Params)
	}
}
