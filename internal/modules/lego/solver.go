package lego

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/children"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
)

// closeGrace is how long a child may take to finish after its stdin
// closes.
const closeGrace = 10 * time.Second

// solver is a contracts.Solver backed by a child running one lego DNS
// provider. The child starts with the first call, and keeps running until
// Close, since some providers remember between Present and CleanUp what
// they created.
type solver struct {
	code string
	env  []string
	log  *slog.Logger

	mu       sync.Mutex
	ctx      context.Context // the child's life
	cancel   context.CancelFunc
	child    *children.Child
	enc      *json.Encoder
	dec      *json.Decoder
	timing   ready
	broken   error // once a call fails to get its reply, every later call does
	logLines *lineWriter
}

var _ contracts.Solver = (*solver)(nil)

func newSolver(code string, env []string, log *slog.Logger) *solver {
	ctx, cancel := context.WithCancel(context.Background())
	return &solver{code: code, env: env, log: log, ctx: ctx, cancel: cancel}
}

// start starts the child if it does not run yet; s.mu is held.
func (s *solver) start() error {
	if s.broken != nil {
		return s.broken
	}
	if s.child != nil {
		return nil
	}
	s.logLines = &lineWriter{log: func(line string) { s.log.Info("lego", "line", line) }}
	child, err := children.Start(s.ctx, childName, []string{s.code}, s.env, s.logLines)
	if err != nil {
		s.broken = err
		return err
	}
	s.child = child
	s.enc = json.NewEncoder(child.In)
	s.dec = json.NewDecoder(child.Out)
	if err := s.dec.Decode(&s.timing); err != nil {
		s.broken = fmt.Errorf("lego %s: the provider did not start: %w", s.code, err)
		child.Kill()
		return s.broken
	}
	if s.timing.Error != "" {
		s.broken = fmt.Errorf("lego %s: %s", s.code, s.timing.Error)
		return s.broken
	}
	return nil
}

// call sends req and waits for the reply, or for ctx to end, which kills
// the child: a provider halfway through a request cannot be trusted with
// the next one.
func (s *solver) call(ctx context.Context, req request) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.start(); err != nil {
		return err
	}
	if err := s.enc.Encode(req); err != nil {
		s.broken = fmt.Errorf("lego %s: the provider stopped: %w", s.code, err)
		return s.broken
	}
	var rep reply
	var readErr error
	done := make(chan struct{})
	go func() {
		readErr = s.dec.Decode(&rep)
		close(done)
	}()
	select {
	case <-done:
		if readErr != nil {
			s.broken = fmt.Errorf("lego %s: the provider stopped: %w", s.code, readErr)
			return s.broken
		}
	case <-ctx.Done():
		s.child.Kill()
		<-done // the read ends with the child
		s.broken = fmt.Errorf("lego %s: %w", s.code, ctx.Err())
		return s.broken
	}
	if rep.Error != "" {
		return fmt.Errorf("lego %s: %s", s.code, rep.Error)
	}
	return nil
}

// Present adds the TXT record of ch.
func (s *solver) Present(ctx context.Context, ch contracts.Challenge) error {
	return s.call(ctx, request{Op: "present", Domain: ch.Domain, Token: ch.Token, KeyAuth: ch.KeyAuth})
}

// CleanUp removes the TXT record of ch.
func (s *solver) CleanUp(ctx context.Context, ch contracts.Challenge) error {
	return s.call(ctx, request{Op: "cleanup", Domain: ch.Domain, Token: ch.Token, KeyAuth: ch.KeyAuth})
}

// Wait returns the provider's own timing, which starts the child to learn
// it; lego's defaults if the child cannot start.
func (s *solver) Wait() (timeout, interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.start(); err != nil || s.timing.Timeout <= 0 {
		return defaultTimeout, defaultInterval
	}
	return s.timing.Timeout, s.timing.Interval
}

// Close ends the child: it closes its stdin and, past closeGrace, kills it.
func (s *solver) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.cancel()
	if s.child == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- s.child.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(closeGrace):
		s.child.Kill()
		err = <-done
	}
	s.child = nil
	if s.logLines != nil {
		s.logLines.flush()
	}
	if s.broken != nil {
		return nil // the child was killed on purpose
	}
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) && exit.ExitCode() > 0 {
		return fmt.Errorf("lego %s: the provider exited with code %d", s.code, exit.ExitCode())
	}
	return nil
}

// lineWriter logs what a child writes, line by line.
type lineWriter struct {
	log func(string)
	mu  sync.Mutex
	buf []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := strings.IndexByte(string(w.buf), '\n')
		if i < 0 {
			break
		}
		if line := strings.TrimSpace(string(w.buf[:i])); line != "" {
			w.log(line)
		}
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if line := strings.TrimSpace(string(w.buf)); line != "" {
		w.log(line)
	}
	w.buf = nil
}

var _ io.Writer = (*lineWriter)(nil)
