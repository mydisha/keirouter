package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func finRec(id, provider, chain string, cost, upstream int64, backfilled bool, at time.Time) UsageRecord {
	return UsageRecord{ID: id, TenantID: DefaultTenantID, APIKeyID: "k-" + id, Provider: provider, ChainID: chain,
		Model: "m", Status: "success", CostNanos: cost, UpstreamCostNanos: upstream,
		PricingStatus: "priced", PricingSource: "official", PricingBackfilled: backfilled, CreatedAt: at}
}

func TestCostBasisGroupsByProviderChain(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	since := now.Add(-time.Hour)
	require.NoError(t, db.Usage().RecordBatch(ctx, []UsageRecord{
		finRec("a", "p1", "c1", 15e9, 10e9, false, now),
		finRec("b", "p1", "c1", 3e9, 0, false, now),
		finRec("c", "p1", "c1", 7e9, 5e9, true, now),
		finRec("d", "p1", "c1", 9e9, 9e9, false, now.Add(-2*time.Hour)),
	}))
	rows, err := db.Usage().CostBasis(ctx, DefaultTenantID, since)
	require.NoError(t, err)
	require.Equal(t, []CostBasisRow{{Provider: "p1", ChainID: "c1", Requests: 2,
		BilledNanos: 18e9, UpstreamNanos: 10e9, UnknownBilledNanos: 3e9}}, rows)

	byProv, err := db.Usage().CostBasisByProviderSince(ctx, DefaultTenantID, map[string]time.Time{
		"p1": since, "p2": now.Add(-3 * time.Hour),
	})
	require.NoError(t, err)
	require.Len(t, byProv, 1)
	require.Equal(t, int64(2), byProv[0].Requests)

	byProv, err = db.Usage().CostBasisByProviderSince(ctx, DefaultTenantID, map[string]time.Time{
		"p1": now.Add(-3 * time.Hour),
	})
	require.NoError(t, err)
	require.Len(t, byProv, 1)
	require.Equal(t, int64(3), byProv[0].Requests)
}

func TestUsageDistinctProviders(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	got, err := db.Usage().DistinctProviders(ctx, DefaultTenantID)
	require.NoError(t, err)
	require.Empty(t, got)
	other := finRec("x", "p9", "c1", 1, 1, false, now)
	other.TenantID = "other"
	require.NoError(t, db.Usage().RecordBatch(ctx, []UsageRecord{
		finRec("a", "p2", "c1", 1, 1, false, now),
		finRec("b", "p1", "c1", 1, 1, false, now.Add(-400*24*time.Hour)),
		finRec("c", "p2", "c2", 1, 1, false, now),
		finRec("d", "", "c2", 1, 1, false, now),
		other,
	}))
	got, err = db.Usage().DistinctProviders(ctx, DefaultTenantID)
	require.NoError(t, err)
	require.Equal(t, []string{"p1", "p2"}, got)
}

func TestPaidTotalsSince(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	mk := func(id string, st PaymentOrderStatus, paidAt string, created time.Time) {
		o := sampleOrder(id)
		o.Status, o.PaidAt, o.CreatedAt, o.UpdatedAt = st, paidAt, created, created
		require.NoError(t, db.PaymentOrders().Create(ctx, o))
	}
	old := now.Add(-48 * time.Hour)
	mk("o1", PaymentCompleted, formatTime(now), old)
	mk("o2", PaymentManual, "", now)
	mk("o3", PaymentPending, "", now)
	mk("o4", PaymentCompleted, formatTime(old), old)
	got, err := db.PaymentOrders().PaidTotalsSince(ctx, now.Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, PaidOrderTotals{Count: 2, AmountIDR: 100000, CreditMicros: 6_200_000}, got)
}

func TestOutstandingPortalCredit(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, id := range []string{"ka", "kb"} {
		require.NoError(t, db.APIKeys().Create(ctx, APIKey{ID: id, TenantID: DefaultTenantID, Name: id,
			KeyHash: "h" + id, LookupHash: "l" + id, Display: "d", CreatedAt: now}))
		require.NoError(t, db.PortalUsers().Upsert(ctx, PortalUser{GoogleSub: "s" + id, Email: id + "@x.io",
			KeyID: id, CreatedAt: now, UpdatedAt: now}))
		require.NoError(t, db.Budgets().Create(ctx, Budget{ID: "b" + id, TenantID: DefaultTenantID,
			ScopeKind: ScopeAPIKey, ScopeID: id, LimitMicros: 10_000_000, Period: "total",
			AlertPct: 80, HardCutoff: true, CreatedAt: now, UpdatedAt: now}))
	}
	// ka spends 4e6 micros (4e9 nanos); kb overspends (12e9 nanos).
	ra := finRec("x", "p", "c", 4e9, 0, false, now)
	ra.APIKeyID = "ka"
	rb := finRec("y", "p", "c", 12e9, 0, false, now)
	rb.APIKeyID = "kb"
	require.NoError(t, db.Usage().RecordBatch(ctx, []UsageRecord{ra, rb}))
	got, err := db.Usage().OutstandingPortalCredit(ctx, DefaultTenantID)
	require.NoError(t, err)
	require.Equal(t, int64(6_000_000), got)
}
