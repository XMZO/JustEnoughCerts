package auth

import (
	"sync"
	"time"
)

// limiter counts failed attempts per client and refuses a client after too
// many within a window.
type limiter struct {
	max    int
	window time.Duration

	mu      sync.Mutex
	clients map[string]*attempts
}

type attempts struct {
	count int
	since time.Time
}

func newLimiter(maxFailures int, window time.Duration) *limiter {
	return &limiter{max: maxFailures, window: window, clients: map[string]*attempts{}}
}

// wait returns how long client must wait before trying again; 0 means it
// may try now.
func (l *limiter) wait(client string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.clients[client]
	if !ok {
		return 0
	}
	if end := a.since.Add(l.window); now.Before(end) {
		if a.count >= l.max {
			return end.Sub(now)
		}
		return 0
	}
	delete(l.clients, client)
	return 0
}

// fail records a failed attempt by client.
func (l *limiter) fail(client string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for c, a := range l.clients { // keep the map small
		if now.Sub(a.since) >= l.window {
			delete(l.clients, c)
		}
	}
	a, ok := l.clients[client]
	if !ok {
		a = &attempts{since: now}
		l.clients[client] = a
	}
	a.count++
}

// reset forgets client's failures, after it succeeded.
func (l *limiter) reset(client string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.clients, client)
}
