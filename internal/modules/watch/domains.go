package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// How the registrations of domains are looked up.
const (
	domainEvery   = 24 * time.Hour      // between the lookups of a domain
	domainSoon    = 30 * 24 * time.Hour // when an end starts to be told
	domainNear    = 12 * time.Hour      // between the lookups of one ending, to see a renewal soon
	domainUnknown = 7 * 24 * time.Hour  // after the registry told no end
	domainRetry   = 2 * time.Hour       // after a lookup failed
	maxDomains    = 500                 // added by hand
	lookups       = 4                   // domains looked up at once
)

// States of a domain beyond those of a site.
const (
	stateUnknown = "unknown"
	stateFailed  = "failed"
)

// via names the module in what it asks the API.
const via = "watch"

// domainLevel is how close a registration that ends at end is to it.
func domainLevel(now, end time.Time) contracts.Expiry {
	switch left := end.Sub(now); {
	case left <= 0:
		return contracts.ExpiryExpired
	case left <= urgent:
		return contracts.ExpiryUrgent
	case left <= domainSoon:
		return contracts.ExpiryWindow
	}
	return ""
}

// uses are what is under a domain.
type uses struct {
	certificates []int64
	sites        []int64
}

// syncDomains brings the domains watched in step with the names of the
// certificates and the sites: one nothing is under any more goes, unless
// it was added by hand. When the certificates cannot be listed, as while
// their module is off, what was known of them stays.
func (m *Module) syncDomains(ctx context.Context) error {
	var list struct {
		Certificates []struct {
			ID      int64    `json:"id"`
			Domains []string `json:"domains"`
		} `json:"certificates"`
	}
	known := true
	if err := m.call(ctx, via, http.MethodGet, "/certs/certificates", nil, &list); err != nil {
		var ce *module.CallError
		if !errors.As(err, &ce) {
			return err
		}
		known = false
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	type row struct {
		manual bool
		was    uses
	}
	rows, err := tx.QueryContext(ctx, "SELECT name, manual, certificates, sites FROM watch_domains")
	if err != nil {
		return err
	}
	have := map[string]row{}
	for rows.Next() {
		var name, certs, sites string
		var manual int64
		if err := rows.Scan(&name, &manual, &certs, &sites); err != nil {
			_ = rows.Close()
			return err
		}
		r := row{manual: manual == 1}
		_ = json.Unmarshal([]byte(certs), &r.was.certificates)
		_ = json.Unmarshal([]byte(sites), &r.was.sites)
		have[name] = r
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}

	want := map[string]*uses{}
	use := func(name string) *uses {
		u := want[name]
		if u == nil {
			u = &uses{certificates: []int64{}, sites: []int64{}}
			want[name] = u
		}
		return u
	}
	for name, r := range have {
		if r.manual {
			use(name)
		}
		if !known {
			for _, id := range r.was.certificates {
				use(name).certificates = append(use(name).certificates, id)
			}
		}
	}
	for _, c := range list.Certificates {
		for _, d := range c.Domains {
			if name := registered(d); name != "" && !slices.Contains(use(name).certificates, c.ID) {
				use(name).certificates = append(use(name).certificates, c.ID)
			}
		}
	}
	srows, err := tx.QueryContext(ctx, "SELECT id, host FROM watch_sites")
	if err != nil {
		return err
	}
	for srows.Next() {
		var id int64
		var host string
		if err := srows.Scan(&id, &host); err != nil {
			_ = srows.Close()
			return err
		}
		if name := registered(host); name != "" {
			use(name).sites = append(use(name).sites, id)
		}
	}
	if err := errors.Join(srows.Err(), srows.Close()); err != nil {
		return err
	}

	now := m.now().UnixMilli()
	for name := range have {
		if want[name] == nil {
			if _, err := tx.ExecContext(ctx, "DELETE FROM watch_domains WHERE name = ?", name); err != nil {
				return err
			}
		}
	}
	for name, u := range want {
		slices.Sort(u.certificates)
		slices.Sort(u.sites)
		certs, _ := json.Marshal(u.certificates)
		sites, _ := json.Marshal(u.sites)
		r, ok := have[name]
		switch {
		case !ok:
			_, err = tx.ExecContext(ctx,
				`INSERT INTO watch_domains (name, manual, certificates, sites, created_at, check_at, state, registrar, error, notified)
				 VALUES (?, 0, ?, ?, ?, ?, ?, '', '', '')`, name, string(certs), string(sites), now, now, statePending)
		case !slices.Equal(r.was.certificates, u.certificates) || !slices.Equal(r.was.sites, u.sites):
			_, err = tx.ExecContext(ctx, "UPDATE watch_domains SET certificates = ?, sites = ? WHERE name = ?", string(certs), string(sites), name)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// domainsDue is the job domains: it brings the domains in step, then looks
// up those that are due.
func (m *Module) domainsDue(ctx context.Context) error {
	if err := m.syncDomains(ctx); err != nil {
		return err
	}
	rows, err := m.db.QueryContext(ctx, "SELECT name FROM watch_domains WHERE check_at <= ? ORDER BY check_at", m.now().UnixMilli())
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
	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)
	slots := make(chan struct{}, lookups)
	for _, name := range due {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			if err := m.checkDomain(ctx, name); err != nil && !errors.Is(err, errNotFound) {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

// checkDomain looks up the registration of domain name now, stores what it
// found and tells of an end that came closer.
func (m *Module) checkDomain(ctx context.Context, name string) error {
	var certs, registrar, notified string
	var expires sql.NullInt64
	err := m.db.QueryRowContext(ctx, "SELECT certificates, expires_at, registrar, notified FROM watch_domains WHERE name = ?", name).
		Scan(&certs, &expires, &registrar, &notified)
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	reg, lerr := m.rdap.lookup(ctx, name)
	now := m.now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if lerr != nil {
		// What was told before stays known.
		state, next := stateFailed, now.Add(domainRetry)
		if errors.Is(lerr, errNoRDAP) || errors.Is(lerr, errNoEnd) {
			state, next = stateUnknown, now.Add(domainUnknown)
		}
		_, err = tx.ExecContext(ctx, "UPDATE watch_domains SET state = ?, error = ?, checked_at = ?, check_at = ? WHERE name = ?",
			state, cut(lerr.Error()), now.UnixMilli(), next.UnixMilli(), name)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	if expires.Valid && reg.expires.UnixMilli() > expires.Int64 {
		notified = "" // renewed: the new end is told anew
	}
	lvl := domainLevel(now, reg.expires)
	state, next := stateOK, now.Add(domainEvery)
	switch lvl {
	case contracts.ExpiryExpired:
		state, next = stateExpired, now.Add(domainNear)
	case contracts.ExpiryUrgent, contracts.ExpiryWindow:
		state, next = stateExpiring, now.Add(domainNear)
	}
	if expiryRank[lvl] > expiryRank[contracts.Expiry(notified)] {
		ev := contracts.DomainEvent{Domain: name, ExpiresAt: reg.expires, Expiry: lvl, Registrar: reg.registrar, Certificates: []int64{}}
		_ = json.Unmarshal([]byte(certs), &ev.Certificates)
		if err := m.events.PublishTx(ctx, tx, contracts.DomainExpiring, name, ev); err != nil {
			return err
		}
		notified = string(lvl)
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE watch_domains SET state = ?, expires_at = ?, registrar = ?, error = '', notified = ?, checked_at = ?, check_at = ?
		 WHERE name = ?`,
		state, reg.expires.UnixMilli(), reg.registrar, notified, now.UnixMilli(), next.UnixMilli(), name)
	if err != nil {
		return err
	}
	return tx.Commit()
}
