# Landing Page Notification Settings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let operators add, edit, and delete (max 5) HTML-capable notifications shown on the public landing page, via a new Settings > Notification tab.

**Architecture:** Notifications are stored as a JSON blob under the settings-store key `landing_notifications`, mirroring `branding.go` (no migration). Admin endpoints live under `/api/settings/notifications`; a public read-only endpoint `/v1/public/notifications` feeds the landing popup. HTML is validated structurally on the backend and sanitized at render time by one shared, dependency-free frontend module.

**Tech Stack:** Go (chi router, `store.SettingsRepo`), React 19 + TanStack Query v5, TypeScript, `node --test`.

## Global Constraints

- Go module: `github.com/mydisha/keirouter/backend`. Run Go commands from `/home/emalution/keirouter/backend`.
- No new dependencies (Go or npm). `golang.org/x/net/html` is NOT a dependency and must not be added.
- Max 5 notifications, enforced server-side.
- `id` is server-generated (8-char lowercase hex); never accepted from the client.
- `title` required (trimmed, ≤200 chars); `body` ≤2000; `tag` ≤40; `href` empty or absolute `http`/`https` ≤2048.
- Unknown JSON fields are rejected.
- Sanitizer allowlist (single definition in `frontend/src/lib/sanitizeHtml.ts`): elements `b,strong,i,em,u,a,br,span,code,small`; only `href` on `a` with `http`/`https`/`mailto`; `a` gets `rel="noopener noreferrer"` + `target="_blank"`; `script/style/iframe/object/embed` contents discarded; all other elements unwrapped.
- Frontend typecheck: `npm run typecheck` (from `frontend/`). Build: `npm run build`.

---

### Task 1: Backend notification model, load/save, validation

**Files:**
- Create: `backend/internal/gateway/notifications.go`
- Test: `backend/internal/gateway/notifications_test.go`

**Interfaces:**
- Consumes: `s.settings *store.SettingsRepo` (interface with `Get(ctx, key) (string, error)` / `Set(ctx, key, value) error`).
- Produces:
  - `type LandingNotification struct { ID, Tag, Title, Body, Href string }` (JSON `id,tag,title,body,href`)
  - `func defaultLandingNotifications() []LandingNotification`
  - `func (s *Server) loadLandingNotifications(ctx context.Context) []LandingNotification`
  - `func (s *Server) saveLandingNotifications(ctx context.Context, items []LandingNotification) error`
  - `func validateLandingNotifications(items []LandingNotification) error`
  - `func newNotificationID() string`

- [ ] **Step 1: Write failing tests**

Create `backend/internal/gateway/notifications_test.go`:

```go
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func newNotificationsGateway(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	t.Cleanup(func() { _ = db.Close() })
	idSvc := identity.New(db.APIKeys())
	return New(Deps{Config: config.Default(), DB: db, Settings: db.Settings(), Identity: idSvc})
}

func TestLoadLandingNotificationsDefaultsWhenUnset(t *testing.T) {
	s := newNotificationsGateway(t)
	got := s.loadLandingNotifications(context.Background())
	require.Len(t, got, 3)
	require.Equal(t, "DeepSeek V4.1 Flash", got[0].Title)
}

func TestLoadLandingNotificationsRespectsExplicitEmpty(t *testing.T) {
	s := newNotificationsGateway(t)
	require.NoError(t, s.saveLandingNotifications(context.Background(), []LandingNotification{}))
	got := s.loadLandingNotifications(context.Background())
	require.Empty(t, got)
}

func TestValidateLandingNotifications(t *testing.T) {
	ok := func() []LandingNotification {
		return []LandingNotification{{ID: "a1b2c3d4", Title: "Hi", Body: "<b>x</b>"}}
	}
	require.NoError(t, validateLandingNotifications(ok()))

	// more than 5
	tooMany := make([]LandingNotification, 6)
	for i := range tooMany {
		tooMany[i] = LandingNotification{ID: "a1b2c3d4", Title: "Hi"}
	}
	require.Error(t, validateLandingNotifications(tooMany))

	// blank title
	require.Error(t, validateLandingNotifications([]LandingNotification{{ID: "a1b2c3d4", Title: "  "}}))

	// bad href scheme
	require.Error(t, validateLandingNotifications([]LandingNotification{{ID: "a1b2c3d4", Title: "Hi", Href: "javascript:alert(1)"}}))
	require.Error(t, validateLandingNotifications([]LandingNotification{{ID: "a1b2c3d4", Title: "Hi", Href: "/relative"}}))
	require.NoError(t, validateLandingNotifications([]LandingNotification{{ID: "a1b2c3d4", Title: "Hi", Href: "https://example.com"}}))

	// oversize title
	big := strings.Repeat("x", 201)
	require.Error(t, validateLandingNotifications([]LandingNotification{{ID: "a1b2c3d4", Title: big}}))
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s := newNotificationsGateway(t)
	in := []LandingNotification{{ID: "a1b2c3d4", Tag: "BARU", Title: "T", Body: "<em>b</em>", Href: "https://x.dev"}}
	require.NoError(t, s.saveLandingNotifications(context.Background(), in))
	got := s.loadLandingNotifications(context.Background())
	require.Equal(t, in, got)
}

func TestNewNotificationIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newNotificationID()
		require.Len(t, id, 8)
		require.False(t, seen[id])
		seen[id] = true
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/emalution/keirouter/backend && go test ./internal/gateway -run 'LandingNotification|ValidateLanding|SaveLoadRoundTrip|NewNotification' -v`
Expected: FAIL / build error — `undefined: ...`.

- [ ] **Step 3: Implement `notifications.go`**

Create `backend/internal/gateway/notifications.go`:

```go
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// landingNotificationsKey is the settings-store key for landing notifications.
const landingNotificationsKey = "landing_notifications"

// maxLandingNotifications caps how many notifications the landing page shows.
const maxLandingNotifications = 5

// LandingNotification is one announcement shown in the landing popup.
// Title and Body may contain limited HTML; they are sanitized at render time.
type LandingNotification struct {
	ID    string `json:"id"`
	Tag   string `json:"tag"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Href  string `json:"href"`
}

// defaultLandingNotifications preserves the pre-settings hardcoded content so
// an unconfigured deployment looks unchanged.
func defaultLandingNotifications() []LandingNotification {
	return []LandingNotification{
		{ID: "deepseek-v4.1-flash", Tag: "BARU", Title: "DeepSeek V4.1 Flash", Body: "Model flash baru dengan konteks 1.000.000 token dan harga per-1M sangat rendah."},
		{ID: "pixel-canary", Tag: "STEALTH", Title: "Pixel Canary", Body: "Akses sementara selama uji coba. Kuota terbatas dan dapat berubah tanpa pemberitahuan."},
		{ID: "space-bunny-alpha", Tag: "STEALTH", Title: "Space Bunny Alpha", Body: "Masih tersedia untuk sementara. Harga tetap Rp 1 / 1M selama periode stealth."},
	}
}

// newNotificationID returns an 8-char lowercase hex id.
func newNotificationID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "notif"
	}
	return hex.EncodeToString(b)
}

// loadLandingNotifications reads stored notifications. When the key is absent or
// unreadable it returns the defaults; an explicitly stored empty list is kept.
func (s *Server) loadLandingNotifications(ctx context.Context) []LandingNotification {
	if s.settings == nil {
		return defaultLandingNotifications()
	}
	raw, err := s.settings.Get(ctx, landingNotificationsKey)
	if err != nil || raw == "" {
		return defaultLandingNotifications()
	}
	var stored struct {
		Notifications []LandingNotification `json:"notifications"`
	}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return defaultLandingNotifications()
	}
	if stored.Notifications == nil {
		stored.Notifications = []LandingNotification{}
	}
	return stored.Notifications
}

// saveLandingNotifications persists the full list.
func (s *Server) saveLandingNotifications(ctx context.Context, items []LandingNotification) error {
	if s.settings == nil {
		return fmt.Errorf("settings store not configured")
	}
	if items == nil {
		items = []LandingNotification{}
	}
	raw, err := json.Marshal(struct {
		Notifications []LandingNotification `json:"notifications"`
	}{Notifications: items})
	if err != nil {
		return err
	}
	return s.settings.Set(ctx, landingNotificationsKey, string(raw))
}

// validateLandingNotifications enforces the structural contract. HTML content
// is not parsed here; it is sanitized at render time.
func validateLandingNotifications(items []LandingNotification) error {
	if len(items) > maxLandingNotifications {
		return fmt.Errorf("at most %d notifications are allowed", maxLandingNotifications)
	}
	for i, n := range items {
		if strings.TrimSpace(n.Title) == "" {
			return fmt.Errorf("notification %d: title is required", i+1)
		}
		if len(n.Title) > 200 {
			return fmt.Errorf("notification %d: title must be at most 200 characters", i+1)
		}
		if len(n.Body) > 2000 {
			return fmt.Errorf("notification %d: body must be at most 2000 characters", i+1)
		}
		if len(n.Tag) > 40 {
			return fmt.Errorf("notification %d: tag must be at most 40 characters", i+1)
		}
		if n.Href != "" {
			u, err := url.Parse(n.Href)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("notification %d: href must be an absolute http(s) URL", i+1)
			}
			if len(n.Href) > 2048 {
				return fmt.Errorf("notification %d: href must be at most 2048 characters", i+1)
			}
		}
	}
	return nil
}
```

The test file above imports only what Task 1 tests use: `context`, `strings`,
`testing`, `require`, `config`, `identity`, `store`. Do not import
`net/http/httptest` yet — Task 2 adds it.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/emalution/keirouter/backend && go test ./internal/gateway -run 'LandingNotification|ValidateLanding|SaveLoadRoundTrip|NewNotification' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/emalution/keirouter
git add backend/internal/gateway/notifications.go backend/internal/gateway/notifications_test.go
git commit -m "feat(gateway): landing notification model, load/save, validation"
```

---

### Task 2: Backend handlers + routes

**Files:**
- Modify: `backend/internal/gateway/notifications.go`
- Modify: `backend/internal/gateway/admin.go:144-146`
- Modify: `backend/internal/gateway/server.go:313-320`
- Test: `backend/internal/gateway/notifications_test.go`

**Interfaces:**
- Consumes: Task 1 symbols + `decodeJSON(w, r, &v) bool`, `writeJSON(w, status, v)`, `writeError(w, status, msg)`.
- Produces:
  - `func (s *Server) adminGetNotifications(w, r)`
  - `func (s *Server) adminUpdateNotifications(w, r)`
  - `func (s *Server) publicNotifications(w, r)`

- [ ] **Step 1: Write failing handler tests**

Append to `backend/internal/gateway/notifications_test.go`:

```go
func TestAdminNotificationsRoundTrip(t *testing.T) {
	s := newNotificationsGateway(t)

	// GET returns defaults first.
	rec := httptest.NewRecorder()
	s.adminGetNotifications(rec, httptest.NewRequest(http.MethodGet, "/settings/notifications", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "DeepSeek V4.1 Flash")

	// POST replaces.
	body := `{"notifications":[{"id":"a1b2c3d4","title":"Halo","body":"<b>hi</b>","href":"https://x.dev"}]}`
	rec = httptest.NewRecorder()
	s.adminUpdateNotifications(rec, httptest.NewRequest(http.MethodPost, "/settings/notifications", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code)

	// GET reflects the replacement.
	rec = httptest.NewRecorder()
	s.adminGetNotifications(rec, httptest.NewRequest(http.MethodGet, "/settings/notifications", nil))
	require.Contains(t, rec.Body.String(), "Halo")
	require.NotContains(t, rec.Body.String(), "DeepSeek V4.1 Flash")
}

func TestAdminNotificationsRejectsInvalid(t *testing.T) {
	s := newNotificationsGateway(t)
	rec := httptest.NewRecorder()
	s.adminUpdateNotifications(rec, httptest.NewRequest(http.MethodPost, "/settings/notifications",
		strings.NewReader(`{"notifications":[{"title":""}]}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPublicNotificationsNoAuth(t *testing.T) {
	s := newNotificationsGateway(t)
	rec := httptest.NewRecorder()
	s.publicNotifications(rec, httptest.NewRequest(http.MethodGet, "/v1/public/notifications", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "notifications")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/emalution/keirouter/backend && go test ./internal/gateway -run 'AdminNotifications|PublicNotifications' -v`
Expected: FAIL — `undefined: s.adminGetNotifications` etc.

- [ ] **Step 3: Implement handlers**

Append to `backend/internal/gateway/notifications.go` (and remove the two scratch lines from Task 1 Step 3):

```go
// ---- admin endpoints --------------------------------------------------------

func (s *Server) adminGetNotifications(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"notifications": s.loadLandingNotifications(r.Context())})
}

func (s *Server) adminUpdateNotifications(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}
	var body struct {
		Notifications []LandingNotification `json:"notifications"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	items := body.Notifications
	if items == nil {
		items = []LandingNotification{}
	}
	// Assign new ids to any item without one (create path).
	for i := range items {
		if strings.TrimSpace(items[i].ID) == "" {
			items[i].ID = newNotificationID()
		}
	}
	if err := validateLandingNotifications(items); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.saveLandingNotifications(r.Context(), items); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": items})
}

// ---- public endpoint --------------------------------------------------------

func (s *Server) publicNotifications(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"notifications": s.loadLandingNotifications(r.Context())})
}
```

Add the required imports to `notifications.go`: `net/http` (and keep `context`, `crypto/rand`, `encoding/hex`, `encoding/json`, `fmt`, `net/url`, `strings`, `store`).

- [ ] **Step 4: Register routes**

In `backend/internal/gateway/admin.go`, after the branding routes (line 146):

```go
	// Landing page notifications.
	r.Get("/settings/notifications", s.adminGetNotifications)
	r.Post("/settings/notifications", s.adminUpdateNotifications)
```

In `backend/internal/gateway/server.go`, inside the public landing group (after line 320):

```go
		r.Get("/v1/public/notifications", s.publicNotifications)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /home/emalution/keirouter/backend && go build ./... && go test ./internal/gateway -run 'Notification|Landing' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd /home/emalution/keirouter
git add backend/internal/gateway/notifications.go backend/internal/gateway/notifications_test.go backend/internal/gateway/admin.go backend/internal/gateway/server.go
git commit -m "feat(gateway): notification admin + public endpoints and routes"
```

---

### Task 3: Frontend sanitizer module

**Files:**
- Create: `frontend/src/lib/sanitizeHtml.ts`
- Test: `frontend/src/lib/sanitizeHtml.test.ts`
- Modify: `frontend/package.json:11`

**Interfaces:**
- Produces: `export function sanitizeHtml(html: string): string`

- [ ] **Step 1: Write failing test**

Create `frontend/src/lib/sanitizeHtml.test.ts`:

```ts
import { test } from "node:test";
import assert from "node:assert/strict";
import { sanitizeHtml } from "./sanitizeHtml.ts";

test("keeps allowlisted tags", () => {
  assert.equal(sanitizeHtml("<b>hi</b> <em>there</em>"), "<b>hi</b> <em>there</em>");
});

test("strips script contents", () => {
  assert.equal(sanitizeHtml("<script>alert(1)</script>ok"), "ok");
});

test("strips event handler attributes", () => {
  assert.equal(sanitizeHtml('<b onmouseover="x()">hi</b>'), "<b>hi</b>");
});

test("drops javascript: href", () => {
  assert.equal(sanitizeHtml('<a href="javascript:alert(1)">x</a>'), "<a>x</a>");
});

test("keeps https href with rel/target", () => {
  assert.equal(
    sanitizeHtml('<a href="https://x.dev">x</a>'),
    '<a href="https://x.dev" rel="noopener noreferrer" target="_blank">x</a>',
  );
});

test("unwraps disallowed elements", () => {
  assert.equal(sanitizeHtml("<div>hi</div>"), "hi");
});

test("drops script with attributes and keeps following text", () => {
  assert.equal(sanitizeHtml('<script type="text/javascript">evil()</script>safe'), "safe");
});

test("escapes stray angle brackets in text", () => {
  assert.equal(sanitizeHtml("a < b & c"), "a &lt; b &amp; c");
});

test("strips attribute injection on allowed tags", () => {
  assert.equal(sanitizeHtml('<b style="color:red" data-x="1">hi</b>'), "<b>hi</b>");
});

test("keeps mailto href", () => {
  assert.equal(
    sanitizeHtml('<a href="mailto:x@y.dev">mail</a>'),
    '<a href="mailto:x@y.dev" rel="noopener noreferrer" target="_blank">mail</a>',
  );
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/emalution/keirouter/frontend && node --test src/lib/sanitizeHtml.test.ts`
Expected: FAIL — cannot find module.

- [ ] **Step 3: Implement `sanitizeHtml.ts`**

Create `frontend/src/lib/sanitizeHtml.ts`:

```ts
// Allowlist HTML sanitizer for landing notifications. Dependency-free and
// environment-independent (no DOMParser), so it runs identically in the
// browser and under `node --test`. Default-deny: only allowlisted tags are
// emitted; everything else is unwrapped (markup dropped, text kept). This is
// the single source of truth for notification HTML rendering (public popup +
// admin preview).

const ALLOWED_TAGS = new Set([
  "b", "strong", "i", "em", "u", "a", "br", "span", "code", "small",
]);
const VOID_TAGS = new Set(["br"]);
const DROP_CONTENT = new Set(["script", "style", "iframe", "object", "embed"]);
const SAFE_SCHEMES = new Set(["http:", "https:", "mailto:"]);

const escapeText = (s: string): string =>
  s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

const escapeAttr = (s: string): string => s.replace(/&/g, "&amp;").replace(/"/g, "&quot;");

// safeHref returns the href only when it is an absolute http/https/mailto URL.
// Relative and anchor hrefs are dropped; so are javascript:/data:/etc.
function safeHref(raw: string): string | null {
  const href = raw.trim();
  if (!href) return null;
  try {
    if (SAFE_SCHEMES.has(new URL(href).protocol)) return href;
  } catch {
    /* not an absolute URL */
  }
  return null;
}

export function sanitizeHtml(html: string): string {
  let out = "";
  let last = 0;
  const tagRe = /<(\/?)([a-zA-Z][a-zA-Z0-9-]*)((?:"[^"]*"|'[^']*'|[^>])*?)(\/?)>/g;
  let m: RegExpExecArray | null;

  while ((m = tagRe.exec(html)) !== null) {
    out += escapeText(html.slice(last, m.index));
    last = tagRe.lastIndex;
    const closing = m[1] === "/";
    const tag = m[2].toLowerCase();
    const attrs = m[3] ?? "";

    if (DROP_CONTENT.has(tag)) {
      if (!closing) {
        const closeRe = new RegExp(`</${tag}\\s*>`, "i");
        const cm = closeRe.exec(html.slice(last));
        last = cm ? last + cm.index + cm[0].length : html.length;
        tagRe.lastIndex = last;
      }
      continue;
    }

    if (!ALLOWED_TAGS.has(tag)) {
      continue; // unwrap: drop markup, keep surrounding text
    }
    if (closing) {
      out += `</${tag}>`;
      continue;
    }
    if (VOID_TAGS.has(tag)) {
      out += `<${tag}>`;
      continue;
    }
    if (tag === "a") {
      const hm = /\bhref\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/i.exec(attrs);
      const href = hm ? safeHref(hm[1] ?? hm[2] ?? hm[3] ?? "") : null;
      out += href
        ? `<a href="${escapeAttr(href)}" rel="noopener noreferrer" target="_blank">`
        : `<a>`;
      continue;
    }
    out += `<${tag}>`;
  }

  out += escapeText(html.slice(last));
  return out;
}
```

- [ ] **Step 4: Wire the test script**

In `frontend/package.json`, update line 11:

```json
    "test": "node --test src/components/chains/chainUtils.test.ts src/lib/sanitizeHtml.test.ts",
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd /home/emalution/keirouter/frontend && npm test`
Expected: PASS (both test files).

- [ ] **Step 6: Commit**

```bash
cd /home/emalution/keirouter
git add frontend/src/lib/sanitizeHtml.ts frontend/src/lib/sanitizeHtml.test.ts frontend/package.json
git commit -m "feat(ui): dependency-free allowlist HTML sanitizer"
```

---

### Task 4: Frontend API clients

**Files:**
- Modify: `frontend/src/lib/api.ts`
- Modify: `frontend/src/lib/publicApi.ts`

**Interfaces:**
- Consumes: `request<T>(method, path, body?)` in `api.ts`; `getJSON<T>(path)` in `publicApi.ts`.
- Produces:
  - `export interface LandingNotification { id: string; tag?: string; title: string; body: string; href?: string }`
  - `api.notifications()`, `api.updateNotifications(items)`
  - `fetchPublicNotifications()`

- [ ] **Step 1: Add admin client**

In `frontend/src/lib/api.ts`, after the `BrandingSettings` interface (~line 110):

```ts
export interface LandingNotification {
  id: string;
  tag?: string;
  title: string;
  body: string;
  href?: string;
}
```

Near the branding entries (~line 1434), add:

```ts
  // Landing page notifications.
  notifications: () => request<{ notifications: LandingNotification[] }>("GET", "/settings/notifications"),
  updateNotifications: (notifications: LandingNotification[]) =>
    request<{ notifications: LandingNotification[] }>("POST", "/settings/notifications", { notifications }),
```

- [ ] **Step 2: Add public client**

In `frontend/src/lib/publicApi.ts`, add:

```ts
export interface LandingNotification {
  id: string;
  tag?: string;
  title: string;
  body: string;
  href?: string;
}
```

and after `fetchPublicModels`:

```ts
export const fetchPublicNotifications = () =>
  getJSON<{ notifications: LandingNotification[] }>("/v1/public/notifications").then((d) => d.notifications);
```

- [ ] **Step 3: Typecheck**

Run: `cd /home/emalution/keirouter/frontend && npm run typecheck`
Expected: PASS (no output).

- [ ] **Step 4: Commit**

```bash
cd /home/emalution/keirouter
git add frontend/src/lib/api.ts frontend/src/lib/publicApi.ts
git commit -m "feat(ui): notification API clients"
```

---

### Task 5: Notification popup renders API data

**Files:**
- Modify: `frontend/src/components/NotificationPopup.tsx`
- Modify: `frontend/src/components/PublicLayout.tsx:194`

**Interfaces:**
- Consumes: `fetchPublicNotifications`, `LandingNotification` (publicApi), `sanitizeHtml`.
- Produces: `NotificationPopup({ open, onClose })` — `items` prop removed.

- [ ] **Step 1: Rewrite the popup's data + render**

In `frontend/src/components/NotificationPopup.tsx`:
- Replace the local `LandingNotification`/`DEFAULT_NOTIFICATIONS` block with imports:

```tsx
import { useQuery } from "@tanstack/react-query";
import { fetchPublicNotifications, type LandingNotification } from "../lib/publicApi";
import { sanitizeHtml } from "../lib/sanitizeHtml";
```

- In `NotificationItem`, render sanitized HTML instead of plain text:

```tsx
<p
  className="truncate text-[13px] font-[650] text-[var(--ink)] [&_a]:underline"
  dangerouslySetInnerHTML={{ __html: sanitizeHtml(item.title) }}
/>
...
<p
  className="mt-0.5 text-xs leading-relaxed text-[var(--muted)] [&_a]:underline"
  dangerouslySetInnerHTML={{ __html: sanitizeHtml(item.body) }}
/>
```

- Change the component signature and fetch inside it:

```tsx
export function NotificationPopup({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { data } = useQuery({ queryKey: ["public-notifications"], queryFn: fetchPublicNotifications });
  const items = data ?? [];
  // ...existing ref/effect/list code, unchanged...
}
```

- Keep `MAX_NOTIFICATIONS = 5`, the `.slice(0, MAX_NOTIFICATIONS)` cap, and the empty state.

- [ ] **Step 2: Update the caller**

In `frontend/src/components/PublicLayout.tsx:194`, remove the `items` prop if present (it is not — confirm the call reads `<NotificationPopup open={announcementOpen} onClose={() => setAnnouncementOpen(false)} />`). No change needed if already so.

- [ ] **Step 3: Typecheck + build**

Run: `cd /home/emalution/keirouter/frontend && npm run typecheck && npm run build`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
cd /home/emalution/keirouter
git add frontend/src/components/NotificationPopup.tsx frontend/src/components/PublicLayout.tsx
git commit -m "feat(ui): landing popup renders configured notifications"
```

---

### Task 6: Settings Notification tab

**Files:**
- Modify: `frontend/src/pages/Settings.tsx`

**Interfaces:**
- Consumes: `api.notifications`, `api.updateNotifications`, `LandingNotification`, `sanitizeHtml`, `Modal`, `Field`, `Input`, `Button`, `Card`, `SectionHeader`, `Spinner`, `useToast`.
- Produces: `NotificationTab` component wired into `settingsTabs`.

- [ ] **Step 1: Add the tab entry**

In `Settings.tsx`:
- Add `Bell` to the lucide-react import.
- Add `notification` to the `SettingsTab` union and `settingsTabs`:

```ts
type SettingsTab = "saving" | "routing" | "network" | "branding" | "import-export" | "system" | "notifications";
```

```ts
  { value: "notifications" as const, label: "Notification", icon: Bell },
```

- Add the render branch after the branding line (~line 155):

```tsx
            {tab === "notifications" && <NotificationTab />}
```

- Add the import for the type: extend the existing `from "../lib/api"` import with `LandingNotification`.

- [ ] **Step 2: Implement `NotificationTab`**

Add to `Settings.tsx` (after `BrandingTab`):

```tsx
function NotificationTab() {
  const qc = useQueryClient();
  const toast = useToast();
  const list = useQuery({ queryKey: ["landing-notifications"], queryFn: () => api.notifications() });
  const save = useMutation({
    mutationFn: (items: LandingNotification[]) => api.updateNotifications(items),
    onSuccess: (data) => {
      qc.setQueryData(["landing-notifications"], data);
      qc.invalidateQueries({ queryKey: ["public-notifications"] });
      toast.success("Notifications saved");
    },
    onError: (e) => toast.error("Save failed", (e as Error).message),
  });

  const [editing, setEditing] = useState<{ index: number; item: LandingNotification } | null>(null);
  const items = list.data?.notifications ?? [];

  const persist = (next: LandingNotification[]) => save.mutate(next);

  const openAdd = () => {
    setEditing({ index: -1, item: { id: "", title: "", body: "", tag: "", href: "" } });
  };
  const openEdit = (index: number) => setEditing({ index, item: { ...items[index] } });

  const commit = () => {
    if (!editing) return;
    const it = editing.item;
    if (!it.title.trim()) return;
    const next = editing.index < 0 ? [...items, it] : items.map((x, i) => (i === editing.index ? it : x));
    persist(next);
    setEditing(null);
  };

  const remove = (index: number) => persist(items.filter((_, i) => i !== index));

  if (list.isLoading) return <Spinner />;

  return (
    <Card>
      <SectionHeader
        title="Landing page notifications"
        description={`Announcements shown in the landing page bell. Max ${MAX_NOTIFICATIONS}. Title and body support limited HTML (bold, italic, links).`}
        icon={Bell}
      />
      <div className="divide-y divide-[var(--border)] border-t border-[var(--border)]">
        {items.length === 0 && (
          <p className="px-6 py-8 text-center text-sm text-[var(--text-muted)]">No notifications yet.</p>
        )}
        {items.map((n, i) => (
          <div key={n.id || i} className="flex items-start justify-between gap-4 px-6 py-4">
            <div className="min-w-0">
              <p className="flex items-center gap-2 text-sm font-medium">
                {n.title}
                {n.tag && (
                  <span className="rounded-full bg-[var(--bg-subtle)] px-2 py-0.5 text-[10px] font-semibold uppercase text-[var(--text-muted)]">{n.tag}</span>
                )}
              </p>
              <p
                className="mt-0.5 truncate text-xs text-[var(--text-muted)] [&_a]:underline"
                dangerouslySetInnerHTML={{ __html: sanitizeHtml(n.body) }}
              />
            </div>
            <div className="flex shrink-0 gap-2">
              <Button variant="ghost" onClick={() => openEdit(i)}>Edit</Button>
              <Button variant="ghost" onClick={() => remove(i)}>Delete</Button>
            </div>
          </div>
        ))}
        <div className="flex justify-end px-6 py-4">
          <Button onClick={openAdd} disabled={items.length >= MAX_NOTIFICATIONS || save.isPending}>
            Add notification
          </Button>
        </div>
      </div>

      <Modal
        open={editing !== null}
        onClose={() => setEditing(null)}
        title={editing && editing.index < 0 ? "Add notification" : "Edit notification"}
      >
        {editing && (
          <>
            <div className="space-y-4 px-6 py-5">
              <Field label="Title (HTML allowed)">
                <Input value={editing.item.title} onChange={(e) => setEditing({ ...editing, item: { ...editing.item, title: e.target.value } })} />
              </Field>
              <Field label="Tag (optional)">
                <Input value={editing.item.tag ?? ""} onChange={(e) => setEditing({ ...editing, item: { ...editing.item, tag: e.target.value } })} />
              </Field>
              <Field label="Body (HTML allowed)">
                <Input value={editing.item.body} onChange={(e) => setEditing({ ...editing, item: { ...editing.item, body: e.target.value } })} />
              </Field>
              <Field label="Link (optional)">
                <Input value={editing.item.href ?? ""} onChange={(e) => setEditing({ ...editing, item: { ...editing.item, href: e.target.value } })} placeholder="https://…" />
              </Field>
              <div>
                <p className="mb-1 text-xs font-medium text-[var(--text-muted)]">Preview</p>
                <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-3 text-sm">
                  <p className="font-medium [&_a]:underline" dangerouslySetInnerHTML={{ __html: sanitizeHtml(editing.item.title) }} />
                  <p className="mt-0.5 text-xs text-[var(--text-muted)] [&_a]:underline" dangerouslySetInnerHTML={{ __html: sanitizeHtml(editing.item.body) }} />
                </div>
              </div>
            </div>
            <div className="flex justify-end gap-2 border-t border-[var(--border)] px-6 py-4">
              <Button variant="ghost" onClick={() => setEditing(null)}>Cancel</Button>
              <Button onClick={commit} disabled={!editing.item.title.trim() || save.isPending}>
                {save.isPending ? "Saving…" : "Save"}
              </Button>
            </div>
          </>
        )}
      </Modal>
    </Card>
  );
}
```

- [ ] **Step 3: Add needed imports to `Settings.tsx`**

Add `useMutation` to the react-query import; add `sanitizeHtml` from `../lib/sanitizeHtml`; ensure `LandingNotification` is imported from `../lib/api`; add a local `const MAX_NOTIFICATIONS = 5;`.

- [ ] **Step 4: Typecheck + build**

Run: `cd /home/emalution/keirouter/frontend && npm run typecheck && npm run build`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/emalution/keirouter
git add frontend/src/pages/Settings.tsx
git commit -m "feat(ui): Notification settings tab"
```

---

### Task 7: End-to-end verification

**Files:** none (verification only).

- [ ] **Step 1: Full backend suite**

Run: `cd /home/emalution/keirouter/backend && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 2: Frontend tests, typecheck, build**

Run: `cd /home/emalution/keirouter/frontend && npm test && npm run typecheck && npm run build`
Expected: PASS.

- [ ] **Step 3: Rebuild + health**

Run: `cd /home/emalution/keirouter && docker compose -f compose.localhost.yaml up -d --build && sleep 6 && curl -s -o /dev/null -w "healthz %{http_code}\n" http://127.0.0.1:20180/healthz`
Expected: `healthz 200`.

- [ ] **Step 4: Live API check**

```bash
COOKIE=$(curl -s -i -X POST http://127.0.0.1:20180/api/auth/login -H 'Content-Type: application/json' -d '{"password":"keirouter"}' | grep -i '^set-cookie' | sed 's/.*: //' | tr -d '\r')
curl -s -b "$COOKIE" http://127.0.0.1:20180/api/settings/notifications
curl -s http://127.0.0.1:20180/v1/public/notifications
```
Expected: both return `{"notifications":[…]}` with the 3 defaults.

- [ ] **Step 5: Live UI check (Playwright)**

Open `http://127.0.0.1:20180/ahoirilaila/settings#notifications`: add a notification with HTML (e.g. title `Hi <b>bold</b>`, body with a link), save, then open the landing page bell and confirm it appears sanitized; delete it and confirm it is gone. Confirm the "Add" button disables at 5.

- [ ] **Step 6: Commit any fixes, then finish branch**

Report results. Then follow `superpowers:finishing-a-development-branch` to merge to `main`, push to `git@github.com:wahidemalution/keirouter.git`, and rebuild.

---

## Self-Review Notes

- Spec coverage: storage model (Task 1), admin+public endpoints (Task 2), sanitizer (Task 3), clients (Task 4), popup render (Task 5), Settings tab (Task 6), verification (Task 7). Default seeding and explicit-empty distinction covered in Task 1 tests.
- No new dependencies: sanitizer is a frontend dependency-free tokenizer; backend adds none.
- Type consistency: `LandingNotification` fields `id/tag/title/body/href` identical across Go, api.ts, publicApi.ts, and the tab.
- Known simplification: the admin POST replaces the whole list rather than per-item CRUD — appropriate at the 5-item cap.
