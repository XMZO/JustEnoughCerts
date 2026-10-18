package docker

import (
	"archive/tar"
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
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

// fakeDocker stands in for the Docker Engine API, with one container,
// nginx, that has the directory /etc/nginx/certs.
type fakeDocker struct {
	t       *testing.T
	mu      sync.Mutex
	calls   []string
	files   map[string]*tar.Header
	content map[string][]byte
	running bool
	// fail answers the calls whose method and path it has instead.
	fail map[string]struct {
		status int
		body   string
	}
}

func newFake(t *testing.T) *fakeDocker {
	return &fakeDocker{t: t, files: map[string]*tar.Header{}, content: map[string][]byte{}, running: true, fail: map[string]struct {
		status int
		body   string
	}{}}
}

func (f *fakeDocker) failing(call string, status int, body string) {
	f.fail[call] = struct {
		status int
		body   string
	}{status, body}
}

func (f *fakeDocker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := r.Method + " " + r.URL.Path
	if q := r.URL.RawQuery; q != "" {
		call += "?" + q
	}
	f.calls = append(f.calls, call)
	if a, ok := f.fail[r.Method+" "+r.URL.Path]; ok {
		w.WriteHeader(a.status)
		_, _ = io.WriteString(w, a.body)
		return
	}
	name, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/containers/"), "/")
	if name != "nginx" {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"No such container: `+name+`"}`)
		return
	}
	switch r.Method + " " + rest {
	case "GET json":
		status := "exited"
		if f.running {
			status = "running"
		}
		_, _ = io.WriteString(w, `{"Id":"abc","Name":"/nginx","State":{"Status":"`+status+`","Running":`+map[bool]string{true: "true", false: "false"}[f.running]+`}}`)
	case "HEAD archive":
		mode := map[string]string{"/etc/nginx/certs": "2147484141", "/etc/nginx/nginx.conf": "420"}[r.URL.Query().Get("path")]
		if mode == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("X-Docker-Container-Path-Stat", base64.StdEncoding.EncodeToString([]byte(`{"name":"x","size":4096,"mode":`+mode+`,"mtime":"2026-10-18T08:00:00Z","linkTarget":""}`)))
	case "PUT archive":
		dir := r.URL.Query().Get("path")
		if dir != "/etc/nginx/certs" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"Could not find the file `+dir+` in container nginx"}`)
			return
		}
		if r.Header.Get("Content-Type") != "application/x-tar" {
			f.t.Errorf("content type %q", r.Header.Get("Content-Type"))
		}
		tr := tar.NewReader(r.Body)
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				f.t.Error(err)
				return
			}
			data, _ := io.ReadAll(tr)
			f.files[h.Name], f.content[h.Name] = h, data
		}
	case "POST kill":
		if !f.running {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"message":"Cannot kill container: nginx: Container abc is not running"}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "POST restart":
		f.running = true
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"page not found"}`)
	}
}

// serve starts the fake over TCP and returns the settings that reach it.
func serve(t *testing.T, f *fakeDocker, config map[string]string) map[string]string {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfg := map[string]string{"endpoint": "tcp://" + strings.TrimPrefix(srv.URL, "http://"), "container": "nginx", "directory": "/etc/nginx/certs/"}
	for k, v := range config {
		cfg[k] = v
	}
	return cfg
}

func open(t *testing.T, config map[string]string) *container {
	t.Helper()
	p, err := factory(t.Context(), providers.Instance{Config: config})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*container)
}

func request(t *testing.T, domains ...string) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), domains...)
	return contracts.DeployRequest{CertificateID: 1, Domains: domains, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key}
}

func TestDeploy(t *testing.T) {
	f := newFake(t)
	c := open(t, serve(t, f, map[string]string{"owner": "101:102"}))
	req := request(t, "*.example.com", "example.com")
	var logged []i18n.Key
	req.Log = func(l contracts.LogLine) { logged = append(logged, l.Key) }
	res, err := c.Deploy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "" || len(logged) != 2 || logged[0] != i18n.DockerLogCopy || logged[1] != i18n.DockerLogSignal {
		t.Errorf("result %+v, log %v", res, logged)
	}
	if len(f.calls) != 2 || f.calls[0] != "PUT /containers/nginx/archive?path=%2Fetc%2Fnginx%2Fcerts" || f.calls[1] != "POST /containers/nginx/kill?signal=HUP" {
		t.Errorf("calls %v", f.calls)
	}
	if d := f.files["star.example.com/"]; d == nil || d.Typeflag != tar.TypeDir || d.Uid != 101 || d.Gid != 102 {
		t.Errorf("directory %+v", d)
	}
	for name, mode := range map[string]int64{"cert.pem": 0o644, "chain.pem": 0o644, "fullchain.pem": 0o644, "privkey.pem": 0o600} {
		h := f.files["star.example.com/"+name]
		if h == nil || h.Mode != mode || h.Uid != 101 || h.Gid != 102 {
			t.Errorf("%s: %+v", name, h)
		}
	}
	b, err := cryptox.ParseBundle(f.content["star.example.com/fullchain.pem"], f.content["star.example.com/privkey.pem"])
	if err != nil || !b.Leaf.Equal(req.Chain[0]) || len(b.Chain) != 1 {
		t.Errorf("the files: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Error(err)
	}
}

func TestDeployFlatRestart(t *testing.T) {
	f := newFake(t)
	f.running = false
	c := open(t, serve(t, f, map[string]string{"layout": flat, "action": actionRestart}))
	if _, err := c.Deploy(t.Context(), request(t, "example.com")); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 || f.calls[1] != "POST /containers/nginx/restart" || f.files["privkey.pem"] == nil || f.files["privkey.pem"].Uid != 0 || !f.running {
		t.Errorf("calls %v, files %v", f.calls, f.files)
	}
}

func TestSignalOnly(t *testing.T) {
	f := newFake(t)
	c := open(t, serve(t, f, map[string]string{"directory": "", "signal": "USR1"}))
	if _, err := c.Deploy(t.Context(), request(t, "example.com")); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || f.calls[0] != "POST /containers/nginx/kill?signal=USR1" {
		t.Errorf("calls %v", f.calls)
	}
}

// A stopped container reads the files when it starts: there is nothing to
// signal.
func TestStopped(t *testing.T) {
	f := newFake(t)
	f.running = false
	c := open(t, serve(t, f, nil))
	req := request(t, "example.com")
	var levels []contracts.Level
	req.Log = func(l contracts.LogLine) { levels = append(levels, l.Level) }
	if _, err := c.Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if len(levels) != 3 || levels[2] != contracts.Warn {
		t.Errorf("log %v", levels)
	}
}

func TestDeployErrors(t *testing.T) {
	for _, c := range []struct {
		config    map[string]string
		fail      string
		status    int
		body      string
		key       i18n.Key
		permanent bool
	}{
		{map[string]string{"container": "web"}, "", 0, "", i18n.DockerErrorContainer, true},
		{map[string]string{"directory": "/etc/ssl/jec"}, "", 0, "", i18n.DockerErrorDirectory, true},
		{nil, "PUT /containers/nginx/archive", 403, `{"message":"container rootfs is marked read-only"}`, i18n.DockerErrorForbidden, true},
		{nil, "POST /containers/nginx/kill", 500, `{"message":"cannot kill container: permission denied"}`, i18n.DockerErrorApi, false},
		{nil, "POST /containers/nginx/kill", 400, `{"message":"Invalid signal: FOO"}`, i18n.DockerErrorApi, true},
		{nil, "PUT /containers/nginx/archive", 403, `<html>Forbidden</html>`, i18n.DockerErrorForbidden, true},
	} {
		f := newFake(t)
		if c.fail != "" {
			f.failing(c.fail, c.status, c.body)
		}
		_, err := open(t, serve(t, f, c.config)).Deploy(t.Context(), request(t, "example.com"))
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%v %s: %v", c.config, c.body, err)
		}
	}
}

func TestSettings(t *testing.T) {
	for _, endpoint := range []string{"/var/run/docker.sock", "unix://", "tcp://", "ftp://docker:21", "tcp://docker:2375/v1.44", "npipe:////./pipe/docker_engine"} {
		_, err := factory(t.Context(), providers.Instance{Config: map[string]string{"endpoint": endpoint, "container": "nginx"}})
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != i18n.DockerErrorEndpoint || !retry.IsPermanent(err) {
			t.Errorf("endpoint %q: %v", endpoint, err)
		}
	}
	if _, err := factory(t.Context(), providers.Instance{Config: map[string]string{"container": "nginx", "owner": "nginx"}}); err == nil {
		t.Error("an owner by name")
	}
	f := newFake(t)
	for _, c := range []struct {
		config map[string]string
		key    i18n.Key
	}{
		{map[string]string{"container": "../images"}, i18n.DockerErrorName},
		{map[string]string{"container": ""}, i18n.DockerErrorName},
		{map[string]string{"directory": "certs"}, i18n.DockerErrorRelative},
		{map[string]string{"signal": "HUP; rm"}, i18n.DockerErrorSignal},
		{map[string]string{"directory": "", "action": actionNone}, i18n.DockerErrorNothing},
	} {
		c1 := open(t, serve(t, f, c.config))
		_, err := c1.Deploy(t.Context(), request(t, "example.com"))
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || !retry.IsPermanent(err) {
			t.Errorf("%v: %v", c.config, err)
		}
		if _, err := c1.Test(t.Context()); !errors.As(err, &p) || p.Key != c.key {
			t.Errorf("test %v: %v", c.config, err)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("calls %v", f.calls)
	}
	// The socket that Docker listens on when none is set.
	if c := open(t, map[string]string{"container": "nginx"}); c.engine.socket != "/var/run/docker.sock" || c.action != actionSignal || c.signal != "HUP" {
		t.Errorf("defaults %+v", c)
	}
}

func TestTest(t *testing.T) {
	f := newFake(t)
	msg, err := open(t, serve(t, f, nil)).Test(t.Context())
	if err != nil || msg.Key != i18n.DockerTestOk || msg.Params["container"] != "nginx" {
		t.Errorf("a running container: %+v %v", msg, err)
	}
	f.running = false
	if msg, err := open(t, serve(t, f, nil)).Test(t.Context()); err != nil || msg.Key != i18n.DockerTestStopped || msg.Params["status"] != "exited" {
		t.Errorf("a stopped container: %+v %v", msg, err)
	}
	for _, dir := range []string{"/etc/ssl/jec", "/etc/nginx/nginx.conf"} {
		_, err := open(t, serve(t, f, map[string]string{"directory": dir})).Test(t.Context())
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != i18n.DockerErrorDirectory {
			t.Errorf("%s: %v", dir, err)
		}
	}
	for _, call := range f.calls {
		if !strings.HasPrefix(call, "GET ") && !strings.HasPrefix(call, "HEAD ") {
			t.Errorf("the test changed something: %s", call)
		}
	}
}

func TestSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Docker's socket is a Unix one")
	}
	sock := filepath.Join(t.TempDir(), "docker.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(newFake(t))
	srv.Listener = l
	srv.Start()
	defer srv.Close()
	if msg, err := open(t, map[string]string{"endpoint": "unix://" + sock, "container": "nginx"}).Test(t.Context()); err != nil || msg.Key != i18n.DockerTestOk {
		t.Errorf("over the socket: %+v %v", msg, err)
	}
	missing := filepath.Join(t.TempDir(), "docker.sock")
	_, err = open(t, map[string]string{"endpoint": "unix://" + missing, "container": "nginx"}).Test(t.Context())
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.DockerErrorSocket || !retry.IsPermanent(err) {
		t.Errorf("no socket: %v", err)
	}
}

func TestNetwork(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	_, err := open(t, map[string]string{"endpoint": "tcp://" + strings.TrimPrefix(srv.URL, "http://"), "container": "nginx"}).Test(t.Context())
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.DockerErrorNetwork || retry.IsPermanent(err) {
		t.Errorf("no Docker: %v", err)
	}
}

// The archive holds what certbot writes, and nothing else.
func TestArchive(t *testing.T) {
	c := &container{layout: flat, dir: "/certs"}
	archive, target, err := c.archive(request(t, "example.com"))
	if err != nil || target != "/certs" {
		t.Fatal(target, err)
	}
	var names []string
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, h.Name)
	}
	if strings.Join(names, " ") != "cert.pem chain.pem fullchain.pem privkey.pem" {
		t.Errorf("archive %v", names)
	}
}
