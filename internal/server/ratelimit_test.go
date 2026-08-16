package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRateLimiterBurst(t *testing.T) {
	t.Parallel()
	l := newRateLimiter(1, 3)

	for i := range 3 {
		require.True(t, l.allow("k"), "request %d should be allowed within burst", i+1)
	}
	require.False(t, l.allow("k"), "request over burst should be rejected")
}

func TestRateLimiterDistinctKeys(t *testing.T) {
	t.Parallel()
	l := newRateLimiter(1, 1)
	require.True(t, l.allow("a"), "first key should be allowed")
	require.True(t, l.allow("b"), "independent key should be allowed")
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
	require.Equal(t, "1.2.3.4", untrusted(mk("1.2.3.4:1234", "9.9.9.9")), "untrusted should use RemoteAddr")

	trusted := clientIP(true)
	require.Equal(t, "9.9.9.9", trusted(mk("1.2.3.4:1234", "9.9.9.9:9999, 8.8.8.8")), "trusted should use leftmost XFF")
	require.Equal(t, "1.2.3.4", trusted(mk("1.2.3.4:1234", "")), "trusted without XFF should use RemoteAddr")
}
