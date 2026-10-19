package contracts

import (
	"context"
	"crypto"
	"crypto/x509"
	"errors"
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
	// it: whichever the certificate's solver is. Records is the solver too,
	// when it adds records of any type, for sources that ask for them.
	Solver  Solver
	Files   FileSolver
	Records RecordSolver
	// State is what the source kept when the last attempt of this issuance
	// ended in a Wait; nil before that.
	State []byte
	// Replaces is the leaf of the version this issuance renews, when the
	// same source issued it: an ACME order says which certificate it
	// replaces (RFC 9773). Nil otherwise.
	Replaces *x509.Certificate
	// Log receives the steps of the issuance.
	Log func(LogLine)
}

// RenewalAdvisor is a Source whose CA suggests when to renew the
// certificates it issued: ACME Renewal Information (RFC 9773). A CA moves
// the window earlier when it is going to revoke a certificate, as in a
// mass revocation.
type RenewalAdvisor interface {
	// RenewalInfo asks about leaf, which the source issued. It returns
	// ErrNoRenewalInfo when the CA makes no suggestions.
	RenewalInfo(ctx context.Context, leaf *x509.Certificate) (RenewalInfo, error)
}

// RenewalInfo is what a CA suggests about renewing a certificate.
type RenewalInfo struct {
	// Start and End bound the window the CA suggests renewing in.
	Start, End time.Time
	// ExplanationURL is a page of the CA that says why, as when it is going
	// to revoke the certificate; empty for none.
	ExplanationURL string
	// RetryAfter is when to ask again, as the CA asks it; zero when it
	// does not say.
	RetryAfter time.Duration
}

// ErrNoRenewalInfo is the error of a RenewalAdvisor whose CA makes no
// suggestions about renewing.
var ErrNoRenewalInfo = errors.New("contracts: the CA makes no suggestions about renewing")

// Revoker is a Source that can have the certificates it issued revoked, as
// an ACME account can (RFC 8555 7.6). Its type sets providers.Type's
// Revocable.
type Revoker interface {
	// Revoke asks the CA to revoke leaf, which the source issued, for
	// reason. A certificate the CA has revoked already is no error.
	Revoke(ctx context.Context, leaf *x509.Certificate, reason RevocationReason) error
}

// RevocationReason is why a certificate is revoked: a reason code of RFC
// 5280 5.3.1 that CAs take from subscribers.
type RevocationReason int

// The reasons a subscriber may give.
const (
	ReasonUnspecified          RevocationReason = 0
	ReasonKeyCompromise        RevocationReason = 1
	ReasonSuperseded           RevocationReason = 4
	ReasonCessationOfOperation RevocationReason = 5
)

// String names r as the API and plugins do, such as key_compromise.
func (r RevocationReason) String() string {
	switch r {
	case ReasonKeyCompromise:
		return "key_compromise"
	case ReasonSuperseded:
		return "superseded"
	case ReasonCessationOfOperation:
		return "cessation_of_operation"
	}
	return "unspecified"
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
	// State is what the source kept when the last attempt of this
	// issuance ended in a Wait, as for IssueRequest; nil before that.
	State []byte
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

// Restart marks the error of a Source whose order is spent, as one the CA
// refused or one deleted at the vendor: the issuance fails as err says,
// or retries if err allows, but starts afresh either way, with a new key
// and none of the State the source kept, nor will a later issuance take
// it over. It returns nil for a nil err.
func Restart(err error) error {
	if err == nil {
		return nil
	}
	return &restartError{err}
}

type restartError struct{ err error }

func (e *restartError) Error() string { return e.err.Error() }
func (e *restartError) Unwrap() error { return e.err }

// IsRestart reports whether err or an error it wraps was marked by
// Restart.
func IsRestart(err error) bool {
	var r *restartError
	return errors.As(err, &r)
}

// KeepState marks the error of an attempt that got somewhere before it
// failed, as an order the vendor took: the issuance fails or retries as
// err says, and keeps state in place of what the source kept before, as
// after a Wait. Restart wins over it. It returns nil for a nil err.
func KeepState(err error, state []byte) error {
	if err == nil {
		return nil
	}
	return &keptError{err, state}
}

type keptError struct {
	err   error
	state []byte
}

func (e *keptError) Error() string { return e.err.Error() }
func (e *keptError) Unwrap() error { return e.err }

// KeptState returns the state that err, or an error it wraps, was marked
// with by KeepState, and nil when it was not.
func KeptState(err error) []byte {
	var k *keptError
	if errors.As(err, &k) {
		return k.state
	}
	return nil
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
