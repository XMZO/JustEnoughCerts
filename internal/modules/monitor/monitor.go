package monitor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// What a domain served.
const (
	stateOK          = "ok"          // the version in use
	stateOld         = "old"         // an older certificate for the domain
	stateOther       = "other"       // a certificate for other names, or a newer one
	stateUnreachable = "unreachable" // nothing over TLS on port 443
)

// When domains are checked.
const (
	// afterIssue gives a new version time to be deployed before its
	// domains are checked; afterDeploy gives a target, such as a CDN, time
	// to take it.
	afterIssue  = 30 * time.Minute
	afterDeploy = 2 * time.Minute
	// grace is how long a version is current before an older one served
	// is told of.
	grace = 30 * time.Minute
	// every is how often domains are checked; again, how soon after one
	// served an older certificate.
	every = 6 * time.Hour
	again = 30 * time.Minute
	// dueAtOnce bounds the certificates one run checks; probes bounds the
	// connections at once, and probeTimeout each.
	dueAtOnce    = 20
	probes       = 4
	probeTimeout = 15 * time.Second
)

// handle keeps track of the certificates from the events of the certs
// module.
func (m *Module) handle(ctx context.Context, ev events.Event) error {
	var d contracts.CertificateEvent
	if err := ev.Decode(&d); err != nil {
		return retry.Permanent(err)
	}
	switch ev.Type {
	case contracts.CertificateIssued, contracts.CertificateCurrent:
		if d.Fingerprint == "" || len(d.Domains) == 0 {
			return nil // published before events told the fingerprint
		}
		domains, err := json.Marshal(d.Domains)
		if err != nil {
			return retry.Permanent(err)
		}
		// A new version is checked once it had time to be deployed; one
		// told of again keeps its schedule.
		check := ev.Time.Add(afterIssue)
		if ev.Type == contracts.CertificateCurrent {
			check = m.now()
		}
		_, err = m.db.ExecContext(ctx,
			`INSERT INTO monitor_certificates (certificate_id, domains, version_id, fingerprint, not_after, current_since, check_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (certificate_id) DO UPDATE SET
			   domains = excluded.domains,
			   current_since = CASE WHEN version_id = excluded.version_id THEN current_since ELSE excluded.current_since END,
			   check_at = CASE WHEN version_id = excluded.version_id THEN check_at ELSE excluded.check_at END,
			   version_id = excluded.version_id, fingerprint = excluded.fingerprint, not_after = excluded.not_after`,
			d.CertificateID, string(domains), d.VersionID, d.Fingerprint, d.NotAfter.UnixMilli(), ev.Time.UnixMilli(), check.UnixMilli())
		return err
	case contracts.CertificateDeployed:
		// The version in use reached a target: look soon.
		_, err := m.db.ExecContext(ctx,
			"UPDATE monitor_certificates SET check_at = min(check_at, ?) WHERE certificate_id = ? AND fingerprint = ?",
			m.now().Add(afterDeploy).UnixMilli(), d.CertificateID, d.Fingerprint)
		return err
	case contracts.CertificateDeleted:
		_, err := m.db.ExecContext(ctx, "DELETE FROM monitor_certificates WHERE certificate_id = ?", d.CertificateID)
		return err
	}
	return nil
}

// checkDue is the job monitor.check: it checks the certificates that are
// due, the longest due first.
func (m *Module) checkDue(ctx context.Context) error {
	rows, err := m.db.QueryContext(ctx,
		"SELECT certificate_id FROM monitor_certificates WHERE check_at <= ? ORDER BY check_at LIMIT ?", m.now().UnixMilli(), dueAtOnce)
	if err != nil {
		return err
	}
	var due []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		due = append(due, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, id := range due {
		if _, err := m.check(ctx, id); err != nil && !errors.Is(err, errUnknown) {
			return err
		}
	}
	return nil
}

var errUnknown = errors.New("no such certificate")

// tracked is a certificate as the monitor knows it.
type tracked struct {
	id           int64
	domains      []string
	version      int64
	fingerprint  string
	notAfter     time.Time
	currentSince time.Time
	checkAt      time.Time
}

func (m *Module) load(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64,
) (tracked, error) {
	t := tracked{id: id}
	var domains string
	var notAfter, since, checkAt int64
	err := q.QueryRowContext(ctx,
		"SELECT domains, version_id, fingerprint, not_after, current_since, check_at FROM monitor_certificates WHERE certificate_id = ?", id).
		Scan(&domains, &t.version, &t.fingerprint, &notAfter, &since, &checkAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, errUnknown
	}
	if err != nil {
		return t, err
	}
	t.notAfter, t.currentSince, t.checkAt = time.UnixMilli(notAfter), time.UnixMilli(since), time.UnixMilli(checkAt)
	return t, json.Unmarshal([]byte(domains), &t.domains)
}

// result is what a domain served.
type result struct {
	domain      string
	state       string
	fingerprint string
	notAfter    time.Time
	issuer      string
	err         string
}

// check connects to each domain of certificate id, but wildcards, stores
// what they served, tells of a domain that still serves an older
// certificate once the grace is over, and sets the next check.
func (m *Module) check(ctx context.Context, id int64) ([]result, error) {
	t, err := m.load(ctx, m.db, id)
	if err != nil {
		return nil, err
	}
	var domains []string
	for _, d := range t.domains {
		if !strings.HasPrefix(d, "*.") {
			domains = append(domains, d)
		}
	}
	results := make([]result, len(domains))
	var wg sync.WaitGroup
	slots := make(chan struct{}, probes)
	for i, d := range domains {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			results[i] = m.probe(ctx, t, d)
		})
	}
	wg.Wait()

	now := m.now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// The certificate may have changed or gone while connecting.
	if cur, err := m.load(ctx, tx, id); err != nil || cur.fingerprint != t.fingerprint {
		if err != nil {
			return nil, err
		}
		return results, nil
	}
	next := now.Add(every)
	settled := !now.Before(t.currentSince.Add(grace))
	for _, r := range results {
		var alerted string
		if err := tx.QueryRowContext(ctx, "SELECT alerted FROM monitor_results WHERE certificate_id = ? AND domain = ?", id, r.domain).Scan(&alerted); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		switch {
		case r.state == stateOld && settled && alerted != t.fingerprint:
			err := m.events.PublishTx(ctx, tx, contracts.CertificateServedOld, strconv.FormatInt(id, 10), contracts.CertificateEvent{
				CertificateID: id, Domains: t.domains, VersionID: t.version, Fingerprint: t.fingerprint, NotAfter: t.notAfter.UTC(),
				Check: &contracts.Check{Domain: r.domain, Fingerprint: r.fingerprint, NotAfter: r.notAfter.UTC()},
			})
			if err != nil {
				return nil, err
			}
			alerted = t.fingerprint
		case r.state == stateOK:
			alerted = ""
		}
		if r.state == stateOld {
			// When the grace ends, to tell of it then, or soon again.
			if settled {
				next = earliest(next, now.Add(again))
			} else {
				next = earliest(next, t.currentSince.Add(grace))
			}
		}
		var notAfter int64
		if !r.notAfter.IsZero() {
			notAfter = r.notAfter.UnixMilli()
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO monitor_results (certificate_id, domain, state, fingerprint, not_after, issuer, error, checked_at, alerted)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (certificate_id, domain) DO UPDATE SET state = excluded.state, fingerprint = excluded.fingerprint,
			   not_after = excluded.not_after, issuer = excluded.issuer, error = excluded.error, checked_at = excluded.checked_at,
			   alerted = excluded.alerted`,
			id, r.domain, r.state, r.fingerprint, notAfter, r.issuer, r.err, now.UnixMilli(), alerted); err != nil {
			return nil, err
		}
	}
	// Domains the certificate no longer has.
	if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_results WHERE certificate_id = ? AND domain NOT IN (SELECT value FROM json_each(?))`,
		id, mustJSON(domains)); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE monitor_certificates SET check_at = ? WHERE certificate_id = ?", next.UnixMilli(), id); err != nil {
		return nil, err
	}
	return results, tx.Commit()
}

// probe connects to domain and says what it served, compared with the
// version t uses.
func (m *Module) probe(ctx context.Context, t tracked, domain string) result {
	r := result{domain: domain}
	leaf, err := m.serve(ctx, domain)
	if err != nil {
		r.state, r.err = stateUnreachable, truncate(err.Error(), 500)
		return r
	}
	r.fingerprint, r.notAfter, r.issuer = cryptox.Fingerprint(leaf), leaf.NotAfter, issuerName(leaf)
	switch {
	case r.fingerprint == t.fingerprint:
		r.state = stateOK
	case leaf.VerifyHostname(domain) == nil && leaf.NotAfter.Before(t.notAfter):
		r.state = stateOld
	default:
		r.state = stateOther
	}
	return r
}

// served connects to domain on port 443 as a browser does and returns the
// leaf of what it serves.
func served(ctx context.Context, domain string) (*x509.Certificate, error) {
	return servedAt(ctx, domain, net.JoinHostPort(domain, "443"))
}

// servedAt connects to address for domain and returns the leaf it serves.
// It does not verify it: what matters is which certificate it is, valid or
// not.
func servedAt(ctx context.Context, domain, address string) (*x509.Certificate, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	d := tls.Dialer{Config: &tls.Config{ServerName: domain, InsecureSkipVerify: true}} //nolint:gosec // it looks at the certificate served, valid or not
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, errors.New("the server sent no certificate")
	}
	return certs[0], nil
}

// issuerName is what the UI shows of the issuer of a certificate.
func issuerName(c *x509.Certificate) string {
	if c.Issuer.CommonName != "" {
		return c.Issuer.CommonName
	}
	if len(c.Issuer.Organization) > 0 {
		return c.Issuer.Organization[0]
	}
	return c.Issuer.String()
}

func earliest(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("monitor: %v", err))
	}
	return string(data)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
