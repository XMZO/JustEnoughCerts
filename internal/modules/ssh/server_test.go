package ssh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

const password = "s3cret"

// sshServer accepts the client key and password, and runs what it is
// asked to with the local sh.
type sshServer struct {
	addr string
	host gossh.PublicKey

	mu       sync.Mutex
	commands []string
}

func newServer(t *testing.T, client gossh.PublicKey) *sshServer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	config := &gossh.ServerConfig{
		PublicKeyCallback: func(_ gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
			if client != nil && bytes.Equal(key.Marshal(), client.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("not this key")
		},
		PasswordCallback: func(_ gossh.ConnMetadata, pw []byte) (*gossh.Permissions, error) {
			if string(pw) == password {
				return nil, nil
			}
			return nil, errors.New("wrong password")
		},
	}
	config.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	s := &sshServer{addr: ln.Addr().String(), host: signer.PublicKey()}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn, config)
		}
	}()
	return s
}

func (s *sshServer) serve(conn net.Conn, config *gossh.ServerConfig) {
	_, chans, reqs, err := gossh.NewServerConn(conn, config)
	if err != nil {
		_ = conn.Close()
		return
	}
	go gossh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(gossh.UnknownChannelType, "sessions only")
			continue
		}
		ch, requests, err := nc.Accept()
		if err != nil {
			continue
		}
		go s.session(ch, requests)
	}
}

func (s *sshServer) session(ch gossh.Channel, requests <-chan *gossh.Request) {
	defer func() { _ = ch.Close() }()
	for req := range requests {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := gossh.Unmarshal(req.Payload, &payload); err != nil {
			_ = req.Reply(false, nil)
			return
		}
		_ = req.Reply(true, nil)
		s.mu.Lock()
		s.commands = append(s.commands, payload.Command)
		s.mu.Unlock()
		cmd := exec.Command("sh", "-c", payload.Command)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = ch, ch, ch.Stderr()
		status := uint32(0)
		if err := cmd.Run(); err != nil {
			status = 127
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				status = uint32(exit.ExitCode())
			}
		}
		_, _ = ch.SendRequest("exit-status", false, gossh.Marshal(struct{ Status uint32 }{status}))
		return
	}
}

// clientKey returns a new key in OpenSSH form, with its public key.
func clientKey(t *testing.T, passphrase string) (string, gossh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase != "" {
		block, err = gossh.MarshalPrivateKeyWithPassphrase(priv, "jec", []byte(passphrase))
	} else {
		block, err = gossh.MarshalPrivateKey(priv, "jec")
	}
	if err != nil {
		t.Fatal(err)
	}
	pub, err := gossh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(block)), pub
}

func open(t *testing.T, config, secrets map[string]string) *server {
	t.Helper()
	p, err := factory(t.Context(), providers.Instance{Config: config, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*server)
}

func settings(s *sshServer, more map[string]string) map[string]string {
	host, port, _ := net.SplitHostPort(s.addr)
	cfg := map[string]string{"host": host, "port": port, "user": "deploy", "host_key": gossh.FingerprintSHA256(s.host), "directory": "/srv/certs", "layout": byDomain}
	for k, v := range more {
		cfg[k] = v
	}
	return cfg
}

func request(t *testing.T, log *[]contracts.LogLine) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "*.example.com", "example.com")
	return contracts.DeployRequest{CertificateID: 1, Domains: []string{"*.example.com", "example.com"}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key,
		Log: func(l contracts.LogLine) { *log = append(*log, l) }}
}

func needShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the test server runs commands with a POSIX sh")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
}

func problemKey(err error) i18n.Key {
	var p *contracts.Problem
	if errors.As(err, &p) {
		return p.Key
	}
	return ""
}

func TestDeploy(t *testing.T) {
	needShell(t)
	key, pub := clientKey(t, "")
	srv := newServer(t, pub)
	root := t.TempDir()
	var log []contracts.LogLine
	req := request(t, &log)
	s := open(t, settings(srv, map[string]string{"directory": root, "command": "echo reloaded"}), map[string]string{"private_key": key})
	res, err := s.Deploy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "" || len(res.Removed) != 0 {
		t.Errorf("result %+v", res)
	}
	dir := filepath.Join(root, "star.example.com")
	full, _ := os.ReadFile(filepath.Join(dir, "fullchain.pem"))
	priv, _ := os.ReadFile(filepath.Join(dir, "privkey.pem"))
	b, err := cryptox.ParseBundle(full, priv)
	if err != nil || !b.Leaf.Equal(req.Chain[0]) {
		t.Fatalf("files: %v", err)
	}
	if info, err := os.Stat(filepath.Join(dir, "privkey.pem")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("privkey.pem: %v %v", info.Mode(), err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 4 {
		t.Errorf("%d files: temporary ones left?", len(entries))
	}
	var keys []i18n.Key
	output := ""
	for _, l := range log {
		keys = append(keys, l.Key)
		if l.Key == i18n.SshLogOutput {
			output = l.Detail
		}
	}
	want := []i18n.Key{i18n.SshLogConnect, i18n.SshLogUpload, i18n.SshLogCommand, i18n.SshLogOutput}
	if strings.Join(keysText(keys), " ") != strings.Join(keysText(want), " ") || strings.TrimSpace(output) != "reloaded" {
		t.Errorf("log %v, output %q", keys, output)
	}

	// With a password, right in the directory, a new version replaces the files.
	log = nil
	req2 := request(t, &log)
	s = open(t, settings(srv, map[string]string{"directory": root, "layout": flat}), map[string]string{"password": password})
	if _, err := s.Deploy(t.Context(), req2); err != nil {
		t.Fatal(err)
	}
	cert, _ := os.ReadFile(filepath.Join(root, "cert.pem"))
	if certs, err := cryptox.ParseCertificates(cert); err != nil || !certs[0].Equal(req2.Chain[0]) {
		t.Errorf("cert.pem: %v", err)
	}
}

func keysText(keys []i18n.Key) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = string(k)
	}
	return out
}

func TestDeployFailures(t *testing.T) {
	needShell(t)
	key, pub := clientKey(t, "")
	srv := newServer(t, pub)
	root := t.TempDir()
	blocked := filepath.Join(root, "file")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		config map[string]string
		key    i18n.Key
	}{
		{"a directory that cannot be made", map[string]string{"directory": blocked}, i18n.SshErrorWrite},
		{"a failing command", map[string]string{"directory": root, "command": "echo broken >&2; exit 3"}, i18n.SshErrorCommand},
	} {
		var log []contracts.LogLine
		_, err := open(t, settings(srv, c.config), map[string]string{"private_key": key}).Deploy(t.Context(), request(t, &log))
		if problemKey(err) != c.key || !retry.IsPermanent(err) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestSignIn(t *testing.T) {
	key, pub := clientKey(t, "")
	srv := newServer(t, pub)
	var log []contracts.LogLine
	for _, c := range []struct {
		name    string
		config  map[string]string
		secrets map[string]string
		key     i18n.Key
	}{
		{"a host key not checked yet", map[string]string{"host_key": ""}, map[string]string{"private_key": key}, i18n.SshErrorHostKeyUnknown},
		{"another host key", map[string]string{"host_key": "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}, map[string]string{"private_key": key}, i18n.SshErrorHostKey},
		{"a wrong password", nil, map[string]string{"password": "wrong"}, i18n.SshErrorAuth},
		{"a relative directory", map[string]string{"directory": "certs"}, map[string]string{"private_key": key}, i18n.SshErrorRelative},
	} {
		_, err := open(t, settings(srv, c.config), c.secrets).Deploy(t.Context(), request(t, &log))
		if problemKey(err) != c.key || !retry.IsPermanent(err) {
			t.Errorf("%s: %v", c.name, err)
		}
		var p *contracts.Problem
		if c.key == i18n.SshErrorHostKeyUnknown && (!errors.As(err, &p) || p.Params["fingerprint"] != gossh.FingerprintSHA256(srv.host)) {
			t.Errorf("the fingerprint shown: %v", err)
		}
	}

	// Nobody listening is worth another try.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err = open(t, map[string]string{"host": host, "port": port, "user": "u", "directory": "/srv"}, map[string]string{"password": "p"}).Deploy(ctx, request(t, &log))
	if problemKey(err) != i18n.SshErrorConnect || retry.IsPermanent(err) {
		t.Errorf("nobody listening: %v", err)
	}
}

func TestHostKeyForms(t *testing.T) {
	_, pub := clientKey(t, "")
	line := string(bytes.TrimSpace(gossh.MarshalAuthorizedKey(pub)))
	_, other := clientKey(t, "")
	for set, want := range map[string]bool{
		gossh.FingerprintSHA256(pub):                         true,
		strings.TrimRight(gossh.FingerprintSHA256(pub), "="): true,
		line:                              true,
		"example.com,203.0.113.7 " + line: true,
		gossh.FingerprintSHA256(other):    false,
		"":                                false,
		"not a key":                       false,
	} {
		if got := (&conn{hostKey: set}).knows(pub); got != want {
			t.Errorf("knows(%q) = %v", set, got)
		}
	}
}

func TestSettings(t *testing.T) {
	plain, _ := clientKey(t, "")
	locked, _ := clientKey(t, "open sesame")
	for _, c := range []struct {
		name    string
		secrets map[string]string
		key     i18n.Key
	}{
		{"no way to sign in", nil, i18n.SshErrorNoAuth},
		{"not a key", map[string]string{"private_key": "-----BEGIN NOTHING-----"}, i18n.SshErrorKey},
		{"a passphrase missing", map[string]string{"private_key": locked}, i18n.SshErrorPassphrase},
		{"a wrong passphrase", map[string]string{"private_key": locked, "passphrase": "wrong"}, i18n.SshErrorKeyOrPassphrase},
	} {
		_, err := factory(t.Context(), providers.Instance{Config: map[string]string{"host": "h", "user": "u"}, Secrets: c.secrets})
		if problemKey(err) != c.key || !retry.IsPermanent(err) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	for _, secrets := range []map[string]string{
		{"private_key": plain},
		{"private_key": locked, "passphrase": "open sesame"},
	} {
		if _, err := factory(t.Context(), providers.Instance{Config: map[string]string{"host": "h", "user": "u"}, Secrets: secrets}); err != nil {
			t.Errorf("a good key: %v", err)
		}
	}
	if s := open(t, map[string]string{"host": "h", "user": "u"}, map[string]string{"password": "p"}); s.addr != "h:22" {
		t.Errorf("default port: %s", s.addr)
	}
}

func TestQuote(t *testing.T) {
	if got := quote("/srv/it's here"); got != `'/srv/it'\''s here'` {
		t.Errorf("quote: %s", got)
	}
	for in, want := range map[string]string{"*.Example.com": "star.example.com", "a/b": "a_b"} {
		if got, err := dirName([]string{in}); err != nil || got != want {
			t.Errorf("dirName(%q) = %q, %v", in, got, err)
		}
	}
}

func TestTest(t *testing.T) {
	key, pub := clientKey(t, "")
	srv := newServer(t, pub)
	msg, err := open(t, settings(srv, nil), map[string]string{"private_key": key}).Test(t.Context())
	if err != nil || msg.Key != i18n.SshTestOk || msg.Params["user"] != "deploy" {
		t.Errorf("test: %+v, %v", msg, err)
	}
	var p *contracts.Problem
	_, err = open(t, settings(srv, map[string]string{"host_key": ""}), map[string]string{"private_key": key}).Test(t.Context())
	if !errors.As(err, &p) || p.Key != i18n.SshErrorHostKeyUnknown || p.Params["fingerprint"] != gossh.FingerprintSHA256(srv.host) {
		t.Errorf("no host key set: %v", err)
	}
}
