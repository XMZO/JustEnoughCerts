package contracts

import (
	"context"
	"crypto"
	"crypto/x509"
	"time"

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

// Step is a stage of an issuance, which the progress view lists in this
// order. A source takes the stages it needs: a CA that does not validate
// domains has no challenge, validate or cleanup.
type Step string

// Steps.
const (
	StepKey       Step = "key"       // making the private key
	StepOrder     Step = "order"     // asking the source for the certificate
	StepChallenge Step = "challenge" // publishing the answers to the domain validation
	StepValidate  Step = "validate"  // waiting for the answers to show and the CA to check them
	StepCleanup   Step = "cleanup"   // removing the answers
	StepDownload  Step = "download"  // getting and storing the certificate
)

// Steps lists the steps in order.
var Steps = []Step{StepKey, StepOrder, StepChallenge, StepValidate, StepCleanup, StepDownload}

// LogLine is a line of an issuance's log, as the UI shows it: a message key
// with its arguments, and the step it belongs to, if any. Detail is the raw
// text behind it, such as a CA's error, for the full log only.
type LogLine struct {
	Level  Level
	Step   Step
	Key    i18n.Key
	Params map[string]any
	Detail string
}

// IssueRequest asks a Source for a certificate.
type IssueRequest struct {
	// Domains to cover, the primary first.
	Domains []string
	// Key is the new certificate's private key, which the caller made and
	// keeps. Every attempt of an issuance gets the same key.
	Key crypto.Signer
	// Solver or Files proves control of the domains, for sources that need
	// it: whichever the certificate's solver is.
	Solver Solver
	Files  FileSolver
	// State is what the source kept when the last attempt of this issuance
	// ended in a Wait; nil before that.
	State []byte
	// Log receives the steps of the issuance.
	Log func(LogLine)
}

// Source gets certificates, such as an ACME account. The providers of kind
// "source" make Sources.
type Source interface {
	// Issue returns the certificate for req.Key and its chain, leaf first,
	// or a *Wait when the certificate is not ready yet.
	Issue(ctx context.Context, req IssueRequest) ([]*x509.Certificate, error)
	Close() error
}

// KeySource is a source whose certificates come with their own private
// key, made elsewhere: the files another ACME client, such as certbot or
// acme.sh, keeps renewing. JEC takes them in rather than issuing, so the
// providers of kind "source" make either a Source or a KeySource.
type KeySource interface {
	// Collect returns the certificate, its chain and its key, or a *Wait
	// while what it finds is no newer than the version in use.
	Collect(ctx context.Context, req CollectRequest) (*Collected, error)
	Close() error
}

// CollectRequest asks a KeySource for a certificate.
type CollectRequest struct {
	// Domains to cover, the primary first.
	Domains []string
	// Current is the SHA-256 of the leaf of the version in use, in
	// lowercase hex, and CurrentEnd when it ends; empty and zero before
	// the first version.
	Current    string
	CurrentEnd time.Time
	// Log receives the steps of the issuance.
	Log func(LogLine)
}

// Collected is what a KeySource found.
type Collected struct {
	// Chain is the certificate and its intermediates, leaf first.
	Chain []*x509.Certificate
	Key   crypto.Signer
}

// Wait is the error of a Source whose certificate takes longer than an
// attempt, as when a CA reviews an order, or a vendor has yet to make the
// next certificate of a subscription. The issuance keeps its key and the
// sealed State, and asks the source again after After, as many times as it
// takes: waiting is not failing, so the source decides when to give up.
type Wait struct {
	State []byte
	After time.Duration
}

func (w *Wait) Error() string { return "the certificate is not ready yet" }

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
