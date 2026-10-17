package retry

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	base, limit := 10*time.Second, time.Hour
	for _, tt := range []struct {
		n    int
		want time.Duration // before jitter
	}{
		{0, 10 * time.Second},
		{1, 10 * time.Second},
		{2, 20 * time.Second},
		{3, 40 * time.Second},
		{9, 2560 * time.Second},
		{10, time.Hour},
		{1000, time.Hour},
	} {
		for range 50 {
			got := Backoff(tt.n, base, limit)
			if got < tt.want/2 || got > tt.want {
				t.Fatalf("Backoff(%d) = %v, want between %v and %v", tt.n, got, tt.want/2, tt.want)
			}
		}
	}
	if got := Backoff(3, 0, time.Hour); got != 0 {
		t.Errorf("Backoff without a base = %v", got)
	}
	if got := Backoff(1, time.Hour, time.Minute); got > time.Minute {
		t.Errorf("Backoff above the limit: %v", got)
	}
}

func TestPermanent(t *testing.T) {
	base := errors.New("payment required")
	err := fmt.Errorf("renew: %w", Permanent(base))
	if !IsPermanent(err) || !errors.Is(err, base) || err.Error() != "renew: payment required" {
		t.Errorf("Permanent: %v, permanent %v", err, IsPermanent(err))
	}
	if IsPermanent(base) || Permanent(nil) != nil {
		t.Error("Permanent marks too much")
	}
}

func TestAfter(t *testing.T) {
	base := errors.New("rate limited")
	err := fmt.Errorf("poll: %w", After(base, 90*time.Second))
	if d, ok := Delay(err); !ok || d != 90*time.Second || !errors.Is(err, base) {
		t.Errorf("Delay = %v, %v", d, ok)
	}
	if d, ok := Delay(After(base, -time.Second)); !ok || d != 0 {
		t.Errorf("negative delay: %v, %v", d, ok)
	}
	if _, ok := Delay(base); ok || After(nil, time.Second) != nil {
		t.Error("After marks too much")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"120", 2 * time.Minute, true},
		{" 0 ", 0, true},
		{"Sat, 17 Oct 2026 08:05:00 GMT", 5 * time.Minute, true},
		{"Sat, 17 Oct 2026 07:00:00 GMT", 0, true},
		{"99999999999999999", time.Duration(1<<63-1) / time.Second * time.Second, true},
		{"-5", 0, false},
		{"", 0, false},
		{"soon", 0, false},
	} {
		got, ok := ParseRetryAfter(tt.in, now)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseRetryAfter(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
