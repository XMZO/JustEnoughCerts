// Package retry decides when a failed operation is tried again (PLAN.md
// 4.4): after an exponential backoff with jitter, after the delay the other
// side asked for, or never.
package retry

import (
	"errors"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Backoff returns the wait after the nth failure in a row, counting from 1:
// base doubled n-1 times, at most limit. A random part of up to half of it
// is dropped, so that retries of many operations spread out.
func Backoff(n int, base, limit time.Duration) time.Duration {
	if base <= 0 || limit <= 0 {
		return 0
	}
	d := min(base, limit)
	for i := 1; i < n && d < limit; i++ {
		d *= 2
	}
	d = min(d, limit)
	return d - rand.N(d/2+1) //nolint:gosec // G404: jitter needs no secure randomness
}

// permanentError marks an error that retrying cannot fix.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent marks err as not to be retried: retrying cannot help, or the
// operation costs money or cannot be undone, so a person decides instead.
// It returns nil for a nil err.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err}
}

// IsPermanent reports whether err or an error it wraps was marked by
// Permanent.
func IsPermanent(err error) bool {
	var p *permanentError
	return errors.As(err, &p)
}

// afterError carries the delay the other side asked for.
type afterError struct {
	err   error
	delay time.Duration
}

func (e *afterError) Error() string { return e.err.Error() }
func (e *afterError) Unwrap() error { return e.err }

// After marks err as worth retrying no sooner than delay from now, as a
// Retry-After header asks. It returns nil for a nil err.
func After(err error, delay time.Duration) error {
	if err == nil {
		return nil
	}
	return &afterError{err, max(delay, 0)}
}

// Delay returns the delay that After attached to err or an error it wraps.
func Delay(err error) (time.Duration, bool) {
	var a *afterError
	if errors.As(err, &a) {
		return a.delay, true
	}
	return 0, false
}

// ParseRetryAfter reads the value of a Retry-After header, in seconds or as
// an HTTP date, as a delay from now.
func ParseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if secs, err := strconv.ParseInt(value, 10, 64); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(min(secs, math.MaxInt64/int64(time.Second))) * time.Second, true
	}
	t, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	return max(t.Sub(now), 0), true
}
