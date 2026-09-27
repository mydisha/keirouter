# Public Landing Page — Design Spec

Date: 2026-09-26
Branch: `feat/landing-page`
Status: Approved (design), pending implementation plan

## 1. Goal

Serve a public marketing/dashboard landing page at the site root (`/`) that is
reachable directly at `http://<host>/` with **no login**, visual clone of
`nutaraline.co.uk` per `design.md`, and whose numbers come **live from the
KeiRouter dashboard data** via a purpose-built, strictly read-only public API.

The dashboard moves behind a secret prefix so `/` is free for the landing page.

## 2. Non-goals

- No billing/purchase flow. The `#purchase` section is informational only
  (shows how to top up / contact), no payment integration.
- No public write endpoints of any kind.
- No exposure of key names, key ids, request ids, raw prompts, upstream error
  bodies, provider credentials, per-request cost, or per-user identity.
- No new frontend dependency (no Three.js/GSAP); the site's 2D SVG glyph
  fallback is what we replicate.

## 3. Routing changes (frontend)

Current: `/` is the authenticated dashboard; `AuthGate` wraps everything.

New:

| Path | Renders |
|---|---|
| `/` | `PublicLanding` — public, no `AuthGate` |
| `/ahoirilaila` | `AuthGate` login screen |
| `/ahoirilaila/*` | existing dashboard routes (index, providers, keys, …) |
| `/portal` | unchanged (public key portal) |
| `/oauth/callback` | unchanged (standalone) |

Implementation: in `frontend/src/App.tsx`, add the public route at the top
level, and wrap the existing authenticated `<Routes>` in a
`<Route path="ahoirilaila">` parent whose child `<Route element={<Layout />}>`
holds the current children. The `oauth/callback` route stays standalone as today.
Most Layout children use relative `to="providers"`-style paths, but a handful of
absolute links (`to="/keys|/providers|/usage|/plans|/cli-tools|/provider-health"`)
and `navigate("/keys")`, `navigate("/chains/...")`, `navigate("/media/...")`
calls exist. These must be updated to stay within the prefix. Chosen approach:
add a single `DASHBOARD_PREFIX = "/ahoirilaila"` constant and prefix the absolute
targets (6 link sites, ~8 navigate call sites). Audit with the grep in §9.

Also update `frontend/src/routePreload.ts` only if needed — preload keys are
internal names, not URLs, so they are unchanged. `vite.config.ts` dev proxies
stay as-is (they proxy `/api`, `/v1`, `/healthz`).

Server SPA fallback already serves `index.html` for unmatched paths
(`backend/internal/gateway/server.go:358-373`), so deep-linking
`/ahoirilaila/keys` works in production without extra config.

## 4. Backend: public read-only API

New file `backend/internal/gateway/public.go`, registered in
`Server.routes()` (`server.go`) as a group **outside** `s.sessionMiddleware`
and **outside** `s.loopbackOnly`, but behind a per-IP rate limiter:

```go
r.Group(func(r chi.Router) {
    r.Use(s.publicRateLimiter) // reuse newGuardrailTestRateLimiter-style token bucket
    r.Get("/v1/public/overview", s.publicOverview)
    r.Get("/v1/public/models", s.publicModels)
    r.Get("/v1/public/performance", s.publicPerformance)
    r.Get("/v1/public/archived", s.publicArchived)
})
```

Design rules (binding):

1. **GET only.** No POST/PUT/PATCH/DELETE under `/v1/public`.
2. **No caller-supplied identifiers.** `performance` accepts only a `model`
   string matched against the already-public catalog; any unknown value returns
   `400`. There is no object-id lookup, so there is no IDOR surface.
3. **No dispatch.** Handlers never call the dispatcher, provider connectors, or
   any upstream. They only read aggregates from `UsageRepo` / pricing tables.
4. **No secrets in payload.** No `request_id`, key id/name, account id, token,
   base URL, or raw error text. Identity is anonymized (§4.2).
5. **Aggregate-only.** Payloads are rollups (totals, per-model, latency bands,
   hourly bars) computed from `SummarizeAccurate`, `ByModelAccurate`,
   `BreakdownAccurate`, `TimelineAccurate`, and a new anonymized recent-records
   projection.
6. **Graceful empty.** Empty DB returns HTTP 200 with zeroed totals and empty
   arrays, never 500.

### 4.1 Endpoints

`GET /v1/public/overview`
- `total_requests`, `total_tokens`, `rps_10s`, `success_24h`, `failed_24h`,
  `total_cost_usd` (aggregate only), `providers` (display name + share, no
  credential fields), `recent` (≤10, anonymized, see §4.2), `top_models`
  (≤10, display name + request/token counts).

`GET /v1/public/models`
- Catalog = operator-defined routing chains only; each entry is one chain
  (provider `combo`).
- Each: `name`, `model_id`, `provider` (display name), `input_per_m`,
  `output_per_m`, `discount_pct` (computed from list vs. effective rate when
  both known; omitted otherwise), `capabilities` (vision/reasoning/tools/…),
  `usage_24h` (`{users, requests, tokens}` where `users` = distinct keys, count only).

`GET /v1/public/performance?model=<model_id>`
- Latency bands (small <8k / medium 8k–200k / large ≥150k context),
  `avg_latency_ms`, `avg_ttft_ms`, `success_rate`, and a 24-point hourly
  request/token series. Unknown `model` → `400 {"error":"unknown model"}`.

`GET /v1/public/archived`
- Podium (top 2 by all-time token) + per-model history (`status: "arsip"`,
  token split, active date range). Empty → `200` with empty arrays.

### 4.2 Anonymization

Add `UsageRepo.PublicRecentRecords(ctx, tenantID, since, limit)` (new method,
`repo_usage_details.go`) that selects **only** `provider, model, status,
latency_ms, ttft_ms, prompt_tokens, completion_tokens, created_at`. It never
selects `request_id`, `id`, `error_kind`, or any pricing/key column. The
handler maps rows to public JSON with no key identity.

Leaderboard/`top users` in the clone maps to "top models" (aggregate), not API
keys, per the approved privacy decision. If a distinct-user count is shown, it
is a count of distinct keys, never a name.

### 4.3 Reused internals

- `s.usage.SummarizeAccurate`, `BreakdownAccurate`, `ByModelAccurate` (existing).
- Pricing: existing model-price table used by `adminModelUsageAccurate`
  (`InputRatePerM`, `OutputRatePerM`). No new pricing source.
- Caching: reuse `s.insightsCache` + `cacheHit`/`writeJSONCached` with distinct
  `public-*` keys (e.g. `public-overview|24h`). Aggregates are cheap enough for
  a 30–60s TTL.
- Rate limit: a small token-bucket middleware mirroring
  `newGuardrailTestRateLimiter` (per-IP, e.g. 60 req/min, burst 30).

## 5. Frontend landing implementation

New files under `frontend/src/`:

- `pages/PublicLanding.tsx` — sections `#overview` (Monitor), `#models`,
  `#purchase`, `#balance`, footer. Uses TanStack Query against `/v1/public/*`,
  `staleTime` 30–60s.
- `components/PublicLayout.tsx` — floating pill nav (5 tabs per `design.md` §5),
  announcement button, theme toggle; mobile top bar + bottom `.app-dock`.
- `components/ModelGlyph.tsx` — all 14 glyph paths from `design.md` §7.1, keyed
  by `data-family`/model id, with `.glyph-depth` + `.glyph-face` layers.
- `components/RankCrown.tsx` — gold/silver/bronze medals (`design.md` §7.3).
- `components/AnnouncementDialog.tsx` — native `<dialog>` (`design.md` §8),
  support CTA `https://wa.me/84826240052`.
- `components/ModelCapabilityIcons.tsx` — extend existing with Tools + exact
  paths (`design.md` §7.2 and the capability matrix).
- `lib/publicApi.ts` — typed fetch helpers for the four public endpoints,
  matching backend JSON exactly. No `api.ts` admin calls are imported by the
  landing bundle (keeps admin client out of the public chunk).

Styling / tokens:
- Scope the landing palette under a `.landing-root` class with
  `[data-theme="dark"]` variant so it does not collide with the dashboard's
  `.dark` theme (documented mismatch in `design.md` §10).
- Self-host DM Sans: add
  `frontend/public/assets/fonts/dm-sans-latin-variable.woff2` + `@font-face`
  in `frontend/src/index.css`.

Routing: when `path === "/"`, render `PublicLanding` outside `AuthGate`.
Otherwise render the `AuthGate`-wrapped dashboard under the `ahoirilaila` prefix.

Empty/degraded states: every section renders a zeroed/empty state instead of an
error card when the public API returns empty or the request fails. Never a
blank screen, never an admin error surface.

## 6. Security acceptance criteria

1. `grep -rE '"/(keys|providers|usage|plans|chains|media|settings|models/disabled|accounts)' backend/internal/gateway/public.go` → no matches.
2. Public payloads contain none of: `request_id`, `key_id`, `key_name`,
   `account`, `api_key`, `base_url`, `error_kind`, `pricing_key`. Covered by
   `public_handlers_test.go`.
3. A request to `/v1/public/performance?model=../../etc` (or unknown) → `400`,
   never a filesystem/DB error leak.
4. Public handlers never implement `http.Handler` calls that reach the
   dispatcher — asserted by a test that stubs a dispatcher and asserts zero calls.
5. Per-IP rate limiter returns `429` beyond budget.
6. `POST /v1/public/*` → `405`/`404`.

## 7. Testing

- Go: `backend/internal/gateway/public_handlers_test.go` — table tests for each
  endpoint on empty DB (200, zeroed) and seeded records (correct aggregates),
  plus the §6 assertions (no secret keys, unknown-model 400, rate-limit 429).
- Frontend: `tsc -b` typecheck and `vite build` must pass; manual check that
  `/` renders with the backend up, with the backend down (graceful empty), and
  that `/ahoirilaila` still gates the dashboard.

## 8. Rollout / deploy notes

- `frontend/dist` is served by the Go binary; no server route table change is
  needed beyond the new public group.
- The landing must work when the operator has zero configured providers
  (empty states), so it is safe to enable by default.
- No config flag required for v1. (Optional future: `server.public_landing`
  toggle; out of scope.)

## 9. Audit greps (for the planner)

Absolute dashboard links/calls to re-prefix:

```
grep -rEo 'to="/[a-z0-9/-]*"' frontend/src --include=*.tsx
grep -rn 'navigate("/' frontend/src --include=*.tsx
```

Known sites: `pages/Chains.tsx`, `pages/KeyDetail.tsx`,
`pages/MediaProviders.tsx`, `pages/ProviderDetail.tsx`, `pages/Keys.tsx`,
`pages/ChainEditor.tsx`, plus any `Link to="/..."` in `components/`.

## 10. Open questions

None. All product decisions resolved:

- Root = landing; dashboard under `/ahoirilaila/*`.
- Catalog = operator-defined routing chains only; each entry is one chain (provider `combo`).
- Leaderboard identity anonymized; aggregate displayed.
- Graceful empty state instead of error.
