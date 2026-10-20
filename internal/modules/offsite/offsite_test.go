package offsite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

// fakeS3 keeps what is put, by path, checking each request is signed.
type fakeS3 struct {
	t       *testing.T
	mu      sync.Mutex
	objects map[string][]byte
	refuse  bool
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	sum := sha256.Sum256(body)
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKID/") ||
		r.Header.Get("X-Amz-Content-Sha256") != hex.EncodeToString(sum[:]) {
		f.t.Errorf("unsigned %s %s: %v", r.Method, r.URL.Path, r.Header)
	}
	if f.refuse {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`)
		return
	}
	switch r.Method {
	case http.MethodPut:
		f.objects[r.URL.Path] = body
	case http.MethodDelete:
		delete(f.objects, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}
}

func TestOffsite(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New()
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Migrate(t.Context(), "offsite", m.Migrations()); err != nil {
		t.Fatal(err)
	}
	box, _, err := secrets.Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	s3 := &fakeS3{t: t, objects: map[string][]byte{}}
	srv := httptest.NewServer(s3)
	defer srv.Close()
	m.db, m.secrets, m.log, m.dataDir = st.DB(), box.Scope("offsite"), slog.New(slog.DiscardHandler), dir
	m.client = srv.Client()
	m.now = func() time.Time { return time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC) }
	m.call = func(_ context.Context, _, _, _ string, _, out any) error {
		return json.Unmarshal([]byte(`{"autoBackup":{"keep":7}}`), out)
	}
	_, a := humatest.New(t)
	m.register(a)

	// Off, nothing is copied; on, the bucket is tried first.
	backups := filepath.Join(dir, backupDir)
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"jec-20261019-020000.db", "jec-20261020-020000.db", "jec-20261020-030000.db.part", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(backups, name), []byte("backup "+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.copyNew(t.Context()); err != nil || len(s3.objects) != 0 {
		t.Fatalf("while off: %v %v", err, s3.objects)
	}
	var first OffsiteSettingsBody
	if resp := a.Get("/settings"); json.Unmarshal(resp.Body.Bytes(), &first) != nil || first.Kind != kindS3 {
		t.Errorf("before any settings: %s", resp.Body)
	}
	settings := map[string]any{"enabled": true, "endpoint": srv.URL, "region": "auto", "bucket": "jec-backups", "prefix": "/server-1/",
		"pathStyle": true, "accessKey": "AKID", "secretKey": "s3cret"}
	if resp := a.Put("/settings", map[string]any{"enabled": true, "endpoint": "ftp://x"}); resp.Code != http.StatusUnprocessableEntity {
		t.Errorf("a bad endpoint: %d", resp.Code)
	}
	s3.refuse = true
	if resp := a.Put("/settings", settings); resp.Code != http.StatusUnprocessableEntity || !strings.Contains(resp.Body.String(), "AccessDenied") {
		t.Errorf("refused: %d %s", resp.Code, resp.Body)
	}
	s3.refuse = false
	resp := a.Put("/settings", settings)
	var out OffsiteSettingsBody
	if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &out) != nil || !out.Enabled || !out.HasSecret || out.Prefix != "server-1/" || out.BackupsOff {
		t.Fatalf("saved: %d %s", resp.Code, resp.Body)
	}
	if len(s3.objects) != 0 {
		t.Errorf("the test file stayed: %v", s3.objects)
	}

	// The backups made, and only those, are copied once.
	if err := m.copyNew(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(s3.objects) != 2 || string(s3.objects["/jec-backups/server-1/jec-20261020-020000.db"]) != "backup jec-20261020-020000.db" {
		t.Errorf("copied %v", s3.objects)
	}
	s3.objects = map[string][]byte{}
	if err := m.copyNew(t.Context()); err != nil || len(s3.objects) != 0 {
		t.Errorf("copied again: %v %v", err, s3.objects)
	}
	resp = a.Get("/settings")
	if json.Unmarshal(resp.Body.Bytes(), &out) != nil || out.Copied != 2 || out.LastCopy == nil || out.LastError != "" {
		t.Errorf("status %s", resp.Body)
	}
	// A key left out of a save keeps the one stored.
	delete(settings, "secretKey")
	if resp := a.Put("/settings", settings); resp.Code != http.StatusOK {
		t.Errorf("saved without the key: %d %s", resp.Code, resp.Body)
	}
}

func TestObjectURL(t *testing.T) {
	for _, c := range []struct {
		endpoint  string
		pathStyle bool
		want      string
	}{
		{"https://s3.us-east-1.amazonaws.com", false, "https://jec.s3.us-east-1.amazonaws.com/a/jec%20b.db"},
		{"http://127.0.0.1:9000/", true, "http://127.0.0.1:9000/jec/a/jec%20b.db"},
	} {
		b := &bucket{endpoint: c.endpoint, name: "jec", pathStyle: c.pathStyle}
		if got, err := b.objectURL("a/jec b.db"); err != nil || got != c.want {
			t.Errorf("objectURL on %s = %s %v, want %s", c.endpoint, got, err, c.want)
		}
	}
}

// fakeDAV keeps what is put, by path, checking the account of each request
// and that a file's folders are made first.
type fakeDAV struct {
	t       *testing.T
	mu      sync.Mutex
	folders map[string]bool
	files   map[string][]byte
}

func (f *fakeDAV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if user, pass, ok := r.BasicAuth(); !ok || user != "me" || pass != "dav-pass" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	parent := r.URL.Path[:strings.LastIndex(strings.TrimSuffix(r.URL.Path, "/"), "/")+1]
	switch r.Method {
	case "MKCOL":
		if f.folders[r.URL.Path] {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		f.folders[r.URL.Path] = true
		w.WriteHeader(http.StatusCreated)
	case http.MethodPut:
		if !f.folders[parent] {
			w.WriteHeader(http.StatusConflict) // as WebDAV answers a PUT into no folder
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.files[r.URL.Path] = body
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		delete(f.files, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}
}

func TestWebDAV(t *testing.T) {
	dav := &fakeDAV{t: t, folders: map[string]bool{"/dav/": true}, files: map[string][]byte{}}
	srv := httptest.NewServer(dav)
	defer srv.Close()
	f := &folder{client: srv.Client(), endpoint: srv.URL + "/dav/", user: "me", password: "dav-pass"}
	if err := f.put(t.Context(), "server-1/jec-20261020-020000.db", []byte("backup")); err != nil {
		t.Fatal(err)
	}
	if string(dav.files["/dav/server-1/jec-20261020-020000.db"]) != "backup" || !dav.folders["/dav/server-1/"] {
		t.Errorf("put %v in %v", dav.files, dav.folders)
	}
	// Into a folder that is there already.
	if err := f.put(t.Context(), "server-1/jec-20261021-020000.db", []byte("backup 2")); err != nil {
		t.Fatal(err)
	}
	if err := f.remove(t.Context(), "server-1/jec-20261020-020000.db"); err != nil || len(dav.files) != 1 {
		t.Errorf("remove: %v %v", err, dav.files)
	}
	wrong := *f
	wrong.password = "wrong"
	if err := wrong.put(t.Context(), "x", []byte("x")); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("a wrong password: %v", err)
	}
	if _, err := (&folder{endpoint: "ftp://x"}).fileURL("a"); err == nil {
		t.Error("an address of no WebDAV")
	}
}
