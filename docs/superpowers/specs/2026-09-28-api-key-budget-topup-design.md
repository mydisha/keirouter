# API Key Budget Top-up — Design

Date: 2026-09-28
Branch: `feat/api-budget-limit-topup`
Status: approved for implementation

## Goal

Give every API key a **budget** (USD credit limit) that can be **topped up** from
the API Key detail page. Each top-up is recorded with full detail (amount,
reason, when, limit before → after) so the key always has a complete top-up
history. The API Keys list also shows a spend/credit progress bar per key.

## Non-goals

- No payment gateway / invoice integration. A top-up is an operator-recorded
  credit adjustment.
- No multi-currency. USD only.
- No change to the public portal endpoints.
- No change to the credit-*reservation* flow; the existing single-node
  in-memory reservation map is reused as-is.

## Money-safety principles (binding)

This feature moves real money-equivalent credit. The following are hard
requirements, not options:

1. **Integer micro-USD only.** All amounts are `int64` micro-USD
   (1 USD = 1,000,000 micros). Never use `float64` for stored or accumulated
   amounts. JSON accepts a decimal `amount_usd` but it is converted to micros
   once at the boundary and validated as integer-safe.
2. **Atomic increment.** `limit_micros` is increased with
   `UPDATE budgets SET limit_micros = limit_micros + ?` inside the same
   transaction that inserts the top-up row. It is never read-modify-write in
   Go, so concurrent top-ups cannot lose an update.
3. **Single transaction.** Budget row lock + `limit_micros` increment + top-up
   history insert + before/after snapshot all commit or all roll back.
4. **Idempotency.** Top-up requires a client-supplied `Idempotency-Key`
   (UUID); a unique index on `(key_id, idempotency_key)` makes retries safe. A
   replayed request returns the original record and does **not** double-credit.
5. **Overflow guard.** Increment is rejected if the resulting `limit_micros`
   would exceed `math.MaxInt64`. Amounts above a sane per-request ceiling
   (e.g. 1,000,000 USD) are rejected.
6. **Positive-only, bounded input.** `amount_usd` must be finite, `> 0`, and
   within ceiling. Negative/zero/NaN/Inf rejected with 400.
7. **Append-only history.** `key_topups` rows are never updated or deleted.
8. **No secrets.** Reason is operator free text; the amount and reason are
   stored, but no key material is written to logs or history.
9. **Exact decimal parsing.** `amount_usd` is parsed from its JSON token as a
   decimal string and converted to micros with rounding-free logic; a value
   with more than 6 decimal places is rejected rather than rounded.

Accuracy note: the existing budget engine evaluates
`spent >= limit_micros` (backend/internal/budget/budget.go:213). Top-up only
raises `limit_micros`, so enforcement, alert thresholds, and the portal budget
read all keep working unchanged and remain the single source of truth.

## Data model

New migration `0033_key_topups.sql` (plain SQL; all TEXT/BIGINT works on both
SQLite and Postgres):

```sql
CREATE TABLE IF NOT EXISTS key_topups (
  id                    TEXT PRIMARY KEY,
  tenant_id            TEXT NOT NULL,
  key_id               TEXT NOT NULL,
  budget_id            TEXT NOT NULL,
  amount_micros        BIGINT NOT NULL,
  reason               TEXT NOT NULL DEFAULT '',
  limit_before_micros  BIGINT NOT NULL,
  limit_after_micros   BIGINT NOT NULL,
  idempotency_key      TEXT NOT NULL DEFAULT '',
  actor                TEXT NOT NULL DEFAULT '',
  created_at           TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_key_topups_key ON key_topups(key_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_key_topups_idem
  ON key_topups(key_id, idempotency_key) WHERE idempotency_key <> '';
```

`actor` is recorded as the dashboard session label (currently a single
operator, so `"dashboard"`); the column exists so a future auth model can fill
it without a migration.

## Backend

### Store — `backend/internal/store/repo_topups.go`

`KeyTopupRepo` with:
- `CreateOnTx(ctx, tx, KeyTopup) error`
- `Create(ctx, KeyTopup) error`
- `ListByKey(ctx, keyID) ([]KeyTopup, error)` — newest first
- `GetByIdempotencyKey(ctx, keyID, idem) (KeyTopup, error)` → `ErrNotFound`

Plus `BudgetRepo.IncrementLimitOnTx(ctx, tx, budgetID, deltaMicros) (afterMicros, error)`
using `UPDATE ... SET limit_micros = limit_micros + ? WHERE id = ?` then
`SELECT limit_micros ... RETURNING` / re-read, and returning `ErrNotFound` if
no row matched.

### Handler — `POST /api/keys/{id}/topup`

Request:
```json
{ "amount_usd": 25.5, "reason": "manual invoice #123", "idempotency_key": "uuid" }
```
`Idempotency-Key` header is accepted as a fallback for the body field.

Flow (all inside one DB transaction after cheap validation):
1. Verify key exists (`identity.Keys().Get`); 404 otherwise.
2. Validate amount (positive, finite, ≤ ceiling, ≤ 6 decimals) → 400.
3. Find the key's `api_key` budget (`budgets.ListByScope`). If none exists,
   create one with `limit_micros = 0`, `period = "total"`, `alert_pct = 80`,
   `hard_cutoff = true` in the same tx, so a top-up works on a key with no
   budget. If one exists but its period is not `"total"`, still top up that
   budget (operator's choice) and report its period in the response.
4. Replay check: if `idempotency_key` is set and a top-up already exists for
   `(key_id, idempotency_key)`, return it with 200 and no new credit.
5. `IncrementLimitOnTx` by `amount_micros`; compute before = after − amount.
6. Insert the `key_topups` row (before/after snapshot, reason, actor, idem).
7. Commit. Then `budgetEngine.InvalidateBudgetCacheForScope(ScopeAPIKey, keyID)`.

Response 201:
```json
{ "topup": { "id","key_id","amount_usd","reason","limit_before_usd",
             "limit_after_usd","created_at" },
  "budget": { "id","limit_micros","period","hard_cutoff" } }
```
Unique-violation on the idempotency index is treated as a replay (re-read and
return the existing row), so a race cannot double-credit.

### Handler — `GET /api/keys/{id}/topups`

Returns `{ "topups": [ … newest first … ] }`. 404 if the key is unknown.

### Enforcement

Unchanged. `budget.Engine.Check` still sums spend for the period and compares
to `limit_micros`. No new code path can allow spend without a stored budget.

## Frontend

### `frontend/src/lib/api.ts`
- `KeyTopup` type.
- `listKeyTopups(id)`, `topupKey(id, { amount_usd, reason, idempotency_key })`.
- Reuse `budgetStatus()` for the list progress bar.

### `frontend/src/pages/KeyDetail.tsx`
New fourth tab **Budget** (`Wallet` icon):
- Current budget summary: limit, spent, remaining, period, alert state, using
  `api.budgetStatus()` filtered to `scope_kind === "api_key" && scope_id === key.id`.
- **Top up** button → `Modal` with a numeric USD amount field, an optional
  reason field, and a live "new limit" preview. Client validates `> 0`.
  Generates a UUID `idempotency_key` per attempt (regenerated only when the
  modal is reopened) so a double-click cannot double-credit.
- History table: when, amount, reason, limit before → after.

### `frontend/src/pages/Keys.tsx`
Add a compact spend/credit progress bar to each key row in the existing keys
table:
- Fetch `api.budgetStatus()` once for the page; index by `scope_id`.
- Render `spent/limit` (USD) with a bar and pct; if no budget or zero limit,
  render "No budget" instead of an empty/zero bar. Alert tone when
  `pct_used >= alert_pct`.

## Verification plan

- Store test: increment + insert commit atomically; rollback on insert error
  leaves `limit_micros` unchanged.
- Store test: `GetByIdempotencyKey` round-trip; unique index blocks duplicate.
- Gateway test (real Postgres DSN `KEIROUTER_TEST_POSTGRES_DSN`):
  - top-up raises the existing budget and returns before/after;
  - same `idempotency_key` replayed → 200, no second credit;
  - non-positive / NaN / > ceiling amount → 400;
  - key with no budget → budget created with period `total` and credited;
  - `GET /topups` returns newest-first history.
- Money-accuracy test: repeated top-ups accumulate exactly
  (`n × amount == limit_delta`), no float drift.
- Manual: rebuild image + container, log into dashboard, top up a live key,
  confirm history rows and the list progress bar update; confirm a blocked
  request is allowed again after top-up.

## Risks / follow-ups

- `reason` is operator-supplied text; it is rendered in the dashboard. Ensure
  it is escaped by React (default) and never used in a shell/SQL context.
- If the operator tops up a `monthly`/`daily` budget, the added credit resets
  at the next period boundary. The UI must show the budget period next to the
  amount so this is not surprising. A dedicated `total` top-up ledger
  (non-resetting balance) is a possible future enhancement — out of scope now.
- Multi-replica deployments already have the pre-existing reservation-limit
  caveat; the DB-level atomic increment in this feature is correct across
  replicas regardless.
