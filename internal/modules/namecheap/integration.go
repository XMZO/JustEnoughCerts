package namecheap

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// How access tokens are kept fresh, as the official client does it: a
// token without known times, or with less than refreshBefore left, is
// refreshed; after the API answers that it does not refresh (404 or a
// redirect), JEC asks again a day later.
const (
	refreshBefore = 10 * 24 * time.Hour
	refreshPause  = 24 * time.Hour
)

// integration is a registration with the vendor.
type integration struct {
	providerID   int64
	tokenDigest  string
	access       string
	hash         string
	issuedAt     int64 // Unix seconds, 0 while unknown
	expiresAt    int64
	refreshAfter int64
	registeredAt int64
}

// session is the API of one registration, signed in.
type session struct {
	*client
	access, hash string
}

func (s *session) status(ctx context.Context) (string, error) {
	return s.client.status(ctx, s.access, s.hash)
}

func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func accessContext(id int64) string { return "access:" + strconv.FormatInt(id, 10) }

// session returns the API of instance in, registering first with its
// one-time token when that has not been done, or the token changed, and
// refreshing the access token when it is due. Instances are locked one at
// a time: a one-time token works only once.
func (m *Module) session(ctx context.Context, in providers.Instance, log func(contracts.LogLine)) (*session, error) {
	unlock := m.lock(in.ID)
	defer unlock()
	c, err := m.client(in)
	if err != nil {
		return nil, err
	}
	oneTime := strings.TrimSpace(in.Value("token"))
	it, err := m.load(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if it == nil || it.tokenDigest != digest(oneTime) {
		if oneTime == "" {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.NamecheapErrorNoToken})
		}
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.NamecheapLogRegister})
		if it, err = m.register(ctx, c, in.ID, oneTime); err != nil {
			return nil, err
		}
	}
	m.refresh(ctx, c, it)
	return &session{client: c, access: it.access, hash: it.hash}, nil
}

// register signs up with a one-time token and stores what comes back.
func (m *Module) register(ctx context.Context, c *client, id int64, oneTime string) (*integration, error) {
	access, hash, err := c.register(ctx, oneTime)
	var refused *apiError
	if errors.As(err, &refused) && refused.Status >= 400 && refused.Status < 500 && refused.Status != http.StatusTooManyRequests {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.NamecheapErrorToken, Err: err})
	}
	if err != nil {
		return nil, explain(err)
	}
	if hash == "" {
		if hash, err = c.hash(ctx, access); err != nil {
			return nil, explain(err)
		}
	}
	it := &integration{providerID: id, tokenDigest: digest(oneTime), access: access, hash: hash, registeredAt: m.now().Unix()}
	if err := m.save(ctx, it); err != nil {
		return nil, err
	}
	// The official client tells what runs it; nothing hangs on that.
	if err := c.meta(ctx, access); err != nil {
		m.log.Warn("namecheap: tell what runs the client", "provider", id, "err", err)
	}
	return it, nil
}

// refresh trades the access token for a new one when it is due, as the
// official client does. A failure leaves the token as it is, which works
// until it expires; one that asked to wait, or that says the API does not
// refresh, holds back the next try.
func (m *Module) refresh(ctx context.Context, c *client, it *integration) {
	now := m.now()
	switch {
	case it.refreshAfter > now.Unix():
		return
	case it.expiresAt != 0 && time.Unix(it.expiresAt, 0).Sub(now) > refreshBefore:
		return
	}
	access, issued, expires, err := c.refresh(ctx, it.access)
	if err != nil {
		var refused *apiError
		if errors.As(err, &refused) {
			switch {
			case refused.Status == http.StatusTooManyRequests && refused.RetryAfter > 0:
				it.refreshAfter = now.Add(refused.RetryAfter).Unix()
			case refused.Status == http.StatusNotFound, refused.Status >= 300 && refused.Status < 400:
				it.refreshAfter = now.Add(refreshPause).Unix()
			}
			if err := m.save(ctx, it); err != nil {
				m.log.Warn("namecheap: hold back refreshing", "provider", it.providerID, "err", err)
			}
		}
		m.log.Warn("namecheap: refresh the access token", "provider", it.providerID, "err", err)
		return
	}
	it.access, it.issuedAt, it.expiresAt, it.refreshAfter = access, issued, expires, 0
	if err := m.save(ctx, it); err != nil {
		m.log.Error("namecheap: store the refreshed access token", "provider", it.providerID, "err", err)
	}
}

// refreshAll is the job refresh: it keeps the access token of every
// registration fresh, even while no certificate is being issued, and
// forgets the registrations of instances that are gone.
func (m *Module) refreshAll(ctx context.Context) error {
	rows, err := m.db.QueryContext(ctx, "SELECT provider_id FROM namecheap_integrations")
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, id := range ids {
		_, in, err := m.providers.Open(ctx, id)
		switch {
		case errors.Is(err, providers.ErrNotFound):
			if _, err := m.db.ExecContext(ctx, "DELETE FROM namecheap_integrations WHERE provider_id = ?", id); err != nil {
				return err
			}
			continue
		case err != nil:
			m.log.Warn("namecheap: open a registration's instance", "provider", id, "err", err)
			continue
		}
		if _, err := m.session(ctx, in, func(contracts.LogLine) {}); err != nil {
			m.log.Warn("namecheap: keep a registration fresh", "provider", id, "err", err)
		}
	}
	return ctx.Err()
}

// load returns the registration of instance id, nil if there is none.
func (m *Module) load(ctx context.Context, id int64) (*integration, error) {
	it := &integration{providerID: id}
	var sealed []byte
	err := m.db.QueryRowContext(ctx,
		`SELECT token_digest, sealed_access, integration_hash, issued_at, expires_at, refresh_after, registered_at
		 FROM namecheap_integrations WHERE provider_id = ?`, id).
		Scan(&it.tokenDigest, &sealed, &it.hash, &it.issuedAt, &it.expiresAt, &it.refreshAfter, &it.registeredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	access, err := m.secrets.Open(sealed, accessContext(id))
	if err != nil {
		return nil, retry.Permanent(err)
	}
	it.access = string(access)
	return it, nil
}

// save stores a registration, replacing the one of its instance.
func (m *Module) save(ctx context.Context, it *integration) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO namecheap_integrations (provider_id, token_digest, sealed_access, integration_hash, issued_at, expires_at, refresh_after, registered_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (provider_id) DO UPDATE SET token_digest = excluded.token_digest, sealed_access = excluded.sealed_access,
		   integration_hash = excluded.integration_hash, issued_at = excluded.issued_at, expires_at = excluded.expires_at,
		   refresh_after = excluded.refresh_after, registered_at = excluded.registered_at`,
		it.providerID, it.tokenDigest, m.secrets.Seal([]byte(it.access), accessContext(it.providerID)), it.hash,
		it.issuedAt, it.expiresAt, it.refreshAfter, it.registeredAt)
	return err
}
