package certs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

// How deployments run.
const (
	maxDeploying  = 4                // attempts under way at once
	deployTimeout = 10 * time.Minute // bounds an attempt
)

// Why a deployment started.
const (
	deployVersion = "version" // a new version came
	deployTarget  = "target"  // the target was added to the certificate
	deployManual  = "manual"  // someone asked to deploy now
)

// Deployment installs a version of a certificate at one of its targets.
// Its states are an issuance's, and canceled: its target was taken off the
// certificate, or a newer version took its place.
type Deployment struct {
	ID            int64
	CertificateID int64
	VersionID     int64
	Target        int64 // the provider instance
	Reason        string
	State         string
	Attempts      int
	NextAt        time.Time // for a pending deployment
	// Ref names what the target holds afterwards; Removed tells that a
	// later deployment removed it.
	Ref     string
	Removed bool
	// Error tells why the last attempt failed; its Key is empty if none did.
	Error      contracts.Message
	CreatedAt  time.Time
	StartedAt  time.Time // of the last attempt; zero before the first
	FinishedAt time.Time
}

const deploymentColumns = `d.id, d.certificate_id, d.version_id, d.provider_id, d.reason, d.state, d.attempts, d.next_at, d.ref,
	d.removed_at, d.error_key, d.error_params, d.created_at, d.started_at, d.finished_at`

func (s *service) queryDeployments(ctx context.Context, q querier, where string, args ...any) ([]*Deployment, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+deploymentColumns+" FROM certs_deployments d "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Deployment
	for rows.Next() {
		d := &Deployment{}
		var nextAt, created int64
		var removed, started, finished sql.NullInt64
		var params string
		if err := rows.Scan(&d.ID, &d.CertificateID, &d.VersionID, &d.Target, &d.Reason, &d.State, &d.Attempts, &nextAt, &d.Ref,
			&removed, &d.Error.Key, &params, &created, &started, &finished); err != nil {
			return nil, err
		}
		d.NextAt, d.CreatedAt, d.Removed = time.UnixMilli(nextAt).UTC(), time.UnixMilli(created).UTC(), removed.Valid
		if started.Valid {
			d.StartedAt = time.UnixMilli(started.Int64).UTC()
		}
		if finished.Valid {
			d.FinishedAt = time.UnixMilli(finished.Int64).UTC()
		}
		if params != "" {
			if err := json.Unmarshal([]byte(params), &d.Error.Params); err != nil {
				return nil, fmt.Errorf("deployment %d: error params: %w", d.ID, err)
			}
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// deployment returns deployment id.
func (s *service) deployment(ctx context.Context, id int64) (*Deployment, error) {
	list, err := s.queryDeployments(ctx, s.db, "WHERE d.id = ?", id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errNotFound
	}
	return list[0], nil
}

// attachTargets fills in the targets of certs and the latest deployment
// to each.
func (s *service) attachTargets(ctx context.Context, q querier, certs []*Certificate) error {
	if len(certs) == 0 {
		return nil
	}
	byID := map[int64]*Certificate{}
	ids := make([]any, 0, len(certs))
	for _, c := range certs {
		byID[c.ID] = c
		ids = append(ids, c.ID)
	}
	in := "(" + strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ") + ")"
	rows, err := q.QueryContext(ctx,
		"SELECT certificate_id, provider_id FROM certs_targets WHERE certificate_id IN "+in+" ORDER BY certificate_id, position", ids...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cert, target int64
		if err := rows.Scan(&cert, &target); err != nil {
			_ = rows.Close()
			return err
		}
		byID[cert].Targets = append(byID[cert].Targets, target)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	latest, err := s.queryDeployments(ctx, q,
		"WHERE d.id IN (SELECT max(id) FROM certs_deployments WHERE certificate_id IN "+in+" GROUP BY certificate_id, provider_id)", ids...)
	if err != nil {
		return err
	}
	for _, c := range certs {
		for _, target := range c.Targets {
			i := slices.IndexFunc(latest, func(d *Deployment) bool { return d.CertificateID == c.ID && d.Target == target })
			if i >= 0 {
				c.Deployments = append(c.Deployments, latest[i])
			}
		}
	}
	return nil
}

// setTargets makes targets those of certificate c, in tx. A target taken
// off loses its waiting deployment; one added gets the current version.
func (s *service) setTargets(ctx context.Context, tx *sql.Tx, c *Certificate, targets []int64) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM certs_targets WHERE certificate_id = ?", c.ID); err != nil {
		return err
	}
	for i, target := range targets {
		if _, err := tx.ExecContext(ctx, "INSERT INTO certs_targets (certificate_id, provider_id, position) VALUES (?, ?, ?)",
			c.ID, target, i); err != nil {
			return err
		}
	}
	now := s.now().UnixMilli()
	for _, old := range c.Targets {
		if slices.Contains(targets, old) {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE certs_deployments SET state = 'canceled', finished_at = ? WHERE certificate_id = ? AND provider_id = ? AND state = 'pending'",
			now, c.ID, old); err != nil {
			return err
		}
	}
	if c.Current == nil {
		return nil
	}
	for _, target := range targets {
		if !slices.Contains(c.Targets, target) {
			if err := s.queueDeployment(ctx, tx, c.ID, c.Current.ID, target, deployTarget); err != nil {
				return err
			}
		}
	}
	return nil
}

// queueDeployments queues version of certificate id for each of its
// targets, in tx.
func (s *service) queueDeployments(ctx context.Context, tx *sql.Tx, id, version int64) error {
	rows, err := tx.QueryContext(ctx, "SELECT provider_id FROM certs_targets WHERE certificate_id = ? ORDER BY position", id)
	if err != nil {
		return err
	}
	var targets []int64
	for rows.Next() {
		var target int64
		if err := rows.Scan(&target); err != nil {
			_ = rows.Close()
			return err
		}
		targets = append(targets, target)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, target := range targets {
		if err := s.queueDeployment(ctx, tx, id, version, target, deployVersion); err != nil {
			return err
		}
	}
	return nil
}

// queueDeployment makes the deployment of version of certificate id to
// target due now, for reason, in tx: a deployment waiting for that target
// takes the version on, as a fresh start, and otherwise a new one is
// added.
func (s *service) queueDeployment(ctx context.Context, tx *sql.Tx, id, version, target int64, reason string) error {
	now := s.now().UnixMilli()
	res, err := tx.ExecContext(ctx,
		`UPDATE certs_deployments SET version_id = ?, reason = ?, attempts = 0, next_at = ?, error_key = '', error_params = '', created_at = ?
		 WHERE certificate_id = ? AND provider_id = ? AND state = 'pending'`, version, reason, now, now, id, target)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO certs_deployments (certificate_id, version_id, provider_id, reason, state, attempts, next_at, ref, error_key, error_params, created_at)
		 VALUES (?, ?, ?, ?, 'pending', 0, ?, '', '', '', ?)`, id, version, target, reason, now, now)
	return err
}

// deployNow queues the current version of certificate id for target, or
// for every target when target is 0, as a person asked.
func (s *service) deployNow(ctx context.Context, id, target int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := s.load(ctx, tx, id)
	if err != nil {
		return err
	}
	switch {
	case c.Current == nil:
		return errNoVersion
	case target != 0 && !slices.Contains(c.Targets, target):
		return errNotFound
	}
	for _, t := range c.Targets {
		if target == 0 || t == target {
			if err := s.queueDeployment(ctx, tx, id, c.Current.ID, t, deployManual); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.deploying.kick()
	return nil
}

// deployClaim is a deployment an attempt was started for.
type deployClaim struct {
	id, certificateID, versionID, target int64
	attempt                              int
}

// runDeployments is the job certs.deploy: it runs the due deployments (see
// queue).
func (s *service) runDeployments(ctx context.Context) error { return s.deploying.run(ctx, s) }

// resumeDeployments makes the deployments that a stop of JEC cut short
// due again, unless a newer one for the same target waits already.
func (s *service) resumeDeployments(ctx context.Context) error {
	now := s.now().UnixMilli()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE certs_deployments SET state = 'canceled', finished_at = ? WHERE state = 'running' AND EXISTS (
		   SELECT 1 FROM certs_deployments p WHERE p.certificate_id = certs_deployments.certificate_id
		   AND p.provider_id = certs_deployments.provider_id AND p.state = 'pending')`, now); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx,
		"UPDATE certs_deployments SET state = 'pending', next_at = ? WHERE state = 'running' RETURNING id, attempts", now)
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
		s.taskLogger(deploymentLogTable, c.id, c.attempt).add(contracts.LogLine{Level: contracts.Warn, Key: i18n.CertsLogInterrupted})
	}
	return nil
}

// claimDeployment marks the deployment due the longest as running,
// counting the attempt, and returns it; nil if none is due. A target gets
// one deployment of a certificate at a time.
func (s *service) claimDeployment(ctx context.Context) (*deployClaim, error) {
	now := s.now().UnixMilli()
	c := &deployClaim{}
	err := s.db.QueryRowContext(ctx,
		`UPDATE certs_deployments SET state = 'running', attempts = attempts + 1, started_at = ?
		 WHERE id = (SELECT d.id FROM certs_deployments d WHERE d.state = 'pending' AND d.next_at <= ? AND NOT EXISTS (
		   SELECT 1 FROM certs_deployments r WHERE r.certificate_id = d.certificate_id AND r.provider_id = d.provider_id
		   AND r.state = 'running') ORDER BY d.next_at, d.id LIMIT 1)
		 RETURNING id, certificate_id, version_id, provider_id, attempts`, now, now).
		Scan(&c.id, &c.certificateID, &c.versionID, &c.target, &c.attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

// attemptDeployment tries once to install the version of c at its target.
func (s *service) attemptDeployment(ctx, attemptCtx context.Context, c *deployClaim) {
	log := s.taskLogger(deploymentLogTable, c.id, c.attempt)
	if c.attempt > 1 {
		log.add(contracts.LogLine{Level: contracts.Info, Key: i18n.CertsLogAttempt, Params: map[string]any{"attempt": c.attempt}})
	}
	start := s.now()
	res, err := s.deploy(attemptCtx, c, log)
	if err == nil {
		storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		err = s.completeDeployment(storeCtx, c, res)
		cancel()
		if err == nil {
			seconds := int(s.now().Sub(start).Round(time.Second) / time.Second)
			log.add(contracts.LogLine{Level: contracts.OK, Key: i18n.CertsLogDeployed, Params: map[string]any{"seconds": seconds}})
			return
		}
	}
	if ctx.Err() != nil || errors.Is(err, errNotFound) {
		return // stopping, or the certificate was deleted
	}
	s.failDeployment(context.WithoutCancel(ctx), c, err, log)
}

// deploy hands the version of c to its target.
func (s *service) deploy(ctx context.Context, c *deployClaim, log *taskLog) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	cert, err := s.load(ctx, s.db, c.certificateID)
	if err != nil {
		return none, err
	}
	var chainPEM, fingerprint string
	var sealed []byte
	err = s.db.QueryRowContext(ctx, "SELECT chain, sealed_key, fingerprint FROM certs_versions WHERE id = ?", c.versionID).
		Scan(&chainPEM, &sealed, &fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return none, errNotFound
	}
	if err != nil {
		return none, err
	}
	keyPEM, err := s.secrets.Open(sealed, "key:"+fingerprint)
	if err != nil {
		return none, retry.Permanent(fmt.Errorf("open the private key of version %d: %w", c.versionID, err))
	}
	key, err := cryptox.ParseKey(keyPEM)
	if err != nil {
		return none, retry.Permanent(err)
	}
	chain, err := cryptox.ParseCertificates([]byte(chainPEM))
	if err != nil {
		return none, retry.Permanent(err)
	}
	previous, err := s.previousRefs(ctx, c)
	if err != nil {
		return none, err
	}

	p, in, err := s.providers.Open(ctx, c.target)
	switch {
	case errors.Is(err, providers.ErrNotFound):
		return none, retry.Permanent(&contracts.Problem{Key: i18n.CertsErrorProviderMissing, Err: err})
	case errors.Is(err, providers.ErrUnavailable):
		return none, &contracts.Problem{Key: i18n.CertsErrorProviderUnavailable, Err: err}
	case err != nil:
		return none, err
	}
	target, ok := p.(contracts.Target)
	if !ok {
		return none, retry.Permanent(fmt.Errorf("provider %d is no target", c.target))
	}
	defer closeProvider(target, s)
	log.add(contracts.LogLine{Level: contracts.Info, Key: i18n.CertsLogDeploy, Params: map[string]any{"target": in.Name}})
	return target.Deploy(ctx, contracts.DeployRequest{
		CertificateID: c.certificateID, Domains: cert.Domains, Chain: chain, Key: key, Previous: previous, Log: log.add,
	})
}

// previousRefs returns what earlier deployments of the certificate of c
// left at its target and no later one removed, the newest first.
func (s *service) previousRefs(ctx context.Context, c *deployClaim) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ref FROM certs_deployments WHERE certificate_id = ? AND provider_id = ? AND state = 'done'
		 AND ref != '' AND removed_at IS NULL AND id != ? ORDER BY id DESC`, c.certificateID, c.target, c.id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
	return refs, rows.Err()
}

// completeDeployment records a deployment that succeeded and publishes
// certificate.deployed.
func (s *service) completeDeployment(ctx context.Context, c *deployClaim, res contracts.DeployResult) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.now().UnixMilli()
	r, err := tx.ExecContext(ctx,
		`UPDATE certs_deployments SET state = 'done', ref = ?, finished_at = ?, error_key = '', error_params = ''
		 WHERE id = ? AND state = 'running'`, res.Ref, now, c.id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return errNotFound // deleted meanwhile
	}
	for _, ref := range res.Removed {
		if _, err := tx.ExecContext(ctx,
			`UPDATE certs_deployments SET removed_at = ? WHERE certificate_id = ? AND provider_id = ? AND ref = ? AND state = 'done' AND id != ?`,
			now, c.certificateID, c.target, ref, c.id); err != nil {
			return err
		}
	}
	cert, err := s.load(ctx, tx, c.certificateID)
	if err != nil {
		return err
	}
	err = s.events.PublishTx(ctx, tx, contracts.CertificateDeployed, strconv.FormatInt(c.certificateID, 10), contracts.CertificateEvent{
		CertificateID: c.certificateID, Domains: cert.Domains, VersionID: c.versionID,
		Deployment: &contracts.Deployment{ID: c.id, Target: c.target, Attempt: c.attempt},
	})
	if err != nil {
		return err
	}
	return tx.Commit()
}

// failDeployment records a failed attempt, as fail does for an issuance.
// When a newer version waits for the same target, the deployment ends
// instead of retrying.
func (s *service) failDeployment(ctx context.Context, c *deployClaim, cause error, log *taskLog) {
	msg, detail := explain(cause, i18n.CertsErrorDeployFailed)
	log.add(contracts.LogLine{Level: contracts.Error, Step: log.current(), Key: msg.Key, Params: msg.Params, Detail: detail})
	now := s.now()
	final, next := nextAttempt(c.attempt, cause, now)
	params := ""
	if len(msg.Params) > 0 {
		b, _ := json.Marshal(msg.Params)
		params = string(b)
	}
	recorded, err := func() (bool, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return false, err
		}
		defer func() { _ = tx.Rollback() }()
		var newer bool
		if err := tx.QueryRowContext(ctx,
			"SELECT EXISTS (SELECT 1 FROM certs_deployments WHERE certificate_id = ? AND provider_id = ? AND state = 'pending')",
			c.certificateID, c.target).Scan(&newer); err != nil {
			return false, err
		}
		var res sql.Result
		switch {
		case newer:
			final, next = true, time.Time{}
			res, err = tx.ExecContext(ctx,
				`UPDATE certs_deployments SET state = 'canceled', finished_at = ?, error_key = ?, error_params = ? WHERE id = ? AND state = 'running'`,
				now.UnixMilli(), string(msg.Key), params, c.id)
		case final:
			res, err = tx.ExecContext(ctx,
				`UPDATE certs_deployments SET state = 'failed', finished_at = ?, error_key = ?, error_params = ? WHERE id = ? AND state = 'running'`,
				now.UnixMilli(), string(msg.Key), params, c.id)
		default:
			res, err = tx.ExecContext(ctx,
				`UPDATE certs_deployments SET state = 'pending', next_at = ?, error_key = ?, error_params = ? WHERE id = ? AND state = 'running'`,
				next.UnixMilli(), string(msg.Key), params, c.id)
		}
		if err != nil {
			return false, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return false, nil // deleted meanwhile
		}
		cert, err := s.load(ctx, tx, c.certificateID)
		if err != nil {
			return false, err
		}
		err = s.events.PublishTx(ctx, tx, contracts.CertificateDeployFailed, strconv.FormatInt(c.certificateID, 10), contracts.CertificateEvent{
			CertificateID: c.certificateID, Domains: cert.Domains, VersionID: c.versionID,
			Deployment: &contracts.Deployment{ID: c.id, Target: c.target, Attempt: c.attempt, Error: msg, Retry: next.UTC()},
		})
		if err != nil {
			return false, err
		}
		return true, tx.Commit()
	}()
	if err != nil {
		s.log.Error("record failed deployment", "deployment", c.id, "err", err)
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

// nextAttempt says, after attempt failed with cause at now, whether no
// attempt follows, or when the next one starts.
func nextAttempt(attempt int, cause error, now time.Time) (final bool, next time.Time) {
	if retry.IsPermanent(cause) || attempt >= maxAttempts {
		return true, time.Time{}
	}
	wait := retry.Backoff(attempt, retryBase, retryLimit)
	if d, ok := retry.Delay(cause); ok {
		wait = max(wait, min(d, retryAfterMax))
	}
	return false, now.Add(wait).Truncate(time.Millisecond) // as stored
}
