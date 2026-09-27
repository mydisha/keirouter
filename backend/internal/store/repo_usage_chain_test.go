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

func TestChainUsageAccurateGroupsAndSkipsEmpty(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	require.NoError(t, db.Usage().RecordBatch(ctx, []UsageRecord{
		{ID: "a", TenantID: DefaultTenantID, APIKeyID: "k1", Provider: "openai", Model: "gpt-4o", ChainID: "c1", Status: "success", PromptTokens: 100, CompletionTokens: 20, CreatedAt: now},
		{ID: "b", TenantID: DefaultTenantID, APIKeyID: "k2", Provider: "openai", Model: "gpt-4o", ChainID: "c1", Status: "success", PromptTokens: 100, CompletionTokens: 20, CreatedAt: now},
		{ID: "d", TenantID: DefaultTenantID, APIKeyID: "k1", Provider: "openai", Model: "gpt-4o", ChainID: "", Status: "success", PromptTokens: 5, CompletionTokens: 5, CreatedAt: now},
	}))

	got, err := db.Usage().ChainUsageAccurate(ctx, DefaultTenantID, time.Time{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	c1 := got["c1"]
	require.Equal(t, int64(2), c1.TotalRequests)
	require.Equal(t, int64(200), c1.PromptTokens)
	require.Equal(t, int64(40), c1.CompletionTokens)
	require.Equal(t, 2, c1.DistinctKeys)
}
