package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func legacyKey(id, lookup string) APIKey {
	return APIKey{
		ID: id, TenantID: DefaultTenantID, Name: id,
		KeyHash: "$argon2id$legacy", LookupHash: lookup,
		Display: "kr_…", CreatedAt: time.Now(),
	}
}

func TestAPIKeyRepo_LegacyRowsHaveNoKeyedIndex(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := db.APIKeys()

	// Several rows without a keyed index must coexist under the unique index.
	require.NoError(t, repo.Create(ctx, legacyKey("a", "sha-a")))
	require.NoError(t, repo.Create(ctx, legacyKey("b", "sha-b")))

	got, err := repo.FindByLookup(ctx, "sha-a")
	require.NoError(t, err)
	require.Empty(t, got.LookupHMAC)
	require.Empty(t, got.LookupKID)

	_, err = repo.FindByLookupHMAC(ctx, "")
	require.ErrorIs(t, err, ErrNotFound, "an empty index must never match a legacy row")
}

func TestAPIKeyRepo_SetLookupHMAC(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := db.APIKeys()
	require.NoError(t, repo.Create(ctx, legacyKey("a", "sha-a")))

	changed, err := repo.SetLookupHMAC(ctx, LookupIndexUpdate{ID: "a", HMAC: "hmac-a", KID: "kid1"})
	require.NoError(t, err)
	require.True(t, changed)

	changed, err = repo.SetLookupHMAC(ctx, LookupIndexUpdate{ID: "a", HMAC: "hmac-a", KID: "kid1"})
	require.NoError(t, err)
	require.False(t, changed, "writing the same value again is a no-op")

	got, err := repo.FindByLookupHMAC(ctx, "hmac-a")
	require.NoError(t, err)
	require.Equal(t, "a", got.ID)
	require.Equal(t, "kid1", got.LookupKID)
	require.Equal(t, "sha-a", got.LookupHash, "the legacy index is kept for rollback")

	got, err = repo.FindByLookup(ctx, "sha-a")
	require.NoError(t, err)
	require.Equal(t, "hmac-a", got.LookupHMAC)

	// A new lookup key re-indexes the row.
	changed, err = repo.SetLookupHMAC(ctx, LookupIndexUpdate{ID: "a", HMAC: "hmac-a2", KID: "kid2"})
	require.NoError(t, err)
	require.True(t, changed)
	_, err = repo.FindByLookupHMAC(ctx, "hmac-a")
	require.ErrorIs(t, err, ErrNotFound)

	changed, err = repo.SetLookupHMAC(ctx, LookupIndexUpdate{ID: "missing", HMAC: "x", KID: "kid2"})
	require.NoError(t, err)
	require.False(t, changed)
}

func TestAPIKeyRepo_KeyedIndexIsUnique(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := db.APIKeys()

	first := legacyKey("a", "sha-a")
	first.LookupHMAC, first.LookupKID = "dup", "kid1"
	require.NoError(t, repo.Create(ctx, first))

	second := legacyKey("b", "sha-b")
	second.LookupHMAC, second.LookupKID = "dup", "kid1"
	require.Error(t, repo.Create(ctx, second))

	require.NoError(t, repo.Create(ctx, legacyKey("c", "sha-c")))
	_, err := repo.SetLookupHMAC(ctx, LookupIndexUpdate{ID: "c", HMAC: "dup", KID: "kid1"})
	require.Error(t, err)
}

func TestAPIKeyRepo_ListPendingLookups(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := db.APIKeys()

	for i := range 5 {
		require.NoError(t, repo.Create(ctx, legacyKey(fmt.Sprintf("k%d", i), fmt.Sprintf("sha-%d", i))))
	}
	_, err := repo.SetLookupHMAC(ctx, LookupIndexUpdate{ID: "k1", HMAC: "h1", KID: "cur"})
	require.NoError(t, err)
	_, err = repo.SetLookupHMAC(ctx, LookupIndexUpdate{ID: "k2", HMAC: "h2", KID: "old"})
	require.NoError(t, err)

	page, err := repo.ListPendingLookups(ctx, "cur", "", 2)
	require.NoError(t, err)
	require.Equal(t, []PendingLookup{
		{ID: "k0", LookupHash: "sha-0"},
		{ID: "k2", LookupHash: "sha-2", LookupHMAC: "h2", LookupKID: "old"},
	}, page, "k1 is already under the current kid")

	page, err = repo.ListPendingLookups(ctx, "cur", "k2", 2)
	require.NoError(t, err)
	require.Len(t, page, 2)
	require.Equal(t, "k3", page[0].ID)
	require.Equal(t, "k4", page[1].ID)

	page, err = repo.ListPendingLookups(ctx, "cur", "k4", 2)
	require.NoError(t, err)
	require.Empty(t, page)
}

func TestAPIKeyRepo_SetLookupHMACs(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := db.APIKeys()
	require.NoError(t, repo.Create(ctx, legacyKey("a", "sha-a")))
	require.NoError(t, repo.Create(ctx, legacyKey("b", "sha-b")))

	n, err := repo.SetLookupHMACs(ctx, nil)
	require.NoError(t, err)
	require.Zero(t, n)

	n, err = repo.SetLookupHMACs(ctx, []LookupIndexUpdate{
		{ID: "a", HMAC: "ha", KID: "k"},
		{ID: "b", HMAC: "hb", KID: "k"},
		{ID: "missing", HMAC: "hm", KID: "k"},
	})
	require.NoError(t, err)
	require.Equal(t, 2, n)

	// A failing update rolls the whole batch back.
	require.NoError(t, repo.Create(ctx, legacyKey("c", "sha-c")))
	require.NoError(t, repo.Create(ctx, legacyKey("d", "sha-d")))
	_, err = repo.SetLookupHMACs(ctx, []LookupIndexUpdate{
		{ID: "c", HMAC: "hc", KID: "k"},
		{ID: "d", HMAC: "ha", KID: "k"}, // collides with a
	})
	require.Error(t, err)
	got, err := repo.Get(ctx, "c")
	require.NoError(t, err)
	require.Empty(t, got.LookupHMAC, "the batch must be atomic")
}

func TestAPIKeyRepo_SetLookupHMACConcurrent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := db.APIKeys()
	require.NoError(t, repo.Create(ctx, legacyKey("a", "sha-a")))

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := repo.SetLookupHMAC(ctx, LookupIndexUpdate{ID: "a", HMAC: "ha", KID: "k"})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	got, err := repo.FindByLookupHMAC(ctx, "ha")
	require.NoError(t, err)
	require.Equal(t, "a", got.ID)
}
