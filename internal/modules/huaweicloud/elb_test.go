package huaweicloud

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

// fakeELB is Huawei Cloud's ELB in a project, with a listener using
// certificate c-theirs at first.
type fakeELB struct {
	t         *testing.T
	mu        sync.Mutex
	certs     map[string]map[string]any // by ID
	listeners map[string]map[string]any
	calls     []string
	next      int
}

const elbPrefix = "/v3/p-1/elb/"

func (f *fakeELB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	rest, ok := strings.CutPrefix(r.URL.Path, elbPrefix)
	if !ok {
		f.t.Errorf("%s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	notFound := func() {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error_code": "ELB.8902", "error_msg": "not found", "request_id": "r"}`)
	}
	var body map[string]map[string]any
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	switch kind, id, _ := strings.Cut(rest, "/"); {
	case kind == "certificates" && id == "" && r.Method == http.MethodGet:
		list := []map[string]any{}
		for _, c := range f.certs {
			if name := r.URL.Query().Get("name"); name == "" || c["name"] == name {
				list = append(list, c)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"certificates": list})
	case kind == "certificates" && id == "" && r.Method == http.MethodPost:
		c := body["certificate"]
		if c["type"] != "server" || !strings.Contains(c["certificate"].(string), "BEGIN CERTIFICATE") || !strings.Contains(c["private_key"].(string), "PRIVATE KEY") {
			f.t.Errorf("created %v", c)
		}
		f.next++
		c["id"] = "c-" + strconv.Itoa(f.next+1)
		f.certs[c["id"].(string)] = c
		f.calls = append(f.calls, "create "+c["name"].(string))
		_ = json.NewEncoder(w).Encode(map[string]any{"certificate": c})
	case kind == "certificates":
		c := f.certs[id]
		if c == nil {
			notFound()
			return
		}
		if r.Method == http.MethodPut {
			c["certificate"], c["private_key"] = body["certificate"]["certificate"], body["certificate"]["private_key"]
			f.calls = append(f.calls, "update "+id)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"certificate": c})
	case kind == "listeners":
		l := f.listeners[id]
		if l == nil {
			notFound()
			return
		}
		if r.Method == http.MethodPut {
			l["default_tls_container_ref"] = body["listener"]["default_tls_container_ref"]
			f.calls = append(f.calls, "listener "+id+" "+l["default_tls_container_ref"].(string))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"listener": l})
	default:
		notFound()
	}
}

func openELB(t *testing.T, f *fakeELB, config map[string]string) *elb {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfg := map[string]string{"region": "cn-north-4", "project_id": "p-1"}
	for k, v := range config {
		cfg[k] = v
	}
	p, err := (&Module{endpoint: srv.URL}).elbFactory(t.Context(), providers.Instance{Config: cfg,
		Secrets: map[string]string{"access_key_id": "AK1", "secret_access_key": "s3cret"}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*elb)
}

func TestELB(t *testing.T) {
	f := &fakeELB{t: t,
		certs:     map[string]map[string]any{"c-theirs": {"id": "c-theirs", "name": "theirs", "type": "server"}},
		listeners: map[string]map[string]any{"l-1": {"id": "l-1", "name": "web", "protocol": "TERMINATED_HTTPS", "default_tls_container_ref": "c-theirs"}},
	}
	e := openELB(t, f, map[string]string{"listeners": "l-1"})
	msg, err := e.Test(t.Context())
	if err != nil || msg.Params["listeners"] != "web (TERMINATED_HTTPS)" {
		t.Fatalf("test %+v: %v", msg, err)
	}

	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	now := time.Now()
	req := contracts.DeployRequest{CertificateID: 4, Key: key, Chain: []*x509.Certificate{ca.Leaf(t, key, now, now.Add(90*24*time.Hour), "example.com"), ca.Intermediate}}

	// The first deployment creates JEC's certificate and has the listener
	// serve it; the next updates it in place.
	res, err := e.Deploy(t.Context(), req)
	if err != nil || res.Ref != "c-2" {
		t.Fatalf("first %+v: %v", res, err)
	}
	req.Previous = []string{res.Ref}
	if res, err := e.Deploy(t.Context(), req); err != nil || res.Ref != "c-2" {
		t.Fatalf("second %+v: %v", res, err)
	}
	if want := []string{"create jec-4", "listener l-1 c-2", "update c-2"}; !slices.Equal(f.calls, want) {
		t.Errorf("calls %v", f.calls)
	}

	// A certificate set is updated, whatever the last deployment left.
	f.calls = nil
	if res, err := openELB(t, f, map[string]string{"certificate_id": "c-theirs"}).Deploy(t.Context(), req); err != nil || res.Ref != "c-theirs" {
		t.Fatalf("set %+v: %v", res, err)
	}
	// One deleted since is found by its name, as after an attempt that
	// failed once it had created it, or made anew.
	delete(f.certs, "c-2")
	req.Previous = []string{"c-2"}
	if res, err := e.Deploy(t.Context(), req); err != nil || res.Ref != "c-3" {
		t.Fatalf("deleted %+v: %v", res, err)
	}
	if want := []string{"update c-theirs", "create jec-4", "listener l-1 c-3"}; !slices.Equal(f.calls, want) {
		t.Errorf("calls %v", f.calls)
	}
	req.Previous = nil
	if res, err := e.Deploy(t.Context(), req); err != nil || res.Ref != "c-3" {
		t.Fatalf("found %+v: %v", res, err)
	}

	if _, err := openELB(t, f, map[string]string{"listeners": "l-gone"}).Test(t.Context()); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("a listener not there: %v", err)
	}
	if _, err := (&Module{}).elbFactory(t.Context(), providers.Instance{Config: map[string]string{"region": "mars-1", "project_id": "p"},
		Secrets: map[string]string{"access_key_id": "AK1", "secret_access_key": "s3cret"}}); err == nil {
		t.Error("took an unknown region")
	}
}
