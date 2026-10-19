package cloudflare

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

const zoneOne = "0123456789abcdef0123456789abcdef"

// fakeAPI is Cloudflare's API with one zone, example.com, and its custom
// certificates.
type fakeAPI struct {
	mu    sync.Mutex
	certs map[string]custom
	pems  map[string]string // the chain each custom certificate got
	next  int
	calls []string
	fail  int // answers every request with this status, unless 0
}

func (f *fakeAPI) answer(w http.ResponseWriter, status int, result any, info map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{"success": status < 300, "errors": []any{}, "result": result}
	if status >= 300 {
		body["errors"] = []map[string]any{{"code": 1000 + status, "message": "refused"}}
	}
	if info != nil {
		body["result_info"] = info
	}
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer t0ken" || f.fail != 0 {
		status := http.StatusForbidden
		if f.fail != 0 {
			status = f.fail
		}
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "30")
		}
		f.answer(w, status, nil, nil)
		return
	}
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/zones" && r.URL.Query().Get("name") == "example.com":
		f.answer(w, 200, []map[string]string{{"id": zoneOne}}, nil)
	case r.Method == http.MethodGet && path == "/zones":
		f.answer(w, 200, []any{}, nil)
	case r.Method == http.MethodGet && path == "/zones/"+zoneOne:
		f.answer(w, 200, map[string]string{"id": zoneOne, "name": "example.com"}, nil)
	case path == "/zones/"+zoneOne+"/custom_certificates":
		if r.Method == http.MethodGet {
			list := []custom{}
			for _, id := range slices.Sorted(func(yield func(string) bool) {
				for id := range f.certs {
					if !yield(id) {
						return
					}
				}
			}) {
				list = append(list, f.certs[id])
			}
			f.answer(w, 200, list, map[string]any{"total_count": len(list)})
			return
		}
		var body upload
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Type != "sni_custom" {
			f.answer(w, 400, nil, nil)
			return
		}
		f.next++
		id := fmt.Sprintf("custom-%d", f.next)
		f.store(id, body)
		f.answer(w, 200, f.certs[id], nil)
	case strings.HasPrefix(path, "/zones/"+zoneOne+"/custom_certificates/") && r.Method == http.MethodPatch:
		id := strings.TrimPrefix(path, "/zones/"+zoneOne+"/custom_certificates/")
		if _, ok := f.certs[id]; !ok {
			f.answer(w, 404, nil, nil)
			return
		}
		var body upload
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.store(id, body)
		f.answer(w, 200, f.certs[id], nil)
	default:
		f.answer(w, 404, nil, nil)
	}
}

// store keeps a custom certificate, as Cloudflare reads it.
func (f *fakeAPI) store(id string, body upload) {
	b, err := cryptox.ParseBundle([]byte(body.Certificate), []byte(body.PrivateKey))
	if err != nil || body.BundleMethod != "ubiquitous" {
		panic(fmt.Sprintf("a bad upload: %v %q", err, body.BundleMethod))
	}
	f.certs[id] = custom{ID: id, Hosts: b.Leaf.DNSNames, ExpiresOn: b.Leaf.NotAfter.UTC().Format(time.RFC3339)}
	f.pems[id] = body.Certificate
}

func open(t *testing.T, f *fakeAPI, config map[string]string) *zone {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	p, err := factory(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"api_token": " t0ken "}})
	if err != nil {
		t.Fatal(err)
	}
	z := p.(*zone)
	z.client, z.base = srv.Client(), srv.URL
	return z
}

func newFake() *fakeAPI { return &fakeAPI{certs: map[string]custom{}, pems: map[string]string{}} }

func request(t *testing.T, names ...string) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour), names...)
	return contracts.DeployRequest{CertificateID: 3, Domains: names, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key}
}

func TestDeploy(t *testing.T) {
	f := newFake()
	z := open(t, f, map[string]string{"zone": "Example.com."})
	first := request(t, "example.com", "*.example.com")
	res, err := z.Deploy(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "custom-1" || len(f.certs) != 1 || strings.Count(f.pems["custom-1"], "BEGIN CERTIFICATE") != 2 {
		t.Fatalf("result %+v, certificates %v", res, f.certs)
	}
	// The next version replaces it in place.
	next := request(t, "example.com", "*.example.com")
	next.Previous = []string{res.Ref}
	res, err = z.Deploy(t.Context(), next)
	if err != nil || res.Ref != "custom-1" || len(res.Removed) != 0 || len(f.certs) != 1 {
		t.Fatalf("again: %+v, %v, %v", res, err, f.certs)
	}
	if !strings.Contains(f.pems["custom-1"], string(cryptox.EncodeCertificates(next.Chain[0]))) {
		t.Error("the custom certificate kept the old certificate")
	}
	msg, err := z.Test(t.Context())
	if err != nil || msg.Key != i18n.CloudflareTestOk || msg.Params["zone"] != "example.com" || msg.Params["count"] != 1 {
		t.Errorf("test: %+v, %v", msg, err)
	}
}

func TestDeployAgain(t *testing.T) {
	f := newFake()
	z := open(t, f, map[string]string{"zone": zoneOne})
	req := request(t, "example.com")
	if _, err := z.Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	// As after a stop before JEC kept the ref: the one added is found.
	if res, err := z.Deploy(t.Context(), req); err != nil || res.Ref != "custom-1" || len(f.certs) != 1 {
		t.Errorf("again: %+v, %v, %v", res, err, f.certs)
	}
	// The one an earlier version went to is gone: a new one comes.
	delete(f.certs, "custom-1")
	next := request(t, "example.com")
	next.Previous = []string{"custom-1"}
	res, err := z.Deploy(t.Context(), next)
	if err != nil || res.Ref != "custom-2" || !slices.Equal(res.Removed, []string{"custom-1"}) {
		t.Errorf("gone: %+v, %v", res, err)
	}
}

func TestDeploySet(t *testing.T) {
	f := newFake()
	f.certs["theirs"] = custom{ID: "theirs"}
	z := open(t, f, map[string]string{"zone": zoneOne, "certificate_id": "theirs"})
	if res, err := z.Deploy(t.Context(), request(t, "example.com")); err != nil || res.Ref != "theirs" || f.pems["theirs"] == "" {
		t.Errorf("the one set: %+v, %v", res, err)
	}
	// Gone, it is not replaced by another.
	delete(f.certs, "theirs")
	var p *contracts.Problem
	if _, err := z.Deploy(t.Context(), request(t, "example.com")); !errors.As(err, &p) || p.Key != i18n.CloudflareErrorApi || !retry.IsPermanent(err) {
		t.Errorf("the one set, gone: %v", err)
	}
}

func TestErrors(t *testing.T) {
	var p *contracts.Problem
	f := newFake()
	z := open(t, f, map[string]string{"zone": "other.example"})
	if _, err := z.Deploy(t.Context(), request(t, "example.com")); !errors.As(err, &p) || p.Key != i18n.CloudflareErrorZone || !retry.IsPermanent(err) {
		t.Errorf("a zone the token does not see: %v", err)
	}
	z = open(t, f, map[string]string{"zone": zoneOne})
	z.token = "wrong"
	if _, err := z.Test(t.Context()); !errors.As(err, &p) || p.Key != i18n.CloudflareErrorAuth || !retry.IsPermanent(err) {
		t.Errorf("a wrong token: %v", err)
	}
	z.token = "t0ken"
	f.fail = http.StatusTooManyRequests
	_, err := z.Deploy(t.Context(), request(t, "example.com"))
	if d, ok := retry.Delay(err); !errors.As(err, &p) || p.Key != i18n.CloudflareErrorThrottled || retry.IsPermanent(err) || !ok || d != 30*time.Second {
		t.Errorf("too many requests: %v", err)
	}
	f.fail = http.StatusBadGateway
	if _, err := z.Deploy(t.Context(), request(t, "example.com")); !errors.As(err, &p) || p.Key != i18n.CloudflareErrorApi || retry.IsPermanent(err) {
		t.Errorf("a bad gateway: %v", err)
	}
	// Nobody listening.
	srv := httptest.NewServer(http.NotFoundHandler())
	z.base = srv.URL
	srv.Close()
	if _, err := z.Test(t.Context()); !errors.As(err, &p) || p.Key != i18n.CloudflareErrorNetwork || retry.IsPermanent(err) {
		t.Errorf("nobody listening: %v", err)
	}
}

// The API's errors come with a code and a message.
func TestAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"success":false,"errors":[{"code":1228,"message":"You must have a Business plan"}],"result":null}`)
	}))
	defer srv.Close()
	z := &zone{rest: rest{client: srv.Client(), base: srv.URL, token: "t"}, zone: zoneOne}
	_, err := z.call(t.Context(), http.MethodGet, "/x", nil, nil)
	var p *contracts.Problem
	if e := explain(err); !errors.As(e, &p) || p.Params["code"] != 1228 || p.Params["message"] != "You must have a Business plan" || !retry.IsPermanent(e) {
		t.Errorf("explained %v", e)
	}
}
