package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/finance"
	"github.com/mydisha/keirouter/backend/internal/inflight"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/vault"
)

func newFinanceTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s := newPaymentTestServer(t)
	s.settings = s.db.Settings()
	s.chains = s.db.Chains()
	s.inflight = inflight.New()
	mk, err := crypto.GenerateMasterKey()
	require.NoError(t, err)
	sealer, err := crypto.NewSealer(mk)
	require.NoError(t, err)
	s.vault = vault.New(sealer)
	r := chi.NewRouter()
	s.mountFinance(r)
	return s, r
}

func doJSON(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m), rec.Body.String())
	return m
}

func createUpstream(t *testing.T, h http.Handler, body string) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/payments/upstream-accounts", body)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	return decodeBody(t, rec)["id"].(string)
}

func TestFinance_RangeValidation(t *testing.T) {
	_, h := newFinanceTestServer(t)
	rec := doJSON(t, h, http.MethodGet, "/payments/finance?range=x", "")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	rec = doJSON(t, h, http.MethodGet, "/payments/finance?range=7d", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	m := decodeBody(t, rec)
	require.Equal(t, "7d", m["range"])
	require.NotNil(t, m["accounts"])
	require.NotNil(t, m["unmapped"].(map[string]any)["providers"])
}

func TestFinance_OverviewProfit(t *testing.T) {
	s, h := newFinanceTestServer(t)
	require.NoError(t, s.db.Usage().RecordBatch(t.Context(), []store.UsageRecord{{
		ID: "u1", TenantID: store.DefaultTenantID, APIKeyID: "k1", Provider: "p1", Model: "m",
		Status: "success", CostNanos: 15e9, UpstreamCostNanos: 10e9,
		PricingStatus: "priced", PricingSource: "official", CreatedAt: time.Now().Add(-time.Minute),
	}}))
	id := createUpstream(t, h, `{"name":"Acc","status":"active","opening_balance_usd":100,"opening_at":"2020-01-01"}`)
	rec := doJSON(t, h, http.MethodPatch, "/payments/upstream-accounts/"+id, `{"providers":["p1"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doJSON(t, h, http.MethodGet, "/payments/finance?range=all", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	m := decodeBody(t, rec)
	ov := m["overview"].(map[string]any)
	require.InDelta(t, 5.0, ov["profit_usd"].(float64), 1e-9)
	require.InDelta(t, 15.0, ov["revenue_usd"].(float64), 1e-9)
	var a map[string]any
	for _, x := range m["accounts"].([]any) {
		if x.(map[string]any)["id"] == id {
			a = x.(map[string]any)
		}
	}
	require.NotNil(t, a, "account %s missing", id)
	require.InDelta(t, 90.0, a["balance_usd"].(float64), 1e-9)
	require.Equal(t, []any{"p1"}, a["providers"])
}

func TestTopup_Validation(t *testing.T) {
	_, h := newFinanceTestServer(t)
	id := createUpstream(t, h, `{"name":"Acc","opening_balance_usd":0,"opening_at":"2020-01-01"}`)
	path := "/payments/upstream-accounts/" + id + "/topups"

	rec := doJSON(t, h, http.MethodPost, path, `{"amount_usd":0,"topup_at":"2024-01-02"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	rec = doJSON(t, h, http.MethodPost, path, `{"amount_usd":1000001,"topup_at":"2024-01-02"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	future := time.Now().Add(48 * time.Hour).Format("2006-01-02")
	rec = doJSON(t, h, http.MethodPost, path, `{"amount_usd":10,"topup_at":"`+future+`"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	rec = doJSON(t, h, http.MethodPost, path, `{"amount_usd":10,"topup_at":"not-a-date"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	rec = doJSON(t, h, http.MethodPost, path, `{"amount_usd":12.5,"topup_at":"2024-01-02","note":"wire"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	topupID := decodeBody(t, rec)["id"].(string)

	rec = doJSON(t, h, http.MethodGet, path, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	list := decodeBody(t, rec)["topups"].([]any)
	require.Len(t, list, 1)
	tp := list[0].(map[string]any)
	require.InDelta(t, 12.5, tp["amount_usd"].(float64), 1e-9)
	require.Equal(t, "dashboard", tp["actor"])
	require.Equal(t, "wire", tp["note"])

	rec = doJSON(t, h, http.MethodDelete, "/payments/upstream-topups/"+topupID, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = doJSON(t, h, http.MethodDelete, "/payments/upstream-topups/"+topupID, "")
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}

func TestAlerts_RejectsNonDiscordURL(t *testing.T) {
	_, h := newFinanceTestServer(t)
	rec := doJSON(t, h, http.MethodPut, "/payments/alerts", `{"enabled":true,"discord_webhook_url":"https://evil.example.com/api/webhooks/1/x"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

func TestAlerts_GetMasksURL(t *testing.T) {
	_, h := newFinanceTestServer(t)
	rec := doJSON(t, h, http.MethodPut, "/payments/alerts", `{"enabled":true,"discord_webhook_url":"https://discord.com/api/webhooks/123/SECRETTOKEN"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "SECRETTOKEN")

	// Omitted URL keeps the stored one.
	rec = doJSON(t, h, http.MethodPut, "/payments/alerts", `{"enabled":false}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doJSON(t, h, http.MethodGet, "/payments/alerts", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "SECRETTOKEN")
	m := decodeBody(t, rec)
	require.Equal(t, false, m["enabled"])
	require.Equal(t, true, m["configured"])
	require.Equal(t, "https://discord.com/api/webhooks/123/****", m["discord_webhook_url_masked"])
}

func TestAlerts_EmptyURLClearsWebhook(t *testing.T) {
	_, h := newFinanceTestServer(t)
	rec := doJSON(t, h, http.MethodPut, "/payments/alerts", `{"enabled":true,"discord_webhook_url":"https://discord.com/api/webhooks/123/SECRETTOKEN"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doJSON(t, h, http.MethodPut, "/payments/alerts", `{"enabled":true,"discord_webhook_url":""}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doJSON(t, h, http.MethodGet, "/payments/alerts", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	m := decodeBody(t, rec)
	require.Equal(t, false, m["enabled"])
	require.Equal(t, false, m["configured"])
	require.Equal(t, "", m["discord_webhook_url_masked"])
}

func TestTopup_BeforeOpeningRejected(t *testing.T) {
	_, h := newFinanceTestServer(t)
	id := createUpstream(t, h, `{"name":"Acc","opening_at":"2026-01-10T07:00:00Z"}`)
	path := "/payments/upstream-accounts/" + id + "/topups"

	rec := doJSON(t, h, http.MethodPost, path, `{"amount_usd":10,"topup_at":"2026-01-10T06:59:59Z"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "Tanggal topup tidak boleh sebelum tanggal saldo awal akun.")

	rec = doJSON(t, h, http.MethodPost, path, `{"amount_usd":10,"topup_at":"2026-01-10T07:00:00Z"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	rec = doJSON(t, h, http.MethodGet, "/payments/finance?range=all", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	for _, x := range decodeBody(t, rec)["accounts"].([]any) {
		if a := x.(map[string]any); a["id"] == id {
			require.InDelta(t, 10.0, a["balance_usd"].(float64), 1e-9)
		}
	}
}

func TestAlerts_TestSend(t *testing.T) {
	s, h := newFinanceTestServer(t)
	rec := doJSON(t, h, http.MethodPost, "/payments/alerts/test", "")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	var hits atomic.Int32
	var status atomic.Int32
	status.Store(http.StatusNoContent)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		b, _ := io.ReadAll(r.Body)
		require.Contains(t, string(b), "Tes notifikasi saldo provider dari Tokenizer.")
		w.WriteHeader(int(status.Load()))
	}))
	defer srv.Close()
	// Bypass URL validation to point the stored webhook at the fake server.
	require.NoError(t, finance.SaveAlertSettings(t.Context(), s.db.Settings().Set, s.financeBox(),
		finance.AlertSettings{Enabled: true, DiscordWebhookURL: srv.URL}))

	rec = doJSON(t, h, http.MethodPost, "/payments/alerts/test", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	status.Store(http.StatusInternalServerError)
	rec = doJSON(t, h, http.MethodPost, "/payments/alerts/test", "")
	require.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
	require.EqualValues(t, 2, hits.Load())
}

func TestUpstream_PatchProvidersMoves(t *testing.T) {
	_, h := newFinanceTestServer(t)
	a := createUpstream(t, h, `{"name":"A","opening_at":"2020-01-01"}`)
	b := createUpstream(t, h, `{"name":"B","opening_at":"2020-01-01"}`)
	rec := doJSON(t, h, http.MethodPatch, "/payments/upstream-accounts/"+a, `{"providers":["p1","p2"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = doJSON(t, h, http.MethodPatch, "/payments/upstream-accounts/"+b, `{"providers":["p2"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doJSON(t, h, http.MethodGet, "/payments/upstream-accounts", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	byName := map[string][]any{}
	for _, x := range decodeBody(t, rec)["accounts"].([]any) {
		m := x.(map[string]any)
		byName[m["name"].(string)] = m["providers"].([]any)
	}
	require.Equal(t, []any{"p1"}, byName["A"])
	require.Equal(t, []any{"p2"}, byName["B"])

	rec = doJSON(t, h, http.MethodPatch, "/payments/upstream-accounts/missing", `{"name":"x"}`)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}

func TestUpstream_PatchThresholdRearmsAlert(t *testing.T) {
	s, h := newFinanceTestServer(t)
	id := createUpstream(t, h, `{"name":"A","opening_at":"2020-01-01","low_balance_usd":5}`)
	require.NoError(t, s.db.Upstream().SetAlertFiredAt(t.Context(), id, time.Now()))
	rec := doJSON(t, h, http.MethodPatch, "/payments/upstream-accounts/"+id, `{"low_balance_usd":20}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	acc, err := s.db.Upstream().Get(t.Context(), id)
	require.NoError(t, err)
	require.True(t, acc.AlertFiredAt.IsZero())
	require.EqualValues(t, 20_000_000, acc.LowBalanceMicros)
}

func TestFinance_ProvidersList(t *testing.T) {
	s, h := newFinanceTestServer(t)
	require.NoError(t, s.db.Usage().RecordBatch(t.Context(), []store.UsageRecord{{
		ID: "u1", TenantID: store.DefaultTenantID, APIKeyID: "k1", Provider: "zeta", Model: "m",
		Status: "success", CostNanos: 1, PricingStatus: "priced", PricingSource: "official", CreatedAt: time.Now(),
	}}))
	require.NoError(t, s.db.CustomProviders().CreateProvider(t.Context(), store.CustomProvider{
		ID: "custom-1", TenantID: store.DefaultTenantID, DisplayName: "My Custom", Dialect: "openai", BaseURL: "https://x",
	}))
	rec := doJSON(t, h, http.MethodGet, "/payments/providers", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	list := decodeBody(t, rec)["providers"].([]any)
	require.Equal(t, []any{
		map[string]any{"id": "custom-1", "label": "My Custom"},
		map[string]any{"id": "zeta", "label": "zeta"},
	}, list)
}
