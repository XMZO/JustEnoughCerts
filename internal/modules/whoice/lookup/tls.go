package lookup

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// legacySuites are TLS 1.2 suites with RSA key exchange, which Go no
// longer offers: some registries serve nothing else (rdap.nic.cat only
// AES128-SHA). What they serve is public and read-only, and their
// certificates are still verified. RC4 and 3DES stay out.
var legacySuites = []uint16{
	tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_RSA_WITH_AES_128_CBC_SHA,
	tls.TLS_RSA_WITH_AES_256_CBC_SHA,
}

// forRegistries sets up t for registries' servers: the legacy suites, and
// a chain verified in full against the system's roots and the server's
// name after fetching through fetch the intermediates a server leaves out,
// from the URLs its certificates name (AIA), as browsers do: some send
// their certificate alone (api.nic.dz, nic.pa).
func forRegistries(t *http.Transport, fetch *http.Client) {
	suites := make([]uint16, 0, len(tls.CipherSuites())+len(legacySuites))
	for _, s := range tls.CipherSuites() {
		suites = append(suites, s.ID)
	}
	t.TLSClientConfig = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		CipherSuites: append(suites, legacySuites...),
		// VerifyConnection checks the chain and the name itself, so that
		// it can add the intermediates fetched.
		InsecureSkipVerify: true, //nolint:gosec // see VerifyConnection
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("tls: the server sent no certificate")
			}
			if cs.ServerName == "" {
				return errors.New("tls: no server name to verify")
			}
			b := cryptox.Bundle{Leaf: cs.PeerCertificates[0], Chain: cs.PeerCertificates[1:]}
			opts := x509.VerifyOptions{DNSName: cs.ServerName, Intermediates: pool(b.Chain)}
			_, err := b.Leaf.Verify(opts)
			if unknown := (x509.UnknownAuthorityError{}); err == nil || !errors.As(err, &unknown) {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if cryptox.CompleteChain(ctx, fetch, &b) != nil {
				return err
			}
			opts.Intermediates = pool(b.Chain)
			_, err = b.Leaf.Verify(opts)
			return err
		},
	}
}

func pool(certs []*x509.Certificate) *x509.CertPool {
	p := x509.NewCertPool()
	for _, c := range certs {
		p.AddCert(c)
	}
	return p
}
