// Package shellx runs scripts with the shell built into JEC (mvdan.cc/sh),
// which reads POSIX sh and Bash, so that scripts run where no shell is
// installed, as in JEC's container image. The programs a script calls are
// looked up and run as a shell would; redirections work on the files of
// the machine.
package shellx

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// MaxOutput bounds what Run keeps of what a script writes: its end.
const MaxOutput = 4000

// Parse reads script as a shell program, POSIX sh or Bash. Its error says
// where the script is wrong.
func Parse(script string) (*syntax.File, error) {
	return syntax.NewParser().Parse(strings.NewReader(script), "")
}

// Script is a program to run, and what it runs with.
type Script struct {
	File *syntax.File
	// Dir is the directory it starts in, which must exist.
	Dir string
	// Env is added to Environ.
	Env []string
	// Stdin is what it reads from its standard input; nil for nothing.
	Stdin []byte
}

// Run runs s until it ends or ctx does, and returns the end of what it
// wrote to its standard output and error, together. The error is nil when
// s exits with status 0, carries the status for another one (Status), and
// is ctx's error when ctx ended first.
func Run(ctx context.Context, s Script) ([]byte, error) {
	var out output
	var stdin io.Reader
	if s.Stdin != nil {
		// A pipe of its own, so that it is closed when the script ends,
		// whether it read it all or not.
		r, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		defer func() { _ = r.Close() }()
		go func() {
			_, _ = w.Write(s.Stdin)
			_ = w.Close()
		}()
		stdin = r
	}
	runner, err := interp.New(
		interp.Dir(s.Dir),
		interp.Env(expand.ListEnviron(append(Environ(), s.Env...)...)),
		interp.StdIO(stdin, &out, &out),
	)
	if err != nil {
		return nil, err
	}
	err = runner.Run(ctx, s.File)
	if ctx.Err() != nil {
		return out.bytes(), ctx.Err()
	}
	return out.bytes(), err
}

// Status returns the exit status an error of Run carries, if it does.
func Status(err error) (int, bool) {
	status, ok := errors.AsType[interp.ExitStatus](err)
	return int(status), ok
}

// Environ is JEC's environment but for its own settings, such as the
// master key, which a script has no use for.
func Environ() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "JEC_") {
			env = append(env, kv)
		}
	}
	return env
}

// output keeps the end of what a script writes, from its standard output
// and error at once.
type output struct {
	mu  sync.Mutex
	buf []byte
}

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.buf = append(o.buf, p...)
	if len(o.buf) > 2*MaxOutput {
		o.buf = append(o.buf[:0], o.buf[len(o.buf)-MaxOutput:]...)
	}
	return len(p), nil
}

func (o *output) bytes() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]byte(nil), o.buf[max(len(o.buf)-MaxOutput, 0):]...)
}
