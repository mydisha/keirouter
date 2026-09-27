package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUsageRecordPersistsChainID(t *testing.T) {
	db := newTestDB(t) // existing helper in store_test.go
	ctx := context.Background()

	require.NoError(t, db.Usage().Record(ctx, UsageRecord{
		ID: "u1", TenantID: DefaultTenantID, Provider: "openai", Model: "gpt-4o",
		ChainID: "chain-1", Status: "success", CreatedAt: time.Now().UTC(),
	}))

	var got string
	q := db.rebind(`SELECT chain_id FROM usage_records WHERE id = ?`)
	require.NoError(t, db.sql.QueryRowContext(ctx, q, "u1").Scan(&got))
	require.Equal(t, "chain-1", got)
}
