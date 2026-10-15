package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestAdminUsageLeaderboardRanksKeys(t *testing.T) {
	s := newPortalTestServer(t)
	s.insightsCache = newTTLCache(insightsCacheTTL)
	ctx := t.Context()
	now := time.Now().UTC()
	for _, id := range []string{"ka", "kb"} {
		require.NoError(t, s.db.APIKeys().Create(ctx, store.APIKey{ID: id, TenantID: store.DefaultTenantID, Name: "key-" + id,
			KeyHash: "h" + id, LookupHash: "l" + id, Display: "d", CreatedAt: now}))
	}
	require.NoError(t, s.db.PortalUsers().Upsert(ctx, store.PortalUser{GoogleSub: "sa", Email: "a@x.io", KeyID: "ka", CreatedAt: now, UpdatedAt: now}))

	rec := func(id, key string, tokens int, cost int64) store.UsageRecord {
		return store.UsageRecord{ID: id, TenantID: store.DefaultTenantID, APIKeyID: key, Provider: "p", Model: "m",
			Status: "success", PromptTokens: tokens, CostNanos: cost, PricingStatus: "priced", PricingSource: "official", CreatedAt: now}
	}
	// ka: 1 request, many tokens, high spend. kb: 2 requests, few tokens.
	require.NoError(t, s.usage.RecordBatch(ctx, []store.UsageRecord{
		rec("u1", "ka", 1000, 5e9),
		rec("u2", "kb", 10, 1e9),
		rec("u3", "kb", 10, 1e9),
	}))
	require.NoError(t, s.db.PaymentOrders().Create(ctx, store.PaymentOrder{ID: "o1", TenantID: store.DefaultTenantID, KeyID: "kb",
		AmountIDR: 50000, CreditMicros: 3_100_000, FxRateMicros: 16_100_000_000, Status: store.PaymentManual,
		Provider: "sumopod", ProviderPaymentID: "pay-o1", CreatedAt: now, UpdatedAt: now}))

	w := httptest.NewRecorder()
	s.adminUsageLeaderboard(w, httptest.NewRequest(http.MethodGet, "/usage/leaderboard?period=today", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body struct {
		Topups, Spend, Tokens, Requests []leaderboardEntry
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	require.Len(t, body.Topups, 1)
	require.Equal(t, "kb", body.Topups[0].KeyID)
	require.Equal(t, "key-kb", body.Topups[0].KeyName)
	require.Empty(t, body.Topups[0].Email)
	require.Equal(t, []string{"ka", "kb"}, []string{body.Spend[0].KeyID, body.Spend[1].KeyID})
	require.Equal(t, "a@x.io", body.Spend[0].Email)
	require.Equal(t, int64(1000), body.Tokens[0].Tokens)
	require.Equal(t, "kb", body.Requests[0].KeyID)
	require.Equal(t, int64(2), body.Requests[0].Requests)
}
