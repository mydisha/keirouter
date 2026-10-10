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

	t.Run("cost rollups scan into int64", func(t *testing.T) {
		now := time.Now().UTC()
		prefix := fmt.Sprintf("pg-cost-%d", now.UnixNano())
		keyID := prefix + "-key"
		require.NoError(t, db.Usage().RecordBatch(ctx, []UsageRecord{
			{ID: prefix + "-a", TenantID: prefix, APIKeyID: keyID, Provider: "p", Model: "m",
				PromptTokens: 10, CompletionTokens: 5, CostNanos: 1_234_567, CreatedAt: now},
			{ID: prefix + "-b", TenantID: prefix, APIKeyID: keyID, Provider: "p", Model: "m",
				PromptTokens: 1, CompletionTokens: 1, CostNanos: 1_000, CreatedAt: now},
		}))
		since := now.Add(-time.Minute)
		const wantMicros = int64(1_236) // (1_235_567 + 500) / 1000

		spent, err := db.Usage().SpendSince(ctx, ScopeAPIKey, keyID, since)
		require.NoError(t, err)
		require.Equal(t, wantMicros, spent)

		batch, err := db.Usage().SpendAndTokensBatch(ctx, []SpendScope{
			{Kind: ScopeAPIKey, ScopeID: keyID, Since: since},
			{Kind: ScopeTenant, ScopeID: prefix + "-empty", Since: since},
		})
		require.NoError(t, err)
		require.Equal(t, []SpendResult{{CostMicros: wantMicros, Tokens: 17}, {}}, batch)

		sum, err := db.Usage().Summarize(ctx, prefix, since)
		require.NoError(t, err)
		require.Equal(t, wantMicros, sum.CostMicros)

		byModel, err := db.Usage().ByModelByKey(ctx, keyID, since)
		require.NoError(t, err)
		require.Len(t, byModel, 1)
		require.Equal(t, wantMicros, byModel[0].CostMicros)

		daily, err := db.Usage().DailyByKey(ctx, keyID, since)
		require.NoError(t, err)
		require.Len(t, daily, 1)
		require.Equal(t, wantMicros, daily[0].CostMicros)
	})

	t.Run("time buckets truncate like SQLite", func(t *testing.T) {
		since := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
		tenant := fmt.Sprintf("pg-bucket-%d", time.Now().UnixNano())
		// 42 minutes is 70% into a one-hour slot; rounding would move it to bucket 1.
		require.NoError(t, db.Usage().Record(ctx, UsageRecord{
			ID: tenant + "-a", TenantID: tenant, Provider: "p", Model: "m",
			CreatedAt: since.Add(42 * time.Minute),
		}))
		buckets, err := db.Usage().Timeline(ctx, tenant, since, since.Add(24*time.Hour), 24)
		require.NoError(t, err)
		require.Equal(t, []TimeBucket{{Bucket: 0, Count: 1}}, buckets)

		accurate, err := db.Usage().TimelineAccurate(ctx, tenant, since, since.Add(24*time.Hour), 24)
		require.NoError(t, err)
		require.Len(t, accurate, 1)
		require.Equal(t, 0, accurate[0].Bucket)
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
