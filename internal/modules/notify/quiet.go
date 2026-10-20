package notify

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/language"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// Quiet hours: a note that comes in the quiet hours of a notifier is
// written at once, in its language, and held; the job release sends it
// once they end. Tests are never held.

// stateHeld is a note waiting for the end of quiet hours.
const stateHeld = "held"

// How held notes that fail to go out are tried again.
const (
	heldRetry    = 10 * time.Minute
	heldAttempts = 6
)

// quietUntil tells when the quiet hours of a notifier set to config end,
// if now is within them, in the time zone loc.
func quietUntil(config map[string]string, now time.Time, loc *time.Location) (time.Time, bool) {
	from, to, ok := parseQuiet(config[contracts.NoteQuiet])
	if !ok {
		return time.Time{}, false
	}
	t := now.In(loc)
	h := t.Hour()
	if from < to && (h < from || h >= to) || from > to && h < from && h >= to {
		return time.Time{}, false
	}
	end := time.Date(t.Year(), t.Month(), t.Day(), to, 0, 0, 0, loc)
	if !end.After(t) {
		end = end.AddDate(0, 0, 1)
	}
	return end, true
}

// parseQuiet reads quiet hours as "22-7": from the hour from to the hour to.
func parseQuiet(s string) (from, to int, ok bool) {
	a, b, found := strings.Cut(s, "-")
	if !found {
		return 0, 0, false
	}
	from, err1 := strconv.Atoi(a)
	to, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || from < 0 || from > 23 || to < 0 || to > 23 || from == to {
		return 0, 0, false
	}
	return from, to, true
}

// hold writes the note about event eventID for notifier provider, in the
// language it is set to, and keeps it until until.
func (s *service) hold(ctx context.Context, eventID, provider int64, event string, config map[string]string,
	write func(language.Tag) contracts.Note, until time.Time,
) error {
	note, err := json.Marshal(write(s.locale(config[contracts.NoteLocale])))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO notify_sends (event_id, provider_id, event, state, attempts, at, note, send_at)
		 VALUES (?, ?, ?, ?, 0, ?, ?, ?)
		 ON CONFLICT (event_id, provider_id) DO UPDATE SET state = excluded.state, at = excluded.at, note = excluded.note,
		 send_at = excluded.send_at`,
		eventID, provider, event, stateHeld, s.now().UnixMilli(), string(note), until.UnixMilli())
	return err
}

// release is the job release: it sends the held notes whose quiet hours
// are over. One that fails is tried again a while later, a few times.
func (s *service) release(ctx context.Context) error {
	type held struct {
		event, provider int64
		typ, note       string
		attempts        int
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT event_id, provider_id, event, note, attempts FROM notify_sends WHERE state = ? AND send_at <= ? ORDER BY send_at",
		stateHeld, s.now().UnixMilli())
	if err != nil {
		return err
	}
	var due []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.event, &h.provider, &h.typ, &h.note, &h.attempts); err != nil {
			_ = rows.Close()
			return err
		}
		due = append(due, h)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, h := range due {
		var n contracts.Note
		if err := json.Unmarshal([]byte(h.note), &n); err != nil {
			if err := s.record(ctx, h.event, h.provider, h.typ, retry.Permanent(err)); err != nil {
				return err
			}
			continue
		}
		sendErr := s.send(ctx, h.provider, func(language.Tag) contracts.Note { return n })
		if sendErr != nil && !retry.IsPermanent(sendErr) && h.attempts+1 < heldAttempts {
			msg := problem(sendErr)
			params, _ := json.Marshal(msg.Params)
			if len(msg.Params) == 0 {
				params = nil
			}
			if _, err := s.db.ExecContext(ctx,
				`UPDATE notify_sends SET attempts = attempts + 1, error_key = ?, error_params = ?, send_at = ?
				 WHERE event_id = ? AND provider_id = ?`,
				string(msg.Key), string(params), s.now().Add(heldRetry).UnixMilli(), h.event, h.provider); err != nil {
				return err
			}
			continue
		}
		if sendErr != nil && !retry.IsPermanent(sendErr) {
			sendErr = retry.Permanent(sendErr) // tried enough
		}
		if err := s.record(ctx, h.event, h.provider, h.typ, sendErr); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, "UPDATE notify_sends SET note = '' WHERE event_id = ? AND provider_id = ?", h.event, h.provider); err != nil {
			return err
		}
	}
	return nil
}
