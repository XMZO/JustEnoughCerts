package certs

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// Settings are the module's settings (PLAN.md 4.6): the defaults of new
// certificates, and when renewals start.
type Settings struct {
	RenewBeforeDays int
	KeyType         string
	// CheckTime is the time of day renewals start, in minutes after
	// midnight in the server's time zone.
	CheckTime int
}

// defaults are the settings no one changed.
var defaults = Settings{RenewBeforeDays: defaultRenewBeforeDays, KeyType: defaultKeyType, CheckTime: 2 * 60}

// Keys of the settings in certs_settings.
const (
	settingRenewBeforeDays = "renew_before_days"
	settingKeyType         = "key_type"
	settingCheckTime       = "check_time"
)

// settings returns the module's settings, with the defaults of those not
// set.
func (s *service) settings(ctx context.Context) (Settings, error) { return settingsFrom(ctx, s.db) }

// settingsFrom returns the settings as q, such as a transaction, has them.
func settingsFrom(ctx context.Context, q querier) (Settings, error) {
	st := defaults
	rows, err := q.QueryContext(ctx, "SELECT key, value FROM certs_settings")
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return st, err
		}
		switch key {
		case settingRenewBeforeDays:
			if n, err := strconv.Atoi(value); err == nil && n >= 1 && n <= 365 {
				st.RenewBeforeDays = n
			}
		case settingKeyType:
			if slices.Contains(cryptox.KeyTypes, value) {
				st.KeyType = value
			}
		case settingCheckTime:
			if m, ok := parseClock(value); ok {
				st.CheckTime = m
			}
		}
	}
	return st, rows.Err()
}

func (s *service) saveSettings(ctx context.Context, st Settings) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := saveSettingsTx(ctx, tx, st); err != nil {
		return err
	}
	return tx.Commit()
}

// saveSettingsTx stores st in tx.
func saveSettingsTx(ctx context.Context, tx *sql.Tx, st Settings) error {
	for key, value := range map[string]string{
		settingRenewBeforeDays: strconv.Itoa(st.RenewBeforeDays),
		settingKeyType:         st.KeyType,
		settingCheckTime:       formatClock(st.CheckTime),
	} {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO certs_settings (key, value) VALUES (?, ?)
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
			return err
		}
	}
	return nil
}

// parseClock reads a time of day, HH:MM, as minutes after midnight.
func parseClock(v string) (int, bool) {
	t, err := time.Parse("15:04", v)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

func formatClock(minutes int) string { return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60) }

// schedule is what the time a renewal starts depends on, besides the
// certificate.
type schedule struct {
	checkTime int // minutes after midnight in loc
	loc       *time.Location
}

func (s *service) schedule(ctx context.Context) (schedule, error) {
	st, err := s.settings(ctx)
	return schedule{checkTime: st.CheckTime, loc: s.loc}, err
}

// renewAt is when the renewal of a version that ends at notAfter starts:
// at the first check time once the version enters its renewal window, or
// as soon as a week or less is left, whichever comes first.
func (sch schedule) renewAt(notAfter time.Time, renewBeforeDays int) time.Time {
	window := notAfter.Add(-time.Duration(renewBeforeDays) * day)
	at := nextClock(window, sch.checkTime, sch.loc)
	if urgent := notAfter.Add(-urgentWithin); at.After(urgent) {
		if window.After(urgent) {
			return window
		}
		return urgent
	}
	return at
}

// nextClock returns the first time at or after t that is minutes after
// midnight in loc.
func nextClock(t time.Time, minutes int, loc *time.Location) time.Time {
	l := t.In(loc)
	at := time.Date(l.Year(), l.Month(), l.Day(), minutes/60, minutes%60, 0, 0, loc)
	if at.Before(t) {
		at = time.Date(l.Year(), l.Month(), l.Day()+1, minutes/60, minutes%60, 0, 0, loc)
	}
	return at
}

// CertSettingsBody is the module's settings as the API returns them.
type CertSettingsBody struct {
	RenewBeforeDays int    `json:"renewBeforeDays" minimum:"1" maximum:"365" doc:"How many days before the end new certificates renew, or remind, unless set otherwise"`
	KeyType         string `json:"keyType" enum:"ecdsa-p256,ecdsa-p384,rsa-2048,rsa-3072,rsa-4096" doc:"The key type of new certificates with a source, unless set otherwise"`
	CheckTime       string `json:"checkTime" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$" doc:"The time of day, HH:MM in timeZone, renewals start once certificates enter their renewal window; with a week or less left, they start at once"`
	TimeZone        string `json:"timeZone" readOnly:"true" doc:"The time zone of checkTime: the server's, which the TZ environment variable sets"`
}

type certSettingsOutput struct{ Body CertSettingsBody }

type certSettingsInput struct {
	Body struct {
		RenewBeforeDays int    `json:"renewBeforeDays" minimum:"1" maximum:"365"`
		KeyType         string `json:"keyType" enum:"ecdsa-p256,ecdsa-p384,rsa-2048,rsa-3072,rsa-4096"`
		CheckTime       string `json:"checkTime" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$"`
	}
}

func (s *service) settingsBody(st Settings) CertSettingsBody {
	return CertSettingsBody{
		RenewBeforeDays: st.RenewBeforeDays, KeyType: st.KeyType, CheckTime: formatClock(st.CheckTime), TimeZone: s.loc.String(),
	}
}

func registerSettingsAPI(a huma.API, s *service) {
	huma.Register(a, huma.Operation{
		OperationID: "certs-settings", Method: http.MethodGet, Path: "/settings",
		Summary: "Get the defaults of new certificates and the time renewals start",
	}, func(ctx context.Context, _ *struct{}) (*certSettingsOutput, error) {
		st, err := s.settings(ctx)
		if err != nil {
			return nil, err
		}
		return &certSettingsOutput{Body: s.settingsBody(st)}, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "certs-save-settings", Method: http.MethodPut, Path: "/settings",
		Summary: "Change the defaults of new certificates and the time renewals start",
	}, func(ctx context.Context, in *certSettingsInput) (*certSettingsOutput, error) {
		minutes, ok := parseClock(in.Body.CheckTime)
		if !ok { // the pattern lets nothing else through
			return nil, api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil).
				WithField("body.checkTime", i18n.KernelErrorInvalidField)
		}
		st := Settings{RenewBeforeDays: in.Body.RenewBeforeDays, KeyType: in.Body.KeyType, CheckTime: minutes}
		if err := s.saveSettings(ctx, st); err != nil {
			return nil, err
		}
		return &certSettingsOutput{Body: s.settingsBody(st)}, nil
	})
}
