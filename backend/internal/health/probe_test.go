package health

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/store"
)

type probeCaptureConn struct {
	id    string
	creds core.Credentials
}

func (c *probeCaptureConn) ID() string            { return c.id }
func (c *probeCaptureConn) Dialect() core.Dialect { return core.DialectOpenAI }
func (c *probeCaptureConn) Chat(context.Context, *core.ChatRequest, core.Credentials) (*core.ChatResponse, error) {
	return &core.ChatResponse{}, nil
}
func (c *probeCaptureConn) Stream(_ context.Context, _ *core.ChatRequest, creds core.Credentials, _ core.StreamConfig) (<-chan core.StreamChunk, error) {
	c.creds = creds
	ch := make(chan core.StreamChunk)
	close(ch)
	return ch, nil
}

type fakePreparer struct{ prepared []string }

func (p *fakePreparer) PrepareCredentials(_ context.Context, acc store.Account) (store.Account, core.Credentials, error) {
	p.prepared = append(p.prepared, acc.ID)
	return acc, core.Credentials{
		AccountID: acc.ID,
		APIKey:    "key-" + acc.ID,
		Extra:     map[string]string{"accountId": "cf-" + acc.ID},
		ProxyURL:  "http://proxy.test:8080",
	}, nil
}

func TestProbeRunnerUsesConfiguredAccountCredentials(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))

	now := time.Now()
	for _, acc := range []store.Account{
		{ID: "disabled", Provider: "cloudflare-ai", Priority: 0, Disabled: true},
		{ID: "reconnect", Provider: "cloudflare-ai", Priority: 1, NeedsReconnect: true},
		{ID: "usable", Provider: "cloudflare-ai", Priority: 2},
		{ID: "other", Provider: "openai", Priority: 0},
	} {
		acc.TenantID = store.DefaultTenantID
		acc.AuthKind = store.AuthAPIKey
		acc.Metadata = "{}"
		acc.CreatedAt, acc.UpdatedAt = now, now
		require.NoError(t, db.Accounts().Create(ctx, acc))
	}

	conn := &probeCaptureConn{id: "cloudflare-ai"}
	prep := &fakePreparer{}
	runner := NewProbeRunner(nil, db.ProviderHealth(), db.Accounts(), connectors.NewRegistry(conn), nil, nil, time.Second)
	runner.SetCredentialPreparer(prep)

	t.Run("provider-level probe picks the first usable account", func(t *testing.T) {
		res, err := runner.Run(ctx, ProbeRequest{Provider: "cloudflare-ai", Model: "@cf/test/model"})
		require.NoError(t, err)
		require.Equal(t, "success", res.Status)
		require.Equal(t, "usable", res.ProviderAccountID)
		require.Equal(t, []string{"usable"}, prep.prepared)
		require.Equal(t, "key-usable", conn.creds.APIKey)
		require.Equal(t, "cf-usable", conn.creds.Extra["accountId"])
		require.Equal(t, "http://proxy.test:8080", conn.creds.ProxyURL)
	})

	t.Run("explicit account must match provider and be enabled", func(t *testing.T) {
		_, err := runner.Run(ctx, ProbeRequest{Provider: "cloudflare-ai", ProviderAccountID: "other", Model: "m"})
		require.ErrorContains(t, err, "belongs to provider openai")
		_, err = runner.Run(ctx, ProbeRequest{Provider: "cloudflare-ai", ProviderAccountID: "disabled", Model: "m"})
		require.ErrorContains(t, err, "is disabled")
	})

	t.Run("provider without usable account is refused", func(t *testing.T) {
		_, err := runner.Run(ctx, ProbeRequest{Provider: "openai", Model: "m"})
		require.ErrorContains(t, err, "unknown provider")
		conn.id = "openai"
		runner.conns = connectors.NewRegistry(conn)
		require.NoError(t, db.Accounts().SetNeedsReconnect(ctx, "other", true))
		_, err = runner.Run(ctx, ProbeRequest{Provider: "openai", Model: "m"})
		require.ErrorContains(t, err, "no usable account")
	})
}
