package namecheap

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

const (
	userPath   = "/api/v1/externalautossl/user"
	vendorPath = "/api/v1/externalautossl/vendor"
	oneTime    = "one-time-1"
	challenge1 = "/.well-known/pki-validation/ABC123.txt"
)

// vendor is Namecheap's API as the official client shows it, with its
// certificates in memory: it registers a one-time token once, refreshes
// access tokens, takes CSRs, hands out an HTTP challenge each, or a DNS
// one, and issues from a test CA when the test says so.
type vendor struct {
	t  *testing.T
	ca *cryptoxtest.CA

	mu          sync.Mutex
	oneTime     string // the token create accepts, once
	access      string // the token every other call needs
	tokens      int    // access tokens handed out
	refreshFail int    // a status for the refresh to answer, if not 0
	retryAfter  string // with a 429
	certs       []*vendorCert
	fingerprint int
	calls       []string // the paths called, without the API prefix
	meta        bool     // updateMeta was called
	confirmed   []string // installation IDs
	// dns, if set, is the record of the DNS challenge each certificate
	// has in place of the file of an HTTP one.
	dns *dnsRecord
}

type vendorCert struct {
	id, domain, state string
	csr               *x509.CertificateRequest
	leaf              *x509.Certificate
	marked            bool
}

func newVendor(t *testing.T) (*vendor, *httptest.Server) {
	t.Helper()
	v := &vendor{t: t, ca: cryptoxtest.NewCA(t), oneTime: oneTime}
	srv := httptest.NewTLSServer(http.HandlerFunc(v.serve))
	t.Cleanup(srv.Close)
	return v, srv
}

// add makes a certificate for domain, the newest; leaf, if not nil, is
// issued already.
func (v *vendor) add(id, domain, state string, leaf *x509.Certificate) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.certs = append([]*vendorCert{{id: id, domain: domain, state: state, leaf: leaf}}, v.certs...)
	v.fingerprint++
}

// issue signs certificate id for the key of its CSR, as the CA would once
// the validation passed.
func (v *vendor) issue(id string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	c := v.find(id)
	c.leaf = v.ca.Leaf(v.t, publicKey{c.csr.PublicKey}, time.Now().Add(-time.Hour), time.Now().AddDate(0, 0, 200), c.domain, "www."+c.domain)
	c.state = stateIssued
	v.fingerprint++
}

func (v *vendor) find(id string) *vendorCert {
	for _, c := range v.certs {
		if c.id == id {
			return c
		}
	}
	return nil
}

// with runs fn with the vendor locked, for a test to look at or change it
// between calls.
func (v *vendor) with(fn func()) {
	v.mu.Lock()
	defer v.mu.Unlock()
	fn()
}

func providersInstance(url string) providers.Instance {
	return providers.Instance{ID: 1, Config: map[string]string{"url": url}}
}

func (v *vendor) called() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.calls...)
}

// publicKey signs nothing: it lends a CSR's key to the test CA, which
// needs only its public half.
type publicKey struct{ pub crypto.PublicKey }

func (k publicKey) Public() crypto.PublicKey { return k.pub }
func (publicKey) Sign(io.Reader, []byte, crypto.SignerOpts) ([]byte, error) {
	panic("a public key signs nothing")
}

func (v *vendor) serve(w http.ResponseWriter, r *http.Request) {
	v.mu.Lock()
	defer v.mu.Unlock()
	path := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, userPath), vendorPath)
	v.calls = append(v.calls, path)
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("User-Agent"), "sslManager/") {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	if path == "/integrations/create" {
		if token == "" || token != v.oneTime || body["tool"] != tool {
			http.Error(w, `{"message":"invalid token"}`, http.StatusUnauthorized)
			return
		}
		v.oneTime = "" // used up
		v.tokens++
		v.access = "access-" + strconv.Itoa(v.tokens)
		writeJSON(w, map[string]string{"accessToken": v.access, "integrationHash": "hash-1"})
		return
	}
	if token != v.access {
		http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	switch path {
	case "/integration-token/refresh":
		if v.refreshFail != 0 {
			w.Header().Set("Retry-After", v.retryAfter)
			http.Error(w, "no", v.refreshFail)
			return
		}
		v.tokens++
		v.access = "access-" + strconv.Itoa(v.tokens)
		now := time.Now()
		writeJSON(w, map[string]any{"accessToken": v.access, "issuedAt": now.Unix(), "expiresAt": now.AddDate(0, 0, 30).Unix()})
	case "/integrations/hash":
		writeJSON(w, map[string]string{"integrationHash": "hash-1"})
	case "/integrations/updateMeta":
		v.meta = body["tool"] == tool
	case "/integrations/status":
		if r.URL.Query().Get("integrationHash") != "hash-1" {
			http.Error(w, "unknown integration", http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]string{"fingerprint": "fp-" + strconv.Itoa(v.fingerprint)})
	case "/certificates/list":
		domain := ""
		if f, ok := body["filters"].(map[string]any); ok {
			domain, _ = f["order.domains.fqdn"].(map[string]any)["$eq"].(string)
		}
		items := []map[string]any{}
		for _, c := range v.certs {
			if domain == "" || c.domain == domain {
				items = append(items, v.model(c))
			}
		}
		writeJSON(w, map[string]any{"items": items, "total": len(items)})
	case "/certificates/request":
		c := v.find(body["id"].(string))
		data, err := base64.StdEncoding.DecodeString(body["content"].(string))
		block, _ := pem.Decode(data)
		if err != nil || block == nil || c == nil || c.state != stateAwaitingCSR {
			http.Error(w, "bad csr", http.StatusUnprocessableEntity)
			return
		}
		if c.csr, err = x509.ParseCertificateRequest(block.Bytes); err != nil || c.csr.Subject.CommonName != c.domain {
			http.Error(w, "bad csr", http.StatusUnprocessableEntity)
			return
		}
		c.state = stateAwaitingChallenges
		v.fingerprint++
		writeJSON(w, v.model(c))
	case "/certificates/listChallenges":
		c := v.find(body["id"].(string))
		ch := map[string]any{
			"id": "ch-" + c.id, "domain": c.domain, "method": methodHTTP, "state": "Pending",
			"meta": map[string]string{"filePath": challenge1, "content": "abc123\ncomodoca.com"},
		}
		if v.dns != nil {
			ch["method"], ch["meta"] = methodDNS, v.dns
		}
		writeJSON(w, map[string]any{"total": 1, "items": []map[string]any{ch}})
	case "/certificates/markChallengeInstalled":
		c := v.find(strings.TrimPrefix(body["id"].(string), "ch-"))
		c.marked = true
		c.state = "Pending Validation"
		v.fingerprint++
	case "/certificates/readPEMFormatted":
		c := v.find(body["id"].(string))
		writeJSON(w, map[string]string{"content": base64.StdEncoding.EncodeToString([]byte(cryptoxtest.CertPEM(v.ca.Intermediate, c.leaf)))})
	case "/certificates/confirmInstallation":
		v.confirmed = append(v.confirmed, body["installationId"].(string))
	default:
		http.Error(w, "no such call", http.StatusNotFound)
	}
}

func (v *vendor) model(c *vendorCert) map[string]any {
	m := map[string]any{"id": c.id, "domains": []string{c.domain}, "state": c.state, "expires": nil, "modulusSha1": nil}
	if c.leaf != nil {
		if pub, ok := c.leaf.PublicKey.(*rsa.PublicKey); ok {
			m["modulusSha1"] = strings.ToUpper(modulusSHA1(pub))
		}
		m["serialNumber"] = c.leaf.SerialNumber.Text(16)
	}
	return m
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// website is a solver over HTTP and the web server behind it: what it
// puts, the module's fetches see.
type website struct {
	mu      sync.Mutex
	files   map[string][]byte // by domain and path
	removed []string
}

func (s *website) PutFile(_ context.Context, f contracts.File) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !f.Valid() {
		return &contracts.Problem{Key: "test.bad_path"}
	}
	s.files[f.Domain+f.Path] = f.Content
	return nil
}

func (s *website) RemoveFile(_ context.Context, f contracts.File) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, f.Domain+f.Path)
	s.removed = append(s.removed, f.Domain+f.Path)
	return nil
}

func (*website) Close() error { return nil }

func (s *website) file(name string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.files[name]
	return data, ok
}

// RoundTrip serves the files to the module's fetches.
func (s *website) RoundTrip(r *http.Request) (*http.Response, error) {
	data, ok := s.file(r.URL.Host + r.URL.Path)
	status := http.StatusOK
	if !ok {
		status, data = http.StatusNotFound, []byte("not found")
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}, Request: r}, nil
}

// zone is a solver of DNS records and the authoritative servers behind it:
// what it adds, the module's lookups see, unless they lag.
type zone struct {
	mu      sync.Mutex
	records map[contracts.Record]string // the refs, by record
	added   int
	removed []string
	lag     bool  // the servers do not answer the records yet
	manual  bool  // people add the records, so there are no refs
	fail    error // what AddRecord answers, if not nil
}

func newZone() *zone { return &zone{records: map[contracts.Record]string{}} }

func (z *zone) AddRecord(_ context.Context, r contracts.Record) (string, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.fail != nil {
		return "", z.fail
	}
	z.added++
	if z.manual {
		return "", nil
	}
	ref := "rec-" + strconv.Itoa(z.added)
	z.records[r] = ref
	return ref, nil
}

func (z *zone) RemoveRecord(_ context.Context, r contracts.Record, ref string) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.records[r] != ref {
		return errors.New("no such record")
	}
	delete(z.records, r)
	z.removed = append(z.removed, ref)
	return nil
}

func (*zone) Close() error { return nil }

// visible stands for the lookup on the authoritative servers.
func (z *zone) visible(_ context.Context, name, typ, value string) (bool, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	_, ok := z.records[contracts.Record{Name: name, Type: typ, Value: value}]
	return !z.lag && (ok || z.manual), nil
}

func (z *zone) with(fn func()) {
	z.mu.Lock()
	defer z.mu.Unlock()
	fn()
}

// fixture is the module against a vendor, with a registry holding one
// instance of the SSL Manager type.
type fixture struct {
	t       *testing.T
	m       *Module
	vendor  *vendor
	site    *website
	reg     *providers.Registry
	id      int64
	now     time.Time
	logged  []contracts.LogLine
	logMu   sync.Mutex
	srvBase string
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
	m := New()
	if _, err := st.Migrate(t.Context(), "namecheap", m.Migrations()); err != nil {
		t.Fatal(err)
	}
	box, _, err := secrets.Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	v, srv := newVendor(t)
	f := &fixture{t: t, m: m, vendor: v, site: &website{files: map[string][]byte{}}, now: time.Now(), srvBase: srv.URL + userPath}
	api := srv.Client()
	api.CheckRedirect = m.api.CheckRedirect
	m.api, m.web = api, &http.Client{Transport: f.site}
	m.now = func() time.Time { return f.now }
	m.db, m.secrets, m.log = st.DB(), box.Scope("namecheap"), slog.New(slog.DiscardHandler)
	f.reg = providers.New(st.DB(), box.Scope("kernel"), func(string) bool { return true })
	m.providers = f.reg.Scope("namecheap")
	if err := m.providers.Add(sslManagerType(), m.factory); err != nil {
		t.Fatal(err)
	}
	f.reg.Seal()
	f.id, err = f.reg.Create(t.Context(), "namecheap.ssl_manager", "Namecheap", map[string]string{"url": f.srvBase}, map[string]string{"token": oneTime})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// source opens the instance.
func (f *fixture) source() *sslManager {
	f.t.Helper()
	p, _, err := f.m.providers.Open(f.t.Context(), f.id)
	if err != nil {
		f.t.Fatal(err)
	}
	return p.(*sslManager)
}

func (f *fixture) log(l contracts.LogLine) {
	f.logMu.Lock()
	defer f.logMu.Unlock()
	f.logged = append(f.logged, l)
}

// issue runs an attempt with key and the state of the last one, over
// HTTP.
func (f *fixture) issue(key crypto.Signer, state []byte) ([]*x509.Certificate, *contracts.Wait, error) {
	f.t.Helper()
	return f.attempt(contracts.IssueRequest{Key: key, Files: f.site, State: state})
}

// attempt runs an attempt of req for example.com.
func (f *fixture) attempt(req contracts.IssueRequest) ([]*x509.Certificate, *contracts.Wait, error) {
	f.t.Helper()
	req.Domains, req.Log = []string{"example.com"}, f.log
	chain, err := f.source().Issue(f.t.Context(), req)
	var w *contracts.Wait
	if errors.As(err, &w) {
		return nil, w, nil
	}
	return chain, nil, err
}

var rsaKeys = sync.OnceValue(func() []*rsa.PrivateKey {
	var keys []*rsa.PrivateKey
	for range 2 {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		keys = append(keys, k)
	}
	return keys
})
