package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPortalUserRepo_BindAndGet(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	key := APIKey{
		ID: "key-1", TenantID: DefaultTenantID, Name: "k",
		KeyHash: "h", LookupHash: "l", Display: "d", CreatedAt: time.Now(),
	}
	require.NoError(t, db.APIKeys().Create(ctx, key))

	pu := PortalUser{GoogleSub: "sub-1", Email: "a@example.com", KeyID: "key-1",
		CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, db.PortalUsers().Upsert(ctx, pu))

	got, err := db.PortalUsers().GetBySub(ctx, "sub-1")
	require.NoError(t, err)
	require.Equal(t, "key-1", got.KeyID)
	require.Equal(t, "a@example.com", got.Email)

	byKey, err := db.PortalUsers().GetByKey(ctx, "key-1")
	require.NoError(t, err)
	require.Equal(t, "sub-1", byKey.GoogleSub)

	// A second sub cannot claim the same key.
	dup := PortalUser{GoogleSub: "sub-2", Email: "b@example.com", KeyID: "key-1",
		CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.ErrorIs(t, db.PortalUsers().Upsert(ctx, dup), ErrAlreadyExists)

	// Delete revokes the binding.
	require.NoError(t, db.PortalUsers().Delete(ctx, "sub-1"))
	_, err = db.PortalUsers().GetBySub(ctx, "sub-1")
	require.ErrorIs(t, err, ErrNotFound)
}
