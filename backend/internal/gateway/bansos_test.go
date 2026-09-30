package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/vault"
)

func newBansosTestServer(t *testing.T) (*Server, *store.DB) {
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

	s := New(Deps{
		Config:   config.Default(),
		DB:       db,
		Identity: identity.New(db.APIKeys()),
		Budgets:  db.Budgets(),
		Usage:    db.Usage(),
		Settings: db.Settings(),
		Vault:    vault.New(sealer),
		Chains:   db.Chains(),
	})
	return s, db
}

func TestBansosConfigRoundTrip(t *testing.T) {
	s, _ := newBansosTestServer(t)
	ctx := context.Background()

	_, ok, err := s.loadBansos(ctx)
	require.NoError(t, err)
	require.False(t, ok, "unconfigured bansos must report not-configured")
	require.Empty(t, s.bansosKeyID(ctx))

	cfg := bansosConfig{
		KeyID: "key-1", PlanID: "plan-1", Active: true, Mode: bansosModeCredit,
		MaskedDisplay: "tkr_ab••••", AllowedModels: []string{"claude-*"},
		RPM: 60, TPM: 200000,
	}
	require.NoError(t, s.saveBansos(ctx, cfg))

	got, ok, err := s.loadBansos(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "key-1", got.KeyID)
	require.Equal(t, bansosModeCredit, got.Mode)
	require.True(t, got.Active)
	require.Equal(t, []string{"claude-*"}, got.AllowedModels)
	require.Equal(t, int64(60), got.RPM)
	require.Equal(t, "key-1", s.bansosKeyID(ctx))
}

func callBansosHandler(t *testing.T, s *Server, h func(http.ResponseWriter, *http.Request), method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestAdminCreateAndGetBansos(t *testing.T) {
	s, db := newBansosTestServer(t)
	ctx := context.Background()

	w := callBansosHandler(t, s, s.adminCreateBansos, http.MethodPost, "/bansos",
		`{"mode":"credit","allowed_models":["claude-*"],"rpm":60,"tpm":200000,"credit_limit_usd":10}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var created struct {
		KeyID string `json:"key_id"`
		Key   string `json:"key"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.NotEmpty(t, created.KeyID)
	require.True(t, strings.HasPrefix(created.Key, crypto.DefaultKeyPrefix), created.Key)

	// The dedicated key exists and is disabled while inactive.
	key, err := s.identity.Get(ctx, created.KeyID)
	require.NoError(t, err)
	require.True(t, key.Disabled, "new bansos must start inactive/disabled")

	// Models + plan wired.
	models, err := db.APIKeys().GetAllowedModels(ctx, created.KeyID)
	require.NoError(t, err)
	require.Equal(t, []string{"claude-*"}, models)

	cfg, ok, err := s.loadBansos(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	plan, err := db.Plans().Get(ctx, cfg.PlanID)
	require.NoError(t, err)
	require.EqualValues(t, 60, plan.RPMLimit)
	require.EqualValues(t, 200000, plan.TPMLimit)

	// Credit budget created for the key.
	budgets, err := db.Budgets().ListByScope(ctx, store.ScopeAPIKey, created.KeyID)
	require.NoError(t, err)
	require.Len(t, budgets, 1)
	require.EqualValues(t, 10_000_000, budgets[0].LimitMicros)

	// Duplicate create is a conflict.
	w2 := callBansosHandler(t, s, s.adminCreateBansos, http.MethodPost, "/bansos",
		`{"mode":"unlimited","allowed_models":["gpt-5"]}`)
	require.Equal(t, http.StatusConflict, w2.Code, w2.Body.String())

	// GET never returns plaintext, returns the masked display.
	w3 := callBansosHandler(t, s, s.adminGetBansos, http.MethodGet, "/bansos", "")
	require.Equal(t, http.StatusOK, w3.Code, w3.Body.String())
	require.NotContains(t, w3.Body.String(), created.Key, "GET must not leak plaintext")
	var state map[string]any
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &state))
	require.Equal(t, created.KeyID, state["key_id"])
	require.Equal(t, false, state["active"])
	require.NotNil(t, state["credit"])
}

func TestAdminCreateBansosRequiresAllowedModel(t *testing.T) {
	s, _ := newBansosTestServer(t)
	w := callBansosHandler(t, s, s.adminCreateBansos, http.MethodPost, "/bansos", `{"mode":"unlimited"}`)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

func TestAdminUpdateBansosToggleAndMode(t *testing.T) {
	s, db := newBansosTestServer(t)
	ctx := context.Background()

	w := callBansosHandler(t, s, s.adminCreateBansos, http.MethodPost, "/bansos",
		`{"mode":"credit","allowed_models":["claude-*"],"rpm":30,"credit_limit_usd":5}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	cfg, _, err := s.loadBansos(ctx)
	require.NoError(t, err)

	// Activate.
	w2 := callBansosHandler(t, s, s.adminUpdateBansos, http.MethodPatch, "/bansos", `{"active":true}`)
	require.Equal(t, http.StatusOK, w2.Code, w2.Body.String())
	key, err := s.identity.Get(ctx, cfg.KeyID)
	require.NoError(t, err)
	require.False(t, key.Disabled)

	// Activating with an empty allowlist fails closed.
	w3 := callBansosHandler(t, s, s.adminUpdateBansos, http.MethodPatch, "/bansos", `{"allowed_models":[]}`)
	require.Equal(t, http.StatusBadRequest, w3.Code, w3.Body.String())

	// Credit -> unlimited removes the budget row.
	w4 := callBansosHandler(t, s, s.adminUpdateBansos, http.MethodPatch, "/bansos", `{"mode":"unlimited"}`)
	require.Equal(t, http.StatusOK, w4.Code, w4.Body.String())
	budgets, err := db.Budgets().ListByScope(ctx, store.ScopeAPIKey, cfg.KeyID)
	require.NoError(t, err)
	require.Empty(t, budgets)

	// Unlimited -> credit recreates a budget row.
	w5 := callBansosHandler(t, s, s.adminUpdateBansos, http.MethodPatch, "/bansos", `{"mode":"credit","credit_limit_usd":3}`)
	require.Equal(t, http.StatusOK, w5.Code, w5.Body.String())
	budgets, err = db.Budgets().ListByScope(ctx, store.ScopeAPIKey, cfg.KeyID)
	require.NoError(t, err)
	require.Len(t, budgets, 1)
	require.EqualValues(t, 3_000_000, budgets[0].LimitMicros)

	// Deactivate disables the key.
	w6 := callBansosHandler(t, s, s.adminUpdateBansos, http.MethodPatch, "/bansos", `{"active":false}`)
	require.Equal(t, http.StatusOK, w6.Code, w6.Body.String())
	key, err = s.identity.Get(ctx, cfg.KeyID)
	require.NoError(t, err)
	require.True(t, key.Disabled)
}

func TestAdminBansosTopupAndRotate(t *testing.T) {
	s, db := newBansosTestServer(t)
	ctx := context.Background()

	w := callBansosHandler(t, s, s.adminCreateBansos, http.MethodPost, "/bansos",
		`{"mode":"credit","allowed_models":["claude-*"],"credit_limit_usd":1}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var created struct {
		KeyID string `json:"key_id"`
		Key   string `json:"key"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))

	// Top-up increases the limit.
	w2 := callBansosHandler(t, s, s.adminBansosTopup, http.MethodPost, "/bansos/topup",
		`{"amount_usd":2,"idempotency_key":"b-1"}`)
	require.Equal(t, http.StatusCreated, w2.Code, w2.Body.String())
	budgets, err := db.Budgets().ListByScope(ctx, store.ScopeAPIKey, created.KeyID)
	require.NoError(t, err)
	require.Len(t, budgets, 1)
	require.EqualValues(t, 3_000_000, budgets[0].LimitMicros)

	// Rotate replaces the key material; the old plaintext no longer authenticates.
	// The key must be active for authentication to be meaningful (create mints it
	// disabled).
	wAct := callBansosHandler(t, s, s.adminUpdateBansos, http.MethodPatch, "/bansos", `{"active":true}`)
	require.Equal(t, http.StatusOK, wAct.Code, wAct.Body.String())
	w3 := callBansosHandler(t, s, s.adminBansosRotate, http.MethodPost, "/bansos/rotate", "")
	require.Equal(t, http.StatusOK, w3.Code, w3.Body.String())
	var rotated struct {
		Key string `json:"key"`
	}
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &rotated))
	require.NotEqual(t, created.Key, rotated.Key)

	_, err = s.identity.Authenticate(ctx, created.Key)
	require.Error(t, err, "old plaintext must stop authenticating")
	rec, err := s.identity.Authenticate(ctx, rotated.Key)
	require.NoError(t, err)
	require.Equal(t, created.KeyID, rec.ID)

	// Top-up on unlimited mode is rejected.
	cfg, _, err := s.loadBansos(ctx)
	require.NoError(t, err)
	cfg.Mode = bansosModeUnlimited
	require.NoError(t, s.saveBansos(ctx, cfg))
	w4 := callBansosHandler(t, s, s.adminBansosTopup, http.MethodPost, "/bansos/topup",
		`{"amount_usd":1,"idempotency_key":"b-2"}`)
	require.Equal(t, http.StatusBadRequest, w4.Code, w4.Body.String())
}

func callBansosHandlerWithID(t *testing.T, s *Server, h func(http.ResponseWriter, *http.Request), method, target, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestGenericKeyEndpointsRejectBansosKey(t *testing.T) {
	s, _ := newBansosTestServer(t)
	ctx := context.Background()

	w := callBansosHandler(t, s, s.adminCreateBansos, http.MethodPost, "/bansos",
		`{"mode":"unlimited","allowed_models":["claude-*"]}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var created struct {
		KeyID string `json:"key_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))

	// List excludes the bansos key.
	lw := callBansosHandler(t, s, s.adminListKeys, http.MethodGet, "/keys", "")
	require.Equal(t, http.StatusOK, lw.Code, lw.Body.String())
	require.NotContains(t, lw.Body.String(), created.KeyID)

	// Create a normal key so the list is non-trivial and the filter is proven.
	normal, err := s.identity.Create(ctx, store.DefaultTenantID, "", "normal")
	require.NoError(t, err)
	lw2 := callBansosHandler(t, s, s.adminListKeys, http.MethodGet, "/keys", "")
	require.Contains(t, lw2.Body.String(), normal.Record.ID)

	// Delete rejects the bansos key.
	dw := callBansosHandlerWithID(t, s, s.adminDeleteKey, http.MethodDelete, "/keys/"+created.KeyID, created.KeyID, "")
	require.Equal(t, http.StatusBadRequest, dw.Code, dw.Body.String())

	// PATCH rejects the bansos key.
	pw := callBansosHandlerWithID(t, s, s.adminUpdateKey, http.MethodPatch, "/keys/"+created.KeyID, created.KeyID, `{"disabled":true}`)
	require.Equal(t, http.StatusBadRequest, pw.Code, pw.Body.String())

	// Top-up endpoint rejects the bansos key.
	tw := callBansosHandlerWithID(t, s, s.adminTopupKey, http.MethodPost, "/keys/"+created.KeyID+"/topup", created.KeyID,
		`{"amount_usd":1,"idempotency_key":"x"}`)
	require.Equal(t, http.StatusBadRequest, tw.Code, tw.Body.String())

	// Limit adjust endpoint rejects the bansos key.
	aw := callBansosHandlerWithID(t, s, s.adminAdjustKeyLimit, http.MethodPost, "/keys/"+created.KeyID+"/limit", created.KeyID,
		`{"limit_usd":1,"reason":"test","idempotency_key":"y"}`)
	require.Equal(t, http.StatusBadRequest, aw.Code, aw.Body.String())
}
