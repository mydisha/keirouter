// Package identity issues and verifies KeiRouter API keys.
//
// Keys are never stored in plaintext: creation returns the plaintext once for
// the user to copy, while the store keeps a keyed lookup index (HMAC-SHA256
// under a key derived from the master key, see crypto.KeyLookup). A key is
// 192 random bits, so finding its row by that index authenticates it; no
// password-style hash is needed on the request path.
//
// Rows also carry the legacy SHA-256 index and an argon2id verifier. Neither
// is read for keys that have the keyed index; they are still written so a
// release that predates the keyed index keeps working after a rollback.
package identity

import (
	"context"
	"errors"
	"log/slog"
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
	keys   *store.APIKeyRepo
	lookup *crypto.KeyLookup

	// authCache caches successful authentication results keyed by the keyed
	// lookup index, so an active key costs no database read per request.
	authMu    sync.RWMutex
	authCache map[string]authCacheEntry

	// authFlight collapses concurrent cache misses for one key into a single
	// database lookup.
	authFlight singleflight.Group
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

// authLookupTimeout bounds a shared lookup. It is detached from the caller
// that started it, so it needs its own deadline.
const authLookupTimeout = 10 * time.Second

// reconcileBatch is how many rows ReconcileLookups indexes per transaction.
const reconcileBatch = 500

// Lookup functions, swapped by tests to observe or slow the shared lookup.
var (
	findByLookupHMAC = (*store.APIKeyRepo).FindByLookupHMAC
	findByLookup     = (*store.APIKeyRepo).FindByLookup
)

// New builds an identity Service. lookup must not be nil: it is derived from
// the master key (crypto.NewKeyLookup) and indexes every key.
func New(keys *store.APIKeyRepo, lookup *crypto.KeyLookup) *Service {
	if lookup == nil {
		panic("identity: nil key lookup")
	}
	return &Service{
		keys:      keys,
		lookup:    lookup,
		authCache: make(map[string]authCacheEntry),
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
		ID:        uuid.NewString(),
		TenantID:  tenantID,
		ProjectID: projectID,
		Name:      name,
		KeyHash:   gen.Hash,
		Display:   gen.Display,
		CreatedAt: time.Now(),
	}
	s.SetLookups(&rec, gen.Plaintext)
	return Issued{Record: rec, Plaintext: gen.Plaintext}, nil
}

// SetLookups fills rec's lookup indexes for plaintext: the keyed index the
// request path reads, and the legacy SHA-256 index kept for rollback.
func (s *Service) SetLookups(rec *store.APIKey, plaintext string) {
	rec.LookupHash = crypto.LookupHash(plaintext)
	rec.LookupHMAC = s.lookup.Of(plaintext)
	rec.LookupKID = s.lookup.KID()
}

// KeyLookup exposes the keyed lookup index, for callers that match many
// plaintext keys against stored rows at once (foreign import).
func (s *Service) KeyLookup() *crypto.KeyLookup { return s.lookup }

// CreateFromIssued persists a previously generated key (from Generate).
func (s *Service) CreateFromIssued(ctx context.Context, issued Issued) error {
	return s.keys.Create(ctx, issued.Record)
}

// Keys exposes the underlying key repo for transactional flows.
func (s *Service) Keys() *store.APIKeyRepo { return s.keys }

// Authenticate verifies a presented plaintext key and returns its record. On
// success it best-effort updates the key's last-used timestamp. A short-lived
// positive-result cache keeps active keys off the database.
func (s *Service) Authenticate(ctx context.Context, plaintext string) (store.APIKey, error) {
	if plaintext == "" {
		return store.APIKey{}, ErrUnauthorized
	}
	lookup := s.lookup.Of(plaintext)

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

	// Slow path. Concurrent misses for the same key share one lookup, which is
	// detached from the caller that started it so one client hanging up does
	// not fail the others waiting on the result.
	ch := s.authFlight.DoChan(lookup, func() (any, error) {
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), authLookupTimeout)
		defer cancel()
		return s.authenticateSlow(lctx, plaintext, lookup)
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

// authenticateSlow loads the key row. A fresh lookup on every cache miss picks
// up disable, deletion and rotation within authCacheTTL.
func (s *Service) authenticateSlow(ctx context.Context, plaintext, lookup string) (store.APIKey, error) {
	rec, err := s.find(ctx, plaintext, lookup, true)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.invalidateAuthCache(lookup)
			return store.APIKey{}, ErrUnauthorized
		}
		return store.APIKey{}, err
	}
	// Managed keys act only through their own surface (the playground key
	// through the dashboard session), never by presenting a secret.
	if rec.Disabled || rec.Kind != "" {
		s.invalidateAuthCache(lookup)
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

// FindByPlaintext returns the row for a plaintext key, enabled or not, without
// touching the auth cache, last-used time or the stored indexes.
func (s *Service) FindByPlaintext(ctx context.Context, plaintext string) (store.APIKey, error) {
	if plaintext == "" {
		return store.APIKey{}, store.ErrNotFound
	}
	return s.find(ctx, plaintext, s.lookup.Of(plaintext), false)
}

// find resolves a plaintext key to its row by the keyed index. A row that has
// no keyed index under the current lookup key yet (minted by a replica still
// on an older release, or indexed under a previous master key) is found by
// the legacy SHA-256 index instead; with repair set it is then given the
// keyed index, so the next lookup takes the fast path.
//
// The legacy index is only trusted for rows that are not already indexed
// under the current key: such a row with a different keyed index means the
// two indexes disagree, and the key is rejected rather than guessed at.
func (s *Service) find(ctx context.Context, plaintext, lookup string, repair bool) (store.APIKey, error) {
	rec, err := findByLookupHMAC(s.keys, ctx, lookup)
	if !errors.Is(err, store.ErrNotFound) {
		return rec, err
	}
	rec, err = findByLookup(s.keys, ctx, crypto.LookupHash(plaintext))
	if err != nil {
		return store.APIKey{}, err
	}
	kid := s.lookup.KID()
	if rec.LookupHMAC != "" && rec.LookupKID == kid {
		return store.APIKey{}, store.ErrNotFound
	}
	if repair {
		if _, err := s.keys.SetLookupHMAC(ctx, store.LookupIndexUpdate{ID: rec.ID, HMAC: lookup, KID: kid}); err != nil {
			slog.Warn("identity: index api key", "key_id", rec.ID, "err", err)
		} else {
			rec.LookupHMAC, rec.LookupKID = lookup, kid
		}
	}
	return rec, nil
}

// ReconcileStats reports what ReconcileLookups changed.
type ReconcileStats struct {
	// Indexed counts rows given a keyed index, Reindexed included.
	Indexed int
	// Reindexed counts rows that had a keyed index under another lookup key,
	// which means the master key changed since they were indexed.
	Reindexed int
	// Skipped counts rows whose legacy index is not a SHA-256 hex digest and
	// so cannot be converted; they cannot authenticate either way.
	Skipped int
}

// ReconcileLookups gives every row the keyed index under the current lookup
// key, computing it from the stored legacy index, so no key has to be
// presented or reissued. It is idempotent and safe to run on several replicas
// at once: each row converges to the same value. Run it at startup, after
// migrations, before serving requests.
func (s *Service) ReconcileLookups(ctx context.Context) (ReconcileStats, error) {
	kid := s.lookup.KID()
	var st ReconcileStats
	after := ""
	for {
		page, err := s.keys.ListPendingLookups(ctx, kid, after, reconcileBatch)
		if err != nil {
			return st, err
		}
		if len(page) == 0 {
			return st, nil
		}
		after = page[len(page)-1].ID

		updates := make([]store.LookupIndexUpdate, 0, len(page))
		reindex := 0
		for _, p := range page {
			h, err := s.lookup.FromLegacy(p.LookupHash)
			if err != nil {
				st.Skipped++
				continue
			}
			if p.LookupHMAC != "" {
				reindex++
			}
			updates = append(updates, store.LookupIndexUpdate{ID: p.ID, HMAC: h, KID: kid})
		}
		n, err := s.keys.SetLookupHMACs(ctx, updates)
		if err != nil {
			return st, err
		}
		st.Indexed += n
		st.Reindexed += min(reindex, n)
	}
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

// PlaygroundKeyName is the name usage, the console and budgets show for
// requests sent from the dashboard playground.
const PlaygroundKeyName = "Playground"

// Playground returns the tenant's playground key, creating it on first use.
// Its secret is discarded at creation (and Authenticate refuses managed keys),
// so the key only ever acts through the dashboard session. A unique index
// keeps one per tenant; a concurrent creator that loses the race reads the
// winner's row.
func (s *Service) Playground(ctx context.Context, tenantID string) (store.APIKey, error) {
	rec, err := s.keys.FindByKind(ctx, tenantID, store.KeyKindPlayground)
	if !errors.Is(err, store.ErrNotFound) {
		return rec, err
	}
	issued, err := s.Generate(tenantID, "", PlaygroundKeyName)
	if err != nil {
		return store.APIKey{}, err
	}
	issued.Record.Kind = store.KeyKindPlayground
	issued.Record.Display = "dashboard"
	if err := s.keys.Create(ctx, issued.Record); err != nil {
		if again, ferr := s.keys.FindByKind(ctx, tenantID, store.KeyKindPlayground); ferr == nil {
			return again, nil
		}
		return store.APIKey{}, err
	}
	return issued.Record, nil
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
