package monitor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/smtp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/dnscheck"
)

// What a domain served.
const (
	stateOK          = "ok"          // the version in use
	stateOld         = "old"         // an older certificate for the domain, from the same CA
	stateOther       = "other"       // someone else's certificate, a CDN's own say, or a newer one
	stateUnreachable = "unreachable" // nothing over TLS on port 443
)

// How visitors reach a domain: one way for all, or, where its DNS answers
// by region, one way for mainland China and another for elsewhere.
const (
	viewAll    = ""
	viewChina  = "cn"
	viewGlobal = "global"
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
		// A new version is checked once it had time to be deployed, which
		// a deploy window may put off; one told of again keeps its
		// schedule.
		since := ev.Time
		if d.DeployAt.After(since) {
			since = d.DeployAt
		}
		check := since.Add(afterIssue)
		if ev.Type == contracts.CertificateCurrent {
			check = m.now()
		}
		_, err = m.db.ExecContext(ctx,
			`INSERT INTO monitor_certificates (certificate_id, domains, version_id, fingerprint, not_after, current_since, check_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (certificate_id) DO UPDATE SET
			   domains = excluded.domains,
			   current_since = CASE WHEN monitor_certificates.version_id = excluded.version_id
			     THEN monitor_certificates.current_since ELSE excluded.current_since END,
			   check_at = CASE WHEN monitor_certificates.version_id = excluded.version_id
			     THEN monitor_certificates.check_at ELSE excluded.check_at END,
			   issuer = CASE WHEN monitor_certificates.version_id = excluded.version_id THEN monitor_certificates.issuer ELSE '' END,
			   version_id = excluded.version_id, fingerprint = excluded.fingerprint, not_after = excluded.not_after`,
			d.CertificateID, string(domains), d.VersionID, d.Fingerprint, d.NotAfter.UnixMilli(), since.UnixMilli(), check.UnixMilli())
		return err
	case contracts.CertificateDeployed:
		// The version in use reached a target: look soon.
		at := m.now().Add(afterDeploy).UnixMilli()
		_, err := m.db.ExecContext(ctx,
			"UPDATE monitor_certificates SET check_at = ? WHERE certificate_id = ? AND fingerprint = ? AND check_at > ?",
			at, d.CertificateID, d.Fingerprint, at)
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
	// issuer is the CA of the version, once a domain served it.
	issuer string
	// ports are those its domains are checked on.
	ports []int
}

// defaultPort is the one domains are checked on unless set otherwise.
const defaultPort = 443

// parsePorts reads ports as stored: "443,993"; 443 alone for "".
func parsePorts(s string) []int {
	var out []int
	for f := range strings.SplitSeq(s, ",") {
		if p, err := strconv.Atoi(strings.TrimSpace(f)); err == nil && p > 0 && p < 65536 && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []int{defaultPort}
	}
	return out
}

// target names what a result is of: the domain, or the domain and port
// when the port is not 443.
func target(domain string, port int) string {
	if port == defaultPort {
		return domain
	}
	return net.JoinHostPort(domain, strconv.Itoa(port))
}

func (m *Module) load(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64,
) (tracked, error) {
	t := tracked{id: id}
	var domains string
	var notAfter, since, checkAt int64
	var ports string
	err := q.QueryRowContext(ctx,
		"SELECT domains, version_id, fingerprint, not_after, current_since, check_at, issuer, ports FROM monitor_certificates WHERE certificate_id = ?", id).
		Scan(&domains, &t.version, &t.fingerprint, &notAfter, &since, &checkAt, &t.issuer, &ports)
	if errors.Is(err, sql.ErrNoRows) {
		return t, errUnknown
	}
	if err != nil {
		return t, err
	}
	t.notAfter, t.currentSince, t.checkAt = time.UnixMilli(notAfter), time.UnixMilli(since), time.UnixMilli(checkAt)
	t.ports = parsePorts(ports)
	return t, json.Unmarshal([]byte(domains), &t.domains)
}

// result is what a domain served, as visitors of a view reach it.
type result struct {
	domain      string
	port        int
	view        string
	state       string
	fingerprint string
	notAfter    time.Time
	issuer      string
	err         string
	// ca names the issuer of what it served, and valid tells that it is
	// for the domain: what tells an older version from another's.
	ca    string
	valid bool
	// incomplete tells that the version in use was served without the
	// intermediates clients need.
	incomplete bool
	// version is the TLS version spoken, as "1.3", and legacy tells that
	// the server still takes TLS 1.0 or 1.1: for the version in use.
	version string
	legacy  bool
}

// peer is what a server served: its certificates, leaf first, and the
// TLS version spoken.
type peer struct {
	certs   []*x509.Certificate
	version uint16
}

// versionName names a TLS version as "1.3".
func versionName(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "1.3"
	case tls.VersionTLS12:
		return "1.2"
	case tls.VersionTLS11:
		return "1.1"
	case tls.VersionTLS10:
		return "1.0"
	}
	return ""
}

// view is how visitors reach a domain: through address, or through the
// domain itself when "".
type view struct{ name, address string }

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
	type job struct {
		domain string
		port   int
		view   view
	}
	var jobs []job
	for _, d := range domains {
		views := m.views(ctx, d)
		for _, p := range t.ports {
			for _, v := range views {
				jobs = append(jobs, job{d, p, v})
			}
		}
	}
	results := make([]result, len(jobs))
	var wg sync.WaitGroup
	slots := make(chan struct{}, probes)
	for i, j := range jobs {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			results[i] = m.probe(ctx, j.domain, j.port, j.view, t.fingerprint)
		})
	}
	wg.Wait()
	results = merged(results)
	// The CA of the version in use, as served now or before, tells an older
	// version of it from a certificate of someone else's.
	issuer := t.issuer
	for _, r := range results {
		if r.fingerprint == t.fingerprint {
			issuer = r.ca
		}
	}
	for i := range results {
		results[i].state = classify(results[i], t, issuer)
		results[i].domain = target(results[i].domain, results[i].port)
	}

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
	if issuer != t.issuer {
		if _, err := tx.ExecContext(ctx, "UPDATE monitor_certificates SET issuer = ? WHERE certificate_id = ?", issuer, id); err != nil {
			return nil, err
		}
	}
	kept := map[[2]string]bool{}
	for _, r := range results {
		kept[[2]string{r.domain, r.view}] = true
		var alerted, chainAlerted string
		if err := tx.QueryRowContext(ctx, "SELECT alerted, chain_alerted FROM monitor_results WHERE certificate_id = ? AND domain = ? AND view = ?",
			id, r.domain, r.view).Scan(&alerted, &chainAlerted); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		// A chain served without its intermediates, told once for the version.
		if r.state == stateOK && r.incomplete && settled && chainAlerted != t.fingerprint {
			err := m.events.PublishTx(ctx, tx, contracts.CertificateChainIncomplete, strconv.FormatInt(id, 10), contracts.CertificateEvent{
				CertificateID: id, Domains: t.domains, VersionID: t.version, Fingerprint: t.fingerprint, NotAfter: t.notAfter.UTC(),
				Check: &contracts.Check{Domain: r.domain, View: r.view, Fingerprint: r.fingerprint, NotAfter: r.notAfter.UTC()},
			})
			if err != nil {
				return nil, err
			}
			chainAlerted = t.fingerprint
		}
		if r.incomplete && !settled {
			next = earliest(next, t.currentSince.Add(grace))
		}
		switch {
		case r.state == stateOld && settled && alerted != t.fingerprint:
			err := m.events.PublishTx(ctx, tx, contracts.CertificateServedOld, strconv.FormatInt(id, 10), contracts.CertificateEvent{
				CertificateID: id, Domains: t.domains, VersionID: t.version, Fingerprint: t.fingerprint, NotAfter: t.notAfter.UTC(),
				Check: &contracts.Check{Domain: r.domain, View: r.view, Fingerprint: r.fingerprint, NotAfter: r.notAfter.UTC()},
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
		incomplete, legacy := 0, 0
		if r.incomplete {
			incomplete = 1
		}
		if r.legacy {
			legacy = 1
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO monitor_results (certificate_id, domain, view, state, fingerprint, not_after, issuer, error, checked_at, alerted, incomplete, chain_alerted,
			   tls_version, legacy_tls)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (certificate_id, domain, view) DO UPDATE SET state = excluded.state, fingerprint = excluded.fingerprint,
			   not_after = excluded.not_after, issuer = excluded.issuer, error = excluded.error, checked_at = excluded.checked_at,
			   alerted = excluded.alerted, incomplete = excluded.incomplete, chain_alerted = excluded.chain_alerted,
			   tls_version = excluded.tls_version, legacy_tls = excluded.legacy_tls`,
			id, r.domain, r.view, r.state, r.fingerprint, notAfter, r.issuer, r.err, now.UnixMilli(), alerted, incomplete, chainAlerted,
			r.version, legacy); err != nil {
			return nil, err
		}
	}
	// Domains the certificate no longer has, and views that came together.
	rows, err := tx.QueryContext(ctx, "SELECT domain, view FROM monitor_results WHERE certificate_id = ?", id)
	if err != nil {
		return nil, err
	}
	var gone [][2]string
	for rows.Next() {
		var k [2]string
		if err := rows.Scan(&k[0], &k[1]); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !kept[k] {
			gone = append(gone, k)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	for _, k := range gone {
		if _, err := tx.ExecContext(ctx, "DELETE FROM monitor_results WHERE certificate_id = ? AND domain = ? AND view = ?", id, k[0], k[1]); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE monitor_certificates SET check_at = ? WHERE certificate_id = ?", next.UnixMilli(), id); err != nil {
		return nil, err
	}
	return results, tx.Commit()
}

// views tells how visitors reach domain: one way, unless its DNS sends
// those from mainland China and those from elsewhere to different places,
// which are then checked apart. When the DNS cannot tell, one way.
func (m *Module) views(ctx context.Context, domain string) []view {
	china, err := m.target(ctx, domain, dnscheck.MainlandChina)
	if err != nil {
		return []view{{}}
	}
	global, err := m.target(ctx, domain, dnscheck.Abroad)
	if err != nil || global == china {
		return []view{{}}
	}
	// Addresses come joined by commas: one of them is enough.
	first := func(s string) string { a, _, _ := strings.Cut(s, ","); return a }
	return []view{{viewChina, first(china)}, {viewGlobal, first(global)}}
}

// merged puts the two views of a domain back together when they served the
// same certificate, or failed alike: nothing to tell apart.
func merged(results []result) []result {
	var out []result
	for i := 0; i < len(results); i++ {
		r := results[i]
		if r.view == viewChina && i+1 < len(results) {
			if g := results[i+1]; g.domain == r.domain && g.port == r.port && g.view == viewGlobal && g.fingerprint == r.fingerprint && (g.err == "") == (r.err == "") &&
				g.incomplete == r.incomplete && g.version == r.version && g.legacy == r.legacy {
				r.view = viewAll
				i++
			}
		}
		out = append(out, r)
	}
	return out
}

// probe connects to domain on port as visitors of v reach it and says what
// it served; for the version in use, by its fingerprint, whether the chain
// served is complete.
func (m *Module) probe(ctx context.Context, domain string, port int, v view, fingerprint string) result {
	r := result{domain: domain, port: port, view: v.name}
	p, err := m.serve(ctx, domain, v.address, port)
	if err != nil {
		r.state, r.err = stateUnreachable, truncate(err.Error(), 500)
		return r
	}
	leaf := p.certs[0]
	r.fingerprint, r.notAfter, r.issuer = cryptox.Fingerprint(leaf), leaf.NotAfter, issuerName(leaf)
	r.ca, r.valid = caOf(leaf), leaf.VerifyHostname(domain) == nil
	if r.fingerprint == fingerprint {
		r.incomplete = m.incomplete(ctx, domain, p.certs)
		r.version = versionName(p.version)
		r.legacy = m.legacy != nil && m.legacy(ctx, domain, v.address, port)
	}
	return r
}

// incomplete tells that certs, as a server sent them for domain, lack an
// intermediate: they reach no trusted root as sent, but do with the
// issuers the certificates name (AIA) fetched. Browsers on computers fetch
// those themselves; many apps, Android and curl do not. A chain that does
// not verify for another reason, as one of a private CA, is not told.
func (m *Module) incomplete(ctx context.Context, domain string, certs []*x509.Certificate) bool {
	leaf := certs[0]
	pool := x509.NewCertPool()
	for _, c := range certs[1:] {
		pool.AddCert(c)
	}
	opts := x509.VerifyOptions{DNSName: domain, Intermediates: pool, Roots: m.roots, CurrentTime: m.now()}
	_, err := leaf.Verify(opts)
	var unknown x509.UnknownAuthorityError
	if err == nil || !errors.As(err, &unknown) || m.issuers == nil {
		return false
	}
	b := &cryptox.Bundle{Leaf: leaf, Chain: cryptox.ChainOf(leaf, certs[1:])}
	if err := m.issuers(ctx, b); err != nil {
		return false
	}
	for _, c := range b.Chain {
		pool.AddCert(c)
	}
	_, err = leaf.Verify(opts)
	return err == nil
}

// classify says what r served compared with the version t uses, whose CA is
// issuer when known: an older certificate for the domain is an older
// version only when the same CA issued it, else someone else's, such as
// the own certificate of a CDN in front of the website for some visitors.
func classify(r result, t tracked, issuer string) string {
	switch {
	case r.err != "":
		return stateUnreachable
	case r.fingerprint == t.fingerprint:
		return stateOK
	case r.valid && r.notAfter.Before(t.notAfter) && (issuer == "" || r.ca == issuer):
		return stateOld
	}
	return stateOther
}

// caOf names the CA that issued c: its organization, or else its name.
func caOf(c *x509.Certificate) string {
	if len(c.Issuer.Organization) > 0 {
		return c.Issuer.Organization[0]
	}
	return c.Issuer.CommonName
}

// served connects to address, or to domain when "", on port as a client
// does for domain, and returns what it serves, leaf first. On 25 and 587,
// TLS comes through SMTP's STARTTLS.
func served(ctx context.Context, domain, address string, port int) (peer, error) {
	if address == "" {
		address = domain
	}
	at := net.JoinHostPort(address, strconv.Itoa(port))
	if port == 25 || port == 587 {
		return servedSMTP(ctx, domain, at)
	}
	return servedAt(ctx, domain, at)
}

// takesLegacy tells whether the server at address, or at domain when "",
// completes a handshake on port with TLS 1.0 or 1.1 at most, which
// browsers dropped. Ports behind STARTTLS are not asked.
func takesLegacy(ctx context.Context, domain, address string, port int) bool {
	if port == 25 || port == 587 {
		return false
	}
	if address == "" {
		address = domain
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	d := tls.Dialer{Config: &tls.Config{ServerName: domain, InsecureSkipVerify: true, //nolint:gosec // it asks what the server takes, not whom it is
		MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11}}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(address, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// servedSMTP connects to the mail server at address, asks for TLS with
// STARTTLS, and returns what it serves for domain, leaf first.
func servedSMTP(ctx context.Context, domain, address string) (peer, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return peer{}, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	c, err := smtp.NewClient(conn, domain)
	if err != nil {
		return peer{}, err
	}
	if ok, _ := c.Extension("STARTTLS"); !ok {
		return peer{}, errors.New("the mail server offers no STARTTLS")
	}
	if err := c.StartTLS(&tls.Config{ServerName: domain, InsecureSkipVerify: true}); err != nil { //nolint:gosec // it looks at the certificate served, valid or not
		return peer{}, err
	}
	state, _ := c.TLSConnectionState()
	if len(state.PeerCertificates) == 0 {
		return peer{}, errors.New("the server sent no certificate")
	}
	return peer{certs: state.PeerCertificates, version: state.Version}, nil
}

// servedAt connects to address for domain and returns what it serves, leaf
// first. It does not verify it: what matters is which certificate it is,
// valid or not.
func servedAt(ctx context.Context, domain, address string) (peer, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	d := tls.Dialer{Config: &tls.Config{ServerName: domain, InsecureSkipVerify: true}} //nolint:gosec // it looks at the certificate served, valid or not
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return peer{}, err
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return peer{}, errors.New("the server sent no certificate")
	}
	return peer{certs: state.PeerCertificates, version: state.Version}, nil
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
