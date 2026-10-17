package children

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func init() {
	Register("printenv", func(_ context.Context, _ []string, _ io.Reader, out io.Writer, _ io.Writer) int {
		_ = json.NewEncoder(out).Encode(os.Environ())
		return 0
	})
	Register("echo", func(_ context.Context, args []string, in io.Reader, out io.Writer, log io.Writer) int {
		fmt.Println("noise for the log, not for the protocol")
		fmt.Fprintln(log, "args", strings.Join(args, ","))
		_, _ = io.Copy(out, in)
		return 3
	})
}

// The test binary is the child too.
func TestMain(m *testing.M) {
	if IsChild() {
		os.Exit(Main(context.Background()))
	}
	os.Exit(m.Run())
}

// clearPassed removes the passed variables of the machine running the test
// until it ends.
func clearPassed(t *testing.T) {
	t.Helper()
	for _, k := range passed {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func TestEnvironment(t *testing.T) {
	clearPassed(t)
	t.Setenv("JEC_SECRET_KEY", "must not leak")
	t.Setenv("HTTPS_PROXY", "http://proxy.example:3128")
	t.Setenv("SSL_CERT_FILE", "/etc/ssl/custom.pem")
	var log bytes.Buffer
	c, err := Start(t.Context(), "printenv", nil, []string{"TENCENTCLOUD_SECRET_ID=abc", "HTTPS_PROXY=http://other:1"}, &log)
	if err != nil {
		t.Fatal(err)
	}
	var env []string
	if err := json.NewDecoder(c.Out).Decode(&env); err != nil {
		t.Fatal(err, log.String())
	}
	if err := c.Wait(); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if runtime.GOOS == "windows" && strings.EqualFold(k, "SYSTEMROOT") {
			continue // Go adds it on Windows
		}
		got = append(got, kv)
	}
	slices.Sort(got)
	want := []string{"HTTPS_PROXY=http://other:1", "SSL_CERT_FILE=/etc/ssl/custom.pem", "TENCENTCLOUD_SECRET_ID=abc"}
	if !slices.Equal(got, want) {
		t.Errorf("child environment %v, want %v", got, want)
	}
}

func TestProtocolAndLog(t *testing.T) {
	var log syncBuffer
	c, err := Start(t.Context(), "echo", []string{"a", "b"}, nil, &log)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(c.In, "hello\n"); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(c.Out).ReadString('\n')
	if err != nil || line != "hello\n" {
		t.Fatalf("read %q, %v", line, err)
	}
	err = c.Wait()
	if code := exitCode(err); code != 3 {
		t.Errorf("exit code %d (%v), want 3", code, err)
	}
	if s := log.String(); !strings.Contains(s, "noise for the log") || !strings.Contains(s, "args a,b") {
		t.Errorf("the child's log: %q", s)
	}
}

func TestKillOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	c, err := Start(ctx, "echo", nil, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	done := make(chan struct{})
	go func() {
		_ = c.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the child outlived its context")
	}
}

func TestUnknownChild(t *testing.T) {
	c, err := Start(t.Context(), "nobody", nil, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if code := exitCode(c.Wait()); code != 2 {
		t.Errorf("exit code %d, want 2", code)
	}
}

func exitCode(err error) int {
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitCode()
	}
	return -1
}

// syncBuffer is a bytes.Buffer safe for the copying goroutine of os/exec.
type syncBuffer struct {
	mu  chan struct{}
	buf bytes.Buffer
}

func (b *syncBuffer) lock() {
	if b.mu == nil {
		b.mu = make(chan struct{}, 1)
	}
	b.mu <- struct{}{}
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.lock()
	defer func() { <-b.mu }()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.lock()
	defer func() { <-b.mu }()
	return b.buf.String()
}
