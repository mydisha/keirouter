# API Key Budget Top-up Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an operator top up an API key's USD budget from the Key detail page, record an append-only top-up history with reason and before/after limits, and show a spend/credit progress bar in the API Keys list.

**Architecture:** A top-up is an exact integer micro-USD increment of the key's existing `api_key` budget row, performed in one DB transaction with an append-only `key_topups` history row. Enforcement is unchanged: `budget.Engine` still blocks when `spent >= limit_micros`. The dashboard gains two admin endpoints and a Budget tab; the public portal is untouched.

**Tech Stack:** Go (chi router, database/sql, pgx + modernc sqlite), React + TypeScript (Vite, TanStack Query), embedded SQL migrations, Docker Compose localhost deploy.

## Global Constraints

- All money is `int64` **micro-USD** (1 USD = 1,000,000 micros). Never accumulate money in `float64`.
- `amount_usd` must be `> 0`, finite, at most 6 decimal places, and ≤ 1,000,000 USD per request.
- A top-up must be idempotent: unique per `(key_id, idempotency_key)`; a replay returns the original record and credits nothing.
- The `limit_micros` increment and the `key_topups` insert happen in **one** transaction.
- `key_topups` is append-only: no UPDATE, no DELETE.
- Overflow guard: reject when `limit_before + amount_micros` would exceed `math.MaxInt64`.
- Go commands run from `/home/emalution/keirouter/backend` (module `github.com/mydisha/keirouter/backend`).
- Migration files: plain `0033_key_topups.sql` (no dialect suffix) — column types are TEXT/BIGINT, valid on both engines.
- Do not modify enforcement logic in `backend/internal/budget/budget.go`.
- Never log key material, secrets, or full request bodies.

---

### Task 1: `key_topups` table, model, and repository

**Files:**
- Create: `backend/internal/store/migrations/0033_key_topups.sql`
- Modify: `backend/internal/store/models.go` (add `KeyTopup` after `Budget`, ~line 224)
- Create: `backend/internal/store/repo_topups.go`
- Test: `backend/internal/store/repo_topups_test.go`

**Interfaces:**
- Consumes: `DB`, `sqlExec`, `formatTime`, `parseTime`, `ErrNotFound`, `DefaultTenantID` from `store`.
- Produces:
  - `type KeyTopup struct { ID, TenantID, KeyID, BudgetID string; AmountMicros, LimitBeforeMicros, LimitAfterMicros int64; Reason, IdempotencyKey, Actor string; CreatedAt time.Time }`
  - `func (db *DB) Topups() *KeyTopupRepo`
  - `func (r *KeyTopupRepo) Create(ctx, KeyTopup) error`
  - `func (r *KeyTopupRepo) CreateOnTx(ctx, *sql.Tx, KeyTopup) error`
  - `func (r *KeyTopupRepo) ListByKey(ctx, keyID string) ([]KeyTopup, error)` (newest first)
  - `func (r *KeyTopupRepo) GetByIdempotencyKey(ctx, keyID, idem string) (KeyTopup, error)`

- [ ] **Step 1: Write the migration**

Create `backend/internal/store/migrations/0033_key_topups.sql`:

```sql
-- Append-only ledger of API key budget top-ups. Each row records the exact
-- micro-USD amount credited, the operator reason, and the budget limit before
-- and after so the balance history is auditable.
CREATE TABLE IF NOT EXISTS key_topups (
  id                   TEXT PRIMARY KEY,
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

- [ ] **Step 2: Add the model**

In `backend/internal/store/models.go`, immediately after the `Budget` struct (ends ~line 224), add:

```go
// KeyTopup is one append-only budget top-up for an API key. Amount and limits
// are integer micro-USD (1 USD = 1,000,000 micros).
type KeyTopup struct {
	ID                string
	TenantID          string
	KeyID             string
	BudgetID          string
	AmountMicros      int64
	Reason            string
	LimitBeforeMicros int64
	LimitAfterMicros  int64
	IdempotencyKey    string
	Actor             string
	CreatedAt         time.Time
}
```

- [ ] **Step 3: Write the failing test**

Create `backend/internal/store/repo_topups_test.go`:

```go
package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKeyTopupRepo_CreateListIdempotency(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	rec := KeyTopup{
		ID:                "t1",
		TenantID:          DefaultTenantID,
		KeyID:             "key1",
		BudgetID:          "b1",
		AmountMicros:      25_500_000,
		Reason:            "invoice #1",
		LimitBeforeMicros: 10_000_000,
		LimitAfterMicros:  35_500_000,
		IdempotencyKey:    "idem-1",
		Actor:             "dashboard",
		CreatedAt:         time.Now(),
	}
	require.NoError(t, db.Topups().Create(ctx, rec))

	got, err := db.Topups().ListByKey(ctx, "key1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(25_500_000), got[0].AmountMicros)
	require.Equal(t, rec.LimitBeforeMicros, got[0].LimitBeforeMicros)
	require.Equal(t, rec.LimitAfterMicros, got[0].LimitAfterMicros)
	require.Equal(t, "invoice #1", got[0].Reason)

	dup, err := db.Topups().GetByIdempotencyKey(ctx, "key1", "idem-1")
	require.NoError(t, err)
	require.Equal(t, "t1", dup.ID)

	_, err = db.Topups().GetByIdempotencyKey(ctx, "key1", "missing")
	require.ErrorIs(t, err, ErrNotFound)

	// The unique index must reject a second row with the same idempotency key.
	rec2 := rec
	rec2.ID = "t2"
	require.Error(t, db.Topups().Create(ctx, rec2))

	// An empty idempotency key is exempt (multiple manual rows allowed).
	rec3 := rec
	rec3.ID = "t3"
	rec3.IdempotencyKey = ""
	require.NoError(t, db.Topups().Create(ctx, rec3))
}
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `go test ./internal/store/ -run TestKeyTopupRepo_CreateListIdempotency -v`
Expected: FAIL — `db.Topups undefined` / `KeyTopup` undefined.

- [ ] **Step 5: Implement the repository**

Create `backend/internal/store/repo_topups.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// KeyTopupRepo persists append-only API key budget top-ups.
type KeyTopupRepo struct{ db *DB }

// Topups returns the key top-up repository.
func (db *DB) Topups() *KeyTopupRepo { return &KeyTopupRepo{db: db} }

const keyTopupSelectCols = `id, tenant_id, key_id, budget_id, amount_micros, reason,
	limit_before_micros, limit_after_micros, idempotency_key, actor, created_at`

// Create inserts a top-up record.
func (r *KeyTopupRepo) Create(ctx context.Context, t KeyTopup) error {
	return r.insert(ctx, r.db.sql, t)
}

// CreateOnTx inserts a top-up record within an existing transaction.
func (r *KeyTopupRepo) CreateOnTx(ctx context.Context, tx *sql.Tx, t KeyTopup) error {
	return r.insert(ctx, tx, t)
}

func (r *KeyTopupRepo) insert(ctx context.Context, ex sqlExec, t KeyTopup) error {
	q := r.db.rebind(`INSERT INTO key_topups
		(id, tenant_id, key_id, budget_id, amount_micros, reason, limit_before_micros,
		 limit_after_micros, idempotency_key, actor, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := ex.ExecContext(ctx, q, t.ID, t.TenantID, t.KeyID, t.BudgetID, t.AmountMicros,
		t.Reason, t.LimitBeforeMicros, t.LimitAfterMicros, t.IdempotencyKey, t.Actor,
		formatTime(t.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: create key topup: %w", err)
	}
	return nil
}

// ListByKey returns a key's top-ups, newest first.
func (r *KeyTopupRepo) ListByKey(ctx context.Context, keyID string) ([]KeyTopup, error) {
	q := r.db.rebind(`SELECT ` + keyTopupSelectCols + ` FROM key_topups WHERE key_id = ? ORDER BY created_at DESC, id DESC`)
	rows, err := r.db.sql.QueryContext(ctx, q, keyID)
	if err != nil {
		return nil, fmt.Errorf("store: list key topups: %w", err)
	}
	defer rows.Close()

	var out []KeyTopup
	for rows.Next() {
		t, err := scanKeyTopup(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetByIdempotencyKey returns the top-up matching (keyID, idem), or ErrNotFound.
func (r *KeyTopupRepo) GetByIdempotencyKey(ctx context.Context, keyID, idem string) (KeyTopup, error) {
	q := r.db.rebind(`SELECT ` + keyTopupSelectCols + ` FROM key_topups WHERE key_id = ? AND idempotency_key = ?`)
	t, err := scanKeyTopup(r.db.sql.QueryRowContext(ctx, q, keyID, idem).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return KeyTopup{}, ErrNotFound
	}
	return t, err
}

func scanKeyTopup(scan func(dest ...any) error) (KeyTopup, error) {
	var (
		t       KeyTopup
		created string
	)
	err := scan(&t.ID, &t.TenantID, &t.KeyID, &t.BudgetID, &t.AmountMicros, &t.Reason,
		&t.LimitBeforeMicros, &t.LimitAfterMicros, &t.IdempotencyKey, &t.Actor, &created)
	if err != nil {
		return KeyTopup{}, err
	}
	t.CreatedAt = parseTime(created)
	return t, nil
}
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./internal/store/ -run TestKeyTopupRepo_CreateListIdempotency -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
cd /home/emalution/keirouter/backend
gofmt -w internal/store/repo_topups.go internal/store/repo_topups_test.go internal/store/models.go
git add internal/store/migrations/0033_key_topups.sql internal/store/models.go internal/store/repo_topups.go internal/store/repo_topups_test.go
git commit -m "feat(store): add append-only key_topups ledger"
```

---

### Task 2: Atomic budget limit increment

**Files:**
- Modify: `backend/internal/store/repo_budgets.go` (add method after `Update`, ~line 76)
- Test: `backend/internal/store/repo_budgets_test.go` (create if it does not exist; otherwise append)

**Interfaces:**
- Consumes: `BudgetRepo`, `sqlExec`, `ErrNotFound`, `Dialect`.
- Produces: `func (r *BudgetRepo) IncrementLimitOnTx(ctx context.Context, tx *sql.Tx, id string, deltaMicros int64) (beforeMicros, afterMicros int64, err error)` — reads the current limit under a row lock (Postgres `FOR UPDATE`), writes `before+delta`, returns both.

- [ ] **Step 1: Write the failing test**

Append to a new file `backend/internal/store/repo_budgets_test.go`:

```go
package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// incrementBudgetTx runs IncrementLimitOnTx in its own committed transaction.
func incrementBudgetTx(t *testing.T, db *DB, id string, delta int64) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.sql.BeginTx(ctx, nil)
	require.NoError(t, err)
	before, after, err := db.Budgets().IncrementLimitOnTx(ctx, tx, id, delta)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return before, after
}

func TestBudgetRepo_IncrementLimitOnTx(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	b := Budget{
		ID: "b1", TenantID: DefaultTenantID, ScopeKind: ScopeAPIKey, ScopeID: "key1",
		LimitMicros: 1_000_000, Period: "total", AlertPct: 80, HardCutoff: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, db.Budgets().Create(ctx, b))

	before, after := incrementBudgetTx(t, db, "b1", 2_500_000)
	require.Equal(t, int64(1_000_000), before)
	require.Equal(t, int64(3_500_000), after)

	got, err := db.Budgets().Get(ctx, "b1")
	require.NoError(t, err)
	require.Equal(t, int64(3_500_000), got.LimitMicros)

	// Unknown id -> ErrNotFound, no row written.
	tx, err := db.sql.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, _, err = db.Budgets().IncrementLimitOnTx(ctx, tx, "missing", 1)
	require.ErrorIs(t, err, ErrNotFound)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run TestBudgetRepo_IncrementLimitOnTx -v`
Expected: FAIL — `IncrementLimitOnTx` undefined.

- [ ] **Step 3: Implement the method**

In `backend/internal/store/repo_budgets.go`, add `"math"` to the import block, then after the `Update` method (~line 76) add:

```go
// IncrementLimitOnTx atomically adds deltaMicros to a budget's spend limit
// inside an existing transaction and returns the limit before and after.
//
// On Postgres the row is read with FOR UPDATE so concurrent top-ups serialize
// and the returned "before" snapshot cannot go stale. On SQLite the write lock
// already serializes the transaction. All arithmetic is int64 micro-USD.
//
// Overflow is checked against the value read under the lock and reported as
// ErrLimitOverflow before any write, so a wrap can never be persisted.
func (r *BudgetRepo) IncrementLimitOnTx(ctx context.Context, tx *sql.Tx, id string, deltaMicros int64) (int64, int64, error) {
	sel := r.db.rebind(`SELECT limit_micros FROM budgets WHERE id = ?`)
	if r.db.Dialect() == DialectPostgres {
		sel += " FOR UPDATE"
	}
	var before int64
	if err := tx.QueryRowContext(ctx, sel, id).Scan(&before); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, ErrNotFound
		}
		return 0, 0, fmt.Errorf("store: read budget limit: %w", err)
	}
	if deltaMicros > 0 && before > math.MaxInt64-deltaMicros {
		return before, before, ErrLimitOverflow
	}
	if deltaMicros < 0 && before < math.MinInt64-deltaMicros {
		return before, before, ErrLimitOverflow
	}
	after := before + deltaMicros
	up := r.db.rebind(`UPDATE budgets SET limit_micros = ?, updated_at = ? WHERE id = ?`)
	if _, err := tx.ExecContext(ctx, up, after, formatTime(time.Now()), id); err != nil {
		return 0, 0, fmt.Errorf("store: increment budget limit: %w", err)
	}
	return before, after, nil
}
```

Add `ErrLimitOverflow` next to `ErrNotFound` in `backend/internal/store/repo_apikeys.go:14`:

```go
// ErrLimitOverflow is returned when a limit increment would exceed int64.
var ErrLimitOverflow = errors.New("store: limit overflow")
```

Then add a per-key top-up serialization helper. Append to `backend/internal/store/repo_budgets.go` (same file, after `IncrementLimitOnTx`):

```go
// LockKeyTopup serializes concurrent top-ups for one API key. On Postgres it
// takes a transaction-scoped advisory lock keyed by the key id, so two requests
// cannot both create the key's budget or lose an increment. On SQLite, whose
// single writer already serializes transactions, it is a no-op.
func (r *BudgetRepo) LockKeyTopup(ctx context.Context, tx *sql.Tx, keyID string) error {
	if r.db.Dialect() != DialectPostgres {
		return nil
	}
	sum := sha256.Sum256([]byte("key_topup:" + keyID))
	lockID := int64(binary.BigEndian.Uint64(sum[:8]))
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", lockID); err != nil {
		return fmt.Errorf("store: lock key topup: %w", err)
	}
	return nil
}
```

The advisory xact lock is released automatically when the transaction commits or rolls back, so no explicit unlock is needed. Add `crypto/sha256` and `encoding/binary` to the file's imports.

Task 3 calls it as `s.budgets.LockKeyTopup(ctx, tx, key.ID)` (the handler already holds `s.budgets`).

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/store/ -run TestBudgetRepo_IncrementLimitOnTx -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/emalution/keirouter/backend
gofmt -w internal/store/repo_budgets.go internal/store/repo_budgets_test.go
git add internal/store/repo_budgets.go internal/store/repo_budgets_test.go
git commit -m "feat(store): atomic budget limit increment"
```

---

### Task 3: Top-up HTTP endpoints

**Files:**
- Create: `backend/internal/gateway/admin_topup.go`
- Modify: `backend/internal/gateway/admin.go` (routes near line 43)
- Test: `backend/internal/gateway/admin_topup_test.go`

**Interfaces:**
- Consumes: `Server`, `s.identity.Get`, `s.budgets`, `s.usage`, `s.budgetEngine`, `s.log`, `store.*`, `writeJSON`, `writeError`, `decodeJSON`, `adminTenant`, `uuid`.
- Produces:
  - `POST /api/keys/{id}/topup` → 201 `{"topup":{...},"budget":{...}}`, or 200 replay, or 400/404/409.
  - `GET /api/keys/{id}/topups` → 200 `{"topups":[...]}`.
  - `func usdToMicros(n json.Number) (int64, error)`

- [ ] **Step 1: Write the failing test**

Create `backend/internal/gateway/admin_topup_test.go`:

```go
package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/stretchr/testify/require"
)

// newTopupTestServer wires a Server with the store-backed repos the top-up
// handlers use, following the &Server{...} pattern in admin_bulk_test.go.
func newTopupTestServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })

	return &Server{
		db:       db,
		identity: identity.New(db.APIKeys()),
		budgets:  db.Budgets(),
		usage:    db.Usage(),
		log:      slog.Default(),
	}
}

func TestAdminTopupKey_RaisesLimitAndRecordsHistory(t *testing.T) {
	s := newTopupTestServer(t)
	ctx := context.Background()

	key, err := s.identity.Create(ctx, store.DefaultTenantID, "", "topup-key")
	require.NoError(t, err)
	require.NoError(t, s.budgets.Create(ctx, store.Budget{
		ID: "b1", TenantID: adminTenant, ScopeKind: store.ScopeAPIKey, ScopeID: key.Record.ID,
		LimitMicros: 1_000_000, Period: "total", AlertPct: 80, HardCutoff: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	r := httptest.NewRequest(http.MethodPost, "/keys/"+key.Record.ID+"/topup",
		strings.NewReader(`{"amount_usd":2.5,"reason":"invoice #1","idempotency_key":"idem-1"}`))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", key.Record.ID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	s.adminTopupKey(w, r)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var resp struct {
		Topup  map[string]any `json:"topup"`
		Budget map[string]any `json:"budget"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, 2.5, resp.Topup["amount_usd"])
	require.Equal(t, 1.0, resp.Topup["limit_before_usd"])
	require.Equal(t, 3.5, resp.Topup["limit_after_usd"])

	b, err := s.budgets.Get(ctx, "b1")
	require.NoError(t, err)
	require.Equal(t, int64(3_500_000), b.LimitMicros)

	// Replay with the same idempotency key must not credit again.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/keys/"+key.Record.ID+"/topup",
		strings.NewReader(`{"amount_usd":2.5,"idempotency_key":"idem-1"}`))
	r2 = r2.WithContext(context.WithValue(r2.Context(), chi.RouteCtxKey, rctx))
	s.adminTopupKey(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code, w2.Body.String())
	b2, err := s.budgets.Get(ctx, "b1")
	require.NoError(t, err)
	require.Equal(t, int64(3_500_000), b2.LimitMicros)
}

func TestAdminTopupKey_RejectsInvalidAmounts(t *testing.T) {
	s := newTopupTestServer(t)
	ctx := context.Background()
	issued, err := s.identity.Create(ctx, store.DefaultTenantID, "", "bad-amount")
	require.NoError(t, err)

	for _, body := range []string{
		`{"amount_usd":0}`,
		`{"amount_usd":-5}`,
		`{"amount_usd":1.0000001}`,
		`{"amount_usd":2000000}`,
	} {
		r := httptest.NewRequest(http.MethodPost, "/keys/"+issued.Record.ID+"/topup", strings.NewReader(body))
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", issued.Record.ID)
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()
		s.adminTopupKey(w, r)
		require.Equal(t, http.StatusBadRequest, w.Code, body)
	}
}

func TestAdminTopupKey_CreatesBudgetWhenMissing(t *testing.T) {
	s := newTopupTestServer(t)
	ctx := context.Background()
	issued, err := s.identity.Create(ctx, store.DefaultTenantID, "", "no-budget")
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodPost, "/keys/"+issued.Record.ID+"/topup",
		strings.NewReader(`{"amount_usd":7,"idempotency_key":"idem-x"}`))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", issued.Record.ID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	s.adminTopupKey(w, r)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	bs, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, issued.Record.ID)
	require.NoError(t, err)
	require.Len(t, bs, 1)
	require.Equal(t, int64(7_000_000), bs[0].LimitMicros)
	require.Equal(t, "total", bs[0].Period)
	require.True(t, bs[0].HardCutoff)
}

func TestUSDToMicros(t *testing.T) {
	cases := map[string]int64{"0.000001": 1, "1": 1_000_000, "2.5": 2_500_000, "1000000": 1_000_000_000_000}
	for in, want := range cases {
		got, err := usdToMicros(json.Number(in))
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	for _, bad := range []string{"0", "-1", "1.0000001", "abc", "2000000"} {
		_, err := usdToMicros(json.Number(bad))
		require.Error(t, err, bad)
	}
}
```

Note: `newTopupTestServer` follows the `&Server{...}` wiring pattern already used in `backend/internal/gateway/admin_bulk_test.go:38`. Keys are minted via `s.identity.Create(ctx, store.DefaultTenantID, "", name)`, which returns `identity.Issued` — use `issued.Record.ID` as the key id. No new server constructor is needed.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/gateway/ -run 'TestAdminTopup|TestUSDToMicros' -v`
Expected: FAIL — `s.adminTopupKey` / `usdToMicros` undefined.

- [ ] **Step 3: Implement the handlers**

Create `backend/internal/gateway/admin_topup.go`:

```go
package gateway

import (
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// maxTopupMicros caps a single top-up at 1,000,000 USD to bound operator error.
const maxTopupMicros int64 = 1_000_000 * 1_000_000

// usdToMicros converts a JSON decimal amount into integer micro-USD without
// ever going through float64, so no rounding drift can enter the ledger.
func usdToMicros(n json.Number) (int64, error) {
	r, ok := new(big.Rat).SetString(n.String())
	if !ok {
		return 0, errors.New("amount_usd is not a number")
	}
	if r.Sign() <= 0 {
		return 0, errors.New("amount_usd must be positive")
	}
	r.Mul(r, big.NewRat(1_000_000, 1))
	if !r.IsInt() {
		return 0, errors.New("amount_usd has more than 6 decimal places")
	}
	if !r.Num().IsInt64() {
		return 0, errors.New("amount_usd is too large")
	}
	micros := r.Num().Int64()
	if micros > maxTopupMicros {
		return 0, errors.New("amount_usd exceeds the per-request limit")
	}
	return micros, nil
}

type topupRequest struct {
	AmountUSD      json.Number `json:"amount_usd"`
	Reason         string      `json:"reason"`
	IdempotencyKey string      `json:"idempotency_key"`
}

func (s *Server) adminTopupKey(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "id")
	key, err := s.identity.Get(r.Context(), keyID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}

	var body topupRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	amountMicros, err := usdToMicros(body.AmountUSD)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	idem := strings.TrimSpace(body.IdempotencyKey)
	if idem == "" {
		idem = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	reason := strings.TrimSpace(body.Reason)
	if len(reason) > 500 {
		writeError(w, http.StatusBadRequest, "reason must be 500 characters or fewer")
		return
	}

	ctx := r.Context()

	// Fast replay path: if this idempotency key was already used, return it.
	if idem != "" {
		if existing, gerr := s.db.Topups().GetByIdempotencyKey(ctx, key.ID, idem); gerr == nil {
			s.writeTopupResponse(w, http.StatusOK, existing)
			return
		} else if !errors.Is(gerr, store.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, gerr, "internal server error"))
			return
		}
	}

	tx, err := s.db.SQL().BeginTx(ctx, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	defer func() { _ = tx.Rollback() }()

	// Serialize all top-ups for this key so concurrent requests cannot race on
	// budget creation or on the read-then-write limit snapshot. On Postgres
	// this takes a transaction-scoped advisory lock; on SQLite the write lock
	// already serializes.
	if err := s.budgets.LockKeyTopup(ctx, tx, key.ID); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}

	// Resolve the key's api_key budget, creating a total-period one if absent.
	var budget store.Budget
	{
		budgets, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, key.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
			return
		}
		if len(budgets) == 0 {
			now := time.Now()
			budget = store.Budget{
				ID: uuid.NewString(), TenantID: adminTenant, ScopeKind: store.ScopeAPIKey,
				ScopeID: key.ID, LimitMicros: 0, Period: "total", AlertPct: 80,
				HardCutoff: true, CreatedAt: now, UpdatedAt: now,
			}
			if err := s.budgets.CreateOnTx(ctx, tx, budget); err != nil {
				writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
				return
			}
		} else {
			budget = budgets[0]
		}
	}

	// Atomic increment; overflow is detected under the row lock before writing.
	before, after, err := s.budgets.IncrementLimitOnTx(ctx, tx, budget.ID, amountMicros)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusConflict, "budget changed concurrently; retry")
		case errors.Is(err, store.ErrLimitOverflow):
			writeError(w, http.StatusBadRequest, "top-up would overflow the budget limit")
		default:
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		}
		return
	}

	rec := store.KeyTopup{
		ID: uuid.NewString(), TenantID: adminTenant, KeyID: key.ID, BudgetID: budget.ID,
		AmountMicros: amountMicros, Reason: reason,
		LimitBeforeMicros: before, LimitAfterMicros: after,
		IdempotencyKey: idem, Actor: "dashboard", CreatedAt: time.Now(),
	}
	if err := s.db.Topups().CreateOnTx(ctx, tx, rec); err != nil {
		// A unique-index violation means a concurrent request already applied
		// this idempotency key. Return the winner instead of double-crediting.
		if idem != "" {
			if existing, gerr := s.db.Topups().GetByIdempotencyKey(ctx, key.ID, idem); gerr == nil {
				s.writeTopupResponse(w, http.StatusOK, existing)
				return
			}
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if s.budgetEngine != nil {
		s.budgetEngine.InvalidateBudgetCacheForScope(store.ScopeAPIKey, key.ID)
	}
	s.writeTopupResponse(w, http.StatusCreated, rec)
}

func (s *Server) writeTopupResponse(w http.ResponseWriter, status int, rec store.KeyTopup) {
	writeJSON(w, status, map[string]any{
		"topup": map[string]any{
			"id":               rec.ID,
			"key_id":           rec.KeyID,
			"amount_usd":       float64(rec.AmountMicros) / 1_000_000,
			"reason":           rec.Reason,
			"limit_before_usd": float64(rec.LimitBeforeMicros) / 1_000_000,
			"limit_after_usd":  float64(rec.LimitAfterMicros) / 1_000_000,
			"created_at":       rec.CreatedAt,
		},
	})
}

func (s *Server) adminListKeyTopups(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "id")
	if _, err := s.identity.Get(r.Context(), keyID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	topups, err := s.db.Topups().ListByKey(r.Context(), keyID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(topups))
	for _, t := range topups {
		out = append(out, map[string]any{
			"id": t.ID, "amount_usd": float64(t.AmountMicros) / 1_000_000,
			"reason": t.Reason, "limit_before_usd": float64(t.LimitBeforeMicros) / 1_000_000,
			"limit_after_usd": float64(t.LimitAfterMicros) / 1_000_000, "created_at": t.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"topups": out})
}
```

The `Server` struct already has a `db *store.DB` field (`backend/internal/gateway/server.go:49`) and `store.DB` already exposes `SQL() *sql.DB` (`backend/internal/store/store.go:175`) and `Topups()` (Task 1). Use those directly — `s.db`, `s.db.SQL()`, `s.db.Topups()`, `s.db.BeginTx(...)`. Do **not** add new accessor methods or a second store handle.

- [ ] **Step 4: Register the routes**

In `backend/internal/gateway/admin.go`, after the `/keys/{id}` DELETE route (~line 43), add:

```go
	r.Post("/keys/{id}/topup", s.adminTopupKey)
	r.Get("/keys/{id}/topups", s.adminListKeyTopups)
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/gateway/ -run 'TestAdminTopup|TestUSDToMicros' -v`
Expected: PASS.

- [ ] **Step 6: Run the wider backend suites**

Run: `go build ./... && go test ./internal/store/ ./internal/gateway/`
Expected: PASS (no regressions).

- [ ] **Step 7: Commit**

```bash
cd /home/emalution/keirouter/backend
gofmt -w internal/gateway/admin_topup.go internal/gateway/admin_topup_test.go internal/gateway/admin.go
git add internal/gateway/admin_topup.go internal/gateway/admin_topup_test.go internal/gateway/admin.go internal/gateway/server.go internal/store/store.go
git commit -m "feat(gateway): API key budget top-up endpoints"
```

---

### Task 4: Frontend API client

**Files:**
- Modify: `frontend/src/lib/api.ts`

**Interfaces:**
- Produces:
  - `interface KeyTopup { id: string; amount_usd: number; reason: string; limit_before_usd: number; limit_after_usd: number; created_at: string }`
  - `api.listKeyTopups(keyId: string): Promise<{ topups: KeyTopup[] }>`
  - `api.topupKey(keyId: string, input: { amount_usd: number; reason?: string; idempotency_key: string }): Promise<{ topup: KeyTopup }>`

- [ ] **Step 1: Add the type and methods**

In `frontend/src/lib/api.ts`, near the `Budget` interface (~line 334), add:

```ts
export interface KeyTopup {
  id: string;
  amount_usd: number;
  reason: string;
  limit_before_usd: number;
  limit_after_usd: number;
  created_at: string;
}
```

In the `api` object, next to `createBudget`/`updateBudget` (~line 1354), add:

```ts
  listKeyTopups: (keyId: string) =>
    request<{ topups: KeyTopup[] }>("GET", `/keys/${keyId}/topups`),
  topupKey: (keyId: string, input: { amount_usd: number; reason?: string; idempotency_key: string }) =>
    request<{ topup: KeyTopup }>("POST", `/keys/${keyId}/topup`, input),
```

Confirm the exact `request` helper signature used by neighboring calls before editing.

- [ ] **Step 2: Typecheck**

Run: `cd /home/emalution/keirouter/frontend && npx tsc --noEmit`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
cd /home/emalution/keirouter
git add frontend/src/lib/api.ts
git commit -m "feat(ui): key top-up api client"
```

---

### Task 5: Budget tab on Key detail

**Files:**
- Modify: `frontend/src/pages/KeyDetail.tsx`

**Interfaces:**
- Consumes: `api.listKeyTopups`, `api.topupKey`, `api.budgetStatus`, `Modal`, `Input`, `Field`, `Button`, `Card`, `CardHeader`, `Badge`, `Spinner`, `EmptyState`.
- Produces: a `BudgetTab` component rendered by a new `"budget"` tab value.

- [ ] **Step 1: Add the tab**

In `frontend/src/pages/KeyDetail.tsx`:

1. Import `Wallet` from `lucide-react` and `KeyTopup` from `../lib/api`.
2. Extend `type Tab` to `"general" | "models" | "budget" | "guardrails"`.
3. Add to `TABS`: `{ value: "budget" as const, label: "Budget", icon: Wallet }` (place before guardrails).
4. Render it: `{tab === "budget" && <BudgetTab apiKey={key} />}`.

- [ ] **Step 2: Implement `BudgetTab`**

Append to `frontend/src/pages/KeyDetail.tsx`:

```tsx
function BudgetTab({ apiKey }: { apiKey: APIKey }) {
  const qc = useQueryClient();
  const toast = useToast();
  const status = useQuery({ queryKey: ["budget-status"], queryFn: () => api.budgetStatus() });
  const topups = useQuery({ queryKey: ["key-topups", apiKey.id], queryFn: () => api.listKeyTopups(apiKey.id) });

  const budget = useMemo(
    () => status.data?.budgets.find((b) => b.scope_kind === "api_key" && b.scope_id === apiKey.id),
    [status.data, apiKey.id],
  );

  const [open, setOpen] = useState(false);
  const [amount, setAmount] = useState("");
  const [reason, setReason] = useState("");
  const [idem, setIdem] = useState("");
  const [error, setError] = useState<string | null>(null);

  const openModal = () => {
    setAmount("");
    setReason("");
    setError(null);
    setIdem(crypto.randomUUID());
    setOpen(true);
  };

  const parsed = Number(amount);
  const valid = Number.isFinite(parsed) && parsed > 0 && Math.round(parsed * 1e6) === parsed * 1e6;

  const submit = useMutation({
    mutationFn: () => api.topupKey(apiKey.id, { amount_usd: parsed, reason: reason.trim() || undefined, idempotency_key: idem }),
    onSuccess: async (data) => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: ["budget-status"] }),
        qc.invalidateQueries({ queryKey: ["key-topups", apiKey.id] }),
      ]);
      setOpen(false);
      toast.success("Budget topped up", `New limit: $${data.topup.limit_after_usd.toFixed(2)}.`);
    },
    onError: (e) => { setError(e instanceof Error ? e.message : "Please try again."); },
  });

  const limit = budget ? budget.limit_micros / 1_000_000 : 0;
  const spent = budget ? budget.spent_micros / 1_000_000 : 0;
  const remaining = Math.max(limit - spent, 0);
  const pct = limit > 0 ? Math.min((spent / limit) * 100, 100) : 0;

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader
          title="Budget"
          description="Spend limit for this key. Top-ups increase the limit and are recorded below."
          action={<Button onClick={openModal}><Wallet className="h-4 w-4" />Top up</Button>}
        />
        <div className="space-y-4 p-4 sm:p-5">
          {status.isLoading ? (
            <Spinner />
          ) : !budget ? (
            <p className="text-sm text-[var(--text-muted)]">This key has no budget yet. Top up to create one.</p>
          ) : (
            <>
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <Badge tone={pct >= (budget.alert_pct || 100) ? "danger" : "neutral"}>{budget.period} limit</Badge>
                <span className="text-[var(--text-muted)]">Alert at {budget.alert_pct}%</span>
              </div>
              <div>
                <div className="mb-1 flex items-baseline justify-between text-sm">
                  <span className="font-medium text-[var(--text)]">${spent.toFixed(2)} spent</span>
                  <span className="text-[var(--text-muted)]">of ${limit.toFixed(2)} · ${remaining.toFixed(2)} left</span>
                </div>
                <div className="h-2.5 overflow-hidden rounded-full bg-[var(--bg-subtle)]">
                  <div className={`h-full rounded-full ${pct >= 100 ? "bg-red-500" : pct >= (budget.alert_pct || 100) ? "bg-amber-500" : "bg-emerald-500"}`} style={{ width: `${pct}%` }} />
                </div>
              </div>
            </>
          )}
        </div>
      </Card>

      <Card>
        <CardHeader title="Top-up history" description="Every top-up is append-only and ordered newest first." />
        <div className="p-4 sm:p-5">
          {topups.isLoading ? <Spinner /> : !topups.data?.topups?.length ? (
            <EmptyState title="No top-ups yet" hint="Top-ups for this key will appear here with their reason and before/after limit." />
          ) : (
            <div className="divide-y divide-[var(--border)]">
              {topups.data.topups.map((t) => (
                <div key={t.id} className="flex flex-wrap items-center justify-between gap-2 py-3 text-sm">
                  <div className="min-w-0">
                    <p className="font-medium text-[var(--text)]">+${t.amount_usd.toFixed(2)}</p>
                    <p className="truncate text-xs text-[var(--text-muted)]">{t.reason || "No reason provided"}</p>
                  </div>
                  <div className="text-right text-xs text-[var(--text-muted)]">
                    <p className="tabular-nums">${t.limit_before_usd.toFixed(2)} → ${t.limit_after_usd.toFixed(2)}</p>
                    <p>{new Date(t.created_at).toLocaleString()}</p>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </Card>

      <Modal open={open} onClose={() => setOpen(false)} title={`Top up ${apiKey.name}`}>
        <div className="space-y-4">
          <Field label="Amount (USD)">
            <Input type="number" min="0" step="0.01" inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} placeholder="25.00" autoFocus />
          </Field>
          <Field label="Reason (optional)">
            <Input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="manual invoice #123" maxLength={500} />
          </Field>
          {budget && valid && <p className="text-xs text-[var(--text-muted)]">New limit: ${(limit + parsed).toFixed(2)}</p>}
          {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={() => setOpen(false)} disabled={submit.isPending}>Cancel</Button>
            <Button onClick={() => submit.mutate()} disabled={!valid || submit.isPending}>{submit.isPending ? "Topping up…" : "Top up"}</Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}
```

Confirm the `Modal` prop names (`open`, `onClose`, `title`) by reading `frontend/src/components/ui.tsx:350` and adjust if the component uses different names.

- [ ] **Step 3: Typecheck and lint**

Run: `cd /home/emalution/keirouter/frontend && npx tsc --noEmit`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
cd /home/emalution/keirouter
git add frontend/src/pages/KeyDetail.tsx
git commit -m "feat(ui): budget tab with top-up on key detail"
```

---

### Task 6: Spend progress bar in the API Keys list

**Files:**
- Modify: `frontend/src/pages/Keys.tsx`

**Interfaces:**
- Consumes: `api.budgetStatus`.
- Produces: `KeyRow` receives a `budget?: BudgetStatus` prop and renders a USD progress bar.

- [ ] **Step 1: Fetch budget status once on the page**

In `KeysPage`, add:

```tsx
const budgetStatus = useQuery({ queryKey: ["budget-status"], queryFn: () => api.budgetStatus() });
const budgetByKey = useMemo(() => {
  const m = new Map<string, BudgetStatus>();
  for (const b of budgetStatus.data?.budgets ?? []) {
    if (b.scope_kind === "api_key") m.set(b.scope_id, b);
  }
  return m;
}, [budgetStatus.data]);
```

Import `BudgetStatus` from `../lib/api`. Pass `budget={budgetByKey.get(k.id)}` into each `<KeyRow>` (~line 630).

- [ ] **Step 2: Render the bar in `KeyRow`**

Add the prop to `KeyRow`'s signature and destructuring (`budget?: BudgetStatus`). Inside the plan/models cell (~line 207), after the model count line, add:

```tsx
{budget && budget.limit_micros > 0 && (() => {
  const limit = budget.limit_micros / 1_000_000;
  const spent = budget.spent_micros / 1_000_000;
  const pct = Math.min((spent / limit) * 100, 100);
  const tone = pct >= 100 ? "bg-red-500" : pct >= (budget.alert_pct || 100) ? "bg-amber-500" : "bg-emerald-500";
  return (
    <div className="mt-2 min-w-[160px]" title={`$${spent.toFixed(2)} of $${limit.toFixed(2)}`}>
      <div className="mb-1 flex justify-between text-[11px] text-[var(--text-muted)]">
        <span className="tabular-nums">${spent.toFixed(2)} spent</span>
        <span className="tabular-nums">${limit.toFixed(2)}</span>
      </div>
      <div className="h-1.5 overflow-hidden rounded-full bg-[var(--bg-subtle)]">
        <div className={`h-full rounded-full ${tone}`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
})()}
```

- [ ] **Step 3: Typecheck**

Run: `cd /home/emalution/keirouter/frontend && npx tsc --noEmit`
Expected: PASS.

- [ ] **Step 4: Build the frontend**

Run: `cd /home/emalution/keirouter/frontend && npm run build`
Expected: build succeeds (no type errors).

- [ ] **Step 5: Commit**

```bash
cd /home/emalution/keirouter
git add frontend/src/pages/Keys.tsx
git commit -m "feat(ui): show key budget spend bar in keys list"
```

---

### Task 7: Concurrency and race-condition verification

Money must never be double-credited under concurrent access. SQLite serializes
writers, so it cannot exercise the race — these tests run against **Postgres**
(`KEIROUTER_TEST_POSTGRES_DSN`) with `-race` and multiple iterations.

**Files:**
- Create: `backend/internal/gateway/admin_topup_race_test.go`

**Interfaces:**
- Consumes: `newTopupTestServer` wiring shape (Task 3), `s.adminTopupKey`, `store.*`.
- Produces: proof that (a) distinct top-ups all apply exactly once, (b) the same
  idempotency key applies exactly once, (c) a budget-less key still ends with
  exactly one budget row under concurrency.

- [ ] **Step 1: Write the concurrent tests**

Create `backend/internal/gateway/admin_topup_race_test.go`:

```go
package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/stretchr/testify/require"
)

// newTopupPostgresServer builds a Server backed by the real Postgres test DSN.
// SQLite cannot exercise concurrent writers, so the race tests require this.
func newTopupPostgresServer(t *testing.T) *Server {
	t.Helper()
	dsn := os.Getenv("KEIROUTER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("KEIROUTER_TEST_POSTGRES_DSN not set; skipping concurrency test")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "postgres", DSN: dsn}, "")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })
	return &Server{db: db, identity: identity.New(db.APIKeys()), budgets: db.Budgets(), usage: db.Usage(), log: slog.Default()}
}

// callTopup invokes the handler once in its own request context.
func callTopup(s *Server, keyID, body string) int {
	r := httptest.NewRequest(http.MethodPost, "/keys/"+keyID+"/topup", strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", keyID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	s.adminTopupKey(w, r)
	return w.Code
}

func TestTopupRace_DistinctKeysAllApplyExactlyOnce(t *testing.T) {
	s := newTopupPostgresServer(t)
	ctx := context.Background()
	issued, err := s.identity.Create(ctx, store.DefaultTenantID, "", "race-distinct")
	require.NoError(t, err)
	keyID := issued.Record.ID
	budgetID := "b-race-1-" + keyID
	require.NoError(t, s.budgets.Create(ctx, store.Budget{
		ID: budgetID, TenantID: adminTenant, ScopeKind: store.ScopeAPIKey, ScopeID: keyID,
		LimitMicros: 0, Period: "total", AlertPct: 80, HardCutoff: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	const n = 25
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = callTopup(s, keyID, fmt.Sprintf(`{"amount_usd":1,"idempotency_key":"race-%d"}`, i))
		}(i)
	}
	wg.Wait()

	for i, code := range codes {
		require.Equal(t, http.StatusCreated, code, "top-up %d", i)
	}

	b, err := s.budgets.Get(ctx, budgetID)
	require.NoError(t, err)
	require.Equal(t, int64(n)*1_000_000, b.LimitMicros, "limit must equal n * amount")

	topups, err := s.db.Topups().ListByKey(ctx, keyID)
	require.NoError(t, err)
	require.Len(t, topups, n)

	// snapshots must form a gapless chain: sorted by before, each after == next before
	byBefore := make(map[int64]int64, n)
	for _, tp := range topups {
		require.Equal(t, tp.LimitBeforeMicros+tp.AmountMicros, tp.LimitAfterMicros)
		byBefore[tp.LimitBeforeMicros] = tp.LimitAfterMicros
	}
	prev := int64(0)
	for i := 0; i < n; i++ {
		next, ok := byBefore[prev]
		require.True(t, ok, "missing snapshot starting at %d", prev)
		prev = next
	}
	require.Equal(t, int64(n)*1_000_000, prev)
}

func TestTopupRace_SameIdempotencyKeyCreditsOnce(t *testing.T) {
	s := newTopupPostgresServer(t)
	ctx := context.Background()
	issued, err := s.identity.Create(ctx, store.DefaultTenantID, "", "race-same-idem")
	require.NoError(t, err)
	keyID := issued.Record.ID
	budgetID := "b-race-2-" + keyID
	require.NoError(t, s.budgets.Create(ctx, store.Budget{
		ID: budgetID, TenantID: adminTenant, ScopeKind: store.ScopeAPIKey, ScopeID: keyID,
		LimitMicros: 0, Period: "total", AlertPct: 80, HardCutoff: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	const n = 25
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = callTopup(s, keyID, `{"amount_usd":3,"idempotency_key":"same-idem"}`)
		}(i)
	}
	wg.Wait()

	b, err := s.budgets.Get(ctx, budgetID)
	require.NoError(t, err)
	require.Equal(t, int64(3_000_000), b.LimitMicros, "limit must be credited exactly once")

	topups, err := s.db.Topups().ListByKey(ctx, keyID)
	require.NoError(t, err)
	require.Len(t, topups, 1, "only one top-up row may exist")

	// exactly one request creates (201); the rest are replays or conflict-free 200
	created := 0
	for _, c := range codes {
		require.Contains(t, []int{http.StatusCreated, http.StatusOK}, c)
		if c == http.StatusCreated {
			created++
		}
	}
	require.Equal(t, 1, created)
}

func TestTopupRace_NoBudgetCreatesExactlyOne(t *testing.T) {
	s := newTopupPostgresServer(t)
	ctx := context.Background()
	issued, err := s.identity.Create(ctx, store.DefaultTenantID, "", "race-no-budget")
	require.NoError(t, err)
	keyID := issued.Record.ID

	const n = 25
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			callTopup(s, keyID, fmt.Sprintf(`{"amount_usd":1,"idempotency_key":"nb-%d"}`, i))
		}(i)
	}
	wg.Wait()

	bs, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, keyID)
	require.NoError(t, err)
	require.Len(t, bs, 1, "concurrent top-ups must not create duplicate budgets")
	require.Equal(t, int64(n)*1_000_000, bs[0].LimitMicros)

	var count int
	require.NoError(t, s.db.SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM key_topups WHERE key_id = $1", keyID).Scan(&count))
	require.Equal(t, n, count)
}
```

If the top-up handler returns 409 under contention instead of 200 for a lost
increment, that is acceptable for distinct keys (retryable) but must never
occur for the same idempotency key with a 5xx. Adjust assertions to match
actual, non-5xx behavior and record the observed codes.

- [ ] **Step 2: Run the race tests against Postgres with -race**

```bash
cd /home/emalution/keirouter/backend
KEIROUTER_TEST_POSTGRES_DSN="postgres://keirouter:<REDACTED>@192.168.32.3:5432/keirouter?sslmode=disable" \
  go test ./internal/gateway/ -run 'TestTopupRace' -race -count=5 -v
```

Expected: PASS on all 5 iterations with no data race reported.

- [ ] **Step 3: Verify no double-credit in the database**

```bash
docker exec keirouter-localhost-postgres-1 psql -U keirouter -d keirouter \
  -c "SELECT key_id, COUNT(*) AS rows, SUM(amount_micros) AS credited FROM key_topups GROUP BY key_id HAVING COUNT(*) > 1;"
```

Expected: every key that used a distinct idempotency key per top-up has the
expected row count. The `same-idem` key must show exactly 1 row. Confirm no key
has more top-up rows than requests issued.

- [ ] **Step 4: Commit**

```bash
cd /home/emalution/keirouter
git add backend/internal/gateway/admin_topup_race_test.go
git commit -m "test(gateway): race-condition coverage for concurrent top-ups"
```

**If any race test fails:** do not weaken the assertion. Fix the root cause in
the handler/repo (serialization lock, atomic increment, or idempotency
handling) and re-run Step 2 until clean.

---

### Task 8: Postgres integration test, rebuild, and live verification

**Files:**
- Modify: `backend/internal/store/postgres_integration_test.go` (add a subtest) — only if a top-up-related SQL path is Postgres-sensitive; otherwise test Task 1/2 against Postgres via the existing harness.

**Interfaces:**
- Consumes: env `KEIROUTER_TEST_POSTGRES_DSN`.
- Produces: verified end-to-end behavior on the local Postgres container.

- [ ] **Step 1: Run the store and gateway suites against Postgres**

Run (DSN from the running container; value below is the local dev password):

```bash
cd /home/emalution/keirouter/backend
KEIROUTER_TEST_POSTGRES_DSN="postgres://keirouter:<REDACTED>@192.168.32.3:5432/keirouter?sslmode=disable" \
  go test ./internal/store/ -run 'TestKeyTopup|TestBudgetRepo|TestPostgresCompatibility' -v
```

Expected: PASS. If the store integration harness does not yet exercise `key_topups`, add a subtest that creates a budget, calls `IncrementLimitOnTx`, inserts a `KeyTopup`, and asserts both persisted (mirroring the existing `postgres_integration_test.go` style).

- [ ] **Step 2: Rebuild and recreate the container**

```bash
cd /home/emalution/keirouter
docker compose -f compose.localhost.yaml up -d --build
```

Expected: image rebuilt, `keirouter-localhost-keirouter-1` recreated and started.

- [ ] **Step 3: Verify health and login**

```bash
sleep 5
curl -s -o /dev/null -w "healthz %{http_code}\n" http://127.0.0.1:20180/healthz
```

Expected: `healthz 200`.

- [ ] **Step 4: Live top-up via the admin API**

Replace `<KEY_ID>` with a real key id from `SELECT id, name FROM api_keys;` in the Postgres container. Log in first to obtain the session cookie:

```bash
COOKIE=$(curl -s -i -X POST http://127.0.0.1:20180/api/auth/login \
  -H 'Content-Type: application/json' -d '{"password":"keirouter"}' | grep -i '^set-cookie' | sed 's/.*: //' | tr -d '\r')
curl -s -b "$COOKIE" -X POST http://127.0.0.1:20180/api/keys/<KEY_ID>/topup \
  -H 'Content-Type: application/json' \
  -d '{"amount_usd":5,"reason":"live verification","idempotency_key":"verify-1"}'
```

Expected: 201 with `amount_usd:5` and `limit_after_usd` = previous limit + 5. Re-running the identical command returns 200 with the same record and does not change the limit. `GET /api/keys/<KEY_ID>/topups` lists the row.

- [ ] **Step 5: Confirm enforcement picks up the new limit**

Read the budget row before/after in Postgres and confirm `limit_micros` increased by exactly `5_000_000`:

```bash
docker exec keirouter-localhost-postgres-1 psql -U keirouter -d keirouter \
  -c "SELECT limit_micros, updated_at FROM budgets WHERE scope_id = '<KEY_ID>';"
docker exec keirouter-localhost-postgres-1 psql -U keirouter -d keirouter \
  -c "SELECT amount_micros, limit_before_micros, limit_after_micros, reason FROM key_topups WHERE key_id = '<KEY_ID>' ORDER BY created_at DESC LIMIT 3;"
```

Expected: the budget limit equals the top-up's `limit_after_micros`.

- [ ] **Step 6: Commit any test additions**

```bash
cd /home/emalution/keirouter
git add backend/internal/store/postgres_integration_test.go
git commit -m "test(store): cover key top-up on Postgres"
```

---

## Self-Review Notes

- Spec coverage: table+repo (Task 1), atomic increment (Task 2), endpoints with idempotency/validation/overflow (Task 3), client (Task 4), Budget tab with history (Task 5), list progress bar (Task 6), concurrency/race verification (Task 7), Postgres + live verification (Task 8).
- Money safety: integer-only conversion (`usdToMicros` via `big.Rat`), single transaction, per-key advisory serialization (`LockKeyTopup`) so budget creation cannot duplicate, `FOR UPDATE` snapshot, unique idempotency index, overflow guard under the lock (`ErrLimitOverflow`) — all present.
- Race coverage (Task 7, Postgres + `-race`): N distinct top-ups apply exactly once with a gapless before/after chain; N requests sharing one idempotency key credit exactly once and create exactly one row; N concurrent top-ups on a budget-less key create exactly one budget row.
- Wiring facts confirmed against the repo: `Server.db` (`server.go:49`), `store.DB.SQL()` (`store.go:175`), `identity.New(db.APIKeys())` (`identity.go:72`), `s.identity.Create(...) (Issued, error)` (`identity.go:109`), `Modal{open,onClose,title}` (`ui.tsx:350`), test wiring via `&Server{...}` (`admin_bulk_test.go:38`).
- Type consistency: `KeyTopup` field names (`LimitBeforeMicros`/`LimitAfterMicros`) are used identically in Tasks 1 and 3; `IncrementLimitOnTx` returns `(before, after, err)` in Tasks 2 and 3.
