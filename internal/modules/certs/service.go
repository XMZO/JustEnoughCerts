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
	"slices"
	"strconv"
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

	issuing   *queue[claimed]
	deploying *queue[deployClaim]
	mu        sync.Mutex                    // guards running
	running   map[runKey]context.CancelFunc // the attempts under way
}

func newService(db *sql.DB, ev *events.Scope, box *secrets.Scope, prov *providers.Scope, log *slog.Logger, now func() time.Time) *service {
	s := &service{
		db: db, events: ev, secrets: box, providers: prov, log: log, now: now,
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
	ID              int64
	Domains         []string
	Source          string
	SourceProvider  int64 // with Source "provider"
	SolverProvider  int64 // 0 for none
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
}

// state tells how a certificate stands at now: being issued or failing to
// be issued since its current version came, failing to be deployed to a
// target or being deployed, or else by the life left in its current
// version.
func (c *Certificate) state(now time.Time) string {
	if is := c.Issuance; is != nil {
		switch {
		case is.State == issuanceRunning, is.State == issuancePending && is.Error.Key == "":
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
		case d.State == issuanceRunning, d.State == issuancePending:
			deploying = true
		}
	}
	if deploying {
		return stateDeploying
	}
	if c.Current == nil {
		return stateExpired
	}
	switch left := c.Current.NotAfter.Sub(now); {
	case left <= 0:
		return stateExpired
	case left <= time.Duration(c.RenewBeforeDays)*day:
		return stateExpiring
	}
	return stateOK
}

// add stores b as a new certificate, deployed to targets.
func (s *service) add(ctx context.Context, b *cryptox.Bundle, renewBeforeDays int, targets []int64) (int64, error) {
	if renewBeforeDays == 0 {
		renewBeforeDays = defaultRenewBeforeDays
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
	if _, err := s.addVersion(ctx, tx, id, b, originUpload); err != nil {
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
	if _, err := s.addVersion(ctx, tx, id, b, originUpload); err != nil {
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
func (s *service) addVersion(ctx context.Context, tx *sql.Tx, id int64, b *cryptox.Bundle, origin string) (int64, error) {
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
		 not_before, not_after, chain, sealed_key, notified, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?) RETURNING id`,
		id, origin, string(namesJSON), b.Leaf.SerialNumber.Text(16), fingerprint, issuerName(b.Leaf),
		cryptox.KeyType(b.Leaf.PublicKey), b.Leaf.NotBefore.UnixMilli(), b.Leaf.NotAfter.UnixMilli(),
		string(chain), s.secrets.Seal(key, "key:"+fingerprint), now.UnixMilli()).Scan(&version)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET current_version_id = ? WHERE id = ?", version, id); err != nil {
		return 0, err
	}
	if err := s.queueDeployments(ctx, tx, id, version); err != nil {
		return 0, err
	}
	err = s.events.PublishTx(ctx, tx, contracts.CertificateIssued, strconv.FormatInt(id, 10), contracts.CertificateEvent{
		CertificateID: id, Domains: names, VersionID: version, NotAfter: b.Leaf.NotAfter.UTC(),
	})
	return version, err
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
	c.renew_before_days, c.created_at,
	v.id, v.origin, v.names, v.serial, v.fingerprint, v.issuer, v.key_type, v.not_before, v.not_after, v.chain, v.created_at,
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
		var sourceProvider, solverProvider sql.NullInt64
		var v struct {
			id                                                         sql.NullInt64
			origin, names, serial, fingerprint, issuer, keyType, chain sql.NullString
			notBefore, notAfter, created                               sql.NullInt64
		}
		var is issuanceRow
		dest := []any{&c.ID, &domains, &c.Source, &sourceProvider, &solverProvider, &c.KeyType, &c.RenewBeforeDays, &created,
			&v.id, &v.origin, &v.names, &v.serial, &v.fingerprint, &v.issuer, &v.keyType, &v.notBefore, &v.notAfter, &v.chain, &v.created}
		if err := rows.Scan(append(dest, is.dest()...)...); err != nil {
			return nil, err
		}
		c.SourceProvider, c.SolverProvider = sourceProvider.Int64, solverProvider.Int64
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
				CreatedAt: time.UnixMilli(v.created.Int64).UTC(),
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
		`SELECT id, origin, names, serial, fingerprint, issuer, key_type, not_before, not_after, chain, created_at
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
		if err := rows.Scan(&v.ID, &v.Origin, &names, &v.Serial, &v.Fingerprint, &v.Issuer, &v.KeyType,
			&notBefore, &notAfter, &v.Chain, &created); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(names), &v.Names); err != nil {
			return nil, err
		}
		v.NotBefore, v.NotAfter, v.CreatedAt = time.UnixMilli(notBefore).UTC(), time.UnixMilli(notAfter).UTC(), time.UnixMilli(created).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}

// update changes the renewal window of certificate id, unless days is 0,
// and its targets, unless targets is nil.
func (s *service) update(ctx context.Context, id int64, days int, targets []int64) error {
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
