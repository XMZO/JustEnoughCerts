package caddy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestLive deploys to a real Caddy, when JEC_TEST_CADDY is the address of
// its admin API and JEC_TEST_CADDY_TLS that of a TLS listener of a site
// of example.test, and checks that the listener serves each version:
//
//	JEC_TEST_CADDY=http://127.0.0.1:12019 JEC_TEST_CADDY_TLS=127.0.0.1:18444 go test -run TestLive ./internal/modules/caddy/
func TestLive(t *testing.T) {
	url, addr := os.Getenv("JEC_TEST_CADDY"), os.Getenv("JEC_TEST_CADDY_TLS")
	if url == "" || addr == "" {
		t.Skip("JEC_TEST_CADDY and JEC_TEST_CADDY_TLS are not set")
	}
	a := openAdmin(t, url)
	if _, err := a.Test(t.Context()); err != nil {
		t.Fatal(err)
	}
	id := time.Now().UnixNano() % 1_000_000_000 // of this run alone
	t.Cleanup(func() {
		if err := a.do(context.Background(), http.MethodDelete, "/id/jec-"+strconv.FormatInt(id, 10), nil, nil); err != nil {
			t.Error(err)
		}
	})
	for version := range 2 {
		req := request(t, id)
		if _, err := a.Deploy(t.Context(), req); err != nil {
			t.Fatalf("version %d: %v", version, err)
		}
		var served *x509.Certificate
		for range 50 {
			if served = handshake(t, addr); served != nil && served.Equal(req.Chain[0]) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if served == nil || !served.Equal(req.Chain[0]) {
			t.Fatalf("version %d: Caddy serves %v", version, served)
		}
	}
}

// handshake returns the certificate the listener at addr serves for
// example.test.
func handshake(t *testing.T, addr string) *x509.Certificate {
	t.Helper()
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: &tls.Config{ServerName: "example.test", InsecureSkipVerify: true}}
	conn, err := d.DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Log(err)
		return nil
	}
	defer func() { _ = conn.Close() }()
	return conn.(*tls.Conn).ConnectionState().PeerCertificates[0]
}
