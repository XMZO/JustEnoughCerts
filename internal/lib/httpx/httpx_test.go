package httpx

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
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
