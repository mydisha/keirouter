package store

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUsageByKeySince(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	rec := func(id, key string, cost int64, at time.Time) UsageRecord {
		r := finRec(id, "p", "c", cost, 0, false, at)
		r.APIKeyID, r.PromptTokens, r.CompletionTokens = key, 10, 5
		return r
	}
	require.NoError(t, db.Usage().RecordBatch(ctx, []UsageRecord{
		rec("a", "ka", 2e9, now),
		rec("b", "ka", 3e9, now),
		rec("c", "kb", 1e9, now),
		rec("d", "kb", 9e9, now.Add(-2*time.Hour)),
		rec("e", "", 4e9, now),
	}))
	got, err := db.Usage().UsageByKeySince(ctx, DefaultTenantID, now.Add(-time.Hour))
	require.NoError(t, err)
	sort.Slice(got, func(i, j int) bool { return got[i].KeyID < got[j].KeyID })
	require.Equal(t, []KeyUsageTotals{
		{KeyID: "ka", Requests: 2, PromptTokens: 20, CompletionTokens: 10, CostNanos: 5e9},
		{KeyID: "kb", Requests: 1, PromptTokens: 10, CompletionTokens: 5, CostNanos: 1e9},
	}, got)
}

func TestPaidTotalsByKeySince(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	mk := func(id, key string, st PaymentOrderStatus, created time.Time) {
		o := sampleOrder(id)
		o.KeyID, o.Status, o.CreatedAt, o.UpdatedAt = key, st, created, created
		require.NoError(t, db.PaymentOrders().Create(ctx, o))
	}
	mk("o1", "ka", PaymentCompleted, now)
	mk("o2", "ka", PaymentManual, now)
	mk("o3", "kb", PaymentPending, now)
	mk("o4", "kb", PaymentCompleted, now.Add(-48*time.Hour))
	mk("o5", "kc", PaymentCompleted, now)
	got, err := db.PaymentOrders().PaidTotalsByKeySince(ctx, now.Add(-time.Hour))
	require.NoError(t, err)
	sort.Slice(got, func(i, j int) bool { return got[i].KeyID < got[j].KeyID })
	require.Equal(t, []KeyPaidTotals{
		{KeyID: "ka", PaidOrderTotals: PaidOrderTotals{Count: 2, AmountIDR: 100000, CreditMicros: 6_200_000}},
		{KeyID: "kc", PaidOrderTotals: PaidOrderTotals{Count: 1, AmountIDR: 50000, CreditMicros: 3_100_000}},
	}, got)
}
