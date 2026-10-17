package contracts

import (
	"context"
	"crypto"
	"crypto/x509"
)

// DeployRequest asks a Target to install a certificate.
type DeployRequest struct {
	// CertificateID and Domains name the certificate, the primary domain
	// first; a target may use them to name what it stores.
	CertificateID int64
	Domains       []string
	// Chain is the certificate to install, the leaf first.
	Chain []*x509.Certificate
	Key   crypto.Signer
	// Previous holds what earlier deployments of this certificate left at
	// the same target, as their Deploy returned it, the newest first. A
	// target that keeps certificates side by side, as a CDN does, removes
	// them once the new one is in place, if it is set to.
	Previous []string
	// Log receives the steps of the deployment.
	Log func(LogLine)
}

// DeployResult says what a Target did.
type DeployResult struct {
	// Ref names what the target holds now, such as a certificate ID at a
	// CDN, for later deployments to replace; empty when there is nothing
	// to name, as for files that are overwritten.
	Ref string
	// Removed lists the refs of Previous that the target removed.
	Removed []string
}

// Target installs certificates where they are served: a CDN, a server, a
// web hook. A deployment may be tried again after a failure or a stop of
// JEC, so Deploy must cope with finding its own earlier work, such as a
// certificate it uploaded already. The providers of kind "target" make
// Targets.
type Target interface {
	Deploy(ctx context.Context, req DeployRequest) (DeployResult, error)
	Close() error
}
