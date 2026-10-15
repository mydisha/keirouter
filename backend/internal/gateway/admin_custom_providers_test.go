package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/vault"
)

// newCustomProviderTestServer wires a minimal Server with an in-memory store
// and vault, sufficient to exercise the custom-provider delete handler.
func newCustomProviderTestServer(t *testing.T) (*Server, *store.DB) {
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

	s := &Server{
		db:       db,
		accounts: db.Accounts(),
		vault:    vault.New(sealer),
		log:      slog.Default(),
	}
	return s, db
}

// withChiID builds a request whose chi URLParam "id" is set, so handlers that
// call chi.URLParam(r, "id") work without mounting a full router.
func withChiID(method, target, id string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	req := httptest.NewRequest(method, target, nil)
	return req.WithContext(ctx)
}

// deleteCustomProvider invokes the handler and returns status + decoded body.
func deleteCustomProvider(t *testing.T, s *Server, id string) (int, map[string]any) {
	t.Helper()
	req := withChiID(http.MethodDelete, "/custom-providers/"+id, id)
	rec := httptest.NewRecorder()
	s.adminDeleteCustomProvider(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	}
	return rec.Code, out
}

func TestDeleteCustomProvider_BuiltinRejected(t *testing.T) {
	s, _ := newCustomProviderTestServer(t)

	for _, id := range []string{"custom-openai", "custom-anthropic"} {
		code, body := deleteCustomProvider(t, s, id)
		require.Equal(t, http.StatusConflict, code, "id=%s body=%v", id, body)
		require.Nil(t, body["deleted"])
	}
}

func TestDeleteCustomProvider_NotFound(t *testing.T) {
	s, _ := newCustomProviderTestServer(t)

	code, body := deleteCustomProvider(t, s, "custom-openai-missing")
	require.Equal(t, http.StatusNotFound, code, body)

	// Sanity: the missing id never accidentally matches a dynamic provider.
	_, ok := connectors.DynamicProviderByID("custom-openai-missing")
	require.False(t, ok)
}

func TestDeleteCustomProvider_SuccessDisablesAccountsAndUnregisters(t *testing.T) {
	s, db := newCustomProviderTestServer(t)
	ctx := context.Background()

	const pid = "custom-openai-testinst"
	now := time.Now()

	// Persist a custom provider row + a custom model bound to it.
	require.NoError(t, db.CustomProviders().CreateProvider(ctx, store.CustomProvider{
		ID:          pid,
		TenantID:    store.DefaultTenantID,
		DisplayName: "Test Instance",
		Alias:       pid,
		Dialect:     string(core.DialectOpenAI),
		BaseURL:     "https://example.test/v1",
		CreatedAt:   now,
		UpdatedAt:   now,
	}))
	require.NoError(t, db.CustomProviders().CreateModel(ctx, store.CustomModel{
		ID:          "cm-1",
		TenantID:    store.DefaultTenantID,
		ProviderID:  pid,
		ModelID:     "gpt-test",
		DisplayName: "gpt-test",
		Kind:        string(core.ServiceLLM),
		Source:      "manual",
	}))

	// Register it live so the registry mirrors runtime state.
	connectors.RegisterDynamicProvider(connectors.DynamicProvider{
		ID: pid, DisplayName: "Test Instance", Alias: pid,
		Dialect: core.DialectOpenAI, BaseURL: "https://example.test/v1",
	})
	t.Cleanup(func() { connectors.UnregisterDynamicProvider(pid) })

	// Create two accounts bound to the provider: one enabled, one disabled.
	require.NoError(t, db.Accounts().Create(ctx, store.Account{
		ID: "acct-enabled", TenantID: store.DefaultTenantID, Provider: pid,
		Label: "enabled", AuthKind: store.AuthAPIKey, Priority: 10, Metadata: "{}",
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, db.Accounts().Create(ctx, store.Account{
		ID: "acct-already-off", TenantID: store.DefaultTenantID, Provider: pid,
		Label: "off", AuthKind: store.AuthAPIKey, Priority: 10, Metadata: "{}",
		Disabled:  true,
		CreatedAt: now, UpdatedAt: now,
	}))

	// Precondition: dynamic provider + an enabled account exist.
	_, ok := connectors.DynamicProviderByID(pid)
	require.True(t, ok)
	enabled, err := db.Accounts().ListByProvider(ctx, store.DefaultTenantID, pid)
	require.NoError(t, err)
	require.Len(t, enabled, 1)

	code, body := deleteCustomProvider(t, s, pid)
	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, true, body["deleted"])
	require.Equal(t, pid, body["id"])
	// accounts_disabled reflects total matched rows (both accounts).
	require.Equal(t, float64(2), body["accounts_disabled"])

	// In-memory dynamic provider + its models are gone.
	_, ok = connectors.DynamicProviderByID(pid)
	require.False(t, ok)

	// Provider row + its models are gone from the DB.
	_, err = db.CustomProviders().GetProvider(ctx, pid)
	require.ErrorIs(t, err, store.ErrNotFound)
	models, err := db.CustomProviders().ListModelsByProvider(ctx, pid)
	require.NoError(t, err)
	require.Empty(t, models)

	// No enabled accounts remain bound to the deleted provider.
	enabled, err = db.Accounts().ListByProvider(ctx, store.DefaultTenantID, pid)
	require.NoError(t, err)
	require.Empty(t, enabled, "all bound accounts must be disabled after delete")
}

func TestDeleteCustomProvider_RepoReturnsNotFoundForMissing(t *testing.T) {
	s, db := newCustomProviderTestServer(t)
	ctx := context.Background()

	// DeleteProvider itself surfaces ErrNotFound for an id that was never
	// inserted (defence-in-depth behind the handler's GetProvider probe).
	err := db.CustomProviders().DeleteProvider(ctx, "custom-openai-ghost")
	require.ErrorIs(t, err, store.ErrNotFound)

	// And the handler maps it to 404 when it slips past the probe.
	code, body := deleteCustomProvider(t, s, "custom-anthropic-ghost")
	require.Equal(t, http.StatusNotFound, code, body)
}

func TestResolveCustomAliasAllowsOwnAlias(t *testing.T) {
	const id = "custom-openai-own-alias-test"
	connectors.RegisterDynamicProvider(connectors.DynamicProvider{
		ID: id, DisplayName: "LM Studio", Alias: "lm-studio", Dialect: core.DialectOpenAI, BaseURL: "http://192.0.2.10:1234/v1",
	})
	t.Cleanup(func() { connectors.UnregisterDynamicProvider(id) })

	// Editing the provider re-submits its current alias; that is not a clash,
	// even though a built-in provider now uses the same ID.
	alias, err := resolveCustomAlias("lm-studio", "LM Studio", id)
	require.NoError(t, err)
	require.Equal(t, "lm-studio", alias)

	// A different custom provider still cannot take it, nor a built-in alias.
	_, err = resolveCustomAlias("lm-studio", "Other", "custom-openai-other")
	require.Error(t, err)
	_, err = resolveCustomAlias("openai", "Other", "custom-openai-other")
	require.Error(t, err)
}
