package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter is a minimal per-key token bucket.
type rateLimiter struct {
	mu    sync.Mutex
	rate  float64
	burst float64
	items map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(rate, burst float64) *rateLimiter {
	return &rateLimiter{
		rate:  rate,
		burst: burst,
		items: make(map[string]*bucket),
	}
}

func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.items[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.items[key] = b
	}

	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// occasional cleanup to avoid unbounded growth
func (l *rateLimiter) gc(now time.Time) {
	if now.Second()%60 != 0 {
		return
	}
	l.mu.Lock()
	for k, b := range l.items {
		if now.Sub(b.last) > 10*time.Minute {
			delete(l.items, k)
		}
	}
	l.mu.Unlock()
}

func rateLimit(l *rateLimiter, key func(r *http.Request) string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l.gc(time.Now())
		if !l.allow(key(r)) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"RateLimitExceeded","message":"too many requests"}`))
			return
		}
		next(w, r)
	}
}

func clientIP(trustProxy bool) func(r *http.Request) string {
	return func(r *http.Request) string {
		if trustProxy {
			if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
				first := xf
				if i := strings.Index(xf, ","); i >= 0 {
					first = xf[:i]
				}
				first = strings.TrimSpace(first)
				if first != "" {
					return stripPort(first)
				}
			}
		}
		return stripPort(r.RemoteAddr)
	}
}

func stripPort(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
