package finance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func newLoaderDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func findBalance(t *testing.T, bs []AccountBalance, id string) AccountBalance {
	t.Helper()
	for _, b := range bs {
		if b.Account.ID == id {
			return b
		}
	}
	t.Fatalf("account %s not found", id)
	return AccountBalance{}
}

func TestLoader_ReportProfitAndBalances(t *testing.T) {
	db := newLoaderDB(t)
	ctx := context.Background()
	now := time.Now()
	require.NoError(t, db.Usage().RecordBatch(ctx, []store.UsageRecord{{
		ID: "u1", TenantID: store.DefaultTenantID, APIKeyID: "k1", Provider: "p1", Model: "m",
		Status: "success", CostNanos: 15e9, UpstreamCostNanos: 10e9,
		PricingStatus: "priced", PricingSource: "official", CreatedAt: now.Add(-time.Minute),
	}}))
	require.NoError(t, db.Upstream().Create(ctx, store.UpstreamAccount{
		ID: "a1", Name: "Acc", Status: "active", OpeningBalanceMicros: 100e6,
		OpeningAt: now.Add(-time.Hour),
	}))
	require.NoError(t, db.Upstream().SetProviders(ctx, "a1", []string{"p1"}))

	l := &Loader{DB: db, Settings: db.Settings().Get, InFlight: func() map[string]int64 { return map[string]int64{"p1": 2} }}
	rep, err := l.Report(ctx, "all")
	require.NoError(t, err)
	require.Equal(t, "all", rep.Range)
	require.InDelta(t, 5.0, rep.Overview.ProfitUSD, 1e-9)
	require.EqualValues(t, 2, rep.Overview.InFlight)
	acc := findBalance(t, rep.Accounts, "a1")
	require.InDelta(t, 90.0, acc.BalanceUSD, 1e-9)
	require.EqualValues(t, 2, acc.InFlight)
	require.Empty(t, rep.Unmapped.Providers)

	bals, err := l.Balances(ctx)
	require.NoError(t, err)
	require.InDelta(t, 90.0, findBalance(t, bals, "a1").BalanceUSD, 1e-9)

	_, err = l.Report(ctx, "x")
	require.True(t, errors.Is(err, ErrBadRange), "got %v", err)
}

func TestLoader_MarkupsGlobalFromSettings(t *testing.T) {
	db := newLoaderDB(t)
	ctx := context.Background()
	require.NoError(t, db.Settings().Set(ctx, "market_pricing_settings", `{"markup_percent":25}`))
	l := &Loader{DB: db, Settings: db.Settings().Get}
	m, err := l.Markups(ctx)
	require.NoError(t, err)
	require.Equal(t, 25.0, m.Global)
}

func TestAlertSettings_RoundTrip(t *testing.T) {
	db := newLoaderDB(t)
	ctx := context.Background()
	got, err := LoadAlertSettings(ctx, db.Settings().Get)
	require.NoError(t, err)
	require.Equal(t, AlertSettings{}, got)
	in := AlertSettings{Enabled: true, DiscordWebhookURL: "https://discord.com/api/webhooks/1/tok"}
	require.NoError(t, SaveAlertSettings(ctx, db.Settings().Set, in))
	got, err = LoadAlertSettings(ctx, db.Settings().Get)
	require.NoError(t, err)
	require.Equal(t, in, got)
}

func TestValidateAndMaskWebhook(t *testing.T) {
	require.NoError(t, ValidateDiscordWebhook("https://discord.com/api/webhooks/123/abc"))
	require.NoError(t, ValidateDiscordWebhook("https://discordapp.com/api/webhooks/123/abc"))
	require.Error(t, ValidateDiscordWebhook("https://example.com/api/webhooks/123/abc"))
	require.Error(t, ValidateDiscordWebhook("http://discord.com/api/webhooks/123/abc"))
	require.Equal(t, "https://discord.com/api/webhooks/123/****", MaskDiscordWebhook("https://discord.com/api/webhooks/123/abcSECRET"))
	require.Equal(t, "", MaskDiscordWebhook(""))
}
