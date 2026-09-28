# Landing Page Notification Settings — Design

Date: 2026-09-28
Status: Approved
Branch: `feat/notification-admin`

## Problem

The public landing page shows a bell ("Pengumuman") that opens a notification
popup. Today the notifications are hardcoded in
`frontend/src/components/NotificationPopup.tsx` (`DEFAULT_NOTIFICATIONS`), and
the file's own comment states: "the dashboard-side editor is a later feature."
Operators have no way to manage what appears there.

## Goal

Give operators an admin Settings UI to add, edit, and delete the notifications
shown on the landing page, with a hard cap of 5 notifications. Notification
content supports limited HTML (bold, links, etc.).

## Non-goals (YAGNI)

- No explicit reordering controls (list renders in stored order).
- No per-user targeting, scheduling, expiry, or read state.
- No images or media in notifications.
- No separate SQL table / migration.

## Data model & storage

Persist a JSON blob under the settings-store key `landing_notifications`,
mirroring the existing branding implementation
(`backend/internal/gateway/branding.go` + `store.SettingsRepo`). No migration.

Shape:

```json
{
  "notifications": [
    { "id": "a1b2c3d4", "tag": "BARU", "title": "DeepSeek V4.1 Flash", "body": "…", "href": "https://…" }
  ]
}
```

- `id`: server-generated short random hex (8 chars). Stable React key; never
  user-supplied. Regenerated only on create.
- `tag`: optional short uppercase badge (empty allowed).
- `title`: required, non-empty.
- `body`: HTML-allowed string.
- `href`: optional external link; empty allowed.

### Default seeding

If the stored blob is unset, empty, or unparseable, `loadLandingNotifications`
returns the current hardcoded three (DeepSeek V4.1 Flash, Pixel Canary, Space
Bunny Alpha) so the landing page is unchanged until an operator edits it.

An explicitly saved empty array (`{"notifications":[]}`) is respected and yields
the popup's existing "Belum ada notifikasi." empty state. This distinction is
detected by presence of the settings key, not by array length alone: the loader
returns defaults only when the key is absent/unreadable; a parsed blob with an
empty array returns empty.

## Backend

New file `backend/internal/gateway/notifications.go`:

- `const landingNotificationsKey = "landing_notifications"`
- `type LandingNotification struct { ID, Tag, Title, Body, Href string }` with
  JSON tags `id, tag, title, body, href`.
- `func (s *Server) loadLandingNotifications(ctx) []LandingNotification` —
  read + unmarshal + backfill defaults when absent.
- `func (s *Server) saveLandingNotifications(ctx, items) error`.
- Admin handlers:
  - `GET /api/settings/notifications` → `{"notifications":[…]}`.
  - `POST /api/settings/notifications` — body `{"notifications":[…]}`; **full
    replace** (simplest correct model for ≤5 items).
- Public handler: `GET /v1/public/notifications` → `{"notifications":[…]}` (no
  auth; mirrors `/v1/public/models`).

### Validation (all fail-closed, HTTP 400 on violation)

- At most **5** items.
- `title` trimmed non-empty, ≤ 200 chars.
- `body` ≤ 2000 chars.
- `tag` ≤ 40 chars.
- `href` empty, or an absolute URL with scheme `http`/`https`, ≤ 2048 chars.
- Unknown JSON fields rejected.
- Serialized blob size ≤ ~16 KB (defense in depth against the crude caps).

### HTML sanitization

`title`/`body` are free-form HTML and are **sanitized at every render sink**,
not parsed on the backend. The backend does not add an HTML parser dependency
(`golang.org/x/net/html` is not currently a dependency and will not be added);
it only performs the structural validation above. This is safe because the
notification HTML is never rendered server-side.

The single shared sanitizer `frontend/src/lib/sanitizeHtml.ts` (dependency-free,
`DOMParser`-based) is applied both in the public popup and in the admin live
preview, so there is one allowlist in one place:

- Allowed elements: `b, strong, i, em, u, a, br, span, code, small`.
- All other elements are unwrapped (text content kept), except
  `script`/`style`/`iframe`/`object`/`embed`, whose contents are discarded
  entirely; raw `<`/`>` text nodes are re-escaped.
- Allowed attributes: only `href` on `a`, and only when the scheme is
  `http`/`https`/`mailto` (relative/anchor hrefs are dropped to avoid open
  redirects). All other attributes are stripped. `a` elements also receive
  `rel="noopener noreferrer"` and `target="_blank"`.
- Disallowed schemes (`javascript:`, `data:`, etc.) drop the `href`.

## Frontend

### `lib/publicApi.ts`

- `export interface LandingNotification { id; tag?; title; body; href? }`
- `export const fetchPublicNotifications = () => getJSON<{notifications: LandingNotification[]}>("/v1/public/notifications").then(d => d.notifications)`.

### `lib/sanitizeHtml.ts` (new, dependency-free)

Allowlist sanitizer using `DOMParser`, mirroring the rules above. Applied in the
public popup and the admin live preview before `dangerouslySetInnerHTML`.
Unit-tested with `node --test`.

### `components/NotificationPopup.tsx`

- Fetch via `useQuery(["public-notifications"], fetchPublicNotifications)`.
- Render `title`/`body` as sanitized HTML (`dangerouslySetInnerHTML`).
- Keep `MAX_NOTIFICATIONS = 5` cap and the existing empty state.
- Optional wrapper `href` still supported.
- Remove `DEFAULT_NOTIFICATIONS` (now server-side).

### `lib/api.ts`

- Re-export/redefine `LandingNotification`.
- `notifications: () => request<{notifications: LandingNotification[]}>("GET", "/settings/notifications")`.
- `updateNotifications: (items) => request<{notifications} …>("POST", "/settings/notifications", { notifications: items })`.

### `pages/Settings.tsx`

- New tab type `"notifications"`, entry `{ value: "notifications", label: "Notification", icon: Bell }`.
- `NotificationTab` component:
  - Lists current notifications (cap 5; "Add" disabled at cap with a hint).
  - Add/Edit via `Modal` (title, tag, body HTML, href) with live sanitized
    preview and inline validation mirroring the server.
  - Delete with confirmation; saves the whole list on each change.
  - Empty state and loading/error handling consistent with other tabs.

## Testing

Backend (`gateway/notifications_test.go`):
- Defaults returned when unset; explicit empty respected.
- Validation rejects: >5 items, blank title, bad href scheme, oversize fields.
- Full-replace round-trip via handlers.
- `/v1/public/notifications` shape and no-auth access.

Frontend:
- `sanitizeHtml` unit test (`node --test`): strips `<script>`, `onerror`,
  `javascript:` href; keeps `b`/`a[href]`; re-escapes raw text.
- `tsc --noEmit` + `vite build`.
- Live verification: add a notification → appears in the landing bell; delete →
  gone; cap enforced.

## Rollout

No migration. Settings blob is seeded lazily. Rebuild image + container and
health-check as with prior features.
