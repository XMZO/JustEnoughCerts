package auth

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/boombuler/barcode/qr"
	"github.com/pquerna/otp/totp"

	"github.com/XMZO/JustEnoughCerts/i18n"
)

// codeAt is the code of secret at t, as an authenticator app shows it.
func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(secret, at, totpOpts)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// wrong is a code that code is not.
func wrong(code string) string {
	if code[0] == '9' {
		return "0" + code[1:]
	}
	return string(code[0]+1) + code[1:]
}

const signIn = `{"username": "admin", "password": "correct horse"}`

func withCode(code string) string {
	return `{"username": "admin", "password": "correct horse", "code": "` + code + `"}`
}

func (f *fixture) secret(session string) TwoFactorSetup {
	f.t.Helper()
	r := f.call(http.MethodPost, "/account/two-factor/secret", session, `{"password": "correct horse"}`)
	if r.status != http.StatusOK {
		f.t.Fatalf("secret = %d %s", r.status, r.body)
	}
	var setup TwoFactorSetup
	if err := json.Unmarshal([]byte(r.body), &setup); err != nil {
		f.t.Fatal(err)
	}
	return setup
}

func TestTwoFactor(t *testing.T) {
	f := signedUp(t)
	session := f.call(http.MethodPost, "/session", "", signIn).cookie.Value
	other := f.call(http.MethodPost, "/session", "", signIn).cookie.Value

	// A new secret, once the password is confirmed.
	if r := f.call(http.MethodPost, "/account/two-factor/secret", session, `{"password": "wrong horse"}`); r.status != http.StatusUnprocessableEntity {
		t.Errorf("a wrong password = %d %s", r.status, r.body)
	}
	setup := f.secret(session)
	if len(setup.Secret) != 32 || !strings.HasPrefix(setup.URI, "otpauth://totp/JEC:admin?") || !strings.Contains(setup.URI, "secret="+setup.Secret) {
		t.Errorf("setup %+v", setup)
	}
	svg, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(setup.QR, "data:image/svg+xml;base64,"))
	if err != nil || !strings.HasPrefix(string(svg), "<svg") || !strings.Contains(string(svg), "<path") {
		t.Errorf("the QR code: %v %.80s", err, svg)
	}

	// A wrong code does not turn it on; the right one does, and ends the
	// other sessions.
	first := codeAt(t, setup.Secret, f.clock)
	if r := f.call(http.MethodPost, "/account/two-factor/enable", session, `{"code": "`+wrong(first)+`"}`); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "body.code") {
		t.Errorf("a wrong code = %d %s", r.status, r.body)
	}
	r := f.call(http.MethodPost, "/account/two-factor/enable", session, `{"code": "`+first[:3]+" "+first[3:]+`"}`)
	var made struct {
		RecoveryCodes []string `json:"recoveryCodes"`
	}
	if r.status != http.StatusOK || json.Unmarshal([]byte(r.body), &made) != nil || len(made.RecoveryCodes) != recoveryCount ||
		len(made.RecoveryCodes[0]) != 9 || made.RecoveryCodes[0][4] != '-' {
		t.Fatalf("enable = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodGet, "/session", other, ""); r.status != http.StatusUnauthorized {
		t.Errorf("the other session = %d", r.status)
	}
	if r := f.call(http.MethodGet, "/session", session, ""); r.status != http.StatusOK || !strings.Contains(r.body, `"twoFactor":true`) {
		t.Errorf("this session = %d %s", r.status, r.body)
	}

	// Signing in takes a code now, which works once.
	if r := f.call(http.MethodPost, "/session", "", signIn); r.status != http.StatusUnauthorized || code(t, r.body) != i18n.KernelErrorTwoFactorRequired {
		t.Errorf("no code = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodPost, "/session", "", withCode(first)); r.status != http.StatusUnauthorized || code(t, r.body) != i18n.KernelErrorTwoFactorCode {
		t.Errorf("the code that turned it on = %d %s", r.status, r.body)
	}
	f.clock = f.clock.Add(30 * time.Second)
	next := codeAt(t, setup.Secret, f.clock)
	if r := f.call(http.MethodPost, "/session", "", withCode(next)); r.status != http.StatusOK || r.cookie == nil {
		t.Errorf("the next code = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodPost, "/session", "", withCode(next)); r.status != http.StatusUnauthorized {
		t.Errorf("the next code again = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodPost, "/account/two-factor/secret", session, `{"password": "correct horse"}`); r.status != http.StatusConflict {
		t.Errorf("a secret while it is on = %d %s", r.status, r.body)
	}

	// A recovery code in place of a code, once each, typed as people do.
	if r := f.call(http.MethodPost, "/session", "", withCode(made.RecoveryCodes[0])); r.status != http.StatusOK {
		t.Errorf("a recovery code = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodPost, "/session", "", withCode(made.RecoveryCodes[0])); r.status != http.StatusUnauthorized {
		t.Errorf("a recovery code again = %d %s", r.status, r.body)
	}
	typed := strings.ToUpper(strings.Replace(made.RecoveryCodes[1], "-", " ", 1))
	if r := f.call(http.MethodPost, "/session", "", withCode(typed)); r.status != http.StatusOK {
		t.Errorf("a recovery code in capitals = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodGet, "/session", session, ""); !strings.Contains(r.body, `"recoveryCodes":8`) {
		t.Errorf("codes left: %s", r.body)
	}
	// New ones, and the old stop working.
	r = f.call(http.MethodPost, "/account/two-factor/recovery-codes", session, `{"password": "correct horse"}`)
	var renewed struct {
		RecoveryCodes []string `json:"recoveryCodes"`
	}
	if r.status != http.StatusOK || json.Unmarshal([]byte(r.body), &renewed) != nil || len(renewed.RecoveryCodes) != recoveryCount {
		t.Fatalf("new codes = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodPost, "/session", "", withCode(made.RecoveryCodes[2])); r.status != http.StatusUnauthorized {
		t.Errorf("an old code = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodPost, "/session", "", withCode(renewed.RecoveryCodes[9])); r.status != http.StatusOK {
		t.Errorf("a new code = %d %s", r.status, r.body)
	}

	// Off again, with the password.
	if r := f.call(http.MethodPost, "/account/two-factor/disable", session, `{"password": "correct horse"}`); r.status != http.StatusNoContent {
		t.Fatalf("disable = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodPost, "/session", "", signIn); r.status != http.StatusOK {
		t.Errorf("no code once off = %d %s", r.status, r.body)
	}
}

func TestTwoFactorSetupExpires(t *testing.T) {
	f := signedUp(t)
	session := f.call(http.MethodPost, "/session", "", signIn).cookie.Value
	setup := f.secret(session)
	f.clock = f.clock.Add(setupTTL + time.Minute)
	r := f.call(http.MethodPost, "/account/two-factor/enable", session, `{"code": "`+codeAt(t, setup.Secret, f.clock)+`"}`)
	if r.status != http.StatusConflict || code(t, r.body) != i18n.KernelErrorTwoFactorExpired {
		t.Errorf("a late code = %d %s", r.status, r.body)
	}
}

// Resetting the password on the server is the way back in for someone
// who lost the authenticator.
func TestResetTurnsTwoFactorOff(t *testing.T) {
	f := signedUp(t)
	session := f.call(http.MethodPost, "/session", "", signIn).cookie.Value
	setup := f.secret(session)
	if r := f.call(http.MethodPost, "/account/two-factor/enable", session, `{"code": "`+codeAt(t, setup.Secret, f.clock)+`"}`); r.status != http.StatusOK {
		t.Fatalf("enable = %d %s", r.status, r.body)
	}
	if err := SetPassword(t.Context(), f.svc.db, "admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	if r := f.call(http.MethodPost, "/session", "", signIn); r.status != http.StatusOK {
		t.Errorf("after the reset = %d %s", r.status, r.body)
	}
}

func TestSessionDays(t *testing.T) {
	f := signedUp(t)
	session := f.call(http.MethodPost, "/session", "", signIn).cookie.Value
	if r := f.call(http.MethodPatch, "/account", session, `{"sessionDays": 30}`); r.status != http.StatusOK || !strings.Contains(r.body, `"sessionDays":30`) {
		t.Fatalf("PATCH /account = %d %s", r.status, r.body)
	}
	if r := f.call(http.MethodPatch, "/account", session, `{"sessionDays": 5}`); r.status != http.StatusUnprocessableEntity {
		t.Errorf("5 days = %d %s", r.status, r.body)
	}
	if c := f.call(http.MethodPost, "/session", "", signIn).cookie; c == nil || c.MaxAge != int((30*24*time.Hour).Seconds()) {
		t.Errorf("a sign-in for 30 days: %+v", c)
	}
}

func TestCheckCode(t *testing.T) {
	const secret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	now := time.Date(2026, 10, 18, 12, 0, 15, 0, time.UTC)
	step := now.Unix() / totpPeriod
	at := func(d int64) string { return codeAt(t, secret, time.Unix((step+d)*totpPeriod, 0)) }
	for d := int64(-1); d <= 1; d++ {
		if got, ok := checkCode(secret, at(d), now, 0); !ok || got != step+d {
			t.Errorf("a code %d steps off: %d %v", d, got, ok)
		}
	}
	for _, c := range []struct {
		code string
		last int64
	}{
		{at(2), 0},
		{at(-2), 0},
		{at(0), step},
		{"12345", 0},
		{"abcdef", 0},
	} {
		if _, ok := checkCode(secret, c.code, now, c.last); ok {
			t.Errorf("%q after step %d works", c.code, c.last)
		}
	}
}

// The SVG draws the modules of the QR code, finder patterns included,
// within a quiet zone.
func TestQRCode(t *testing.T) {
	const uri = "otpauth://totp/JEC:admin?algorithm=SHA1&digits=6&issuer=JEC&period=30&secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	url, err := qrDataURL(uri)
	if err != nil {
		t.Fatal(err)
	}
	svg, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/svg+xml;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	code, err := qr.Encode(uri, qr.M, qr.Auto)
	if err != nil {
		t.Fatal(err)
	}
	size, dark := code.Bounds().Dx(), 0
	for y := range size {
		for x := range size {
			if r, _, _, _ := code.At(x, y).RGBA(); r == 0 {
				dark++
			}
		}
	}
	if n := strings.Count(string(svg), "h1v1h-1z"); n != dark || dark == 0 {
		t.Errorf("%d modules drawn, %d dark", n, dark)
	}
	for _, m := range []string{"M0 0h", "M6 0h", "M0 6h", "M6 6h", "M2 2h", "M4 4h"} {
		if !strings.Contains(string(svg), m) {
			t.Errorf("the finder pattern lacks %s", m)
		}
	}
	if !strings.Contains(string(svg), `viewBox="-4 -4 `) {
		t.Error("no quiet zone")
	}
}
