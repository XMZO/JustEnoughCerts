package certs

import (
	"context"
	"crypto"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// States of an issuance.
const (
	issuancePending = "pending" // the next attempt starts at NextAt
	issuanceRunning = "running"
	issuanceWaiting = "waiting" // on its source, asked again at NextAt in the same attempt
	issuanceDone    = "done"
	issuanceFailed  = "failed" // no attempt follows unless a person starts one
)

// Reasons for an issuance.
const (
	reasonFirst   = "first" // the certificate was added, or has no version yet
	reasonRenewal = "renewal"
	reasonManual  = "manual"
)

// How issuances run and are retried (PLAN.md 4.4).
const (
	maxRunning     = 2                // attempts under way at once
	attemptTimeout = 30 * time.Minute // bounds an attempt
	// defaultRetries is how many times a failed issuance or deployment is
	// tried again on its own, unless the settings say otherwise; it fails
	// for good after that. maxRetries bounds what they may say.
	defaultRetries = 7
	maxRetries     = 20
	retryBase      = 10 * time.Minute // the wait after the first failed attempt, doubled after each
	retryLimit     = 12 * time.Hour
	retryAfterMax  = 24 * time.Hour // the longest wait a source may ask for
	// failedPause is how long renewal leaves a certificate alone after an
	// issuance of it failed for good.
	failedPause  = 24 * time.Hour
	maxDetailLen = 4000 // bytes of raw text kept with a log line
	// shortestWait and longestWait bound how long an issuance waits on its
	// source before asking again.
	shortestWait = time.Minute
	longestWait  = 24 * time.Hour
)

// keyContext and stateContext are what an issuance's key and its source's
// state are sealed for. They name the certificate, not the issuance, so
// that a new issuance can take them over (startIssuance).
func keyContext(certificateID int64) string {
	return "issuance-key:" + strconv.FormatInt(certificateID, 10)
}

func stateContext(certificateID int64) string {
	return "issuance-state:" + strconv.FormatInt(certificateID, 10)
}

// errNoSource: the certificate gets new versions by upload only.
var errNoSource = errors.New("the certificate has no source to issue it")

// errIssuing: an issuance of the certificate is under way.
var errIssuing = errors.New("an issuance of the certificate is under way")

// setDomains replaces the domains of certificate id, which has a source,
// and issues it at once for them; it does nothing when they are the same.
// An order kept from an issuance that failed is for the old domains: it
// is dropped.
func (s *service) setDomains(ctx context.Context, id int64, domains []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := s.load(ctx, tx, id)
	if err != nil {
		return err
	}
	if c.Source != sourceProvider {
		return errNoSource
	}
	if open, err := s.openIssuance(ctx, tx, id); err != nil {
		return err
	} else if open != nil {
		return errIssuing
	}
	if slices.Equal(c.Domains, domains) {
		return nil
	}
	list, _ := json.Marshal(domains)
	now := s.now().UnixMilli()
	if _, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET domains = ?, updated_at = ? WHERE id = ?", string(list), now, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE certs_issuances SET sealed_key = NULL, sealed_state = NULL WHERE certificate_id = ? AND state = 'failed'", id); err != nil {
		return err
	}
	if _, err := s.startIssuance(ctx, tx, id, reasonManual); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.issuing.kick()
	return nil
}

// Issuance is getting a new version of a certificate from its source.
type Issuance struct {
	ID            int64
	CertificateID int64
	Reason        string
	State         string
	Attempts      int
	NextAt        time.Time // for a pending issuance
	VersionID     int64     // the version issued, once done
	// Error tells why the last attempt failed; its Key is empty if none did.
	Error      contracts.Message
	CreatedAt  time.Time
	StartedAt  time.Time // of the last attempt; zero before the first
	FinishedAt time.Time // zero until done or failed
}

// issuanceColumns are the columns of certs_issuances i that issuanceRow
// scans.
const issuanceColumns = `i.id, i.certificate_id, i.reason, i.state, i.attempts, i.next_at, i.version_id,
	i.error_key, i.error_params, i.created_at, i.started_at, i.finished_at`

// issuanceRow scans issuanceColumns, which are all NULL for a certificate
// never issued.
type issuanceRow struct {
	id, certificateID, attempts, nextAt, versionID, created, started, finished sql.NullInt64
	reason, state, errorParams                                                 sql.NullString
	errorKey                                                                   sql.Null[i18n.Key] // as stored from a constant
}

func (r *issuanceRow) dest() []any {
	return []any{&r.id, &r.certificateID, &r.reason, &r.state, &r.attempts, &r.nextAt, &r.versionID,
		&r.errorKey, &r.errorParams, &r.created, &r.started, &r.finished}
}

func (r *issuanceRow) issuance() (*Issuance, error) {
	is := &Issuance{
		ID: r.id.Int64, CertificateID: r.certificateID.Int64, Reason: r.reason.String, State: r.state.String,
		Attempts: int(r.attempts.Int64), VersionID: r.versionID.Int64,
		NextAt: time.UnixMilli(r.nextAt.Int64).UTC(), CreatedAt: time.UnixMilli(r.created.Int64).UTC(),
		Error: contracts.Message{Key: r.errorKey.V},
	}
	if r.started.Valid {
		is.StartedAt = time.UnixMilli(r.started.Int64).UTC()
	}
	if r.finished.Valid {
		is.FinishedAt = time.UnixMilli(r.finished.Int64).UTC()
	}
	if is.Error.Key != "" && r.errorParams.String != "" {
		if err := json.Unmarshal([]byte(r.errorParams.String), &is.Error.Params); err != nil {
			return nil, fmt.Errorf("issuance %d: error params: %w", is.ID, err)
		}
	}
	return is, nil
}

// open is the issuance of a certificate that is under way, if any.
func (s *service) openIssuance(ctx context.Context, q querier, certificateID int64) (*Issuance, error) {
	list, err := s.queryIssuances(ctx, q, "WHERE i.certificate_id = ? AND i.state IN ('pending', 'running', 'waiting')", certificateID)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return list[0], nil
}

// issuance returns issuance id.
func (s *service) issuance(ctx context.Context, id int64) (*Issuance, error) {
	list, err := s.queryIssuances(ctx, s.db, "WHERE i.id = ?", id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errNotFound
	}
	return list[0], nil
}

// issuances returns the latest issuances of certificate id, the newest
// first.
func (s *service) issuances(ctx context.Context, certificateID int64, limit int) ([]*Issuance, error) {
	return s.queryIssuances(ctx, s.db, "WHERE i.certificate_id = ? ORDER BY i.id DESC LIMIT ?", certificateID, limit)
}

func (s *service) queryIssuances(ctx context.Context, q querier, where string, args ...any) ([]*Issuance, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+issuanceColumns+" FROM certs_issuances i "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Issuance
	for rows.Next() {
		var r issuanceRow
		if err := rows.Scan(r.dest()...); err != nil {
			return nil, err
		}
		is, err := r.issuance()
		if err != nil {
			return nil, err
		}
		out = append(out, is)
	}
	return out, rows.Err()
}

// startIssuance opens an issuance of certificate id, due now, in tx. If
// one is under way, it returns that one, made due now if it was waiting to
// retry or on its source. A new issuance takes over the key and source
// state of the last one if that failed while its source still had an
// order: a vendor may yet issue the certificate for that key.
func (s *service) startIssuance(ctx context.Context, tx *sql.Tx, certificateID int64, reason string) (int64, error) {
	now := s.now().UnixMilli()
	open, err := s.openIssuance(ctx, tx, certificateID)
	if err != nil {
		return 0, err
	}
	if open != nil {
		if open.State == issuancePending || open.State == issuanceWaiting {
			_, err = tx.ExecContext(ctx, "UPDATE certs_issuances SET next_at = ? WHERE id = ?", now, open.ID)
		}
		return open.ID, err
	}
	var id int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO certs_issuances (certificate_id, reason, state, attempts, next_at, error_key, error_params, created_at)
		 VALUES (?, ?, 'pending', 0, ?, '', '', ?) RETURNING id`,
		certificateID, reason, now, now).Scan(&id)
	if err != nil {
		return 0, err
	}
	var took bool
	err = tx.QueryRowContext(ctx,
		`UPDATE certs_issuances SET (sealed_key, sealed_state) = (SELECT sealed_key, sealed_state FROM certs_issuances
		   WHERE certificate_id = ? AND state = 'failed' AND sealed_state IS NOT NULL ORDER BY id DESC LIMIT 1)
		 WHERE id = ? RETURNING sealed_state IS NOT NULL`, certificateID, id).Scan(&took)
	if err != nil {
		return 0, err
	}
	if !took {
		return id, nil
	}
	if _, err = tx.ExecContext(ctx, "UPDATE certs_issuances SET sealed_key = NULL, sealed_state = NULL WHERE certificate_id = ? AND id < ?",
		certificateID, id); err != nil {
		return 0, err
	}
	// The key step is done: the first attempt logs no key of its own.
	_, err = tx.ExecContext(ctx, issuanceLogTable.insert, id, id, now, 1, string(contracts.Info), string(contracts.StepKey),
		string(i18n.CertsLogTakeOver), "", "")
	return id, err
}

// issue starts an issuance of certificate id that a person asked for, and
// wakes the runner.
func (s *service) issue(ctx context.Context, id int64) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := s.load(ctx, tx, id)
	if err != nil {
		return 0, err
	}
	if c.Source != sourceProvider {
		return 0, errNoSource
	}
	reason := reasonManual
	if c.Current == nil {
		reason = reasonFirst
	}
	issuance, err := s.startIssuance(ctx, tx, id, reason)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.issuing.kick()
	return issuance, nil
}

// claimed is an issuance an attempt was started for, or continued for
// after a wait on its source.
type claimed struct {
	id, certificateID int64
	attempt           int
	started           time.Time // the attempt
	resumed           bool      // after a wait
	source            int64     // the source provider asked, once it is
}

// runIssuances is the job certs.issue: it runs the due issuances (see
// queue).
func (s *service) runIssuances(ctx context.Context) error { return s.issuing.run(ctx, s) }

// resume makes the issuances that a stop of JEC cut short due again.
func (s *service) resume(ctx context.Context) error {
	now := s.now().UnixMilli()
	rows, err := s.db.QueryContext(ctx,
		"UPDATE certs_issuances SET state = 'pending', next_at = ? WHERE state = 'running' RETURNING id, attempts", now)
	if err != nil {
		return err
	}
	type cut struct {
		id      int64
		attempt int
	}
	var list []cut
	for rows.Next() {
		var c cut
		if err := rows.Scan(&c.id, &c.attempt); err != nil {
			_ = rows.Close()
			return err
		}
		list = append(list, c)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, c := range list {
		s.taskLogger(issuanceLogTable, c.id, c.attempt).add(contracts.LogLine{Level: contracts.Warn, Key: i18n.CertsLogInterrupted})
	}
	return nil
}

// claim marks the issuance due the longest as running and returns it; nil
// if none is due. A pending issuance starts a new attempt, which counts; a
// waiting one goes on with its attempt.
func (s *service) claim(ctx context.Context) (*claimed, error) {
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	c := &claimed{}
	var state string
	var started sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT id, certificate_id, state, attempts, started_at FROM certs_issuances
		 WHERE state IN ('pending', 'waiting') AND next_at <= ? ORDER BY next_at, id LIMIT 1`, now.UnixMilli()).
		Scan(&c.id, &c.certificateID, &state, &c.attempt, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if state == issuanceWaiting && started.Valid {
		c.resumed, c.started = true, time.UnixMilli(started.Int64)
		_, err = tx.ExecContext(ctx, "UPDATE certs_issuances SET state = 'running' WHERE id = ?", c.id)
	} else {
		c.attempt++
		c.started = now
		_, err = tx.ExecContext(ctx, "UPDATE certs_issuances SET state = 'running', attempts = ?, started_at = ? WHERE id = ?",
			c.attempt, now.UnixMilli(), c.id)
	}
	if err != nil {
		return nil, err
	}
	return c, tx.Commit()
}

// attempt tries once to issue the certificate of c, within attemptCtx. The
// job's ctx ending means that JEC stops: the issuance then stays running,
// to start over after the restart.
func (s *service) attempt(ctx, attemptCtx context.Context, c *claimed) {
	log := s.taskLogger(issuanceLogTable, c.id, c.attempt)
	if c.attempt > 1 && !c.resumed {
		log.add(contracts.LogLine{Level: contracts.Info, Key: i18n.CertsLogAttempt, Params: map[string]any{"attempt": c.attempt}})
	}
	b, err := s.obtain(attemptCtx, c, log.add)
	// Store what the source gave, even if JEC is stopping now.
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	var wait *contracts.Wait
	switch {
	case err == nil:
		err = s.complete(storeCtx, c, b, log)
		if err == nil {
			seconds := int(s.now().Sub(c.started).Round(time.Second) / time.Second)
			log.add(contracts.LogLine{Level: contracts.OK, Key: i18n.CertsLogDone, Params: map[string]any{"seconds": seconds}})
			return
		}
	case errors.As(err, &wait):
		if err := s.wait(storeCtx, c, wait); err != nil {
			s.log.Error("record waiting issuance", "issuance", c.id, "err", err)
		}
		return
	}
	if ctx.Err() != nil || errors.Is(err, errNotFound) {
		return // stopping, or the certificate was deleted
	}
	s.fail(context.WithoutCancel(ctx), c, err, log)
}

// obtain gets a new certificate for the certificate of c from its source,
// with the issuance's private key, which its first attempt makes.
func (s *service) obtain(ctx context.Context, cl *claimed, log func(contracts.LogLine)) (*cryptox.Bundle, error) {
	c, err := s.load(ctx, s.db, cl.certificateID)
	if err != nil {
		return nil, err
	}
	if c.Source != sourceProvider {
		return nil, retry.Permanent(errNoSource)
	}
	// The providers first: a problem with them belongs to no step.
	provider, err := openProvider[io.Closer](ctx, s.providers, c.SourceProvider)
	if err != nil {
		return nil, err
	}
	defer closeProvider(provider, s)
	cl.source = c.SourceProvider
	if ks, ok := provider.(contracts.KeySource); ok {
		return s.collect(ctx, cl, c, ks, log)
	}
	source, ok := provider.(contracts.Source)
	if !ok {
		return nil, retry.Permanent(fmt.Errorf("provider %d is no source", c.SourceProvider))
	}
	req := contracts.IssueRequest{Domains: c.Domains, Log: log, Replaces: replaced(c)}
	if c.SolverProvider != 0 {
		solver, err := openProvider[io.Closer](ctx, s.providers, c.SolverProvider)
		if err != nil {
			return nil, err
		}
		defer closeProvider(solver, s)
		req.Solver, _ = solver.(contracts.Solver)
		req.Files, _ = solver.(contracts.FileSolver)
		req.Records, _ = solver.(contracts.RecordSolver)
		if req.Solver == nil && req.Files == nil {
			return nil, retry.Permanent(fmt.Errorf("provider %d is no solver", c.SolverProvider))
		}
	}
	if req.Key, req.State, err = s.kept(ctx, cl, c.KeyType, log); err != nil {
		return nil, err
	}
	chain, err := source.Issue(ctx, req)
	if err != nil {
		return nil, err
	}
	b, err := cryptox.NewBundle(chain, req.Key)
	if err != nil {
		return nil, fmt.Errorf("the certificate the source returned: %w", err)
	}
	if missing := uncovered(c.Domains, cryptox.Names(b.Leaf)); len(missing) > 0 {
		return nil, &contracts.Problem{Key: i18n.CertsErrorNamesMissing, Params: map[string]any{"domains": strings.Join(missing, ", ")}}
	}
	return b, nil
}

// collect takes in what a KeySource found for c, key included: there is no
// key to make, and nothing new is a Wait the source returns.
func (s *service) collect(ctx context.Context, cl *claimed, c *Certificate, ks contracts.KeySource, log func(contracts.LogLine)) (*cryptox.Bundle, error) {
	state, err := s.keptState(ctx, cl)
	if err != nil {
		return nil, err
	}
	if state == nil {
		log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepKey, Key: i18n.CertsLogKeyFromSource})
	}
	req := contracts.CollectRequest{Domains: c.Domains, Log: log, State: state}
	if c.Current != nil {
		req.Current, req.CurrentEnd = c.Current.Fingerprint, c.Current.NotAfter
	}
	got, err := ks.Collect(ctx, req)
	if err != nil {
		return nil, err
	}
	b, err := cryptox.NewBundle(got.Chain, got.Key)
	if err != nil {
		return nil, fmt.Errorf("the certificate the source found: %w", err)
	}
	if missing := uncovered(c.Domains, cryptox.Names(b.Leaf)); len(missing) > 0 {
		return nil, &contracts.Problem{Key: i18n.CertsErrorNamesMissing, Params: map[string]any{"domains": strings.Join(missing, ", ")}}
	}
	return b, nil
}

// keptState returns what the source of the issuance of c kept from its
// last attempt, if anything.
func (s *service) keptState(ctx context.Context, c *claimed) ([]byte, error) {
	var sealed []byte
	err := s.db.QueryRowContext(ctx, "SELECT sealed_state FROM certs_issuances WHERE id = ?", c.id).Scan(&sealed)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, errNotFound
	case err != nil || sealed == nil:
		return nil, err
	}
	state, err := s.secrets.Open(sealed, stateContext(c.certificateID))
	if err != nil {
		return nil, retry.Permanent(err)
	}
	return state, nil
}

// kept returns the private key of the issuance of c, making it of
// keyType and storing it sealed on the first attempt, and what its source
// kept from the last attempt, if anything.
func (s *service) kept(ctx context.Context, c *claimed, keyType string, log func(contracts.LogLine)) (crypto.Signer, []byte, error) {
	var sealedKey, sealedState []byte
	err := s.db.QueryRowContext(ctx, "SELECT sealed_key, sealed_state FROM certs_issuances WHERE id = ?", c.id).Scan(&sealedKey, &sealedState)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, errNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	var state []byte
	if sealedState != nil {
		if state, err = s.secrets.Open(sealedState, stateContext(c.certificateID)); err != nil {
			return nil, nil, retry.Permanent(err)
		}
	}
	if sealedKey != nil {
		pemKey, err := s.secrets.Open(sealedKey, keyContext(c.certificateID))
		if err != nil {
			return nil, nil, retry.Permanent(err)
		}
		key, err := cryptox.ParseKey(pemKey)
		return key, state, err
	}
	kind, size, _ := strings.Cut(keyType, "-")
	key, pemKey, err := s.reusedKey(ctx, c.certificateID, keyType)
	if err != nil {
		return nil, nil, err
	}
	if key != nil {
		log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepKey, Key: i18n.CertsLogKeyReused, Params: map[string]any{"kind": kind, "size": keySize(size)}})
	} else {
		log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepKey, Key: i18n.CertsLogKey, Params: map[string]any{"kind": kind, "size": keySize(size)}})
		if key, err = cryptox.GenerateKey(keyType); err != nil {
			return nil, nil, retry.Permanent(err)
		}
		if pemKey, err = cryptox.EncodeKey(key); err != nil {
			return nil, nil, err
		}
	}
	res, err := s.db.ExecContext(ctx, "UPDATE certs_issuances SET sealed_key = ? WHERE id = ? AND state = 'running'",
		s.secrets.Seal(pemKey, keyContext(c.certificateID)), c.id)
	if err != nil {
		return nil, nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil, errNotFound // deleted meanwhile
	}
	return key, state, nil
}

// reusedKey returns the private key of the version in use of certificate
// id, as PEM too, when the certificate keeps it for renewals and it is of
// keyType; nil otherwise. A version its CA or JEC revoked gives its key up.
func (s *service) reusedKey(ctx context.Context, id int64, keyType string) (crypto.Signer, []byte, error) {
	var reuse int64
	var sealed []byte
	var fingerprint, versionType sql.NullString
	var caRevoked, revoked sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT c.reuse_key, v.sealed_key, v.fingerprint, v.key_type, v.ca_revoked_at, v.revoked_at
		 FROM certs_certificates c LEFT JOIN certs_versions v ON v.id = c.current_version_id WHERE c.id = ?`, id).
		Scan(&reuse, &sealed, &fingerprint, &versionType, &caRevoked, &revoked)
	if err != nil {
		return nil, nil, err
	}
	if reuse != 1 || sealed == nil || versionType.String != keyType || caRevoked.Valid || revoked.Valid {
		return nil, nil, nil
	}
	pemKey, err := s.secrets.Open(sealed, "key:"+fingerprint.String)
	if err != nil {
		return nil, nil, retry.Permanent(err)
	}
	key, err := cryptox.ParseKey(pemKey)
	if err != nil {
		return nil, nil, retry.Permanent(err)
	}
	return key, pemKey, nil
}

// wait records that the source of the issuance of c is not done: the
// issuance waits, keeping what the source asked to, and asks again later.
func (s *service) wait(ctx context.Context, c *claimed, w *contracts.Wait) error {
	var state any // NULL when the source keeps nothing
	if w.State != nil {
		state = s.secrets.Seal(w.State, stateContext(c.certificateID))
	}
	next := s.now().Add(min(max(w.After, shortestWait), longestWait))
	_, err := s.db.ExecContext(ctx,
		`UPDATE certs_issuances SET state = 'waiting', next_at = ?, sealed_state = ?, error_key = '', error_params = ''
		 WHERE id = ? AND state = 'running'`, next.UnixMilli(), state, c.id)
	return err
}

// keySize writes the size part of a key type as the UI shows it: 2048, or
// P-256 for p256.
func keySize(size string) string {
	if rest, ok := strings.CutPrefix(size, "p"); ok {
		return "P-" + rest
	}
	return size
}

// openProvider opens provider instance id as a T, such as a
// contracts.Source.
func openProvider[T any](ctx context.Context, p *providers.Scope, id int64) (T, error) {
	var zero T
	v, _, err := p.Open(ctx, id)
	switch {
	case errors.Is(err, providers.ErrNotFound):
		return zero, retry.Permanent(&contracts.Problem{Key: i18n.CertsErrorProviderMissing, Err: err})
	case errors.Is(err, providers.ErrUnavailable):
		return zero, &contracts.Problem{Key: i18n.CertsErrorProviderUnavailable, Err: err}
	case err != nil:
		return zero, err
	}
	t, ok := v.(T)
	if !ok {
		if c, ok := v.(io.Closer); ok {
			_ = c.Close()
		}
		return zero, retry.Permanent(fmt.Errorf("provider %d does not do what the certificate needs it for", id))
	}
	return t, nil
}

func closeProvider(p io.Closer, s *service) {
	if err := p.Close(); err != nil {
		s.log.Warn("close provider", "err", err)
	}
}

// complete stores b as the new version and marks the issuance done.
func (s *service) complete(ctx context.Context, c *claimed, b *cryptox.Bundle, log *taskLog) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	version, err := s.addVersion(ctx, tx, c.certificateID, b, originIssued, c.source)
	if err != nil {
		return err
	}
	now := s.now().UnixMilli()
	if _, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET updated_at = ? WHERE id = ?", now, c.certificateID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE certs_issuances SET state = 'done', version_id = ?, finished_at = ?, error_key = '', error_params = '',
		 sealed_key = NULL, sealed_state = NULL WHERE id = ? AND state = 'running'`, version, now, c.id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound // deleted meanwhile
	}
	// The new version settles any order an earlier issuance left behind.
	if _, err := tx.ExecContext(ctx, "UPDATE certs_issuances SET sealed_key = NULL, sealed_state = NULL WHERE certificate_id = ? AND id < ?",
		c.certificateID, c.id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.deploying.kick()
	days := int(b.Leaf.NotAfter.Sub(s.now()) / day)
	log.add(contracts.LogLine{Level: contracts.Info, Step: contracts.StepDownload, Key: i18n.CertsLogStored, Params: map[string]any{"days": days}})
	return nil
}

// fail records a failed attempt: the issuance retries after a backoff, or
// fails for good when retrying cannot help or the attempts ran out.
func (s *service) fail(ctx context.Context, c *claimed, cause error, log *taskLog) {
	msg, detail := explain(cause, i18n.CertsErrorIssueFailed)
	log.add(contracts.LogLine{Level: contracts.Error, Step: log.current(), Key: msg.Key, Params: msg.Params, Detail: detail})

	now := s.now()
	final, next := nextAttempt(c.attempt, s.retries(ctx), cause, now)
	recorded, err := s.recordFailure(ctx, c, msg, now, next, contracts.IsRestart(cause), contracts.KeptState(cause))
	if err != nil {
		s.log.Error("record failed issuance", "issuance", c.id, "err", err)
	}
	if !recorded || err != nil {
		return
	}
	if final {
		log.add(contracts.LogLine{Level: contracts.Error, Key: i18n.CertsLogGaveUp})
	} else {
		minutes := int(next.Sub(now).Round(time.Minute) / time.Minute)
		log.add(contracts.LogLine{Level: contracts.Info, Key: i18n.CertsLogRetry, Params: map[string]any{"minutes": max(minutes, 1)}})
	}
}

// recordFailure stores how an attempt failed, with the time of the next
// one, zero when none follows, and publishes certificate.issue_failed;
// with restart, it drops the key and what the source kept, so that the
// next attempt, or the next issuance, starts afresh, and otherwise keeps
// state, when the source gave one, in place of what it kept before. It
// reports false when the issuance is gone, as its certificate was deleted.
func (s *service) recordFailure(ctx context.Context, c *claimed, msg contracts.Message, now, next time.Time, restart bool, state []byte) (bool, error) {
	params := ""
	if len(msg.Params) > 0 {
		b, _ := json.Marshal(msg.Params)
		params = string(b)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if state != nil && !restart {
		// First, so that a final failure keeps the key with it.
		if _, err := tx.ExecContext(ctx, "UPDATE certs_issuances SET sealed_state = ? WHERE id = ?",
			s.secrets.Seal(state, stateContext(c.certificateID)), c.id); err != nil {
			return false, err
		}
	}
	var res sql.Result
	if next.IsZero() {
		// The key stays only with an order the source may still fill.
		res, err = tx.ExecContext(ctx,
			`UPDATE certs_issuances SET state = 'failed', finished_at = ?, error_key = ?, error_params = ?,
			 sealed_key = CASE WHEN sealed_state IS NULL THEN NULL ELSE sealed_key END
			 WHERE id = ? AND state = 'running'`, now.UnixMilli(), string(msg.Key), params, c.id)
	} else {
		res, err = tx.ExecContext(ctx,
			`UPDATE certs_issuances SET state = 'pending', next_at = ?, error_key = ?, error_params = ?
			 WHERE id = ? AND state = 'running'`, next.UnixMilli(), string(msg.Key), params, c.id)
	}
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	if restart {
		// The order is spent: what the source kept goes, with the key.
		if _, err := tx.ExecContext(ctx, "UPDATE certs_issuances SET sealed_key = NULL, sealed_state = NULL WHERE id = ?", c.id); err != nil {
			return false, err
		}
	}
	cert, err := s.load(ctx, tx, c.certificateID)
	if err != nil {
		return false, err
	}
	err = s.events.PublishTx(ctx, tx, contracts.CertificateIssueFailed, strconv.FormatInt(c.certificateID, 10), contracts.CertificateEvent{
		CertificateID: c.certificateID, Domains: cert.Domains,
		Issuance: &contracts.Issuance{ID: c.id, Attempt: c.attempt, Error: msg, Retry: next.UTC()},
	})
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// explain returns the message for why an attempt failed, and the raw text
// behind it; fallback is the message for an error that explains nothing.
func explain(err error, fallback i18n.Key) (contracts.Message, string) {
	var p *contracts.Problem
	if errors.As(err, &p) {
		detail := ""
		if p.Err != nil {
			detail = p.Err.Error()
		}
		return contracts.Message{Key: p.Key, Params: p.Params}, detail
	}
	if errors.Is(err, errNoSource) {
		return contracts.Message{Key: i18n.CertsErrorNoSource}, ""
	}
	return contracts.Message{Key: fallback}, err.Error()
}

// usesProvider reports whether a certificate uses provider instance id,
// as its source, solver or a target, which may then not be deleted.
func (s *service) usesProvider(ctx context.Context, id int64) (bool, error) {
	var used bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM certs_certificates WHERE source_provider = ? OR solver_provider = ?)
		 OR EXISTS (SELECT 1 FROM certs_targets WHERE provider_id = ?)`, id, id, id).Scan(&used)
	return used, err
}

// renew is the job certs.renew. For each certificate with a source whose
// current version is due for renewal (schedule.due), or that has no
// version, it starts an issuance, unless one is under way or the last one
// failed for good less than failedPause ago.
func (s *service) renew(ctx context.Context) error {
	certs, err := s.list(ctx)
	if err != nil {
		return err
	}
	sch, err := s.schedule(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	started := false
	for _, c := range certs {
		if c.Source != sourceProvider {
			continue
		}
		if c.Current != nil && now.Before(sch.due(c)) {
			continue
		}
		if is := c.Issuance; is != nil {
			switch {
			case is.State == issuancePending, is.State == issuanceRunning, is.State == issuanceWaiting:
				continue
			case is.State == issuanceFailed && now.Sub(is.FinishedAt) < failedPause:
				continue
			}
		}
		reason := reasonRenewal
		if c.Current == nil {
			reason = reasonFirst
		}
		if err := s.startRenewal(ctx, c.ID, reason); err != nil {
			return err
		}
		started = true
	}
	if started {
		s.issuing.kick()
	}
	return nil
}

func (s *service) startRenewal(ctx context.Context, id int64, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := s.startIssuance(ctx, tx, id, reason); err != nil {
		return err
	}
	return tx.Commit()
}

// StepState is how far a step of an issuance got.
type StepState struct {
	Step  contracts.Step
	State string    // pending, running, done or failed
	At    time.Time // of its last line; zero for a pending step
}

// steps tells how far each step got in the last attempt of is, given its
// log. A step that an earlier attempt went through counts as done once the
// last attempt goes past it, as when that attempt went on with the order of
// the one before. A certificate without a solver skips the steps of the
// domain validation; a done issuance lists the steps it went through.
func steps(is *Issuance, lines []LogEntry, withSolver bool) []StepState {
	var out []StepState
	index := map[contracts.Step]int{}
	for _, st := range contracts.Steps {
		if !withSolver && (st == contracts.StepChallenge || st == contracts.StepValidate || st == contracts.StepCleanup) {
			continue
		}
		index[st] = len(out)
		out = append(out, StepState{Step: st, State: issuancePending})
	}
	var last contracts.Step
	before := map[contracts.Step]time.Time{} // the last line of each step in earlier attempts
	for _, l := range lines {
		i, ok := index[l.Step]
		switch {
		case !ok:
			continue
		case l.Attempt != is.Attempts:
			before[l.Step] = l.At
			continue
		}
		out[i].State, out[i].At = issuanceDone, l.At
		last = l.Step
	}
	if i, ok := index[last]; ok {
		for j := range i {
			if at, ok := before[out[j].Step]; ok && out[j].State == issuancePending {
				out[j].State, out[j].At = issuanceDone, at
			}
		}
		switch {
		case is.State == issuanceRunning, is.State == issuanceWaiting:
			out[i].State = issuanceRunning
		case is.State == issuanceFailed, is.State == issuancePending && is.Error.Key != "":
			out[i].State = issuanceFailed
		}
	}
	if is.State == issuanceDone {
		out = slices.DeleteFunc(out, func(st StepState) bool { return st.State == issuancePending })
	}
	return out
}
