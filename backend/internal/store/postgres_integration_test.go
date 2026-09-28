package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
)

// TestPostgresCompatibility exercises behavior that differs materially from
// SQLite. It runs only when CI or a developer provides a disposable test DSN.
func TestPostgresCompatibility(t *testing.T) {
	dsn := os.Getenv("KEIROUTER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("KEIROUTER_TEST_POSTGRES_DSN is not set")
	}

	ctx := context.Background()
	db, err := Open(ctx, config.DatabaseConfig{
		Driver:          "postgres",
		DSN:             dsn,
		MaxOpenConns:    8,
		MaxIdleConns:    4,
		ConnMaxLifetime: time.Minute,
		ConnMaxIdleTime: time.Minute,
	}, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Migrate(ctx), "migrations must be idempotent")
	require.NoError(t, db.Tenants().EnsureDefault(ctx))

	t.Run("resource samples accept production-sized counters", func(t *testing.T) {
		now := time.Now().UTC()
		require.NoError(t, db.Resources().InsertResourceSample(ctx, ResourceSample{
			TenantID: DefaultTenantID, CreatedAt: now,
			Goroutines: 128, HeapAllocBytes: 4 << 30, HeapSysBytes: 6 << 30,
			GCPauseNS: 3_000_000_000, NextGCBytes: 8 << 30, NumGC: 3_000_000_000,
			ProcCPUPercent: 12.5, ProcRSSBytes: 5 << 30, ProcThreads: 32,
			HostCPUPercent: 45.5, HostMemUsedBytes: 12 << 30, HostMemTotalBytes: 16 << 30,
			HostDiskUsedBytes: 2 << 40, HostDiskTotalBytes: 4 << 40,
			HostNetSentBytes: 6 << 30, HostNetRecvBytes: 7 << 30,
			InflightRequests: 24,
		}))
	})

	t.Run("large usage batches stay below bind limits", func(t *testing.T) {
		now := time.Now().UTC()
		records := make([]UsageRecord, 2500)
		prefix := fmt.Sprintf("pg-it-%d", now.UnixNano())
		for i := range records {
			records[i] = UsageRecord{
				ID: fmt.Sprintf("%s-%d", prefix, i), TenantID: DefaultTenantID,
				Provider: "integration", Model: "postgres", Client: "test", CreatedAt: now,
			}
		}
		require.NoError(t, db.Usage().RecordBatch(ctx, records))
		var count int
		require.NoError(t, db.sql.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM usage_records WHERE id LIKE $1", prefix+"-%").Scan(&count))
		require.Equal(t, len(records), count)
	})

	t.Run("chain_id persists and aggregates per chain", func(t *testing.T) {
		now := time.Now().UTC()
		prefix := fmt.Sprintf("pg-chain-%d", now.UnixNano())
		require.NoError(t, db.Usage().RecordBatch(ctx, []UsageRecord{
			{ID: prefix + "-a", TenantID: DefaultTenantID, APIKeyID: "k1", Provider: "openai", Model: "gpt-4o", ChainID: "c1", Status: "success", PromptTokens: 100, CompletionTokens: 20, CreatedAt: now},
			{ID: prefix + "-b", TenantID: DefaultTenantID, APIKeyID: "k2", Provider: "openai", Model: "gpt-4o", ChainID: "c1", Status: "success", PromptTokens: 100, CompletionTokens: 20, CreatedAt: now},
			{ID: prefix + "-c", TenantID: DefaultTenantID, APIKeyID: "k1", Provider: "openai", Model: "gpt-4o", ChainID: "", Status: "success", PromptTokens: 5, CompletionTokens: 5, CreatedAt: now},
		}))

		got, err := db.Usage().ChainUsageAccurate(ctx, DefaultTenantID, time.Time{})
		require.NoError(t, err)
		c1 := got["c1"]
		require.Equal(t, int64(2), c1.TotalRequests)
		require.Equal(t, int64(200), c1.PromptTokens)
		require.Equal(t, int64(40), c1.CompletionTokens)
		require.Equal(t, 2, c1.DistinctKeys)

		// Empty chain_id (direct target) must be excluded from the aggregate.
		var empty string
		q := db.rebind("SELECT chain_id FROM usage_records WHERE id = ?")
		require.NoError(t, db.sql.QueryRowContext(ctx, q, prefix+"-c").Scan(&empty))
		require.Equal(t, "", empty)
	})

	t.Run("chain pricing round-trips", func(t *testing.T) {
		now := time.Now().UTC()
		c := Chain{
			ID: fmt.Sprintf("pg-price-%d", now.UnixNano()), TenantID: DefaultTenantID,
			Name: "priced", Strategy: "priority",
			InputPerM: 2.5, OutputPerM: 10, CacheWritePerM: 3.125, CacheReadPerM: 0.25,
			CreatedAt: now, UpdatedAt: now,
		}
		c.Steps = []ChainStep{{
			ID: c.ID + "-s1", ChainID: c.ID, Position: 0, Provider: "openai", Model: "gpt-4o", CreatedAt: now,
		}}
		require.NoError(t, db.Chains().Create(ctx, c))

		got, err := db.Chains().Get(ctx, c.ID)
		require.NoError(t, err)
		require.Equal(t, 2.5, got.InputPerM)
		require.Equal(t, 10.0, got.OutputPerM)
		require.Equal(t, 3.125, got.CacheWritePerM)
		require.Equal(t, 0.25, got.CacheReadPerM)

		c.CacheReadPerM = 0.5
		require.NoError(t, db.Chains().Update(ctx, c))
		again, err := db.Chains().Get(ctx, c.ID)
		require.NoError(t, err)
		require.Equal(t, 0.5, again.CacheReadPerM)
	})

	t.Run("cost aggregates scan into int64", func(t *testing.T) {
		now := time.Now().UTC()
		prefix := fmt.Sprintf("pg-cost-%d", now.UnixNano())
		require.NoError(t, db.Usage().RecordBatch(ctx, []UsageRecord{
			{ID: prefix, TenantID: DefaultTenantID, APIKeyID: prefix, Provider: "openai",
				Model: "gpt-4o", Status: "success", PromptTokens: 10, CompletionTokens: 5,
				CostNanos: 1500, PricingStatus: "priced", CreatedAt: now},
		}))

		// 1500 nanos + 500 rounds to 2 micros; Postgres returns the SUM(...)/1000
		// division as numeric, which must be cast back to BIGINT before scanning.
		micros, tokens, err := db.Usage().SpendAndTokens(ctx, ScopeAPIKey, prefix, time.Time{})
		require.NoError(t, err)
		require.Equal(t, int64(2), micros)
		require.Equal(t, int64(15), tokens)

		sum, err := db.Usage().SummarizeByKey(ctx, prefix, time.Time{})
		require.NoError(t, err)
		require.Equal(t, int64(2), sum.CostMicros)

		acc, err := db.Usage().SummarizeAccurate(ctx, DefaultTenantID, time.Time{})
		require.NoError(t, err)
		require.GreaterOrEqual(t, acc.CostNanos, int64(1500))
	})

	t.Run("calendar grouping is UTC", func(t *testing.T) {
		_, err := db.sql.ExecContext(ctx, "SET TIME ZONE 'America/Los_Angeles'")
		require.NoError(t, err)
		q := db.rebind("SELECT " + db.dateExpr("?"))
		var day string
		require.NoError(t, db.sql.QueryRowContext(ctx, q, "2026-01-01T00:30:00Z").Scan(&day))
		require.Equal(t, "2026-01-01", day)
	})

	t.Run("duplicate column recovery restores transaction", func(t *testing.T) {
		_, err := db.sql.ExecContext(ctx,
			"DELETE FROM schema_migrations WHERE version = '0022_headroom_ponytail_savings'")
		require.NoError(t, err)
		require.NoError(t, db.Migrate(ctx))
	})
}
