package auth

import (
	"sync"
	"time"
)

// Limiter is a token bucket keyed by client IP. Two of them are used by the
// API: one counts *failed* credential attempts (so a brute-force run against
// the login form or an API key runs out of attempts), and one counts every
// request made by a remote API-key client (the general rate limiting called
// for by ROADMAP 2.5).
//
// Buckets live in memory only. That is deliberate: GWatch is a single process
// on one machine, restarts are rare, and a limiter that survived restarts
// would only add a way to lock yourself out of your own monitor.
type Limiter struct {
	burst  float64       // bucket capacity
	rate   float64       // tokens refilled per second
	ttl    time.Duration // buckets idle for this long are dropped
	now    func() time.Time
	mu     sync.Mutex
	stop   chan struct{}
	closed bool
	seen   map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter returns a limiter that allows n events per window per key, with a
// burst of n. A zero or negative n disables limiting entirely.
func NewLimiter(n int, window time.Duration) *Limiter {
	if window <= 0 {
		window = time.Minute
	}
	l := &Limiter{
		burst: float64(n),
		rate:  float64(n) / window.Seconds(),
		ttl:   window * 4,
		now:   time.Now,
		seen:  make(map[string]*bucket),
		stop:  make(chan struct{}),
	}
	if l.ttl < time.Minute {
		l.ttl = time.Minute
	}
	go l.janitor()
	return l
}

// Allow consumes one token for key. It reports whether the event is allowed
// and, when it is not, how long the caller should wait before retrying.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	if l == nil || l.burst <= 0 {
		return true, 0
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.seen[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.seen[key] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens += elapsed * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
	}
	b.last = now
	if b.tokens < 1 {
		wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
		if wait < time.Second {
			wait = time.Second
		}
		return false, wait
	}
	b.tokens--
	return true, 0
}

// Reset clears the bucket for key. The API calls it after a successful login
// so that a person who mistyped their password a few times is not held back
// once they get it right.
func (l *Limiter) Reset(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.seen, key)
	l.mu.Unlock()
}

// Close stops the background cleanup goroutine.
func (l *Limiter) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		l.closed = true
		close(l.stop)
	}
}

func (l *Limiter) janitor() {
	t := time.NewTicker(l.ttl)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
			l.sweep()
		}
	}
}

func (l *Limiter) sweep() {
	cutoff := l.now().Add(-l.ttl)
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, b := range l.seen {
		// Only drop buckets that are both idle and fully refilled, so that a
		// client hammering the server cannot clear its own record by pausing.
		if b.last.Before(cutoff) && b.tokens+l.rate*l.now().Sub(b.last).Seconds() >= l.burst {
			delete(l.seen, k)
		}
	}
}

// Size reports how many buckets are currently tracked (used by tests).
func (l *Limiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.seen)
}
