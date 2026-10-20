package monitor

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// TestLiveChain connects to real sites, when JEC_TEST_CHAIN is set: the
// one badssl.com sets up without its intermediate, and one set up right.
func TestLiveChain(t *testing.T) {
	if os.Getenv("JEC_TEST_CHAIN") == "" {
		t.Skip("JEC_TEST_CHAIN not set")
	}
	client, err := httpx.Client(httpx.Options{PublicOnly: true, Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	m := &Module{now: time.Now, issuers: func(ctx context.Context, b *cryptox.Bundle) error { return cryptox.CompleteChain(ctx, client, b) }}
	for domain, want := range map[string]bool{"incomplete-chain.badssl.com": true, "badssl.com": false, "getjec.dev": false} {
		p, err := served(t.Context(), domain, "", defaultPort)
		if err != nil {
			t.Fatal(err)
		}
		if got := m.incomplete(t.Context(), domain, p.certs); got != want {
			t.Errorf("%s: incomplete %v, want %v (%d sent)", domain, got, want, len(p.certs))
		}
	}
}

// TestLivePorts connects to real mail servers: IMAP over TLS on 993, and
// SMTP with STARTTLS on 587. CI does not run it: JEC_TEST_PORTS turns it
// on.
func TestLivePorts(t *testing.T) {
	if os.Getenv("JEC_TEST_PORTS") == "" {
		t.Skip("JEC_TEST_PORTS not set")
	}
	for _, c := range []struct {
		host string
		port int
	}{{"imap.gmail.com", 993}, {"smtp.gmail.com", 587}} {
		p, err := served(t.Context(), c.host, "", c.port)
		if err != nil {
			t.Fatalf("%s:%d: %v", c.host, c.port, err)
		}
		if err := p.certs[0].VerifyHostname(c.host); err != nil || versionName(p.version) == "" {
			t.Errorf("%s:%d served %v over %x: %v", c.host, c.port, p.certs[0].Subject, p.version, err)
		}
	}
	// A server of TLS 1.0, and one that took it off.
	if !takesLegacy(t.Context(), "tls-v1-0.badssl.com", "", 1010) {
		t.Error("tls-v1-0.badssl.com:1010 takes TLS 1.0, and was not seen to")
	}
	if takesLegacy(t.Context(), "github.com", "", 443) {
		t.Error("github.com takes no TLS 1.1, and was seen to")
	}
}
