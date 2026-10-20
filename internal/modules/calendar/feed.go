package calendar

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/text/language"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// via names the module in what it asks the API.
const via = "calendar"

// remindBefore is when the reminder of an end comes.
const remindBefore = "-P7D"

// What the feed reads of the API: only the fields it shows.
type certificate struct {
	ID      int64      `json:"id"`
	Domains []string   `json:"domains"`
	RenewAt *time.Time `json:"renewAt"`
	Current *struct {
		NotAfter time.Time `json:"notAfter"`
	} `json:"current"`
}

// FeedBody tells whether the feed is on, and the path of its URL, under
// the panel's.
type FeedBody struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path,omitempty" doc:"The path of the feed, from the panel's root, such as /api/v1/calendar/ics/<key>; secret"`
}

type feedOutput struct{ Body FeedBody }

type switchInput struct {
	Body struct {
		Enabled bool `json:"enabled"`
	}
}

type icsInput struct {
	Key  string `path:"key" maxLength:"128"`
	Lang string `query:"lang" maxLength:"35" doc:"The language of the events, such as zh-CN; English when left out"`
}

type icsOutput struct {
	ContentType  string `header:"Content-Type"`
	CacheControl string `header:"Cache-Control"`
	Body         []byte
}

func register(a huma.API, m *Module) {
	tags := []string{"calendar"}
	huma.Register(a, huma.Operation{
		OperationID: "calendar-feed", Method: http.MethodGet, Path: "/feed", Tags: tags,
		Summary: "Tell whether the calendar feed is on, and its URL",
	}, func(ctx context.Context, _ *struct{}) (*feedOutput, error) {
		return m.feed(ctx)
	})
	huma.Register(a, huma.Operation{
		OperationID: "calendar-switch", Method: http.MethodPut, Path: "/feed", Tags: tags,
		Summary: "Turn the calendar feed on, at a new secret URL, or off",
	}, func(ctx context.Context, in *switchInput) (*feedOutput, error) {
		if !in.Body.Enabled {
			if _, err := m.db.ExecContext(ctx, "DELETE FROM calendar_feed"); err != nil {
				return nil, err
			}
		} else if err := m.newKey(ctx, false); err != nil {
			return nil, err
		}
		return m.feed(ctx)
	})
	huma.Register(a, huma.Operation{
		OperationID: "calendar-rotate", Method: http.MethodPost, Path: "/feed/rotate", Tags: tags,
		Summary: "Move the calendar feed to a new secret URL: the old one stops working",
	}, func(ctx context.Context, _ *struct{}) (*feedOutput, error) {
		if err := m.newKey(ctx, true); err != nil {
			return nil, err
		}
		return m.feed(ctx)
	})
	huma.Register(a, huma.Operation{
		OperationID: "calendar-ics", Method: http.MethodGet, Path: "/ics/{key}", Tags: tags, Security: api.Public,
		Metadata:    map[string]any{api.AnyClient: true},
		Summary:     "The calendar feed, for calendar apps to subscribe to",
		Description: "When each certificate ends, with a reminder a week before, and when its next renewal starts, in iCalendar.",
		Responses:   map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"text/calendar": {}}}},
	}, func(ctx context.Context, in *icsInput) (*icsOutput, error) {
		key, err := m.key(ctx)
		if err != nil {
			return nil, err
		}
		if key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(in.Key)) != 1 {
			return nil, api.NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
		}
		body, err := m.render(ctx, in.Lang)
		if err != nil {
			return nil, err
		}
		return &icsOutput{ContentType: "text/calendar; charset=utf-8", CacheControl: "no-store", Body: body}, nil
	})
}

// key is the feed's key, "" while it is off.
func (m *Module) key(ctx context.Context) (string, error) {
	var key string
	err := m.db.QueryRowContext(ctx, "SELECT key FROM calendar_feed WHERE id = 1").Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return key, err
}

// newKey makes the feed's key, unless there is one and replace is false.
func (m *Module) newKey(ctx context.Context, replace bool) error {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	query := `INSERT INTO calendar_feed (id, key, created_at) VALUES (1, ?, ?) ON CONFLICT (id) DO NOTHING`
	if replace {
		query = `INSERT INTO calendar_feed (id, key, created_at) VALUES (1, ?, ?)
			ON CONFLICT (id) DO UPDATE SET key = excluded.key, created_at = excluded.created_at`
	}
	_, err := m.db.ExecContext(ctx, query, hex.EncodeToString(b), m.now().UnixMilli())
	return err
}

func (m *Module) feed(ctx context.Context) (*feedOutput, error) {
	key, err := m.key(ctx)
	if err != nil {
		return nil, err
	}
	out := &feedOutput{}
	if key != "" {
		out.Body = FeedBody{Enabled: true, Path: api.Prefix + "/calendar/ics/" + key}
	}
	return out, nil
}

// render writes the feed in iCalendar (RFC 5545), its words in lang.
func (m *Module) render(ctx context.Context, lang string) ([]byte, error) {
	var list struct {
		Certificates []certificate `json:"certificates"`
	}
	if err := m.call(ctx, via, http.MethodGet, "/certs/certificates", nil, &list); err != nil {
		return nil, err
	}
	tag := language.AmericanEnglish
	if lang != "" {
		if t, err := language.Parse(lang); err == nil {
			tag = m.cat.Match(t)
		}
	}
	text := func(key i18n.Key, args i18n.Args) string {
		s, err := m.cat.Format(tag, key, args)
		if err != nil {
			return string(key)
		}
		return s
	}
	certs := list.Certificates
	slices.SortFunc(certs, func(a, b certificate) int { return cmp.Compare(a.ID, b.ID) })
	zone := m.zone()
	stamp := m.now().UTC().Format("20060102T150405Z")

	var w icsWriter
	w.line("BEGIN:VCALENDAR")
	w.line("VERSION:2.0")
	w.line("PRODID:-//Just Enough Certs//JEC//EN")
	w.line("CALSCALE:GREGORIAN")
	w.line("METHOD:PUBLISH")
	w.prop("X-WR-CALNAME", text(i18n.CalendarFeedName, nil))
	w.line("REFRESH-INTERVAL;VALUE=DURATION:PT6H")
	w.line("X-PUBLISHED-TTL:PT6H")
	for _, c := range certs {
		if len(c.Domains) == 0 {
			continue
		}
		domain := c.Domains[0]
		id := strconv.FormatInt(c.ID, 10)
		detail := text(i18n.CalendarEventDomains, i18n.Args{"domains": strings.Join(c.Domains, ", ")})
		link := ""
		if m.baseURL != "" {
			link = strings.TrimRight(m.baseURL, "/") + "/certificates/" + id
		}
		if c.Current != nil {
			end := c.Current.NotAfter.In(zone)
			summary := text(i18n.CalendarEventEnds, i18n.Args{"domain": domain})
			w.line("BEGIN:VEVENT")
			w.prop("UID", "jec-"+id+"-end-"+strconv.FormatInt(c.Current.NotAfter.Unix(), 10))
			w.line("DTSTAMP:" + stamp)
			w.line("DTSTART;VALUE=DATE:" + end.Format("20060102"))
			w.line("DTEND;VALUE=DATE:" + end.AddDate(0, 0, 1).Format("20060102"))
			w.prop("SUMMARY", summary)
			w.prop("DESCRIPTION", detail)
			if link != "" {
				w.prop("URL", link)
			}
			w.line("TRANSP:TRANSPARENT")
			w.line("BEGIN:VALARM")
			w.line("ACTION:DISPLAY")
			w.line("TRIGGER:" + remindBefore)
			w.prop("DESCRIPTION", summary)
			w.line("END:VALARM")
			w.line("END:VEVENT")
		}
		if c.RenewAt != nil {
			w.line("BEGIN:VEVENT")
			w.prop("UID", "jec-"+id+"-renew-"+strconv.FormatInt(c.RenewAt.Unix(), 10))
			w.line("DTSTAMP:" + stamp)
			w.line("DTSTART:" + c.RenewAt.UTC().Format("20060102T150405Z"))
			w.line("DURATION:PT15M")
			w.prop("SUMMARY", text(i18n.CalendarEventRenews, i18n.Args{"domain": domain}))
			w.prop("DESCRIPTION", detail)
			if link != "" {
				w.prop("URL", link)
			}
			w.line("TRANSP:TRANSPARENT")
			w.line("END:VEVENT")
		}
	}
	// The ends of the sites watched, unless their module is off.
	var watched struct {
		Sites []struct {
			ID       int64      `json:"id"`
			Host     string     `json:"host"`
			Port     int        `json:"port"`
			Name     string     `json:"name"`
			NotAfter *time.Time `json:"notAfter"`
		} `json:"sites"`
	}
	err := m.call(ctx, via, http.MethodGet, "/watch/sites", nil, &watched)
	var ce *module.CallError
	if err != nil && (!errors.As(err, &ce) || ce.Status != http.StatusNotFound) {
		return nil, err
	}
	// An end, all day, with a reminder before it.
	ends := func(uid string, at time.Time, summary string) {
		end := at.In(zone)
		w.line("BEGIN:VEVENT")
		w.prop("UID", uid)
		w.line("DTSTAMP:" + stamp)
		w.line("DTSTART;VALUE=DATE:" + end.Format("20060102"))
		w.line("DTEND;VALUE=DATE:" + end.AddDate(0, 0, 1).Format("20060102"))
		w.prop("SUMMARY", summary)
		if m.baseURL != "" {
			w.prop("URL", strings.TrimRight(m.baseURL, "/")+"/sites")
		}
		w.line("TRANSP:TRANSPARENT")
		w.line("BEGIN:VALARM")
		w.line("ACTION:DISPLAY")
		w.line("TRIGGER:" + remindBefore)
		w.prop("DESCRIPTION", summary)
		w.line("END:VALARM")
		w.line("END:VEVENT")
	}
	for _, s := range watched.Sites {
		if s.NotAfter == nil {
			continue
		}
		site := s.Host
		if s.Port != 443 {
			site = net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
		}
		if s.Name != "" {
			site = s.Name + " (" + site + ")"
		}
		ends("jec-site-"+strconv.FormatInt(s.ID, 10)+"-end-"+strconv.FormatInt(s.NotAfter.Unix(), 10), *s.NotAfter,
			text(i18n.CalendarEventSiteEnds, i18n.Args{"site": site}))
	}
	// And the ends of the domain registrations, of the same module.
	if err == nil {
		var registrations struct {
			Domains []struct {
				Name      string     `json:"name"`
				ExpiresAt *time.Time `json:"expiresAt"`
			} `json:"domains"`
		}
		if err := m.call(ctx, via, http.MethodGet, "/watch/domains", nil, &registrations); err != nil {
			return nil, err
		}
		for _, d := range registrations.Domains {
			if d.ExpiresAt != nil {
				ends("jec-domain-"+d.Name+"-end-"+strconv.FormatInt(d.ExpiresAt.Unix(), 10), *d.ExpiresAt,
					text(i18n.CalendarEventDomainEnds, i18n.Args{"domain": d.Name}))
			}
		}
	}
	w.line("END:VCALENDAR")
	return []byte(w.String()), nil
}

// icsWriter writes the lines of iCalendar: ended by CRLF, folded at 75
// bytes, never inside a character.
type icsWriter struct{ strings.Builder }

func (w *icsWriter) line(s string) {
	limit := 75
	for len(s) > limit {
		cut := limit
		for cut > 0 && s[cut]&0xC0 == 0x80 { // within a UTF-8 character
			cut--
		}
		_, _ = w.WriteString(s[:cut] + "\r\n ") // a strings.Builder never fails
		s = s[cut:]
		limit = 74 // after the space that starts the next line
	}
	_, _ = w.WriteString(s + "\r\n")
}

// prop writes a property whose value is text, escaped.
func (w *icsWriter) prop(name, value string) {
	w.line(name + ":" + strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", `\n`).Replace(value))
}
