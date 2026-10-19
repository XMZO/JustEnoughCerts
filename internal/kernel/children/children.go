// Package children runs module code in a child process: the jec binary
// started again with a hidden command, for code that must not share the
// server's process. lego's DNS providers are the reason (PLAN.md 5.5):
// they read their configuration from the environment, so each runs in a
// child whose environment holds its settings and nothing else.
//
// A module registers its child's main function from an init function;
// cmd/jec hands the hidden command to Main. The parent talks to the child
// over its stdin and stdout; the child's stderr is its log.
package children

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
)

// Command is the hidden first argument that starts a child.
const Command = "__child"

// Func is the main function of a child. args are the arguments after its
// name; it returns the exit code.
type Func func(ctx context.Context, args []string, in io.Reader, out io.Writer, log io.Writer) int

var (
	mu    sync.RWMutex
	funcs = map[string]Func{}
)

// Register adds a child's main function under name. Modules call it from
// an init function, so that every binary that includes the module, a test
// binary too, can run its children.
func Register(name string, f Func) {
	mu.Lock()
	defer mu.Unlock()
	if funcs[name] != nil {
		panic("children: " + name + " registered twice")
	}
	funcs[name] = f
}

// IsChild reports whether the process was started as a child.
func IsChild() bool { return len(os.Args) > 1 && os.Args[1] == Command }

// Main runs the child named by os.Args[2] and returns its exit code. Test
// binaries call it from TestMain when IsChild.
func Main(ctx context.Context) int {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "children: no child named")
		return 2
	}
	mu.RLock()
	f := funcs[os.Args[2]]
	mu.RUnlock()
	if f == nil {
		fmt.Fprintf(os.Stderr, "children: no child called %q\n", os.Args[2])
		return 2
	}
	// The protocol owns stdout: anything a library prints there goes to the
	// log instead.
	out := os.Stdout
	os.Stdout = os.Stderr
	return f(ctx, os.Args[3:], os.Stdin, out, os.Stderr)
}

// passed are the variables a child gets from the parent's environment
// besides the ones it is started with: proxies, certificate locations and
// the DNS resolvers set for JEC, which networking code needs to work as
// the server does.
var passed = []string{
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "JEC_DNS_RESOLVERS",
}

// Child is a running child process.
type Child struct {
	cmd *exec.Cmd
	In  io.WriteCloser // the child's stdin
	Out io.ReadCloser  // the child's stdout
}

// Start starts the child called name with args. env is its environment, in
// KEY=value form, to which only the proxy and certificate variables of the
// parent's environment are added: nothing else of the parent's reaches it.
// log receives the child's stderr. ctx ending kills the child.
func Start(ctx context.Context, name string, args, env []string, log io.Writer) (*Child, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("start child %s: %w", name, err)
	}
	cmd := exec.CommandContext(ctx, exe, append([]string{Command, name}, args...)...) //nolint:gosec // G204: our own binary
	cmd.Env = Environ(env)
	cmd.Stderr = log
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start child %s: %w", name, err)
	}
	return &Child{cmd: cmd, In: in, Out: out}, nil
}

// Environ returns the environment of a child started with env: env plus
// the parent's proxy and certificate variables that env does not set,
// sorted. Names compare without case, as Windows does.
func Environ(env []string) []string {
	out := []string{}
	set := map[string]bool{}
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		set[strings.ToUpper(k)] = true
		out = append(out, kv)
	}
	for _, k := range passed {
		v, ok := os.LookupEnv(k)
		if !ok || set[strings.ToUpper(k)] {
			continue
		}
		set[strings.ToUpper(k)] = true
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// Wait closes the child's stdin, which tells it to finish, and waits for
// it to exit.
func (c *Child) Wait() error {
	_ = c.In.Close()
	return c.cmd.Wait()
}

// Kill ends the child at once.
func (c *Child) Kill() {
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}
