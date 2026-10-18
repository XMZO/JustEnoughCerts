package auth

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/boombuler/barcode/qr"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
)

// Two-factor authentication: once an account turns it on, signing in also
// takes a code of an authenticator app, as RFC 6238 makes it: TOTP with
// SHA-1, six digits and steps of 30 seconds, which every such app knows.
const (
	totpIssuer = "JEC"
	totpPeriod = 30
	// totpSkew is how many steps a code may be off either way, for clocks
	// that differ.
	totpSkew = 1
	// setupTTL is how long a new secret waits for its first code.
	setupTTL = 10 * time.Minute
)

var totpOpts = totp.ValidateOpts{Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}

// pendingSecret is a secret shown to an account, waiting for a code of it
// to turn two-factor authentication on.
type pendingSecret struct {
	secret  string
	expires time.Time
}

// checkCode returns the step, within totpSkew of now, whose code is code,
// if it comes after last: a code works once.
func checkCode(secret, code string, now time.Time, last int64) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0, false
	}
	step := now.Unix() / totpPeriod
	for s := step - totpSkew; s <= step+totpSkew; s++ {
		if s <= last {
			continue
		}
		want, err := totp.GenerateCodeCustom(secret, time.Unix(s*totpPeriod, 0), totpOpts)
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}

// secretContext binds a sealed secret to its account.
func secretContext(id int64) string { return "kernel_users.totp_secret:" + strconv.FormatInt(id, 10) }

// qrDataURL is the QR code of text, as an SVG data URL.
func qrDataURL(text string) (string, error) {
	code, err := qr.Encode(text, qr.M, qr.Auto)
	if err != nil {
		return "", err
	}
	size := code.Bounds().Dx()
	var b strings.Builder
	// A quiet zone of four modules, as scanners want.
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="-4 -4 %d %d" shape-rendering="crispEdges"><rect x="-4" y="-4" width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, size+8, size+8, size+8, size+8)
	for y := range size {
		for x := range size {
			if r, _, _, _ := code.At(x, y).RGBA(); r == 0 {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x, y)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(b.String())), nil
}

// TwoFactorSetup is a new secret to add to an authenticator app.
type TwoFactorSetup struct {
	Secret string `json:"secret" doc:"The secret in base32, to type in"`
	URI    string `json:"uri" doc:"The otpauth URI of the secret"`
	QR     string `json:"qr" doc:"The QR code of the URI, as an SVG data URL"`
}

type confirmInput struct {
	Body struct {
		Password string `json:"password" maxLength:"1024" doc:"The current password"`
	}
}

type setupSecretOutput struct {
	Body TwoFactorSetup
}

type codeInput struct {
	Body struct {
		Code string `json:"code" maxLength:"16" doc:"A code of the authenticator app"`
	}
}

type accountInput struct {
	Body struct {
		SessionDays int `json:"sessionDays" enum:"1,7,30" doc:"How many days a sign-in lasts"`
	}
}

// twoFactorOn reports whether account id has two-factor authentication on.
func (s *Service) twoFactorOn(ctx context.Context, id int64) (bool, error) {
	var secret []byte
	if err := s.db.QueryRowContext(ctx, "SELECT totp_secret FROM kernel_users WHERE id = ?", id).Scan(&secret); err != nil {
		return false, err
	}
	return len(secret) > 0, nil
}

// setUpTwoFactor makes a new secret for the account, once its password is
// confirmed, and keeps it until a code of it turns two-factor
// authentication on.
func (s *Service) setUpTwoFactor(ctx context.Context, in *confirmInput) (*setupSecretOutput, error) {
	if err := s.ConfirmPassword(ctx, in.Body.Password); err != nil {
		return nil, err
	}
	u, _ := UserFrom(ctx)
	switch on, err := s.twoFactorOn(ctx, u.ID); {
	case err != nil:
		return nil, err
	case on:
		return nil, api.NewError(http.StatusConflict, i18n.KernelErrorTwoFactorOn, nil)
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer: totpIssuer, AccountName: u.Username, Period: totpPeriod, SecretSize: 20,
		Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		return nil, err
	}
	image, err := qrDataURL(key.URL())
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.pending[u.ID] = pendingSecret{secret: key.Secret(), expires: s.now().Add(setupTTL)}
	s.mu.Unlock()
	return &setupSecretOutput{Body: TwoFactorSetup{Secret: key.Secret(), URI: key.URL(), QR: image}}, nil
}

// enableTwoFactor turns two-factor authentication on with a code of the
// new secret, and ends the account's other sessions.
func (s *Service) enableTwoFactor(ctx context.Context, in *codeInput) (*struct{}, error) {
	client := clientFrom(ctx)
	if err := s.throttle(client); err != nil {
		return nil, err
	}
	u, _ := UserFrom(ctx)
	sess, _ := ctx.Value(sessionKey{}).(session)
	now := s.now()
	s.mu.Lock()
	p, ok := s.pending[u.ID]
	s.mu.Unlock()
	if !ok || now.After(p.expires) {
		return nil, api.NewError(http.StatusConflict, i18n.KernelErrorTwoFactorExpired, nil)
	}
	step, ok := checkCode(p.secret, in.Body.Code, now, 0)
	if !ok {
		s.limit.fail(client, now)
		return nil, api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorTwoFactorCode, nil).
			WithField("body.code", i18n.KernelErrorTwoFactorCode)
	}
	sealed := s.opts.Secrets.Seal([]byte(p.secret), secretContext(u.ID))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "UPDATE kernel_users SET totp_secret = ?, totp_step = ?, updated_at = ? WHERE id = ?",
		sealed, step, timestamp(now), u.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM kernel_sessions WHERE user_id = ? AND token_hash != ?", u.ID, sess.tokenHash); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	delete(s.pending, u.ID)
	s.mu.Unlock()
	s.limit.reset(client)
	s.log.Info("two-factor authentication on", "username", u.Username)
	return nil, nil
}

// disableTwoFactor turns two-factor authentication off, once the password
// is confirmed.
func (s *Service) disableTwoFactor(ctx context.Context, in *confirmInput) (*struct{}, error) {
	if err := s.ConfirmPassword(ctx, in.Body.Password); err != nil {
		return nil, err
	}
	u, _ := UserFrom(ctx)
	if _, err := s.db.ExecContext(ctx, "UPDATE kernel_users SET totp_secret = NULL, totp_step = 0, updated_at = ? WHERE id = ?",
		timestamp(s.now()), u.ID); err != nil {
		return nil, err
	}
	s.log.Info("two-factor authentication off", "username", u.Username)
	return nil, nil
}

// updateAccount changes the settings of the account: how long its next
// sign-ins last.
func (s *Service) updateAccount(ctx context.Context, in *accountInput) (*accountOutput, error) {
	u, _ := UserFrom(ctx)
	if _, err := s.db.ExecContext(ctx, "UPDATE kernel_users SET session_days = ?, updated_at = ? WHERE id = ?",
		in.Body.SessionDays, timestamp(s.now()), u.ID); err != nil {
		return nil, err
	}
	account, err := s.account(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return &accountOutput{Body: account}, nil
}

// secondFactor checks the code of a sign-in to account id, whose sealed
// secret is sealed, and uses it up.
func (s *Service) secondFactor(ctx context.Context, client string, id int64, sealed []byte, last int64, code string) error {
	if strings.TrimSpace(code) == "" {
		return api.NewError(http.StatusUnauthorized, i18n.KernelErrorTwoFactorRequired, nil)
	}
	secret, err := s.opts.Secrets.Open(sealed, secretContext(id))
	if err != nil {
		return err
	}
	step, ok := checkCode(string(secret), code, s.now(), last)
	if ok {
		// Two sign-ins with one code: only the first moves the step on.
		res, err := s.db.ExecContext(ctx, "UPDATE kernel_users SET totp_step = ? WHERE id = ? AND totp_step < ?", step, id, step)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		ok = n == 1
	}
	if !ok {
		s.limit.fail(client, s.now())
		return api.NewError(http.StatusUnauthorized, i18n.KernelErrorTwoFactorCode, nil).WithField("body.code", i18n.KernelErrorTwoFactorCode)
	}
	return nil
}
