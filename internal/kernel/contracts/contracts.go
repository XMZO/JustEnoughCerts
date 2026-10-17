// Package contracts holds what modules agree on without importing each
// other (PLAN.md 4.2): the event types they publish with their payloads,
// and the interfaces of the providers (PLAN.md 5.1). A module that
// publishes an event documents it here; the modules that react to it read
// the payload with these types.
package contracts

import (
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
)

// Events of the certs module. Their subject is the certificate ID, in
// decimal.
const (
	// CertificateIssued: a new version of a certificate became current, as
	// issued by its source or uploaded. Payload CertificateEvent.
	CertificateIssued = "certificate.issued"
	// CertificateExpiring: the current version entered the renewal window,
	// has a week or less left, or expired; see Expiry. Payload
	// CertificateEvent with Expiry set.
	CertificateExpiring = "certificate.expiring"
	// CertificateDeleted: a certificate and all its versions were deleted.
	// Payload CertificateEvent without a version.
	CertificateDeleted = "certificate.deleted"
	// CertificateKeyDownloaded: someone downloaded a version with its
	// private key. Payload CertificateEvent with Username set.
	CertificateKeyDownloaded = "certificate.key_downloaded"
	// CertificateIssueFailed: an attempt to get a new version from the
	// certificate's source failed. Payload CertificateEvent with Issuance
	// set; the current version, if any, is still in use.
	CertificateIssueFailed = "certificate.issue_failed"
	// CertificateDeployed: a version was installed at one of the
	// certificate's targets. Payload CertificateEvent with VersionID and
	// Deployment set.
	CertificateDeployed = "certificate.deployed"
	// CertificateDeployFailed: an attempt to install a version at a target
	// failed. Payload CertificateEvent with VersionID and Deployment set.
	CertificateDeployFailed = "certificate.deploy_failed"
)

// Expiry says how close a certificate is to its end, in increasing order of
// urgency.
type Expiry string

// Expiry levels.
const (
	ExpiryWindow  Expiry = "window"  // within the certificate's renewal window
	ExpiryUrgent  Expiry = "urgent"  // seven days or less left
	ExpiryExpired Expiry = "expired" // past its end
)

// CertificateEvent is the payload of the certificate events.
type CertificateEvent struct {
	CertificateID int64       `json:"certificateId"`
	Domains       []string    `json:"domains"` // the primary domain first
	VersionID     int64       `json:"versionId,omitempty"`
	NotAfter      time.Time   `json:"notAfter,omitzero"` // the end of the version
	Expiry        Expiry      `json:"expiry,omitempty"`
	Username      string      `json:"username,omitempty"` // who did it, where a person did
	Issuance      *Issuance   `json:"issuance,omitempty"`
	Deployment    *Deployment `json:"deployment,omitempty"`
}

// Issuance tells how an attempt to issue a certificate went.
type Issuance struct {
	ID      int64 `json:"id"`
	Attempt int   `json:"attempt"` // counting from 1
	// Error says why the attempt failed.
	Error Message `json:"error"`
	// Retry is when the next attempt starts; zero when no attempt follows
	// without a person starting it.
	Retry time.Time `json:"retry,omitzero"`
}

// Deployment tells how an attempt to install a version at a target went.
type Deployment struct {
	ID int64 `json:"id"`
	// Target is the provider instance of the target.
	Target  int64 `json:"target"`
	Attempt int   `json:"attempt"` // counting from 1
	// Error says why the attempt failed; empty when it did not.
	Error Message `json:"error,omitzero"`
	// Retry is when the next attempt starts, after a failure; zero when no
	// attempt follows without a person starting it.
	Retry time.Time `json:"retry,omitzero"`
}

// Message is a message key with its arguments, shown in the reader's
// language.
type Message struct {
	Key    i18n.Key       `json:"key"`
	Params map[string]any `json:"params,omitempty"`
}
