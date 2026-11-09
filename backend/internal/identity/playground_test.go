package identity

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestPlayground_CreatesOnceAndReuses(t *testing.T) {
	s, _ := newAuthTestService(t)
	ctx := context.Background()

	first, err := s.Playground(ctx, store.DefaultTenantID)
	require.NoError(t, err)
	require.Equal(t, store.KeyKindPlayground, first.Kind)
	require.Equal(t, PlaygroundKeyName, first.Name)

	again, err := s.Playground(ctx, store.DefaultTenantID)
	require.NoError(t, err)
	require.Equal(t, first.ID, again.ID)
}

func TestPlayground_ConcurrentFirstUseMakesOneKey(t *testing.T) {
	s, db := newAuthTestService(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	ids := make([]string, 8)
	errs := make([]error, 8)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k, err := s.Playground(ctx, store.DefaultTenantID)
			ids[i], errs[i] = k.ID, err
		}(i)
	}
	wg.Wait()
	for i := range ids {
		require.NoError(t, errs[i])
		require.Equal(t, ids[0], ids[i], "every caller gets the same key")
	}
	keys, err := db.APIKeys().List(ctx, store.DefaultTenantID)
	require.NoError(t, err)
	require.Len(t, keys, 1)
}

// A managed key never authenticates by secret, even if its plaintext leaked
// (the playground discards it; this test keeps one to prove the refusal).
func TestAuthenticate_RefusesManagedKey(t *testing.T) {
	s, db := newAuthTestService(t)
	ctx := context.Background()
	issued, err := s.Generate(store.DefaultTenantID, "", "managed")
	require.NoError(t, err)
	issued.Record.Kind = store.KeyKindPlayground
	require.NoError(t, db.APIKeys().Create(ctx, issued.Record))

	_, err = s.Authenticate(ctx, issued.Plaintext)
	require.ErrorIs(t, err, ErrUnauthorized)

	// An ordinary key still authenticates.
	ok, err := s.Create(ctx, store.DefaultTenantID, "", "ordinary")
	require.NoError(t, err)
	rec, err := s.Authenticate(ctx, ok.Plaintext)
	require.NoError(t, err)
	require.Equal(t, ok.Record.ID, rec.ID)
	require.Empty(t, rec.Kind)
}
