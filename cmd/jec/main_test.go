package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// clearEnv hides the developer's own JEC_* variables from the commands.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"JEC_CONFIG", "JEC_DATA_DIR", "JEC_LISTEN", "JEC_BASE_URL", "JEC_LOG_LEVEL", "JEC_LOG_FORMAT", "JEC_DNS_RESOLVERS"} {
		t.Setenv(key, "") // empty counts as unset
	}
}

func runCLI(ctx context.Context, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(ctx, args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// freeAddr returns a loopback address with a port that was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func TestUsage(t *testing.T) {
	tests := []struct {
		args     []string
		code     int
		inStdout bool
	}{
		{nil, exitUsage, false},
		{[]string{"help"}, exitOK, true},
		{[]string{"--help"}, exitOK, true},
		{[]string{"bogus"}, exitUsage, false},
	}
	for _, tt := range tests {
		code, stdout, stderr := runCLI(t.Context(), tt.args...)
		if code != tt.code {
			t.Errorf("jec %q: exit %d, want %d", tt.args, code, tt.code)
		}
		out := stderr
		if tt.inStdout {
			out = stdout
		}
		if !strings.Contains(out, "healthcheck") {
			t.Errorf("jec %q: usage missing from output: %q", tt.args, out)
		}
	}
}

func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		code, stdout, _ := runCLI(t.Context(), args...)
		if code != exitOK || !strings.HasPrefix(stdout, "jec ") || !strings.Contains(stdout, runtime.Version()) {
			t.Errorf("jec %q: exit %d, output %q", args, code, stdout)
		}
	}
}

func TestBadFlags(t *testing.T) {
	clearEnv(t)
	for _, args := range [][]string{
		{"serve", "--no-such-flag"},
		{"serve", "extra-argument"},
		{"healthcheck", "--timeout", "soon"},
	} {
		if code, _, _ := runCLI(t.Context(), args...); code != exitUsage {
			t.Errorf("jec %q: exit %d, want %d", args, code, exitUsage)
		}
	}
	if code, _, stderr := runCLI(t.Context(), "serve", "--listen", "nope"); code != exitError || !strings.Contains(stderr, "listen") {
		t.Errorf("serve with a bad listen address: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := runCLI(t.Context(), "serve", "--dns-resolvers", "223.5.5.5,1.2.3.4:dns"); code != exitError || !strings.Contains(stderr, "1.2.3.4:dns") {
		t.Errorf("serve with a bad DNS resolver: exit %d, stderr %q", code, stderr)
	}
}

func TestServeAndHealthcheck(t *testing.T) {
	clearEnv(t)
	dataDir := filepath.Join(t.TempDir(), "nested", "data")
	addr := freeAddr(t)
	common := []string{"--data-dir", dataDir, "--listen", addr}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	served := make(chan int, 1)
	var serveErr bytes.Buffer
	go func() {
		served <- run(ctx, append([]string{"serve", "--log-level", "error"}, common...), &bytes.Buffer{}, &serveErr)
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		code, _, stderr := runCLI(t.Context(), append([]string{"healthcheck", "--timeout", "1s"}, common...)...)
		if code == exitOK {
			break
		}
		select {
		case code := <-served:
			t.Fatalf("serve exited early with %d: %s", code, serveErr.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never became healthy; last healthcheck: %s", stderr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(dataDir); err != nil {
		t.Errorf("data directory was not created: %v", err)
	}

	cancel()
	select {
	case code := <-served:
		if code != exitOK {
			t.Errorf("serve exited with %d: %s", code, serveErr.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not stop after cancel")
	}

	if code, _, _ := runCLI(t.Context(), append([]string{"healthcheck", "--timeout", "1s"}, common...)...); code != exitError {
		t.Errorf("healthcheck against a stopped server: exit %d, want %d", code, exitError)
	}
}

func TestServeUnwritableDataDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions enforced for the current user")
	}
	clearEnv(t)
	dataDir := t.TempDir()
	if err := os.Chmod(dataDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dataDir, 0o700) })

	code, _, stderr := runCLI(t.Context(), "serve", "--data-dir", dataDir, "--listen", freeAddr(t))
	if code != exitError || !strings.Contains(stderr, "not writable") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

func TestHealthURL(t *testing.T) {
	tests := map[string]string{
		":8080":            "http://127.0.0.1:8080/healthz",
		"0.0.0.0:8080":     "http://127.0.0.1:8080/healthz",
		"[::]:8080":        "http://[::1]:8080/healthz",
		"192.0.2.10:9000":  "http://192.0.2.10:9000/healthz",
		"localhost:8080":   "http://localhost:8080/healthz",
		"[2001:db8::1]:80": "http://[2001:db8::1]:80/healthz",
	}
	for listen, want := range tests {
		got, err := healthURL(listen)
		if err != nil || got != want {
			t.Errorf("healthURL(%q) = %q, %v; want %q", listen, got, err, want)
		}
	}
}
