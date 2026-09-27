# Configurable API Key Prefix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the operator set the prefix used by newly created API keys from the dashboard Branding tab, while all previously issued keys keep authenticating unchanged.

**Architecture:** Parameterize `crypto.GenerateAPIKey` with a prefix, hold the active prefix in `identity.Service` (guarded, default `kr_`), persist it inside the existing `branding_settings` JSON blob, and wire it at startup for both the server and `-bootstrap`. Authentication is untouched because it hashes the full plaintext and never parses a prefix.

**Tech Stack:** Go 1.26 (chi, testify), React 19 + TypeScript + TanStack Query + Vite.

## Global Constraints

- Default prefix is exactly `"kr_"`.
- Prefix token charset: `^[a-z0-9]{1,16}$`; normalized form always ends with a single `_`.
- Invalid prefix input fails closed on the backend with HTTP 400 and is never persisted.
- Existing keys are never re-keyed, rewritten, or migrated.
- No new backend dependency; no new frontend runtime dependency.
- Do not change `admin_foreign_import.go`'s foreign-key detection (`kr_`, `sk_`, `sk-`).
- Follow existing repo patterns for settings blobs and handler validation.

---

### Task 1: Parameterize the crypto prefix

**Files:**
- Modify: `backend/internal/crypto/apikey.go`
- Test: `backend/internal/crypto/apikey_test.go`
- Test: `backend/internal/identity/identity_test.go:22` (compile fix for renamed const)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `crypto.DefaultKeyPrefix string = "kr_"`
  - `func GenerateAPIKey(prefix string) (GeneratedKey, error)`
  - `func maskKey(plaintext, prefix string) string`

- [ ] **Step 1: Write the failing test**

Add to `backend/internal/crypto/apikey_test.go`:

```go
func TestGenerateAPIKey_CustomPrefix(t *testing.T) {
	k, err := GenerateAPIKey("tkr_")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(k.Plaintext, "tkr_"), "plaintext must carry custom prefix")
	require.True(t, strings.HasPrefix(k.Display, "tkr_"), "display must carry custom prefix")
	require.NotContains(t, k.Display, "kr_")

	ok, err := VerifyAPIKey(k.Plaintext, k.Hash)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestGenerateAPIKey_EmptyPrefixUsesDefault(t *testing.T) {
	k, err := GenerateAPIKey("")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(k.Plaintext, DefaultKeyPrefix))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/crypto/ -run TestGenerateAPIKey_CustomPrefix -v`
Expected: FAIL — `too many arguments in call to GenerateAPIKey` (compile error).

- [ ] **Step 3: Update `apikey.go`**

Rename the constant and thread the prefix through:

```go
// DefaultKeyPrefix is the human-visible prefix used when none is configured.
const DefaultKeyPrefix = "kr_"
```

```go
func GenerateAPIKey(prefix string) (GeneratedKey, error) {
	if prefix == "" {
		prefix = DefaultKeyPrefix
	}
	raw := make([]byte, secretBytes)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return GeneratedKey{}, fmt.Errorf("generate key entropy: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)
	plaintext := prefix + secret

	hash, err := HashAPIKey(plaintext)
	if err != nil {
		return GeneratedKey{}, err
	}

	return GeneratedKey{
		Plaintext: plaintext,
		Hash:      hash,
		Lookup:    LookupHash(plaintext),
		Display:   maskKey(plaintext, prefix),
	}, nil
}
```

```go
func maskKey(plaintext, prefix string) string {
	body := strings.TrimPrefix(plaintext, prefix)
	if len(body) <= 8 {
		return prefix + "…"
	}
	return fmt.Sprintf("%s%s…%s", prefix, body[:4], body[len(body)-4:])
}
```

- [ ] **Step 4: Fix existing callers/const references**

In `backend/internal/crypto/apikey_test.go`: replace `GenerateAPIKey()` with `GenerateAPIKey("")`, and `KeyPrefix` with `DefaultKeyPrefix` (lines ~14, 18, 22, 26, 34).

In `backend/internal/identity/identity_test.go:22`: replace `crypto.KeyPrefix` with `crypto.DefaultKeyPrefix`.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/crypto/ ./internal/identity/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/crypto/apikey.go backend/internal/crypto/apikey_test.go backend/internal/identity/identity_test.go
git commit -m "refactor(crypto): parameterize API key prefix"
```

---

### Task 2: Identity prefix service + normalization

**Files:**
- Modify: `backend/internal/identity/identity.go`
- Test: `backend/internal/identity/identity_test.go`

**Interfaces:**
- Consumes: `crypto.DefaultKeyPrefix`, `crypto.GenerateAPIKey(prefix)` from Task 1.
- Produces:
  - `func NormalizePrefix(raw string) (string, bool)`
  - `func (s *Service) SetKeyPrefix(prefix string)`
  - `Service.Generate` now stamps the active prefix.

- [ ] **Step 1: Write the failing tests**

Add to `backend/internal/identity/identity_test.go` (add `"github.com/stretchr/testify/require"` to imports):

```go
func TestNormalizePrefix(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", crypto.DefaultKeyPrefix, true},
		{"_", crypto.DefaultKeyPrefix, true},
		{"tkr", "tkr_", true},
		{"tkr_", "tkr_", true},
		{"TKR_", "tkr_", true},
		{"  tkr  ", "tkr_", true},
		{"a", "a_", true},
		{"abc123", "abc123_", true},
		{"abcdefghijklmnop", "abcdefghijklmnop_", true}, // 16 chars: allowed
		{"tkr!", "", false},
		{"tk r", "", false},
		{"abcdefghijklmnopq", "", false}, // 17 chars: rejected
	}
	for _, c := range cases {
		got, ok := NormalizePrefix(c.in)
		require.Equal(t, c.ok, ok, "input %q", c.in)
		if c.ok {
			require.Equal(t, c.want, got, "input %q", c.in)
		}
	}
}

func TestSetKeyPrefixAppliesToNewKeys(t *testing.T) {
	s := New(nil)
	p, ok := NormalizePrefix("tkr")
	require.True(t, ok)
	s.SetKeyPrefix(p)

	issued, err := s.Generate("t", "p", "k")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(issued.Plaintext, "tkr_"))
	require.True(t, strings.HasPrefix(issued.Record.Display, "tkr_"))
}

func TestChangingPrefixKeepsOldKeysValid(t *testing.T) {
	s := New(nil)

	old, err := s.Generate("t", "p", "k")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(old.Plaintext, crypto.DefaultKeyPrefix))

	p, ok := NormalizePrefix("tkr")
	require.True(t, ok)
	s.SetKeyPrefix(p)

	// Requirement: a key issued before the prefix change must still authenticate.
	ok, err = crypto.VerifyAPIKey(old.Plaintext, old.Record.KeyHash)
	require.NoError(t, err)
	require.True(t, ok, "existing key must keep authenticating after prefix change")
	require.Equal(t, crypto.LookupHash(old.Plaintext), old.Record.LookupHash)

	// Newly issued keys use the new prefix.
	fresh, err := s.Generate("t", "p", "k")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(fresh.Plaintext, "tkr_"))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/identity/ -run 'TestNormalizePrefix|TestSetKeyPrefix|TestChangingPrefix' -v`
Expected: FAIL — `undefined: NormalizePrefix` / `s.SetKeyPrefix undefined`.

- [ ] **Step 3: Implement in `identity.go`**

Add `"regexp"` and `"strings"` to imports. Extend the struct and constructor:

```go
type Service struct {
	keys *store.APIKeyRepo

	prefixMu sync.RWMutex
	prefix   string

	authMu    sync.RWMutex
	authCache map[string]authCacheEntry
}

func New(keys *store.APIKeyRepo) *Service {
	return &Service{
		keys:      keys,
		prefix:    crypto.DefaultKeyPrefix,
		authCache: make(map[string]authCacheEntry),
	}
}

// keyPrefix returns the active issued-key prefix, defaulting when unset.
func (s *Service) keyPrefix() string {
	s.prefixMu.RLock()
	defer s.prefixMu.RUnlock()
	if s.prefix == "" {
		return crypto.DefaultKeyPrefix
	}
	return s.prefix
}

// SetKeyPrefix sets the prefix used for newly issued keys. It is safe for
// concurrent use and takes effect without restart. An empty value resets the
// default.
func (s *Service) SetKeyPrefix(prefix string) {
	if prefix == "" {
		prefix = crypto.DefaultKeyPrefix
	}
	s.prefixMu.Lock()
	s.prefix = prefix
	s.prefixMu.Unlock()
}
```

Add the normalizer (place near the top of the file, after imports):

```go
// prefixTokenRe matches an allowed prefix token: 1-16 lowercase letters or digits.
var prefixTokenRe = regexp.MustCompile(`^[a-z0-9]{1,16}$`)

// NormalizePrefix validates a user-supplied API key prefix and returns its
// canonical "token_" form. An empty or underscore-only input resets to the
// default. Invalid input returns ok=false and must be rejected by the caller.
func NormalizePrefix(raw string) (string, bool) {
	token := strings.Trim(strings.ToLower(strings.TrimSpace(raw)), "_")
	if token == "" {
		return crypto.DefaultKeyPrefix, true
	}
	if !prefixTokenRe.MatchString(token) {
		return "", false
	}
	return token + "_", true
}
```

Change `Generate` to stamp the active prefix:

```go
func (s *Service) Generate(tenantID, projectID, name string) (Issued, error) {
	gen, err := crypto.GenerateAPIKey(s.keyPrefix())
	if err != nil {
		return Issued{}, err
	}
	// ...unchanged...
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/identity/ ./internal/crypto/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/identity/identity.go backend/internal/identity/identity_test.go
git commit -m "feat(identity): configurable API key prefix with normalization"
```

---

### Task 3: Persist prefix in branding settings + apply live

**Files:**
- Modify: `backend/internal/gateway/branding.go`
- Test: `backend/internal/gateway/branding_test.go` (create)

**Interfaces:**
- Consumes: `identity.NormalizePrefix`, `identity.Service.SetKeyPrefix` from Task 2.
- Produces:
  - `BrandingSettings.APIKeyPrefix string` (json `api_key_prefix`)
  - `func LoadAPIKeyPrefix(ctx context.Context, settings *store.SettingsRepo) string`

- [ ] **Step 1: Write the failing tests**

Create `backend/internal/gateway/branding_test.go`:

```go
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func newBrandingGateway(t *testing.T) (*Server, *identity.Service) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	t.Cleanup(func() { _ = db.Close() })

	idSvc := identity.New(db.APIKeys())
	s := New(Deps{Config: config.Default(), DB: db, Settings: db.Settings(), Identity: idSvc})
	return s, idSvc
}

func TestUpdateBrandingPersistsKeyPrefix(t *testing.T) {
	s, idSvc := newBrandingGateway(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/settings/branding", strings.NewReader(`{"api_key_prefix":"tkr_"}`))
	s.adminUpdateBranding(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var got BrandingSettings
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "tkr_", got.APIKeyPrefix)

	// Persisted value round-trips through the loader.
	require.Equal(t, "tkr_", s.loadBrandingSettings(context.Background()).APIKeyPrefix)

	// The running identity service mints new keys with the new prefix.
	issued, err := idSvc.Generate(store.DefaultTenantID, "", "k")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(issued.Plaintext, "tkr_"))
}

func TestUpdateBrandingRejectsInvalidKeyPrefix(t *testing.T) {
	s, _ := newBrandingGateway(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/settings/branding", strings.NewReader(`{"api_key_prefix":"tkr!"}`))
	s.adminUpdateBranding(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// Nothing persisted: loader still returns the default.
	require.Equal(t, "kr_", s.loadBrandingSettings(context.Background()).APIKeyPrefix)
}

func TestLoadBrandingFallsBackForInvalidStoredPrefix(t *testing.T) {
	s, _ := newBrandingGateway(t)
	require.NoError(t, s.settings.Set(context.Background(), brandingSettingsKey, `{"name":"X","api_key_prefix":"BAD!"}`))
	require.Equal(t, "kr_", s.loadBrandingSettings(context.Background()).APIKeyPrefix)
}

func TestBrandingDefaultsKeyPrefix(t *testing.T) {
	s, _ := newBrandingGateway(t)
	require.Equal(t, "kr_", s.loadBrandingSettings(context.Background()).APIKeyPrefix)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway/ -run 'Branding|KeyPrefix' -v`
Expected: FAIL — `got.APIKeyPrefix undefined` (compile error).

- [ ] **Step 3: Implement in `branding.go`**

Add `"github.com/mydisha/keirouter/backend/internal/crypto"` and `"github.com/mydisha/keirouter/backend/internal/identity"` and `"github.com/mydisha/keirouter/backend/internal/store"` to imports (store may already be needed for the helper signature; add if absent).

Add the field:

```go
type BrandingSettings struct {
	Name         string `json:"name"`
	LogoURL      string `json:"logo_url"`
	FaviconURL   string `json:"favicon_url"`
	Tagline      string `json:"tagline"`
	ColorPalette string `json:"color_palette"`
	// APIKeyPrefix is the canonical "token_" prefix stamped onto newly created
	// API keys. Existing keys are unaffected.
	APIKeyPrefix string `json:"api_key_prefix"`
}
```

```go
func defaultBrandingSettings() BrandingSettings {
	return BrandingSettings{
		Name:         "KeiRouter",
		LogoURL:      "",
		FaviconURL:   "",
		Tagline:      "",
		ColorPalette: "sage-terra",
		APIKeyPrefix: crypto.DefaultKeyPrefix,
	}
}
```

In `loadBrandingSettings`, after the existing `if bs.Name == ""` backfill:

```go
	if normalized, ok := identity.NormalizePrefix(bs.APIKeyPrefix); ok {
		bs.APIKeyPrefix = normalized
	} else {
		bs.APIKeyPrefix = def.APIKeyPrefix
	}
```

In `adminUpdateBranding`, extend the patch struct and merge:

```go
	var patch struct {
		Name         *string `json:"name"`
		LogoURL      *string `json:"logo_url"`
		FaviconURL   *string `json:"favicon_url"`
		Tagline      *string `json:"tagline"`
		ColorPalette *string `json:"color_palette"`
		APIKeyPrefix *string `json:"api_key_prefix"`
	}
```

```go
	if patch.APIKeyPrefix != nil {
		normalized, ok := identity.NormalizePrefix(*patch.APIKeyPrefix)
		if !ok {
			writeError(w, http.StatusBadRequest, "api_key_prefix must be 1-16 lowercase letters or digits")
			return
		}
		current.APIKeyPrefix = normalized
	}
```

After the successful `s.settings.Set(...)` and before `writeJSON`, apply it live:

```go
	if s.identity != nil {
		s.identity.SetKeyPrefix(current.APIKeyPrefix)
	}
```

Add the exported helper at the end of the file:

```go
// LoadAPIKeyPrefix reads the persisted API key prefix from the settings store,
// normalized to its canonical "token_" form. It returns the default ("kr_") when
// unset, unreadable, or invalid. Exported so non-Server callers (app startup,
// CLI bootstrap) share the same persisted branding blob.
func LoadAPIKeyPrefix(ctx context.Context, settings *store.SettingsRepo) string {
	if settings == nil {
		return crypto.DefaultKeyPrefix
	}
	raw, err := settings.Get(ctx, brandingSettingsKey)
	if err != nil || raw == "" {
		return crypto.DefaultKeyPrefix
	}
	var bs BrandingSettings
	if err := json.Unmarshal([]byte(raw), &bs); err != nil {
		return crypto.DefaultKeyPrefix
	}
	if normalized, ok := identity.NormalizePrefix(bs.APIKeyPrefix); ok {
		return normalized
	}
	return crypto.DefaultKeyPrefix
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/gateway/ -run 'Branding|KeyPrefix' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/gateway/branding.go backend/internal/gateway/branding_test.go
git commit -m "feat(gateway): persist configurable API key prefix in branding settings"
```

---

### Task 4: Apply the persisted prefix at startup

**Files:**
- Modify: `backend/internal/app/app.go:121`
- Modify: `backend/internal/app/bootstrap.go:37`

**Interfaces:**
- Consumes: `gateway.LoadAPIKeyPrefix(ctx, *store.SettingsRepo)` from Task 3; `identity.Service.SetKeyPrefix` from Task 2.
- Produces: nothing new (wiring only).

- [ ] **Step 1: Wire the server build**

In `backend/internal/app/app.go`, replace line 121:

```go
	idSvc := identity.New(db.APIKeys())
```

with:

```go
	idSvc := identity.New(db.APIKeys())
	idSvc.SetKeyPrefix(gateway.LoadAPIKeyPrefix(ctx, db.Settings()))
```

(`gateway` is already imported at `app.go:28`.)

- [ ] **Step 2: Wire `-bootstrap`**

In `backend/internal/app/bootstrap.go`, replace line 37:

```go
	issued, err := identity.New(db.APIKeys()).Create(ctx, store.DefaultTenantID, "", name)
```

with:

```go
	idSvc := identity.New(db.APIKeys())
	idSvc.SetKeyPrefix(gateway.LoadAPIKeyPrefix(ctx, db.Settings()))
	issued, err := idSvc.Create(ctx, store.DefaultTenantID, "", name)
```

Add the import:

```go
	"github.com/mydisha/keirouter/backend/internal/gateway"
```

- [ ] **Step 3: Build and run the app package tests**

Run: `go build ./... && go test ./internal/app/`
Expected: build exits 0; `ok  github.com/mydisha/keirouter/backend/internal/app`.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/app/app.go backend/internal/app/bootstrap.go
git commit -m "feat(app): load API key prefix from settings at startup"
```

---

### Task 5: Branding tab input for the prefix

**Files:**
- Modify: `frontend/src/lib/api.ts` (BrandingSettings type)
- Modify: `frontend/src/pages/Settings.tsx` (BrandingTab)

**Interfaces:**
- Consumes: `POST /settings/branding` with `{ api_key_prefix }` from Task 3.
- Produces: no new exports; extends the existing `BrandingSettings` type.

- [ ] **Step 1: Extend the API type**

In `frontend/src/lib/api.ts`, in `interface BrandingSettings` (around line 103), add:

```ts
export interface BrandingSettings {
  name: string;
  logo_url: string;
  favicon_url: string;
  tagline: string;
  color_palette: string;
  api_key_prefix: string;
}
```

- [ ] **Step 2: Add the input to BrandingTab**

In `frontend/src/pages/Settings.tsx`, inside `BrandingTab`'s `<div className="divide-y ...">`, add a new block after the image-uploads block (before the Color palette block):

```tsx
        {/* API key prefix */}
        <div className="px-6 py-5">
          <Field label="API Key Prefix">
            <Input
              value={local.api_key_prefix}
              onChange={(e) => update({ api_key_prefix: e.target.value })}
              placeholder="kr"
            />
            <p className="mt-1 text-xs text-[var(--text-muted)]">
              Used for newly created keys. Enter <code>tkr</code> or <code>tkr_</code> → new keys
              look like <code>tkr_xxxx</code>. Existing keys are unaffected.
            </p>
          </Field>
        </div>
```

- [ ] **Step 3: Typecheck and build**

Run: `npm run typecheck && npm run build` (in `frontend/`)
Expected: both exit 0.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/lib/api.ts frontend/src/pages/Settings.tsx
git commit -m "feat(frontend): API key prefix field in Branding settings"
```

---

### Task 6: Full verification

**Files:** none (verification only).

- [ ] **Step 1: Backend build + focused tests**

Run: `go build ./... && go test ./internal/crypto/ ./internal/identity/ ./internal/gateway/ ./internal/app/`
Expected: build exits 0; all packages `ok`.

- [ ] **Step 2: Full backend test suite**

Run: `go test ./...`
Expected: all `ok` (or report any pre-existing failures explicitly; do not mask).

- [ ] **Step 3: Search for stale references**

Run: `grep -rn "crypto.KeyPrefix\|\bKeyPrefix\b" backend/internal --include=*.go`
Expected: only `DefaultKeyPrefix` and `SetKeyPrefix`/`keyPrefix` matches remain; no bare `KeyPrefix` constant references.

- [ ] **Step 4: Frontend typecheck + build**

Run: `npm run typecheck && npm run build` (in `frontend/`)
Expected: both exit 0.

- [ ] **Step 5: Manual acceptance (optional, if a local instance is running)**

1. Open the dashboard Branding tab, set **API Key Prefix** to `tkr`, save.
2. Create a new key on the Keys page; confirm its plaintext starts with `tkr_`.
3. Confirm an existing `kr_…` key still authenticates against `/v1/models`.

---

## Self-Review

**Spec coverage:**
- §6.1 crypto parameterization → Task 1.
- §6.2 identity prefix + `NormalizePrefix` → Task 2.
- §6.3 gateway persistence + live apply + `LoadAPIKeyPrefix` → Task 3.
- §6.4 startup wiring (app + bootstrap) → Task 4.
- §6.5 frontend type + input → Task 5.
- §7 tests → embedded per task; §9 acceptance criteria → Task 6.

**Placeholder scan:** none — every code step contains complete code.

**Type consistency:** `DefaultKeyPrefix`, `GenerateAPIKey(prefix)`, `maskKey(plaintext, prefix)`, `NormalizePrefix`, `SetKeyPrefix`, `keyPrefix`, `APIKeyPrefix`, `LoadAPIKeyPrefix` are used consistently across tasks.

**Out of scope honored:** no foreign-import changes, no re-keying, no new endpoint/tab/settings key.
