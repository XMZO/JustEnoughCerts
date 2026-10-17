package contracts

import (
	"context"
	"path"
	"strings"
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
// releases it. The providers of kind "solver" whose challenge is "dns" make
// Solvers.
type Solver interface {
	Present(ctx context.Context, ch Challenge) error
	CleanUp(ctx context.Context, ch Challenge) error
	// Wait says how long an answer may take to become visible, and how
	// often to look for it.
	Wait() (timeout, interval time.Duration)
	Close() error
}

// File is what a domain's web server must serve to prove control of the
// domain: http://<Domain><Path> answers Content, as for ACME's http-01
// (RFC 8555 section 8.3) or a CA's file validation.
type File struct {
	Domain string
	// Path is the URL path, such as /.well-known/acme-challenge/<token>.
	Path    string
	Content []byte
}

// WellKnown is where the files of validations live (RFC 8615). Solvers put
// no file elsewhere, so that no source can replace a page of a website.
const WellKnown = "/.well-known/"

// Valid reports whether f may be put on a web server: its Path is clean,
// below WellKnown, and made of letters, digits and -._~ only.
func (f File) Valid() bool {
	if !strings.HasPrefix(f.Path, WellKnown) || len(f.Path) == len(WellKnown) || path.Clean(f.Path) != f.Path {
		return false
	}
	for _, r := range f.Path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("-._~/", r):
		default:
			return false
		}
	}
	return true
}

// FileSolver proves control of domains over HTTP: it puts files where the
// domains' web servers serve them and removes them afterwards. One
// FileSolver serves one issuance; Close releases it. The providers of kind
// "solver" whose challenge is "http" make FileSolvers.
type FileSolver interface {
	PutFile(ctx context.Context, f File) error
	RemoveFile(ctx context.Context, f File) error
	Close() error
}
