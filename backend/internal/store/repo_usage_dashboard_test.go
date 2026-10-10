package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSummarizeAccurateRangeExcludesUpperBound(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	records := []UsageRecord{
		{ID: "prev-1", TenantID: DefaultTenantID, Provider: "openai", Model: "m", Status: "success", CostNanos: 10, CreatedAt: now.Add(-90 * time.Minute)},
		{ID: "prev-2", TenantID: DefaultTenantID, Provider: "openai", Model: "m", Status: "failed", CreatedAt: now.Add(-70 * time.Minute)},
		{ID: "cur-1", TenantID: DefaultTenantID, Provider: "openai", Model: "m", Status: "success", CostNanos: 5, CreatedAt: now.Add(-60 * time.Minute)},
	}
	require.NoError(t, db.Usage().RecordBatch(ctx, records))

	prev, err := db.Usage().SummarizeAccurateRange(ctx, DefaultTenantID, now.Add(-2*time.Hour), now.Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(2), prev.TotalRequests)
	require.Equal(t, int64(1), prev.SuccessCount)
	require.Equal(t, int64(10), prev.CostNanos)

	open, err := db.Usage().SummarizeAccurateRange(ctx, DefaultTenantID, now.Add(-2*time.Hour), time.Time{})
	require.NoError(t, err)
	require.Equal(t, int64(3), open.TotalRequests)
}

func TestLatencyPercentilesUsesSuccessfulRequestsOnly(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	var records []UsageRecord
	for i := 1; i <= 100; i++ {
		records = append(records, UsageRecord{
			ID: fmt.Sprintf("ok-%d", i), TenantID: DefaultTenantID, Provider: "openai", Model: "m",
			Status: "success", EndToEndLatencyMS: i * 10, CreatedAt: now,
		})
	}
	// A slow failure must not drag the percentiles up.
	records = append(records, UsageRecord{
		ID: "failed", TenantID: DefaultTenantID, Provider: "openai", Model: "m",
		Status: "failed", EndToEndLatencyMS: 999_999, CreatedAt: now,
	})
	require.NoError(t, db.Usage().RecordBatch(ctx, records))

	p50, p95, err := db.Usage().LatencyPercentiles(ctx, DefaultTenantID, now.Add(-time.Hour), time.Time{})
	require.NoError(t, err)
	require.Equal(t, int64(500), p50)
	require.Equal(t, int64(950), p95)

	p50, p95, err = db.Usage().LatencyPercentiles(ctx, DefaultTenantID, now.Add(time.Hour), time.Time{})
	require.NoError(t, err)
	require.Zero(t, p50)
	require.Zero(t, p95)
}

func TestRecentAccurateIncludesKeyAccountAndClient(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	require.NoError(t, db.Usage().RecordBatch(ctx, []UsageRecord{{
		ID: "r1", TenantID: DefaultTenantID, APIKeyID: "key-1", AccountID: "acc-1", Client: "claude-code",
		Provider: "anthropic", Model: "m", Status: "success", CreatedAt: now,
	}, {
		ID: "r2", TenantID: DefaultTenantID, Provider: "anthropic", Model: "m", Status: "success", CreatedAt: now.Add(-time.Second),
	}}))

	recent, err := db.Usage().RecentAccurate(ctx, DefaultTenantID, now.Add(-time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, recent, 2)
	require.Equal(t, "key-1", recent[0].APIKeyID)
	require.Equal(t, "acc-1", recent[0].AccountID)
	require.Equal(t, "claude-code", recent[0].Client)
	require.Empty(t, recent[1].APIKeyID)
}

func TestRollupSnapshotsGroupsByProviderAndBucket(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	since := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour)
	p95 := func(v int) *int { return &v }
	snaps := []ProviderHealthSnapshot{
		{ID: "a1", BucketStart: since.Add(5 * time.Minute), BucketSizeSeconds: 60, Provider: "gemini", Model: "x",
			RequestCount: 10, SuccessCount: 9, FailureCount: 1, RateLimitedCount: 1, LatencyP95Ms: p95(800), HealthStatus: "degraded"},
		{ID: "a2", BucketStart: since.Add(10 * time.Minute), BucketSizeSeconds: 60, Provider: "gemini", Model: "y",
			RequestCount: 5, SuccessCount: 5, LatencyP95Ms: p95(1200), HealthStatus: "healthy"},
		{ID: "b1", BucketStart: since.Add(70 * time.Minute), BucketSizeSeconds: 60, Provider: "openai", Model: "z",
			RequestCount: 3, SuccessCount: 3, HealthStatus: "healthy"},
	}
	for _, s := range snaps {
		s.CreatedAt = time.Now().UTC()
		require.NoError(t, db.ProviderHealth().InsertSnapshot(ctx, s))
	}

	rows, err := db.ProviderHealth().RollupSnapshots(ctx, since, 3600)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	byProvider := map[string]ProviderHealthRollup{}
	for _, r := range rows {
		byProvider[r.Provider] = r
	}
	g := byProvider["gemini"]
	require.Equal(t, 0, g.Bucket)
	require.Equal(t, int64(15), g.Requests)
	require.Equal(t, int64(1), g.RateLimited)
	require.Equal(t, int64(1200), g.LatencyP95Ms)
	require.Equal(t, 2, g.WorstStatusRnk)
	require.Equal(t, 1, byProvider["openai"].Bucket)
}

func TestChainUsageGroupsByServingTarget(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	rec := func(id, chain, provider, model, status string, fallbacks int) UsageRecord {
		return UsageRecord{ID: id, TenantID: DefaultTenantID, ChainID: chain, FallbackCount: fallbacks,
			Provider: provider, Model: model, Status: status, CostNanos: 100, EndToEndLatencyMS: 200, CreatedAt: now}
	}
	require.NoError(t, db.Usage().RecordBatch(ctx, []UsageRecord{
		rec("a", "c1", "anthropic", "sonnet", "success", 0),
		rec("b", "c1", "anthropic", "sonnet", "success", 0),
		rec("c", "c1", "codex", "gpt-5", "success", 1),
		rec("d", "c1", "codex", "gpt-5", "failed", 1),
		rec("e", "", "openai", "gpt-4o", "success", 0), // direct request: excluded
	}))

	rows, err := db.Usage().ChainUsage(ctx, DefaultTenantID, now.Add(-time.Hour))
	require.NoError(t, err)
	require.Len(t, rows, 2)
	by := map[string]ChainServedUsage{}
	for _, r := range rows {
		by[r.Provider] = r
	}
	require.Equal(t, int64(2), by["anthropic"].Requests)
	require.Equal(t, int64(0), by["anthropic"].FellBack)
	require.Equal(t, int64(2), by["codex"].Requests)
	require.Equal(t, int64(1), by["codex"].Successes)
	require.Equal(t, int64(2), by["codex"].FellBack)
}
