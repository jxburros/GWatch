package main

import (
	"crypto/subtle"
	"net"
	"net/http"
	"sync"
	"time"
)

type serveFailure struct {
	count int
	until time.Time
}
type serveLimiter struct {
	mu       sync.Mutex
	failures map[string]serveFailure
}

func newServeLimiter() *serveLimiter { return &serveLimiter{failures: map[string]serveFailure{}} }

// Check the lockout before comparing secrets: returning 429 only for wrong
// guesses would still let an attacker identify a correct guess without limit.
func (l *serveLimiter) authenticate(remote string, got, want []byte, now time.Time) int {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			host = v4.String()
		} else {
			host = ip.Mask(net.CIDRMask(64, 128)).String()
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.failures) >= 1024 {
		for key, f := range l.failures {
			if !now.Before(f.until) {
				delete(l.failures, key)
			}
		}
		if _, ok := l.failures[host]; !ok && len(l.failures) >= 1024 {
			host = "overflow"
		}
	}
	f := l.failures[host]
	if !now.Before(f.until) {
		f = serveFailure{until: now.Add(time.Minute)}
	}
	if f.count >= 10 {
		return http.StatusTooManyRequests
	}
	if len(want) > 0 && subtle.ConstantTimeCompare(got, want) == 1 {
		return 0
	}
	f.count++
	l.failures[host] = f
	return http.StatusUnauthorized
}
