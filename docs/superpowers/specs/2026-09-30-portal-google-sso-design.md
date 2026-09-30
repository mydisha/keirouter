# Portal Google SSO — Design

Date: 2026-09-30
Status: Approved (pending implementation)

## Problem

The usage portal at `/portal` currently authenticates by pasting an API key
(`?key=`) or by a **public, unauthenticated** key look-up (`?id=`). There is no
notion of a user: anyone with a leaked key ID can view that key's usage. The
goal is a real per-user dashboard where users sign in with Google.

## Decisions (agreed)

1. **Mapping**: self-claim. A user signs in with Google, then binds one API key
   by pasting the **full API key** (not just the ID).
2. **One key per user**, enforced by a `UNIQUE` constraint on `key_id`.
3. **Claim proof**: the full API key, verified by `identity.Service.Authenticate`
   (constant-time argon2). A bare key ID is never sufficient.
4. **Old portal replaced**: the paste-key and public `?id=` flows are removed;
   `GET /v1/portal/keys/{id}/usage` is deleted. `GET /v1/portal/branding` stays
   public.
5. **id_token verification**: official library `google.golang.org/api/idtoken`.
6. **Config**: hybrid. Non-secret fields in `config.yaml`; `google_client_secret`
   supplied via environment only (highest precedence in the koanf loader).
7. **Fail closed**: `enabled=true` with missing client id/secret refuses to start.

## Non-goals

- Multi-key dashboards per user.
- Admin UI to revoke claims (a follow-up; the DB row is deleted to revoke).
- Google Workspace domain-wide features beyond optional `allowed_domains`.
- Git history rewrite for the leaked Postgres password (rotation is the fix).

## Data model

New migration `0035_portal_users.sql` (portable SQLite/Postgres):

```sql
CREATE TABLE IF NOT EXISTS portal_users (
    google_sub TEXT PRIMARY KEY,
    email      TEXT NOT NULL,
    key_id     TEXT NOT NULL UNIQUE REFERENCES api_keys(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_portal_users_email ON portal_users(email);
```

- `google_sub` is Google's stable subject id, never the email.
- `key_id UNIQUE` enforces exactly one user per key.
- `ON DELETE CASCADE` on the key: deleting the API key removes the binding.
- Revocation = delete the `portal_users` row.
- Re-claim is allowed only when the target key is not already bound to another
  sub (enforced by the unique constraint; the handler returns a clear error).

## Config

`config.go` gets a new `PortalSSOConfig` on `Config`:

```go
type PortalSSOConfig struct {
    Enabled       bool          `koanf:"enabled"`
    GoogleClientID string       `koanf:"google_client_id"`
    GoogleClientSecret string   `koanf:"google_client_secret"`
    AllowedDomains []string     `koanf:"allowed_domains"`
    RedirectURL   string        `koanf:"redirect_url"`
    SessionTTL    time.Duration `koanf:"session_ttl"`
}
```

Defaults in `Default()`: `Enabled=false`, `SessionTTL=24h`.

`config.example.yaml` documents all fields with `google_client_secret: ""`
(empty; env only). `.env.example` gains the `KEIROUTER_PORTAL_SSO__*` placeholders.

Env overrides work automatically through the existing loader
(`config.go:381`, `KEIROUTER_` prefix, `__` nesting).

Validation in `validate()`: if `Enabled` and (`GoogleClientID==""` or
`GoogleClientSecret=="`), return an error and refuse to start.

## Backend

### Package `internal/portalauth`

- `Service` holds client id/secret, allowed domains, redirect URL, session TTL,
  the portal users repo, and an `*idtoken.Validator` (Google).
- `AuthURL(state string) string` — Google authorize URL, scope `openid email
  profile`, PKCE optional (confidential client; state is the CSRF guard).
- `Exchange(ctx, code) (idToken string, err error)` — server-side token exchange
  via `oauth2.Config`; never exposes the secret to the browser.
- `Verify(ctx, rawIDToken) (Identity{Sub, Email, EmailVerified}, error)` — uses
  `idtoken.NewValidator` / `Validate` with the configured audience; checks
  `email_verified==true` and, when configured, that the email domain is allowed.
- `Repo`: `Upsert`, `GetBySub`, `GetByKey`, `Delete` over `portal_users`
  (mirror the existing `store.TenantRepo` pattern).

### Session changes (`internal/auth`)

- Add `IssueSessionFor(sub string) (string, error)` and
  `VerifySessionSub(token, sub string) bool` reusing the existing payload+HMAC.
- Keep `IssueSession`/`VerifySession` for the admin dashboard as thin wrappers
  with `sub=="dashboard"`.
- `sessionMiddleware` in `auth_handlers.go` must require `sub=="dashboard"` so a
  portal cookie can never reach `/api` admin routes.
- Portal session uses a **separate cookie** (`kr_portal_session`), separate TTL,
  `HttpOnly`, `SameSite=Lax`, `Secure` per `sessionCookieSecure`.

### Endpoints (`gateway/server.go`)

Registered outside the admin group:

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| GET | `/portal/auth/google/start` | public | set state cookie, 302 to Google |
| GET | `/portal/auth/google/callback` | public | exchange code, verify id_token, set portal cookie, 302 `/portal` |
| GET | `/portal/auth/status` | portal session | `{authenticated,email,claimed,key_id}` |
| POST | `/portal/auth/claim` | portal session | body `{api_key}`; verify + bind |
| GET | `/portal/usage` | portal session | usage for the user's key |
| POST | `/portal/auth/logout` | portal session | clear cookie |

Removed: `GET /v1/portal/keys/{id}/usage`.

- `handlePortalKeyUsage` usage aggregation is factored into a shared helper
  (`buildKeyUsage(ctx, key) map`) reused by both `/portal/usage` and any
  remaining internal caller.
- Claim handler: `identity.Authenticate(ctx, plaintext)` → on success, reject if
  the key is already bound to a different `google_sub`; else upsert the binding.
- CSRF: `state` is a random value stored in a short-lived `HttpOnly` cookie and
  compared on callback.

## Frontend

- `App.tsx` `/portal` keeps `KeyPortalPage` but the page now:
  1. queries `/portal/auth/status`;
  2. if not authenticated → "Sign in with Google" button (`<a href="/portal/auth/google/start">`);
  3. if authenticated but unclaimed → claim form (full API key input);
  4. if claimed → the existing usage dashboard, fed by `/portal/usage`.
- Remove `?key=`/`?id=` handling and `fetchKeyUsageById` (`api.ts:1282`); add
  portal API functions using same-origin cookies.
- `BrandingContext` unchanged.

## Security

- Google id_token: verify signature (JWKS), `aud`, `iss`, `exp`,
  `email_verified=true`.
- Claim requires the full key; IDs alone cannot bind.
- Portal session cannot access admin `/api` (separate sub + cookie).
- Fail closed: disabled/misconfigured SSO never opens the portal.
- Secrets never logged; client secret only from env.

## Secret hygiene (separate, no code change)

- Redact the real Postgres password in
  `docs/superpowers/plans/2026-09-28-api-key-budget-topup.md:1337,1383` to
  `<REDACTED>`.
- Operator rotates `POSTGRES_PASSWORD` (server + local untracked `.env`).
- No git history rewrite.

## Testing / verification

Unit tests (`internal/portalauth`, `internal/auth`, `gateway` handlers):
- id_token rejected on wrong `aud`/`iss`/expired/`email_verified=false`.
- state mismatch rejected.
- claim with invalid key rejected; valid key binds; second user on same key rejected.
- portal cookie rejected by admin `sessionMiddleware`.
- usage endpoint returns data for the bound key only.

Build/typecheck: `go build ./...`, `go test ./...`, frontend `npm run build`
and lint. Manual: full Google login flow on `localhost:20180`.
