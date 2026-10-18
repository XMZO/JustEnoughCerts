package certs

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"software.sslmate.com/src/go-pkcs12"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/events"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

var start = time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)

const testPassword = "correct horse"

type fixture struct {
	t   *testing.T
	api humatest.TestAPI
	db  *sql.DB
	svc *service
	box *secrets.Scope
	reg *providers.Registry
	ca  *cryptoxtest.CA
	now time.Time

	triggered atomic.Int32 // runs of the issuance runner asked for
	fake      fakes        // the providers of module "fake"
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Migrate(t.Context(), "certs", New().Migrations()); err != nil {
		t.Fatal(err)
	}
	box, _, err := secrets.Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	bus := events.New(st.DB(), slog.New(slog.DiscardHandler), func(string) bool { return true })
	bus.Seal()
	f := &fixture{t: t, db: st.DB(), box: box.Scope("certs"), ca: cryptoxtest.NewCA(t), now: start}
	f.reg = providers.New(st.DB(), box.Scope("kernel"), func(string) bool { return true })
	f.addFakes()
	f.reg.Seal()
	f.svc = newService(st.DB(), bus.Scope("certs"), f.box, f.reg.Scope("certs"), slog.New(slog.DiscardHandler), func() time.Time { return f.now })
	f.svc.issuing.trigger = func() { f.triggered.Add(1) }
	f.svc.loc = time.UTC // the server's time zone, whatever the machine's
	f.reg.Scope("certs").UsedBy(f.svc.usesProvider)
	_, f.api = humatest.New(t)
	registerAPI(f.api, f.svc, func(_ context.Context, password string) error {
		if password != testPassword {
			return api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorPasswordWrong, nil).WithField("body.password", i18n.KernelErrorPasswordWrong)
		}
		return nil
	})
	return f
}

// upload is a certificate with its key, as PEM.
type upload struct {
	cert, key string
	key0      crypto.Signer
}

// leaf issues a certificate for names that ends after days.
func (f *fixture) leaf(days int, names ...string) upload {
	f.t.Helper()
	key := cryptoxtest.NewKey(f.t)
	leaf := f.ca.Leaf(f.t, key, f.now.Add(-time.Hour), f.now.Add(time.Duration(days)*day), names...)
	return upload{cryptoxtest.CertPEM(leaf, f.ca.Intermediate), cryptoxtest.KeyPEM(f.t, key), key}
}

func (f *fixture) call(resp *httptest.ResponseRecorder, wantStatus int, out any) {
	f.t.Helper()
	if resp.Code != wantStatus {
		f.t.Fatalf("status %d, want %d: %s", resp.Code, wantStatus, resp.Body.String())
	}
	if out != nil {
		if err := json.Unmarshal(resp.Body.Bytes(), out); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *fixture) add(u upload) CertificateBody {
	f.t.Helper()
	var c CertificateBody
	f.call(f.api.Post("/certificates", map[string]any{"certificate": u.cert, "privateKey": u.key}), http.StatusCreated, &c)
	return c
}

type apiError struct {
	Code   string         `json:"code"`
	Params map[string]any `json:"params"`
	Fields []struct {
		Location string `json:"location"`
		Code     string `json:"code"`
	} `json:"fields"`
}

func (f *fixture) apiError(resp *httptest.ResponseRecorder, wantStatus int) apiError {
	f.t.Helper()
	var e apiError
	f.call(resp, wantStatus, &e)
	return e
}

type storedEvent struct {
	typ, subject string
	data         contracts.CertificateEvent
}

func (f *fixture) events() []storedEvent {
	f.t.Helper()
	rows, err := f.db.QueryContext(f.t.Context(), "SELECT type, subject, data FROM kernel_events ORDER BY id")
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []storedEvent
	for rows.Next() {
		var e storedEvent
		var data string
		if err := rows.Scan(&e.typ, &e.subject, &data); err != nil {
			f.t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(data), &e.data); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func TestAdd(t *testing.T) {
	f := newFixture(t)
	u := f.leaf(90, "example.com", "www.example.com")
	c := f.add(u)

	if strings.Join(c.Domains, ",") != "example.com,www.example.com" || c.Source != "upload" || c.KeyType != "ecdsa-p256" ||
		c.RenewBeforeDays != 30 || c.State != "ok" || c.Current == nil {
		t.Fatalf("added %+v", c)
	}
	v := c.Current
	if v.Issuer != "Test Intermediate" || v.Origin != "upload" || len(v.Fingerprint) != 64 || v.Serial == "" ||
		!v.NotAfter.Equal(f.now.Add(90*day)) || strings.Join(v.Names, ",") != "example.com,www.example.com" {
		t.Errorf("version %+v", v)
	}

	// The key is stored sealed, and opens to the uploaded one.
	var sealed []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT sealed_key FROM certs_versions WHERE id = ?", v.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "PRIVATE KEY") {
		t.Error("the private key is stored in the clear")
	}
	keyPEM, err := f.box.Open(sealed, "key:"+v.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if key, err := cryptox.ParseKey(keyPEM); err != nil || !key.(interface{ Equal(crypto.PrivateKey) bool }).Equal(u.key0) {
		t.Errorf("the stored key is not the uploaded one: %v", err)
	}

	evs := f.events()
	if len(evs) != 1 || evs[0].typ != contracts.CertificateIssued || evs[0].subject != strconv.FormatInt(c.ID, 10) ||
		evs[0].data.VersionID != v.ID || evs[0].data.CertificateID != c.ID {
		t.Errorf("events %+v", evs)
	}
}

func TestAddWithKeyInline(t *testing.T) {
	f := newFixture(t)
	u := f.leaf(90, "example.com")
	var c CertificateBody
	f.call(f.api.Post("/certificates", map[string]any{"certificate": u.key + u.cert, "renewBeforeDays": 14}), http.StatusCreated, &c)
	if c.RenewBeforeDays != 14 {
		t.Errorf("renewBeforeDays %d", c.RenewBeforeDays)
	}
}

func TestAddErrors(t *testing.T) {
	f := newFixture(t)
	u := f.leaf(90, "example.com")
	other := f.leaf(90, "example.com")
	for name, tt := range map[string]struct {
		body  map[string]any
		code  string
		field string
	}{
		"no key":       {map[string]any{"certificate": u.cert}, "certs.error.no_key", "body.privateKey"},
		"wrong key":    {map[string]any{"certificate": u.cert, "privateKey": other.key}, "certs.error.key_mismatch", "body.privateKey"},
		"not PEM":      {map[string]any{"certificate": "hello", "privateKey": u.key}, "certs.error.no_certificate", "body.certificate"},
		"two keys":     {map[string]any{"certificate": u.cert, "privateKey": u.key + other.key}, "certs.error.several_keys", "body.privateKey"},
		"broken chain": {map[string]any{"certificate": "-----BEGIN CERTIFICATE-----\nAQID\n-----END CERTIFICATE-----\n", "privateKey": u.key}, "certs.error.bad_certificate", "body.certificate"},
	} {
		e := f.apiError(f.api.Post("/certificates", tt.body), http.StatusUnprocessableEntity)
		if e.Code != tt.code || len(e.Fields) != 1 || e.Fields[0].Location != tt.field || e.Fields[0].Code != tt.code {
			t.Errorf("%s: %+v", name, e)
		}
	}
	e := f.apiError(f.api.Post("/certificates", map[string]any{"privateKey": u.key}), http.StatusUnprocessableEntity)
	if e.Code != "certs.error.no_certificate" || len(e.Fields) != 1 || e.Fields[0].Location != "body.certificate" {
		t.Errorf("without a certificate: %+v", e)
	}

	c := f.add(u)
	e = f.apiError(f.api.Post("/certificates", map[string]any{"certificate": u.cert, "privateKey": u.key}), http.StatusConflict)
	if e.Code != "certs.error.duplicate" || e.Params["certificateId"] != float64(c.ID) {
		t.Errorf("duplicate: %+v", e)
	}
}

func TestListAndStates(t *testing.T) {
	f := newFixture(t)
	f.add(f.leaf(120, "later.example.com"))
	f.add(f.leaf(20, "soon.example.com"))
	f.add(f.leaf(1, "gone.example.com"))
	f.now = f.now.Add(2 * day)

	var out struct{ Certificates []CertificateBody }
	f.call(f.api.Get("/certificates"), http.StatusOK, &out)
	var got []string
	for _, c := range out.Certificates {
		got = append(got, c.Domains[0]+"="+c.State)
	}
	if strings.Join(got, " ") != "gone.example.com=expired soon.example.com=expiring later.example.com=ok" {
		t.Errorf("list: %v", got)
	}
}

func TestDetail(t *testing.T) {
	f := newFixture(t)
	c := f.add(f.leaf(90, "example.com"))
	f.now = f.now.Add(30 * day)
	next := f.leaf(90, "example.com")
	f.call(f.api.Post("/certificates/"+strconv.FormatInt(c.ID, 10)+"/versions", map[string]any{"certificate": next.cert, "privateKey": next.key}), http.StatusCreated, nil)

	var d struct {
		Certificate CertificateBody
		Versions    []VersionBody
		Chain       []ChainEntry
	}
	f.call(f.api.Get("/certificates/"+strconv.FormatInt(c.ID, 10)), http.StatusOK, &d)
	if len(d.Versions) != 2 || d.Versions[0].ID != d.Certificate.Current.ID || d.Versions[1].ID != c.Current.ID {
		t.Errorf("versions: %+v", d.Versions)
	}
	if len(d.Chain) != 2 || d.Chain[0].Subject != "example.com" || d.Chain[0].Issuer != "Test Intermediate" ||
		d.Chain[1].Subject != "Test Intermediate" || d.Chain[1].Issuer != "Test Root" {
		t.Errorf("chain: %+v", d.Chain)
	}
	f.apiError(f.api.Get("/certificates/999"), http.StatusNotFound)
}

func TestUploadVersion(t *testing.T) {
	f := newFixture(t)
	c := f.add(f.leaf(60, "example.com"))
	path := "/certificates/" + strconv.FormatInt(c.ID, 10) + "/versions"

	older := f.leaf(30, "example.com")
	e := f.apiError(f.api.Post(path, map[string]any{"certificate": older.cert, "privateKey": older.key}), http.StatusUnprocessableEntity)
	if e.Code != "certs.error.not_newer" {
		t.Errorf("an older version: %+v", e)
	}
	elsewhere := f.leaf(90, "other.example.com")
	e = f.apiError(f.api.Post(path, map[string]any{"certificate": elsewhere.cert, "privateKey": elsewhere.key}), http.StatusUnprocessableEntity)
	if e.Code != "certs.error.other_domains" || e.Params["domain"] != "example.com" {
		t.Errorf("a version for other domains: %+v", e)
	}
	missing := f.leaf(90, "example.com")
	f.apiError(f.api.Post("/certificates/999/versions", map[string]any{"certificate": missing.cert, "privateKey": missing.key}), http.StatusNotFound)

	newer := f.leaf(200, "example.com", "www.example.com")
	var got CertificateBody
	f.call(f.api.Post(path, map[string]any{"certificate": newer.cert, "privateKey": newer.key}), http.StatusCreated, &got)
	if got.Current.ID == c.Current.ID || !got.Current.NotAfter.Equal(f.now.Add(200*day)) || len(got.Domains) != 2 {
		t.Errorf("after the upload: %+v", got)
	}
	if evs := f.events(); len(evs) != 2 || evs[1].typ != contracts.CertificateIssued || evs[1].data.VersionID != got.Current.ID {
		t.Errorf("events %+v", evs)
	}
}

func TestUpdateAndDelete(t *testing.T) {
	f := newFixture(t)
	c := f.add(f.leaf(40, "example.com"))
	path := "/certificates/" + strconv.FormatInt(c.ID, 10)
	var got CertificateBody
	f.call(f.api.Patch(path, map[string]any{"renewBeforeDays": 45}), http.StatusOK, &got)
	if got.RenewBeforeDays != 45 || got.State != "expiring" {
		t.Errorf("after the update: %+v", got)
	}
	f.apiError(f.api.Patch(path, map[string]any{"renewBeforeDays": 0}), http.StatusUnprocessableEntity)
	f.apiError(f.api.Patch("/certificates/999", map[string]any{"renewBeforeDays": 10}), http.StatusNotFound)

	f.call(f.api.Delete(path), http.StatusNoContent, nil)
	f.apiError(f.api.Get(path), http.StatusNotFound)
	f.apiError(f.api.Delete(path), http.StatusNotFound)
	var n int
	if err := f.db.QueryRowContext(t.Context(), "SELECT count(*) FROM certs_versions").Scan(&n); err != nil || n != 0 {
		t.Errorf("%d versions left, %v", n, err)
	}
	if evs := f.events(); len(evs) != 2 || evs[1].typ != contracts.CertificateDeleted || evs[1].data.Domains[0] != "example.com" {
		t.Errorf("events %+v", evs)
	}
}

func TestCheckExpiry(t *testing.T) {
	f := newFixture(t)
	f.add(f.leaf(40, "example.com"))
	f.add(f.leaf(3, "soon.example.com"))
	expiring := func() []string {
		var out []string
		for _, e := range f.events() {
			if e.typ == contracts.CertificateExpiring {
				out = append(out, e.data.Domains[0]+"="+string(e.data.Expiry))
			}
		}
		return out
	}
	check := func() {
		t.Helper()
		if err := f.svc.checkExpiry(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	check()
	// Three days left: only the urgent notice, not the window one as well.
	if got := strings.Join(expiring(), " "); got != "soon.example.com=urgent" {
		t.Errorf("first check: %s", got)
	}
	check()
	f.now = f.now.Add(11 * day) // 29 days left for example.com, soon.example.com expired
	check()
	check()
	f.now = f.now.Add(23 * day) // 6 days left
	check()
	f.now = f.now.Add(7 * day)
	check()
	want := "soon.example.com=urgent soon.example.com=expired example.com=window example.com=urgent example.com=expired"
	if got := strings.Join(expiring(), " "); got != want {
		t.Errorf("notices:\n got %s\nwant %s", got, want)
	}
}

func TestDownload(t *testing.T) {
	f := newFixture(t)
	u := f.leaf(90, "*.example.com", "example.com")
	c := f.add(u)
	path := "/certificates/" + strconv.FormatInt(c.ID, 10) + "/versions/" + strconv.FormatInt(c.Current.ID, 10) + "/download"

	e := f.apiError(f.api.Post(path, map[string]any{"password": "wrong"}), http.StatusUnprocessableEntity)
	if e.Code != "kernel.error.password_wrong" || len(e.Fields) != 1 || e.Fields[0].Location != "body.password" {
		t.Errorf("wrong password: %+v", e)
	}
	f.apiError(f.api.Post("/certificates/999/versions/1/download", map[string]any{"password": testPassword}), http.StatusNotFound)
	f.apiError(f.api.Post("/certificates/"+strconv.FormatInt(c.ID, 10)+"/versions/999/download", map[string]any{"password": testPassword}), http.StatusNotFound)

	resp := f.api.Post(path, map[string]any{"password": testPassword})
	if resp.Code != http.StatusOK || resp.Header().Get("Content-Type") != "application/zip" || resp.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("download: %d %v", resp.Code, resp.Header())
	}
	want := `attachment; filename="star.example.com_` + f.now.Add(90*day).Format(time.DateOnly) + `.zip"`
	if got := resp.Header().Get("Content-Disposition"); got != want {
		t.Errorf("Content-Disposition %q, want %q", got, want)
	}
	zr, err := zip.NewReader(bytes.NewReader(resp.Body.Bytes()), int64(resp.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, zf := range zr.File {
		r, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		_, _ = b.ReadFrom(r)
		_ = r.Close()
		files[zf.Name] = b.String()
	}
	const end = "-----END CERTIFICATE-----\n"
	leaf, inter, _ := strings.Cut(u.cert, end)
	leaf += end
	switch {
	case len(files) != 4:
		t.Errorf("files %v", len(files))
	case files["cert.pem"] != leaf, files["chain.pem"] != inter, files["fullchain.pem"] != u.cert:
		t.Errorf("certificates in the ZIP:\n%v", files)
	case files["privkey.pem"] != u.key:
		t.Error("privkey.pem is not the uploaded key")
	}
	evs := f.events()
	if last := evs[len(evs)-1]; last.typ != contracts.CertificateKeyDownloaded || last.data.VersionID != c.Current.ID {
		t.Errorf("events %+v", evs)
	}
}

// pfx is u as a PKCS #12 file, sealed with password.
func (f *fixture) pfx(u upload, password string) []byte {
	f.t.Helper()
	b, err := cryptox.ParseBundle([]byte(u.cert), []byte(u.key))
	if err != nil {
		f.t.Fatal(err)
	}
	data, err := pkcs12.Modern.Encode(b.Key, b.Leaf, b.Chain, password)
	if err != nil {
		f.t.Fatal(err)
	}
	return data
}

func TestAddPFX(t *testing.T) {
	f := newFixture(t)
	u := f.leaf(90, "example.com")
	data := f.pfx(u, "correct horse")
	for name, tt := range map[string]struct {
		body  map[string]any
		code  string
		field string
	}{
		"wrong password": {map[string]any{"pfx": data, "pfxPassword": "wrong horse"}, "certs.error.pfx_password", "body.pfxPassword"},
		"not PFX":        {map[string]any{"pfx": []byte("hello")}, "certs.error.bad_pfx", "body.pfx"},
		"wrong key": {
			map[string]any{"pfx": func() []byte {
				b, _ := cryptox.ParseBundle([]byte(u.cert), []byte(u.key))
				d, _ := pkcs12.Modern.Encode(cryptoxtest.NewKey(t), b.Leaf, b.Chain, "")
				return d
			}()},
			"certs.error.key_mismatch", "body.pfx",
		},
	} {
		e := f.apiError(f.api.Post("/certificates", tt.body), http.StatusUnprocessableEntity)
		if e.Code != tt.code || len(e.Fields) != 1 || e.Fields[0].Location != tt.field {
			t.Errorf("%s: %+v", name, e)
		}
	}

	var c CertificateBody
	f.call(f.api.Post("/certificates", map[string]any{"pfx": data, "pfxPassword": "correct horse"}), http.StatusCreated, &c)
	if c.Current == nil || c.Domains[0] != "example.com" {
		t.Fatalf("added %+v", c)
	}
	// A new version, as a file too.
	next := f.leaf(120, "example.com")
	f.call(f.api.Post("/certificates/"+strconv.FormatInt(c.ID, 10)+"/versions", map[string]any{"pfx": f.pfx(next, "")}), http.StatusCreated, &c)
	if !c.Current.NotAfter.After(f.now.Add(100 * day)) {
		t.Errorf("the new version ends %s", c.Current.NotAfter)
	}
}
