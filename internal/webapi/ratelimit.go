package webapi

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// ipRateLimiter is a fixed-window-per-bucket in-memory rate limiter keyed by
// client IP. Chetter assumes a single server replica (see AGENTS.md), so an
// in-process limiter is sufficient; horizontal scaling would need a shared
// store.
type ipRateLimiter struct {
	mu       sync.Mutex
	window   time.Duration
	max      int
	entries  map[string]*rateBucket
	lastGC   time.Time
	gcPeriod time.Duration
}

type rateBucket struct {
	count     int
	windowEnd time.Time
}

func newIPRateLimiter(max int, window time.Duration) *ipRateLimiter {
	return &ipRateLimiter{
		window:   window,
		max:      max,
		entries:  make(map[string]*rateBucket),
		lastGC:   time.Now(),
		gcPeriod: window * 10,
	}
}

// allow reports whether one more request from ip fits the budget.
func (l *ipRateLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.maybeGC(now)
	b, ok := l.entries[ip]
	if !ok || now.After(b.windowEnd) {
		l.entries[ip] = &rateBucket{count: 1, windowEnd: now.Add(l.window)}
		return true
	}
	if b.count >= l.max {
		return false
	}
	b.count++
	return true
}

// maybeGC drops expired buckets so the map cannot grow without bound. Caller
// holds l.mu.
func (l *ipRateLimiter) maybeGC(now time.Time) {
	if now.Sub(l.lastGC) < l.gcPeriod {
		return
	}
	l.lastGC = now
	for ip, b := range l.entries {
		if now.After(b.windowEnd) {
			delete(l.entries, ip)
		}
	}
}

// RateLimit wraps next with per-IP fixed-window limiting. When limit <= 0 the
// middleware is a passthrough (limiting disabled).
func RateLimit(limit int, window time.Duration, next http.Handler) http.Handler {
	if limit <= 0 {
		return next
	}
	limiter := newIPRateLimiter(limit, window)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.allow(clientIP(r), time.Now()) {
			retryAfter := int((window + time.Second - 1) / time.Second)
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts the direct peer identity for rate limiting. It deliberately
// ignores X-Forwarded-For because Chetter has no trusted-proxy allowlist; a
// directly reachable client could otherwise bypass limits by rotating a
// spoofed header.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
