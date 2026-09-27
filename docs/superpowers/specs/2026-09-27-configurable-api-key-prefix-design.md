# Configurable API key prefix — Design Spec

Date: 2026-09-27
Branch: `feat/prefix-api`
Status: Approved (design), pending implementation plan

## 1. Goal

Let the operator set the human-visible prefix used for **newly created** API
keys from the dashboard Settings page. Entering `tkr` (or `tkr_`) means a newly
created key looks like `tkr_xxxxxxxx…`. Default stays `kr_`.

## 2. Non-goals

- No change to how existing keys authenticate. Old `kr_…` keys keep working
  unchanged (see §4).
- No re-keying, migration, or rewrite of already-issued keys.
- No change to the key's entropy, hashing, lookup index, or display masking
  rules beyond substituting the prefix.
- No per-tenant or per-key prefix. One global prefix for the deployment.
- No new frontend runtime dependency.

## 3. Findings (evidence)

- `crypto.KeyPrefix = "kr_"` is the single issued-prefix constant; it is used in
  `GenerateAPIKey` and `maskKey`.
  (`backend/internal/crypto/apikey.go:19,59,110,114`)
- All key creation funnels through `identity.Service.Generate` →
  `crypto.GenerateAPIKey`.
  (`backend/internal/identity/identity.go:78`)
- Callers of key generation are exactly two:
  - dashboard create: `s.identity.Generate(...)` (`backend/internal/gateway/admin.go:435`)
  - CLI bootstrap: `identity.New(db.APIKeys()).Create(...)` (`backend/internal/app/bootstrap.go:37`)
- Authentication never parses the prefix. `Authenticate` computes
  `crypto.LookupHash(fullPlaintext)` and verifies argon2 over the entire
  plaintext, so the prefix is cosmetic with respect to verification.
  (`backend/internal/identity/identity.go:113,141`;
  `backend/internal/gateway/middleware.go:37`)
- Settings persist as JSON blobs behind `store.SettingsRepo`; the gateway already
  owns `Server.settings` and has a `branding_settings` blob surfaced through the
  Branding tab. (`backend/internal/gateway/branding.go:10`; `backend/internal/gateway/server.go:177`)
- The frontend contains no `kr_` literal and no prefix parsing. (`grep frontend/src`)

## 4. Why old keys keep working (no code required)

`Authenticate` hashes and verifies the **full** presented string; it does not
strip or compare a prefix. Changing the issued prefix therefore cannot invalidate
existing keys. This is a load-bearing invariant and gets an explicit regression
test (see §7) rather than being left as an assumption.

## 5. Decision

Persist the prefix inside the **existing `branding_settings` JSON blob** and edit
it on the existing **Branding** tab. Reuse `loadBrandingSettings`,
`POST /settings/branding`, and the Branding save mutation. No new endpoint, no
new settings key, no new tab.

Rejected alternatives:

- **Dedicated settings key + endpoint** — more surface area for no functional
  gain; branding is already the "identity of this deployment" blob.
- **Config/env only** — the request explicitly asks for a Settings-page control.

## 6. Design

### 6.1 Backend — crypto (pure)

`backend/internal/crypto/apikey.go`:

- Rename the constant `KeyPrefix` → `DefaultKeyPrefix` (value stays `"kr_"`).
- `GenerateAPIKey(prefix string) (GeneratedKey, error)` — prepends the supplied
  prefix; an empty prefix falls back to `DefaultKeyPrefix`.
- `maskKey(plaintext, prefix string) string` — renders the masked display using
  the supplied prefix instead of the constant.
- Update `apikey_test.go` and `identity_test.go` for the new signatures
  (they are in-package and call these directly).

`backend/internal/crypto` stays free of config/DB dependencies.

### 6.2 Backend — identity

`backend/internal/identity/identity.go`:

- `Service` holds the active prefix (guarded by a mutex; read on the generate
  path, written on settings change).
- `New(keys)` initializes it to `DefaultKeyPrefix`.
- `SetKeyPrefix(prefix string)` stores the raw value (callers pass a normalized
  value; unknown/empty remains `DefaultKeyPrefix`).
- `Generate` calls `crypto.GenerateAPIKey(s.keyPrefix())`.
- New `NormalizePrefix(raw string) (string, bool)`:
  1. trim space, lowercase;
  2. if empty → `("kr_", true)` (explicit reset to default);
  3. strip leading/trailing `_`;
  4. if the remaining token does not match `^[a-z0-9]{1,16}$` → `("", false)`;
  5. otherwise return `token + "_"`, `true`.
  Backend is the trust boundary: invalid input fails closed (HTTP 400).

### 6.3 Backend — gateway (persistence + wiring)

`backend/internal/gateway/branding.go`:

- Add `APIKeyPrefix string \`json:"api_key_prefix"\`` to `BrandingSettings`.
- `defaultBrandingSettings()` sets `APIKeyPrefix: "kr_"`.
- `loadBrandingSettings` normalizes the stored value via
  `identity.NormalizePrefix`; empty or invalid falls back to `"kr_"`.
- `adminUpdateBranding`: when `api_key_prefix` is present, normalize it; on
  failure return `400 "api_key_prefix must be 1-16 lowercase letters or digits"`.
  On a successful persist, call `s.identity.SetKeyPrefix(prefix)` so the change
  takes effect without a restart.
- Add an exported helper `LoadAPIKeyPrefix(ctx, *store.SettingsRepo) string` so
  non-`Server` callers (bootstrap) read the same persisted value through the
  same normalization.

### 6.4 Backend — startup (bootstrap)

- `backend/internal/app/app.go`: after `idSvc := identity.New(...)`, set the
  prefix from persisted branding (`gateway.LoadAPIKeyPrefix(ctx, db.Settings())`).
  This ordering avoids an import cycle: `gateway` imports `identity`, so `app`
  may import `gateway`.
- `backend/internal/app/bootstrap.go`: same, immediately after
  `identity.New(db.APIKeys())`, so `keirouter -bootstrap` honors the setting.

### 6.5 Frontend

- `frontend/src/lib/api.ts`: add `api_key_prefix: string` to `EndpointSettings`-
  style `BrandingSettings` type (the Branding tab's own type).
- `frontend/src/pages/Settings.tsx` (Branding tab): add an **API Key Prefix**
  `Input` with helper text — "Used for newly created keys. Enter `tkr` or `tkr_`
  → new keys look like `tkr_xxxx`. Existing keys are unaffected." Client-side
  preview/hint only; the backend normalizes and validates on save.
- Saving reuses the existing Branding `save` mutation; no new query key.

## 7. Testing

| Layer | Test |
|---|---|
| `crypto` | custom prefix produces plaintext with that prefix; `maskKey` masks with it; empty prefix → `kr_`. |
| `identity` | `NormalizePrefix` cases (empty→`kr_`, `tkr`/`tkr_`/`TKR_`→`tkr_`, `tkr!`/over-16 → reject). `SetKeyPrefix("tkr_")` → `Generate` yields `tkr_…`. **Regression:** a key generated under `kr_`, then prefix switched to `tkr_`, still authenticates. |
| `gateway` | `POST /settings/branding {api_key_prefix:"tkr_"}` persists and returns `tkr_`; invalid value → 400; `GET` round-trips; `loadBrandingSettings` falls back to `kr_` on empty/invalid stored value. |

Commands: `go build ./...`; `go test ./internal/crypto/ ./internal/identity/ ./internal/gateway/`; `npm run typecheck`; `npm run build`.

## 8. Out of scope

- Existing-key display strings are not rewritten.
- No prefix length beyond 1–16, no uppercase (normalized to lowercase), no
  arbitrary symbols.
- `admin_foreign_import.go`'s foreign-key detection (`kr_`, `sk_`, `sk-`) is
  untouched; it is unrelated to issued-prefix generation.

## 9. Acceptance criteria

1. Setting prefix to `tkr` (or `tkr_`) in Settings makes a newly created key
   start with `tkr_`.
2. An existing `kr_` key created before the change still authenticates
   (regression test).
3. Invalid prefix input is rejected with 400 and never persisted.
4. Empty/unset prefix keeps `kr_`.
5. Backend build, focused Go tests, and frontend typecheck/build pass.
