package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
)

func TestClientIP(t *testing.T) {
	trusting := &Server{cfg: config.Default()}
	require.True(t, trusting.cfg.Security.TrustForwardedHeaders)
	strict := &Server{cfg: config.Default()}
	strict.cfg.Security.TrustForwardedHeaders = false

	req := func(remote string, xff ...string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		for _, v := range xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		return r
	}
	cases := []struct {
		name string
		s    *Server
		r    *http.Request
		want string
	}{
		{"direct client", trusting, req("203.0.113.7:5000"), "203.0.113.7"},
		{"direct client cannot spoof", trusting, req("203.0.113.7:5000", "1.2.3.4"), "203.0.113.7"},
		{"local proxy: right-most entry", trusting, req("127.0.0.1:5000", "1.2.3.4, 198.51.100.9"), "198.51.100.9"},
		{"local proxy: last header wins", trusting, req("127.0.0.1:5000", "1.2.3.4", "198.51.100.9"), "198.51.100.9"},
		{"private-network proxy", trusting, req("10.0.0.5:5000", "198.51.100.9"), "198.51.100.9"},
		{"local proxy, junk header", trusting, req("127.0.0.1:5000", "not-an-ip"), "127.0.0.1"},
		{"forwarded headers off", strict, req("127.0.0.1:5000", "198.51.100.9"), "127.0.0.1"},
		{"ipv6 grouped per /64", trusting, req("[2001:db8:1:2:aaaa::1]:5000"), "2001:db8:1:2::/64"},
		{"ipv6 behind proxy", trusting, req("127.0.0.1:5000", "2001:db8:1:2:bbbb::9"), "2001:db8:1:2::/64"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.s.clientIP(tc.r))
		})
	}
}

// Behind a same-host proxy every request arrives from 127.0.0.1. Invalid API
// keys must still count against the real client, or key guessing is never
// slowed down.
func TestAuthFailureIPBehindLocalProxy(t *testing.T) {
	s := &Server{cfg: config.Default()}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	_, ok := s.authFailureIP(r)
	require.False(t, ok, "a genuinely local client is the operator")

	r.Header.Set("X-Forwarded-For", "198.51.100.9")
	ip, ok := s.authFailureIP(r)
	require.True(t, ok)
	require.Equal(t, "198.51.100.9", ip)
}
