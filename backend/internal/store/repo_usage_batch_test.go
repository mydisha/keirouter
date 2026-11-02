package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
)

func usageBatchFixture(n int, prefix string) []UsageRecord {
	now := time.Now().UTC().Truncate(time.Second)
	out := make([]UsageRecord, n)
	for i := range out {
		out[i] = UsageRecord{
			ID: fmt.Sprintf("%s-%d", prefix, i), TenantID: DefaultTenantID, Provider: "openai",
			Model: "mödel-🔑", Status: "success", PromptTokens: 12, CompletionTokens: 30,
			CostNanos: int64(i), CreatedAt: now.Add(-time.Duration(i) * time.Second),
		}
	}
	return out
}

func countUsageRows(t *testing.T, db *DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.sql.QueryRow(`SELECT COUNT(*) FROM usage_records`).Scan(&n))
	return n
}

func TestRecordBatchInsertsEveryRow(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	require.NoError(t, db.Usage().RecordBatch(ctx, nil))
	require.NoError(t, db.Usage().RecordBatch(ctx, usageBatchFixture(1, "one")))
	// Larger than the old multi-row chunk, so a chunking bug would show.
	require.NoError(t, db.Usage().RecordBatch(ctx, usageBatchFixture(1200, "many")))
	require.Equal(t, 1201, countUsageRows(t, db))

	var model string
	var cost int64
	require.NoError(t, db.sql.QueryRow(`SELECT model, cost_nanos FROM usage_records WHERE id = 'many-1199'`).Scan(&model, &cost))
	require.Equal(t, "mödel-🔑", model)
	require.EqualValues(t, 1199, cost)
}

func TestRecordBatchIsAllOrNothing(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	require.NoError(t, db.Usage().RecordBatch(ctx, usageBatchFixture(1, "dup")))

	batch := usageBatchFixture(50, "fresh")
	batch[40].ID = "dup-0" // primary-key clash late in the batch
	require.Error(t, db.Usage().RecordBatch(ctx, batch))
	require.Equal(t, 1, countUsageRows(t, db), "rows before the failing one must be rolled back")
}

func TestRecordBatchCanceledContextWritesNothing(t *testing.T) {
	db := newTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, db.Usage().RecordBatch(ctx, usageBatchFixture(10, "c")))
	require.Zero(t, countUsageRows(t, db))
}

func BenchmarkRecordBatch100(b *testing.B) {
	ctx := context.Background()
	db, err := Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		i++
		if err := db.Usage().RecordBatch(ctx, usageBatchFixture(100, fmt.Sprintf("b%d", i))); err != nil {
			b.Fatal(err)
		}
	}
}

func TestListUnpricedModelsMatchesListUnpricedFilter(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	rows := []UsageRecord{
		{ID: "1", TenantID: DefaultTenantID, Provider: "lm-studio", Model: "qwën", Status: "success", PromptTokens: 5, PricingStatus: "missing", CreatedAt: now},
		{ID: "2", TenantID: DefaultTenantID, Provider: "lm-studio", Model: "qwën", Status: "success", PromptTokens: 7, PricingStatus: "missing", CreatedAt: now},
		{ID: "3", TenantID: DefaultTenantID, Provider: "openai", Model: "old", Status: "success", CompletionTokens: 3, PricingStatus: "legacy", CreatedAt: now},
		{ID: "4", TenantID: DefaultTenantID, Provider: "openai", Model: "priced", Status: "success", PromptTokens: 3, CostNanos: 9, PricingStatus: "priced", CreatedAt: now},
		{ID: "5", TenantID: DefaultTenantID, Provider: "openai", Model: "empty", Status: "failed", PricingStatus: "missing", CreatedAt: now},
	}
	require.NoError(t, db.Usage().RecordBatch(ctx, rows))

	models, err := db.Usage().ListUnpricedModels(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []UnpricedModel{{Provider: "lm-studio", Model: "qwën"}, {Provider: "openai", Model: "old"}}, models)

	listed, err := db.Usage().ListUnpriced(ctx, time.Time{}, "", 100)
	require.NoError(t, err)
	seen := map[UnpricedModel]bool{}
	for _, r := range listed {
		seen[UnpricedModel{Provider: r.Provider, Model: r.Model}] = true
	}
	require.Len(t, seen, len(models), "both queries must select the same rows")
}

func TestRedundantUsageIndexesAreDropped(t *testing.T) {
	db := newTestDB(t)
	q := `SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='usage_records'`
	if db.dialect == DialectPostgres {
		q = `SELECT indexname FROM pg_indexes WHERE tablename='usage_records' AND schemaname=current_schema()`
	}
	var names []string
	rows, err := db.sql.Query(q)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		names = append(names, n)
	}
	require.NotContains(t, names, "idx_usage_tenant_time")
	require.NotContains(t, names, "idx_usage_tenant_time_desc")
	require.NotContains(t, names, "idx_usage_tenant_time_provider")
	// The wider indexes that serve the same range scans stay.
	require.Contains(t, names, "idx_usage_tenant_time_provider_model")
	require.Contains(t, names, "idx_usage_tenant_time_status")
}
