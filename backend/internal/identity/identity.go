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
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// ErrUnauthorized is returned when a presented key is invalid or disabled.
var ErrUnauthorized = errors.New("identity: unauthorized")

// Service manages API key lifecycle and authentication.
type Service struct {
	keys *store.APIKeyRepo

	// authCache caches successful authentication results keyed by lookup hash.
	// Under high concurrency, this avoids re-running argon2id verification
	// (64 MiB + 4 threads each) on every request for the same key.
	authMu    sync.RWMutex
	authCache map[string]authCacheEntry

	// authFlight collapses concurrent cache misses for one key into a single
	// lookup and verification. Without it, every request in flight when an
	// entry expires runs argon2 on its own: 50 requests hold 50×64 MiB.
	authFlight singleflight.Group

	// verifySem bounds argon2 verifications running at once across all keys,
	// so a burst of distinct keys costs at most maxConcurrentVerify×64 MiB.
	verifySem chan struct{}
}

// authCacheEntry holds a cached authentication result. An expired entry is
// kept until evicted: it records that the plaintext with this lookup hash
// already verified against record.KeyHash, so revalidation only needs to
// confirm the row is unchanged.
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

// maxConcurrentVerify caps parallel argon2id verifications (64 MiB each).
const maxConcurrentVerify = 2

// authVerifyTimeout bounds a shared lookup+verification. It is detached from
// the caller that started it, so it needs its own deadline.
const authVerifyTimeout = 10 * time.Second

// verifyAPIKey is the argon2 verifier; tests swap it to count calls.
var verifyAPIKey = crypto.VerifyAPIKey

// New builds an identity Service.
func New(keys *store.APIKeyRepo) *Service {
	return &Service{
		keys:      keys,
		authCache: make(map[string]authCacheEntry),
		verifySem: make(chan struct{}, maxConcurrentVerify),
	}
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
	gen, err := crypto.GenerateAPIKey()
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

	// Slow path. Concurrent misses for the same key share one call, which is
	// detached from the caller that started it so one client hanging up does
	// not fail the others waiting on the result.
	ch := s.authFlight.DoChan(lookup, func() (any, error) {
		vctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), authVerifyTimeout)
		defer cancel()
		return s.authenticateSlow(vctx, plaintext, lookup)
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return store.APIKey{}, res.Err
		}
		return res.Val.(store.APIKey), nil
	case <-ctx.Done():
		return store.APIKey{}, ctx.Err()
	}
}

// authenticateSlow loads the key row and verifies the plaintext against it.
// argon2 runs only when this plaintext has not already verified against the
// row's current hash; an unchanged row is revalidated with the lookup alone,
// which still picks up disable, deletion and rotation within authCacheTTL.
func (s *Service) authenticateSlow(ctx context.Context, plaintext, lookup string) (store.APIKey, error) {
	rec, err := s.keys.FindByLookup(ctx, lookup)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.invalidateAuthCache(lookup)
			return store.APIKey{}, ErrUnauthorized
		}
		return store.APIKey{}, err
	}
	if rec.Disabled {
		s.invalidateAuthCache(lookup)
		return store.APIKey{}, ErrUnauthorized
	}

	if !s.verifiedBefore(lookup, rec) {
		ok, err := s.verify(ctx, plaintext, rec.KeyHash)
		if err != nil && ctx.Err() != nil {
			return store.APIKey{}, err
		}
		if err != nil || !ok {
			return store.APIKey{}, ErrUnauthorized
		}
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

// verifiedBefore reports whether this lookup hash already verified against
// rec's current argon2 hash. The lookup hash is SHA-256 of the plaintext, so
// a matching entry means the same plaintext; a rotated hash or a recreated
// row (new id) forces a fresh verification.
func (s *Service) verifiedBefore(lookup string, rec store.APIKey) bool {
	s.authMu.RLock()
	ent, ok := s.authCache[lookup]
	s.authMu.RUnlock()
	return ok && rec.KeyHash != "" && ent.record.ID == rec.ID && ent.record.KeyHash == rec.KeyHash
}

// verify runs argon2 under verifySem so concurrent verifications stay bounded.
func (s *Service) verify(ctx context.Context, plaintext, hash string) (bool, error) {
	select {
	case s.verifySem <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	defer func() { <-s.verifySem }()
	return verifyAPIKey(plaintext, hash)
}

// cacheAuthResult stores a successful auth result in the cache.
func (s *Service) cacheAuthResult(lookup string, rec store.APIKey) {
	s.authMu.Lock()
	// Evict oldest entries if the cache is full. Refreshing an existing entry
	// does not grow the map, so it must not evict another key.
	if _, exists := s.authCache[lookup]; !exists && len(s.authCache) >= authCacheMaxEntries {
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
