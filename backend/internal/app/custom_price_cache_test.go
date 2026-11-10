package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/meter"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func newPriceTestDB(t *testing.T, custom ...store.CustomModel) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now()
	for i, cm := range custom {
		cm.ID = "cm-" + string(rune('a'+i))
		cm.TenantID = store.DefaultTenantID
		cm.Kind = "llm"
		cm.Source = "manual"
		cm.CreatedAt, cm.UpdatedAt = now, now
		require.NoError(t, db.CustomProviders().CreateModel(ctx, cm))
	}
	return db
}

func newPriceTestMeter(t *testing.T, custom ...store.CustomModel) *meter.Meter {
	t.Helper()
	db := newPriceTestDB(t, custom...)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return meter.New(nil, buildPricing(), buildModelPrices(context.Background(), db, log))
}

// cachedHeavy is one agent turn: almost the whole prompt is a cache hit.
var cachedHeavy = core.Usage{PromptTokens: 1_000_000, CachedTokens: 900_000}

func costUSD(m *meter.Meter, provider, model string, u core.Usage) float64 {
	return float64(m.CalculateCost(provider, model, u, false, 0).CostNanos) / 1e9
}

const (
	cfDeepSeek = "@cf/deepseek-ai/deepseek-v4.1-flash-1017"
	cfGLM      = "@cf/zai-org/glm-5.3-flash"
)

func TestModelPrices_CloudflareCachedTiersFromVendorPage(t *testing.T) {
	m := newPriceTestMeter(t)

	ds := m.ResolvePrice("cloudflare-ai", cfDeepSeek)
	require.Equal(t, "exact", ds.MatchKind)
	require.Equal(t, 0.006, ds.Price.CachedInputPerM)
	// 100k uncached × $0.30 + 900k cached × $0.006
	require.InDelta(t, 0.0354, costUSD(m, "cloudflare-ai", cfDeepSeek, cachedHeavy), 1e-9)

	glm := m.ResolvePrice("cloudflare-ai", cfGLM)
	require.Equal(t, "exact", glm.MatchKind)
	// 100k × $0.15 + 900k × $0.03
	require.InDelta(t, 0.042, costUSD(m, "cloudflare-ai", cfGLM, cachedHeavy), 1e-9)
}

func TestModelPrices_CustomOverrideKeepsVendorCachedTier(t *testing.T) {
	// An operator entering Cloudflare's own list price for the model must not
	// turn the $0.006 cached rate into the $0.30 input rate.
	m := newPriceTestMeter(t, store.CustomModel{ProviderID: "cloudflare-ai", ModelID: cfDeepSeek, InputPerM: 0.30, OutputPerM: 1.20})

	p := m.ResolvePrice("cloudflare-ai", cfDeepSeek)
	require.Equal(t, "custom", p.Source)
	require.InDelta(t, 0.006, p.Price.CachedInputPerM, 1e-12)
	require.InDelta(t, 0.0354, costUSD(m, "cloudflare-ai", cfDeepSeek, cachedHeavy), 1e-9)
}

func TestModelPrices_CustomMarkupScalesCachedTier(t *testing.T) {
	m := newPriceTestMeter(t, store.CustomModel{ProviderID: "cloudflare-ai", ModelID: cfGLM, InputPerM: 0.30, OutputPerM: 1.0})

	p := m.ResolvePrice("cloudflare-ai", cfGLM)
	require.InDelta(t, 0.30, p.Price.InputPerM, 1e-12)
	require.InDelta(t, 0.06, p.Price.CachedInputPerM, 1e-12, "a 2× input markup doubles the cached tier too")
}

func TestModelPrices_CustomWithoutVendorRowBillsCachedAtInput(t *testing.T) {
	// A reseller's caching discount is unknown, so the conservative full rate
	// stays for models the catalog does not carry.
	m := newPriceTestMeter(t, store.CustomModel{ProviderID: "custom-openai-sumopod", ModelID: "deepseek-v4.1-flash:netra", InputPerM: 0.30, OutputPerM: 1.20})

	p := m.ResolvePrice("custom-openai-sumopod", "deepseek-v4.1-flash:netra")
	require.Equal(t, "exact", p.MatchKind)
	require.InDelta(t, 0.30, p.Price.CachedInputPerM, 1e-12)
}

func TestModelPrices_CustomOverVendorWithoutCachedTierStaysFull(t *testing.T) {
	// Cloudflare lists no cached rate for glm-4.7-flash.
	m := newPriceTestMeter(t, store.CustomModel{ProviderID: "cloudflare-ai", ModelID: "@cf/zai-org/glm-4.7-flash", InputPerM: 0.06, OutputPerM: 0.4})

	p := m.ResolvePrice("cloudflare-ai", "@cf/zai-org/glm-4.7-flash")
	require.InDelta(t, 0.06, p.Price.CachedInputPerM, 1e-12)
}

func TestModelPrices_ResellerRowNoLongerPricesCloudflareTraffic(t *testing.T) {
	// A custom reseller row with the same model fingerprint used to win the
	// canonical match for Cloudflare traffic and bring its full-rate cache tier.
	m := newPriceTestMeter(t, store.CustomModel{ProviderID: "custom-openai-sumopod", ModelID: "deepseek-v4.1-flash-1017", InputPerM: 0.30, OutputPerM: 1.20})

	p := m.ResolvePrice("cloudflare-ai", cfDeepSeek)
	require.Equal(t, "cloudflare-ai/"+cfDeepSeek, p.Key)
	require.InDelta(t, 0.0354, costUSD(m, "cloudflare-ai", cfDeepSeek, cachedHeavy), 1e-9)
}
