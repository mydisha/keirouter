# Portal Google SSO Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the paste-key/public-`?id=` usage portal with a per-user dashboard that authenticates users via Google SSO and binds exactly one API key per user.

**Architecture:** A new `internal/portalauth` package handles the Google OIDC authorization-code flow and id_token verification (official `google.golang.org/api/idtoken`). A new `portal_users` table maps `google_sub` → `key_id`. Portal sessions reuse the existing HMAC signing in `internal/auth` but with a distinct `sub` (`portal:<google_sub>`) and a separate cookie, so a portal cookie can never reach the admin API. Usage aggregation currently inline in `handlePortalKeyUsage` is factored into a shared helper reused by the new `/portal/usage` endpoint.

**Tech Stack:** Go 1.26, chi router, koanf config, `golang.org/x/oauth2`, `google.golang.org/api/idtoken`, React + Vite + TanStack Query frontend.

## Global Constraints

- Go version: `go 1.26.0` (from `backend/go.mod`).
- New dependencies allowed: `golang.org/x/oauth2` and `google.golang.org/api` only. No other new deps.
- All new SQL must be portable across SQLite and Postgres (TEXT ids/timestamps, INTEGER counters), and use `db.rebind(...)` in Go.
- Secrets must never be logged or committed. `google_client_secret` comes from env only.
- Fail closed: misconfigured SSO must never expose the portal.
- One key per user enforced by `UNIQUE(key_id)`.
- Claiming requires the full API key verified by `identity.Service.Authenticate`; a key ID alone is never sufficient.
- Do not rewrite git history. The leaked Postgres password is fixed by redaction + operator rotation only.
- Follow existing patterns: repos mirror `store.TenantRepo` (`store/repo_misc.go`), handlers use `writeJSON`/`writeError`, frontend uses `frontend/src/lib/api.ts` `request()` helper.

---

### Task 1: Secret hygiene — redact leaked DSN (no code change)

**Files:**
- Modify: `docs/superpowers/plans/2026-09-28-api-key-budget-topup.md:1337,1383`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing (documentation only).

- [ ] **Step 1: Confirm the exact lines**

Run: `grep -n "KEIROUTER_TEST_POSTGRES_DSN" docs/superpowers/plans/2026-09-28-api-key-budget-topup.md`
Expected: two lines (1337, 1383) each containing a `KEIROUTER_TEST_POSTGRES_DSN="postgres://keirouter:<REDACTED>@192.168.32.3:5432/keirouter?sslmode=disable"`.

- [ ] **Step 2: Replace the password with a placeholder**

Replace the plaintext password in both occurrences with `<REDACTED>`, leaving the rest of the DSN intact.

- [ ] **Step 3: Verify no residual occurrence**

Run: `grep -rn "<REDACTED>" docs/superpowers/plans/2026-09-28-api-key-budget-topup.md`
Expected: two matches; no plaintext credential remains in the plan file.

- [ ] **Step 4: Commit**

```bash
git add docs/superpowers/plans/2026-09-28-api-key-budget-topup.md
git commit -m "docs: redact leaked postgres password from plan"
```

- [ ] **Step 5: Note operator action (not automated)**

Report to the user: rotate `POSTGRES_PASSWORD` on the database server and in the local untracked `.env`. This is outside the repo and must be done by the operator.

---

### Task 2: Config — add `portal_sso` section and fail-closed validation

**Files:**
- Modify: `backend/internal/config/config.go`
- Modify: `config.example.yaml`
- Modify: `.env.example`
- Test: `backend/internal/config/config_test.go`

**Interfaces:**
- Consumes: existing `Config`, `Default()`, `Load()`, `cfg.validate()`.
- Produces: `config.PortalSSOConfig` with fields `Enabled bool`, `GoogleClientID string`, `GoogleClientSecret string`, `AllowedDomains []string`, `RedirectURL string`, `SessionTTL time.Duration`; `Config.PortalSSO PortalSSOConfig`.

- [ ] **Step 1: Write the failing test**

Add to `backend/internal/config/config_test.go`:

```go
func TestPortalSSOValidation(t *testing.T) {
	// Enabled without credentials must fail closed.
	cfg := Default()
	cfg.PortalSSO.Enabled = true
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error when portal_sso enabled without client id/secret")
	}

	cfg = Default()
	cfg.PortalSSO.Enabled = true
	cfg.PortalSSO.GoogleClientID = "id"
	cfg.PortalSSO.GoogleClientSecret = "secret"
	if err := cfg.validate(); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}

	// Disabled without credentials is fine.
	cfg = Default()
	if err := cfg.validate(); err != nil {
		t.Fatalf("disabled portal_sso must validate, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/config/ -run TestPortalSSOValidation -v`
Expected: FAIL (compile error: `cfg.PortalSSO` undefined).

- [ ] **Step 3: Add the config struct and field**

In `backend/internal/config/config.go`, add to the `Config` struct (after `Guardrails`):

```go
	PortalSSO      PortalSSOConfig      `koanf:"portal_sso"`
```

Add the struct (near `SecurityConfig`):

```go
// PortalSSOConfig configures Google sign-in for the public usage portal.
// The client secret is intended to be supplied via environment variable
// (KEIROUTER_PORTAL_SSO__GOOGLE_CLIENT_SECRET), never committed to YAML.
type PortalSSOConfig struct {
	Enabled            bool          `koanf:"enabled"`
	GoogleClientID     string        `koanf:"google_client_id"`
	GoogleClientSecret string        `koanf:"google_client_secret"`
	AllowedDomains     []string      `koanf:"allowed_domains"`
	RedirectURL        string        `koanf:"redirect_url"`
	SessionTTL         time.Duration `koanf:"session_ttl"`
}
```

In `Default()`, add:

```go
		PortalSSO: PortalSSOConfig{
			Enabled:    false,
			SessionTTL: 24 * time.Hour,
		},
```

- [ ] **Step 4: Add fail-closed validation**

In `cfg.validate()` add:

```go
	if c.PortalSSO.Enabled {
		if c.PortalSSO.GoogleClientID == "" || c.PortalSSO.GoogleClientSecret == "" {
			return errors.New("portal_sso.enabled requires google_client_id and google_client_secret")
		}
		if c.PortalSSO.SessionTTL <= 0 {
			c.PortalSSO.SessionTTL = 24 * time.Hour
		}
	}
```

Ensure `errors` is imported (check the existing import block; add if absent).

- [ ] **Step 5: Document in config.example.yaml**

Append:

```yaml
portal_sso:
  # Google sign-in for the public usage portal at /portal.
  enabled: false
  # OAuth client id from Google Cloud Console. Not a secret; may live here.
  google_client_id: ""
  # OAuth client secret. Leave empty here and provide via environment:
  #   KEIROUTER_PORTAL_SSO__GOOGLE_CLIENT_SECRET=...
  google_client_secret: ""
  # Optional: restrict sign-in to these email domains (empty = any Google account).
  allowed_domains: []
  # Optional: defaults to <public-base-url>/portal/auth/google/callback.
  redirect_url: ""
  session_ttl: 24h
```

- [ ] **Step 6: Document in .env.example**

Append:

```bash
# Portal Google SSO. Client secret belongs in the environment, not config.yaml.
KEIROUTER_PORTAL_SSO__ENABLED=false
KEIROUTER_PORTAL_SSO__GOOGLE_CLIENT_ID=
KEIROUTER_PORTAL_SSO__GOOGLE_CLIENT_SECRET=
```

- [ ] **Step 7: Run tests and build**

Run: `cd backend && go test ./internal/config/ -run TestPortalSSOValidation -v && go build ./...`
Expected: PASS, build succeeds.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/config/config.go backend/internal/config/config_test.go config.example.yaml .env.example
git commit -m "feat(config): add portal_sso section with fail-closed validation"
```

---

### Task 3: Migration + `PortalUserRepo`

**Files:**
- Create: `backend/internal/store/migrations/0035_portal_users.sql`
- Create: `backend/internal/store/repo_portal_users.go`
- Test: `backend/internal/store/repo_portal_users_test.go`

**Interfaces:**
- Consumes: `DB.rebind`, `formatTime`, `parseTime`, `ErrNotFound`, `ErrAlreadyExists`.
- Produces:
  - `store.PortalUser{ GoogleSub, Email, KeyID string; CreatedAt, UpdatedAt time.Time }`
  - `(*DB).PortalUsers() *PortalUserRepo`
  - `(*PortalUserRepo) Upsert(ctx, PortalUser) error`
  - `(*PortalUserRepo) GetBySub(ctx, sub string) (PortalUser, error)`
  - `(*PortalUserRepo) GetByKey(ctx, keyID string) (PortalUser, error)`
  - `(*PortalUserRepo) Delete(ctx, sub string) error`

- [ ] **Step 1: Write the migration**

Create `backend/internal/store/migrations/0035_portal_users.sql`:

```sql
-- Portal users: a Google identity bound to exactly one API key. The Google
-- subject (never the email) is the stable identity. key_id is UNIQUE so a key
-- can be claimed by at most one user; deleting the key removes the binding.
CREATE TABLE IF NOT EXISTS portal_users (
    google_sub TEXT PRIMARY KEY,
    email      TEXT NOT NULL,
    key_id     TEXT NOT NULL UNIQUE REFERENCES api_keys(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_portal_users_email ON portal_users(email);
```

- [ ] **Step 2: Write the failing test**

Create `backend/internal/store/repo_portal_users_test.go`. Follow the existing store test setup helper (find it with `grep -rn "func newTestDB\|func testDB" backend/internal/store/*_test.go` and reuse it). If tests use an in-memory DB helper named e.g. `newTestDB(t)`, use that name; adjust to the real helper found.

```go
func TestPortalUserRepo_BindAndGet(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Seed a tenant + key so the FK is satisfiable.
	tenant := Tenant{ID: DefaultTenantID, Name: "Default", CreatedAt: time.Now()}
	if err := db.Tenants().Upsert(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	key := APIKey{
		ID: "key-1", TenantID: DefaultTenantID, Name: "k",
		KeyHash: "h", LookupHash: "l", Display: "d", CreatedAt: time.Now(),
	}
	if err := db.APIKeys().Create(ctx, key); err != nil {
		t.Fatal(err)
	}

	pu := PortalUser{GoogleSub: "sub-1", Email: "a@example.com", KeyID: "key-1",
		CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.PortalUsers().Upsert(ctx, pu); err != nil {
		t.Fatal(err)
	}

	got, err := db.PortalUsers().GetBySub(ctx, "sub-1")
	if err != nil || got.KeyID != "key-1" || got.Email != "a@example.com" {
		t.Fatalf("GetBySub = %+v, %v", got, err)
	}

	byKey, err := db.PortalUsers().GetByKey(ctx, "key-1")
	if err != nil || byKey.GoogleSub != "sub-1" {
		t.Fatalf("GetByKey = %+v, %v", byKey, err)
	}

	// A second sub cannot claim the same key.
	dup := PortalUser{GoogleSub: "sub-2", Email: "b@example.com", KeyID: "key-1",
		CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.PortalUsers().Upsert(ctx, dup); err == nil {
		t.Fatal("expected conflict binding an already-claimed key")
	}

	// Delete revokes the binding.
	if err := db.PortalUsers().Delete(ctx, "sub-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PortalUsers().GetBySub(ctx, "sub-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd backend && go test ./internal/store/ -run TestPortalUserRepo_BindAndGet -v`
Expected: FAIL (compile error: `PortalUser` undefined).

- [ ] **Step 4: Implement the model**

Add to `backend/internal/store/models.go`:

```go
// PortalUser binds one Google identity to exactly one API key for the public
// usage portal. GoogleSub is Google's stable subject id.
type PortalUser struct {
	GoogleSub string
	Email     string
	KeyID     string
	CreatedAt time.Time
	UpdatedAt time.Time
}
```

- [ ] **Step 5: Implement the repo**

Create `backend/internal/store/repo_portal_users.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PortalUserRepo persists portal_users bindings.
type PortalUserRepo struct{ db *DB }

// PortalUsers returns the portal user repository.
func (db *DB) PortalUsers() *PortalUserRepo { return &PortalUserRepo{db: db} }

// Upsert inserts or updates a portal user binding. It returns ErrAlreadyExists
// when the target key is already bound to a different Google subject.
func (r *PortalUserRepo) Upsert(ctx context.Context, u PortalUser) error {
	if existing, err := r.GetByKey(ctx, u.KeyID); err == nil && existing.GoogleSub != u.GoogleSub {
		return ErrAlreadyExists
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	now := time.Now()
	q := r.db.rebind(`INSERT INTO portal_users (google_sub, email, key_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (google_sub) DO UPDATE SET email = excluded.email, key_id = excluded.key_id, updated_at = excluded.updated_at`)
	_, err := r.db.sql.ExecContext(ctx, q, u.GoogleSub, u.Email, u.KeyID, formatTime(now), formatTime(now))
	if err != nil {
		return fmt.Errorf("store: upsert portal user: %w", err)
	}
	return nil
}

// GetBySub returns the binding for a Google subject.
func (r *PortalUserRepo) GetBySub(ctx context.Context, sub string) (PortalUser, error) {
	return r.scanOne(r.db.rebind(`SELECT google_sub, email, key_id, created_at, updated_at FROM portal_users WHERE google_sub = ?`), sub)
}

// GetByKey returns the binding for an API key id.
func (r *PortalUserRepo) GetByKey(ctx context.Context, keyID string) (PortalUser, error) {
	return r.scanOne(r.db.rebind(`SELECT google_sub, email, key_id, created_at, updated_at FROM portal_users WHERE key_id = ?`), keyID)
}

func (r *PortalUserRepo) scanOne(q string, arg string) (PortalUser, error) {
	// Callers pass ctx-bound queries; ctx is captured via the DB wrapper pattern
	// used by other repos, so this helper is intentionally avoided below.
	return PortalUser{}, errors.New("unused")
}

// Delete removes a binding (revocation).
func (r *PortalUserRepo) Delete(ctx context.Context, sub string) error {
	q := r.db.rebind(`DELETE FROM portal_users WHERE google_sub = ?`)
	if _, err := r.db.sql.ExecContext(ctx, q, sub); err != nil {
		return fmt.Errorf("store: delete portal user: %w", err)
	}
	return nil
}
```

**Correction — do not use the `scanOne` sketch above.** Repos in this codebase take `ctx` explicitly. Implement `GetBySub` and `GetByKey` directly, each performing `QueryRowContext` + `Scan`, returning `ErrNotFound` on `sql.ErrNoRows`, exactly like `TenantRepo.Get` in `store/repo_misc.go`. Delete the `scanOne` helper. The final file must be:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PortalUserRepo persists portal_users bindings.
type PortalUserRepo struct{ db *DB }

// PortalUsers returns the portal user repository.
func (db *DB) PortalUsers() *PortalUserRepo { return &PortalUserRepo{db: db} }

// Upsert inserts or updates a portal user binding. It returns ErrAlreadyExists
// when the target key is already bound to a different Google subject.
func (r *PortalUserRepo) Upsert(ctx context.Context, u PortalUser) error {
	if existing, err := r.GetByKey(ctx, u.KeyID); err == nil && existing.GoogleSub != u.GoogleSub {
		return ErrAlreadyExists
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	now := time.Now()
	q := r.db.rebind(`INSERT INTO portal_users (google_sub, email, key_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (google_sub) DO UPDATE SET email = excluded.email, key_id = excluded.key_id, updated_at = excluded.updated_at`)
	_, err := r.db.sql.ExecContext(ctx, q, u.GoogleSub, u.Email, u.KeyID, formatTime(now), formatTime(now))
	if err != nil {
		return fmt.Errorf("store: upsert portal user: %w", err)
	}
	return nil
}

// GetBySub returns the binding for a Google subject.
func (r *PortalUserRepo) GetBySub(ctx context.Context, sub string) (PortalUser, error) {
	q := r.db.rebind(`SELECT google_sub, email, key_id, created_at, updated_at FROM portal_users WHERE google_sub = ?`)
	return r.scanRow(ctx, q, sub)
}

// GetByKey returns the binding for an API key id.
func (r *PortalUserRepo) GetByKey(ctx context.Context, keyID string) (PortalUser, error) {
	q := r.db.rebind(`SELECT google_sub, email, key_id, created_at, updated_at FROM portal_users WHERE key_id = ?`)
	return r.scanRow(ctx, q, keyID)
}

func (r *PortalUserRepo) scanRow(ctx context.Context, q, arg string) (PortalUser, error) {
	var (
		u                 PortalUser
		created, updated  string
	)
	err := r.db.sql.QueryRowContext(ctx, q, arg).Scan(&u.GoogleSub, &u.Email, &u.KeyID, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return PortalUser{}, ErrNotFound
	}
	if err != nil {
		return PortalUser{}, fmt.Errorf("store: get portal user: %w", err)
	}
	u.CreatedAt, u.UpdatedAt = parseTime(created), parseTime(updated)
	return u, nil
}

// Delete removes a binding (revocation).
func (r *PortalUserRepo) Delete(ctx context.Context, sub string) error {
	q := r.db.rebind(`DELETE FROM portal_users WHERE google_sub = ?`)
	if _, err := r.db.sql.ExecContext(ctx, q, sub); err != nil {
		return fmt.Errorf("store: delete portal user: %w", err)
	}
	return nil
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `cd backend && go test ./internal/store/ -run TestPortalUserRepo_BindAndGet -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/store/migrations/0035_portal_users.sql backend/internal/store/repo_portal_users.go backend/internal/store/repo_portal_users_test.go backend/internal/store/models.go
git commit -m "feat(store): add portal_users binding table and repo"
```

---

### Task 4: Auth — audience-scoped sessions

**Files:**
- Modify: `backend/internal/auth/auth.go`
- Test: `backend/internal/auth/auth_test.go`

**Interfaces:**
- Consumes: existing `Service.sign`, `Service.ttl`, session payload.
- Produces:
  - `(*Service) IssueSessionFor(sub string) (string, error)`
  - `(*Service) VerifySessionSub(token, sub string) bool`
  - `(*Service) SessionSubject(token string) (string, bool)` — returns the signed `sub` without requiring a specific audience (used by the portal middleware to read the subject, then re-verify with `VerifySessionSub`).
  - Existing `IssueSession()` delegates to `IssueSessionFor("dashboard")`.
  - Existing `VerifySession(token)` delegates to `VerifySessionSub(token, "dashboard")`.

- [ ] **Step 1: Write the failing test**

Add to `backend/internal/auth/auth_test.go` (reuse the existing test harness that builds a `Service` with a settings repo; if none exists, the `New(...)`/`EnsureDefaults` pattern with an in-memory store is used by the package tests — mirror it):

```go
func TestSessionAudienceIsolation(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if _, err := s.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}

	admin, err := s.IssueSession()
	if err != nil {
		t.Fatal(err)
	}
	portal, err := s.IssueSessionFor("portal:sub-1")
	if err != nil {
		t.Fatal(err)
	}

	if !s.VerifySession(admin) {
		t.Fatal("admin session must verify as dashboard")
	}
	if s.VerifySession(portal) {
		t.Fatal("portal session must NOT verify as dashboard")
	}
	if !s.VerifySessionSub(portal, "portal:sub-1") {
		t.Fatal("portal session must verify for its own sub")
	}
	if s.VerifySessionSub(portal, "portal:other") {
		t.Fatal("portal session must not verify for a different sub")
	}
}
```

Note: if `newTestService` does not exist, add a local helper in the test that creates an in-memory store DB, a `SettingsRepo`, and calls `auth.New(...)`. Check how existing auth tests construct a `Service` and reuse that exact approach.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/auth/ -run TestSessionAudienceIsolation -v`
Expected: FAIL (compile error: `IssueSessionFor` / `VerifySessionSub` undefined).

- [ ] **Step 3: Implement audience-scoped sessions**

In `backend/internal/auth/auth.go`, replace `IssueSession` and `VerifySession`:

```go
// IssueSession mints a dashboard session token.
func (s *Service) IssueSession() (string, error) {
	return s.IssueSessionFor("dashboard")
}

// IssueSessionFor mints a signed session token for the given subject audience.
// The portal uses a distinct subject (e.g. "portal:<google_sub>") so a portal
// token can never be accepted by the dashboard middleware.
func (s *Service) IssueSessionFor(sub string) (string, error) {
	payload := session{Sub: sub, Exp: time.Now().Add(s.ttl).Unix()}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	return body + "." + s.sign(body), nil
}

// VerifySession reports whether a dashboard session token is valid.
func (s *Service) VerifySession(token string) bool {
	return s.VerifySessionSub(token, "dashboard")
}

// VerifySessionSub reports whether a token is valid and carries the given sub.
func (s *Service) VerifySessionSub(token, sub string) bool {
	got, ok := s.SessionSubject(token)
	return ok && got == sub
}

// SessionSubject returns the signed subject of a valid, unexpired token.
func (s *Service) SessionSubject(token string) (string, bool) {
	body, sig, ok := strings.Cut(token, ".")
	if !ok {
		return "", false
	}
	if !hmac.Equal([]byte(sig), []byte(s.sign(body))) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", false
	}
	var p session
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", false
	}
	if time.Now().Unix() >= p.Exp {
		return "", false
	}
	return p.Sub, true
}
```

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/auth/ ./internal/gateway/ -run 'Session|Auth' -v`
Expected: PASS (existing session tests still pass because `IssueSession`/`VerifySession` preserve prior behavior).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/auth.go backend/internal/auth/auth_test.go
git commit -m "feat(auth): audience-scoped session tokens"
```

---

### Task 5: `internal/portalauth` — Google OIDC service

**Files:**
- Create: `backend/internal/portalauth/portalauth.go`
- Create: `backend/internal/portalauth/google.go`
- Test: `backend/internal/portalauth/google_test.go`
- Modify: `backend/go.mod`, `backend/go.sum` (via `go get`)

**Interfaces:**
- Consumes: `oauth2` package; `google.golang.org/api/idtoken`.
- Produces:
  - `portalauth.Identity{ Sub, Email string; EmailVerified bool }`
  - `portalauth.Config{ ClientID, ClientSecret, RedirectURL string; AllowedDomains []string }`
  - `portalauth.Service`; `New(Config) *Service`
  - `(*Service) AuthURL(state string) string`
  - `(*Service) Exchange(ctx, code string) (Identity, error)`
  - `(*Service) Verify(ctx, rawIDToken string) (Identity, error)`

- [ ] **Step 1: Add dependencies**

Run: `cd backend && go get golang.org/x/oauth2@latest google.golang.org/api/idtoken@latest`
Expected: `go.mod`/`go.sum` updated.

Note: If the module proxy is unavailable in the environment, record the failure and stop — do not vendor or fake the dependency.

- [ ] **Step 2: Write the failing test**

Create `backend/internal/portalauth/google_test.go`:

```go
package portalauth

import (
	"strings"
	"testing"
)

func TestAuthURLContainsGoogleAuthorizeAndState(t *testing.T) {
	s := New(Config{
		ClientID:     "client-abc",
		ClientSecret: "secret",
		RedirectURL:  "http://localhost:20180/portal/auth/google/callback",
	})
	u := s.AuthURL("state-123")
	if !strings.HasPrefix(u, "https://accounts.google.com/o/oauth2/v2/auth?") {
		t.Fatalf("unexpected authorize URL: %s", u)
	}
	for _, want := range []string{"state=state-123", "client_id=client-abc", "scope=openid", "redirect_uri="} {
		if !strings.Contains(u, want) {
			t.Fatalf("authorize URL missing %q: %s", want, u)
		}
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd backend && go test ./internal/portalauth/ -run TestAuthURLContainsGoogleAuthorizeAndState -v`
Expected: FAIL (compile error: package undefined).

- [ ] **Step 4: Implement `portalauth.go` (types + authorize URL)**

Create `backend/internal/portalauth/portalauth.go`:

```go
// Package portalauth implements Google sign-in for the public usage portal.
// It performs the OIDC authorization-code flow server-side and verifies the
// returned id_token with Google's published keys.
package portalauth

import (
	"context"
	"errors"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// ErrEmailNotVerified is returned when Google reports an unverified email.
var ErrEmailNotVerified = errors.New("portalauth: email not verified")

// ErrDomainNotAllowed is returned when the email domain is not allow-listed.
var ErrDomainNotAllowed = errors.New("portalauth: email domain not allowed")

// Identity is the verified Google identity.
type Identity struct {
	Sub           string
	Email         string
	EmailVerified bool
}

// Config holds the OAuth client settings and access policy.
type Config struct {
	ClientID       string
	ClientSecret   string
	RedirectURL    string
	AllowedDomains []string
}

// Service drives the Google OIDC flow.
type Service struct {
	cfg   Config
	oauth *oauth2.Config
}

// New builds a Service.
func New(cfg Config) *Service {
	return &Service{
		cfg: cfg,
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		},
	}
}

// AuthURL builds the Google authorize URL for the given CSRF state.
func (s *Service) AuthURL(state string) string {
	return s.oauth.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

// domainAllowed reports whether the email passes the allow-list.
func (s *Service) domainAllowed(email string) bool {
	if len(s.cfg.AllowedDomains) == 0 {
		return true
	}
	_, domain, ok := strings.Cut(email, "@")
	if !ok {
		return false
	}
	for _, d := range s.cfg.AllowedDomains {
		if strings.EqualFold(strings.TrimSpace(d), domain) {
			return true
		}
	}
	return false
}

// accept validates policy for a verified identity.
func (s *Service) accept(id Identity) (Identity, error) {
	if !id.EmailVerified {
		return Identity{}, ErrEmailNotVerified
	}
	if !s.domainAllowed(id.Email) {
		return Identity{}, ErrDomainNotAllowed
	}
	return id, nil
}

var _ = context.Background
```

Note: remove the trailing `var _ = context.Background` if `context` is used elsewhere in the file; keep imports gofmt-clean. `Exchange` and `Verify` live in `google.go`.

- [ ] **Step 5: Implement `google.go` (exchange + verify)**

Create `backend/internal/portalauth/google.go`:

```go
package portalauth

import (
	"context"
	"fmt"

	"google.golang.org/api/idtoken"
)

// Exchange swaps an authorization code for a verified Identity.
func (s *Service) Exchange(ctx context.Context, code string) (Identity, error) {
	tok, err := s.oauth.Exchange(ctx, code)
	if err != nil {
		return Identity{}, fmt.Errorf("portalauth: exchange code: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return Identity{}, fmt.Errorf("portalauth: token response missing id_token")
	}
	return s.Verify(ctx, raw)
}

// Verify validates a raw id_token against Google's keys and the configured
// audience, then applies email policy.
func (s *Service) Verify(ctx context.Context, rawIDToken string) (Identity, error) {
	payload, err := idtoken.Validate(ctx, rawIDToken, s.cfg.ClientID)
	if err != nil {
		return Identity{}, fmt.Errorf("portalauth: verify id_token: %w", err)
	}
	sub, _ := payload.Claims["sub"].(string)
	email, _ := payload.Claims["email"].(string)
	verified, _ := payload.Claims["email_verified"].(bool)
	if sub == "" || email == "" {
		return Identity{}, fmt.Errorf("portalauth: id_token missing sub/email")
	}
	return s.accept(Identity{Sub: sub, Email: email, EmailVerified: verified})
}
```

- [ ] **Step 6: Add policy tests**

Add to `google_test.go`:

```go
func TestAcceptPolicy(t *testing.T) {
	s := New(Config{AllowedDomains: []string{"example.com"}})
	if _, err := s.accept(Identity{Email: "a@example.com", EmailVerified: false}); err != ErrEmailNotVerified {
		t.Fatalf("want ErrEmailNotVerified, got %v", err)
	}
	if _, err := s.accept(Identity{Email: "a@other.com", EmailVerified: true}); err != ErrDomainNotAllowed {
		t.Fatalf("want ErrDomainNotAllowed, got %v", err)
	}
	if _, err := s.accept(Identity{Email: "a@example.com", EmailVerified: true}); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	open := New(Config{})
	if _, err := open.accept(Identity{Email: "x@any.com", EmailVerified: true}); err != nil {
		t.Fatalf("empty allow-list must accept any verified email, got %v", err)
	}
}
```

- [ ] **Step 7: Run tests and build**

Run: `cd backend && go test ./internal/portalauth/ -v && go build ./...`
Expected: PASS, build succeeds.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/portalauth/ backend/go.mod backend/go.sum
git commit -m "feat(portalauth): Google OIDC flow with id_token verification"
```

---

### Task 6: Gateway — portal session middleware, handlers, wiring

**Files:**
- Modify: `backend/internal/gateway/server.go`
- Modify: `backend/internal/gateway/handlers.go` (factor usage helper)
- Modify: `backend/internal/gateway/auth_handlers.go` (admin middleware sub check)
- Create: `backend/internal/gateway/portal_sso.go`
- Test: `backend/internal/gateway/portal_sso_test.go`

**Interfaces:**
- Consumes: `portalauth.Service`, `store.PortalUserRepo`, `identity.Service.Authenticate`, `auth.Service.IssueSessionFor/VerifySessionSub`.
- Produces:
  - Server field `portalSSO *portalauth.Service` (nil when disabled).
  - `(*Server) buildKeyUsageMap(ctx, key store.APIKey) (map[string]any, error)` shared helper.
  - Handlers: `handlePortalLoginStart`, `handlePortalLoginCallback`, `handlePortalStatus`, `handlePortalClaim`, `handlePortalUsage`, `handlePortalLogout`, `portalSessionMiddleware`.

- [ ] **Step 1: Factor the shared usage helper**

In `backend/internal/gateway/handlers.go`, extract the body of `handlePortalKeyUsage` (everything after the key lookup, from the budgets loop through the final `writeJSON` payload) into a new method:

```go
// buildKeyUsageMap assembles the portal usage payload for one key.
func (s *Server) buildKeyUsageMap(ctx context.Context, key store.APIKey, days int) (map[string]any, error) {
	// ... existing budgets/summary/daily/models/recent logic ...
}
```

Then rewrite `handlePortalKeyUsage` to look up the key, parse `days`, call `buildKeyUsageMap`, and `writeJSON`. Keep behavior identical so existing tests (if any) still pass. If `handlePortalKeyUsage` is removed entirely per Task 6 Step 4, delete it and keep only `buildKeyUsageMap` plus the new `/portal/usage` handler.

- [ ] **Step 2: Write the failing test**

Create `backend/internal/gateway/portal_sso_test.go`. Use the existing gateway test harness (find how other handler tests build a `Server`; grep `func newTestServer` in the package).

```go
func TestPortalClaimRejectsBadKey(t *testing.T) {
	srv := newTestServer(t) // existing helper; adjust name to match

	// A portal session cookie for sub-1.
	tok, err := srv.auth.IssueSessionFor("portal:sub-1")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.NewReader(`{"api_key":"not-a-real-key"}`)
	req := httptest.NewRequest(http.MethodPost, "/portal/auth/claim", body)
	req.AddCookie(&http.Cookie{Name: "kr_portal_session", Value: tok})
	rec := httptest.NewRecorder()
	srv.handlePortalClaim(rec, req)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusBadRequest {
		t.Fatalf("want 401/400 for bad key, got %d", rec.Code)
	}
}

func TestPortalSessionRejectedByAdminMiddleware(t *testing.T) {
	srv := newTestServer(t)
	tok, _ := srv.auth.IssueSessionFor("portal:sub-1")
	req := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
	req.AddCookie(&http.Cookie{Name: "kr_session", Value: tok})
	rec := httptest.NewRecorder()
	srv.sessionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("portal cookie must not authorize admin API, got %d", rec.Code)
	}
}
```

If `newTestServer` does not exist, create a minimal helper in the test file that constructs a `Server` with an in-memory store and the required services, mirroring the package's existing test setup. Do not invent a `Server` constructor; use `NewServer`/the struct literal the package already uses.

- [ ] **Step 3: Run test to verify it fails**

Run: `cd backend && go test ./internal/gateway/ -run 'Portal' -v`
Expected: FAIL (undefined handlers / field).

- [ ] **Step 4: Harden the admin session middleware**

In `auth_handlers.go`, change `sessionMiddleware` to require the dashboard subject:

```go
func (s *Server) sessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || !s.auth.VerifySession(c.Value) {
			writeError(w, http.StatusUnauthorized, "dashboard session required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

(`VerifySession` now already enforces `sub=="dashboard"` per Task 4 — no further change needed. Keep the comment noting the audience check.)

- [ ] **Step 5: Implement `portal_sso.go`**

Create `backend/internal/gateway/portal_sso.go` with:
- `const portalSessionCookie = "kr_portal_session"`
- `const portalStateCookie = "kr_portal_state"`
- `portalSessionMiddleware` — reads `kr_portal_session`, `VerifySessionSub(token, sub)` where sub is parsed from token; to avoid a second decode, add `auth.Service.SessionSubject(token) (string, bool)`, then verify. Implement `SessionSubject` in Task 4 as an addition: decode without sub check and return `p.Sub`. (Add it in this task if Task 4 is already committed; keep the same file.)
- `handlePortalLoginStart` — generate random state, set `HttpOnly` state cookie (5 min), redirect to `s.portalSSO.AuthURL(state)`.
- `handlePortalLoginCallback` — compare state cookie, `Exchange`, `IssueSessionFor("portal:"+id.Sub)`, set portal cookie, upsert nothing yet (claim is separate), redirect `/portal`.
- `handlePortalStatus` — read portal session subject; if none → `{authenticated:false}`; else look up binding; return `{authenticated:true,email,claimed,key_id}`.
- `handlePortalClaim` — require portal session; decode `{api_key}`; `s.identity.Authenticate(ctx, key)`; on success call `s.store.PortalUsers().Upsert(...)`; map `store.ErrAlreadyExists` → 409 with a clear message.
- `handlePortalUsage` — require portal session; look up binding; if none → 409; else load the key and call `buildKeyUsageMap(ctx, key, days)`; `writeJSON`.
- `handlePortalLogout` — clear `kr_portal_session`.

Use `crypto/rand` + `base64.RawURLEncoding` for state, and reuse `s.sessionCookieSecure(r)` for the cookie `Secure` flag.

- [ ] **Step 6: Wire routes and service construction**

In `server.go`:
- Add fields to the `Server` struct: `portalSSO *portalauth.Service`, `store *store.DB` (if not already present).
- In the constructor (`NewServer` / deps struct), construct `portalSSO` only when `cfg.PortalSSO.Enabled`; otherwise leave nil.
- Register routes (near the existing portal block):

```go
	// Public portal SSO + portal-scoped API.
	r.Get("/portal/auth/google/start", s.handlePortalLoginStart)
	r.Get("/portal/auth/google/callback", s.handlePortalLoginCallback)
	r.Get("/portal/auth/status", s.handlePortalStatus) // returns authenticated:false when no session
	r.Get("/v1/portal/branding", s.portalBranding)     // unchanged
	r.Group(func(r chi.Router) {
		r.Use(s.portalSessionMiddleware)
		r.Post("/portal/auth/claim", s.handlePortalClaim)
		r.Get("/portal/usage", s.handlePortalUsage)
		r.Post("/portal/auth/logout", s.handlePortalLogout)
	})
```

- Delete `r.Get("/v1/portal/keys/{id}/usage", s.handlePortalKeyUsage)`.
- Guard start/callback: if `s.portalSSO == nil`, return 503 `{"error":"portal sso not configured"}`.

Verify the existing public-base-URL setting (used by bansos, per recent commits) and use it to default `RedirectURL` when empty: `redirect_url = <public_base_url> + "/portal/auth/google/callback"`.

- [ ] **Step 7: Generate a random state helper test (optional but cheap)**

Include in `portal_sso_test.go` a small test that two consecutive `handlePortalLoginStart` calls produce different `state` cookies. Skip if the handler requires a full Server fixture that is costly.

- [ ] **Step 8: Run tests and build**

Run: `cd backend && go test ./internal/gateway/ -run 'Portal' -v && go build ./...`
Expected: PASS, build succeeds.

- [ ] **Step 9: Commit**

```bash
git add backend/internal/gateway/portal_sso.go backend/internal/gateway/portal_sso_test.go backend/internal/gateway/server.go backend/internal/gateway/handlers.go backend/internal/gateway/auth_handlers.go backend/internal/auth/auth.go
git commit -m "feat(gateway): portal SSO endpoints and portal session middleware"
```

---

### Task 7: Frontend — Google sign-in, claim, usage dashboard

**Files:**
- Modify: `frontend/src/pages/KeyPortal.tsx`
- Modify: `frontend/src/lib/api.ts`
- Modify: `frontend/src/App.tsx` (only if a route guard is needed)

**Interfaces:**
- Consumes: `/portal/auth/status`, `/portal/auth/google/start`, `/portal/auth/claim`, `/portal/usage`, `/portal/auth/logout`.
- Produces: portal API functions in `api.ts`:
  - `fetchPortalStatus()`
  - `claimPortalKey(apiKey: string)`
  - `fetchPortalUsage(days?: number)`
  - `portalLogout()`

- [ ] **Step 1: Add portal API functions**

In `frontend/src/lib/api.ts`, add:

```ts
export interface PortalStatus { authenticated: boolean; email?: string; claimed?: boolean; key_id?: string }

export async function fetchPortalStatus(): Promise<PortalStatus> {
  const resp = await fetch("/portal/auth/status");
  if (!resp.ok) return { authenticated: false };
  return resp.json();
}

export async function claimPortalKey(apiKey: string): Promise<{ ok: boolean; key_id: string }> {
  const resp = await fetch("/portal/auth/claim", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ api_key: apiKey }),
  });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error || "Failed to claim key");
  return data;
}

export async function fetchPortalUsage(days?: number): Promise<KeyUsageData> {
  const qs = days ? `?days=${days}` : "";
  const resp = await fetch(`/portal/usage${qs}`);
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error || "Failed to load usage");
  return data;
}

export async function portalLogout(): Promise<void> {
  await fetch("/portal/auth/logout", { method: "POST" });
}
```

Remove `fetchKeyUsageById` (no longer used) and the `?key=` path in `fetchKeyUsage` if it becomes unused; keep `fetchKeyUsage` only if another caller exists (grep first: `grep -rn "fetchKeyUsage" frontend/src`).

- [ ] **Step 2: Rewrite the KeyPortalPage auth flow**

In `frontend/src/pages/KeyPortal.tsx`:
- Remove `useSearchParams`, `?key=`/`?id=` logic, the API-key `Input` login form, and the `handleLogin` that set query params.
- Add a `useQuery` for `fetchPortalStatus()`.
- Render:
  1. not authenticated → card with `<a href="/portal/auth/google/start">Sign in with Google</a>`.
  2. authenticated && !claimed → a claim form (single password-type input for the full API key) calling `claimPortalKey`, then invalidate status.
  3. authenticated && claimed → the existing dashboard, with the usage `useQuery` switched to `fetchPortalUsage(days)` (no Bearer header).
- Logout calls `portalLogout()` then reloads/redirects.

Keep the existing presentational components (`OverviewSection`, `TrendSection`, etc.) unchanged; only the data source and the auth gate change.

- [ ] **Step 3: Typecheck and build**

Run: `cd frontend && npm run build`
Expected: build succeeds, no type errors.

- [ ] **Step 4: Lint**

Run: `cd frontend && npm run lint` (if a lint script exists; check `frontend/package.json` scripts first).
Expected: passes.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/KeyPortal.tsx frontend/src/lib/api.ts frontend/src/App.tsx
git commit -m "feat(portal): Google sign-in, key claim, and per-user usage dashboard"
```

---

### Task 8: Documentation

**Files:**
- Modify: `README.md` (Usage Portal section)
- Modify: `config.example.yaml` (already touched in Task 2; ensure wording is final)

**Interfaces:**
- Consumes: nothing.
- Produces: updated docs.

- [ ] **Step 1: Update the README Usage Portal section**

Replace the paragraph that says the portal "All it asks for is the API key" with the new flow: sign in with Google, claim one API key, view usage. Document the required Google Cloud OAuth client, the redirect URI, and that the client secret comes from env.

- [ ] **Step 2: Verify no stale `?id=` references**

Run: `grep -rn "portal.*id=\|/v1/portal/keys" README.md frontend/src backend/internal --exclude-dir=node_modules`
Expected: no references to the removed public endpoint.

- [ ] **Step 3: Commit**

```bash
git add README.md config.example.yaml
git commit -m "docs: document portal Google SSO"
```

---

### Task 9: End-to-end verification

**Files:**
- None (verification only).

**Interfaces:**
- Consumes: all prior tasks.
- Produces: verified evidence.

- [ ] **Step 1: Full backend test + build**

Run: `cd backend && go build ./... && go test ./...`
Expected: build succeeds; tests pass (report any pre-existing failures separately).

- [ ] **Step 2: Frontend build + lint**

Run: `cd frontend && npm run build && npm run lint`
Expected: pass.

- [ ] **Step 3: Manual Google flow**

With `portal_sso.enabled=true` and a real Google OAuth client whose redirect URI is `http://localhost:20180/portal/auth/google/callback`:
1. Open `http://localhost:20180/portal` → "Sign in with Google".
2. Complete Google sign-in → redirected back, prompted to claim a key.
3. Paste a valid `kr_...` key → dashboard loads usage.
4. Attempt to claim the same key with a second Google account → 409.
5. Confirm `GET /v1/portal/keys/{some-id}/usage` now returns 404.
6. Confirm the portal cookie does not authorize `GET /api/...`.

- [ ] **Step 4: Report**

Summarize verified commands/results and any remaining unknowns per the repo's Final Report Format.

---

## Self-Review

**Spec coverage:**
- Mapping (self-claim, one key) → Tasks 3, 6.
- Claim proof (full key) → Task 6 Step 5.
- Old portal replaced / public route removed → Task 6 Step 6, Task 7.
- id_token official lib → Task 5.
- Hybrid config, secret via env, fail closed → Task 2.
- Secret hygiene (redaction + rotation) → Task 1.
- Session isolation → Task 4, Task 6 Step 4.
- Frontend flow → Task 7.
- Tests/verification → Tasks 3–6, 9.

**Placeholder scan:** No TBD/TODO. Two intentional "find the existing helper" instructions (store test harness, gateway test server) are grounded with explicit grep commands, because the exact helper name must be confirmed in-repo rather than invented.

**Type consistency:** `PortalUser{GoogleSub,Email,KeyID,CreatedAt,UpdatedAt}`, `PortalUserRepo.Upsert/GetBySub/GetByKey/Delete`, `auth.IssueSessionFor/VerifySessionSub/SessionSubject`, `portalauth.Service.AuthURL/Exchange/Verify`, `portalauth.Identity{Sub,Email,EmailVerified}` are used consistently across tasks.

**Note:** `auth.Service.SessionSubject` is referenced in Task 6; it must be added in Task 4 alongside `VerifySessionSub`. Task 6 Step 5 documents this.
