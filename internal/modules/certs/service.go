package certs

import (
	"cmp"
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// Defaults and limits of the renewal policy.
const (
	defaultRenewBeforeDays = 30
	urgentWithin           = 7 * 24 * time.Hour
	day                    = 24 * time.Hour
)

// Sources of certificates: uploads only, or a source provider.
const (
	sourceUpload   = "upload"
	sourceProvider = "provider"
)

// Origins of versions.
const (
	originUpload = "upload"
	originIssued = "issued"
)

// States of a certificate.
const (
	stateOK           = "ok"
	stateExpiring     = "expiring"
	stateExpired      = "expired"
	stateIssuing      = "issuing"
	stateDeploying    = "deploying"
	stateFailed       = "failed"
	stateDeployFailed = "deploy_failed"
	stateRevoked      = "revoked"  // its CA revoked the current version
	stateAwaiting     = "awaiting" // no version yet, but a CSR waiting for its certificate
)

// Errors of the service, which the API turns into responses.
var (
	errNotFound = errors.New("no such certificate")
	// errNoVersion: there is no version to deploy yet.
	errNoVersion = errors.New("the certificate has no version yet")
	// errNotNewer: the version to add ends before the current one.
	errNotNewer = errors.New("the certificate expires before the current version")
)

// duplicateError: the certificate was added before, to certificate.
type duplicateError struct{ certificate int64 }

func (e *duplicateError) Error() string {
	return fmt.Sprintf("the certificate is a version of certificate %d already", e.certificate)
}

// otherDomainsError: the version to add does not cover the primary domain.
type otherDomainsError struct{ domain string }

func (e *otherDomainsError) Error() string {
	return "the certificate does not cover " + e.domain
}

type service struct {
	db        *sql.DB
	events    *events.Scope
	secrets   *secrets.Scope
	providers *providers.Scope
	log       *slog.Logger
	now       func() time.Time
	// zone returns the time zone of the time renewals start: the one
	// chosen for JEC, or the server's.
	zone func() *time.Location

	// status asks CAs whether they revoked versions; nil to not ask.
	status *statusChecker
	// issuers fetches the intermediates an upload lacks; nil to not.
	issuers *http.Client

	issuing   *queue[claimed]
	deploying *queue[deployClaim]
	mu        sync.Mutex                    // guards running
	running   map[runKey]context.CancelFunc // the attempts under way
}

func newService(db *sql.DB, ev *events.Scope, box *secrets.Scope, prov *providers.Scope, log *slog.Logger, now func() time.Time) *service {
	s := &service{
		db: db, events: ev, secrets: box, providers: prov, log: log, now: now, zone: func() *time.Location { return time.Local },
		issuing: newQueue[claimed](maxRunning, attemptTimeout), deploying: newQueue[deployClaim](maxDeploying, deployTimeout),
		running: map[runKey]context.CancelFunc{},
	}
	q := s.issuing
	q.resume, q.claim, q.attempt = s.resume, s.claim, s.attempt
	q.key = func(c *claimed) runKey { return runKey{certificate: c.certificateID, part: "issue"} }
	d := s.deploying
	d.resume, d.claim, d.attempt = s.resumeDeployments, s.claimDeployment, s.attemptDeployment
	d.key = func(c *deployClaim) runKey {
		return runKey{certificate: c.certificateID, part: fmt.Sprintf("deploy/%d", c.target)}
	}
	return s
}

// Certificate is a certificate with its current version.
type Certificate struct {
	ID             int64
	Domains        []string
	Source         string
	SourceProvider int64 // with Source "provider"
	SolverProvider int64 // 0 for none
	// BackupProvider is the source that stands in for SourceProvider when
	// an attempt with it failed: every second attempt of an issuance goes
	// to it. 0 for none.
	BackupProvider  int64
	KeyType         string
	RenewBeforeDays int
	Current         *Version
	// Issuance is the latest issuance, if any.
	Issuance *Issuance
	// Targets are the provider instances the certificate is installed at,
	// in order; Deployments holds the latest deployment to each that had one.
	Targets     []int64
	Deployments []*Deployment
	CreatedAt   time.Time
	// Note is what people wrote about it, "" for nothing.
	Note string
	// ReuseKey keeps the private key of the version in use for renewals.
	ReuseKey bool
	// Paused stops the renewal: the certificate is issued only when
	// someone asks.
	Paused bool
	// DeployWindow holds the hours new versions are deployed in, as
	// "1-6"; "" for any time.
	DeployWindow string
	// NotifyTo are the notifiers its notes go to; empty for all.
	NotifyTo []int64
	// CSRAt is when the CSR waiting for its certificate was made; zero for
	// none.
	CSRAt time.Time
	// ReminderOn is the day of a reminder someone set, as 2027-05-01 in
	// the panel's time zone, "" for none; ReminderNote what it is about;
	// ReminderSent tells that it went out.
	ReminderOn, ReminderNote string
	ReminderSent             bool
}

// Version is a certificate issued or uploaded for a Certificate.
type Version struct {
	ID          int64
	Origin      string
	Names       []string
	Serial      string
	Fingerprint string
	Issuer      string
	KeyType     string
	NotBefore   time.Time
	NotAfter    time.Time
	Chain       string // PEM, the leaf first
	CreatedAt   time.Time
	// SourceProvider is the source that issued the version: 0 for an
	// uploaded one, or one issued before JEC kept it.
	SourceProvider int64
	// Renewal is what the CA suggests about renewing the version, nil
	// until it answered (renewal.go); RenewalCheck is when to ask again.
	Renewal      *RenewalWindow
	RenewalCheck time.Time
	// RevokedAt is when JEC had the version revoked, zero if it has not,
	// and RevokeReason the reason it gave (revoke.go).
	RevokedAt    time.Time
	RevokeReason string
	// CARevokedAt is when its CA revoked the version, as OCSP or the CRL
	// told, zero while it has not, with the reason code of RFC 5280;
	// StatusCheck is when to ask again (revocation.go).
	CARevokedAt    time.Time
	CARevokeReason int
	StatusCheck    time.Time
}

// RenewalWindow is the window a CA suggests renewing a version in, and the
// time JEC picked in it.
type RenewalWindow struct {
	Start, End, At time.Time
	// Explanation is a page of the CA that says why, or empty.
	Explanation string
}

// state tells how a certificate stands at now: being issued or failing to
// be issued since its current version came, failing to be deployed to a
// target or being deployed, or else by the life left in its current
// version.
func (c *Certificate) state(now time.Time) string {
	if is := c.Issuance; is != nil {
		switch {
		case is.State == issuanceRunning, is.State == issuanceWaiting, is.State == issuancePending && is.Error.Key == "":
			return stateIssuing
		case is.State == issuancePending, is.State == issuanceFailed && (c.Current == nil || is.CreatedAt.After(c.Current.CreatedAt)):
			return stateFailed
		}
	}
	deploying := false
	for _, d := range c.Deployments {
		switch {
		case d.State == issuanceFailed, d.State == issuancePending && d.Error.Key != "":
			return stateDeployFailed
		case d.scheduled(now):
			// waits for its window: the certificate is fine till then
		case d.State == issuanceRunning, d.State == issuancePending:
			deploying = true
		}
	}
	if deploying {
		return stateDeploying
	}
	if c.Current == nil {
		if !c.CSRAt.IsZero() {
			return stateAwaiting
		}
		return stateExpired
	}
	window, _ := limits(c.Current.NotBefore, c.Current.NotAfter, c.RenewBeforeDays)
	switch left := c.Current.NotAfter.Sub(now); {
	case left <= 0:
		return stateExpired
	case !c.Current.CARevokedAt.IsZero():
		return stateRevoked
	case left <= window:
		return stateExpiring
	}
	return stateOK
}

// completeChain fetches the intermediates of an upload that came without
// them, as a leaf copied from a CA's email does, which some clients could
// not trust once deployed. One that cannot be completed stays as it is.
func (s *service) completeChain(ctx context.Context, b *cryptox.Bundle) {
	if s.issuers == nil || len(b.Chain) > 0 {
		return
	}
	if err := cryptox.CompleteChain(ctx, s.issuers, b); err != nil {
		s.log.Warn("complete the chain of an upload", "domains", cryptox.Names(b.Leaf), "err", err)
	} else if len(b.Chain) > 0 {
		s.log.Info("completed the chain of an upload", "domains", cryptox.Names(b.Leaf), "intermediates", len(b.Chain))
	}
}

// add stores b as a new certificate, deployed to targets.
func (s *service) add(ctx context.Context, b *cryptox.Bundle, renewBeforeDays int, targets []int64) (int64, error) {
	s.completeChain(ctx, b)
	if renewBeforeDays == 0 {
		st, err := s.settings(ctx)
		if err != nil {
			return 0, err
		}
		renewBeforeDays = st.RenewBeforeDays
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.now()
	domains, _ := json.Marshal(cryptox.Names(b.Leaf))
	var id int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO certs_certificates (domains, source, key_type, renew_before_days, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
		string(domains), sourceUpload, cryptox.KeyType(b.Leaf.PublicKey), renewBeforeDays, now.UnixMilli(), now.UnixMilli()).Scan(&id)
	if err != nil {
		return 0, err
	}
	if err := s.setTargets(ctx, tx, &Certificate{ID: id}, targets); err != nil {
		return 0, err
	}
	if _, err := s.addVersion(ctx, tx, id, b, originUpload, 0); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.deploying.kick()
	return id, nil
}

// upload adds b as the new current version of certificate id.
func (s *service) upload(ctx context.Context, id int64, b *cryptox.Bundle) error {
	s.completeChain(ctx, b)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := s.load(ctx, tx, id)
	if err != nil {
		return err
	}
	names := cryptox.Names(b.Leaf)
	if !slices.Contains(names, c.Domains[0]) {
		return &otherDomainsError{c.Domains[0]}
	}
	if c.Current != nil && !b.Leaf.NotAfter.After(c.Current.NotAfter) {
		return errNotNewer
	}
	domains, _ := json.Marshal(names)
	_, err = tx.ExecContext(ctx, "UPDATE certs_certificates SET domains = ?, key_type = ?, updated_at = ? WHERE id = ?",
		string(domains), cryptox.KeyType(b.Leaf.PublicKey), s.now().UnixMilli(), id)
	if err != nil {
		return err
	}
	if err := s.settleCSR(ctx, tx, id, b.Key); err != nil {
		return err
	}
	if _, err := s.addVersion(ctx, tx, id, b, originUpload, 0); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.deploying.kick()
	return nil
}

// addVersion stores b as the current version of certificate id and
// publishes certificate.issued, in tx. It returns the version's ID.
func (s *service) addVersion(ctx context.Context, tx *sql.Tx, id int64, b *cryptox.Bundle, origin string, source int64) (int64, error) {
	fingerprint := cryptox.Fingerprint(b.Leaf)
	var owner int64
	err := tx.QueryRowContext(ctx, "SELECT certificate_id FROM certs_versions WHERE fingerprint = ?", fingerprint).Scan(&owner)
	switch {
	case err == nil:
		return 0, &duplicateError{owner}
	case !errors.Is(err, sql.ErrNoRows):
		return 0, err
	}

	key, err := cryptox.EncodeKey(b.Key)
	if err != nil {
		return 0, err
	}
	names := cryptox.Names(b.Leaf)
	namesJSON, _ := json.Marshal(names)
	chain := cryptox.EncodeCertificates(append([]*x509.Certificate{b.Leaf}, b.Chain...)...)
	now := s.now()
	var version int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO certs_versions (certificate_id, origin, names, serial, fingerprint, issuer, key_type,
		 not_before, not_after, chain, sealed_key, notified, created_at, source_provider)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?) RETURNING id`,
		id, origin, string(namesJSON), b.Leaf.SerialNumber.Text(16), fingerprint, issuerName(b.Leaf),
		cryptox.KeyType(b.Leaf.PublicKey), b.Leaf.NotBefore.UnixMilli(), b.Leaf.NotAfter.UnixMilli(),
		string(chain), s.secrets.Seal(key, "key:"+fingerprint), now.UnixMilli(), source).Scan(&version)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET current_version_id = ? WHERE id = ?", version, id); err != nil {
		return 0, err
	}
	if err := s.queueDeployments(ctx, tx, id, version); err != nil {
		return 0, err
	}
	ev := contracts.CertificateEvent{
		CertificateID: id, Domains: names, VersionID: version, Fingerprint: fingerprint, NotAfter: b.Leaf.NotAfter.UTC(),
	}
	if at, err := s.deployAt(ctx, tx, id); err != nil {
		return 0, err
	} else if at.After(now) {
		ev.DeployAt = at.UTC()
	}
	err = s.events.PublishTx(ctx, tx, contracts.CertificateIssued, strconv.FormatInt(id, 10), ev)
	return version, err
}

// announce publishes CertificateCurrent for each certificate with a
// version, as the module that published CertificatesAnnounce asked.
func (s *service) announce(ctx context.Context, _ events.Event) error {
	certs, err := s.list(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, c := range certs {
		if c.Current == nil {
			continue
		}
		if err := s.events.PublishTx(ctx, tx, contracts.CertificateCurrent, strconv.FormatInt(c.ID, 10), contracts.CertificateEvent{
			CertificateID: c.ID, Domains: c.Domains, VersionID: c.Current.ID, Fingerprint: c.Current.Fingerprint, NotAfter: c.Current.NotAfter.UTC(),
		}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// issuerName and subjectName are what the UI shows of a certificate's
// issuer and subject.
func issuerName(c *x509.Certificate) string  { return displayName(c.Issuer) }
func subjectName(c *x509.Certificate) string { return displayName(c.Subject) }

func displayName(n pkix.Name) string {
	switch {
	case n.CommonName != "":
		return n.CommonName
	case len(n.Organization) > 0:
		return n.Organization[0]
	}
	return n.String()
}

// querier is a *sql.DB or a *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

const selectCertificates = `SELECT c.id, c.domains, c.source, c.source_provider, c.solver_provider, c.key_type,
	c.renew_before_days, c.created_at, c.note, c.reuse_key, c.notify_to, c.paused, c.deploy_window,
	c.reminder_on, c.reminder_note, c.reminder_sent, c.backup_provider,
	v.id, v.origin, v.names, v.serial, v.fingerprint, v.issuer, v.key_type, v.not_before, v.not_after, v.chain, v.created_at,
	v.source_provider, v.renewal_start, v.renewal_end, v.renewal_at, v.renewal_explanation, v.renewal_check,
	v.ca_revoked_at, v.ca_revoke_reason, v.status_check,
	(SELECT created_at FROM certs_csr WHERE certificate_id = c.id),
	` + issuanceColumns + `
	FROM certs_certificates c LEFT JOIN certs_versions v ON v.id = c.current_version_id
	LEFT JOIN certs_issuances i ON i.id = (SELECT max(id) FROM certs_issuances WHERE certificate_id = c.id)`

// list returns every certificate, the soonest to expire first.
func (s *service) list(ctx context.Context) ([]*Certificate, error) {
	certs, err := s.query(ctx, s.db, selectCertificates)
	if err != nil {
		return nil, err
	}
	// Those without a version yet come first, as they need attention too.
	end := func(c *Certificate) int64 {
		if c.Current == nil {
			return math.MinInt64
		}
		return c.Current.NotAfter.UnixMilli()
	}
	slices.SortStableFunc(certs, func(a, b *Certificate) int { return cmp.Compare(end(a), end(b)) })
	return certs, nil
}

// load returns certificate id.
func (s *service) load(ctx context.Context, q querier, id int64) (*Certificate, error) {
	certs, err := s.query(ctx, q, selectCertificates+" WHERE c.id = ?", id)
	if err != nil {
		return nil, err
	}
	if len(certs) == 0 {
		return nil, errNotFound
	}
	return certs[0], nil
}

func (s *service) query(ctx context.Context, q querier, query string, args ...any) ([]*Certificate, error) {
	rows, err := q.QueryContext(ctx, query+" ORDER BY c.id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var certs []*Certificate
	for rows.Next() {
		c := &Certificate{}
		var domains string
		var created int64
		var sourceProvider, solverProvider, backupProvider sql.NullInt64
		var v struct {
			id                                                                      sql.NullInt64
			origin, names, serial, fingerprint, issuer, keyType, chain, explanation sql.NullString
			notBefore, notAfter, created, source, start, end, at, check             sql.NullInt64
			caRevoked, caReason, statusCheck                                        sql.NullInt64
		}
		var csrAt sql.NullInt64
		var is issuanceRow
		var reuseKey, paused, reminderSent int64
		var notifyTo string
		dest := []any{&c.ID, &domains, &c.Source, &sourceProvider, &solverProvider, &c.KeyType, &c.RenewBeforeDays, &created, &c.Note, &reuseKey, &notifyTo, &paused, &c.DeployWindow,
			&c.ReminderOn, &c.ReminderNote, &reminderSent, &backupProvider,
			&v.id, &v.origin, &v.names, &v.serial, &v.fingerprint, &v.issuer, &v.keyType, &v.notBefore, &v.notAfter, &v.chain, &v.created,
			&v.source, &v.start, &v.end, &v.at, &v.explanation, &v.check, &v.caRevoked, &v.caReason, &v.statusCheck, &csrAt}
		if err := rows.Scan(append(dest, is.dest()...)...); err != nil {
			return nil, err
		}
		c.ReuseKey, c.Paused, c.ReminderSent = reuseKey == 1, paused == 1, reminderSent == 1
		if csrAt.Valid {
			c.CSRAt = time.UnixMilli(csrAt.Int64).UTC()
		}
		if json.Unmarshal([]byte(notifyTo), &c.NotifyTo) != nil || c.NotifyTo == nil {
			c.NotifyTo = []int64{}
		}
		c.SourceProvider, c.SolverProvider, c.BackupProvider = sourceProvider.Int64, solverProvider.Int64, backupProvider.Int64
		if is.id.Valid {
			var err error
			if c.Issuance, err = is.issuance(); err != nil {
				return nil, err
			}
		}
		if err := json.Unmarshal([]byte(domains), &c.Domains); err != nil {
			return nil, err
		}
		c.CreatedAt = time.UnixMilli(created).UTC()
		if v.id.Valid {
			c.Current = &Version{
				ID: v.id.Int64, Origin: v.origin.String, Serial: v.serial.String, Fingerprint: v.fingerprint.String,
				Issuer: v.issuer.String, KeyType: v.keyType.String, Chain: v.chain.String,
				NotBefore: time.UnixMilli(v.notBefore.Int64).UTC(), NotAfter: time.UnixMilli(v.notAfter.Int64).UTC(),
				CreatedAt: time.UnixMilli(v.created.Int64).UTC(), SourceProvider: v.source.Int64,
				RenewalCheck:   time.UnixMilli(v.check.Int64).UTC(),
				CARevokeReason: int(v.caReason.Int64), StatusCheck: time.UnixMilli(v.statusCheck.Int64).UTC(),
			}
			if v.caRevoked.Valid {
				c.Current.CARevokedAt = time.UnixMilli(v.caRevoked.Int64).UTC()
			}
			if v.start.Valid && v.end.Valid && v.at.Valid {
				c.Current.Renewal = &RenewalWindow{Start: time.UnixMilli(v.start.Int64).UTC(), End: time.UnixMilli(v.end.Int64).UTC(),
					At: time.UnixMilli(v.at.Int64).UTC(), Explanation: v.explanation.String}
			}
			if err := json.Unmarshal([]byte(v.names.String), &c.Current.Names); err != nil {
				return nil, err
			}
		}
		certs = append(certs, c)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if err := s.attachTargets(ctx, q, certs); err != nil {
		return nil, err
	}
	return certs, nil
}

// versions returns every version of certificate id, the newest first.
func (s *service) versions(ctx context.Context, id int64) ([]*Version, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, origin, names, serial, fingerprint, issuer, key_type, not_before, not_after, chain, created_at,
		 source_provider, revoked_at, revoke_reason, ca_revoked_at, ca_revoke_reason
		 FROM certs_versions WHERE certificate_id = ? ORDER BY created_at DESC, id DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Version
	for rows.Next() {
		v := &Version{}
		var names string
		var notBefore, notAfter, created int64
		var revoked, caRevoked sql.NullInt64
		if err := rows.Scan(&v.ID, &v.Origin, &names, &v.Serial, &v.Fingerprint, &v.Issuer, &v.KeyType,
			&notBefore, &notAfter, &v.Chain, &created, &v.SourceProvider, &revoked, &v.RevokeReason, &caRevoked, &v.CARevokeReason); err != nil {
			return nil, err
		}
		if revoked.Valid {
			v.RevokedAt = time.UnixMilli(revoked.Int64).UTC()
		}
		if caRevoked.Valid {
			v.CARevokedAt = time.UnixMilli(caRevoked.Int64).UTC()
		}
		if err := json.Unmarshal([]byte(names), &v.Names); err != nil {
			return nil, err
		}
		v.NotBefore, v.NotAfter, v.CreatedAt = time.UnixMilli(notBefore).UTC(), time.UnixMilli(notAfter).UTC(), time.UnixMilli(created).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}

// changes are what an update of a certificate changes: the renewal window,
// unless days is 0, the targets, unless nil, and each of the others that
// is not nil.
type changes struct {
	days             int
	targets          []int64
	note             *string
	reuseKey, paused *bool
	notifyTo         *[]int64
	source           *int64
	solver, backup   *int64
	keyType          *string
	deployWindow     *string
	reminder         *reminder
}

// changeSource has certificate c issued by source from its next issuance
// on, as one uploaded is to be renewed, or one that certbot renewed is to
// be issued by JEC: not while an issuance is under way. The versions stay;
// the key and state a failed issuance kept for an order at the old source
// go, as does the solver of a source that needs none. The key type is the
// new source's own, or that of the settings when the old one's was.
func (s *service) changeSource(ctx context.Context, tx *sql.Tx, c *Certificate, source int64) error {
	if open, err := s.openIssuance(ctx, tx, c.ID); err != nil {
		return err
	} else if open != nil {
		return errIssuing
	}
	typ, err := s.providers.Type(ctx, source)
	if err != nil {
		return err
	}
	solver := sql.NullInt64{Int64: c.SolverProvider, Valid: c.SolverProvider != 0 && typ.NeedsSolver}
	keyType := c.KeyType
	switch {
	case typ.OwnKey:
		keyType = ""
	case keyType == "":
		st, err := settingsFrom(ctx, tx)
		if err != nil {
			return err
		}
		keyType = st.KeyType
	}
	if _, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET source = ?, source_provider = ?, solver_provider = ?, key_type = ?, updated_at = ? WHERE id = ?",
		sourceProvider, source, solver, keyType, s.now().UnixMilli(), c.ID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE certs_issuances SET sealed_key = NULL, sealed_state = NULL WHERE certificate_id = ? AND state = 'failed'", c.ID)
	return err
}

// reminder is a day to be reminded on, "" for none, and what of.
type reminder struct{ on, note string }

// update makes the changes ch to certificate id.
func (s *service) update(ctx context.Context, id int64, ch changes) error {
	days, targets, note, reuseKey, notifyTo := ch.days, ch.targets, ch.note, ch.reuseKey, ch.notifyTo
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := s.load(ctx, tx, id)
	if err != nil {
		return err
	}
	if days != 0 {
		if _, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET renew_before_days = ?, updated_at = ? WHERE id = ?",
			days, s.now().UnixMilli(), id); err != nil {
			return err
		}
	}
	if targets != nil {
		if err := s.setTargets(ctx, tx, c, targets); err != nil {
			return err
		}
	}
	if r := ch.reminder; r != nil {
		note := strings.TrimSpace(r.note)
		if r.on == "" {
			note = ""
		}
		// A day changed is to be sent again.
		if _, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET reminder_on = ?, reminder_note = ?, reminder_sent = 0, updated_at = ? WHERE id = ?",
			r.on, note, s.now().UnixMilli(), id); err != nil {
			return err
		}
	}
	if note != nil {
		if _, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET note = ?, updated_at = ? WHERE id = ?",
			strings.TrimSpace(*note), s.now().UnixMilli(), id); err != nil {
			return err
		}
	}
	if notifyTo != nil {
		list, _ := json.Marshal(*notifyTo)
		if _, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET notify_to = ?, updated_at = ? WHERE id = ?",
			string(list), s.now().UnixMilli(), id); err != nil {
			return err
		}
	}
	if reuseKey != nil {
		reuse := 0
		if *reuseKey {
			reuse = 1
		}
		if _, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET reuse_key = ?, updated_at = ? WHERE id = ?",
			reuse, s.now().UnixMilli(), id); err != nil {
			return err
		}
	}
	if ch.deployWindow != nil {
		if _, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET deploy_window = ?, updated_at = ? WHERE id = ?",
			*ch.deployWindow, s.now().UnixMilli(), id); err != nil {
			return err
		}
	}
	if ch.source != nil && (*ch.source != c.SourceProvider || c.Source != sourceProvider) {
		if err := s.changeSource(ctx, tx, c, *ch.source); err != nil {
			return err
		}
	}
	if ch.solver != nil {
		if _, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET solver_provider = ?, updated_at = ? WHERE id = ?",
			*ch.solver, s.now().UnixMilli(), id); err != nil {
			return err
		}
	}
	if ch.backup != nil {
		backup := sql.NullInt64{Int64: *ch.backup, Valid: *ch.backup != 0}
		if _, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET backup_provider = ?, updated_at = ? WHERE id = ?",
			backup, s.now().UnixMilli(), id); err != nil {
			return err
		}
	}
	if ch.keyType != nil {
		if _, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET key_type = ?, updated_at = ? WHERE id = ?",
			*ch.keyType, s.now().UnixMilli(), id); err != nil {
			return err
		}
	}
	if ch.paused != nil {
		paused := 0
		if *ch.paused {
			paused = 1
		}
		if _, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET paused = ?, updated_at = ? WHERE id = ?",
			paused, s.now().UnixMilli(), id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.deploying.kick()
	return nil
}

// remove deletes certificate id with its versions and publishes
// certificate.deleted. An attempt to issue it that is under way stops.
func (s *service) remove(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := s.load(ctx, tx, id)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM certs_certificates WHERE id = ?", id); err != nil {
		return err
	}
	err = s.events.PublishTx(ctx, tx, contracts.CertificateDeleted, strconv.FormatInt(id, 10),
		contracts.CertificateEvent{CertificateID: id, Domains: c.Domains})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.stopAttempts(id)
	return nil
}
