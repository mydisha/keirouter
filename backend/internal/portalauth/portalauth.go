// Package portalauth implements Google sign-in for the public usage portal.
// It performs the OIDC authorization-code flow server-side and verifies the
// returned id_token with Google's published keys.
package portalauth

import (
	"errors"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// ErrEmailNotVerified is returned when Google reports an unverified email.
var ErrEmailNotVerified = errors.New("portalauth: email not verified")

// ErrDomainNotAllowed is returned when the email domain is not allow-listed.
var ErrDomainNotAllowed = errors.New("portalauth: email domain not allowed")

// Identity is the verified Google identity.
type Identity struct {
	Sub           string
	Email         string
	EmailVerified bool
}

// Config holds the OAuth client settings and access policy.
type Config struct {
	ClientID       string
	ClientSecret   string
	RedirectURL    string
	AllowedDomains []string
}

// Service drives the Google OIDC flow.
type Service struct {
	cfg   Config
	oauth *oauth2.Config
}

// New builds a Service.
func New(cfg Config) *Service {
	return &Service{
		cfg: cfg,
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		},
	}
}

// AuthURL builds the Google authorize URL for the given CSRF state.
func (s *Service) AuthURL(state string) string {
	return s.oauth.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

// domainAllowed reports whether the email passes the allow-list.
func (s *Service) domainAllowed(email string) bool {
	if len(s.cfg.AllowedDomains) == 0 {
		return true
	}
	_, domain, ok := strings.Cut(email, "@")
	if !ok {
		return false
	}
	for _, d := range s.cfg.AllowedDomains {
		if strings.EqualFold(strings.TrimSpace(d), domain) {
			return true
		}
	}
	return false
}

// accept validates policy for a verified identity.
func (s *Service) accept(id Identity) (Identity, error) {
	if !id.EmailVerified {
		return Identity{}, ErrEmailNotVerified
	}
	if !s.domainAllowed(id.Email) {
		return Identity{}, ErrDomainNotAllowed
	}
	return id, nil
}
