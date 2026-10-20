package httpserver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
)

// pair makes the PEM of a self-signed certificate named name and of its
// key.
func pair(t *testing.T, name string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	k, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: k})
}

func write(t *testing.T, path string, data []byte, at time.Time) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestCertFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	c := &certFiles{certPath: filepath.Join(dir, "cert.pem"), keyPath: filepath.Join(dir, "key.pem"), log: discard, now: func() time.Time { return now }}
	served := func() string {
		t.Helper()
		cert, err := c.get(nil)
		if err != nil {
			t.Fatal(err)
		}
		return cert.Leaf.Subject.CommonName
	}

	// No files yet: a certificate of its own stands in.
	if err := c.load(); err != nil {
		t.Fatal(err)
	}
	if got := served(); got != "Just Enough Certs" {
		t.Fatalf("before the files: %q", got)
	}

	// The files come, and are taken once it is time to look again.
	cert1, key1 := pair(t, "one.example")
	write(t, c.certPath, cert1, now.Add(-time.Minute))
	write(t, c.keyPath, key1, now.Add(-time.Minute))
	if got := served(); got != "Just Enough Certs" {
		t.Fatalf("looked again too soon: %q", got)
	}
	now = now.Add(recheck)
	if got := served(); got != "one.example" {
		t.Fatalf("after the files came: %q", got)
	}

	// A certificate written before its key does not make a pair, and the
	// one served stays until the key comes too.
	cert2, key2 := pair(t, "two.example")
	write(t, c.certPath, cert2, now)
	now = now.Add(recheck)
	if got := served(); got != "one.example" {
		t.Fatalf("with half a pair: %q", got)
	}
	write(t, c.keyPath, key2, now)
	now = now.Add(recheck)
	if got := served(); got != "two.example" {
		t.Fatalf("with the new pair: %q", got)
	}

	// Files that are there but hold no certificate fail at the start.
	write(t, c.certPath, []byte("not a certificate"), now)
	if _, err := TLSConfig(c.certPath, c.keyPath, discard); err == nil {
		t.Error("files of no certificate loaded")
	}
}

func TestServeTLS(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.TLSCert, cfg.TLSKey = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	certPEM, keyPEM := pair(t, "panel.example")
	write(t, cfg.TLSCert, certPEM, time.Now())
	write(t, cfg.TLSKey, keyPEM, time.Now())
	s := newServer(t, cfg)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve(t.Context(), ln) }()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, ForceAttemptHTTP2: true}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+ln.Addr().String()+HealthPath, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.TLS == nil || resp.TLS.PeerCertificates[0].Subject.CommonName != "panel.example" {
		t.Errorf("over HTTPS: %s, %+v", resp.Status, resp.TLS)
	}
	if resp.ProtoMajor != 2 {
		t.Errorf("over %s, want HTTP/2", resp.Proto)
	}
}
