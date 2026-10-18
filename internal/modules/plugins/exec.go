package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// Limits of a call.
const (
	maxAnswer = 1 << 20 // bytes a plugin may write on its standard output
	maxStderr = 4000    // bytes of its standard error kept for the log
	maxLog    = 100     // lines of log an answer may add
	maxText   = 4000    // bytes of a text in an answer
)

// How long a plugin may take: to describe itself while JEC starts, to test
// settings, and to do anything else.
var (
	describeTimeout = 10 * time.Second
	testTimeout     = time.Minute
	actionTimeout   = 5 * time.Minute
)

// plugin is an executable of the plugins directory.
type plugin struct {
	id   string
	path string
	// data is the plugin's own directory, where it runs and may keep what
	// it needs to.
	data string
}

// answerer is an answer of a plugin: answer, and what an action adds.
type answerer interface{ common() *answer }

func (a *answer) common() *answer { return a }

// call runs the plugin with req on its standard input, and decodes what it
// writes on its standard output into ans. It returns what the plugin wrote
// on its standard error, for the log, and an error when the plugin could
// not run or gave no answer; an answer that says it failed is no error
// here.
func (p *plugin) call(ctx context.Context, timeout time.Duration, req request, ans answerer) ([]byte, error) {
	req.Protocol = protocol
	input, err := json.Marshal(req)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	if err := os.MkdirAll(p.data, 0o700); err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.PluginsErrorRun, Params: map[string]any{"plugin": p.id}, Err: err})
	}
	run, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(run, p.path) //nolint:gosec // G204: running the plugins put in the plugins directory is the point
	cmd.Dir = p.data
	cmd.Env = environ(p.id, p.data)
	cmd.Stdin = bytes.NewReader(input)
	out, errOut := &capped{limit: maxAnswer}, &tail{limit: maxStderr}
	cmd.Stdout, cmd.Stderr = out, errOut
	cmd.WaitDelay = 5 * time.Second
	ownGroup(cmd)
	err = cmd.Run()
	stderr := errOut.bytes()
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return stderr, ctx.Err()
	case errors.Is(run.Err(), context.DeadlineExceeded):
		return stderr, &contracts.Problem{Key: i18n.PluginsErrorTimeout, Params: map[string]any{"plugin": p.id, "seconds": int(timeout.Seconds())}}
	case err != nil && !errors.As(err, &exit):
		// It did not start: not executable, or not for this system.
		return stderr, retry.Permanent(&contracts.Problem{Key: i18n.PluginsErrorRun, Params: map[string]any{"plugin": p.id}, Err: err})
	case out.over:
		return stderr, invalid(p.id, fmt.Sprintf("the answer is longer than %d bytes", maxAnswer))
	case len(bytes.TrimSpace(out.buf)) == 0 && exit != nil:
		return stderr, &contracts.Problem{Key: i18n.PluginsErrorExit, Params: map[string]any{"plugin": p.id, "status": exit.ExitCode()}, Err: errors.New(string(stderr))}
	}
	if err := json.Unmarshal(out.buf, ans); err != nil {
		return stderr, invalid(p.id, err.Error())
	}
	if v := ans.common().Protocol; v != protocol {
		return stderr, invalid(p.id, fmt.Sprintf("the answer is of protocol %d, not %d", v, protocol))
	}
	return stderr, nil
}

// invalid is an answer that JEC cannot read, which trying again cannot
// fix.
func invalid(plugin, problem string) error {
	return retry.Permanent(&contracts.Problem{Key: i18n.PluginsErrorAnswer, Params: map[string]any{"plugin": plugin, "problem": problem}})
}

// failure is the error of an answer that says the action failed, or nil.
func failure(plugin string, a *answer) error {
	if a.OK {
		return nil
	}
	p := &contracts.Problem{Key: i18n.PluginsErrorFailed, Params: map[string]any{"plugin": plugin, "message": clip(a.Error)}}
	switch {
	case !a.Retry:
		return retry.Permanent(p)
	case a.RetryAfter > 0:
		return retry.After(p, time.Duration(a.RetryAfter)*time.Second)
	}
	return p
}

// environ is what a plugin runs with: JEC's environment but for its own
// settings, such as the master key, and what the plugin may want to know.
func environ(id, data string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "JEC_") {
			env = append(env, kv)
		}
	}
	return append(env, "JEC_PLUGIN="+id, "JEC_PLUGIN_DATA="+data, fmt.Sprintf("JEC_PLUGIN_PROTOCOL=%d", protocol))
}

// clip shortens a text of a plugin to maxText bytes, between characters.
func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxText {
		return s
	}
	n := maxText
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// capped keeps what is written up to limit bytes, and notes when there is
// more.
type capped struct {
	limit int
	buf   []byte
	over  bool
}

func (c *capped) Write(p []byte) (int, error) {
	n := len(p)
	if room := c.limit - len(c.buf); n > room {
		// The rest is dropped, without failing the copy.
		c.over = true
		p = p[:max(room, 0)]
	}
	c.buf = append(c.buf, p...)
	return n, nil
}

// tail keeps the last limit bytes written.
type tail struct {
	limit int
	buf   []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*t.limit {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.limit:]...)
	}
	return len(p), nil
}

func (t *tail) bytes() []byte {
	if len(t.buf) > t.limit {
		return t.buf[len(t.buf)-t.limit:]
	}
	return t.buf
}
