// Package contracts holds what modules agree on without importing each
// other (PLAN.md 4.2): the event types they publish with their payloads,
// and later the provider interfaces of PLAN.md 5.1. A module that
// publishes an event documents it here; the modules that react to it read
// the payload with these types.
package contracts

import "time"

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
	CertificateID int64     `json:"certificateId"`
	Domains       []string  `json:"domains"` // the primary domain first
	VersionID     int64     `json:"versionId,omitempty"`
	NotAfter      time.Time `json:"notAfter,omitzero"` // the end of the version
	Expiry        Expiry    `json:"expiry,omitempty"`
}
