package watch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// How sites are checked.
const (
	every        = 6 * time.Hour    // between the checks of a site
	retry        = time.Hour        // after a site did not answer
	probeTimeout = 15 * time.Second // to connect and shake hands
	soon         = 21 * 24 * time.Hour
	urgent       = 7 * 24 * time.Hour
	maxError     = 300 // characters of an error kept
	parallel     = 8   // sites checked at once
)

// States of a site.
const (
	statePending     = "pending"
	stateOK          = "ok"
	stateExpiring    = "expiring"
	stateExpired     = "expired"
	stateInvalid     = "invalid"
	stateUnreachable = "unreachable"
)

var expiryRank = map[contracts.Expiry]int{"": 0, contracts.ExpiryWindow: 1, contracts.ExpiryUrgent: 2, contracts.ExpiryExpired: 3}

// fetch connects to address for host, as a browser does, and returns the
// certificates it serves, leaf first. It does not verify them: a site that
// serves a bad certificate is to be told about, not left unseen.
func fetch(ctx context.Context, host, address string) ([]*x509.Certificate, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cfg := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // it looks at the certificate served, valid or not
	if net.ParseIP(host) == nil {
		cfg.ServerName = host
	}
	d := tls.Dialer{Config: cfg}
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, errors.New("the server sent no certificate")
	}
	return certs, nil
}

// result is what a check of a site found.
type result struct {
	state       string
	notAfter    time.Time
	issuer      string
	names       []string
	fingerprint string
	err         string
	// incomplete tells that the site sent no intermediates clients need.
	incomplete bool
}

// inspect checks what a site at host served, at now.
func (m *Module) inspect(ctx context.Context, host string, port int) result {
	certs, err := m.fetch(ctx, host, net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return result{state: stateUnreachable, err: cut(err.Error())}
	}
	leaf := certs[0]
	r := result{
		state: stateOK, notAfter: leaf.NotAfter, issuer: issuerName(leaf),
		names: cryptox.Names(leaf), fingerprint: cryptox.Fingerprint(leaf),
	}
	pool := x509.NewCertPool()
	for _, c := range certs[1:] {
		pool.AddCert(c)
	}
	now := m.now()
	opts := x509.VerifyOptions{DNSName: host, Intermediates: pool, Roots: m.roots, CurrentTime: now}
	_, verr := leaf.Verify(opts)
	// Without its intermediates: browsers on computers fetch them as the
	// certificates name them (AIA), many apps and curl do not.
	var unknown x509.UnknownAuthorityError
	if errors.As(verr, &unknown) && m.issuers != nil {
		b := &cryptox.Bundle{Leaf: leaf, Chain: cryptox.ChainOf(leaf, certs[1:])}
		if m.issuers(ctx, b) == nil {
			for _, c := range b.Chain {
				pool.AddCert(c)
			}
			if _, err := leaf.Verify(opts); err == nil {
				verr, r.incomplete = nil, true
			}
		}
	}
	switch left := leaf.NotAfter.Sub(now); {
	case left <= 0:
		r.state = stateExpired
	case verr != nil:
		r.state, r.err = stateInvalid, cut(verr.Error())
	case left <= soon:
		r.state = stateExpiring
	}
	return r
}

// incomplete is the column's value of whether a chain is incomplete.
func incomplete(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func issuerName(c *x509.Certificate) string {
	if c.Issuer.CommonName != "" {
		return c.Issuer.CommonName
	}
	if len(c.Issuer.Organization) > 0 {
		return c.Issuer.Organization[0]
	}
	return c.Issuer.String()
}

func cut(s string) string {
	if r := []rune(s); len(r) > maxError {
		return string(r[:maxError]) + "…"
	}
	return s
}

// level is how close a certificate that ends at notAfter is to its end.
func level(now, notAfter time.Time) contracts.Expiry {
	switch left := notAfter.Sub(now); {
	case left <= 0:
		return contracts.ExpiryExpired
	case left <= urgent:
		return contracts.ExpiryUrgent
	case left <= soon:
		return contracts.ExpiryWindow
	}
	return ""
}

// checkDue is the job sites.check: it checks the sites that are due.
func (m *Module) checkDue(ctx context.Context) error {
	rows, err := m.db.QueryContext(ctx, "SELECT id FROM watch_sites WHERE check_at <= ? ORDER BY check_at", m.now().UnixMilli())
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
	// A few at a time: a site that does not answer takes its full timeout.
	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)
	slots := make(chan struct{}, parallel)
	for _, id := range due {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			if err := m.check(ctx, id); err != nil && !errors.Is(err, errNotFound) {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

var errNotFound = errors.New("no such site")

// check checks the site id now, stores what it found and tells of an end
// that came closer.
func (m *Module) check(ctx context.Context, id int64) error {
	var host, name, fingerprint, notified string
	var port int
	err := m.db.QueryRowContext(ctx, "SELECT host, port, name, fingerprint, notified FROM watch_sites WHERE id = ?", id).
		Scan(&host, &port, &name, &fingerprint, &notified)
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	r := m.inspect(ctx, host, port)
	now := m.now()
	next := now.Add(every)
	if r.state == stateUnreachable {
		next = now.Add(retry)
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if r.state == stateUnreachable {
		// What it served last stays known.
		_, err = tx.ExecContext(ctx, "UPDATE watch_sites SET state = ?, error = ?, checked_at = ?, check_at = ? WHERE id = ?",
			r.state, r.err, now.UnixMilli(), next.UnixMilli(), id)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	if r.fingerprint != fingerprint {
		notified = "" // another certificate: its end is told anew
	}
	lvl := level(now, r.notAfter)
	if expiryRank[lvl] > expiryRank[contracts.Expiry(notified)] {
		err := m.events.PublishTx(ctx, tx, contracts.SiteExpiring, strconv.FormatInt(id, 10), contracts.SiteEvent{
			SiteID: id, Host: host, Port: port, Name: name, NotAfter: r.notAfter.UTC(), Expiry: lvl, Issuer: r.issuer,
		})
		if err != nil {
			return err
		}
		notified = string(lvl)
	}
	names, _ := json.Marshal(r.names)
	_, err = tx.ExecContext(ctx,
		`UPDATE watch_sites SET state = ?, not_after = ?, issuer = ?, names = ?, fingerprint = ?, error = ?, notified = ?,
		 checked_at = ?, check_at = ?, incomplete = ? WHERE id = ?`,
		r.state, r.notAfter.UnixMilli(), r.issuer, string(names), r.fingerprint, r.err, notified, now.UnixMilli(), next.UnixMilli(),
		incomplete(r.incomplete), id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
