package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
)

// Recovery codes: turning two-factor authentication on makes ten, shown
// once, each of which signs in once in place of a code of the
// authenticator app, for when it is lost. They are kept sealed by the
// master key: a copy of the database alone does not tell them.
const (
	recoveryCount = 10
	// recoveryAlphabet leaves out letters and digits easily taken for one
	// another.
	recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
)

// newRecoveryCodes makes a set of recovery codes, as "abcd-2345".
func newRecoveryCodes() ([]string, error) {
	out := make([]string, recoveryCount)
	size := big.NewInt(int64(len(recoveryAlphabet)))
	for i := range out {
		var b strings.Builder
		for j := range 8 {
			if j == 4 {
				b.WriteByte('-')
			}
			n, err := rand.Int(rand.Reader, size)
			if err != nil {
				return nil, err
			}
			b.WriteByte(recoveryAlphabet[n.Int64()])
		}
		out[i] = b.String()
	}
	return out, nil
}

// normalCode is a recovery code as typed, without spaces, hyphens or
// capitals.
func normalCode(code string) string {
	return strings.NewReplacer(" ", "", "-", "").Replace(strings.ToLower(strings.TrimSpace(code)))
}

func recoveryContext(id int64) string {
	return "kernel_users.recovery_codes:" + strconv.FormatInt(id, 10)
}

// sealCodes seals the codes of account id.
func (s *Service) sealCodes(id int64, codes []string) ([]byte, error) {
	data, err := json.Marshal(codes)
	if err != nil {
		return nil, err
	}
	return s.opts.Secrets.Seal(data, recoveryContext(id)), nil
}

// openCodes returns the recovery codes of account id left, from sealed.
func (s *Service) openCodes(id int64, sealed []byte) ([]string, error) {
	if len(sealed) == 0 {
		return nil, nil
	}
	data, err := s.opts.Secrets.Open(sealed, recoveryContext(id))
	if err != nil {
		return nil, err
	}
	var codes []string
	return codes, json.Unmarshal(data, &codes)
}

// useRecoveryCode signs account id in with code if it is one of its
// recovery codes not used yet, which it uses up.
func (s *Service) useRecoveryCode(ctx context.Context, id int64, code string) (bool, error) {
	given := normalCode(code)
	if len(given) != 8 {
		return false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var sealed []byte
	if err := tx.QueryRowContext(ctx, "SELECT recovery_codes FROM kernel_users WHERE id = ?", id).Scan(&sealed); err != nil {
		return false, err
	}
	codes, err := s.openCodes(id, sealed)
	if err != nil {
		return false, err
	}
	found := -1
	for i, c := range codes {
		if subtle.ConstantTimeCompare([]byte(normalCode(c)), []byte(given)) == 1 {
			found = i
		}
	}
	if found < 0 {
		return false, nil
	}
	left, err := s.sealCodes(id, slices.Delete(codes, found, found+1))
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE kernel_users SET recovery_codes = ? WHERE id = ?", left, id); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	s.log.Info("signed in with a recovery code", "account", id, "left", len(codes)-1)
	return true, nil
}

// recoveryLeft tells how many recovery codes account id has left.
func (s *Service) recoveryLeft(id int64, sealed []byte) int {
	codes, err := s.openCodes(id, sealed)
	if err != nil {
		s.log.Warn("open the recovery codes", "err", err)
	}
	return len(codes)
}

type recoveryOutput struct {
	Body struct {
		RecoveryCodes []string `json:"recoveryCodes" doc:"Each signs in once in place of a code of the authenticator app; shown this once"`
	}
}

// registerRecovery adds the operation that makes new recovery codes.
func (s *Service) registerRecovery(a huma.API, tags []string) {
	huma.Register(a, huma.Operation{
		OperationID: "two-factor-recovery", Method: http.MethodPost, Path: "/account/two-factor/recovery-codes", Tags: tags,
		Metadata: map[string]any{sessionOnly: true},
		Summary:  "Make new recovery codes, once the password is confirmed; the old ones stop working",
	}, func(ctx context.Context, in *confirmInput) (*recoveryOutput, error) {
		if err := s.ConfirmPassword(ctx, in.Body.Password); err != nil {
			return nil, err
		}
		u, _ := UserFrom(ctx)
		switch on, err := s.twoFactorOn(ctx, u.ID); {
		case err != nil:
			return nil, err
		case !on:
			return nil, api.NewError(http.StatusConflict, i18n.KernelErrorTwoFactorOff, nil)
		}
		codes, err := newRecoveryCodes()
		if err != nil {
			return nil, err
		}
		sealed, err := s.sealCodes(u.ID, codes)
		if err != nil {
			return nil, err
		}
		if _, err := s.db.ExecContext(ctx, "UPDATE kernel_users SET recovery_codes = ? WHERE id = ?", sealed, u.ID); err != nil {
			return nil, err
		}
		s.log.Info("new recovery codes", "username", u.Username)
		out := &recoveryOutput{}
		out.Body.RecoveryCodes = codes
		return out, nil
	})
}
