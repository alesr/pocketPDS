package server

import (
	"net/http"
	"testing"
)

func TestRateLimiterBurst(t *testing.T) {
	t.Parallel()
	l := newRateLimiter(1, 3)

	for i := range 3 {
		if !l.allow("k") {
			t.Fatalf("request %d should be allowed within burst", i+1)
		}
	}
	if l.allow("k") {
		t.Fatal("request over burst should be rejected")
	}
}

func TestRateLimiterDistinctKeys(t *testing.T) {
	t.Parallel()
	l := newRateLimiter(1, 1)
	if !l.allow("a") {
		t.Fatal("first key should be allowed")
	}
	if !l.allow("b") {
		t.Fatal("independent key should be allowed")
	}
}

func TestClientIP(t *testing.T) {
	t.Parallel()
	mk := func(remote, xff string) *http.Request {
		r := &http.Request{RemoteAddr: remote}
		if xff != "" {
			r.Header = http.Header{"X-Forwarded-For": []string{xff}}
		}
		return r
	}

	untrusted := clientIP(false)
	if got := untrusted(mk("1.2.3.4:1234", "9.9.9.9")); got != "1.2.3.4" {
		t.Fatalf("untrusted should use RemoteAddr, got %q", got)
	}

	trusted := clientIP(true)
	if got := trusted(mk("1.2.3.4:1234", "9.9.9.9:9999, 8.8.8.8")); got != "9.9.9.9" {
		t.Fatalf("trusted should use leftmost XFF, got %q", got)
	}
	if got := trusted(mk("1.2.3.4:1234", "")); got != "1.2.3.4" {
		t.Fatalf("trusted without XFF should use RemoteAddr, got %q", got)
	}
}
