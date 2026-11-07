package identity

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func testLookup(b byte) *crypto.KeyLookup {
	return crypto.NewKeyLookup(bytes.Repeat([]byte{b}, crypto.KeySize))
}

func newTestDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newAuthTestService(t *testing.T) (*Service, *store.DB) {
	t.Helper()
	db := newTestDB(t)
	return New(db.APIKeys(), testLookup(1)), db
}

// insertLegacyKey stores a key the way a release without the keyed index did:
// legacy SHA-256 index and argon2 verifier only.
func insertLegacyKey(t *testing.T, db *store.DB, id, plaintext string) {
	t.Helper()
	require.NoError(t, db.APIKeys().Create(context.Background(), store.APIKey{
		ID: id, TenantID: store.DefaultTenantID, Name: id,
		KeyHash: "$argon2id$v=19$m=65536,t=1,p=4$legacy$legacy", LookupHash: crypto.LookupHash(plaintext),
		Display: "kr_…", CreatedAt: time.Now(),
	}))
}

// countLookups wraps the keyed and legacy lookups to count calls; hold keeps
// each keyed lookup busy so overlapping callers are observable.
func countLookups(t *testing.T, hold time.Duration) (keyed, legacy *atomic.Int64) {
	t.Helper()
	keyed, legacy = new(atomic.Int64), new(atomic.Int64)
	origKeyed, origLegacy := findByLookupHMAC, findByLookup
	findByLookupHMAC = func(r *store.APIKeyRepo, ctx context.Context, lookup string) (store.APIKey, error) {
		keyed.Add(1)
		time.Sleep(hold)
		return origKeyed(r, ctx, lookup)
	}
	findByLookup = func(r *store.APIKeyRepo, ctx context.Context, lookup string) (store.APIKey, error) {
		legacy.Add(1)
		return origLegacy(r, ctx, lookup)
	}
	t.Cleanup(func() { findByLookupHMAC, findByLookup = origKeyed, origLegacy })
	return keyed, legacy
}

// expireAuthCache ages every cached entry past its TTL without removing it,
// which is the state the cache is in when a busy key's entry runs out.
func expireAuthCache(s *Service) {
	s.authMu.Lock()
	for k, e := range s.authCache {
		e.expires = time.Now().Add(-time.Second)
		s.authCache[k] = e
	}
	s.authMu.Unlock()
}

func TestAuthenticateConcurrentMissesLookupOnce(t *testing.T) {
	s, _ := newAuthTestService(t)
	ctx := context.Background()
	issued, err := s.Create(ctx, store.DefaultTenantID, "", "busy")
	require.NoError(t, err)
	keyed, legacy := countLookups(t, 20*time.Millisecond)

	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for range 50 {
		wg.Go(func() {
			rec, err := s.Authenticate(ctx, issued.Plaintext)
			if err == nil && rec.ID != issued.Record.ID {
				err = ErrUnauthorized
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, keyed.Load(), "50 concurrent first requests must share one lookup")
	require.Zero(t, legacy.Load(), "a key minted with the keyed index never needs the legacy one")
}

func TestAuthenticateDoesNotReadKeyHash(t *testing.T) {
	s, db := newAuthTestService(t)
	ctx := context.Background()

	// A verifier that could never pass argon2 proves auth does not consult it.
	rec := store.APIKey{
		ID: "k", TenantID: store.DefaultTenantID, Name: "k",
		KeyHash: "not-an-argon2-hash", Display: "kr_…", CreatedAt: time.Now(),
	}
	const plaintext = "kr_plaintext-for-test"
	s.SetLookups(&rec, plaintext)
	require.NoError(t, db.APIKeys().Create(ctx, rec))

	got, err := s.Authenticate(ctx, plaintext)
	require.NoError(t, err)
	require.Equal(t, "k", got.ID)

	for range 3 {
		expireAuthCache(s)
		_, err = s.Authenticate(ctx, plaintext)
		require.NoError(t, err)
	}
}

func TestAuthenticateExpiredEntrySeesDisableInStore(t *testing.T) {
	s, db := newAuthTestService(t)
	ctx := context.Background()
	issued, err := s.Create(ctx, store.DefaultTenantID, "", "revoked")
	require.NoError(t, err)

	_, err = s.Authenticate(ctx, issued.Plaintext)
	require.NoError(t, err)

	// Disable straight through the repo, bypassing the service's cache
	// invalidation, as another replica sharing the database would.
	require.NoError(t, db.APIKeys().SetDisabled(ctx, issued.Record.ID, true))
	expireAuthCache(s)
	_, err = s.Authenticate(ctx, issued.Plaintext)
	require.ErrorIs(t, err, ErrUnauthorized)

	require.NoError(t, db.APIKeys().SetDisabled(ctx, issued.Record.ID, false))
	_, err = s.Authenticate(ctx, issued.Plaintext)
	require.NoError(t, err)
}

func TestAuthenticateExpiredEntrySeesDeleteInStore(t *testing.T) {
	s, db := newAuthTestService(t)
	ctx := context.Background()
	issued, err := s.Create(ctx, store.DefaultTenantID, "", "gone")
	require.NoError(t, err)
	_, err = s.Authenticate(ctx, issued.Plaintext)
	require.NoError(t, err)

	require.NoError(t, db.APIKeys().Delete(ctx, issued.Record.ID))
	expireAuthCache(s)
	_, err = s.Authenticate(ctx, issued.Plaintext)
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestAuthenticateRejectsUnknownKeys(t *testing.T) {
	s, _ := newAuthTestService(t)
	issued, err := s.Create(context.Background(), store.DefaultTenantID, "", "real")
	require.NoError(t, err)
	for _, k := range []string{"kr_not-a-real-key", "kr_🔑ünïcode", "x", issued.Plaintext + "x", issued.Plaintext[:len(issued.Plaintext)-1]} {
		_, err := s.Authenticate(context.Background(), k)
		require.ErrorIs(t, err, ErrUnauthorized, "key %q", k)
	}
	_, err = s.Authenticate(context.Background(), "")
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestAuthenticateWaiterCancelDoesNotFailOthers(t *testing.T) {
	s, _ := newAuthTestService(t)
	issued, err := s.Create(context.Background(), store.DefaultTenantID, "", "shared")
	require.NoError(t, err)
	countLookups(t, 50*time.Millisecond)

	cctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() {
		_, err := s.Authenticate(cctx, issued.Plaintext)
		first <- err
	}()
	time.Sleep(10 * time.Millisecond) // let the first caller start the shared call
	second := make(chan error, 1)
	go func() {
		_, err := s.Authenticate(context.Background(), issued.Plaintext)
		second <- err
	}()
	cancel()

	require.ErrorIs(t, <-first, context.Canceled)
	require.NoError(t, <-second, "the shared lookup must survive the first caller hanging up")
}

// A key minted by a release without the keyed index (or by a replica still on
// one during a rolling deploy) authenticates, and is indexed on first use.
func TestAuthenticateLegacyRowFallsBackAndIsIndexed(t *testing.T) {
	s, db := newAuthTestService(t)
	ctx := context.Background()
	const plaintext = "kr_minted-by-an-older-release"
	insertLegacyKey(t, db, "old", plaintext)
	keyed, legacy := countLookups(t, 0)

	got, err := s.Authenticate(ctx, plaintext)
	require.NoError(t, err)
	require.Equal(t, "old", got.ID)
	require.EqualValues(t, 1, legacy.Load())

	row, err := db.APIKeys().Get(ctx, "old")
	require.NoError(t, err)
	require.Equal(t, s.lookup.Of(plaintext), row.LookupHMAC)
	require.Equal(t, s.lookup.KID(), row.LookupKID)
	require.Equal(t, crypto.LookupHash(plaintext), row.LookupHash, "legacy index is kept for rollback")

	expireAuthCache(s)
	_, err = s.Authenticate(ctx, plaintext)
	require.NoError(t, err)
	require.EqualValues(t, 2, keyed.Load())
	require.EqualValues(t, 1, legacy.Load(), "once indexed, the legacy lookup is not needed")
}

func TestAuthenticateLegacyFallbackRejectsDisagreeingIndex(t *testing.T) {
	s, db := newAuthTestService(t)
	ctx := context.Background()
	const plaintext = "kr_some-key"

	// The legacy index matches, but the row is already indexed under the
	// current key with another value: the indexes disagree, so no match.
	rec := store.APIKey{
		ID: "x", TenantID: store.DefaultTenantID, Name: "x", KeyHash: "h",
		LookupHash: crypto.LookupHash(plaintext), LookupHMAC: s.lookup.Of("kr_another-key"),
		LookupKID: s.lookup.KID(), Display: "kr_…", CreatedAt: time.Now(),
	}
	require.NoError(t, db.APIKeys().Create(ctx, rec))

	_, err := s.Authenticate(ctx, plaintext)
	require.ErrorIs(t, err, ErrUnauthorized)
}

// Changing the master key must not lock out existing keys while the legacy
// index is still stored: they are found by it and re-indexed.
func TestAuthenticateSurvivesMasterKeyChange(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	before := New(db.APIKeys(), testLookup(1))
	issued, err := before.Create(ctx, store.DefaultTenantID, "", "k")
	require.NoError(t, err)

	after := New(db.APIKeys(), testLookup(2))
	_, err = after.Authenticate(ctx, issued.Plaintext)
	require.NoError(t, err)
	row, err := db.APIKeys().Get(ctx, issued.Record.ID)
	require.NoError(t, err)
	require.Equal(t, after.lookup.KID(), row.LookupKID)

	// Restoring the previous master key works the same way.
	restored := New(db.APIKeys(), testLookup(1))
	_, err = restored.Authenticate(ctx, issued.Plaintext)
	require.NoError(t, err)
}

func TestFindByPlaintext(t *testing.T) {
	s, db := newAuthTestService(t)
	ctx := context.Background()
	issued, err := s.Create(ctx, store.DefaultTenantID, "", "k")
	require.NoError(t, err)

	got, err := s.FindByPlaintext(ctx, issued.Plaintext)
	require.NoError(t, err)
	require.Equal(t, issued.Record.ID, got.ID)

	require.NoError(t, db.APIKeys().SetDisabled(ctx, issued.Record.ID, true))
	got, err = s.FindByPlaintext(ctx, issued.Plaintext)
	require.NoError(t, err)
	require.True(t, got.Disabled, "disabled keys are still found, for status display")

	_, err = s.FindByPlaintext(ctx, "")
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = s.FindByPlaintext(ctx, "kr_unknown")
	require.ErrorIs(t, err, store.ErrNotFound)

	// A legacy row is found but left as it is.
	const plaintext = "kr_legacy"
	insertLegacyKey(t, db, "old", plaintext)
	got, err = s.FindByPlaintext(ctx, plaintext)
	require.NoError(t, err)
	require.Equal(t, "old", got.ID)
	row, err := db.APIKeys().Get(ctx, "old")
	require.NoError(t, err)
	require.Empty(t, row.LookupHMAC, "FindByPlaintext must not write")
}

// The regression this change must not introduce: keys minted before the
// upgrade keep authenticating, all of them, without being presented first.
func TestReconcileLookupsIndexesLegacyRows(t *testing.T) {
	s, db := newAuthTestService(t)
	ctx := context.Background()

	const n = reconcileBatch + 37 // spans more than one batch
	plaintexts := make([]string, n)
	for i := range n {
		plaintexts[i] = fmt.Sprintf("kr_legacy-%04d", i)
		insertLegacyKey(t, db, fmt.Sprintf("id-%04d", i), plaintexts[i])
	}

	st, err := s.ReconcileLookups(ctx)
	require.NoError(t, err)
	require.Equal(t, ReconcileStats{Indexed: n}, st)

	_, legacy := countLookups(t, 0)
	for _, p := range []string{plaintexts[0], plaintexts[reconcileBatch], plaintexts[n-1]} {
		_, err := s.Authenticate(ctx, p)
		require.NoError(t, err)
	}
	require.Zero(t, legacy.Load(), "reconciled rows authenticate by the keyed index alone")

	st, err = s.ReconcileLookups(ctx)
	require.NoError(t, err)
	require.Equal(t, ReconcileStats{}, st, "a second run changes nothing")
}

func TestReconcileLookupsSkipsUnconvertibleRows(t *testing.T) {
	s, db := newAuthTestService(t)
	ctx := context.Background()
	require.NoError(t, db.APIKeys().Create(ctx, store.APIKey{
		ID: "a-bad", TenantID: store.DefaultTenantID, Name: "bad", KeyHash: "h",
		LookupHash: "not-hex", Display: "kr_…", CreatedAt: time.Now(),
	}))
	insertLegacyKey(t, db, "b-good", "kr_good")

	st, err := s.ReconcileLookups(ctx)
	require.NoError(t, err)
	require.Equal(t, ReconcileStats{Indexed: 1, Skipped: 1}, st)

	_, err = s.Authenticate(ctx, "kr_good")
	require.NoError(t, err)

	st, err = s.ReconcileLookups(ctx)
	require.NoError(t, err)
	require.Equal(t, ReconcileStats{Skipped: 1}, st, "the bad row is skipped again without looping")
}

func TestReconcileLookupsReindexesAfterMasterKeyChange(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	before := New(db.APIKeys(), testLookup(1))
	issued, err := before.Create(ctx, store.DefaultTenantID, "", "k")
	require.NoError(t, err)
	_, err = before.Create(ctx, store.DefaultTenantID, "", "k2")
	require.NoError(t, err)

	after := New(db.APIKeys(), testLookup(2))
	st, err := after.ReconcileLookups(ctx)
	require.NoError(t, err)
	require.Equal(t, ReconcileStats{Indexed: 2, Reindexed: 2}, st)

	_, legacy := countLookups(t, 0)
	_, err = after.Authenticate(ctx, issued.Plaintext)
	require.NoError(t, err)
	require.Zero(t, legacy.Load())
}
