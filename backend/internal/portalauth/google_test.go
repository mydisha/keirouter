package portalauth

import (
	"strings"
	"testing"
)

func TestAuthURLContainsGoogleAuthorizeAndState(t *testing.T) {
	s := New(Config{
		ClientID:     "client-abc",
		ClientSecret: "secret",
		RedirectURL:  "http://localhost:20180/portal/auth/google/callback",
	})
	u := s.AuthURL("state-123")
	if !strings.HasPrefix(u, "https://accounts.google.com/o/oauth2/auth?") {
		t.Fatalf("unexpected authorize URL: %s", u)
	}
	for _, want := range []string{"state=state-123", "client_id=client-abc", "scope=openid", "redirect_uri="} {
		if !strings.Contains(u, want) {
			t.Fatalf("authorize URL missing %q: %s", want, u)
		}
	}
}

func TestAcceptPolicy(t *testing.T) {
	s := New(Config{AllowedDomains: []string{"example.com"}})
	if _, err := s.accept(Identity{Email: "a@example.com", EmailVerified: false}); err != ErrEmailNotVerified {
		t.Fatalf("want ErrEmailNotVerified, got %v", err)
	}
	if _, err := s.accept(Identity{Email: "a@other.com", EmailVerified: true}); err != ErrDomainNotAllowed {
		t.Fatalf("want ErrDomainNotAllowed, got %v", err)
	}
	if _, err := s.accept(Identity{Email: "a@example.com", EmailVerified: true}); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	open := New(Config{})
	if _, err := open.accept(Identity{Email: "x@any.com", EmailVerified: true}); err != nil {
		t.Fatalf("empty allow-list must accept any verified email, got %v", err)
	}
}
