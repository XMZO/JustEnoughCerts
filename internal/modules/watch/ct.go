package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// Certificate transparency: the certificates logged for the registered
// domains JEC holds certificates under are searched through Cert Spotter,
// and one whose key JEC does not hold is told of, as one a CA issued to
// someone else. What was issued before the search was first switched on
// was there before and is only listed.
const (
	ctEvery  = 6 * time.Hour      // between the searches of a domain
	ctRetry  = time.Hour          // after a search failed
	ctGrace  = 2 * time.Hour      // before a certificate found is told of, as JEC may still be storing its own
	ctKeep   = 7 * 24 * time.Hour // how long a certificate that ended is kept
	ctPages  = 20                 // pages of one search at most; a long first one goes on in the next run
	ctBudget = 8                  // requests of one run without an API key, which Cert Spotter keeps few
	ctKeyed  = 100                // requests of one run with an API key
)

// States of a certificate found.
const (
	ctNew      = "new"
	ctOwn      = "own"
	ctBaseline = "baseline"
	ctUnknown  = "unknown"
	ctTrusted  = "trusted"
	ctIgnored  = "ignored"
)

// certSpotterURL is Cert Spotter's search of the certificates of a domain.
const certSpotterURL = "https://api.certspotter.com/v1/issuances"

// errRateLimited is Cert Spotter refusing more requests for now.
var errRateLimited = errors.New("certificate transparency search: too many requests, going on later")

// spotted is a certificate as Cert Spotter tells it.
type spotted struct {
	ID       string   `json:"id"`
	SHA256   string   `json:"cert_sha256"`
	DNSNames []string `json:"dns_names"`
	Pubkey   string   `json:"pubkey_sha256"`
	Issuer   struct {
		FriendlyName string `json:"friendly_name"`
		Name         string `json:"name"`
	} `json:"issuer"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
}

// issuer is what the certificate's issuer is called: the CA's name where
// Cert Spotter knows it, else its distinguished name.
func (s spotted) issuer() string {
	if s.Issuer.FriendlyName != "" {
		return s.Issuer.FriendlyName
	}
	return s.Issuer.Name
}

// spotter searches Cert Spotter.
type spotter struct {
	client *http.Client
	url    string
}

// search returns a page of the unexpired certificates logged for domain
// and the names under it, found after the one with ID after ("" for the
// first page); none when there are no more.
func (s *spotter) search(ctx context.Context, domain, after, key string) ([]spotted, error) {
	q := url.Values{"domain": {domain}, "include_subdomains": {"true"}, "expand": {"dns_names", "issuer"}}
	if after != "" {
		q.Set("after", after)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, errRateLimited
	case resp.StatusCode != http.StatusOK:
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &e) == nil && e.Message != "" {
			return nil, fmt.Errorf("certificate transparency search: %s: %s", resp.Status, e.Message)
		}
		return nil, fmt.Errorf("certificate transparency search: %s", resp.Status)
	}
	var page []spotted
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("certificate transparency search: %w", err)
	}
	return page, nil
}

// ctOn is the search switched on: the API key if any, and since when.
type ctOn struct {
	key   string
	since time.Time // first switched on: what was issued before was there before
}

// ctSettings returns the search's settings, or nil while it is off.
func (m *Module) ctSettings(ctx context.Context) (*ctOn, error) {
	var enabled, since int64
	var sealed []byte
	err := m.db.QueryRowContext(ctx, "SELECT enabled, api_key, since FROM watch_ct WHERE id = 1").Scan(&enabled, &sealed, &since)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil || enabled == 0 {
		return nil, err
	}
	on := &ctOn{since: time.UnixMilli(since)}
	if len(sealed) > 0 {
		plain, err := m.secrets.Open(sealed, ctKeyContext)
		if err != nil {
			return nil, err
		}
		on.key = string(plain)
	}
	return on, nil
}

// ctKeyContext binds the sealed API key to its use.
const ctKeyContext = "watch certificate transparency key"

// transparencyDue is the job transparency: it searches the domains that
// are due, as many as Cert Spotter allows a run, then judges what was
// found.
func (m *Module) transparencyDue(ctx context.Context) error {
	on, err := m.ctSettings(ctx)
	if err != nil || on == nil {
		return err
	}
	if err := m.syncCT(ctx); err != nil {
		return err
	}
	rows, err := m.db.QueryContext(ctx, "SELECT name FROM watch_ct_domains WHERE check_at <= ? ORDER BY check_at, name", m.now().UnixMilli())
	if err != nil {
		return err
	}
	var due []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return err
		}
		due = append(due, name)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	budget := ctBudget
	if on.key != "" {
		budget = ctKeyed
	}
	for _, name := range due {
		if budget <= 0 {
			break
		}
		used, err := m.searchCT(ctx, name, on, budget)
		budget -= used
		if errors.Is(err, errRateLimited) {
			break
		}
		if err != nil {
			return err
		}
	}
	return m.judgeCT(ctx)
}

// syncCT brings the domains searched in step with the registered domains
// JEC holds certificates under; one that goes is forgotten with what was
// found for it.
func (m *Module) syncCT(ctx context.Context) error {
	if err := m.syncDomains(ctx); err != nil {
		return err
	}
	rows, err := m.db.QueryContext(ctx, "SELECT name FROM watch_domains WHERE certificates <> '[]'")
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return err
		}
		want[name] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err = tx.QueryContext(ctx, "SELECT name FROM watch_ct_domains")
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return err
		}
		have[name] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for name := range have {
		if want[name] {
			continue
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM watch_ct_certificates WHERE domain = ?", name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM watch_ct_domains WHERE name = ?", name); err != nil {
			return err
		}
	}
	now := m.now().UnixMilli()
	for name := range want {
		if have[name] {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO watch_ct_domains (name, last_id, scanned, check_at, error) VALUES (?, '', 0, ?, '')", name, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// searchCT searches domain name with at most budget requests, stores what
// it found and returns the requests it made. A search that has more pages
// left stays due, to go on in the next run.
func (m *Module) searchCT(ctx context.Context, name string, on *ctOn, budget int) (int, error) {
	var after string
	var scanned int64
	err := m.db.QueryRowContext(ctx, "SELECT last_id, scanned FROM watch_ct_domains WHERE name = ?", name).Scan(&after, &scanned)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var found []spotted
	used, end := 0, false
	var serr error
	for used < min(budget, ctPages) {
		page, err := m.spotter.search(ctx, name, after, on.key)
		used++
		if err != nil {
			serr = err
			break
		}
		if len(page) == 0 {
			end = true
			break
		}
		found = append(found, page...)
		after = page[len(page)-1].ID
	}

	now := m.now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return used, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, s := range found {
		if s.ID == "" || !s.NotAfter.After(now) {
			continue
		}
		// Found by the first search of its domain, which may be long after
		// the search was switched on, as for a domain JEC holds certificates
		// under only later: there before when issued before.
		state := ctNew
		if scanned == 0 && s.NotBefore.Before(on.since) {
			state = ctBaseline
		}
		names := slices.Clone(s.DNSNames)
		slices.Sort(names)
		names = slices.Compact(names)
		raw, _ := json.Marshal(names)
		_, err := tx.ExecContext(ctx,
			`INSERT INTO watch_ct_certificates (id, domain, names, issuer, issuer_dn, pubkey, sha256, not_before, not_after, state, certificate_id, found_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?) ON CONFLICT (id) DO NOTHING`,
			s.ID, name, string(raw), cut(s.issuer()), cut(s.Issuer.Name), strings.ToLower(s.Pubkey), strings.ToLower(s.SHA256),
			s.NotBefore.UnixMilli(), s.NotAfter.UnixMilli(), state, now.UnixMilli())
		if err != nil {
			return used, err
		}
	}
	next, msg := now, ""
	switch {
	case serr != nil:
		next, msg = now.Add(ctRetry), cut(serr.Error())
	case end:
		next, scanned = now.Add(ctEvery), 1
	}
	_, err = tx.ExecContext(ctx,
		"UPDATE watch_ct_domains SET last_id = ?, scanned = ?, check_at = ?, checked_at = ?, error = ? WHERE name = ?",
		after, scanned, next.UnixMilli(), now.UnixMilli(), msg, name)
	if err != nil {
		return used, err
	}
	if err := tx.Commit(); err != nil {
		return used, err
	}
	return used, serr
}

// judgeCT sorts out the certificates found: one with a key JEC holds is its
// own; one whose names and issuer are trusted is trusted; a new one found a
// while ago is told of. One told of that turns out to be JEC's, as one a
// cloud issued that JEC fetched later, becomes its own. Certificates that
// ended a while ago are forgotten.
func (m *Module) judgeCT(ctx context.Context) error {
	now := m.now()
	if _, err := m.db.ExecContext(ctx, "DELETE FROM watch_ct_certificates WHERE not_after < ?", now.Add(-ctKeep).UnixMilli()); err != nil {
		return err
	}
	type found struct {
		id, domain, names, issuer, pubkey, sha256, state string
		notBefore, notAfter, foundAt                     int64
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT id, domain, names, issuer, pubkey, sha256, state, not_before, not_after, found_at
		 FROM watch_ct_certificates WHERE state IN (?, ?, ?)`, ctNew, ctBaseline, ctUnknown)
	if err != nil {
		return err
	}
	var open []found
	for rows.Next() {
		var f found
		if err := rows.Scan(&f.id, &f.domain, &f.names, &f.issuer, &f.pubkey, &f.sha256, &f.state, &f.notBefore, &f.notAfter, &f.foundAt); err != nil {
			_ = rows.Close()
			return err
		}
		open = append(open, f)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil || len(open) == 0 {
		return err
	}

	var list struct {
		Keys []struct {
			CertificateID int64  `json:"certificateId"`
			SHA256        string `json:"sha256"`
		} `json:"keys"`
	}
	if err := m.call(ctx, via, http.MethodGet, "/certs/public-keys", nil, &list); err != nil {
		var ce *module.CallError
		if errors.As(err, &ce) {
			return nil // the certificates' module is off: nothing can be told apart till it is back
		}
		return err
	}
	keys := map[string]int64{}
	for _, k := range list.Keys {
		keys[strings.ToLower(k.SHA256)] = k.CertificateID
	}
	trusted, err := m.trustedCT(ctx)
	if err != nil {
		return err
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, f := range open {
		if id, ok := keys[f.pubkey]; ok {
			if _, err := tx.ExecContext(ctx, "UPDATE watch_ct_certificates SET state = ?, certificate_id = ? WHERE id = ?", ctOwn, id, f.id); err != nil {
				return err
			}
			continue
		}
		if f.state == ctUnknown {
			continue
		}
		if trusted[trustKey(f.names, f.issuer)] {
			if _, err := tx.ExecContext(ctx, "UPDATE watch_ct_certificates SET state = ? WHERE id = ?", ctTrusted, f.id); err != nil {
				return err
			}
			continue
		}
		if f.state != ctNew || now.Sub(time.UnixMilli(f.foundAt)) < ctGrace {
			continue
		}
		ev := contracts.SpottedEvent{
			Domain: f.domain, Issuer: f.issuer, SHA256: f.sha256,
			NotBefore: time.UnixMilli(f.notBefore).UTC(), NotAfter: time.UnixMilli(f.notAfter).UTC(),
		}
		if json.Unmarshal([]byte(f.names), &ev.Names) != nil || ev.Names == nil {
			ev.Names = []string{}
		}
		if err := m.events.PublishTx(ctx, tx, contracts.CertificateSpotted, f.id, ev); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE watch_ct_certificates SET state = ? WHERE id = ?", ctUnknown, f.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// trustKey is how a trust of names and issuer is looked up.
func trustKey(names, issuer string) string { return names + "\n" + issuer }

// trustedCT returns the trusted names and issuers, by trustKey.
func (m *Module) trustedCT(ctx context.Context) (map[string]bool, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT names, issuer FROM watch_ct_trusted")
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for rows.Next() {
		var names, issuer string
		if err := rows.Scan(&names, &issuer); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out[trustKey(names, issuer)] = true
	}
	return out, errors.Join(rows.Err(), rows.Close())
}
