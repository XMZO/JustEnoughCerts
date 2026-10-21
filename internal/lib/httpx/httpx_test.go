package httpx

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

func TestPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "203.0.114.1": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false, "169.254.169.254": false,
		"100.64.0.1": false, "0.0.0.0": false, "224.0.0.1": false, "198.18.0.1": false,
		"::1": false, "fd00::1": false, "fe80::1": false, "::ffff:10.0.0.1": false, "64:ff9b::a00:1": false, "2002:a00:1::": false,
	} {
		if got := Public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Public(%s) = %v", addr, got)
		}
	}
}

func TestPublicOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()

	c, err := Client(Options{PublicOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(srv.URL + "/hook?token=s3cret")
	if err == nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrNotPublic) {
		t.Fatalf("a loopback address: %v", err)
	}
	if strings.Contains(Cause(err).Error(), "s3cret") {
		t.Errorf("the cause holds the URL: %v", Cause(err))
	}

	c, err = Client(Options{})
	if err != nil {
		t.Fatal(err)
	}
	resp, err = c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

func TestProxy(t *testing.T) {
	for _, p := range []string{"http://127.0.0.1:7890", "socks5://user:pass@127.0.0.1:1080", "socks5h://proxy:1080"} {
		if _, err := Client(Options{Proxy: p}); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	for _, p := range []string{"127.0.0.1:7890", "ftp://proxy", "http://"} {
		if _, err := Client(Options{Proxy: p}); !errors.Is(err, ErrProxy) {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := Client(Options{Proxy: "http://127.0.0.1:7890", PublicOnly: true}); err == nil {
		t.Error("a proxy with PublicOnly")
	}
}

func TestSkipVerify(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()

	c, err := Client(Options{})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("a certificate of no known CA was accepted")
	}

	c, err = Client(Options{SkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	resp, err = c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

// A server whose certificate no CA signed is trusted by its fingerprint, as
// the error of a connection that did not pin it tells.
func TestPin(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()

	c, err := Client(Options{})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("a certificate of no known CA was accepted")
	}
	fp, ok := Fingerprint(err)
	sum := sha256.Sum256(srv.Certificate().Raw)
	if want := strings.ToUpper(hex.EncodeToString(sum[:])); !ok || strings.ReplaceAll(fp, ":", "") != want || len(fp) != 95 {
		t.Fatalf("fingerprint %q %v, want %s", fp, ok, want)
	}

	for _, pin := range []string{fp, strings.ToLower(strings.ReplaceAll(fp, ":", ""))} {
		c, err = Client(Options{Pin: pin})
		if err != nil {
			t.Fatal(err)
		}
		resp, err = c.Get(srv.URL)
		if err != nil {
			t.Fatalf("pinned %s: %v", pin, err)
		}
		_ = resp.Body.Close()
	}

	other := strings.Repeat("AB:", 31) + "AB"
	c, err = Client(Options{Pin: other})
	if err != nil {
		t.Fatal(err)
	}
	resp, err = c.Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrPin) || !strings.Contains(err.Error(), fp) {
		t.Errorf("another fingerprint: %v", err)
	}
	if _, err := Client(Options{Pin: "not hex"}); err == nil {
		t.Error("a pin that is no fingerprint")
	}
}

func TestPinOrNames(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	// The server's certificate is no longer the one pinned, as when a panel
	// serves the one JEC deployed: trusted when vouched for and for a name
	// given (httptest's is for example.com).
	vouchers := x509.NewCertPool()
	vouchers.AddCert(srv.Certificate())
	other := strings.Repeat("AB:", 31) + "AB"
	for _, tt := range []struct {
		roots *x509.CertPool
		names []string
		ok    bool
	}{
		{vouchers, []string{"jec.example", "example.com"}, true},
		{vouchers, []string{"jec.example"}, false},
		{nil, []string{"example.com"}, false}, // no CA of the system vouches for it
		{vouchers, nil, false},
	} {
		c, err := Client(Options{Pin: other, Roots: tt.roots, Names: tt.names})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := c.Get(srv.URL)
		if err == nil {
			_ = resp.Body.Close()
		}
		if (err == nil) != tt.ok || err != nil && !errors.Is(err, ErrPin) {
			t.Errorf("names %v, roots %v: %v", tt.names, tt.roots != nil, err)
		}
	}
}

func TestNamesAlone(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	vouchers := x509.NewCertPool()
	vouchers.AddCert(srv.Certificate())
	// Reached at 127.0.0.1, a certificate for example.com is trusted for it.
	c, err := Client(Options{Roots: vouchers, Names: []string{"example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("vouched for a name: %v", err)
	}
	_ = resp.Body.Close()
	// For no name given, it is one that does not verify, with its fingerprint.
	if c, err = Client(Options{Roots: vouchers, Names: []string{"jec.example"}}); err != nil {
		t.Fatal(err)
	}
	resp, err = c.Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
	}
	if _, ok := Fingerprint(err); !ok || errors.Is(err, ErrPin) {
		t.Errorf("vouched for no name given: %v", err)
	}
}

func TestRoots(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	c, err := Client(Options{Roots: roots})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("a server of the CA trusted: %v", err)
	}
	_ = resp.Body.Close()

	// Only the roots given are trusted, not those of the system.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "another CA"}, IsCA: true, BasicConstraintsValid: true,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	others := x509.NewCertPool()
	others.AddCert(ca)
	if c, err = Client(Options{Roots: others}); err != nil {
		t.Fatal(err)
	}
	resp, err = c.Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
	}
	if _, ok := Fingerprint(err); !ok {
		t.Errorf("a server of another CA: %v", err)
	}
}

func TestPrivateOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	c, err := Client(Options{PrivateOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("a loopback address: %v", err)
	}
	_ = resp.Body.Close()
	resp, err = c.Get("http://93.184.215.14:9/")
	if err == nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrPublic) {
		t.Errorf("a public address: %v", err)
	}
	if _, err := Client(Options{PrivateOnly: true, PublicOnly: true}); err == nil {
		t.Error("both PrivateOnly and PublicOnly")
	}
}

func TestClientFor(t *testing.T) {
	// A proxy that answers every request itself, and tells which it got.
	var got string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Method + " " + r.URL.String()
		_, _ = io.WriteString(w, "through the proxy")
	}))
	t.Cleanup(proxy.Close)

	// Through it, a client that would keep off private networks reaches
	// the proxy, which dials for it.
	c, err := ClientFor(Options{PublicOnly: true}, map[string]string{ProxyKey: proxy.URL})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get("http://service.example/hook")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "through the proxy" || got != "GET http://service.example/hook" {
		t.Errorf("the proxy got %q and answered %q", got, body)
	}

	// Without one, the client is as asked.
	c, err = ClientFor(Options{PublicOnly: true}, map[string]string{ProxyKey: " "})
	if err != nil {
		t.Fatal(err)
	}
	resp, err = c.Get(proxy.URL)
	if err == nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrNotPublic) {
		t.Errorf("without a proxy, a private address: %v", err)
	}

	_, err = ClientFor(Options{}, map[string]string{ProxyKey: "127.0.0.1:7890"})
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.HttpxErrorProxy || !retry.IsPermanent(err) {
		t.Errorf("a proxy without a scheme: %v", err)
	}
}
