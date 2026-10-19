package gcore

import (
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// fakeAPI answers as Gcore's CDN API does, for an account with two CDN
// resources: 10 without HTTPS and 11 serving certificate 7.
type fakeAPI struct {
	t         *testing.T
	mu        sync.Mutex
	certs     map[int64]string // ID to name
	next      int64
	resources map[int64]*resource
	patches   []string
	deleted   []string
}

func newFake(t *testing.T) *fakeAPI {
	seven := int64(7)
	return &fakeAPI{t: t, certs: map[int64]string{7: "theirs"}, next: 100, resources: map[int64]*resource{
		10: {ID: 10, CNAME: "cdn.example.com"},
		11: {ID: 11, CNAME: "static.example.com", SSLEnabled: true, SSLData: &seven},
	}}
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "APIKey good-token" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message": "Invalid token"}`)
		return
	}
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch rest, ok := strings.CutPrefix(r.URL.Path, "/cdn/resources/"); {
	case ok:
		id, _ := strconv.ParseInt(rest, 10, 64)
		res := f.resources[id]
		if res == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPatch {
			body, _ := io.ReadAll(r.Body)
			f.patches = append(f.patches, rest+":"+string(body))
			var p struct {
				SSLEnabled bool  `json:"sslEnabled"`
				SSLData    int64 `json:"sslData"`
			}
			_ = json.Unmarshal(body, &p)
			res.SSLEnabled, res.SSLData = p.SSLEnabled, &p.SSLData
		}
		reply(res)
	case r.URL.Path == "/cdn/sslData" && r.Method == http.MethodPost:
		var p struct {
			Name string `json:"name"`
			Cert string `json:"sslCertificate"`
			Key  string `json:"sslPrivateKey"`
		}
		_ = json.NewDecoder(r.Body).Decode(&p)
		if !strings.Contains(p.Cert, "BEGIN CERTIFICATE") || !strings.Contains(p.Key, "PRIVATE KEY") {
			f.t.Errorf("uploaded %+v", p)
		}
		for _, name := range f.certs {
			if name == p.Name {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"errors": {"name": ["SSL certificate with this name already exists."]}}`)
				return
			}
		}
		f.next++
		f.certs[f.next] = p.Name
		w.WriteHeader(http.StatusCreated)
		reply(storedCert{ID: f.next, Name: p.Name})
	case r.URL.Path == "/cdn/sslData":
		var all []storedCert
		for id, name := range f.certs {
			all = append(all, storedCert{ID: id, Name: name})
		}
		reply(all)
	case strings.HasPrefix(r.URL.Path, "/cdn/sslData/") && r.Method == http.MethodDelete:
		id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/cdn/sslData/"), 10, 64)
		for _, res := range f.resources {
			if res.SSLData != nil && *res.SSLData == id {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"errors": {"sslData": ["The certificate is in use."]}}`)
				return
			}
		}
		if _, ok := f.certs[id]; !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(f.certs, id)
		f.deleted = append(f.deleted, strconv.FormatInt(id, 10))
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func open(t *testing.T, srv *httptest.Server, token, resources string) *cdn {
	t.Helper()
	p, err := factory(t.Context(), providers.Instance{Config: map[string]string{"resources": resources}, Secrets: map[string]string{"api_token": token}})
	if err != nil {
		t.Fatal(err)
	}
	c := p.(*cdn)
	c.base, c.client = srv.URL, srv.Client()
	return c
}

func TestCDN(t *testing.T) {
	f := newFake(t)
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := open(t, srv, "good-token", "10, 11 10")
	if !slices.Equal(c.resources, []int64{10, 11}) {
		t.Errorf("resources %v", c.resources)
	}
	msg, err := c.Test(t.Context())
	if err != nil || msg.Params["names"] != "cdn.example.com, static.example.com" {
		t.Fatalf("test %+v: %v", msg, err)
	}

	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	now := time.Now()
	req := contracts.DeployRequest{CertificateID: 3, Key: key, Chain: []*x509.Certificate{ca.Leaf(t, key, now, now.Add(90*24*time.Hour), "example.com", "*.example.com"), ca.Intermediate}}
	res, err := c.Deploy(t.Context(), req)
	if err != nil || res.Ref != "101" {
		t.Fatalf("first deployment %+v: %v", res, err)
	}
	if want := []string{`10:{"sslData":101,"sslEnabled":true}`, `11:{"sslData":101,"sslEnabled":true}`}; !slices.Equal(f.patches, want) {
		t.Errorf("patches %v", f.patches)
	}

	// Deploying the same version again, as after a failure further on,
	// finds it uploaded and served: nothing changes.
	if res, err := c.Deploy(t.Context(), req); err != nil || res.Ref != "101" || len(f.patches) != 2 {
		t.Fatalf("again %+v, patches %v: %v", res, f.patches, err)
	}

	// The next version replaces it on both, and the old one goes, but for
	// the one a resource JEC does not manage serves.
	f.resources[12] = &resource{ID: 12, CNAME: "other.example.com", SSLEnabled: true, SSLData: new(int64(50))}
	f.certs[50] = "jec-3-older"
	next := req
	next.Chain = []*x509.Certificate{ca.Leaf(t, key, now, now.Add(90*24*time.Hour), "example.com"), ca.Intermediate}
	next.Previous = []string{"101", "50", "999"}
	res, err = c.Deploy(t.Context(), next)
	if err != nil || res.Ref != "102" || len(f.patches) != 4 {
		t.Fatalf("next %+v, patches %v: %v", res, f.patches, err)
	}
	if !slices.Equal(res.Removed, []string{"101", "999"}) || !slices.Equal(f.deleted, []string{"101"}) {
		t.Errorf("removed %v, deleted %v", res.Removed, f.deleted)
	}

	// Refused tokens and unknown resources end the attempts.
	if _, err := open(t, srv, "bad", "10").Test(t.Context()); err == nil || !strings.Contains(err.Error(), "Invalid token") {
		t.Errorf("bad token: %v", err)
	}
	if _, err := open(t, srv, "good-token", "404").Test(t.Context()); err == nil {
		t.Error("found a resource not there")
	}
	for _, bad := range []string{"", "cdn.example.com", "-1"} {
		if _, err := factory(t.Context(), providers.Instance{Config: map[string]string{"resources": bad}}); err == nil {
			t.Errorf("took resources %q", bad)
		}
	}
}

func TestProblemOf(t *testing.T) {
	for raw, want := range map[string]string{
		`{"message": "Invalid token"}`:                                    "Invalid token",
		`{"errors": {"sslData": ["in use"], "name": ["taken", "again"]}}`: "name: taken again; sslData: in use",
		`{"errors": ["one", "two"]}`:                                      "one; two",
		`<html>`:                                                          "",
	} {
		if got := problemOf([]byte(raw)); got != want {
			t.Errorf("%s: %q", raw, got)
		}
	}
}
