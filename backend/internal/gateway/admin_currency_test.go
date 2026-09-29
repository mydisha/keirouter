package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/currency"
)

func TestCurrencyStatusShape(t *testing.T) {
	// The GET handler serializes the same status shape the UI expects.
	st := currencyStatus{
		Config:        currency.Defaults(currency.Settings{}),
		EffectiveRate: 0,
		Source:        currency.SourceNone,
	}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"config", "effective_rate", "source", "fetched_at", "last_error"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing key %q in %s", k, raw)
		}
	}
}

// currencyTestServer wires just the currency service the handler needs.
func currencyTestServer(t *testing.T) *Server {
	t.Helper()
	return &Server{currencySvc: currency.New(nil)}
}

func postCurrency(t *testing.T, s *Server, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/settings/currency", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.adminUpdateCurrency(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

// Contract: the writable API surface is exactly the five pointer fields. The UI
// sends only those (Finding 1); the strict decoder rejects the read-only state
// fields that the GET payload also carries, so the client must not echo the
// full config back.
func TestAdminUpdateCurrencyWritablePatch(t *testing.T) {
	s := currencyTestServer(t)

	code, out := postCurrency(t, s, `{
		"auto_refresh_enabled": false,
		"refresh_interval_h": 6,
		"override_enabled": true,
		"override_rate": 17000,
		"source_url": "https://example.com/rates"
	}`)
	if code != http.StatusOK {
		t.Fatalf("writable patch: status = %d, body = %v", code, out)
	}
	cfg, ok := out["config"].(map[string]any)
	if !ok {
		t.Fatalf("missing config in %v", out)
	}
	if cfg["refresh_interval_h"] != float64(6) || cfg["override_enabled"] != true {
		t.Fatalf("patch not applied: %v", cfg)
	}
	if out["effective_rate"] != float64(17000) {
		t.Fatalf("override should resolve as effective rate, got %v", out["effective_rate"])
	}
}

func TestAdminUpdateCurrencyRejectsFullConfigBody(t *testing.T) {
	s := currencyTestServer(t)
	// The old UI posted the full config; rate/fetched_at/source/last_error are
	// not accepted by the patch decoder, which is why the UI now sends the five
	// writable fields only.
	code, out := postCurrency(t, s, `{
		"auto_refresh_enabled": true,
		"refresh_interval_h": 24,
		"override_enabled": false,
		"override_rate": 0,
		"source_url": "https://example.com/rates",
		"rate": 16000,
		"fetched_at": "2026-01-01T00:00:00Z",
		"source": "api",
		"last_error": ""
	}`)
	if code != http.StatusBadRequest {
		t.Fatalf("full config body should be rejected, status = %d, body = %v", code, out)
	}
}

func TestAdminUpdateCurrencyRejectsInvalidConfig(t *testing.T) {
	s := currencyTestServer(t)
	code, _ := postCurrency(t, s, `{"override_enabled": true, "override_rate": 0}`)
	if code != http.StatusBadRequest {
		t.Fatalf("override enabled with zero rate: status = %d, want 400", code)
	}
}

func TestAdminGetCurrencyShape(t *testing.T) {
	s := currencyTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/settings/currency", nil)
	rec := httptest.NewRecorder()
	s.adminGetCurrency(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"config", "effective_rate", "source", "fetched_at", "last_error"} {
		if _, ok := out[k]; !ok {
			t.Fatalf("GET missing key %q in %s", k, rec.Body.String())
		}
	}
}

