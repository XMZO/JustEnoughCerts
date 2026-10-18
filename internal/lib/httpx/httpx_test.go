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
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
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
