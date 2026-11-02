package identity

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func newAuthTestService(t *testing.T) (*Service, *store.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })
	return New(db.APIKeys()), db
}

// countVerifies swaps the argon2 verifier for one that counts calls and tracks
// how many run at once. hold keeps each call busy so overlaps are observable.
func countVerifies(t *testing.T, hold time.Duration) (calls, maxInFlight *atomic.Int64) {
	t.Helper()
	calls, maxInFlight = new(atomic.Int64), new(atomic.Int64)
	var inFlight atomic.Int64
	orig := verifyAPIKey
	verifyAPIKey = func(plaintext, hash string) (bool, error) {
		calls.Add(1)
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(hold)
		return orig(plaintext, hash)
	}
	t.Cleanup(func() { verifyAPIKey = orig })
	return calls, maxInFlight
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

func TestAuthenticateConcurrentMissesVerifyOnce(t *testing.T) {
	s, _ := newAuthTestService(t)
	ctx := context.Background()
	issued, err := s.Create(ctx, store.DefaultTenantID, "", "busy")
	require.NoError(t, err)
	calls, _ := countVerifies(t, 20*time.Millisecond)

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
	require.EqualValues(t, 1, calls.Load(), "50 concurrent first requests must share one argon2 verification")
}

func TestAuthenticateExpiredEntryRevalidatesWithoutArgon2(t *testing.T) {
	s, _ := newAuthTestService(t)
	ctx := context.Background()
	issued, err := s.Create(ctx, store.DefaultTenantID, "", "steady")
	require.NoError(t, err)
	calls, _ := countVerifies(t, 0)

	_, err = s.Authenticate(ctx, issued.Plaintext)
	require.NoError(t, err)
	for range 3 {
		expireAuthCache(s)
		_, err = s.Authenticate(ctx, issued.Plaintext)
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, calls.Load(), "an unchanged row must not be re-verified with argon2")
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

	// Re-enabling must not reuse a revoked entry: it was dropped, so the key
	// verifies again.
	calls, _ := countVerifies(t, 0)
	require.NoError(t, db.APIKeys().SetDisabled(ctx, issued.Record.ID, false))
	_, err = s.Authenticate(ctx, issued.Plaintext)
	require.NoError(t, err)
	require.EqualValues(t, 1, calls.Load())
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

func TestAuthenticateUnknownKeySkipsArgon2(t *testing.T) {
	s, _ := newAuthTestService(t)
	calls, _ := countVerifies(t, 0)
	for _, k := range []string{"kr_not-a-real-key", "kr_🔑ünïcode", "x"} {
		_, err := s.Authenticate(context.Background(), k)
		require.ErrorIs(t, err, ErrUnauthorized)
	}
	_, err := s.Authenticate(context.Background(), "")
	require.ErrorIs(t, err, ErrUnauthorized)
	require.Zero(t, calls.Load(), "keys with no matching row must be rejected before argon2")
}

func TestAuthenticateBoundsConcurrentArgon2AcrossKeys(t *testing.T) {
	s, _ := newAuthTestService(t)
	ctx := context.Background()
	var keys []string
	for range 8 {
		issued, err := s.Create(ctx, store.DefaultTenantID, "", "k")
		require.NoError(t, err)
		keys = append(keys, issued.Plaintext)
	}
	calls, maxInFlight := countVerifies(t, 30*time.Millisecond)

	var wg sync.WaitGroup
	for _, k := range keys {
		wg.Go(func() {
			_, err := s.Authenticate(ctx, k)
			require.NoError(t, err)
		})
	}
	wg.Wait()
	require.EqualValues(t, len(keys), calls.Load())
	require.LessOrEqual(t, maxInFlight.Load(), int64(maxConcurrentVerify))
}

func TestAuthenticateWaiterCancelDoesNotFailOthers(t *testing.T) {
	s, _ := newAuthTestService(t)
	issued, err := s.Create(context.Background(), store.DefaultTenantID, "", "shared")
	require.NoError(t, err)
	countVerifies(t, 50*time.Millisecond)

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
	require.NoError(t, <-second, "the shared verification must survive the first caller hanging up")
}
