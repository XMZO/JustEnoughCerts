package certs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
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
	maxAttempts    = 8                // of an issuance before it fails for good
	retryBase      = 10 * time.Minute // the wait after the first failed attempt, doubled after each
	retryLimit     = 12 * time.Hour
	retryAfterMax  = 24 * time.Hour // the longest wait a source may ask for
	// failedPause is how long renewal leaves a certificate alone after an
	// issuance of it failed for good.
	failedPause  = 24 * time.Hour
	maxDetailLen = 4000 // bytes of raw text kept with a log line
)

// errNoSource: the certificate gets new versions by upload only.
var errNoSource = errors.New("the certificate has no source to issue it")

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
	list, err := s.queryIssuances(ctx, q, "WHERE i.certificate_id = ? AND i.state IN ('pending', 'running')", certificateID)
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
// retry.
func (s *service) startIssuance(ctx context.Context, tx *sql.Tx, certificateID int64, reason string) (int64, error) {
	now := s.now().UnixMilli()
	open, err := s.openIssuance(ctx, tx, certificateID)
	if err != nil {
		return 0, err
	}
	if open != nil {
		if open.State == issuancePending {
			_, err = tx.ExecContext(ctx, "UPDATE certs_issuances SET next_at = ? WHERE id = ?", now, open.ID)
		}
		return open.ID, err
	}
	var id int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO certs_issuances (certificate_id, reason, state, attempts, next_at, error_key, error_params, created_at)
		 VALUES (?, ?, 'pending', 0, ?, '', '', ?) RETURNING id`,
		certificateID, reason, now, now).Scan(&id)
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
	s.kick()
	return issuance, nil
}

// kick tells the issuance runner that there is work: it picks the work up
// at once if it runs, and starts otherwise.
func (s *service) kick() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
	s.trigger()
}

// stopAttempt cancels the attempt under way for certificate id, if any.
func (s *service) stopAttempt(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel := s.running[id]; cancel != nil {
		cancel()
	}
}

// claimed is an issuance an attempt was started for.
type claimed struct {
	id, certificateID int64
	attempt           int
}

// runIssuances is the job certs.issue. It runs the due issuances, at most
// maxRunning at once, starts those that become due while others run, and
// returns once none is due or under way. As the job never overlaps itself,
// an issuance still marked running when it starts was cut short by a stop
// of JEC, and starts over.
func (s *service) runIssuances(ctx context.Context) error {
	if err := s.resume(ctx); err != nil {
		return err
	}
	ended := make(chan struct{}, maxRunning)
	var wg sync.WaitGroup
	defer wg.Wait()
	running := 0
	for {
		for running < maxRunning {
			c, err := s.claim(ctx)
			if err != nil {
				return err
			}
			if c == nil {
				break
			}
			running++
			attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
			s.mu.Lock()
			s.running[c.certificateID] = cancel
			s.mu.Unlock()
			wg.Go(func() {
				defer func() {
					s.mu.Lock()
					delete(s.running, c.certificateID)
					s.mu.Unlock()
					cancel()
					ended <- struct{}{}
				}()
				s.attempt(ctx, attemptCtx, c)
			})
		}
		if running == 0 {
			return nil
		}
		select {
		case <-ended:
			running--
		case <-s.wake:
		case <-ctx.Done():
			return nil
		}
	}
}

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
		s.logger(c.id, c.attempt).add(contracts.LogLine{Level: contracts.Warn, Key: i18n.CertsLogInterrupted})
	}
	return nil
}

// claim marks the issuance due the longest as running, counting the
// attempt, and returns it; nil if none is due.
func (s *service) claim(ctx context.Context) (*claimed, error) {
	now := s.now().UnixMilli()
	c := &claimed{}
	err := s.db.QueryRowContext(ctx,
		`UPDATE certs_issuances SET state = 'running', attempts = attempts + 1, started_at = ?
		 WHERE id = (SELECT id FROM certs_issuances WHERE state = 'pending' AND next_at <= ? ORDER BY next_at, id LIMIT 1)
		 RETURNING id, certificate_id, attempts`, now, now).Scan(&c.id, &c.certificateID, &c.attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

// attempt tries once to issue the certificate of c, within attemptCtx. The
// job's ctx ending means that JEC stops: the issuance then stays running,
// to start over after the restart.
func (s *service) attempt(ctx, attemptCtx context.Context, c *claimed) {
	log := s.logger(c.id, c.attempt)
	if c.attempt > 1 {
		log.add(contracts.LogLine{Level: contracts.Info, Key: i18n.CertsLogAttempt, Params: map[string]any{"attempt": c.attempt}})
	}
	start := s.now()
	b, err := s.obtain(attemptCtx, c.certificateID, log.add)
	if err == nil {
		// Store what the source gave, even if JEC is stopping now.
		storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		err = s.complete(storeCtx, c, b, log)
		cancel()
		if err == nil {
			seconds := int(s.now().Sub(start).Round(time.Second) / time.Second)
			log.add(contracts.LogLine{Level: contracts.OK, Key: i18n.CertsLogDone, Params: map[string]any{"seconds": seconds}})
			return
		}
	}
	if ctx.Err() != nil || errors.Is(err, errNotFound) {
		return // stopping, or the certificate was deleted
	}
	s.fail(context.WithoutCancel(ctx), c, err, log)
}

// obtain gets a new certificate for certificate id from its source, with a
// new private key.
func (s *service) obtain(ctx context.Context, id int64, log func(contracts.LogLine)) (*cryptox.Bundle, error) {
	c, err := s.load(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	if c.Source != sourceProvider {
		return nil, retry.Permanent(errNoSource)
	}
	kind, size, _ := strings.Cut(c.KeyType, "-")
	log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepKey, Key: i18n.CertsLogKey, Params: map[string]any{"kind": kind, "size": keySize(size)}})
	key, err := cryptox.GenerateKey(c.KeyType)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	source, err := openProvider[contracts.Source](ctx, s.providers, c.SourceProvider)
	if err != nil {
		return nil, err
	}
	defer closeProvider(source, s)
	var solver contracts.Solver
	if c.SolverProvider != 0 {
		if solver, err = openProvider[contracts.Solver](ctx, s.providers, c.SolverProvider); err != nil {
			return nil, err
		}
		defer closeProvider(solver, s)
	}
	chain, err := source.Issue(ctx, contracts.IssueRequest{Domains: c.Domains, Key: key, Solver: solver, Log: log})
	if err != nil {
		return nil, err
	}
	b, err := cryptox.NewBundle(chain, key)
	if err != nil {
		return nil, fmt.Errorf("the certificate the source returned: %w", err)
	}
	if missing := uncovered(c.Domains, cryptox.Names(b.Leaf)); len(missing) > 0 {
		return nil, &contracts.Problem{Key: i18n.CertsErrorNamesMissing, Params: map[string]any{"domains": strings.Join(missing, ", ")}}
	}
	return b, nil
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
func (s *service) complete(ctx context.Context, c *claimed, b *cryptox.Bundle, log *issuanceLog) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	version, err := s.addVersion(ctx, tx, c.certificateID, b, originIssued)
	if err != nil {
		return err
	}
	now := s.now().UnixMilli()
	if _, err := tx.ExecContext(ctx, "UPDATE certs_certificates SET updated_at = ? WHERE id = ?", now, c.certificateID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE certs_issuances SET state = 'done', version_id = ?, finished_at = ?, error_key = '', error_params = ''
		 WHERE id = ? AND state = 'running'`, version, now, c.id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound // deleted meanwhile
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	days := int(b.Leaf.NotAfter.Sub(s.now()) / day)
	log.add(contracts.LogLine{Level: contracts.Info, Step: contracts.StepDownload, Key: i18n.CertsLogStored, Params: map[string]any{"days": days}})
	return nil
}

// fail records a failed attempt: the issuance retries after a backoff, or
// fails for good when retrying cannot help or the attempts ran out.
func (s *service) fail(ctx context.Context, c *claimed, cause error, log *issuanceLog) {
	msg, detail := explain(cause)
	log.add(contracts.LogLine{Level: contracts.Error, Step: log.current(), Key: msg.Key, Params: msg.Params, Detail: detail})

	final := retry.IsPermanent(cause) || c.attempt >= maxAttempts
	now := s.now()
	var next time.Time
	if !final {
		wait := retry.Backoff(c.attempt, retryBase, retryLimit)
		if d, ok := retry.Delay(cause); ok {
			wait = max(wait, min(d, retryAfterMax))
		}
		next = now.Add(wait).Truncate(time.Millisecond) // as stored
	}
	recorded, err := s.recordFailure(ctx, c, msg, now, next)
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
// one, zero when none follows, and publishes certificate.issue_failed. It
// reports false when the issuance is gone, as its certificate was deleted.
func (s *service) recordFailure(ctx context.Context, c *claimed, msg contracts.Message, now, next time.Time) (bool, error) {
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
	var res sql.Result
	if next.IsZero() {
		res, err = tx.ExecContext(ctx,
			`UPDATE certs_issuances SET state = 'failed', finished_at = ?, error_key = ?, error_params = ?
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
// behind it.
func explain(err error) (contracts.Message, string) {
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
	return contracts.Message{Key: i18n.CertsErrorIssueFailed}, err.Error()
}

// usesProvider reports whether a certificate uses provider instance id,
// which may then not be deleted.
func (s *service) usesProvider(ctx context.Context, id int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		"SELECT count(*) FROM certs_certificates WHERE source_provider = ? OR solver_provider = ?", id, id).Scan(&n)
	return n > 0, err
}

// renew is the job certs.renew. For each certificate with a source whose
// current version entered its renewal window, or that has no version, it
// starts an issuance, unless one is under way or the last one failed for
// good less than failedPause ago.
func (s *service) renew(ctx context.Context) error {
	certs, err := s.list(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	started := false
	for _, c := range certs {
		if c.Source != sourceProvider {
			continue
		}
		if c.Current != nil && c.Current.NotAfter.Sub(now) > time.Duration(c.RenewBeforeDays)*day {
			continue
		}
		if is := c.Issuance; is != nil {
			switch {
			case is.State == issuancePending, is.State == issuanceRunning:
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
		s.kick()
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

// issuanceLog appends the lines of an attempt to an issuance's log. It
// is safe for concurrent use.
type issuanceLog struct {
	s       *service
	id      int64
	attempt int

	mu   sync.Mutex
	step contracts.Step // of the last line that had one
}

func (s *service) logger(id int64, attempt int) *issuanceLog {
	return &issuanceLog{s: s, id: id, attempt: attempt}
}

// current returns the step of the last line that had one.
func (l *issuanceLog) current() contracts.Step {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.step
}

// add stores line. A line that cannot be stored is logged instead: the log
// is for people, and the issuance goes on without it.
func (l *issuanceLog) add(line contracts.LogLine) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if line.Step != "" {
		l.step = line.Step
	}
	params := ""
	if len(line.Params) > 0 {
		b, err := json.Marshal(line.Params)
		if err != nil {
			l.s.log.Warn("issuance log params", "key", line.Key, "err", err)
		} else {
			params = string(b)
		}
	}
	detail := line.Detail
	if len(detail) > maxDetailLen {
		detail = detail[:maxDetailLen]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := l.s.db.ExecContext(ctx,
		`INSERT INTO certs_issuance_log (issuance_id, seq, at, attempt, level, step, key, params, detail)
		 VALUES (?, (SELECT coalesce(max(seq), 0) + 1 FROM certs_issuance_log WHERE issuance_id = ?), ?, ?, ?, ?, ?, ?, ?)`,
		l.id, l.id, l.s.now().UnixMilli(), l.attempt, string(line.Level), string(line.Step), string(line.Key), params, detail)
	if err != nil {
		l.s.log.Warn("issuance log", "issuance", l.id, "key", line.Key, "err", err)
	}
}

// LogEntry is a stored line of an issuance's log.
type LogEntry struct {
	Seq     int
	At      time.Time
	Attempt int
	contracts.LogLine
}

// issuanceLines returns the log of issuance id, in order.
func (s *service) issuanceLines(ctx context.Context, id int64) ([]LogEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT seq, at, attempt, level, step, key, params, detail FROM certs_issuance_log WHERE issuance_id = ? ORDER BY seq", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogEntry
	for rows.Next() {
		var e LogEntry
		var at int64
		var level, step, params string
		// The key is scanned as it is: it was stored from a constant.
		if err := rows.Scan(&e.Seq, &at, &e.Attempt, &level, &step, &e.Key, &params, &e.Detail); err != nil {
			return nil, err
		}
		e.At = time.UnixMilli(at).UTC()
		e.Level, e.Step = contracts.Level(level), contracts.Step(step)
		if params != "" {
			if err := json.Unmarshal([]byte(params), &e.Params); err != nil {
				return nil, err
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// StepState is how far a step of an issuance got.
type StepState struct {
	Step  contracts.Step
	State string    // pending, running, done or failed
	At    time.Time // of its last line; zero for a pending step
}

// steps tells how far each step got in the last attempt of is, given its
// log. A certificate without a solver skips the steps of the domain
// validation; a done issuance lists the steps it went through.
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
	for _, l := range lines {
		i, ok := index[l.Step]
		if l.Attempt != is.Attempts || !ok {
			continue
		}
		out[i].State, out[i].At = issuanceDone, l.At
		last = l.Step
	}
	if i, ok := index[last]; ok {
		switch {
		case is.State == issuanceRunning:
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
