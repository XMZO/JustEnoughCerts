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
	// issued by its source or uploaded. Payload CertificateEvent with
	// VersionID, Fingerprint and NotAfter set.
	CertificateIssued = "certificate.issued"
	// CertificateCurrent: the version a certificate uses, told again when a
	// module asks with CertificatesAnnounce. Payload as CertificateIssued.
	CertificateCurrent = "certificate.current"
	// CertificateExpiring: the current version entered the renewal window,
	// has a week or less left, or expired; see Expiry. Payload
	// CertificateEvent with Expiry set.
	CertificateExpiring = "certificate.expiring"
	// CertificateRevoked: the CA of the current version revoked it, as its
	// OCSP responder or CRL told; one with a source is renewed at once.
	// Payload CertificateEvent with VersionID, Fingerprint, NotAfter and
	// Revocation set.
	CertificateRevoked = "certificate.revoked"
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
	// certificate's targets. Payload CertificateEvent with VersionID,
	// Fingerprint and Deployment set.
	CertificateDeployed = "certificate.deployed"
	// CertificateDeployFailed: an attempt to install a version at a target
	// failed. Payload CertificateEvent with VersionID and Deployment set.
	CertificateDeployFailed = "certificate.deploy_failed"
)

// CertificatesDigest is the event of how the certificates stand, told
// once a week, on Monday from nine in the panel's time zone. Its subject is
// the ISO week, as 2026-W43; payload Digest.
const CertificatesDigest = "certificates.digest"

// Digest is the payload of CertificatesDigest.
type Digest struct {
	// Total counts the certificates; OK those in good standing.
	Total int `json:"total"`
	OK    int `json:"ok"`
	// Attention lists those that need someone: whose issuance or deployment
	// failed, that expired, or that end soon with no source to renew them.
	Attention []DigestItem `json:"attention"`
	// Renewals lists those whose renewal starts within a week.
	Renewals []DigestItem `json:"renewals"`
}

// DigestItem is a certificate in a Digest.
type DigestItem struct {
	CertificateID int64     `json:"certificateId"`
	Domain        string    `json:"domain"` // the primary one
	State         string    `json:"state"`  // as the API of the certs module tells it
	NotAfter      time.Time `json:"notAfter,omitzero"`
	RenewAt       time.Time `json:"renewAt,omitzero"`
}

// CertificatesAnnounce asks the certs module to publish CertificateCurrent
// for each certificate with a version, as a module that keeps track of them
// does when it starts with nothing to go on. It has no subject or payload.
const CertificatesAnnounce = "certificates.announce"

// Events of the monitor module. Their subject is the certificate ID, in
// decimal.
const (
	// CertificateServedOld: a domain of a certificate still serves an older
	// certificate than the version in use, a while after that one became
	// current, as when a CDN did not take it. Payload CertificateEvent with
	// VersionID, Fingerprint, NotAfter and Check set.
	CertificateServedOld = "certificate.served_old"
	// CertificateChainIncomplete: a domain of a certificate serves the
	// version in use without the intermediates clients need, a while after
	// it became current. Payload CertificateEvent with VersionID,
	// Fingerprint, NotAfter and Check set.
	CertificateChainIncomplete = "certificate.chain_incomplete"
)

// Events of the sites module, which watches the certificates of sites JEC
// does not manage. Their subject is the site's ID, in decimal.
const (
	// SiteExpiring: the certificate a site serves ends within three weeks,
	// within a week, or ended; see Expiry. Payload SiteEvent.
	SiteExpiring = "site.expiring"
	// DomainExpiring: the registration of a domain ends within 30 days,
	// within a week, or ended, as its registry tells through RDAP; see
	// Expiry. The subject is the domain. Payload DomainEvent.
	DomainExpiring = "domain.expiring"
	// CertificateSpotted: a certificate transparency log has a certificate
	// for names under a domain JEC holds certificates under, with a key
	// JEC does not hold, as one issued to someone else. The subject is the
	// log search's ID of the certificate. Payload SpottedEvent.
	CertificateSpotted = "domain.certificate_spotted"
)

// Events of the accounts of the kernel.
const (
	// AccountSignedIn: someone signed in to JEC, with a password, through
	// single sign-on or at the setup. The subject is the username. Payload
	// SignInEvent.
	AccountSignedIn = "account.signed_in"
	// AccountLocked: a client failed to sign in too many times in a row
	// and is locked out for a while. The subject is the client's address.
	// Payload SignInEvent, with Minutes.
	AccountLocked = "account.locked"
)

// SignInEvent is the payload of the events of the accounts.
type SignInEvent struct {
	Username  string `json:"username,omitempty"` // the account, or the name tried
	Client    string `json:"client"`             // the client's address
	UserAgent string `json:"userAgent,omitempty"`
	Device    string `json:"device,omitempty"`  // the browser and system, as "Chrome 141 · Windows"
	Via       string `json:"via,omitempty"`     // password, sso or setup
	Minutes   int    `json:"minutes,omitempty"` // how long a client is locked out
}

// SpottedEvent is the payload of CertificateSpotted.
type SpottedEvent struct {
	Domain    string    `json:"domain"` // the registered domain, as example.com
	Names     []string  `json:"names"`
	Issuer    string    `json:"issuer"`
	NotBefore time.Time `json:"notBefore"`
	NotAfter  time.Time `json:"notAfter"`
	SHA256    string    `json:"sha256"` // of the certificate, lowercase hex
}

// SiteEvent is the payload of the events of the sites module.
type SiteEvent struct {
	SiteID   int64     `json:"siteId"`
	Host     string    `json:"host"`
	Port     int       `json:"port"`
	Name     string    `json:"name,omitempty"` // what people call it, if anything
	NotAfter time.Time `json:"notAfter"`
	Expiry   Expiry    `json:"expiry"`
	Issuer   string    `json:"issuer"`
}

// DomainEvent is the payload of DomainExpiring.
type DomainEvent struct {
	Domain       string    `json:"domain"` // the registered domain, as example.com
	ExpiresAt    time.Time `json:"expiresAt"`
	Expiry       Expiry    `json:"expiry"`
	Registrar    string    `json:"registrar,omitempty"`
	Certificates []int64   `json:"certificates"` // those with names under the domain
}

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
	Fingerprint   string      `json:"fingerprint,omitempty"` // SHA-256 of the version's leaf, lowercase hex
	NotAfter      time.Time   `json:"notAfter,omitzero"`     // the end of the version
	Expiry        Expiry      `json:"expiry,omitempty"`
	Username      string      `json:"username,omitempty"` // who did it, where a person did
	Issuance      *Issuance   `json:"issuance,omitempty"`
	Deployment    *Deployment `json:"deployment,omitempty"`
	Check         *Check      `json:"check,omitempty"`
	Revocation    *Revocation `json:"revocation,omitempty"`
}

// RevocationReasonText names a reason code of RFC 5280.
func RevocationReasonText(code int) i18n.Key {
	switch code {
	case 1:
		return i18n.CertsCaRevokedReasonKeyCompromise
	case 2, 10:
		return i18n.CertsCaRevokedReasonCaCompromise
	case 3:
		return i18n.CertsCaRevokedReasonAffiliationChanged
	case 4:
		return i18n.CertsCaRevokedReasonSuperseded
	case 5:
		return i18n.CertsCaRevokedReasonCessationOfOperation
	case 6:
		return i18n.CertsCaRevokedReasonCertificateHold
	case 9:
		return i18n.CertsCaRevokedReasonPrivilegeWithdrawn
	}
	return i18n.CertsCaRevokedReasonUnspecified
}

// Revocation tells that a CA revoked a version.
type Revocation struct {
	At       time.Time `json:"at"`
	Reason   int       `json:"reason"`   // the reason code of RFC 5280: 1 key compromise, 4 superseded, …
	Renewing bool      `json:"renewing"` // the certificate has a source, which issues a new version at once
}

// Check tells what a domain of a certificate served when the monitor
// connected to it.
type Check struct {
	Domain string `json:"domain"`
	// View is "cn" or "global" when the domain was checked as visitors from
	// mainland China or from elsewhere reach it, its DNS sending them apart.
	View string `json:"view,omitempty"`
	// Fingerprint and NotAfter are those of the certificate it served.
	Fingerprint string    `json:"fingerprint"`
	NotAfter    time.Time `json:"notAfter,omitzero"`
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
