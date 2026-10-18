package kubernetes

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

const token = "the-token"

// fakeServer answers as a Kubernetes API server does, for what JEC uses,
// for the namespace jec and the token it knows.
type fakeServer struct {
	t       *testing.T
	mu      sync.Mutex
	token   string
	denied  map[string]bool // verbs the token may not use
	secrets map[string]map[string]any
	// race has another client create the Secret between JEC's get and
	// create.
	race     bool
	requests []string
}

func (f *fakeServer) status(w http.ResponseWriter, code int, reason, message string) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "reason": reason, "message": message, "code": code})
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		f.status(w, 401, "Unauthorized", "Unauthorized")
		return
	}
	body, _ := io.ReadAll(r.Body)
	const secrets = "/api/v1/namespaces/jec/secrets"
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews":
		var review struct {
			Spec struct {
				ResourceAttributes map[string]string `json:"resourceAttributes"`
			} `json:"spec"`
		}
		_ = json.Unmarshal(body, &review)
		attrs := review.Spec.ResourceAttributes
		if attrs["namespace"] != "jec" || attrs["resource"] != "secrets" || (attrs["verb"] == "create") != (attrs["name"] == "") {
			f.t.Errorf("review %v", attrs)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"kind": "SelfSubjectAccessReview", "status": map[string]any{"allowed": !f.denied[attrs["verb"]]}})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, secrets+"/"):
		name := strings.TrimPrefix(r.URL.Path, secrets+"/")
		if s := f.secrets[name]; s != nil {
			_ = json.NewEncoder(w).Encode(s)
			return
		}
		f.status(w, 404, "NotFound", fmt.Sprintf("secrets %q not found", name))
	case r.Method == http.MethodPost && r.URL.Path == secrets:
		if r.URL.Query().Get("fieldManager") != "jec" || r.Header.Get("Content-Type") != "application/json" {
			f.t.Errorf("create %v %s", r.URL.Query(), r.Header.Get("Content-Type"))
		}
		var s map[string]any
		_ = json.Unmarshal(body, &s)
		name := s["metadata"].(map[string]any)["name"].(string)
		if f.race {
			f.secrets[name] = map[string]any{"type": "kubernetes.io/tls", "data": map[string]any{"other": "eA=="}}
		}
		if f.secrets[name] != nil {
			f.status(w, 409, "AlreadyExists", fmt.Sprintf("secrets %q already exists", name))
			return
		}
		f.secrets[name] = s
		_ = json.NewEncoder(w).Encode(s)
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, secrets+"/"):
		if r.URL.Query().Get("fieldManager") != "jec" || r.Header.Get("Content-Type") != "application/merge-patch+json" {
			f.t.Errorf("patch %v %s", r.URL.Query(), r.Header.Get("Content-Type"))
		}
		s := f.secrets[strings.TrimPrefix(r.URL.Path, secrets+"/")]
		var patch struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(body, &patch)
		data, _ := s["data"].(map[string]any)
		if data == nil {
			data = map[string]any{}
		}
		for k, v := range patch.Data {
			data[k] = v
		}
		s["data"] = data
		_ = json.NewEncoder(w).Encode(s)
	default:
		f.status(w, 404, "NotFound", "the server could not find the requested resource")
	}
}

// serve starts f, and returns the settings of a provider for it.
func serve(t *testing.T, f *fakeServer) (*httptest.Server, map[string]string) {
	t.Helper()
	f.t = t
	if f.token == "" {
		f.token = token
	}
	if f.secrets == nil {
		f.secrets = map[string]map[string]any{}
	}
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	return srv, map[string]string{"url": srv.URL, "ca": base64.StdEncoding.EncodeToString(ca), "namespace": "jec", "secret": "example-tls", "allow_private": "true"}
}

func open(t *testing.T, config map[string]string) *cluster {
	t.Helper()
	p, err := factory(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"token": token}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*cluster)
}

// request makes a certificate for example.com, logging into lines.
func request(t *testing.T, lines *[]contracts.LogLine) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "example.com")
	return contracts.DeployRequest{CertificateID: 7, Domains: []string{"example.com"}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key,
		Log: func(l contracts.LogLine) { *lines = append(*lines, l) }}
}

// checkData checks that data holds the chain of req and its key.
func checkData(t *testing.T, data map[string]any, req contracts.DeployRequest) {
	t.Helper()
	crt, err1 := base64.StdEncoding.DecodeString(fmt.Sprint(data["tls.crt"]))
	key, err2 := base64.StdEncoding.DecodeString(fmt.Sprint(data["tls.key"]))
	b, err := cryptox.ParseBundle(crt, key)
	if err1 != nil || err2 != nil || err != nil || !b.Leaf.Equal(req.Chain[0]) || len(b.Chain) != 1 {
		t.Errorf("data %v: %v %v %v", data, err1, err2, err)
	}
}

func TestDeploy(t *testing.T) {
	f := &fakeServer{}
	_, config := serve(t, f)
	c := open(t, config)
	var lines []contracts.LogLine
	req := request(t, &lines)
	if res, err := c.Deploy(t.Context(), req); err != nil || res.Ref != "" {
		t.Fatalf("%+v %v", res, err)
	}
	s := f.secrets["example-tls"]
	if s["type"] != "kubernetes.io/tls" || fmt.Sprint(s["metadata"].(map[string]any)["labels"]) != "map[app.kubernetes.io/managed-by:jec]" {
		t.Errorf("created %v", s)
	}
	checkData(t, s["data"].(map[string]any), req)
	if len(lines) != 1 || lines[0].Key != i18n.KubernetesLogCreate || lines[0].Params["secret"] != "jec/example-tls" {
		t.Errorf("logged %+v", lines)
	}

	// The next version patches the two keys, leaving the others.
	s["data"].(map[string]any)["ca.crt"] = "eA=="
	lines = nil
	next := request(t, &lines)
	if _, err := c.Deploy(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	data := f.secrets["example-tls"]["data"].(map[string]any)
	checkData(t, data, next)
	if data["ca.crt"] != "eA==" || len(lines) != 1 || lines[0].Key != i18n.KubernetesLogPatch {
		t.Errorf("patched %v, logged %+v", data, lines)
	}
}

func TestDeployRace(t *testing.T) {
	f := &fakeServer{race: true}
	_, config := serve(t, f)
	var lines []contracts.LogLine
	req := request(t, &lines)
	if _, err := open(t, config).Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	data := f.secrets["example-tls"]["data"].(map[string]any)
	checkData(t, data, req)
	if data["other"] != "eA==" {
		t.Errorf("data %v", data)
	}
}

func TestType(t *testing.T) {
	f := &fakeServer{secrets: map[string]map[string]any{"example-tls": {"type": "kubernetes.io/dockerconfigjson"}}}
	_, config := serve(t, f)
	var lines []contracts.LogLine
	_, err := open(t, config).Deploy(t.Context(), request(t, &lines))
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.KubernetesErrorType || pr.Params["type"] != "kubernetes.io/dockerconfigjson" || !retry.IsPermanent(err) {
		t.Errorf("deploy: %v", err)
	}
	_, err = open(t, config).Test(t.Context())
	if !errors.As(err, &pr) || pr.Key != i18n.KubernetesErrorType {
		t.Errorf("test: %v", err)
	}
	// An Opaque Secret with the two keys will do.
	f.secrets["example-tls"]["type"] = "Opaque"
	if _, err := open(t, config).Deploy(t.Context(), request(t, &lines)); err != nil {
		t.Errorf("opaque: %v", err)
	}
}

func TestTest(t *testing.T) {
	f := &fakeServer{}
	_, config := serve(t, f)
	msg, err := open(t, config).Test(t.Context())
	if err != nil || msg.Key != i18n.KubernetesTestNew || msg.Params["secret"] != "jec/example-tls" {
		t.Errorf("new: %+v %v", msg, err)
	}
	f.secrets["example-tls"] = map[string]any{"type": "kubernetes.io/tls"}
	msg, err = open(t, config).Test(t.Context())
	if err != nil || msg.Key != i18n.KubernetesTestFound {
		t.Errorf("found: %+v %v", msg, err)
	}
	f.denied = map[string]bool{"create": true, "patch": true}
	_, err = open(t, config).Test(t.Context())
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.KubernetesErrorVerbs || pr.Params["verbs"] != "create, patch" || !retry.IsPermanent(err) {
		t.Errorf("denied: %v", err)
	}
	for _, r := range f.requests {
		if strings.HasPrefix(r, "POST /api/") || strings.HasPrefix(r, "PATCH") {
			t.Errorf("the test changed something: %s", r)
		}
	}
}

func TestErrors(t *testing.T) {
	f := &fakeServer{token: "another"}
	srv, config := serve(t, f)
	var lines []contracts.LogLine
	_, err := open(t, config).Deploy(t.Context(), request(t, &lines))
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.KubernetesErrorToken || !retry.IsPermanent(err) {
		t.Errorf("token: %v", err)
	}

	// Without the CA, the server's certificate cannot be verified; with
	// another name, it is not valid for it.
	delete(config, "ca")
	_, err = open(t, config).Test(t.Context())
	if !errors.As(err, &pr) || pr.Key != i18n.KubernetesErrorUnverified || !retry.IsPermanent(err) {
		t.Errorf("no CA: %v", err)
	}
	_, config = serve(t, &fakeServer{})
	config["url"] = strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	_, err = open(t, config).Test(t.Context())
	if !errors.As(err, &pr) || pr.Key != i18n.KubernetesErrorHostname {
		t.Errorf("another name: %v", err)
	}
	config["allow_private"] = "false"
	_, err = open(t, config).Test(t.Context())
	if !errors.As(err, &pr) || pr.Key != i18n.KubernetesErrorPrivate {
		t.Errorf("private: %v", err)
	}
}

func TestSettings(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	for _, in := range []providers.Instance{
		{Config: map[string]string{"namespace": "jec", "secret": "s"}},
		{Config: map[string]string{"url": "http://192.168.1.10:6443", "namespace": "jec", "secret": "s"}, Secrets: map[string]string{"token": token}},
		{Config: map[string]string{"url": "https://192.168.1.10:6443", "namespace": "jec", "secret": "s"}},
		{Config: map[string]string{"url": "https://192.168.1.10:6443", "ca": "not a certificate", "namespace": "jec", "secret": "s"}, Secrets: map[string]string{"token": token}},
	} {
		if _, err := factory(t.Context(), in); err == nil || !retry.IsPermanent(err) {
			t.Errorf("%v: %v", in.Config, err)
		}
	}
}

// TestInCluster runs as in a pod: the address comes from the environment,
// and the token and CA from the files of the service account, the token
// read anew each time.
func TestInCluster(t *testing.T) {
	f := &fakeServer{}
	srv, config := serve(t, f)
	dir := t.TempDir()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), ca, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	env := map[string]string{"KUBERNETES_SERVICE_HOST": host, "KUBERNETES_SERVICE_PORT": port}
	getenv, accountDir = func(k string) string { return env[k] }, dir
	t.Cleanup(func() { getenv, accountDir = os.Getenv, "/var/run/secrets/kubernetes.io/serviceaccount" })

	p, err := factory(t.Context(), providers.Instance{Config: map[string]string{"namespace": "jec", "secret": config["secret"]}})
	if err != nil {
		t.Fatal(err)
	}
	c := p.(*cluster)
	var lines []contracts.LogLine
	if _, err := c.Deploy(t.Context(), request(t, &lines)); err != nil {
		t.Fatal(err)
	}
	f.token = "renewed"
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte("renewed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Deploy(t.Context(), request(t, &lines)); err != nil {
		t.Errorf("renewed token: %v", err)
	}
}
