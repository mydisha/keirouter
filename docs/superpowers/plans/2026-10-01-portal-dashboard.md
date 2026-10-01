# Portal Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Convert the one-page `/portal` into a multi-page dashboard with a left sidebar, move API-key creation inside the dashboard, and add a masked key preview plus a topup-history page.

**Architecture:** Portal gets its own nested React Router tree under `/portal`, mirroring the admin dashboard: a root auth gate renders a sign-in card or the sidebar shell (`PortalLayout`) with `<Outlet/>`. Two new session-scoped backend handlers expose the masked key metadata and the topup ledger, reusing existing repos — no schema change.

**Tech Stack:** Go (chi), SQLite/Postgres, React + React Router v6 + TanStack Query + Recharts + Tailwind, Vitest, Vite.

## Global Constraints

- Branch: `fix/dashboard-portal`.
- No new database tables or migrations.
- Portal session cookie `kr_portal_session` is isolated from the admin cookie; all new endpoints sit behind `portalSessionMiddleware`.
- `GET /portal/key` must return only the masked `store.APIKey.Display`, never plaintext or hashes.
- Reuse existing UI primitives from `frontend/src/components/ui.tsx` (`Card`, `Button`, `Input`, `Badge`, `Spinner`, `ErrorCard`, `EmptyState`, `SegmentedControl`).
- Reuse existing brand/theme: `useBranding()` from `contexts/BrandingContext`, `ThemeToggle` from `components/ThemeToggle`.
- Backend tests use the `newPortalTestServer` helper in `internal/gateway/portal_sso_test.go`.
- Verification per task: `go build ./...` + targeted `go test` for backend; `npx tsc --noEmit` + `npm run build` for frontend.

---

### Task 1: Backend — `GET /portal/key` masked key preview

**Files:**
- Modify: `backend/internal/gateway/portal_sso.go` (add handler after `handlePortalUsage`, ~line 609)
- Modify: `backend/internal/gateway/server.go` (~line 341, inside the portal session group)
- Test: `backend/internal/gateway/portal_sso_test.go`

**Interfaces:**
- Consumes: `s.portalSubject(r)`, `portalGoogleSub`, `s.db.PortalUsers().GetBySub`, `s.identity.Get`, `s.db.Plans().Get`, `writeJSON`, `writeError`.
- Produces: `func (s *Server) handlePortalKey(w http.ResponseWriter, r *http.Request)`; route `GET /portal/key`.

- [ ] **Step 1: Write the failing tests**

Append to `backend/internal/gateway/portal_sso_test.go`:

```go
func TestPortalKeyRequiresSession(t *testing.T) {
	srv := newPortalTestServer(t)
	rec := httptest.NewRecorder()
	srv.handlePortalKey(rec, httptest.NewRequest(http.MethodGet, "/portal/key", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestPortalKeyNotFoundWithoutBinding(t *testing.T) {
	srv := newPortalTestServer(t)
	tok, err := srv.auth.IssuePortalSession("portal:sub-key-1", "k@example.com")
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "/portal/key", nil)
	req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	srv.handlePortalKey(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPortalKeyReturnsMaskedPreview(t *testing.T) {
	srv := newPortalTestServer(t)
	ctx := context.Background()
	issued, err := srv.identity.Create(ctx, store.DefaultTenantID, "", "portal-key")
	require.NoError(t, err)
	require.NoError(t, srv.db.PortalUsers().Upsert(ctx, store.PortalUser{
		GoogleSub: "sub-key-2", Email: "k2@example.com", KeyID: issued.Record.ID, PlanID: "free",
	}))

	tok, err := srv.auth.IssuePortalSession("portal:sub-key-2", "k2@example.com")
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "/portal/key", nil)
	req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	srv.handlePortalKey(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, issued.Record.ID, body["key_id"])
	require.Equal(t, issued.Record.Display, body["display"])
	require.NotContains(t, rec.Body.String(), issued.Plaintext, "plaintext must never be returned")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/gateway/ -run TestPortalKey -v`
Expected: FAIL — `srv.handlePortalKey undefined`.

- [ ] **Step 3: Implement the handler**

Add to `backend/internal/gateway/portal_sso.go` after `handlePortalUsage`:

```go
// handlePortalKey returns a masked preview of the signed-in user's API key plus
// its metadata. The plaintext and hashes are never returned.
func (s *Server) handlePortalKey(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.portalSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "portal session required")
		return
	}
	u, err := s.db.PortalUsers().GetBySub(r.Context(), portalGoogleSub(sub))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no api key claimed")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load portal user")
		return
	}
	key, err := s.identity.Get(r.Context(), u.KeyID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "key no longer exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load key")
		return
	}
	out := map[string]any{
		"key_id":     key.ID,
		"name":       key.Name,
		"display":    key.Display,
		"disabled":   key.Disabled,
		"created_at": key.CreatedAt,
		"plan_id":    u.PlanID,
	}
	if key.LastUsedAt != nil {
		out["last_used_at"] = key.LastUsedAt
	}
	if u.PlanID != "" {
		if plan, err := s.db.Plans().Get(r.Context(), u.PlanID); err == nil {
			out["plan_name"] = plan.Name
		}
	}
	writeJSON(w, http.StatusOK, out)
}
```

- [ ] **Step 4: Register the route**

In `backend/internal/gateway/server.go`, inside the `r.Group` guarded by `s.portalSessionMiddleware` (right after `r.Get("/portal/usage", s.handlePortalUsage)`):

```go
		r.Get("/portal/key", s.handlePortalKey)
		r.Get("/portal/topups", s.handlePortalTopups)
```

(`handlePortalTopups` is added in Task 2; if executing tasks strictly in order, add only the `/portal/key` line now and add the topups line in Task 2.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd backend && go test ./internal/gateway/ -run TestPortalKey -v`
Expected: PASS (3 tests).

- [ ] **Step 6: Commit**

```bash
git add backend/internal/gateway/portal_sso.go backend/internal/gateway/server.go backend/internal/gateway/portal_sso_test.go
git commit -m "feat(portal): GET /portal/key masked key preview"
```

---

### Task 2: Backend — `GET /portal/topups` ledger + balance

**Files:**
- Modify: `backend/internal/gateway/portal_sso.go` (add handler)
- Modify: `backend/internal/gateway/server.go` (route already noted in Task 1 Step 4)
- Test: `backend/internal/gateway/portal_sso_test.go`

**Interfaces:**
- Consumes: `s.db.Topups().ListByKey`, `s.budgets.ListByScope`, `s.usage.SpendAndTokens`, `budget.PeriodStart`.
- Produces: `func (s *Server) handlePortalTopups(w http.ResponseWriter, r *http.Request)`; route `GET /portal/topups`.

- [ ] **Step 1: Write the failing tests**

Append to `backend/internal/gateway/portal_sso_test.go`:

```go
func TestPortalTopupsRequiresSession(t *testing.T) {
	srv := newPortalTestServer(t)
	rec := httptest.NewRecorder()
	srv.handlePortalTopups(rec, httptest.NewRequest(http.MethodGet, "/portal/topups", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestPortalTopupsReturnsLedgerAndBalance(t *testing.T) {
	srv := newPortalTestServer(t)
	ctx := context.Background()
	issued, err := srv.identity.Create(ctx, store.DefaultTenantID, "", "portal-key")
	require.NoError(t, err)
	require.NoError(t, srv.db.PortalUsers().Upsert(ctx, store.PortalUser{
		GoogleSub: "sub-top-1", Email: "t@example.com", KeyID: issued.Record.ID,
	}))
	require.NoError(t, srv.db.Topups().Create(ctx, store.KeyTopup{
		ID: "top-1", TenantID: store.DefaultTenantID, KeyID: issued.Record.ID,
		AmountMicros: 5_000_000, Reason: "goodwill", CreatedAt: time.Now(),
	}))

	tok, err := srv.auth.IssuePortalSession("portal:sub-top-1", "t@example.com")
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "/portal/topups", nil)
	req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	srv.handlePortalTopups(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Topups []struct {
			AmountUSD float64 `json:"amount_usd"`
			Reason    string  `json:"reason"`
		} `json:"topups"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Topups, 1)
	require.Equal(t, 5.0, body.Topups[0].AmountUSD)
	require.Equal(t, "goodwill", body.Topups[0].Reason)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/gateway/ -run TestPortalTopups -v`
Expected: FAIL — `srv.handlePortalTopups undefined`.

- [ ] **Step 3: Implement the handler**

Add to `backend/internal/gateway/portal_sso.go` after `handlePortalKey`. Ensure `budget` is imported as `"github.com/mydisha/keirouter/backend/internal/budget"` and `time` is already imported.

```go
// handlePortalTopups returns the read-only topup ledger for the signed-in
// user's key plus its current budget balance.
func (s *Server) handlePortalTopups(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.portalSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "portal session required")
		return
	}
	u, err := s.db.PortalUsers().GetBySub(r.Context(), portalGoogleSub(sub))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no api key claimed")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load portal user")
		return
	}

	topups, err := s.db.Topups().ListByKey(r.Context(), u.KeyID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load topups")
		return
	}
	out := make([]map[string]any, 0, len(topups))
	for _, t := range topups {
		out = append(out, map[string]any{
			"id": t.ID, "amount_usd": float64(t.AmountMicros) / 1_000_000,
			"reason": t.Reason, "created_at": t.CreatedAt,
		})
	}

	resp := map[string]any{"topups": out}
	if budgets, err := s.budgets.ListByScope(r.Context(), store.ScopeAPIKey, u.KeyID); err == nil && len(budgets) > 0 {
		b := budgets[0]
		spentMicros, _, _ := s.usage.SpendAndTokens(r.Context(), b.ScopeKind, b.ScopeID, budget.PeriodStart(b.Period, time.Now()))
		limitUSD := float64(b.LimitMicros) / 1_000_000
		spentUSD := float64(spentMicros) / 1_000_000
		remaining := limitUSD - spentUSD
		if remaining < 0 {
			remaining = 0
		}
		resp["balance"] = map[string]any{
			"limit_usd": limitUSD, "spent_usd": spentUSD, "usd_remaining": remaining,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
```

- [ ] **Step 4: Add the route (if not already added in Task 1)**

Confirm `backend/internal/gateway/server.go` has `r.Get("/portal/topups", s.handlePortalTopups)` in the portal session group.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd backend && go test ./internal/gateway/ -run TestPortalTopups -v`
Expected: PASS (2 tests).

- [ ] **Step 6: Full package check + commit**

Run: `cd backend && go build ./... && go test ./internal/gateway/ ./internal/store/`
Expected: PASS.

```bash
git add backend/internal/gateway/portal_sso.go backend/internal/gateway/server.go backend/internal/gateway/portal_sso_test.go
git commit -m "feat(portal): GET /portal/topups ledger and balance"
```

---

### Task 3: Frontend — API client + portal route helper

**Files:**
- Modify: `frontend/src/lib/api.ts` (near the existing portal functions, ~line 1359)
- Create: `frontend/src/lib/portalRoutes.ts`

**Interfaces:**
- Produces:
  - `export interface PortalKeyInfo { key_id: string; name: string; display: string; disabled: boolean; created_at: string; last_used_at?: string | null; plan_id?: string; plan_name?: string; }`
  - `export interface PortalTopup { id: string; amount_usd: number; reason: string; created_at: string; }`
  - `export interface PortalTopupData { topups: PortalTopup[]; balance?: { limit_usd: number; spent_usd: number; usd_remaining: number }; }`
  - `export async function fetchPortalKey(): Promise<PortalKeyInfo>`
  - `export async function fetchPortalTopups(): Promise<PortalTopupData>`
  - `export const PORTAL_PREFIX = "/portal"; export const portal = (path: string) => PORTAL_PREFIX + path;`

- [ ] **Step 1: Add types and fetchers to `api.ts`**

Insert after `createPortalKey` (~line 1359):

```ts
export interface PortalKeyInfo {
  key_id: string;
  name: string;
  display: string;
  disabled: boolean;
  created_at: string;
  last_used_at?: string | null;
  plan_id?: string;
  plan_name?: string;
}

export interface PortalTopup {
  id: string;
  amount_usd: number;
  reason: string;
  created_at: string;
}

export interface PortalTopupData {
  topups: PortalTopup[];
  balance?: { limit_usd: number; spent_usd: number; usd_remaining: number };
}

/** Masked metadata for the signed-in portal user's key. */
export async function fetchPortalKey(): Promise<PortalKeyInfo> {
  const resp = await fetch("/portal/key");
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error?.message || data.error || "Failed to load key");
  return data;
}

/** Read-only topup ledger + balance for the signed-in portal user. */
export async function fetchPortalTopups(): Promise<PortalTopupData> {
  const resp = await fetch("/portal/topups");
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error?.message || data.error || "Failed to load topups");
  return data;
}
```

- [ ] **Step 2: Create `portalRoutes.ts`**

```ts
// The portal dashboard lives under this prefix so the marketing sign-in card
// and the authenticated shell share /portal. portal() builds absolute URLs.
export const PORTAL_PREFIX = "/portal";

export const portal = (path: string) => `${PORTAL_PREFIX}${path}`;
```

- [ ] **Step 3: Typecheck**

Run: `cd frontend && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/lib/api.ts frontend/src/lib/portalRoutes.ts
git commit -m "feat(portal): api client fetchers and route helper"
```

---

### Task 4: Frontend — extract shared portal components

**Files:**
- Create: `frontend/src/pages/portal/components.tsx`
- Reference (do not delete yet): `frontend/src/pages/KeyPortal.tsx`

**Interfaces:**
- Produces (all exported from `components.tsx`):
  - Constants: `C_INPUT`, `C_OUTPUT`, `C_COST`, `C_REQ`
  - Components: `DateFilter`, `OverviewSection`, `TrendSection`, `InsightsSection`, `ModelSection`, `RecentRequestsSection`, `SectionTitle`, `LiveIndicator`, `KpiCard`, `BudgetProgress`, `CompositionRow`, `Highlight`, `LegendDot`, `ChartTooltip`, `ProviderIcon`, `OptBadge`, `CopyButton`
  - Utils: `aggregate`, `formatTokens`, `formatNumber`, `relativeTime`, `relTime`, `formatDateTime`, `axisTick`

- [ ] **Step 1: Move the shared code**

Create `frontend/src/pages/portal/components.tsx` with `"use client"` omitted (this is Vite, not Next). Move the following functions **verbatim** out of `frontend/src/pages/KeyPortal.tsx` into it, preserving their bodies exactly:
- Lines 19-24 chart-colour consts
- `CopyButton` (877-898), `SectionTitle` (900-910), `LiveIndicator` (912-929), `KpiCard` (931-964), `BudgetProgress` (966-993), `CompositionRow` (995-1010), `Highlight` (1012-1020), `LegendDot` (1022-1029), `ChartTooltip` (1031-1053), `ProviderIcon` (1055-1070)
- `aggregate`, `formatTokens`, `formatNumber`, `relativeTime`, `relTime`, `formatDateTime`, `axisTick` (1074-1126)
- `DateFilter` (357-377), `OverviewSection` (380-465), `TrendSection` (470-567), `RecentRequestsSection` (570-687), `OptBadge` (689-706), `optDetail` (708-723), `InsightsSection` (726-790), `ModelSection` (793-873)

Add the needed imports at the top of `components.tsx`:

```tsx
import { useState, useEffect, useMemo } from "react";
import {
  AreaChart, Area, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer,
  CartesianGrid, PieChart, Pie, Cell, ComposedChart, Line,
} from "recharts";
import {
  AlertTriangle, ArrowDownRight, ArrowUpRight, DollarSign, Layers, Key, Radio,
  TrendingUp, Coins, Calendar, Trophy, Infinity as InfinityIcon, Clock,
  ChevronLeft, ChevronRight, Copy, Check,
} from "lucide-react";
import { Card, Badge, SegmentedControl } from "../../components/ui";
import type { KeyUsageData, PortalRecentRequest } from "../../lib/api";
```

Prefix every moved declaration with `export `.

- [ ] **Step 2: Typecheck**

Run: `cd frontend && npx tsc --noEmit`
Expected: no errors (KeyPortal still has its own copies; nothing imports components yet — this only validates the new file compiles).

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/portal/components.tsx
git commit -m "refactor(portal): extract shared usage components"
```

---

### Task 5: Frontend — portal shell (root gate, sidebar layout, routes, preload)

**Files:**
- Create: `frontend/src/pages/portal/PortalRoot.tsx`
- Create: `frontend/src/pages/portal/PortalLayout.tsx`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/routePreload.ts`
- Create placeholder pages (filled in later tasks): `frontend/src/pages/portal/PortalDashboard.tsx`, `PortalKey.tsx`, `PortalUsage.tsx`, `PortalModels.tsx`, `PortalTopup.tsx`

**Interfaces:**
- Consumes: `fetchPortalStatus`, `PortalStatus` from `lib/api`; `portal` from `lib/portalRoutes`; `useBranding`; `ThemeToggle`.
- Produces: exports `PortalRootPage`, `PortalLayoutPage`, `PortalDashboardPage`, `PortalKeyPage`, `PortalUsagePage`, `PortalModelsPage`, `PortalTopupPage`.

- [ ] **Step 1: Create placeholder pages**

Each of `PortalDashboard.tsx`, `PortalKey.tsx`, `PortalUsage.tsx`, `PortalModels.tsx`, `PortalTopup.tsx` for now:

```tsx
export function PortalDashboardPage() {
  return <div className="text-[var(--text)]">Dashboard</div>;
}
```

(Adjust the exported name per file: `PortalKeyPage`, `PortalUsagePage`, `PortalModelsPage`, `PortalTopupPage`.)

- [ ] **Step 2: Create `PortalRoot.tsx`**

```tsx
import { useQuery } from "@tanstack/react-query";
import { Outlet } from "react-router-dom";
import { fetchPortalStatus } from "../../lib/api";
import { useBranding } from "../../contexts/BrandingContext";
import { Card, Button, Spinner } from "../../components/ui";

function SignInCard() {
  const { branding, logoSrc } = useBranding();
  return (
    <div className="flex min-h-screen items-center justify-center bg-[var(--bg)] p-4 md:p-8">
      <Card className="w-full max-w-md p-8 md:p-10 text-center">
        <div className="mx-auto mb-6 flex h-16 w-16 items-center justify-center rounded-2xl bg-[var(--bg-subtle)] ring-1 ring-inset ring-[var(--border)]">
          <img src={logoSrc} alt={branding.name || "KeiRouter"} className="h-8 object-contain" />
        </div>
        <h1 className="mb-2 text-2xl font-display tracking-tight text-[var(--text)]">Portal Access</h1>
        <p className="mb-8 text-sm text-[var(--text-muted)]">
          {branding.tagline || "Sign in with Google to manage your API key and monitor usage."}
        </p>
        <a href="/portal/auth/google/start" className="block">
          <Button className="w-full h-11 text-base font-medium">Sign in with Google</Button>
        </a>
      </Card>
    </div>
  );
}

export function PortalRootPage() {
  const { data: status, isLoading } = useQuery({
    queryKey: ["portal-status"],
    queryFn: fetchPortalStatus,
    retry: false,
  });
  if (isLoading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[var(--bg)]">
        <Spinner />
      </div>
    );
  }
  if (!status?.authenticated) return <SignInCard />;
  return <Outlet />;
}
```

- [ ] **Step 3: Create `PortalLayout.tsx`**

Sidebar shell mirroring the admin `Layout` patterns. Use `NavLink`, `useLocation`, `useState`, `useEffect`, `useCallback`, and `Outlet`.

```tsx
import { useState, useEffect, useCallback } from "react";
import { NavLink, Outlet, useLocation } from "react-router-dom";
import { LayoutGrid, Key, BarChart3, Layers, Wallet, LogOut, Menu, X } from "lucide-react";
import { portalLogout } from "../../lib/api";
import { useBranding } from "../../contexts/BrandingContext";
import { ThemeToggle } from "../../components/ThemeToggle";
import { portal } from "../../lib/portalRoutes";

const NAV = [
  { to: portal("/"), label: "Dashboard", icon: LayoutGrid, end: true },
  { to: portal("/key"), label: "API Key", icon: Key },
  { to: portal("/usage"), label: "Usage", icon: BarChart3 },
  { to: portal("/models"), label: "Models", icon: Layers },
  { to: portal("/topup"), label: "Topup", icon: Wallet },
];

const TITLES: Record<string, string> = {
  "/portal": "Dashboard",
  "/portal/key": "API Key",
  "/portal/usage": "Usage",
  "/portal/models": "Models",
  "/portal/topup": "Topup",
};

export function PortalLayoutPage() {
  const location = useLocation();
  const { branding, logoSrc } = useBranding();
  const [open, setOpen] = useState(false);
  const close = useCallback(() => setOpen(false), []);

  useEffect(() => {
    document.title = `${branding.name || "KeiRouter"} - ${TITLES[location.pathname] ?? "Portal"}`;
  }, [location.pathname, branding.name]);

  useEffect(() => {
    document.body.style.overflow = open ? "hidden" : "";
    return () => { document.body.style.overflow = ""; };
  }, [open]);

  const handleLogout = async () => {
    await portalLogout();
    window.location.href = "/portal";
  };

  const linkClass = ({ isActive }: { isActive: boolean }) =>
    `flex items-center gap-3 rounded-xl px-3 py-2 text-sm font-medium transition-colors ${
      isActive
        ? "bg-accent-500/10 text-accent-600 dark:text-accent-400"
        : "text-[var(--text-muted)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text)]"
    }`;

  return (
    <div className="min-h-screen bg-[var(--bg)] text-[var(--text)]">
      <aside className="fixed inset-y-0 left-0 z-40 hidden w-64 flex-col border-r border-[var(--border)] bg-[var(--bg-elevated)] p-4 md:flex">
        <div className="mb-6 flex items-center gap-3 px-2">
          <img src={logoSrc} alt={branding.name || "KeiRouter"} className="h-7 object-contain" />
        </div>
        <nav className="flex flex-1 flex-col gap-1">
          {NAV.map((n) => (
            <NavLink key={n.to} to={n.to} end={n.end} className={linkClass}>
              <n.icon size={17} /> {n.label}
            </NavLink>
          ))}
        </nav>
        <div className="mt-4 flex items-center justify-between border-t border-[var(--border)] pt-4">
          <ThemeToggle />
          <button onClick={handleLogout} className="flex items-center gap-2 text-sm text-[var(--text-muted)] hover:text-[var(--text)]">
            <LogOut size={16} /> Sign out
          </button>
        </div>
      </aside>

      {open && (
        <div className="fixed inset-0 z-40 md:hidden" role="dialog">
          <div className="absolute inset-0 bg-black/40" onClick={close} />
          <aside className="absolute inset-y-0 left-0 w-64 bg-[var(--bg-elevated)] p-4">
            <button className="mb-4 text-[var(--text-muted)]" onClick={close} aria-label="Close menu"><X size={18} /></button>
            <nav className="flex flex-col gap-1">
              {NAV.map((n) => (
                <NavLink key={n.to} to={n.to} end={n.end} className={linkClass} onClick={close}>
                  <n.icon size={17} /> {n.label}
                </NavLink>
              ))}
            </nav>
          </aside>
        </div>
      )}

      <div className="md:pl-64">
        <header className="sticky top-0 z-30 flex items-center justify-between border-b border-[var(--border)] bg-[var(--bg)]/90 px-4 py-3 backdrop-blur md:px-8">
          <button className="md:hidden text-[var(--text-muted)]" onClick={() => setOpen(true)} aria-label="Open menu"><Menu size={20} /></button>
          <h1 className="text-lg font-display font-semibold">{TITLES[location.pathname] ?? "Portal"}</h1>
          <div className="md:hidden"><ThemeToggle /></div>
        </header>
        <main className="mx-auto max-w-[1040px] p-4 md:p-8">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Wire the routes in `App.tsx`**

Replace the single portal route (lines 55-59) with a nested tree. Also change the lazy imports: keep `KeyPortalPage` import for now (removed in Task 11) and add the portal page imports.

```tsx
const PortalRoot = lazy(routeLoaders["/portal"]);
const PortalLayout = lazy(routeLoaders["/portal-layout"]);
const PortalDashboard = lazy(routeLoaders["/portal-dashboard"]);
const PortalKeyPageRoute = lazy(routeLoaders["/portal-key"]);
const PortalUsageRoute = lazy(routeLoaders["/portal-usage"]);
const PortalModelsRoute = lazy(routeLoaders["/portal-models"]);
const PortalTopupRoute = lazy(routeLoaders["/portal-topup"]);
```

Route tree:

```tsx
<Route path="portal" element={
  <PortalBrandingProvider>
    <PortalRoot />
  </PortalBrandingProvider>
}>
  <Route element={<PortalLayout />}>
    <Route index element={<PortalDashboard />} />
    <Route path="key" element={<PortalKeyPageRoute />} />
    <Route path="usage" element={<PortalUsageRoute />} />
    <Route path="models" element={<PortalModelsRoute />} />
    <Route path="topup" element={<PortalTopupRoute />} />
  </Route>
</Route>
```

- [ ] **Step 5: Add preload entries in `routePreload.ts`**

```ts
  "/portal": () => named(import("./pages/portal/PortalRoot"), "PortalRootPage"),
  "/portal-layout": () => named(import("./pages/portal/PortalLayout"), "PortalLayoutPage"),
  "/portal-dashboard": () => named(import("./pages/portal/PortalDashboard"), "PortalDashboardPage"),
  "/portal-key": () => named(import("./pages/portal/PortalKey"), "PortalKeyPage"),
  "/portal-usage": () => named(import("./pages/portal/PortalUsage"), "PortalUsagePage"),
  "/portal-models": () => named(import("./pages/portal/PortalModels"), "PortalModelsPage"),
  "/portal-topup": () => named(import("./pages/portal/PortalTopup"), "PortalTopupPage"),
```

- [ ] **Step 6: Typecheck and build**

Run: `cd frontend && npx tsc --noEmit && npm run build`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/App.tsx frontend/src/routePreload.ts frontend/src/pages/portal/
git commit -m "feat(portal): sidebar shell, nested routes, auth gate"
```

---

### Task 6: Frontend — Dashboard page

**Files:**
- Modify: `frontend/src/pages/portal/PortalDashboard.tsx`

**Interfaces:**
- Consumes: `fetchPortalStatus`, `fetchPortalUsage`, `createPortalKey`, `claimPortalKey`; `OverviewSection`, `TrendSection`, `RecentRequestsSection` from `./components`.

- [ ] **Step 1: Implement the page**

Render, in order, inside a spaced column: a header (key name/ID or welcome), then:
- If `status.has_key === false`: an "unlock" card with the create form (calls `createPortalKey`, then invalidates `["portal-status"]`) and a claim-existing form; `provisioning_enabled === false` hides the create button and shows "contact an admin".
- Else: `<DateFilter>` (local `days` state, default 30), `<OverviewSection d={data} />`, `<TrendSection daily={data.daily} days={days} />`, and `<RecentRequestsSection recent={data.recent.slice(0, 10)} days={days} />`.
- Loading → `Spinner`; error → `ErrorCard`.

Use `useQuery({ queryKey: ["portal-usage", days], queryFn: () => fetchPortalUsage(days), enabled: !!status?.has_key, refetchInterval: 30000 })`. When the user creates a key, show the returned plaintext once in a reveal card with a `CopyButton` and a "Continue" button that invalidates `["portal-status"]` and `["portal-key"]`.

- [ ] **Step 2: Typecheck and build**

Run: `cd frontend && npx tsc --noEmit && npm run build`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/portal/PortalDashboard.tsx
git commit -m "feat(portal): dashboard page with inline key setup"
```

---

### Task 7: Frontend — API Key page

**Files:**
- Modify: `frontend/src/pages/portal/PortalKey.tsx`

**Interfaces:**
- Consumes: `fetchPortalKey`, `fetchPortalStatus`, `createPortalKey`, `claimPortalKey`; `CopyButton`, `SectionTitle` from `./components`.

- [ ] **Step 1: Implement the page**

- Query `fetchPortalKey` with `queryKey: ["portal-key"]`, `retry: false`; query `fetchPortalStatus` for `provisioning_enabled`.
- On 404 (`no api key claimed`): show setup card — create button (if `provisioning_enabled`) + claim-existing form.
- On success: show a metadata card (name, masked `display` in a mono block with `CopyButton`, key ID, plan name, created date, last-used date, disabled badge) and a create/claim section hidden behind a `<details>`.
- Creating shows the plaintext once in a reveal panel; "Done" invalidates `["portal-status"]` and `["portal-key"]`.

- [ ] **Step 2: Typecheck and build**

Run: `cd frontend && npx tsc --noEmit && npm run build`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/portal/PortalKey.tsx
git commit -m "feat(portal): API key page with masked preview"
```

---

### Task 8: Frontend — Usage page

**Files:**
- Modify: `frontend/src/pages/portal/PortalUsage.tsx`

**Interfaces:**
- Consumes: `fetchPortalStatus`, `fetchPortalUsage`; `DateFilter`, `OverviewSection`, `TrendSection`, `InsightsSection`, `RecentRequestsSection` from `./components`.

- [ ] **Step 1: Implement the page**

- If `status.has_key === false` → `EmptyState` with a link to `portal("/key")`.
- Else: local `days` state; `DateFilter`; `OverviewSection`; `TrendSection`; `InsightsSection`; full `RecentRequestsSection` (paginated).

- [ ] **Step 2: Typecheck and build**

Run: `cd frontend && npx tsc --noEmit && npm run build`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/portal/PortalUsage.tsx
git commit -m "feat(portal): dedicated usage page"
```

---

### Task 9: Frontend — Models page

**Files:**
- Modify: `frontend/src/pages/portal/PortalModels.tsx`

**Interfaces:**
- Consumes: `fetchPortalStatus`, `fetchPortalUsage`; `ModelSection`, `SectionTitle` from `./components`.

- [ ] **Step 1: Implement the page**

- If `status.has_key === false` → `EmptyState` linking to `portal("/key")`.
- Else: `ModelSection` from the usage payload; above it, an "Authorized Routes" card rendering `data.allowed_models` as chips (copy the markup from `KeyPortal.tsx` lines 327-341, adapting to the new file).

- [ ] **Step 2: Typecheck and build**

Run: `cd frontend && npx tsc --noEmit && npm run build`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/portal/PortalModels.tsx
git commit -m "feat(portal): models and authorized routes page"
```

---

### Task 10: Frontend — Topup page

**Files:**
- Modify: `frontend/src/pages/portal/PortalTopup.tsx`

**Interfaces:**
- Consumes: `fetchPortalTopups`, `fetchPortalStatus`; `formatDateTime` from `./components`; `microsToUSD` or inline formatting.

- [ ] **Step 1: Implement the page**

- If `status.has_key === false` → `EmptyState` linking to `portal("/key")`.
- Query `fetchPortalTopups` (`queryKey: ["portal-topups"]`, `retry: false`).
- Show a balance card when `data.balance` exists (limit / spent / remaining), else "No budget limit configured".
- Show a table of `data.topups` (date via `formatDateTime`, reason, amount `$${amount_usd.toFixed(2)}`); empty → `EmptyState` "No top-ups yet".
- Add a small note: "Top-ups are applied by an administrator."

- [ ] **Step 2: Typecheck and build**

Run: `cd frontend && npx tsc --noEmit && npm run build`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/portal/PortalTopup.tsx
git commit -m "feat(portal): topup history page"
```

---

### Task 11: Remove old page, full verification, runtime smoke

**Files:**
- Delete: `frontend/src/pages/KeyPortal.tsx`
- Modify: `frontend/src/routePreload.ts` (remove the `/portal` `KeyPortal` loader if any duplicate remains — `/portal` now points to `PortalRoot`)
- Modify: `frontend/src/App.tsx` (remove the `KeyPortalPage` lazy import)

- [ ] **Step 1: Remove the old component**

```bash
git rm frontend/src/pages/KeyPortal.tsx
```

Remove `const KeyPortalPage = lazy(...)` from `App.tsx` and confirm no `import("./pages/KeyPortal")` remains:

Run: `cd frontend && grep -rn "KeyPortal" src/ || echo "no references"`

- [ ] **Step 2: Full frontend verification**

Run: `cd frontend && npx tsc --noEmit && npm run build && npm test`
Expected: build succeeds; tests pass (16/16).

- [ ] **Step 3: Full backend verification**

Run: `cd backend && go build ./... && go vet ./internal/gateway/ ./internal/store/ && go test ./internal/gateway/ ./internal/store/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "refactor(portal): remove legacy single-page key portal"
```

- [ ] **Step 5: Rebuild and smoke test**

```bash
docker compose -f compose.localhost.yaml build keirouter
docker compose -f compose.localhost.yaml up -d keirouter
sleep 6
curl -s -o /dev/null -w "portal:%{http_code}\n" http://127.0.0.1:20180/portal
curl -s -o /dev/null -w "portal-key:%{http_code}\n" http://127.0.0.1:20180/portal/key
curl -s -o /dev/null -w "portal-topups:%{http_code}\n" http://127.0.0.1:20180/portal/topups
curl -s -o /dev/null -w "health:%{http_code}\n" http://127.0.0.1:20180/healthz
```

Expected: `portal:200` (SPA), `portal-key:401`, `portal-topups:401`, `health:200`.

- [ ] **Step 6: Report**

State verified facts (commands + results). Explicitly report the known unknown: an authenticated visual walkthrough requires live Google SSO credentials the agent cannot fabricate.

---

## Self-Review

**Spec coverage:**
- Sidebar layout → Task 5 (PortalLayout). ✓
- Key creation inside dashboard → Tasks 6, 7. ✓
- Masked preview → Tasks 1, 7. ✓
- Separate pages (Usage/Models/Topup) → Tasks 8, 9, 10. ✓
- Topup history-only → Task 10 (uses Task 2 endpoint). ✓
- No new tables/migrations → confirmed, no migration task. ✓
- Verification + unknown SSO creds → Task 11. ✓

**Placeholder scan:** Backend tasks contain full test + implementation code. Frontend tasks specify exact files, exports, data sources, control flow, and precise line references for moved markup — no "TBD"/"implement later".

**Type consistency:** `PortalKeyInfo`/`PortalTopupData` in Task 3 match handler JSON in Tasks 1-2. `CopyButton`, `SectionTitle`, `OverviewSection`, `TrendSection`, `InsightsSection`, `ModelSection`, `RecentRequestsSection`, `DateFilter` exported in Task 4 and consumed in Tasks 6-10. `portal()` helper from Task 3 used in Tasks 6-10. Route exports `PortalRootPage` etc. consistent between Task 5, `App.tsx`, and `routePreload.ts`.

**Gap found & fixed:** Task 1 Step 4 and Task 2 Step 4 both mention the `/portal/topups` route to avoid a broken build if tasks run out of order — the second execution is a no-op.
