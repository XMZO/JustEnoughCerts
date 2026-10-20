package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/lib/useragent"
)

// How a session began.
const (
	viaPassword = "password"
	viaSSO      = "sso"
	viaSetup    = "setup"
)

// seenEvery is how often the last use of a session is written at most.
const seenEvery = 5 * time.Minute

// maxAgent bounds the User-Agent header kept.
const maxAgent = 300

// agentFrom returns the User-Agent header of the request of ctx, cut short.
func agentFrom(ctx context.Context) string {
	a, _ := ctx.Value(agentKey{}).(string)
	if len(a) > maxAgent {
		a = a[:maxAgent]
	}
	return a
}

// touchSession writes that the session hashed to hash was used now, at most every
// seenEvery.
func (s *Service) touchSession(ctx context.Context, hash []byte) {
	now := s.now()
	key := string(hash)
	s.mu.Lock()
	last, ok := s.seen[key]
	if ok && now.Sub(last) < seenEvery {
		s.mu.Unlock()
		return
	}
	s.seen[key] = now
	for k, t := range s.seen { // keep the map small
		if now.Sub(t) >= seenEvery {
			delete(s.seen, k)
		}
	}
	s.mu.Unlock()
	if _, err := s.db.ExecContext(ctx, "UPDATE kernel_sessions SET last_seen = ? WHERE token_hash = ?", timestamp(now), hash); err != nil {
		s.log.Warn("note the use of a session", "err", err)
	}
}

// failed records a failed sign-in of client, as username if any, and tells
// of the lockout it may bring.
func (s *Service) failed(ctx context.Context, client, username string) {
	if !s.limit.fail(client, s.now()) || s.opts.Events == nil {
		return
	}
	ev := contracts.SignInEvent{Username: username, Client: client, UserAgent: agentFrom(ctx), Device: useragent.Describe(agentFrom(ctx)),
		Minutes: int(lockout.Minutes())}
	if err := s.opts.Events.Publish(context.WithoutCancel(ctx), contracts.AccountLocked, client, ev); err != nil {
		s.log.Warn("tell of a lockout", "err", err)
	}
}

// tellSignIn tells that username signed in by way of via.
func (s *Service) tellSignIn(ctx context.Context, username, via string) {
	if s.opts.Events == nil {
		return
	}
	ev := contracts.SignInEvent{Username: username, Client: clientFrom(ctx), UserAgent: agentFrom(ctx), Device: useragent.Describe(agentFrom(ctx)), Via: via}
	if err := s.opts.Events.Publish(context.WithoutCancel(ctx), contracts.AccountSignedIn, username, ev); err != nil {
		s.log.Warn("tell of a sign-in", "err", err)
	}
}

// SessionBody is a sign-in of the account, as the API tells it.
type SessionBody struct {
	ID        string     `json:"id" doc:"Names the session in this API; not its cookie"`
	Current   bool       `json:"current" doc:"The session of this request"`
	Via       string     `json:"via,omitempty" enum:"password,sso,setup" doc:"How it began; absent for those from before JEC noted it"`
	Client    string     `json:"client,omitempty" doc:"The client's address when it signed in"`
	Device    string     `json:"device,omitempty" doc:"The browser and system, as its User-Agent header told them"`
	UserAgent string     `json:"userAgent,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	LastSeen  *time.Time `json:"lastSeen,omitempty" doc:"When it was last used, to within a few minutes"`
	ExpiresAt time.Time  `json:"expiresAt"`
}

type sessionsOutput struct {
	Body struct {
		Sessions []SessionBody `json:"sessions"`
	}
}

type sessionIDInput struct {
	ID string `path:"id" maxLength:"64"`
}

func (s *Service) registerSessions(a huma.API, tags []string) {
	only := map[string]any{sessionOnly: true}
	huma.Register(a, huma.Operation{
		OperationID: "sessions-list", Method: http.MethodGet, Path: "/account/sessions", Tags: tags, Metadata: only,
		Summary: "List where the account is signed in, the latest first",
	}, func(ctx context.Context, _ *struct{}) (*sessionsOutput, error) {
		list, err := s.sessions(ctx)
		if err != nil {
			return nil, err
		}
		out := &sessionsOutput{}
		out.Body.Sessions = make([]SessionBody, 0, len(list))
		for _, x := range list {
			out.Body.Sessions = append(out.Body.Sessions, x.SessionBody)
		}
		return out, nil
	})
	huma.Register(a, huma.Operation{
		OperationID: "sessions-delete", Method: http.MethodDelete, Path: "/account/sessions/{id}", Tags: tags, Metadata: only,
		Summary: "Sign a session out; this one, through DELETE /session", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *sessionIDInput) (*struct{}, error) {
		list, err := s.sessions(ctx)
		if err != nil {
			return nil, err
		}
		i := slices.IndexFunc(list, func(x storedSession) bool { return x.ID == in.ID })
		switch {
		case i < 0:
			return nil, api.NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
		case list[i].Current:
			return nil, api.NewError(http.StatusConflict, i18n.KernelErrorSessionCurrent, nil)
		}
		_, err = s.db.ExecContext(ctx, "DELETE FROM kernel_sessions WHERE token_hash = ?", list[i].hash)
		return nil, err
	})
	huma.Register(a, huma.Operation{
		OperationID: "sessions-sign-out-others", Method: http.MethodPost, Path: "/account/sessions/sign-out-others", Tags: tags, Metadata: only,
		Summary: "Sign the account out everywhere but here", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		u, _ := UserFrom(ctx)
		sess, _ := ctx.Value(sessionKey{}).(session)
		_, err := s.db.ExecContext(ctx, "DELETE FROM kernel_sessions WHERE user_id = ? AND token_hash != ?", u.ID, sess.tokenHash)
		if err == nil {
			s.log.Info("signed out everywhere else", "username", u.Username)
		}
		return nil, err
	})
}

// storedSession is a session with its hash, which only the server sees.
type storedSession struct {
	SessionBody
	hash []byte
}

// sessions returns the unexpired sessions of the account of ctx, the
// latest first.
func (s *Service) sessions(ctx context.Context) ([]storedSession, error) {
	u, _ := UserFrom(ctx)
	sess, _ := ctx.Value(sessionKey{}).(session)
	rows, err := s.db.QueryContext(ctx,
		`SELECT token_hash, created_at, expires_at, last_seen, client, user_agent, via FROM kernel_sessions
		 WHERE user_id = ? AND expires_at > ?`, u.ID, timestamp(s.now()))
	if err != nil {
		return nil, err
	}
	var out []storedSession
	for rows.Next() {
		var x storedSession
		var created, expires string
		var seen sql.NullString
		if err := rows.Scan(&x.hash, &created, &expires, &seen, &x.Client, &x.UserAgent, &x.Via); err != nil {
			_ = rows.Close()
			return nil, err
		}
		id := sha256.Sum256(x.hash) // not a prefix of the hash, which the cookie's check compares
		x.ID = hex.EncodeToString(id[:8])
		x.Current = bytes.Equal(x.hash, sess.tokenHash)
		x.Device = useragent.Describe(x.UserAgent)
		x.CreatedAt, _ = time.Parse(time.RFC3339, created)
		x.ExpiresAt, _ = time.Parse(time.RFC3339, expires)
		if seen.Valid {
			if t, err := time.Parse(time.RFC3339, seen.String); err == nil {
				x.LastSeen = &t
			}
		}
		out = append(out, x)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b storedSession) int {
		if a.Current != b.Current {
			if a.Current {
				return -1
			}
			return 1
		}
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return out, nil
}
