// Package auth manages dashboard authentication: a single dashboard password
// (argon2id-hashed) and short-lived HMAC-signed session tokens.
//
// It is intentionally lightweight — one shared dashboard credential rather than
// per-user accounts — matching KeiRouter's local-first, single-operator model.
// On first run a default password is seeded and an onboarding flow guides the
// operator to change it. Session tokens are signed with a key persisted in
// settings so sessions survive restarts.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// DefaultPassword is seeded on first run. The onboarding flow prompts the
// operator to change it; a warning is logged while it remains in use.
const DefaultPassword = "keirouter"

// Settings keys used to persist auth state.
const (
	keyPasswordHash = "auth.password_hash"
	keySigningKey   = "auth.signing_key"
	keyOnboarding   = "onboarding.complete"
)

// ErrInvalidPassword is returned when a login password does not match.
var ErrInvalidPassword = errors.New("auth: invalid password")

// Service provides password and session operations over the settings store.
type Service struct {
	settings   *store.SettingsRepo
	signingKey []byte
	ttl        time.Duration
}

// New builds an auth Service. configKey, when non-empty, overrides the persisted
// signing key (e.g. from KEIROUTER_SECURITY__JWT_SECRET).
func New(settings *store.SettingsRepo, configKey string, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Service{settings: settings, signingKey: []byte(configKey), ttl: ttl}
}

// EnsureDefaults seeds a default password and signing key on first run. It
// returns true when the default password was just created, so the caller can
// warn the operator. It is safe to call on every startup.
func (s *Service) EnsureDefaults(ctx context.Context) (seededDefault bool, err error) {
	// Signing key: prefer configured value, else load/generate a persisted one.
	if len(s.signingKey) == 0 {
		key, kerr := s.loadOrCreateSigningKey(ctx)
		if kerr != nil {
			return false, kerr
		}
		s.signingKey = key
	}

	// Password: seed the default if none exists yet.
	if _, gerr := s.settings.Get(ctx, keyPasswordHash); errors.Is(gerr, store.ErrNotFound) {
		hash, herr := crypto.HashPassword(DefaultPassword)
		if herr != nil {
			return false, herr
		}
		if serr := s.settings.Set(ctx, keyPasswordHash, hash); serr != nil {
			return false, serr
		}
		_ = s.settings.Set(ctx, keyOnboarding, "false")
		return true, nil
	} else if gerr != nil {
		return false, gerr
	}
	return false, nil
}

func (s *Service) loadOrCreateSigningKey(ctx context.Context) ([]byte, error) {
	if v, err := s.settings.Get(ctx, keySigningKey); err == nil {
		decoded, derr := base64.StdEncoding.DecodeString(v)
		if derr == nil && len(decoded) >= 32 {
			return decoded, nil
		}
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("auth: generate signing key: %w", err)
	}
	if err := s.settings.Set(ctx, keySigningKey, base64.StdEncoding.EncodeToString(key)); err != nil {
		return nil, err
	}
	return key, nil
}

// VerifyPassword reports whether the given password matches the stored hash.
// A successful match against an imported bcrypt hash ($2a$/$2b$) is lazily
// upgraded to the native argon2id verifier so subsequent logins never touch
// the bcrypt path again.
func (s *Service) VerifyPassword(ctx context.Context, password string) (bool, error) {
	hash, err := s.settings.Get(ctx, keyPasswordHash)
	if err != nil {
		return false, err
	}
	ok, err := crypto.VerifyPassword(password, hash)
	if err != nil || !ok {
		return ok, err
	}
	if crypto.IsLegacyBcrypt(hash) {
		if newHash, herr := crypto.HashPassword(password); herr == nil {
			_ = s.settings.Set(ctx, keyPasswordHash, newHash)
		}
	}
	return true, nil
}

// SetPassword changes the dashboard password.
func (s *Service) SetPassword(ctx context.Context, newPassword string) error {
	if len(newPassword) < 6 {
		return errors.New("auth: password must be at least 6 characters")
	}
	hash, err := crypto.HashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.settings.Set(ctx, keyPasswordHash, hash)
}

// UsingDefaultPassword reports whether the current password still matches the
// seeded default, so the UI can nudge the operator to change it.
func (s *Service) UsingDefaultPassword(ctx context.Context) bool {
	ok, err := s.VerifyPassword(ctx, DefaultPassword)
	return err == nil && ok
}

// OnboardingComplete reports whether the operator finished onboarding.
func (s *Service) OnboardingComplete(ctx context.Context) bool {
	v, err := s.settings.Get(ctx, keyOnboarding)
	return err == nil && v == "true"
}

// CompleteOnboarding marks onboarding finished.
func (s *Service) CompleteOnboarding(ctx context.Context) error {
	return s.settings.Set(ctx, keyOnboarding, "true")
}

// session is the signed token payload.
type session struct {
	Sub   string `json:"sub"`
	Email string `json:"email,omitempty"`
	Exp   int64  `json:"exp"`
}

// IssueSession mints a signed session token valid for the configured TTL.
func (s *Service) IssueSession() (string, error) {
	return s.IssueSessionFor("dashboard")
}

// IssueSessionFor mints a signed session token for the given subject audience.
// The portal uses a distinct subject (e.g. "portal:<google_sub>") so a portal
// token can never be accepted by the dashboard middleware.
func (s *Service) IssueSessionFor(sub string) (string, error) {
	return s.issue(session{Sub: sub, Exp: time.Now().Add(s.ttl).Unix()})
}

// IssuePortalSession mints a portal token carrying the verified Google subject
// and email, so the claim handler can bind without a second id_token lookup.
func (s *Service) IssuePortalSession(sub, email string) (string, error) {
	return s.issue(session{Sub: sub, Email: email, Exp: time.Now().Add(s.ttl).Unix()})
}

func (s *Service) issue(p session) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	return body + "." + s.sign(body), nil
}

// VerifySession reports whether a session token is valid and unexpired.
func (s *Service) VerifySession(token string) bool {
	return s.VerifySessionSub(token, "dashboard")
}

// VerifySessionSub reports whether a token is valid and carries the given sub.
func (s *Service) VerifySessionSub(token, sub string) bool {
	got, ok := s.SessionSubject(token)
	return ok && got == sub
}

// SessionSubject returns the signed subject of a valid, unexpired token.
func (s *Service) SessionSubject(token string) (string, bool) {
	p, ok := s.claims(token)
	return p.Sub, ok
}

// SessionEmail returns the email claim of a valid, unexpired token.
func (s *Service) SessionEmail(token string) (string, bool) {
	p, ok := s.claims(token)
	return p.Email, ok
}

// claims verifies the signature and expiry and returns the decoded payload.
func (s *Service) claims(token string) (session, bool) {
	body, sig, ok := strings.Cut(token, ".")
	if !ok {
		return session{}, false
	}
	if !hmac.Equal([]byte(sig), []byte(s.sign(body))) {
		return session{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return session{}, false
	}
	var p session
	if err := json.Unmarshal(raw, &p); err != nil {
		return session{}, false
	}
	if time.Now().Unix() >= p.Exp {
		return session{}, false
	}
	return p, true
}

// TTL returns the session lifetime.
func (s *Service) TTL() time.Duration { return s.ttl }

func (s *Service) sign(body string) string {
	mac := hmac.New(sha256.New, s.signingKey)
	mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
