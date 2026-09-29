# Currency Service (IDR↔USD) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store a refreshable USD→IDR rate with auto/manual refresh and a manual override, controllable from a new Settings → Currency tab.

**Architecture:** A `backend/internal/currency` package owns load/save of one JSON blob in the existing key/value `SettingsRepo` (key `currency_settings`) plus a background refresh goroutine. Pure helpers (`ParseUSDIDR`, `Defaults`, `Validate`, `Resolve`) carry the logic and are unit-tested without a DB. Gateway handlers expose GET/POST/refresh endpoints; the React Settings page adds a Currency tab.

**Tech Stack:** Go 1.26 (stdlib `net/http`, `encoding/json`, `time`), existing `store.SettingsRepo`, React 19 + TypeScript + TanStack Query, existing `components/ui` primitives.

## Global Constraints

- No DB migration. Persist via `store.SettingsRepo` (`Get(ctx,key) (string,error)`, `Set(ctx,key,value string) error`), same pattern as `endpoint_settings`.
- Base currency is always USD; only the IDR quote is stored. Rate is USD→IDR (IDR per 1 USD).
- Fail-closed: when no rate and no override exist, `Resolve` returns `ok=false`; the future conversion must reject, never guess.
- A failed upstream fetch must never wipe a good stored rate — it only records `last_error`.
- Override is a total replacement: while enabled, the API rate is ignored and `source` reports `"override"`.
- Defaults: auto-refresh on, interval 24h (min 1), source `https://open.er-api.com/v6/latest/USD`.
- Backend verification commands run from `backend/` (`go test ./...`, `go vet ./...`); frontend from `frontend/` (`npm run typecheck`).
- Follow the existing file/route/UI patterns; no new dependencies.

---

### Task 1: Currency package — pure helpers

**Files:**
- Create: `backend/internal/currency/service.go`
- Test: `backend/internal/currency/service_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Settings struct{...}` (JSON tags per spec)
  - `const SettingsKey = "currency_settings"`, `DefaultSourceURL`, `DefaultIntervalH = 24`
  - `const SourceAPI = "api"`, `SourceOverride = "override"`, `SourceNone = "none"`
  - `func ParseUSDIDR(body []byte) (float64, error)`
  - `func Defaults(s Settings) Settings`
  - `func Validate(s Settings) error`
  - `func Resolve(s Settings) (rate float64, source string, ok bool)`

- [ ] **Step 1: Write the failing test**

Create `backend/internal/currency/service_test.go`:

```go
package currency

import (
	"strings"
	"testing"
)

func TestParseUSDIDR(t *testing.T) {
	body := []byte(`{"result":"success","base_code":"USD","rates":{"USD":1,"IDR":16665.5}}`)
	rate, err := ParseUSDIDR(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rate != 16665.5 {
		t.Fatalf("rate = %v, want 16665.5", rate)
	}
}

func TestParseUSDIDRRejectsFailureAndMissingIDR(t *testing.T) {
	cases := []string{
		`{"result":"error","rates":{"IDR":16665}}`,
		`{"result":"success","rates":{"EUR":0.87}}`,
		`{"result":"success","rates":{"IDR":0}}`,
		`not json`,
	}
	for _, c := range cases {
		if _, err := ParseUSDIDR([]byte(c)); err == nil {
			t.Fatalf("expected error for %q", c)
		}
	}
}

func TestDefaults(t *testing.T) {
	d := Defaults(Settings{})
	if !d.AutoRefreshEnabled || d.RefreshIntervalH != DefaultIntervalH || d.SourceURL != DefaultSourceURL {
		t.Fatalf("unexpected defaults: %+v", d)
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(Defaults(Settings{})); err != nil {
		t.Fatalf("defaults should validate: %v", err)
	}
	bad := Defaults(Settings{}); bad.RefreshIntervalH = 0
	if err := Validate(bad); err == nil || !strings.Contains(err.Error(), "refresh_interval_h") {
		t.Fatalf("interval 0 should fail, got %v", err)
	}
	bad = Defaults(Settings{}); bad.OverrideEnabled = true; bad.OverrideRate = 0
	if err := Validate(bad); err == nil {
		t.Fatal("override enabled with zero rate should fail")
	}
	bad = Defaults(Settings{}); bad.SourceURL = "ftp://x"
	if err := Validate(bad); err == nil {
		t.Fatal("non-http source url should fail")
	}
}

func TestResolve(t *testing.T) {
	// Override wins when enabled and positive.
	rate, src, ok := Resolve(Settings{OverrideEnabled: true, OverrideRate: 17000, Rate: 16000, Source: SourceAPI})
	if !ok || rate != 17000 || src != SourceOverride {
		t.Fatalf("override: got rate=%v src=%q ok=%v", rate, src, ok)
	}
	// Falls back to the fetched rate.
	rate, src, ok = Resolve(Settings{Rate: 16000, Source: SourceAPI})
	if !ok || rate != 16000 || src != SourceAPI {
		t.Fatalf("fetched: got rate=%v src=%q ok=%v", rate, src, ok)
	}
	// Invalid override falls back to fetched rate.
	rate, src, ok = Resolve(Settings{OverrideEnabled: true, OverrideRate: 0, Rate: 16000, Source: SourceAPI})
	if !ok || rate != 16000 || src != SourceAPI {
		t.Fatalf("invalid override fallback: got rate=%v src=%q ok=%v", rate, src, ok)
	}
	// Nothing available.
	if _, _, ok := Resolve(Settings{}); ok {
		t.Fatal("empty settings should not resolve")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/currency/`
Expected: FAIL — package/undefined symbols.

- [ ] **Step 3: Write minimal implementation**

Create `backend/internal/currency/service.go`:

```go
// Package currency stores a refreshable USD->IDR exchange rate used to convert
// IDR payment-gateway top-ups into USD credit. Persistence reuses the existing
// key/value SettingsRepo; the rate and its fetch metadata live in one JSON blob.
package currency

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
)

// SettingsKey is the settings-store key holding the currency JSON blob.
const SettingsKey = "currency_settings"

// DefaultSourceURL is the free, key-less exchange-rate endpoint (verified shape:
// {"result":"success","rates":{"IDR":<float>,"...":<float>}}).
const DefaultSourceURL = "https://open.er-api.com/v6/latest/USD"

// DefaultIntervalH is the default auto-refresh interval in hours.
const DefaultIntervalH = 24

// Rate source labels reported to the UI.
const (
	SourceAPI      = "api"
	SourceOverride = "override"
	SourceNone     = "none"
)

// Settings is the persisted currency configuration plus last-fetched rate state.
type Settings struct {
	AutoRefreshEnabled bool    `json:"auto_refresh_enabled"`
	RefreshIntervalH   int     `json:"refresh_interval_h"`
	OverrideEnabled    bool    `json:"override_enabled"`
	OverrideRate       float64 `json:"override_rate"`
	SourceURL          string  `json:"source_url"`

	Rate      float64 `json:"rate"`
	FetchedAt string  `json:"fetched_at"`
	Source    string  `json:"source"`
	LastError string  `json:"last_error"`
}

// Defaults fills unset/invalid fields with their defaults.
func Defaults(s Settings) Settings {
	if s.RefreshIntervalH <= 0 {
		s.RefreshIntervalH = DefaultIntervalH
	}
	if strings.TrimSpace(s.SourceURL) == "" {
		s.SourceURL = DefaultSourceURL
	}
	return s
}

// Validate rejects a configuration that cannot be persisted or used.
func Validate(s Settings) error {
	if s.RefreshIntervalH < 1 {
		return errors.New("refresh_interval_h must be at least 1")
	}
	if s.OverrideEnabled && !(s.OverrideRate > 0) {
		return errors.New("override_rate must be positive when override is enabled")
	}
	u, err := url.Parse(strings.TrimSpace(s.SourceURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("source_url must be a valid http(s) URL")
	}
	return nil
}

// Resolve returns the effective USD->IDR rate and its source. Override wins when
// enabled and valid; otherwise the last fetched rate is used; otherwise ok=false.
func Resolve(s Settings) (rate float64, source string, ok bool) {
	if s.OverrideEnabled && s.OverrideRate > 0 && !math.IsInf(s.OverrideRate, 0) {
		return s.OverrideRate, SourceOverride, true
	}
	if s.Rate > 0 && !math.IsInf(s.Rate, 0) {
		return s.Rate, SourceAPI, true
	}
	return 0, SourceNone, false
}

// ParseUSDIDR extracts the IDR rate from an er-api response body.
func ParseUSDIDR(body []byte) (float64, error) {
	var resp struct {
		Result string             `json:"result"`
		Rates  map[string]float64 `json:"rates"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("currency: decode response: %w", err)
	}
	if resp.Result != "success" {
		return 0, fmt.Errorf("currency: provider returned result %q", resp.Result)
	}
	rate, ok := resp.Rates["IDR"]
	if !ok || rate <= 0 || math.IsInf(rate, 0) || math.IsNaN(rate) {
		return 0, errors.New("currency: response has no valid IDR rate")
	}
	return rate, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/currency/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/currency/service.go backend/internal/currency/service_test.go
git commit -m "feat(currency): pure parse/defaults/validate/resolve helpers"
```

---

### Task 2: Currency service — load/save/fetch/refresh/background loop

**Files:**
- Modify: `backend/internal/currency/service.go`
- Test: `backend/internal/currency/service_test.go` (add)

**Interfaces:**
- Consumes: `store.SettingsRepo.Get/Set`; Task 1 helpers.
- Produces:
  - `func New(settings *store.SettingsRepo) *Service`
  - `func (svc *Service) Load(ctx context.Context) Settings`
  - `func (svc *Service) Save(ctx context.Context, s Settings) error`
  - `func (svc *Service) Fetch(ctx context.Context, sourceURL string) (float64, error)`
  - `func (svc *Service) Refresh(ctx context.Context) (Settings, error)`
  - `func (svc *Service) Current(ctx context.Context) (rate float64, source string, ok bool)`
  - `func (svc *Service) Start(ctx context.Context)`

- [ ] **Step 1: Write the failing test**

Append to `backend/internal/currency/service_test.go`:

```go
import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":"success","rates":{"IDR":16600}}`))
	}))
	defer srv.Close()

	svc := New(nil)
	rate, err := svc.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if rate != 16600 {
		t.Fatalf("rate = %v, want 16600", rate)
	}
}

func TestFetchRejectsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := New(nil).Fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestLoadSaveRoundTripNilRepo(t *testing.T) {
	svc := New(nil)
	// With no repo, Load returns defaults and Save is a no-op (best-effort).
	if got := svc.Load(context.Background()); got.RefreshIntervalH != DefaultIntervalH {
		t.Fatalf("load defaults: %+v", got)
	}
	if err := svc.Save(context.Background(), Defaults(Settings{})); err != nil {
		t.Fatalf("save without repo: %v", err)
	}
}

func TestRefreshKeepsOldRateOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	var saved []Settings
	svc := New(nil)
	svc.persist = func(_ context.Context, s Settings) error { saved = append(saved, s); return nil }
	svc.initial = Settings{SourceURL: srv.URL, Rate: 16000, Source: SourceAPI, FetchedAt: time.Now().UTC().Format(time.RFC3339), RefreshIntervalH: 24}

	out, err := svc.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if out.Rate != 16000 {
		t.Fatalf("old rate must be kept, got %v", out.Rate)
	}
	if out.LastError == "" {
		t.Fatal("last_error must be set")
	}
	if len(saved) == 0 || saved[len(saved)-1].LastError == "" {
		t.Fatal("failure state must be persisted")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/currency/`
Expected: FAIL — `New`, `Fetch`, `Refresh`, fields `persist`/`initial` undefined.

- [ ] **Step 3: Write minimal implementation**

Add to `backend/internal/currency/service.go` (imports: add `context`, `io`, `net/http`, `os`, `time`, and the store package):

```go
// Service owns currency settings persistence and a background refresh loop.
type Service struct {
	settings *store.SettingsRepo

	// persist and initial are seams for tests; when nil they fall back to
	// settings.Set / settings.Get.
	persist func(ctx context.Context, s Settings) error
	initial Settings
}

// New returns a currency service backed by the given settings repo.
func New(settings *store.SettingsRepo) *Service { return &Service{settings: settings} }

func (svc *Service) readRaw(ctx context.Context) string {
	if svc.settings == nil {
		return ""
	}
	raw, err := svc.settings.Get(ctx, SettingsKey)
	if err != nil {
		return ""
	}
	return raw
}

func (svc *Service) writeRaw(ctx context.Context, s Settings) error {
	if svc.persist != nil {
		return svc.persist(ctx, s)
	}
	if svc.settings == nil {
		return nil
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return svc.settings.Set(ctx, SettingsKey, string(raw))
}

// Load reads the persisted settings, applying defaults. Never errors.
func (svc *Service) Load(ctx context.Context) Settings {
	if svc.initial != (Settings{}) {
		return Defaults(svc.initial)
	}
	raw := svc.readRaw(ctx)
	if raw == "" {
		return Defaults(Settings{})
	}
	var s Settings
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return Defaults(Settings{})
	}
	return Defaults(s)
}

// Save validates and persists settings.
func (svc *Service) Save(ctx context.Context, s Settings) error {
	s = Defaults(s)
	if err := Validate(s); err != nil {
		return err
	}
	return svc.writeRaw(ctx, s)
}

// Fetch retrieves the USD->IDR rate from sourceURL.
func (svc *Service) Fetch(ctx context.Context, sourceURL string) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("currency: upstream status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	return ParseUSDIDR(body)
}

// Refresh fetches the rate and persists it. On failure it keeps the previous
// rate and records the error; the returned error mirrors the failure.
func (svc *Service) Refresh(ctx context.Context) (Settings, error) {
	s := svc.Load(ctx)
	rate, err := svc.Fetch(ctx, s.SourceURL)
	if err != nil {
		s.LastError = err.Error()
		_ = svc.writeRaw(ctx, s)
		return s, err
	}
	s.Rate = rate
	s.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	s.Source = SourceAPI
	s.LastError = ""
	if werr := svc.writeRaw(ctx, s); werr != nil {
		return s, werr
	}
	return s, nil
}

// Current returns the effective rate for consumers (e.g. top-up conversion).
func (svc *Service) Current(ctx context.Context) (float64, string, bool) {
	return Resolve(svc.Load(ctx))
}

// Start launches the background refresh loop. It refreshes once on start when
// no rate exists and auto-refresh is on, then re-checks every minute and
// refreshes when the configured interval has elapsed.
func (svc *Service) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s := svc.Load(ctx)
				if !s.AutoRefreshEnabled {
					continue
				}
				last := time.Time{}
				if s.FetchedAt != "" {
					if t, err := time.Parse(time.RFC3339, s.FetchedAt); err == nil {
						last = t
					}
				}
				due := s.Rate == 0 || time.Since(last) >= time.Duration(s.RefreshIntervalH)*time.Hour
				if due {
					if _, err := svc.Refresh(ctx); err != nil {
						slog.Default().Warn("currency refresh failed", "err", err)
					}
				}
			}
		}
	}()
}
```

Add imports `"log/slog"` and `"github.com/mydisha/keirouter/backend/internal/store"`.

Note: `svc.initial != (Settings{})` uses struct comparability — `Settings` is all comparable fields, which is fine. The test uses `svc.persist` + `svc.initial` because they are unexported test seams in the same package.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/currency/ && go vet ./internal/currency/`
Expected: PASS, vet clean.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/currency/service.go backend/internal/currency/service_test.go
git commit -m "feat(currency): service with fetch, refresh, and background loop"
```

---

### Task 3: Gateway handlers and routes

**Files:**
- Create: `backend/internal/gateway/admin_currency.go`
- Modify: `backend/internal/gateway/admin.go` (route block, after line 123 `r.Post("/settings/proxy-test", ...)`)
- Modify: `backend/internal/gateway/server.go` (field + `NewServer` + `Start`)

**Interfaces:**
- Consumes: `currency.Service` (Task 2); `s.settings` (`*store.SettingsRepo`, server.go:62); `writeJSON`, `writeError`, `decodeJSON`.
- Produces:
  - `Server.currencySvc *currency.Service`
  - `func (s *Server) adminGetCurrency(w, r)`
  - `func (s *Server) adminUpdateCurrency(w, r)`
  - `func (s *Server) adminRefreshCurrency(w, r)`

- [ ] **Step 1: Write the failing test**

Create `backend/internal/gateway/admin_currency_test.go`:

```go
package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/gateway/ -run TestCurrencyStatusShape`
Expected: FAIL — `currencyStatus` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `backend/internal/gateway/admin_currency.go`:

```go
package gateway

import (
	"net/http"

	"github.com/mydisha/keirouter/backend/internal/currency"
)

// currencyStatus is the GET/refresh response shape consumed by the UI.
type currencyStatus struct {
	Config        currency.Settings `json:"config"`
	EffectiveRate float64           `json:"effective_rate"`
	Source        string            `json:"source"`
	FetchedAt     string            `json:"fetched_at"`
	LastError     string            `json:"last_error"`
}

func currencyStatusOf(s currency.Settings) currencyStatus {
	rate, source, _ := currency.Resolve(s)
	return currencyStatus{Config: s, EffectiveRate: rate, Source: source, FetchedAt: s.FetchedAt, LastError: s.LastError}
}

func (s *Server) adminGetCurrency(w http.ResponseWriter, r *http.Request) {
	if s.currencySvc == nil {
		writeJSON(w, http.StatusOK, currencyStatusOf(currency.Defaults(currency.Settings{})))
		return
	}
	writeJSON(w, http.StatusOK, currencyStatusOf(s.currencySvc.Load(r.Context())))
}

type currencyPatch struct {
	AutoRefreshEnabled *bool    `json:"auto_refresh_enabled"`
	RefreshIntervalH   *int     `json:"refresh_interval_h"`
	OverrideEnabled    *bool    `json:"override_enabled"`
	OverrideRate       *float64 `json:"override_rate"`
	SourceURL          *string  `json:"source_url"`
}

func (s *Server) adminUpdateCurrency(w http.ResponseWriter, r *http.Request) {
	if s.currencySvc == nil {
		writeError(w, http.StatusServiceUnavailable, "currency service unavailable")
		return
	}
	var patch currencyPatch
	if !decodeJSON(w, r, &patch) {
		return
	}
	cur := s.currencySvc.Load(r.Context())
	if patch.AutoRefreshEnabled != nil {
		cur.AutoRefreshEnabled = *patch.AutoRefreshEnabled
	}
	if patch.RefreshIntervalH != nil {
		cur.RefreshIntervalH = *patch.RefreshIntervalH
	}
	if patch.OverrideEnabled != nil {
		cur.OverrideEnabled = *patch.OverrideEnabled
	}
	if patch.OverrideRate != nil {
		cur.OverrideRate = *patch.OverrideRate
	}
	if patch.SourceURL != nil {
		cur.SourceURL = *patch.SourceURL
	}
	if err := s.currencySvc.Save(r.Context(), cur); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, currencyStatusOf(cur))
}

func (s *Server) adminRefreshCurrency(w http.ResponseWriter, r *http.Request) {
	if s.currencySvc == nil {
		writeError(w, http.StatusServiceUnavailable, "currency service unavailable")
		return
	}
	out, err := s.currencySvc.Refresh(r.Context())
	// A failed upstream fetch is a 200 with last_error set so the UI can show it.
	if err != nil && out.LastError == "" {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, currencyStatusOf(out))
}
```

Modify `backend/internal/gateway/admin.go` route block — add after line 123 (`r.Post("/settings/proxy-test", s.adminTestProxy)`):

```go
	r.Get("/settings/currency", s.adminGetCurrency)
	r.Post("/settings/currency", s.adminUpdateCurrency)
	r.Post("/settings/currency/refresh", s.adminRefreshCurrency)
```

Modify `backend/internal/gateway/server.go`:
- Add import `"github.com/mydisha/keirouter/backend/internal/currency"`.
- In the `Server` struct add field `currencySvc *currency.Service`.
- In `NewServer`, before `s.router = s.routes()`, add:

```go
	s.currencySvc = currency.New(d.Settings)
	if d.Settings != nil {
		s.currencySvc.Start(context.Background())
	}
```

Ensure `context` is imported in server.go (check; it already imports `context` for `reloadPricing func(context.Context) error`). Verify with `go vet` if not.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/gateway/ -run 'Currency|Settings' && go vet ./internal/gateway/`
Expected: PASS, vet clean.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/gateway/admin_currency.go backend/internal/gateway/admin_currency_test.go backend/internal/gateway/admin.go backend/internal/gateway/server.go
git commit -m "feat(gateway): currency settings + refresh endpoints"
```

---

### Task 4: Frontend API client

**Files:**
- Modify: `frontend/src/lib/api.ts` (types near `EndpointSettings`; methods after `updateEndpointSettings`, api.ts:1433)

**Interfaces:**
- Consumes: backend endpoints from Task 3.
- Produces: `CurrencyConfig`, `CurrencyStatus`, `api.currencySettings()`, `api.updateCurrencySettings(patch)`, `api.refreshCurrency()`.

- [ ] **Step 1: Add the types**

Add near the other settings types:

```ts
export interface CurrencyConfig {
  auto_refresh_enabled: boolean;
  refresh_interval_h: number;
  override_enabled: boolean;
  override_rate: number;
  source_url: string;
  rate: number;
  fetched_at: string;
  source: string;
  last_error: string;
}

export interface CurrencyStatus {
  config: CurrencyConfig;
  effective_rate: number;
  source: string;
  fetched_at: string;
  last_error: string;
}
```

- [ ] **Step 2: Add the client methods**

After `updateEndpointSettings` (api.ts:1433):

```ts
  currencySettings: () => request<CurrencyStatus>("GET", "/settings/currency"),
  updateCurrencySettings: (patch: Partial<Pick<CurrencyConfig, "auto_refresh_enabled" | "refresh_interval_h" | "override_enabled" | "override_rate" | "source_url">>) =>
    request<CurrencyStatus>("POST", "/settings/currency", patch),
  refreshCurrency: () => request<CurrencyStatus>("POST", "/settings/currency/refresh", {}),
```

- [ ] **Step 3: Verify types compile**

Run: `cd frontend && npm run typecheck`
Expected: PASS (the new API object keys type-check; unused-until-Task-5 is fine for `tsc`).

- [ ] **Step 4: Commit**

```bash
git add frontend/src/lib/api.ts
git commit -m "feat(api): currency settings client"
```

---

### Task 5: Settings → Currency tab

**Files:**
- Modify: `frontend/src/pages/Settings.tsx`

**Interfaces:**
- Consumes: `api.currencySettings/updateCurrencySettings/refreshCurrency`, `CurrencyStatus` (Task 4); `Card`, `SectionHeader`, `Field`, `Input`, `Toggle`, `Button`, `Spinner`, `ErrorBanner` from `../components/ui`; `useToast`.
- Produces: `settingsTabs` entry `"currency"`, union member, `CurrencyTab` component, render line.

- [ ] **Step 1: Register the tab**

In `Settings.tsx`:
- Add an icon to the lucide import (line 3-8), e.g. `CircleDollarSign`.
- Extend the union (line 23):

```ts
type SettingsTab = "saving" | "routing" | "network" | "branding" | "import-export" | "system" | "notifications" | "currency";
```

- Add to `settingsTabs` (after the `notifications` entry, line 32):

```ts
  { value: "currency" as const, label: "Currency", icon: CircleDollarSign },
```

- Add the type import: extend the `../lib/api` import (line 9) with `type CurrencyStatus`.

- Add the render line after the notifications render (line 160):

```tsx
            {tab === "notifications" && <NotificationTab />}
            {tab === "currency" && <CurrencyTab />}
```

- [ ] **Step 2: Implement the tab**

Add this component next to `NotificationTab` (e.g. after it, around line 1300):

```tsx
function CurrencyTab() {
  const qc = useQueryClient();
  const toast = useToast();
  const status = useQuery({ queryKey: ["currency-settings"], queryFn: () => api.currencySettings() });
  const [local, setLocal] = useState<CurrencyStatus["config"] | null>(null);

  useEffect(() => {
    if (status.data) setLocal(status.data.config);
  }, [status.data]);

  const save = useMutation({
    mutationFn: (patch: Partial<CurrencyStatus["config"]>) => api.updateCurrencySettings(patch),
    onSuccess: (data) => {
      setLocal(data.config);
      qc.setQueryData(["currency-settings"], data);
      toast.success("Currency settings saved");
    },
    onError: (e) => toast.error("Save failed", (e as Error).message),
  });

  const refresh = useMutation({
    mutationFn: () => api.refreshCurrency(),
    onSuccess: (data) => {
      setLocal(data.config);
      qc.setQueryData(["currency-settings"], data);
      if (data.last_error) toast.error("Refresh failed", data.last_error);
      else toast.success("Rate updated", `1 USD = ${data.effective_rate} IDR`);
    },
    onError: (e) => toast.error("Refresh failed", (e as Error).message),
  });

  const update = (patch: Partial<CurrencyStatus["config"]>) => {
    if (!local) return;
    const next = { ...local, ...patch };
    setLocal(next);
    save.mutate(patch);
  };

  if (status.isLoading || !local) return <Spinner />;
  if (status.isError) {
    return <ErrorBanner message={`Failed to load currency settings: ${(status.error as Error)?.message ?? "unknown error"}`} />;
  }

  const st = status.data!;
  const sourceLabel: Record<string, string> = {
    api: "Live API",
    override: "Manual override",
    none: "No rate yet",
  };

  return (
    <Card>
      <SectionHeader
        title="Currency"
        description="USD→IDR rate used to convert IDR top-ups into USD credit. Auto-refreshes on an interval, with an optional manual override."
        icon={CircleDollarSign}
      />
      <div className="divide-y divide-[var(--border)] border-t border-[var(--border)]">
        <div className="px-6 py-5">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <div className="flex items-center justify-between">
                <span className="text-sm font-medium">Auto refresh</span>
                <Toggle checked={local.auto_refresh_enabled} onChange={(v) => update({ auto_refresh_enabled: v })} />
              </div>
              <p className="mt-1 text-xs text-[var(--text-muted)]">Fetch the rate automatically in the background.</p>
            </div>
            <Field label="Refresh interval (hours)">
              <Input
                type="number"
                min={1}
                value={local.refresh_interval_h}
                onChange={(e) => update({ refresh_interval_h: Number(e.target.value) || 1 })}
              />
              <p className="mt-1 text-xs text-[var(--text-muted)]">Default 24. The upstream rate updates about once a day.</p>
            </Field>
          </div>
        </div>

        <div className="px-6 py-5">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <div className="flex items-center justify-between">
                <span className="text-sm font-medium">Override rate</span>
                <Toggle checked={local.override_enabled} onChange={(v) => update({ override_enabled: v })} />
              </div>
              <p className="mt-1 text-xs text-[var(--text-muted)]">While on, the API rate is ignored entirely.</p>
            </div>
            <Field label="IDR per 1 USD">
              <Input
                type="number"
                min={0}
                step="0.01"
                disabled={!local.override_enabled}
                value={local.override_rate}
                onChange={(e) => update({ override_rate: Number(e.target.value) || 0 })}
              />
            </Field>
          </div>
        </div>

        <div className="px-6 py-5">
          <Field label="Source URL">
            <Input value={local.source_url} onChange={(e) => update({ source_url: e.target.value })} />
          </Field>
        </div>

        <div className="flex flex-wrap items-center justify-between gap-3 px-6 py-5">
          <div className="text-sm">
            <p className="font-medium">
              1 USD = {st.effective_rate > 0 ? `${st.effective_rate} IDR` : "—"}
            </p>
            <p className="mt-0.5 text-xs text-[var(--text-muted)]">
              Source: {sourceLabel[st.source] ?? st.source}
              {st.fetched_at ? ` · updated ${new Date(st.fetched_at).toLocaleString()}` : ""}
            </p>
            {st.last_error && <p className="mt-1 text-xs text-red-500">Last error: {st.last_error}</p>}
          </div>
          <Button onClick={() => refresh.mutate()} disabled={refresh.isPending}>
            {refresh.isPending ? "Refreshing…" : "Refresh now"}
          </Button>
        </div>
      </div>
    </Card>
  );
}
```

- [ ] **Step 3: Verify types compile**

Run: `cd frontend && npm run typecheck`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/Settings.tsx
git commit -m "feat(ui): Settings > Currency tab"
```

---

### Task 6: End-to-end verification

**Files:** none (verification only).

- [ ] **Step 1: Backend suite**

Run: `cd backend && go test ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 2: Frontend build**

Run: `cd frontend && npm run typecheck && npm run build`
Expected: PASS.

- [ ] **Step 3: Manual against the running stack**

Rebuild and restart the localhost compose stack, then in the dashboard:
- Open Settings → Currency; confirm defaults (auto on, 24h, override off).
- Click **Refresh now**; confirm the rate + "updated" timestamp + source "Live API" appear (requires outbound network).
- Toggle **Override**, set e.g. `17000`, reload; confirm effective rate shows 17000 and source "Manual override", and the settings persist across reload.
- Confirm `curl -s -u :<pw> http://127.0.0.1:20180/api/settings/currency` returns the same status.

```bash
docker compose -f compose.localhost.yaml up -d --build
```

- [ ] **Step 4: Commit any fixes** (if verification surfaced changes)

---

## Self-Review

- **Spec coverage:** data model → Tasks 1-2; auto refresh + interval → Tasks 2 (Start) + 5; manual refresh → Tasks 3 (`adminRefreshCurrency`) + 5; override → Tasks 1 (`Resolve`) + 3 + 5; source URL → Tasks 1-3, 5; status/errors → Task 3 `currencyStatus`; Settings tab → Task 5; tests/typecheck → Tasks 1-2, 6. All spec sections mapped.
- **Placeholder scan:** no TBD/TODO; every code step has full code.
- **Type consistency:** `Settings`/`SettingsKey`/`DefaultSourceURL`/`DefaultIntervalH`/`SourceAPI|Override|None` defined in Task 1 and reused; `currencyStatus`/`currencyStatusOf`/`currencySvc` names consistent across Task 3; frontend `CurrencyConfig`/`CurrencyStatus`/`currencySettings`/`updateCurrencySettings`/`refreshCurrency` consistent across Tasks 4-5.
- **Note:** Task 2's tests use unexported seams (`svc.persist`, `svc.initial`) intentionally so `Load`/`Save`/`Refresh` are testable without a database; production behavior uses the real `SettingsRepo`.

## Risks / Follow-ups

- `Start` runs even when no rate exists; if auto-refresh is off it never fetches until the operator clicks Refresh. That matches the fail-closed design.
- The free er-api endpoint is rate-limited; the default 24h interval stays well within limits.
- Future (not this plan): call `currency.Service.Current` in the top-up conversion and show the IDR amount on the purchase flow.
