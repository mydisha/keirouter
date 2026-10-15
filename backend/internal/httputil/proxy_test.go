package httputil

import (
	"net/url"
	"testing"
)

func TestNoProxyRulesMatch(t *testing.T) {
	cases := []struct {
		list, target string
		want         bool
	}{
		{"", "https://api.openai.com/v1", false},
		{"*", "https://api.openai.com/v1", true},
		{"openai.com", "https://api.openai.com/v1", true},
		{"openai.com", "https://openai.com/v1", true},
		{"openai.com", "https://notopenai.com/v1", false},
		{".openai.com", "https://api.openai.com/v1", true},
		{".openai.com", "https://openai.com/v1", false},
		{"*.openai.com", "https://api.openai.com/v1", true},
		{"*.openai.com", "https://openai.com/v1", false},
		{"OpenAI.COM", "https://API.openai.com/v1", true},
		{" localhost , 127.0.0.1 ", "http://127.0.0.1:11434/api", true},
		{"localhost", "http://localhost:11434/api", true},
		{"10.0.0.0/8", "http://10.1.2.3:8080/v1", true},
		{"10.0.0.0/8", "http://192.168.1.2:8080/v1", false},
		{"localhost:8080", "http://localhost:8080/v1", true},
		{"localhost:8080", "http://localhost:9090/v1", false},
		{"example.com:443", "https://example.com/v1", true},
		{"::1", "http://[::1]:8080/v1", true},
		{"[::1]:8080", "http://[::1]:8080/v1", true},
		{"[::1]:8080", "http://[::1]:9090/v1", false},
	}
	for _, tc := range cases {
		u, err := url.Parse(tc.target)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.target, err)
		}
		if got := ParseNoProxy(tc.list).Match(u); got != tc.want {
			t.Errorf("NO_PROXY %q, target %q: got %v, want %v", tc.list, tc.target, got, tc.want)
		}
	}
}

func TestIsSupportedProxyScheme(t *testing.T) {
	for _, s := range []string{"http", "HTTPS", "socks5", "socks5h"} {
		if !IsSupportedProxyScheme(s) {
			t.Errorf("%q should be supported", s)
		}
	}
	for _, s := range []string{"", "ftp", "socks4", "file"} {
		if IsSupportedProxyScheme(s) {
			t.Errorf("%q should be rejected", s)
		}
	}
}

func TestValidateProxyURLAcceptsSocks5h(t *testing.T) {
	if err := ValidateProxyURL("socks5h://proxy.example.com:1080"); err != nil {
		t.Fatalf("socks5h rejected: %v", err)
	}
	if err := ValidateProxyURL("ftp://proxy.example.com:21"); err == nil {
		t.Fatal("ftp proxy accepted")
	}
}
