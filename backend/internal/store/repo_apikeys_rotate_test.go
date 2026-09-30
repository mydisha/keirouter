package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyRepo_SetKeyMaterial(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	require.NoError(t, db.APIKeys().Create(ctx, APIKey{
		ID: "k1", TenantID: DefaultTenantID, Name: "rot", KeyHash: "old-hash",
		LookupHash: "old-lookup", Display: "tkr_ol••••", CreatedAt: time.Now(),
	}))

	require.NoError(t, db.APIKeys().SetKeyMaterial(ctx, "k1", "new-hash", "new-lookup", "tkr_ne••••"))

	got, err := db.APIKeys().Get(ctx, "k1")
	require.NoError(t, err)
	require.Equal(t, "new-hash", got.KeyHash)
	require.Equal(t, "new-lookup", got.LookupHash)
	require.Equal(t, "tkr_ne••••", got.Display)

	err = db.APIKeys().SetKeyMaterial(ctx, "missing", "h", "l", "d")
	require.ErrorIs(t, err, ErrNotFound)
}
