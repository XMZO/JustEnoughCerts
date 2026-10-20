package httpserver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math/big"
	"os"
	"sync"
	"time"
)

// recheck is how often, at most, the files of the certificate are looked
// at again for a new one.
const recheck = 10 * time.Second

// certFiles serves the certificate in a pair of PEM files and takes it
// again once they change, as when a deploy target of JEC writes a new
// version there. Until both files are there, a self-signed certificate of
// its own stands in, so that the certificate JEC serves can be one JEC
// issues.
type certFiles struct {
	certPath, keyPath string
	log               *slog.Logger
	now               func() time.Time

	mu      sync.Mutex
	cert    *tls.Certificate
	stamp   string // of the files the certificate came from
	checked time.Time
	failed  string // the stamp of files that did not load, logged once
}

// TLSConfig returns the TLS configuration that serves the certificate in
// the files certPath and keyPath. It fails when they are there but do not
// hold a certificate and its key.
func TLSConfig(certPath, keyPath string, log *slog.Logger) (*tls.Config, error) {
	c := &certFiles{certPath: certPath, keyPath: keyPath, log: log, now: time.Now}
	if err := c.load(); err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: c.get}, nil
}

// stampOf tells the files apart by their sizes and modification times;
// empty when one is not there.
func (c *certFiles) stampOf() (string, error) {
	var stamp string
	for _, p := range []string{c.certPath, c.keyPath} {
		fi, err := os.Stat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		stamp += fmt.Sprintf("%d.%d;", fi.Size(), fi.ModTime().UnixNano())
	}
	return stamp, nil
}

// load takes the certificate at the start.
func (c *certFiles) load() error {
	stamp, err := c.stampOf()
	if err != nil {
		return fmt.Errorf("tls: %w", err)
	}
	c.checked = c.now()
	if stamp == "" {
		cert, err := selfSigned(c.now())
		if err != nil {
			return err
		}
		c.cert = cert
		c.log.Warn("the TLS certificate files are not there yet: serving a self-signed certificate until they are", "cert", c.certPath, "key", c.keyPath)
		return nil
	}
	cert, err := tls.LoadX509KeyPair(c.certPath, c.keyPath)
	if err != nil {
		return fmt.Errorf("tls: %w", err)
	}
	c.cert, c.stamp = &cert, stamp
	c.log.Info("serving HTTPS", "cert", c.certPath, "not_after", cert.Leaf.NotAfter)
	return nil
}

func (c *certFiles) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now := c.now(); now.Sub(c.checked) >= recheck {
		c.checked = now
		c.reload()
	}
	return c.cert, nil
}

// reload takes the certificate again if the files changed. Files that do
// not make a pair, as while a target writes one before the other, leave
// the certificate as it was until they change again.
func (c *certFiles) reload() {
	stamp, err := c.stampOf()
	if err != nil || stamp == "" || stamp == c.stamp || stamp == c.failed {
		return
	}
	cert, err := tls.LoadX509KeyPair(c.certPath, c.keyPath)
	if err != nil {
		c.failed = stamp
		c.log.Warn("the TLS certificate files changed but do not load: keeping the certificate served", "err", err)
		return
	}
	c.cert, c.stamp, c.failed = &cert, stamp, ""
	c.log.Info("took the new TLS certificate", "cert", c.certPath, "not_after", cert.Leaf.NotAfter)
}

// selfSigned makes the certificate that stands in for the files.
func selfSigned(now time.Time) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Just Enough Certs"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(0, 0, 30),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, nil
}
