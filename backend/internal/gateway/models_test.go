package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/vault"
)

func TestListModelsOnlyShowsConnectedProviders(t *testing.T) {
	gw, apiKey := newModelDiscoveryTestGateway(t, []store.Account{
		modelDiscoveryAccount("acc-openai", "openai", false, false),
		modelDiscoveryAccount("acc-anthropic-disabled", "anthropic", true, false),
		modelDiscoveryAccount("acc-gemini-reconnect", "gemini", false, true),
	})

	body := getAuthedJSON(t, gw, apiKey, "/v1/models")
	models := modelIDsFromResponse(t, body)

	require.NotEmpty(t, models)
	require.Contains(t, models, "openai/gpt-4o")
	require.NotContains(t, models, "anthropic/claude-sonnet-4-20250514")
	require.NotContains(t, models, "gemini/gemini-2.5-pro")
	for _, id := range models {
		if strings.Contains(id, "/") {
			require.Truef(t, strings.HasPrefix(id, "openai/"), "unexpected unconnected provider model %q", id)
		}
	}
}

func TestListModelsByKindOnlyShowsConnectedProviders(t *testing.T) {
	gw, apiKey := newModelDiscoveryTestGateway(t, []store.Account{
		modelDiscoveryAccount("acc-openai", "openai", false, false),
	})

	body := getAuthedJSON(t, gw, apiKey, "/v1/models/embedding")
	models := modelIDsFromResponse(t, body)

	require.Contains(t, models, "openai/text-embedding-3-small")
	for _, id := range models {
		require.Truef(t, strings.HasPrefix(id, "openai/"), "unexpected unconnected provider model %q", id)
	}
}

func TestModelInfoOnlyShowsConnectedProviders(t *testing.T) {
	gw, apiKey := newModelDiscoveryTestGateway(t, []store.Account{
		modelDiscoveryAccount("acc-openai", "openai", false, false),
	})

	body := getAuthedJSON(t, gw, apiKey, "/v1/models/info?id=openai/gpt-4o")
	require.Equal(t, "openai", body["provider"])
	require.Equal(t, "gpt-4o", body["model"])

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models/info?id=anthropic/claude-sonnet-4-20250514", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	gw.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}

func TestListModelsStillShowsChains(t *testing.T) {
	gw, apiKey := newModelDiscoveryTestGateway(t, nil)
	require.NoError(t, gw.chains.Create(context.Background(), store.Chain{
		ID:       "chain-fast",
		TenantID: store.DefaultTenantID,
		Name:     "fast",
		Strategy: "fallback",
		Steps: []store.ChainStep{{
			Position: 0,
			Provider: "openai",
			Model:    "gpt-4o",
		}},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}))

	body := getAuthedJSON(t, gw, apiKey, "/v1/models")
	models := modelIDsFromResponse(t, body)

	require.Equal(t, []string{"fast"}, models)
}

func newModelDiscoveryTestGateway(t *testing.T, accounts []store.Account) (*Server, string) {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })

	mk, err := crypto.GenerateMasterKey()
	require.NoError(t, err)
	sealer, err := crypto.NewSealer(mk)
	require.NoError(t, err)
	v := vault.New(sealer)

	for _, acc := range accounts {
		require.NoError(t, v.Seal(&acc, vault.NewSecret{APIKey: "sk-test"}))
		require.NoError(t, db.Accounts().Create(ctx, acc))
	}

	idSvc := identity.New(db.APIKeys(), crypto.NewKeyLookup(mk))
	issued, err := idSvc.Create(ctx, store.DefaultTenantID, "", "test-key")
	require.NoError(t, err)

	gw := New(Deps{
		Config:   config.Default(),
		Identity: idSvc,
		Chains:   db.Chains(),
		Accounts: db.Accounts(),
		Vault:    v,
	})
	return gw, issued.Plaintext
}

func modelDiscoveryAccount(id, provider string, disabled, needsReconnect bool) store.Account {
	now := time.Now()
	return store.Account{
		ID:             id,
		TenantID:       store.DefaultTenantID,
		Provider:       provider,
		Label:          id,
		AuthKind:       store.AuthAPIKey,
		Priority:       10,
		Disabled:       disabled,
		NeedsReconnect: needsReconnect,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func getAuthedJSON(t *testing.T, gw *Server, apiKey, path string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	gw.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func modelIDsFromResponse(t *testing.T, body map[string]any) []string {
	t.Helper()
	items, ok := body["data"].([]any)
	require.True(t, ok)
	out := make([]string, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		require.True(t, ok)
		id, ok := m["id"].(string)
		require.True(t, ok)
		out = append(out, id)
	}
	return out
}
