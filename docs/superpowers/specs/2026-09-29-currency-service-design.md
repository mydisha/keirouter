# Currency Service (IDR↔USD) — Design

Date: 2026-09-29
Branch: `feat/currency`
Status: approved for implementation

## Goal

Provide a stored, refreshable **USD→IDR exchange rate** as the foundation for a
future IDR payment-gateway top-up that converts IDR to USD credit. Operators can
see and control the rate from a new **Settings → Currency** tab: auto-refresh
with a configurable interval (default 24h), a manual refresh button, and a
manual override.

This branch ships the currency service + settings only. It does **not** ship the
top-up conversion flow itself.

## Non-goals

- No payment-gateway integration, invoices, or checkout.
- No IDR→USD-credit conversion endpoint and no change to the top-up handler.
- No multi-currency ledger. Base is always USD; the only quote stored is IDR.
- No change to the public landing/portal endpoints.

## External source (verified)

`GET https://open.er-api.com/v6/latest/USD` (free, no key). Verified live
response shape:

```json
{
  "result": "success",
  "time_last_update_unix": 1790640151,
  "time_next_update_unix": 1790727981,
  "base_code": "USD",
  "rates": { "USD": 1, "IDR": 16665.0, "...": 0 }
}
```

Parsing rules: require `result == "success"` and a finite `rates.IDR > 0`;
otherwise treat the response as a failure and keep the previous rate.

## Data model

No DB migration. Reuse the existing key/value `SettingsRepo` (same pattern as
`endpoint_settings`), under key `currency_settings`, holding one JSON blob with
both the config and the last-fetched rate state:

```go
type CurrencySettings struct {
    // Config.
    AutoRefreshEnabled bool   `json:"auto_refresh_enabled"` // default true
    RefreshIntervalH   int    `json:"refresh_interval_h"`   // default 24, min 1
    OverrideEnabled    bool   `json:"override_enabled"`     // default false
    OverrideRate       float64 `json:"override_rate"`       // IDR per 1 USD
    SourceURL          string `json:"source_url"`           // default er-api URL

    // Rate state (written by refresh, not by the settings form).
    Rate      float64 `json:"rate"`
    FetchedAt string  `json:"fetched_at"` // RFC3339, empty until first fetch
    Source    string  `json:"source"`     // "api" | "override" | "default" | "none"
    LastError string  `json:"last_error"` // last fetch error, cleared on success
}
```

`Rate` is USD→IDR (`IDR` per 1 USD). IDR→USD conversion is `usd = idr / Rate`;
it is only meaningful when a rate exists.

## Backend — `backend/internal/currency/service.go`

A `Service` (value, like the other gateway helpers) that owns load/save of the
blob and the background refresh loop.

- `Load(ctx) CurrencySettings` — read blob, apply defaults for missing/invalid
  fields (default interval 24, default URL).
- `Save(ctx, CurrencySettings) error` — validate (`RefreshIntervalH >= 1`,
  override rate `> 0` when enabled, URL parses as http/https) and persist.
- `Current(ctx) (rate float64, source string, fetchedAt string, ok bool)` —
  the single entry point the future top-up conversion will call.
  Resolution order: **override enabled and valid → override rate**; else
  **last fetched rate**; else `ok=false`.
- `Refresh(ctx) (CurrencySettings, error)` — fetch `SourceURL` with a bounded
  timeout, parse per the rules above, on success store `Rate/FetchedAt/Source="api"`
  and clear `LastError`; on failure store `LastError` and **keep the old rate**.
- `Start(ctx)` — background goroutine (pattern of `heartbeat.go` / `system.go`):
  on start, if no rate yet and auto-refresh on, refresh once; then a ticker that
  re-reads settings every tick (so interval changes take effect without restart)
  and refreshes when auto-refresh is enabled and the interval has elapsed.

Fail-closed: when no rate exists and no override is set, `Current` returns
`ok=false`; the future conversion must reject rather than guess a rate.

### Handler + routes (in `admin.go` route block)

- `GET  /api/settings/currency` → `{ config, effective_rate, source, fetched_at, last_error }`
  where `effective_rate` reflects override (if enabled) or the fetched rate.
- `POST /api/settings/currency` → update config fields.
- `POST /api/settings/currency/refresh` → manual refresh, returns the updated
  status (rate, source, fetched_at, last_error). Non-2xx only for transport
  errors; a failed upstream fetch returns 200 with `last_error` set so the UI
  can show it.

## Frontend

### `frontend/src/lib/api.ts`
- `CurrencySettings` type mirroring the JSON above.
- `currencySettings()`, `updateCurrencySettings(patch)`,
  `refreshCurrency()`.

### `frontend/src/pages/Settings.tsx`
New tab **"Currency"** (`Coins`/`CircleDollarSign` icon) added to
`settingsTabs` + `SettingsTab` union, rendered like the other tabs. Contents:
- Toggle **Auto refresh**; number input **Interval (hours)** (min 1, default 24).
- Toggle **Override rate**; number input **IDR per USD** (enabled only when
  override is on).
- Text input **Source URL**.
- Button **Refresh now** (calls the refresh endpoint; shows spinner/toast).
- Status panel: effective rate, source (`api` / `override` / `default` / `none`),
  last updated, and `last_error` when present.
- Save via the existing settings save pattern (`useMutation` + `toast`).

## Verification plan

- Go unit test (`currency/service_test.go`, `node`-free): parse a success body
  and a malformed/missing-IDR body using `httptest`; assert `Refresh` stores the
  rate on success and keeps the old rate + sets `LastError` on failure.
- Go unit test: `Current` resolution — override wins when enabled; falls back to
  fetched rate; `ok=false` when neither; invalid override rate falls back.
- Go unit test: `Save` validation (interval ≥ 1; non-positive override rejected;
  bad URL rejected).
- Frontend: `npm run typecheck`.
- Manual: rebuild image + container, open Settings → Currency, set interval,
  toggle override, click Refresh now, confirm rate + timestamp + source update
  and persist across reload; confirm `GET /api/settings/currency` reflects it.

## Risks / follow-ups

- The free er-api endpoint is rate-limited and updates ~daily; the default 24h
  interval matches that. A failed fetch never wipes a good stored rate.
- Override is a total replacement (per approved behavior): while enabled the API
  rate is ignored, and `source` reports `"override"` so the UI makes that clear.
- Future work (not this branch): wire `Current` into the top-up conversion and
  surface the IDR amount on the purchase flow.
