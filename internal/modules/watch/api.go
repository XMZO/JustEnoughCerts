package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/net/idna"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
)

// maxSites bounds the sites watched.
const maxSites = 500

// SiteBody is a site as the API tells it.
type SiteBody struct {
	ID          int64      `json:"id"`
	Host        string     `json:"host"`
	Port        int        `json:"port"`
	Name        string     `json:"name,omitempty"`
	State       string     `json:"state" enum:"pending,ok,expiring,expired,invalid,unreachable" doc:"pending: not checked yet; expiring: ends within three weeks; invalid: not trusted, or not for the host; unreachable: no TLS on the port, the last certificate seen kept"`
	NotAfter    *time.Time `json:"notAfter,omitempty"`
	Issuer      string     `json:"issuer,omitempty"`
	Names       []string   `json:"names"`
	Fingerprint string     `json:"fingerprint,omitempty"`
	Error       string     `json:"error,omitempty" doc:"What went wrong, as the TLS library said it, for invalid and unreachable"`
	Incomplete  bool       `json:"incomplete,omitempty" doc:"The site sends its certificate without the intermediates clients need: browsers on computers fetch them, many apps, Android and curl fail"`
	CheckedAt   *time.Time `json:"checkedAt,omitempty"`
	CheckAt     time.Time  `json:"checkAt" doc:"When the next check comes"`
	CreatedAt   time.Time  `json:"createdAt"`
}

type listOutput struct {
	Body struct {
		Sites []SiteBody `json:"sites"`
	}
}

type siteOutput struct{ Body SiteBody }

type addInput struct {
	Body struct {
		Host string `json:"host" minLength:"1" maxLength:"300" doc:"A host, with a port when it is not 443, or a URL"`
		Name string `json:"name,omitempty" maxLength:"100" doc:"What people call it"`
	}
}

type idInput struct {
	ID int64 `path:"id"`
}

func registerAPI(a huma.API, m *Module) {
	tags := []string{"watch"}
	huma.Register(a, huma.Operation{
		OperationID: "watch-list", Method: http.MethodGet, Path: "/sites", Tags: tags,
		Summary: "List the sites watched, with what each served when last checked",
	}, func(ctx context.Context, _ *struct{}) (*listOutput, error) {
		sites, err := m.list(ctx, 0)
		if err != nil {
			return nil, err
		}
		out := &listOutput{}
		out.Body.Sites = sites
		return out, nil
	})
	huma.Register(a, huma.Operation{
		OperationID: "watch-add", Method: http.MethodPost, Path: "/sites", Tags: tags, DefaultStatus: http.StatusCreated,
		Summary: "Watch a site, which is checked at once",
	}, func(ctx context.Context, in *addInput) (*siteOutput, error) {
		host, port, ok := parseHost(in.Body.Host)
		if !ok {
			return nil, api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil).WithField("body.host", i18n.WatchErrorHost)
		}
		var n int
		if err := m.db.QueryRowContext(ctx, "SELECT count(*) FROM watch_sites").Scan(&n); err != nil {
			return nil, err
		}
		if n >= maxSites {
			return nil, api.NewError(http.StatusConflict, i18n.WatchErrorTooMany, map[string]any{"max": maxSites})
		}
		now := m.now()
		var id int64
		err := m.db.QueryRowContext(ctx,
			`INSERT INTO watch_sites (host, port, name, created_at, check_at, state, issuer, names, fingerprint, error, notified)
			 VALUES (?, ?, ?, ?, ?, ?, '', '[]', '', '', '') ON CONFLICT (host, port) DO NOTHING RETURNING id`,
			host, port, strings.TrimSpace(in.Body.Name), now.UnixMilli(), now.UnixMilli(), statePending).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, api.NewError(http.StatusConflict, i18n.KernelErrorInvalidRequest, nil).WithField("body.host", i18n.WatchErrorWatched)
		}
		if err != nil {
			return nil, err
		}
		if err := m.check(ctx, id); err != nil {
			return nil, err
		}
		m.trigger("domains")
		return m.one(ctx, id)
	})
	huma.Register(a, huma.Operation{
		OperationID: "watch-delete", Method: http.MethodDelete, Path: "/sites/{id}", Tags: tags, DefaultStatus: http.StatusNoContent,
		Summary: "Stop watching a site",
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		res, err := m.db.ExecContext(ctx, "DELETE FROM watch_sites WHERE id = ?", in.ID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, api.NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
		}
		m.trigger("domains")
		return nil, nil
	})
	huma.Register(a, huma.Operation{
		OperationID: "watch-check", Method: http.MethodPost, Path: "/sites/{id}/check", Tags: tags,
		Summary: "Check a site now",
	}, func(ctx context.Context, in *idInput) (*siteOutput, error) {
		if err := m.check(ctx, in.ID); errors.Is(err, errNotFound) {
			return nil, api.NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
		} else if err != nil {
			return nil, err
		}
		return m.one(ctx, in.ID)
	})
	registerDomainsAPI(a, m)
	registerTransparencyAPI(a, m)
}

func (m *Module) one(ctx context.Context, id int64) (*siteOutput, error) {
	sites, err := m.list(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(sites) == 0 {
		return nil, api.NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
	}
	return &siteOutput{Body: sites[0]}, nil
}

// list returns the sites, by name and host, or only site id when not 0.
func (m *Module) list(ctx context.Context, id int64) ([]SiteBody, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT id, host, port, name, state, not_after, issuer, names, fingerprint, error, checked_at, check_at, created_at, incomplete
		 FROM watch_sites WHERE ? = 0 OR id = ? ORDER BY CASE WHEN name = '' THEN host ELSE name END, host, port`, id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SiteBody{}
	for rows.Next() {
		var s SiteBody
		var notAfter, checked sql.NullInt64
		var names string
		var checkAt, created, incomplete int64
		if err := rows.Scan(&s.ID, &s.Host, &s.Port, &s.Name, &s.State, &notAfter, &s.Issuer, &names, &s.Fingerprint, &s.Error,
			&checked, &checkAt, &created, &incomplete); err != nil {
			return nil, err
		}
		s.Incomplete = incomplete == 1 && s.State != stateUnreachable
		if err := json.Unmarshal([]byte(names), &s.Names); err != nil || s.Names == nil {
			s.Names = []string{}
		}
		if notAfter.Valid {
			t := time.UnixMilli(notAfter.Int64).UTC()
			s.NotAfter = &t
		}
		if checked.Valid {
			t := time.UnixMilli(checked.Int64).UTC()
			s.CheckedAt = &t
		}
		s.CheckAt, s.CreatedAt = time.UnixMilli(checkAt).UTC(), time.UnixMilli(created).UTC()
		out = append(out, s)
	}
	return out, rows.Err()
}

var hostProfile = idna.New(idna.MapForLookup(), idna.BidiRule(), idna.VerifyDNSLength(true))

// parseHost reads a host to watch: a name or an address, with a port or
// not (443), or a URL, as pasted from a browser.
func parseHost(s string) (string, int, bool) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return "", 0, false
		}
		s = u.Host
	} else if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	host, port := s, 443
	if h, p, err := net.SplitHostPort(s); err == nil {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", 0, false
		}
		host, port = h, n
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), port, true
	}
	ascii, err := hostProfile.ToASCII(host)
	if err != nil || ascii == "" || !strings.Contains(ascii, ".") && ascii != "localhost" {
		return "", 0, false
	}
	return strings.ToLower(ascii), port, true
}
