package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
)

type localKey struct{}

// WithLocal marks ctx as a call of the API from inside JEC, by a module
// acting for the account another way than through a browser, as a chat
// bot does; via names it, as an API token's name names the token. Only
// code in the process can set it: no request from outside carries it.
func WithLocal(ctx context.Context, via string) context.Context {
	return context.WithValue(ctx, localKey{}, via)
}

// authorizeLocal lets a call from inside JEC act as the account, with the
// rights of a token that writes: managing the account stays the person's.
func (s *Service) authorizeLocal(ctx huma.Context, via string) (session, bool) {
	sess := session{token: via, scope: scopeWrite}
	err := s.db.QueryRowContext(ctx.Context(), "SELECT id, username, coalesce(locale, '') FROM kernel_users ORDER BY id LIMIT 1").
		Scan(&sess.user.ID, &sess.user.Username, &sess.user.Locale)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		api.WriteError(ctx, api.NewError(http.StatusUnauthorized, i18n.KernelErrorUnauthorized, nil))
		return session{}, false
	case err != nil:
		s.log.Error("look up the account for a local call", "err", err)
		api.WriteError(ctx, api.NewError(http.StatusInternalServerError, i18n.KernelErrorInternal, nil))
		return session{}, false
	}
	if op := ctx.Operation(); op != nil && op.Metadata[sessionOnly] == true {
		api.WriteError(ctx, api.NewError(http.StatusForbidden, i18n.KernelErrorTokenSessionOnly, nil))
		return session{}, false
	}
	return sess, true
}
