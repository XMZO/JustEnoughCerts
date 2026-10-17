package contracts

import (
	"context"
	"time"
)

// Challenge is an ACME domain validation (RFC 8555 section 8), as a Solver
// completes it: for DNS-01, a TXT record at _acme-challenge.<Domain>, with
// a value derived from KeyAuth.
type Challenge struct {
	// Domain is validated without the "*." of a wildcard.
	Domain  string
	Token   string
	KeyAuth string
}

// Solver proves control of domains for ACME: it presents a challenge's
// answer where the CA looks for it and removes it afterwards. One Solver
// serves one issuance, from the first Present to the last CleanUp; Close
// releases it. The providers of kind "solver" make Solvers.
type Solver interface {
	Present(ctx context.Context, ch Challenge) error
	CleanUp(ctx context.Context, ch Challenge) error
	// Wait says how long an answer may take to become visible, and how
	// often to look for it.
	Wait() (timeout, interval time.Duration)
	Close() error
}
