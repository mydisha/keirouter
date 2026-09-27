// Package identity issues and verifies KeiRouter API keys.
//
// Keys are never stored in plaintext: creation returns the plaintext once for
// the user to copy, while the store keeps only an argon2id verifier plus a
// fast SHA-256 lookup index. Authentication looks up the candidate row by the
// lookup index, then confirms with a constant-time argon2 comparison.
package identity

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// ErrUnauthorized is returned when a presented key is invalid or disabled.
var ErrUnauthorized = errors.New("identity: unauthorized")

// prefixTokenRe matches an allowed prefix token: 1-16 lowercase letters or digits.
var prefixTokenRe = regexp.MustCompile(`^[a-z0-9]{1,16}$`)

// NormalizePrefix validates a user-supplied API key prefix and returns its
// canonical "token_" form. An empty or underscore-only input resets to the
// default. Invalid input returns ok=false and must be rejected by the caller.
func NormalizePrefix(raw string) (string, bool) {
	token := strings.Trim(strings.ToLower(strings.TrimSpace(raw)), "_")
	if token == "" {
		return crypto.DefaultKeyPrefix, true
	}
	if !prefixTokenRe.MatchString(token) {
		return "", false
	}
	return token + "_", true
}

// Service manages API key lifecycle and authentication.
type Service struct {
	keys *store.APIKeyRepo

	prefixMu sync.RWMutex
	prefix   string

	// authCache caches successful authentication results keyed by lookup hash.
	// Under high concurrency, this avoids re-running argon2id verification
	// (64 MiB + 4 threads each) on every request for the same key.
	authMu    sync.RWMutex
	authCache map[string]authCacheEntry
}

// authCacheEntry holds a cached authentication result.
type authCacheEntry struct {
	record  store.APIKey
	expires time.Time
}

// authCacheTTL bounds how long a successful auth result is cached. Short enough
// that key disable/deletion takes effect within seconds, long enough to absorb
// bursty traffic from the same key (Claude Code sends 10+ req/s).
const authCacheTTL = 5 * time.Second

// authCacheMaxEntries limits the cache size to prevent unbounded memory growth.
const authCacheMaxEntries = 256

// New builds an identity Service.
func New(keys *store.APIKeyRepo) *Service {
	return &Service{
		keys:      keys,
		prefix:    crypto.DefaultKeyPrefix,
		authCache: make(map[string]authCacheEntry),
	}
}

// keyPrefix returns the active issued-key prefix, defaulting when unset.
func (s *Service) keyPrefix() string {
	s.prefixMu.RLock()
	defer s.prefixMu.RUnlock()
	if s.prefix == "" {
		return crypto.DefaultKeyPrefix
	}
	return s.prefix
}

// SetKeyPrefix sets the prefix used for newly issued keys. It is safe for
// concurrent use and takes effect without restart. An empty value resets the
// default.
func (s *Service) SetKeyPrefix(prefix string) {
	if prefix == "" {
		prefix = crypto.DefaultKeyPrefix
	}
	s.prefixMu.Lock()
	s.prefix = prefix
	s.prefixMu.Unlock()
}

// Issued is the result of creating a key. Plaintext is shown exactly once.
type Issued struct {
	Record    store.APIKey
	Plaintext string
}

// Create mints a new API key for a tenant/project, persisting only its hashes.
func (s *Service) Create(ctx context.Context, tenantID, projectID, name string) (Issued, error) {
	issued, err := s.Generate(tenantID, projectID, name)
	if err != nil {
		return Issued{}, err
	}
	if err := s.keys.Create(ctx, issued.Record); err != nil {
		return Issued{}, err
	}
	return issued, nil
}

// Generate creates key material (hashes, display) without persisting.
// The caller is responsible for inserting Issued.Record into the store,
// typically inside a transaction when co-creating related resources.
func (s *Service) Generate(tenantID, projectID, name string) (Issued, error) {
	gen, err := crypto.GenerateAPIKey(s.keyPrefix())
	if err != nil {
		return Issued{}, err
	}
	rec := store.APIKey{
		ID:         uuid.NewString(),
		TenantID:   tenantID,
		ProjectID:  projectID,
		Name:       name,
		KeyHash:    gen.Hash,
		LookupHash: gen.Lookup,
		Display:    gen.Display,
		CreatedAt:  time.Now(),
	}
	return Issued{Record: rec, Plaintext: gen.Plaintext}, nil
}

// CreateFromIssued persists a previously generated key (from Generate).
func (s *Service) CreateFromIssued(ctx context.Context, issued Issued) error {
	return s.keys.Create(ctx, issued.Record)
}

// Keys exposes the underlying key repo for transactional flows.
func (s *Service) Keys() *store.APIKeyRepo { return s.keys }

// Authenticate verifies a presented plaintext key and returns its record. On
// success it best-effort updates the key's last-used timestamp. A short-lived
// positive-result cache avoids re-running argon2id on every request for active
// keys, which is critical under high concurrency (each argon2 verification
// consumes 64 MiB + 4 OS threads).
func (s *Service) Authenticate(ctx context.Context, plaintext string) (store.APIKey, error) {
	if plaintext == "" {
		return store.APIKey{}, ErrUnauthorized
	}
	lookup := crypto.LookupHash(plaintext)

	// Fast path: check auth cache for a recent positive result.
	s.authMu.RLock()
	if ent, ok := s.authCache[lookup]; ok && time.Now().Before(ent.expires) {
		rec := ent.record
		s.authMu.RUnlock()
		// Verify the key hasn't been disabled since caching.
		if rec.Disabled {
			s.invalidateAuthCache(lookup)
			return store.APIKey{}, ErrUnauthorized
		}
		return rec, nil
	}
	s.authMu.RUnlock()

	// Slow path: full DB lookup + argon2 verification.
	rec, err := s.keys.FindByLookup(ctx, lookup)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.APIKey{}, ErrUnauthorized
		}
		return store.APIKey{}, err
	}
	if rec.Disabled {
		return store.APIKey{}, ErrUnauthorized
	}

	ok, err := crypto.VerifyAPIKey(plaintext, rec.KeyHash)
	if err != nil || !ok {
		return store.APIKey{}, ErrUnauthorized
	}

	// Cache the positive result for subsequent requests.
	s.cacheAuthResult(lookup, rec)

	// Best-effort last-used update; failure here must not fail auth.
	// Run in a goroutine with a bounded timeout to prevent goroutine
	// accumulation when SQLite's single writer is busy.
	go func() {
		touchCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.keys.TouchLastUsed(touchCtx, rec.ID, time.Now())
	}()
	return rec, nil
}

// cacheAuthResult stores a successful auth result in the cache.
func (s *Service) cacheAuthResult(lookup string, rec store.APIKey) {
	s.authMu.Lock()
	// Evict oldest entries if the cache is full.
	if len(s.authCache) >= authCacheMaxEntries {
		var oldest string
		var oldestExpiry time.Time
		for k, v := range s.authCache {
			if oldest == "" || v.expires.Before(oldestExpiry) {
				oldest, oldestExpiry = k, v.expires
			}
		}
		if oldest != "" {
			delete(s.authCache, oldest)
		}
	}
	s.authCache[lookup] = authCacheEntry{record: rec, expires: time.Now().Add(authCacheTTL)}
	s.authMu.Unlock()
}

// invalidateAuthCache removes a cached auth entry (e.g. when a key is disabled).
func (s *Service) invalidateAuthCache(lookup string) {
	s.authMu.Lock()
	delete(s.authCache, lookup)
	s.authMu.Unlock()
}

// InvalidateAuthCacheForKey clears the auth cache for a specific key ID.
// Call this after key mutations (disable, delete) so the next auth attempt
// re-verifies immediately.
func (s *Service) InvalidateAuthCacheForKey(keyID string) {
	s.authMu.Lock()
	for lookup, ent := range s.authCache {
		if ent.record.ID == keyID {
			delete(s.authCache, lookup)
		}
	}
	s.authMu.Unlock()
}

// List returns the keys for a tenant (without secrets).
func (s *Service) List(ctx context.Context, tenantID string) ([]store.APIKey, error) {
	return s.keys.List(ctx, tenantID)
}

// Get returns a single key by id.
func (s *Service) Get(ctx context.Context, id string) (store.APIKey, error) {
	return s.keys.Get(ctx, id)
}

// SetDisabled toggles a key's disabled state.
func (s *Service) SetDisabled(ctx context.Context, id string, disabled bool) error {
	err := s.keys.SetDisabled(ctx, id, disabled)
	if err == nil {
		s.InvalidateAuthCacheForKey(id)
	}
	return err
}

// Delete removes a key.
func (s *Service) Delete(ctx context.Context, id string) error {
	err := s.keys.Delete(ctx, id)
	if err == nil {
		s.InvalidateAuthCacheForKey(id)
	}
	return err
}
