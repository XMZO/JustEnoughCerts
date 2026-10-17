package contracts

import (
	"context"
	"crypto"
	"crypto/x509"

	"github.com/XMZO/JustEnoughCerts/i18n"
)

// Level is how an issuance log line reads (docs/design/system/components/LogView.md).
type Level string

// Levels.
const (
	Info  Level = "info"
	OK    Level = "ok"
	Warn  Level = "warn"
	Error Level = "error"
)

// LogLine is a step of an issuance, as the UI shows it: a message key with
// its arguments. Detail is the raw text behind it, such as a CA's error,
// for the full log only.
type LogLine struct {
	Level  Level
	Key    i18n.Key
	Params map[string]any
	Detail string
}

// IssueRequest asks a Source for a certificate.
type IssueRequest struct {
	// Domains to cover, the primary first.
	Domains []string
	// Key is the new certificate's private key, which the caller made and
	// keeps.
	Key crypto.Signer
	// Solver proves control of the domains, for sources that need it.
	Solver Solver
	// Log receives the steps of the issuance.
	Log func(LogLine)
}

// Source gets certificates, such as an ACME account. The providers of kind
// "source" make Sources.
type Source interface {
	// Issue returns the certificate for req.Key and its chain, leaf first.
	Issue(ctx context.Context, req IssueRequest) ([]*x509.Certificate, error)
	Close() error
}

// Problem is an error a provider can explain: a message key with its
// arguments, which the UI shows in the viewer's language, and the error
// behind it for the full log.
type Problem struct {
	Key    i18n.Key
	Params map[string]any
	Err    error
}

func (p *Problem) Error() string {
	if p.Err != nil {
		return string(p.Key) + ": " + p.Err.Error()
	}
	return string(p.Key)
}

func (p *Problem) Unwrap() error { return p.Err }
