package certs

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A run told to stop while it looks for work, which interrupts the query,
// ends without an error; other failures still count.
func TestQueueStops(t *testing.T) {
	s := &service{running: map[runKey]context.CancelFunc{}}
	interrupted := errors.New("sqlite3: interrupted")
	for _, during := range []string{"resume", "claim"} {
		q := newQueue[int](2, time.Minute)
		ctx, stop := context.WithCancel(t.Context())
		q.resume = func(context.Context) error {
			if during == "resume" {
				stop()
				return interrupted
			}
			return nil
		}
		q.claim = func(context.Context) (*int, error) {
			stop()
			return nil, interrupted
		}
		if err := q.run(ctx, s); err != nil {
			t.Errorf("stopped during %s: %v", during, err)
		}
		stop()
	}

	q := newQueue[int](2, time.Minute)
	q.resume = func(context.Context) error { return nil }
	q.claim = func(context.Context) (*int, error) { return nil, interrupted }
	if err := q.run(t.Context(), s); !errors.Is(err, interrupted) {
		t.Errorf("a failed claim: %v", err)
	}
}
