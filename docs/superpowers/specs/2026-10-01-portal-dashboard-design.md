# Portal Dashboard — Design

Date: 2026-10-01
Status: Approved (pending implementation)
Branch: fix/dashboard-portal

## Problem

The public portal at `/portal` is a single long page. Before a user has an API
key it shows only a claim/create card; after claiming it dumps every widget
(budget, KPIs, trend, composition, models, request log) into one scroll. There
is no persistent navigation, no key visibility, and no topup history.

Goal: turn `/portal` into a multi-page dashboard that looks and behaves like the
admin dashboard — sidebar on the left, content on the right — and move API-key
creation *inside* the dashboard rather than gating entry to it.

## Decisions (agreed)

1. **Sidebar layout.** Portal gets its own nested router under `/portal` with a
   left sidebar + right content outlet, mirroring the admin `Layout`.
2. **Key creation inside the dashboard.** `/portal` no longer gates on claim.
   Creating/setting up the key happens on the dashboard and on the dedicated
   API Key page.
3. **Key preview.** The signed-in user can see a masked preview of their key
   (`kr_AbC1…7xQ2`) plus metadata. The plaintext is still shown exactly once, at
   creation/rotation time.
4. **Separate pages.** Dashboard, API Key, Usage, Models, Topup — each its own
   route. The request log and model breakdown move off the main dashboard.
5. **Topup = history only.** The Topup page shows current balance plus the
   read-only topup ledger. No payment gateway, no request form.

## Non-goals

- Payment gateway / self-service purchase of credit.
- Self-service key rotation or deletion from the portal (admin keeps that).
- Multiple keys per portal user (one key per user remains enforced).
- New database tables or migrations — every endpoint reuses existing repos.

## Routes

| Path | Page | Content |
|------|------|---------|
| `/portal` | Dashboard | Budget allocations + KPI cards + condensed usage trend + recent-requests preview. If no key: inline "Set up your key" empty state (create + claim). |
| `/portal/key` | API Key | Masked key preview + metadata; create form; claim-existing form; one-time plaintext reveal panel. |
| `/portal/usage` | Usage | Date-range filter, multi-metric trend chart, token composition, full paginated request log. |
| `/portal/models` | Models | Authorized routes (allowed models) + per-model breakdown table. |
| `/portal/topup` | Topup | Current limit/spent/remaining + read-only topup history table. |

Navigation (sidebar): logo, then Dashboard, API Key, Usage, Models, Topup;
Sign out at the bottom. Theme toggle reuses the existing component. Mobile:
hamburger drawer, matching admin behaviour.

The existing pre-auth sign-in card ("Sign in with Google") is retained for
unauthenticated visitors only. Once authenticated, the user always lands in the
sidebar shell.

## Data flow

```
/portal (root)
  → PortalBrandingProvider
  → PortalRoot: fetchPortalStatus()
      ├─ !authenticated → sign-in card (Google start)
      └─ authenticated  → PortalLayout (sidebar) → <Outlet/>
                          /            PortalDashboard
                          /key         PortalKey
                          /usage       PortalUsage
                          /models      PortalModels
                          /topup       PortalTopup
```

Every page that needs usage calls `fetchPortalUsage(days)`. The Dashboard, Key,
and Topup pages also read `fetchPortalStatus()` (which already returns
`claimed` / `has_key` / `provisioning_enabled`) to decide empty vs. populated
state. React Query keys are shared so switching pages reuses cache.

## Backend additions

Two small session-authenticated handlers, reusing existing repos — no schema
change.

`GET /portal/key` (new) → `handlePortalKey`:
- 401 without a portal session.
- 404 when the user has no binding (`portal_users` row absent).
- 200 `{ key_id, name, display, created_at, last_used_at, disabled, plan_id,
  plan_name }`.
- `display` is `store.APIKey.Display` (the masked form produced by
  `crypto.GenerateAPIKey`). The plaintext is never returned.
- `plan_name` resolved via `Plans().Get(plan_id)` when `plan_id` is set.

`GET /portal/topups` (new) → `handlePortalTopups`:
- 401 without a portal session; 404 without a binding.
- 200 `{ topups: [{ amount_usd, reason, created_at }], limit_usd, spent_usd,
  usd_remaining }` for the user's key.
- History from `Topups().ListByKey(keyID)`; the balance figures come from the
  same budget computation `buildKeyUsageMap` already performs (reusing
  `budgets.ListByScope` + `usage.SpendAndTokens`). If there is no budget, the
  limit trio is omitted and only history is returned.

Both routes are added to the existing `portalSessionMiddleware` group in
`server.go` next to `/portal/usage`.

## Frontend structure

- `lib/portalRoutes.ts` (new): `PORTAL_PREFIX = "/portal"`, `portal(path)` —
  mirrors `lib/dashboardRoutes.ts` so links stay inside the prefix.
- `pages/portal/PortalRoot.tsx` — brand provider consumer; status query;
  renders sign-in card or `PortalLayout`.
- `pages/portal/PortalLayout.tsx` — sidebar + topbar + `<Outlet/>`; sidebar
  state, Escape-to-close, body-scroll lock, title from path (same patterns as
  admin `Layout.tsx`).
- `pages/portal/PortalDashboard.tsx`, `PortalKey.tsx`, `PortalUsage.tsx`,
  `PortalModels.tsx`, `PortalTopup.tsx`.
- `pages/portal/components.tsx` — shared presentational pieces moved out of the
  current 1126-line `KeyPortal.tsx` (KPI card, budget progress, chart tooltip,
  provider icon, section title, formatting helpers, model table, request-log
  table). The existing chart/table code is reused, not rewritten.
- `pages/KeyPortal.tsx` is removed once the new pages exist.

`App.tsx`: the single `/portal` route becomes a nested route tree:
`/portal` (element `<PortalRoot/>`, wrapped in `PortalBrandingProvider`) with
child routes for `index`, `key`, `usage`, `models`, `topup`.

`routePreload.ts`: add loader keys for the new portal routes.

## Error handling

- Unauthenticated page access → sign-in card, never a dead end.
- Authenticated but no key → each page shows its own empty state; Key and
  Dashboard offer create/claim; Usage/Models/Topup explain that a key is
  required and link to `/portal/key`.
- Backend 409 (`no api key claimed`) on usage stays a client error state, but
  the status query normally prevents reaching it.
- Failed create/claim surfaces the server message inline; fail closed.

## Security

- Portal session cookie is unchanged and remains isolated from the admin
  cookie; new endpoints sit behind the same `portalSessionMiddleware`.
- `GET /portal/key` returns only the masked `display`, never the plaintext or
  hashes.
- `GET /portal/topups` scopes strictly to the caller's bound `key_id`.

## Verification

- Backend: `go build ./...`, `go vet`, `gofmt`; new gateway tests for
  `handlePortalKey` / `handlePortalTopups` (no-session → 401, no-binding → 404,
  populated → 200 with masked display and history); run `internal/gateway` +
  `internal/store` tests.
- Frontend: `tsc --noEmit`, `npm run build`, `npm test`.
- Runtime: rebuild image, recreate container, smoke `GET /portal` (SPA),
  `GET /portal/key` and `GET /portal/topups` → 401 unauthenticated.
- Known unknown: an authenticated visual walkthrough needs live Google SSO
  credentials (`.env` `GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET` +
  `KEIROUTER_PORTAL_SSO__ENABLED=true`), which the agent cannot fabricate. This
  will be reported as unverified rather than assumed.

## Reused backend facts

- `store.PortalUser{GoogleSub, Email, KeyID, PlanID}`; `PortalUsers().GetBySub`.
- `identity.Keys().Get` returns `store.APIKey` incl. `Display`, `Disabled`,
  `LastUsedAt`, `CreatedAt`.
- `Topups().ListByKey` → `store.KeyTopup` ledger.
- `budgets.ListByScope(ScopeAPIKey, keyID)` + `usage.SpendAndTokens` for
  balance; `usage.SummarizeByKey`, `usage.DailyByKey` for the rest of the usage
  payload (already surfaced by `buildKeyUsageMap`).
- Portal session helpers: `portalSubject`, `portalGoogleSub`,
  `portalSessionMiddleware`.
