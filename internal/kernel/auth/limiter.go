package auth

import (
	"sync"
	"time"
)

// limiter locks a client out for a while after too many failed attempts
// in a row. A streak ends with a success, or once the client has not failed
// for as long as a lockout lasts.
type limiter struct {
	max  int
	lock time.Duration

	mu      sync.Mutex
	clients map[string]*attempts
}

type attempts struct {
	streak int       // failures in a row
	last   time.Time // latest failure
	until  time.Time // locked out until
}

func newLimiter(maxFailures int, lock time.Duration) *limiter {
	return &limiter{max: maxFailures, lock: lock, clients: map[string]*attempts{}}
}

// wait returns how long client must wait before trying again; 0 means it
// may try now.
func (l *limiter) wait(client string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if a, ok := l.clients[client]; ok && now.Before(a.until) {
		return a.until.Sub(now)
	}
	return 0
}

// fail records a failed attempt by client and locks it out when that makes
// too many in a row, which it tells.
func (l *limiter) fail(client string, now time.Time) (locked bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for c, a := range l.clients { // keep the map small
		if now.Sub(a.last) >= l.lock && !now.Before(a.until) {
			delete(l.clients, c)
		}
	}
	a, ok := l.clients[client]
	if !ok {
		a = &attempts{}
		l.clients[client] = a
	}
	a.streak++
	a.last = now
	if a.streak >= l.max {
		a.streak = 0
		a.until = now.Add(l.lock)
		return true
	}
	return false
}

// reset ends client's streak, after it succeeded.
func (l *limiter) reset(client string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.clients, client)
}
