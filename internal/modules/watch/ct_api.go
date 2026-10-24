package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
)

// TransparencyBody is the search of certificate transparency logs, as the
// API tells it.
type TransparencyBody struct {
	Enabled      bool                    `json:"enabled"`
	HasKey       bool                    `json:"hasKey" doc:"A Cert Spotter API key is saved"`
	Domains      []TransparencyDomain    `json:"domains" doc:"The registered domains searched: those JEC holds certificates under"`
	Certificates []SpottedBody           `json:"certificates" doc:"The certificates found that are not JEC's, not trusted and not ignored, the newest first"`
	Own          int                     `json:"own" doc:"How many of the certificates found are JEC's"`
	Ignored      int                     `json:"ignored"`
	Trusted      []TransparencyTrustBody `json:"trusted" doc:"The names and issuers trusted, with how many certificates found each covers"`
}

// TransparencyDomain is a domain searched.
type TransparencyDomain struct {
	Name      string     `json:"name"`
	Scanned   bool       `json:"scanned" doc:"A search went to the end once; till then, what is found that was issued before the search was first switched on is listed but not told of"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	CheckAt   time.Time  `json:"checkAt"`
	Error     string     `json:"error,omitempty" doc:"Why the last search failed, as the search said it"`
}

// SpottedBody is a certificate found that is not JEC's.
type SpottedBody struct {
	ID        string    `json:"id" doc:"Cert Spotter's ID of the certificate"`
	Domain    string    `json:"domain"`
	Names     []string  `json:"names"`
	Issuer    string    `json:"issuer"`
	IssuerDN  string    `json:"issuerDn"`
	SHA256    string    `json:"sha256" doc:"SHA-256 of the certificate, lowercase hex; of its precertificate while only that was logged"`
	Pubkey    string    `json:"pubkey" doc:"SHA-256 of the certificate's SubjectPublicKeyInfo, lowercase hex: the same for the precertificate, and what a server's certificate is told by"`
	NotBefore time.Time `json:"notBefore"`
	NotAfter  time.Time `json:"notAfter"`
	State     string    `json:"state" enum:"new,baseline,unknown" doc:"new: found lately, told of soon unless it turns out to be JEC's; baseline: issued before the search was first switched on, not told of; unknown: told of, or to look at as no longer trusted"`
	FoundAt   time.Time `json:"foundAt"`
}

// TransparencyTrustBody is a trust of the certificates of names by an
// issuer, as those a CDN makes.
type TransparencyTrustBody struct {
	Names  []string `json:"names" minItems:"1" maxItems:"500"`
	Issuer string   `json:"issuer" maxLength:"300"`
	Count  int      `json:"count,omitempty" readOnly:"true"`
}

type transparencyOutput struct{ Body TransparencyBody }

type transparencyInput struct {
	Body struct {
		Enabled bool    `json:"enabled"`
		APIKey  *string `json:"apiKey,omitempty" maxLength:"200" doc:"A Cert Spotter API key; left out, the saved one stays; empty, it goes"`
	}
}

type spottedInput struct {
	ID string `path:"id" maxLength:"40"`
}

type untrustInput struct {
	Body TransparencyTrustBody
}

func registerTransparencyAPI(a huma.API, m *Module) {
	tags := []string{"watch"}
	huma.Register(a, huma.Operation{
		OperationID: "watch-transparency", Method: http.MethodGet, Path: "/transparency", Tags: tags,
		Summary: "Tell the search of certificate transparency logs and what it found",
	}, func(ctx context.Context, _ *struct{}) (*transparencyOutput, error) {
		return m.transparency(ctx)
	})
	huma.Register(a, huma.Operation{
		OperationID: "watch-transparency-save", Method: http.MethodPut, Path: "/transparency", Tags: tags,
		Summary:     "Switch the search of certificate transparency logs, and set its API key",
		Description: "The search sends the registered domains JEC holds certificates under to Cert Spotter, of SSLMate.",
	}, func(ctx context.Context, in *transparencyInput) (*transparencyOutput, error) {
		now := m.now().UnixMilli()
		enabled := int64(0)
		if in.Body.Enabled {
			enabled = 1
		}
		tx, err := m.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO watch_ct (id, enabled, updated_at) VALUES (1, ?, ?)
			 ON CONFLICT (id) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at`, enabled, now); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE watch_ct SET since = ? WHERE id = 1 AND enabled = 1 AND since = 0", now); err != nil {
			return nil, err
		}
		if in.Body.APIKey != nil {
			var sealed []byte
			if key := strings.TrimSpace(*in.Body.APIKey); key != "" {
				sealed = m.secrets.Seal([]byte(key), ctKeyContext)
			}
			if _, err := tx.ExecContext(ctx, "UPDATE watch_ct SET api_key = ? WHERE id = 1", sealed); err != nil {
				return nil, err
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		if in.Body.Enabled {
			m.trigger("transparency")
		}
		return m.transparency(ctx)
	})
	huma.Register(a, huma.Operation{
		OperationID: "watch-transparency-check", Method: http.MethodPost, Path: "/transparency/check", Tags: tags, DefaultStatus: http.StatusAccepted,
		Summary:     "Search every domain soon",
		Description: "The search runs in the background, as far as Cert Spotter allows.",
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		if _, err := m.db.ExecContext(ctx, "UPDATE watch_ct_domains SET check_at = ?", m.now().UnixMilli()); err != nil {
			return nil, err
		}
		m.trigger("transparency")
		return nil, nil
	})
	huma.Register(a, huma.Operation{
		OperationID: "watch-transparency-ignore", Method: http.MethodPost, Path: "/transparency/certificates/{id}/ignore", Tags: tags, DefaultStatus: http.StatusNoContent,
		Summary: "Ignore a certificate found",
	}, func(ctx context.Context, in *spottedInput) (*struct{}, error) {
		m.spottedNamed(ctx, in.ID)
		res, err := m.db.ExecContext(ctx, "UPDATE watch_ct_certificates SET state = ? WHERE id = ? AND state IN (?, ?, ?)",
			ctIgnored, in.ID, ctNew, ctBaseline, ctUnknown)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, api.NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
		}
		return nil, nil
	})
	huma.Register(a, huma.Operation{
		OperationID: "watch-transparency-trust", Method: http.MethodPost, Path: "/transparency/certificates/{id}/trust", Tags: tags, DefaultStatus: http.StatusNoContent,
		Summary:     "Trust the certificates for the names of a certificate found, by its issuer",
		Description: "As those a CDN makes: the certificates found and found later for the same names by the same issuer are not told of.",
	}, func(ctx context.Context, in *spottedInput) (*struct{}, error) {
		m.spottedNamed(ctx, in.ID)
		var names, issuer string
		err := m.db.QueryRowContext(ctx, "SELECT names, issuer FROM watch_ct_certificates WHERE id = ?", in.ID).Scan(&names, &issuer)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, api.NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
		}
		if err != nil {
			return nil, err
		}
		tx, err := m.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, "INSERT INTO watch_ct_trusted (names, issuer, created_at) VALUES (?, ?, ?) ON CONFLICT (names, issuer) DO NOTHING",
			names, issuer, m.now().UnixMilli()); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE watch_ct_certificates SET state = ? WHERE names = ? AND issuer = ? AND state IN (?, ?, ?)",
			ctTrusted, names, issuer, ctNew, ctBaseline, ctUnknown); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	})
	huma.Register(a, huma.Operation{
		OperationID: "watch-transparency-untrust", Method: http.MethodPost, Path: "/transparency/untrust", Tags: tags, DefaultStatus: http.StatusNoContent,
		Summary:     "Stop trusting the certificates for some names by an issuer",
		Description: "The certificates found that it covered are listed again, without being told of: as there before when issued before the search was first switched on, else as to look at.",
	}, func(ctx context.Context, in *untrustInput) (*struct{}, error) {
		names := slices.Clone(in.Body.Names)
		slices.Sort(names)
		raw, _ := json.Marshal(slices.Compact(names))
		tx, err := m.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback() }()
		res, err := tx.ExecContext(ctx, "DELETE FROM watch_ct_trusted WHERE names = ? AND issuer = ?", string(raw), in.Body.Issuer)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, api.NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE watch_ct_certificates SET state = CASE WHEN not_before < (SELECT since FROM watch_ct WHERE id = 1) THEN ? ELSE ? END
			 WHERE names = ? AND issuer = ? AND state = ?`,
			ctBaseline, ctUnknown, string(raw), in.Body.Issuer, ctTrusted); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	})
}

// transparency tells the search and what it found.
func (m *Module) transparency(ctx context.Context) (*transparencyOutput, error) {
	out := &transparencyOutput{}
	b := &out.Body
	b.Domains, b.Certificates, b.Trusted = []TransparencyDomain{}, []SpottedBody{}, []TransparencyTrustBody{}
	var enabled int64
	var key []byte
	err := m.db.QueryRowContext(ctx, "SELECT enabled, api_key FROM watch_ct WHERE id = 1").Scan(&enabled, &key)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	b.Enabled, b.HasKey = enabled == 1, len(key) > 0

	rows, err := m.db.QueryContext(ctx, "SELECT name, scanned, checked_at, check_at, error FROM watch_ct_domains")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d TransparencyDomain
		var scanned, checkAt int64
		var checked sql.NullInt64
		if err := rows.Scan(&d.Name, &scanned, &checked, &checkAt, &d.Error); err != nil {
			_ = rows.Close()
			return nil, err
		}
		d.Scanned = scanned == 1
		if checked.Valid {
			t := time.UnixMilli(checked.Int64).UTC()
			d.CheckedAt = &t
		}
		d.CheckAt = time.UnixMilli(checkAt).UTC()
		b.Domains = append(b.Domains, d)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	slices.SortFunc(b.Domains, func(x, y TransparencyDomain) int { return strings.Compare(x.Name, y.Name) })

	counts := map[string]int{}
	rows, err = m.db.QueryContext(ctx,
		`SELECT id, domain, names, issuer, issuer_dn, sha256, pubkey, not_before, not_after, state, found_at
		 FROM watch_ct_certificates`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s SpottedBody
		var names string
		var notBefore, notAfter, found int64
		if err := rows.Scan(&s.ID, &s.Domain, &names, &s.Issuer, &s.IssuerDN, &s.SHA256, &s.Pubkey, &notBefore, &notAfter, &s.State, &found); err != nil {
			_ = rows.Close()
			return nil, err
		}
		switch s.State {
		case ctOwn:
			b.Own++
			continue
		case ctIgnored:
			b.Ignored++
			continue
		case ctTrusted:
			counts[trustKey(names, s.Issuer)]++
			continue
		}
		if json.Unmarshal([]byte(names), &s.Names) != nil || s.Names == nil {
			s.Names = []string{}
		}
		s.NotBefore, s.NotAfter, s.FoundAt = time.UnixMilli(notBefore).UTC(), time.UnixMilli(notAfter).UTC(), time.UnixMilli(found).UTC()
		b.Certificates = append(b.Certificates, s)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	// Those told of first, then the newest.
	rank := map[string]int{ctUnknown: 0, ctNew: 1, ctBaseline: 2}
	slices.SortFunc(b.Certificates, func(x, y SpottedBody) int {
		if c := rank[x.State] - rank[y.State]; c != 0 {
			return c
		}
		if c := y.NotBefore.Compare(x.NotBefore); c != 0 {
			return c
		}
		return strings.Compare(x.ID, y.ID)
	})

	rows, err = m.db.QueryContext(ctx, "SELECT names, issuer FROM watch_ct_trusted")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t TransparencyTrustBody
		var names string
		if err := rows.Scan(&names, &t.Issuer); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if json.Unmarshal([]byte(names), &t.Names) != nil || t.Names == nil {
			t.Names = []string{}
		}
		t.Count = counts[trustKey(names, t.Issuer)]
		b.Trusted = append(b.Trusted, t)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	slices.SortFunc(b.Trusted, func(x, y TransparencyTrustBody) int {
		if c := strings.Compare(strings.Join(x.Names, ","), strings.Join(y.Names, ",")); c != 0 {
			return c
		}
		return strings.Compare(x.Issuer, y.Issuer)
	})
	return out, nil
}

// spottedNamed gives the audit log the first name of certificate id found
// in the logs.
func (m *Module) spottedNamed(ctx context.Context, id string) {
	var raw string
	var names []string
	if m.db.QueryRowContext(ctx, "SELECT names FROM watch_ct_certificates WHERE id = ?", id).Scan(&raw) == nil &&
		json.Unmarshal([]byte(raw), &names) == nil && len(names) > 0 {
		api.Subject(ctx, names[0])
	}
}
