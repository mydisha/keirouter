# KeiRouter design system

The dashboard is an operator's console for an AI gateway: paper and ink,
hairline rules, wide margins, and one thin blue signal. This file holds the
rules. The tokens live in `frontend/src/index.css`, the primitives in
`frontend/src/components/ui/`.

## 1. Colour

All neutrals are one ladder at the brand hue (257) with chroma at or under
0.012, so greys never fight the blue.

| Role | Light | Dark |
| --- | --- | --- |
| Desk (sidebar, behind the content) | neutral-100 | neutral-975 |
| Paper (content panel, `canvas`) | neutral-50 | neutral-950 |
| Surface (cards, tables, inputs) | neutral-0 | neutral-900 |
| Text / muted / faint | neutral-925 / 700 / 600 | 0.955 / 300 / 350 |
| Hairline / strong rule | neutral-200 / 250 | neutral-750 / 0.37 |
| Input border | neutral-500 | 0.52 |

Rules:

- **The brand blue is the only signal**: links, the focus ring, the active
  nav icon and pill edge, live data, data bars. It never fills more than a
  dot, a bar or a soft tint (`brand-soft`).
- **Every button is ink.** Primary buttons are ink on paper in light mode and
  paper on ink in dark mode. Secondary buttons are outlined paper.
- Status colours (`ok`, `warn`, `bad`) mean status only, each with a `-soft`
  background. Colour is never the only signal: a label or icon says the same.
- Dark mode is charcoal with a blue cast, never pure black.
- No gradients, glows, glass, blur, or grid/dot backgrounds. Depth comes from
  desk versus paper plus hairlines.
- No raw hex or Tailwind palette classes in components; use the tokens.
- The white-label palette in Settings swaps the `accent-*` scale at runtime,
  so components reference `accent-*`, `brand*`, `link` and `ring`, never a
  literal blue.
- `npm run contrast` checks every text/surface pair in both themes against
  WCAG 2.2 AA. Change a token, rerun it.

## 2. Typography

Geist for the interface, Geist Mono for values someone might copy or compare
(keys, ids, model names, endpoints, costs, token counts, latency). Weights
400, 500 and 600 only.

| Token | Size | Use |
| --- | --- | --- |
| `text-display-2` | 28 → 38px | Page titles (`font-display`, weight 500) |
| `text-figure` | 28 → 32px | KPI figures |
| `text-title` | 18px | Dialog and sheet titles |
| `text-heading` | 15px | Section titles |
| `text-body-lg` | 15px | Page descriptions |
| `text-[13px]` | 13px | Dense UI: tables, lists, controls |
| `text-meta` | 13px | Hints, eyebrows, labels |

- Sentence case everywhere. No uppercase labels with wide tracking.
- Titles are weight 500, never semibold.
- Every number uses tabular figures (`.tnum`; tables do it by default).
- Inputs use 16px text on touch screens so iOS does not zoom.

## 3. Space, radius, elevation

- Content sits in `max-w-6xl` with a 16px gutter on phones and 24px from `md`.
  Page header to content: 32px, 40px from `md`.
- Radius roles: `rounded-md` 4px badges and chips, `rounded-lg` 6px buttons,
  inputs and menu items, `rounded-xl` 8px popovers and code, `rounded-2xl`
  12px cards, tables, dialogs. Nothing is a pill except avatars, status dots
  and switches.
- Two elevations: `shadow-paper` (a 1px lip on resting surfaces, buttons and
  inputs) and `shadow-lift` (menus, popovers, dialogs, toasts).
- One surface per region with hairlines inside it. No card in a card.
- Touch targets are 44px under `pointer: coarse`.

## 4. Shell

- The sidebar sits on the desk with no border. From `lg` the content is a
  sheet of paper floating on the desk: `rounded-2xl`, hairline, `shadow-paper`.
- The active nav item is marked by one sliding pill (`NavIndicator`), ink
  text and a blue icon. Hover is a tone change only.
- The sidebar folds to icons with ⌘B; below `lg` it is a sheet.
- The header is solid (no blur): sidebar toggle, where you are, search (⌘K),
  theme toggle, account.
- `PageHeader`: an eyebrow with a 20px brand rule naming the nav group, the
  title, one line of description, the page's actions. No icon tile.

## 5. Motion

Motion is feedback, not decoration. 150 to 450ms, `ease-out-soft`, no bounce.

- Press: `.press` scales to 0.97, never on popup triggers.
- Hover changes tone only, never layout.
- Overlays: one recipe. Fade plus a 4px shift (dialogs also scale from 0.98),
  180ms in, 120ms out. No blur, rotate or spring.
- Selection: a sliding indicator (the nav pill, tab underline).
- Theme toggle: a sun/moon morph plus one cross-fade of the page.
- Live data: `live-dot` / `animate-pulse-dot` is the only loop, and only on
  data that is actually live.
- Skeletons shimmer at 1.6s linear.
- Under reduced motion nothing moves or loops; colour and opacity fades of
  150ms or less remain.

## 6. Numbers

| Kind | Format |
| --- | --- |
| Cost | USD, up to 6 decimals per request, mono |
| Tokens | `9,812` / `12.4K` / `1.2M` |
| Latency | `840 ms` / `1.2 s` |
| Percent | One decimal |
| Missing value | Muted `n/a` |

No invented numbers. A metric with no data shows its empty state, not a zero.

## 7. Components

- **Icons**: Lucide at 16px, stroke 1.75 (set globally), only where they carry
  meaning. They sit bare beside their text (`Glyph`), never in a tinted tile.
  Icon-only buttons always have an `aria-label`.
- **Provider and tool logos**: `ProviderLogo` draws the vendor's LobeHub mark
  (MIT, vendored in `components/provider-mark/marks`) from one sprite in
  `currentColor`. Ids without a mark get a monogram. Add a mark by dropping
  its mono SVG in `marks/` (and an alias in `marks.ts` if the id differs),
  then run `npm run sprite`.
- **Badges** are small `rounded-md` stamps in `-soft` + ink. Only for real
  states, never "New" or "Beta".
- **KPIs**: one surface split by hairlines (`KpiGrid` + `Kpi`), label, figure,
  optional delta or hint. No icon tiles, no sparklines beside the figure.
- **Empty states**: one sentence plus the action that fills them. No icon.
- **Dialogs** for confirmations; never `window.confirm`.

## 8. Copy

English, sentence case. Errors say what went wrong and how to fix it. Buttons
are verbs. No buzzwords ("seamless", "powerful", "supercharge") and no claims
the product cannot back with data.
