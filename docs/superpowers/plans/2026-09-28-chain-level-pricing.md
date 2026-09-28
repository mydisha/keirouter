# Chain-Level Model Pricing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move model pricing from each `chain_steps` row up to the `chains` row, so a chain (the model the user calls) has one price set that applies no matter which route step serves the request.

**Architecture:** The four rate fields move from `store.ChainStep` to `store.Chain`. `dispatch.TargetsFromChain` copies the chain's rates onto every emitted `Target`; everything downstream (`Target → Attempt → meter.Event → chainPrice → cost`) is already correct and unchanged. A migration adds the columns to `chains` and drops them from `chain_steps`. The admin API and dashboard move the pricing control to the chain level and stop accepting per-step rates.

**Tech Stack:** Go 1.26 (chi, database/sql, modernc.org/sqlite + lib/pq), React 19 + Vite + Tailwind v4 + TanStack Query, node:test.

## Global Constraints

- Go commands run from `/home/emalution/keirouter/backend` (repo root fails under `go.work`).
- Money math is nanodollars: `tokenCostNanos = tokens * ratePerM * 1000`; no float accumulation across classes.
- Rate columns: `REAL` for SQLite, `DOUBLE PRECISION` for Postgres; migrations come in dialect pairs using the `.sqlite.sql` / `.postgres.sql` suffix scheme (`migrationVersion` collapses to `0032_chain_pricing`).
- All-zero rates on a chain mean "unpriced" → catalog fallback (`PricingSource == "catalog"`). A partially-set price (any rate > 0) requires `input_per_m > 0` AND `output_per_m > 0`, else HTTP 400. Cache write/read may be 0 even when set.
- Rates are finite and non-negative (`validRate`).
- Cache derivation (UI only): write = 1.25 × input, read = 0.1 × input, auto-derived, manually overridable.
- `decodeJSON` uses `DisallowUnknownFields`: step payloads must no longer carry rate fields (breaking change, accepted).

---

### Task 1: Move rate fields to `store.Chain` and migrate the schema

**Files:**
- Create: `backend/internal/store/migrations/0032_chain_pricing.sqlite.sql`
- Create: `backend/internal/store/migrations/0032_chain_pricing.postgres.sql`
- Modify: `backend/internal/store/models.go:105-131`
- Modify: `backend/internal/store/repo_budgets.go:130-282`
- Test: `backend/internal/store/repo_chain_pricing_test.go`

**Interfaces:**
- Produces: `store.Chain.InputPerM/OutputPerM/CacheWritePerM/CacheReadPerM float64`; `store.ChainStep` no longer has rate fields; `ChainRepo.Create/Update/Get/ListByTenant` round-trip the chain rates.

- [ ] **Step 1: Write the failing test**

Replace the body of `backend/internal/store/repo_chain_pricing_test.go` with:

```go
package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChainPricingRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	now := time.Now().UTC()
	c := Chain{
		ID: "c-price", TenantID: DefaultTenantID, Name: "priced", Strategy: "priority",
		InputPerM: 2.5, OutputPerM: 10, CacheWritePerM: 3.125, CacheReadPerM: 0.25,
		CreatedAt: now, UpdatedAt: now,
		Steps: []ChainStep{{
			ID: "s1", ChainID: "c-price", Position: 0, Provider: "openai", Model: "gpt-4o", CreatedAt: now,
		}},
	}
	require.NoError(t, db.Chains().Create(ctx, c))

	got, err := db.Chains().Get(ctx, "c-price")
	require.NoError(t, err)
	require.Equal(t, 2.5, got.InputPerM)
	require.Equal(t, 10.0, got.OutputPerM)
	require.Equal(t, 3.125, got.CacheWritePerM)
	require.Equal(t, 0.25, got.CacheReadPerM)

	// Update path must keep chain rates.
	c.CacheReadPerM = 0.5
	require.NoError(t, db.Chains().Update(ctx, c))
	again, err := db.Chains().Get(ctx, "c-price")
	require.NoError(t, err)
	require.Equal(t, 0.5, again.CacheReadPerM)

	list, err := db.Chains().ListByTenant(ctx, DefaultTenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, 3.125, list[0].CacheWritePerM)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestChainPricingRoundTrip -count=1`
Expected: FAIL — `unknown field InputPerM in struct literal of type Chain` (compile error).

- [ ] **Step 3: Add the migration files**

`backend/internal/store/migrations/0032_chain_pricing.sqlite.sql`:

```sql
ALTER TABLE chains ADD COLUMN input_per_m REAL NOT NULL DEFAULT 0;
ALTER TABLE chains ADD COLUMN output_per_m REAL NOT NULL DEFAULT 0;
ALTER TABLE chains ADD COLUMN cache_write_per_m REAL NOT NULL DEFAULT 0;
ALTER TABLE chains ADD COLUMN cache_read_per_m REAL NOT NULL DEFAULT 0;
ALTER TABLE chain_steps DROP COLUMN input_per_m;
ALTER TABLE chain_steps DROP COLUMN output_per_m;
ALTER TABLE chain_steps DROP COLUMN cache_write_per_m;
ALTER TABLE chain_steps DROP COLUMN cache_read_per_m;
```

`backend/internal/store/migrations/0032_chain_pricing.postgres.sql`:

```sql
ALTER TABLE chains ADD COLUMN input_per_m DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE chains ADD COLUMN output_per_m DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE chains ADD COLUMN cache_write_per_m DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE chains ADD COLUMN cache_read_per_m DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE chain_steps DROP COLUMN input_per_m;
ALTER TABLE chain_steps DROP COLUMN output_per_m;
ALTER TABLE chain_steps DROP COLUMN cache_write_per_m;
ALTER TABLE chain_steps DROP COLUMN cache_read_per_m;
```

- [ ] **Step 4: Move the fields in `models.go`**

In `Chain`, add after `FallbackModel string` (before `Steps`):

```go
	// Operator price for the chain model. All zero = fall back to catalog.
	InputPerM      float64
	OutputPerM     float64
	CacheWritePerM float64
	CacheReadPerM  float64
```

Delete the four `InputPerM/OutputPerM/CacheWritePerM/CacheReadPerM` fields and the preceding comment from `ChainStep` (leaving `ID, ChainID, Position, Provider, Model, CreatedAt`).

- [ ] **Step 5: Update `repo_budgets.go` queries**

`Create` — chain INSERT becomes:

```go
	cq := r.db.rebind(`INSERT INTO chains (id, tenant_id, name, strategy, fallback_provider, fallback_model,
		input_per_m, output_per_m, cache_write_per_m, cache_read_per_m, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if _, err := tx.ExecContext(ctx, cq, c.ID, c.TenantID, c.Name, c.Strategy, c.FallbackProvider, c.FallbackModel,
		c.InputPerM, c.OutputPerM, c.CacheWritePerM, c.CacheReadPerM, formatTime(c.CreatedAt), formatTime(c.UpdatedAt)); err != nil {
		return fmt.Errorf("store: create chain: %w", err)
	}
```

and the step INSERT drops the rate columns:

```go
	sq := r.db.rebind(`INSERT INTO chain_steps (id, chain_id, position, provider, model, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`)
	for _, s := range c.Steps {
		if _, err := tx.ExecContext(ctx, sq, s.ID, c.ID, s.Position, s.Provider, s.Model,
			formatTime(s.CreatedAt)); err != nil {
			return fmt.Errorf("store: create chain step: %w", err)
		}
	}
```

`Get` chain SELECT becomes `SELECT id, tenant_id, name, strategy, fallback_provider, fallback_model, input_per_m, output_per_m, cache_write_per_m, cache_read_per_m, created_at, updated_at FROM chains WHERE id = ?` and its `Scan` appends `&c.InputPerM, &c.OutputPerM, &c.CacheWritePerM, &c.CacheReadPerM` before `&created, &updated`.

`ListByTenant` chain SELECT and `Scan` get the same four columns/fields, in the same position.

`Update` becomes:

```go
	uq := r.db.rebind(`UPDATE chains SET name = ?, strategy = ?, fallback_provider = ?, fallback_model = ?,
		input_per_m = ?, output_per_m = ?, cache_write_per_m = ?, cache_read_per_m = ?, updated_at = ? WHERE id = ?`)
	if _, err := tx.ExecContext(ctx, uq, c.Name, c.Strategy, c.FallbackProvider, c.FallbackModel,
		c.InputPerM, c.OutputPerM, c.CacheWritePerM, c.CacheReadPerM, formatTime(time.Now()), c.ID); err != nil {
		return fmt.Errorf("store: update chain: %w", err)
	}
```

and its step INSERT drops the rate columns exactly like `Create`.

`steps()` query becomes `SELECT id, chain_id, position, provider, model, created_at FROM chain_steps WHERE chain_id = ? ORDER BY position ASC` and its `Scan` drops the four rate fields.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go build ./... && go test ./internal/store/ -run 'TestChainPricingRoundTrip|TestChain' -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/store/migrations/0032_chain_pricing.sqlite.sql backend/internal/store/migrations/0032_chain_pricing.postgres.sql backend/internal/store/models.go backend/internal/store/repo_budgets.go backend/internal/store/repo_chain_pricing_test.go
git commit -m "feat(store): move chain model pricing from steps to chains"
```

---

### Task 2: Spread chain rates over every target in `TargetsFromChain`

**Files:**
- Modify: `backend/internal/dispatch/dispatch.go:82-93` (`Target` comment), `1027-1038` (`TargetsFromChain`)
- Test: `backend/internal/dispatch/dispatch_chain_pricing_test.go`

**Interfaces:**
- Consumes: `store.Chain.InputPerM/OutputPerM/CacheWritePerM/CacheReadPerM` (Task 1).
- Produces: `TargetsFromChain(store.Chain) []Target` copies the chain's four rates onto **every** target; the rates no longer come from individual steps.

- [ ] **Step 1: Write the failing test**

Replace `backend/internal/dispatch/dispatch_chain_pricing_test.go` with:

```go
package dispatch

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestTargetsFromChainSpreadsChainRateToEveryStep(t *testing.T) {
	chain := store.Chain{
		InputPerM: 2.5, OutputPerM: 10, CacheWritePerM: 3.125, CacheReadPerM: 0.25,
		Steps: []store.ChainStep{
			{Provider: "openai", Model: "gpt-4o"},
			{Provider: "anthropic", Model: "claude-3-5-sonnet"},
		},
	}
	targets := TargetsFromChain(chain)
	require.Len(t, targets, 2)
	for i, tgt := range targets {
		require.Equal(t, 2.5, tgt.InputPerM, "target %d input", i)
		require.Equal(t, 10.0, tgt.OutputPerM, "target %d output", i)
		require.Equal(t, 3.125, tgt.CacheWritePerM, "target %d cache write", i)
		require.Equal(t, 0.25, tgt.CacheReadPerM, "target %d cache read", i)
	}
	require.Equal(t, "openai", targets[0].Provider)
	require.Equal(t, "anthropic", targets[1].Provider)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/dispatch/ -run TestTargetsFromChainSpreadsChainRateToEveryStep -count=1`
Expected: FAIL — targets have zero rates (input 0, expected 2.5).

- [ ] **Step 3: Update `TargetsFromChain` and the `Target` comment**

Replace the function (dispatch.go ~1027):

```go
// TargetsFromChain flattens a stored chain into ordered targets. The chain's
// price applies to every candidate, so all emitted targets carry the same rates.
func TargetsFromChain(chain store.Chain) []Target {
	out := make([]Target, 0, len(chain.Steps))
	for _, s := range chain.Steps {
		out = append(out, Target{
			Provider: s.Provider, Model: s.Model,
			InputPerM: chain.InputPerM, OutputPerM: chain.OutputPerM,
			CacheWritePerM: chain.CacheWritePerM, CacheReadPerM: chain.CacheReadPerM,
		})
	}
	return out
}
```

Update the `Target` field comment (dispatch.go ~87) from "Optional per-step price override carried from the source chain." to "Optional chain price carried from the source chain."

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/dispatch/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/dispatch/dispatch.go backend/internal/dispatch/dispatch_chain_pricing_test.go
git commit -m "feat(dispatch): apply chain price to every route target"
```

---

### Task 3: Chain-level pricing in the admin API (create/update/list/export/import)

**Files:**
- Modify: `backend/internal/gateway/admin.go:1784-1972` (list/create/update), `2792-2808` (export), `2946-2984` (import)
- Test: `backend/internal/gateway/admin_chain_pricing_test.go`

**Interfaces:**
- Consumes: `store.Chain` rate fields (Task 1), `validRate` (admin.go ~3375).
- Produces: chain JSON gains `input_per_m/output_per_m/cache_write_per_m/cache_read_per_m`; step JSON no longer has them; create/update validate chain rates; export/import carry chain rates.

- [ ] **Step 1: Write the failing test**

Replace `backend/internal/gateway/admin_chain_pricing_test.go` with:

```go
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func newChainPricingTestServer(t *testing.T) *Server {
	t.Helper()
	s, db := newCustomProviderTestServer(t)
	s.chains = db.Chains()
	return s
}

func postChain(t *testing.T, s *Server, body string) (int, map[string]any, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/chains", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.adminCreateChain(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	}
	return rec.Code, out, rec.Body.String()
}

func patchChain(t *testing.T, s *Server, id, body string) (int, string) {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	req := httptest.NewRequest(http.MethodPatch, "/chains/"+id, strings.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()
	s.adminUpdateChain(rec, req)
	return rec.Code, rec.Body.String()
}

func listChains(t *testing.T, s *Server) []map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/chains", nil)
	rec := httptest.NewRecorder()
	s.adminListChains(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		Chains []map[string]any `json:"chains"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out.Chains
}

func TestAdminChainPricing_ExportImportRoundTrip(t *testing.T) {
	s := newChainPricingTestServer(t)
	db := s.db
	s.identity = identity.New(db.APIKeys())
	s.budgets = db.Budgets()
	s.pools = db.ProxyPools()
	s.aliases = db.Aliases()
	s.settings = db.Settings()

	require.NoError(t, db.Chains().Create(context.Background(), store.Chain{
		ID: "c1", TenantID: adminTenant, Name: "priced", Strategy: "priority",
		InputPerM: 2.5, OutputPerM: 10, CacheWritePerM: 3.125, CacheReadPerM: 0.25,
		Steps: []store.ChainStep{{ID: "step-1", Provider: "openai", Model: "gpt-4o", Position: 0}},
	}))

	// Export.
	expRec := httptest.NewRecorder()
	s.adminExportDatabase(expRec, httptest.NewRequest(http.MethodGet, "/settings/database", nil))
	require.Equal(t, http.StatusOK, expRec.Code, expRec.Body.String())
	var export map[string]any
	require.NoError(t, json.Unmarshal(expRec.Body.Bytes(), &export))
	rawChains := export["chains"].([]any)
	require.Len(t, rawChains, 1)
	entry := rawChains[0].(map[string]any)
	require.Equal(t, 2.5, entry["input_per_m"])
	require.Equal(t, 10.0, entry["output_per_m"])
	require.Equal(t, 3.125, entry["cache_write_per_m"])
	require.Equal(t, 0.25, entry["cache_read_per_m"])

	// Import into a fresh store and confirm rates survive.
	s2, db2 := newCustomProviderTestServer(t)
	s2.chains = db2.Chains()
	s2.settings = db2.Settings()
	impRec := httptest.NewRecorder()
	s2.adminImportDatabase(impRec, httptest.NewRequest(http.MethodPost, "/settings/database", strings.NewReader(expRec.Body.String())))
	require.Equal(t, http.StatusOK, impRec.Code, impRec.Body.String())

	imported, err := db2.Chains().ListByTenant(context.Background(), adminTenant)
	require.NoError(t, err)
	require.Len(t, imported, 1)
	require.Equal(t, 2.5, imported[0].InputPerM)
	require.Equal(t, 10.0, imported[0].OutputPerM)
	require.Equal(t, 3.125, imported[0].CacheWritePerM)
	require.Equal(t, 0.25, imported[0].CacheReadPerM)
}

func TestAdminChainPricing_CreateRejectsNegativeInput(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, _, body := postChain(t, s, `{"name":"priced","input_per_m":-1,"output_per_m":1,"steps":[{"provider":"openai","model":"gpt-4o"}]}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, body, "finite and non-negative")
}

func TestAdminChainPricing_CreateRejectsNegativeOutput(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, _, body := postChain(t, s, `{"name":"priced","input_per_m":1,"output_per_m":-1,"steps":[{"provider":"openai","model":"gpt-4o"}]}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, body, "finite and non-negative")
}

func TestAdminChainPricing_CreateRejectsZeroInput(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, _, body := postChain(t, s, `{"name":"priced","input_per_m":0,"output_per_m":1,"steps":[{"provider":"openai","model":"gpt-4o"}]}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, body, "greater than 0")
}

func TestAdminChainPricing_CreateRejectsZeroOutput(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, _, body := postChain(t, s, `{"name":"priced","input_per_m":1,"steps":[{"provider":"openai","model":"gpt-4o"}]}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, body, "greater than 0")
}

func TestAdminChainPricing_CreateRejectsPerStepRate(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, _, body := postChain(t, s, `{"name":"priced","steps":[{"provider":"openai","model":"gpt-4o","input_per_m":2.5}]}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, body, "unknown field")
}

func TestAdminChainPricing_CreateUnpricedChainAllowed(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, body, _ := postChain(t, s, `{"name":"free","steps":[{"provider":"openai","model":"gpt-4o"}]}`)
	require.Equal(t, http.StatusCreated, code, body)
}

func TestAdminChainPricing_CreateAndListRoundTrip(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, body, _ := postChain(t, s, `{"name":"priced","input_per_m":2.5,"output_per_m":10,"cache_write_per_m":3.125,"cache_read_per_m":0.25,"steps":[{"provider":"openai","model":"gpt-4o"}]}`)
	require.Equal(t, http.StatusCreated, code, body)

	chains := listChains(t, s)
	require.Len(t, chains, 1)
	require.Equal(t, 2.5, chains[0]["input_per_m"])
	require.Equal(t, 10.0, chains[0]["output_per_m"])
	require.Equal(t, 3.125, chains[0]["cache_write_per_m"])
	require.Equal(t, 0.25, chains[0]["cache_read_per_m"])
}

func TestAdminChainPricing_UpdateRejectsNegative(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, body, _ := postChain(t, s, `{"name":"priced","input_per_m":1,"output_per_m":2,"steps":[{"provider":"openai","model":"gpt-4o"}]}`)
	require.Equal(t, http.StatusCreated, code, body)
	id := body["id"].(string)

	code, respBody := patchChain(t, s, id, `{"input_per_m":1,"output_per_m":-1}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, respBody, "finite and non-negative")
}

func TestAdminChainPricing_UpdateRejectsZeroOutput(t *testing.T) {
	s := newChainPricingTestServer(t)
	code, body, _ := postChain(t, s, `{"name":"priced","input_per_m":1,"output_per_m":2,"steps":[{"provider":"openai","model":"gpt-4o"}]}`)
	require.Equal(t, http.StatusCreated, code, body)
	id := body["id"].(string)

	code, respBody := patchChain(t, s, id, `{"input_per_m":1,"output_per_m":0}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, respBody, "greater than 0")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/ -run TestAdminChainPricing -count=1`
Expected: FAIL — create with `input_per_m` at chain level is rejected as an unknown field / rates never persist.

- [ ] **Step 3: Update `adminListChains`**

Remove the four rate keys from the step map (keep `provider`, `model`, `position`), and add them to the chain `entry`:

```go
		entry := map[string]any{
			"id": c.ID, "name": c.Name, "strategy": c.Strategy, "steps": steps,
			"input_per_m": c.InputPerM, "output_per_m": c.OutputPerM,
			"cache_write_per_m": c.CacheWritePerM, "cache_read_per_m": c.CacheReadPerM,
		}
```

- [ ] **Step 4: Update `adminCreateChain`**

Add the four rates to the top-level body struct and drop them from the step struct:

```go
	var body struct {
		Name             string  `json:"name"`
		Strategy         string  `json:"strategy"`
		FallbackProvider string  `json:"fallback_provider"`
		FallbackModel    string  `json:"fallback_model"`
		InputPerM        float64 `json:"input_per_m"`
		OutputPerM       float64 `json:"output_per_m"`
		CacheWritePerM   float64 `json:"cache_write_per_m"`
		CacheReadPerM    float64 `json:"cache_read_per_m"`
		Steps            []struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"steps"`
	}
```

After the fallback validation and before `now := time.Now()`, add the chain-price validation:

```go
	if !validRate(body.InputPerM) || !validRate(body.OutputPerM) ||
		!validRate(body.CacheWritePerM) || !validRate(body.CacheReadPerM) {
		writeError(w, http.StatusBadRequest, "invalid chain price: rates must be finite and non-negative")
		return
	}
	if (body.InputPerM > 0 || body.OutputPerM > 0 || body.CacheWritePerM > 0 || body.CacheReadPerM > 0) &&
		(body.InputPerM <= 0 || body.OutputPerM <= 0) {
		writeError(w, http.StatusBadRequest, "chain input and output price must be greater than 0")
		return
	}
```

Set the rates on the `chain` literal and remove the per-step rate validation from the step loop:

```go
	chain := store.Chain{
		ID:               uuid.NewString(),
		TenantID:         adminTenant,
		Name:             body.Name,
		Strategy:         defaultStr(body.Strategy, "priority"),
		FallbackProvider: body.FallbackProvider,
		FallbackModel:    body.FallbackModel,
		InputPerM:        body.InputPerM,
		OutputPerM:       body.OutputPerM,
		CacheWritePerM:   body.CacheWritePerM,
		CacheReadPerM:    body.CacheReadPerM,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	for i, st := range body.Steps {
		if _, ok := connectors.SpecByID(st.Provider); !ok {
			writeError(w, http.StatusBadRequest, "unknown provider in step: "+st.Provider)
			return
		}
		chain.Steps = append(chain.Steps, store.ChainStep{
			ID: uuid.NewString(), ChainID: chain.ID, Position: i,
			Provider: st.Provider, Model: st.Model, CreatedAt: now,
		})
	}
```

- [ ] **Step 5: Update `adminUpdateChain`**

Change the body struct: add `InputPerM/OutputPerM/CacheWritePerM/CacheReadPerM *float64` at the top level, and reduce the step struct to `Provider`/`Model`. Add validation after the `FallbackModel` block (before the `body.Steps` block):

```go
	if body.InputPerM != nil || body.OutputPerM != nil || body.CacheWritePerM != nil || body.CacheReadPerM != nil {
		rates := []*float64{body.InputPerM, body.OutputPerM, body.CacheWritePerM, body.CacheReadPerM}
		for _, r := range rates {
			if r != nil && !validRate(*r) {
				writeError(w, http.StatusBadRequest, "invalid chain price: rates must be finite and non-negative")
				return
			}
		}
		if body.InputPerM != nil {
			existing.InputPerM = *body.InputPerM
		}
		if body.OutputPerM != nil {
			existing.OutputPerM = *body.OutputPerM
		}
		if body.CacheWritePerM != nil {
			existing.CacheWritePerM = *body.CacheWritePerM
		}
		if body.CacheReadPerM != nil {
			existing.CacheReadPerM = *body.CacheReadPerM
		}
		priced := existing.InputPerM > 0 || existing.OutputPerM > 0 || existing.CacheWritePerM > 0 || existing.CacheReadPerM > 0
		if priced && (existing.InputPerM <= 0 || existing.OutputPerM <= 0) {
			writeError(w, http.StatusBadRequest, "chain input and output price must be greater than 0")
			return
		}
	}
```

Remove the rate validation and rate assignment from the step loop (keep the provider/model assignment):

```go
	if body.Steps != nil {
		now := time.Now()
		existing.Steps = make([]store.ChainStep, len(*body.Steps))
		for i, st := range *body.Steps {
			existing.Steps[i] = store.ChainStep{
				ID:        uuid.NewString(),
				ChainID:   id,
				Position:  i,
				Provider:  st.Provider,
				Model:     st.Model,
				CreatedAt: now,
			}
		}
	}
```

- [ ] **Step 6: Update export and import**

Export: remove the four rates from the step map and add them to the chain entry:

```go
		chainsOut = append(chainsOut, map[string]any{
			"name": c.Name, "strategy": c.Strategy, "steps": steps,
			"input_per_m": c.InputPerM, "output_per_m": c.OutputPerM,
			"cache_write_per_m": c.CacheWritePerM, "cache_read_per_m": c.CacheReadPerM,
		})
```

Import: move the four rate fields from the step struct to the outer chain struct and set them on the chain literal:

```go
		var chains []struct {
			Name           string  `json:"name"`
			Strategy       string  `json:"strategy"`
			InputPerM      float64 `json:"input_per_m"`
			OutputPerM     float64 `json:"output_per_m"`
			CacheWritePerM float64 `json:"cache_write_per_m"`
			CacheReadPerM  float64 `json:"cache_read_per_m"`
			Steps          []struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
				Position int    `json:"position"`
			} `json:"steps"`
		}
```

and inside the loop:

```go
				chain := store.Chain{
					ID:        uuid.NewString(),
					TenantID:  adminTenant,
					Name:      c.Name,
					Strategy:  defaultStr(c.Strategy, "priority"),
					InputPerM: c.InputPerM, OutputPerM: c.OutputPerM,
					CacheWritePerM: c.CacheWritePerM, CacheReadPerM: c.CacheReadPerM,
					CreatedAt: now,
					UpdatedAt: now,
				}
				for _, st := range c.Steps {
					chain.Steps = append(chain.Steps, store.ChainStep{
						ID: uuid.NewString(), ChainID: chain.ID, Position: st.Position,
						Provider: st.Provider, Model: st.Model, CreatedAt: now,
					})
				}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go build ./... && go test ./internal/gateway/ -run TestAdminChainPricing -count=1`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/gateway/admin.go backend/internal/gateway/admin_chain_pricing_test.go
git commit -m "feat(gateway): price chains at the chain level"
```

---

### Task 4: Retarget meter/pipeline/PG tests and the model-import path to chain-level

**Files:**
- Modify: `backend/internal/meter/meter_chain_test.go` (names only)
- Modify: `backend/internal/pipeline/pipeline_chain_pricing_test.go:19-31`
- Modify: `backend/internal/store/postgres_integration_test.go:93-120`
- Modify: `backend/internal/gateway/admin_foreign_import.go:553-580` (comment/verification only)

**Interfaces:**
- Consumes: `store.Chain` rate fields (Task 1), `Target` rates (Task 2), chain API (Task 3).
- Produces: no production behavior change; confirms the meter/pipeline path still prices from the rates carried on the target.

- [ ] **Step 1: Rename the meter tests**

In `backend/internal/meter/meter_chain_test.go`, rename `TestRecordUsesChainStepPrice` to `TestRecordUsesChainPrice` and `TestRecordChainPriceSplitsAllFourClasses` stays. No body changes (the `Event` fields are unchanged, so these already pass). Run them to confirm.

Run: `go test ./internal/meter/ -run TestRecord -count=1`
Expected: PASS.

- [ ] **Step 2: Update the pipeline attempt test comment/name**

In `backend/internal/pipeline/pipeline_chain_pricing_test.go`, rename `TestAttemptForTargetsKeepsChainRates` to `TestAttemptForTargetsKeepsChainRates` (keep the name) but change its doc comment first line from "the source target wholesale, so the four chain rates survive" — no code change; the `Target` still carries rates. Run to confirm it still passes.

Run: `go test ./internal/pipeline/ -run 'TestAttemptForTargetsKeepsChainRates|TestPipelineForwardsChainStepPrice' -count=1`
Expected: PASS. (No code edit needed if it passes; only update the comment wording from "chain step rates" to "chain rates" in the `TestPipelineForwardsChainStepPrice` doc comment.)

- [ ] **Step 3: Retarget the Postgres integration subtest**

In `backend/internal/store/postgres_integration_test.go`, replace the `"chain step pricing round-trips"` subtest body (lines 93-120) with a chain-level version:

```go
	t.Run("chain pricing round-trips", func(t *testing.T) {
		now := time.Now().UTC()
		c := Chain{
			ID: fmt.Sprintf("pg-price-%d", now.UnixNano()), TenantID: DefaultTenantID,
			Name: "priced", Strategy: "priority",
			InputPerM: 2.5, OutputPerM: 10, CacheWritePerM: 3.125, CacheReadPerM: 0.25,
			CreatedAt: now, UpdatedAt: now,
		}
		c.Steps = []ChainStep{{
			ID: c.ID + "-s1", ChainID: c.ID, Position: 0, Provider: "openai", Model: "gpt-4o", CreatedAt: now,
		}}
		require.NoError(t, db.Chains().Create(ctx, c))

		got, err := db.Chains().Get(ctx, c.ID)
		require.NoError(t, err)
		require.Equal(t, 2.5, got.InputPerM)
		require.Equal(t, 10.0, got.OutputPerM)
		require.Equal(t, 3.125, got.CacheWritePerM)
		require.Equal(t, 0.25, got.CacheReadPerM)

		c.CacheReadPerM = 0.5
		require.NoError(t, db.Chains().Update(ctx, c))
		again, err := db.Chains().Get(ctx, c.ID)
		require.NoError(t, err)
		require.Equal(t, 0.5, again.CacheReadPerM)
	})
```

- [ ] **Step 4: Confirm the n9router import still compiles**

`admin_foreign_import.go` builds `store.ChainStep` without rate fields already, so it needs no change. Verify by building.

Run: `go build ./...`
Expected: success, no references to removed `ChainStep` rate fields anywhere.

- [ ] **Step 5: Run the full backend suite**

Run: `go test ./... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/meter/meter_chain_test.go backend/internal/pipeline/pipeline_chain_pricing_test.go backend/internal/store/postgres_integration_test.go
git commit -m "test: cover chain-level pricing across meter, pipeline, and Postgres"
```

---

### Task 5: Move chain pricing to the chain level in the dashboard

**Files:**
- Modify: `frontend/src/lib/api.ts:315-332`, `1346-1349`
- Modify: `frontend/src/components/chains/chainUtils.ts` (types + `makeDraftStep`)
- Modify: `frontend/src/pages/ChainEditor.tsx`
- Test: `frontend/src/components/chains/chainUtils.test.ts`

**Interfaces:**
- Consumes: chain JSON with top-level rates and step JSON with `provider`/`model`/`position` only (Task 3).
- Produces: `api.Chain` gains the four rates; `api.ChainStep` loses them; `createChain`/`updateChain` payloads take chain-level rates; `DraftChainStep` keeps its rate fields only as UI-local state, no longer mapped from step objects; the editor renders one pricing group for the chain.

- [ ] **Step 1: Write the failing test**

Replace `frontend/src/components/chains/chainUtils.test.ts` with:

```ts
import { test } from "node:test";
import assert from "node:assert/strict";
import { deriveCacheRates, makeDraftStep, toDraftSteps } from "./chainUtils.ts";

test("deriveCacheRates derives write 1.25x and read 0.1x from input", () => {
  assert.deepEqual(deriveCacheRates(2, { cacheWritePerM: 0, cacheReadPerM: 0 }), {
    cacheWritePerM: 2.5,
    cacheReadPerM: 0.2,
  });
});

test("deriveCacheRates preserves a manual override that differs from the old derivation", () => {
  assert.deepEqual(
    deriveCacheRates(4, { cacheWritePerM: 9, cacheReadPerM: 0.2, prevInput: 2 }),
    { cacheWritePerM: 9, cacheReadPerM: 0.4 },
  );
});

test("deriveCacheRates re-derives a field that still equals the old formula", () => {
  assert.deepEqual(
    deriveCacheRates(4, { cacheWritePerM: 2.5, cacheReadPerM: 0.2, prevInput: 2 }),
    { cacheWritePerM: 5, cacheReadPerM: 0.4 },
  );
});

test("makeDraftStep drops any pricing carried on the step payload", () => {
  const step = makeDraftStep({ provider: "openai", model: "gpt-4o" });
  assert.equal(step.inputPerM, 0);
  assert.equal(step.outputPerM, 0);
  assert.equal(step.cacheWritePerM, 0);
  assert.equal(step.cacheReadPerM, 0);
});

test("toDraftSteps maps steps without reading step pricing", () => {
  const steps = toDraftSteps({
    id: "c1",
    name: "priced",
    strategy: "priority",
    input_per_m: 2.5,
    output_per_m: 10,
    cache_write_per_m: 3.125,
    cache_read_per_m: 0.25,
    steps: [{ provider: "openai", model: "gpt-4o", position: 0 }],
  });
  assert.equal(steps.length, 1);
  assert.equal(steps[0].provider, "openai");
  assert.equal(steps[0].inputPerM, 0);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npm test`
Expected: FAIL — `toDraftSteps` still reads step pricing / `api.Chain` typing mismatch (TS strip).

- [ ] **Step 3: Update `api.ts` types**

In `frontend/src/lib/api.ts`, reduce `ChainStep` (line 315) to:

```ts
export interface ChainStep {
  provider: string;
  model: string;
  position: number;
}
```

and add the rates to `Chain`:

```ts
export interface Chain {
  id: string;
  name: string;
  strategy: string;
  fallback_provider?: string;
  fallback_model?: string;
  input_per_m: number;
  output_per_m: number;
  cache_write_per_m: number;
  cache_read_per_m: number;
  steps: ChainStep[];
}
```

Update the method signatures (line 1346):

```ts
  createChain: (input: { name: string; strategy?: string; fallback_provider?: string; fallback_model?: string; input_per_m?: number; output_per_m?: number; cache_write_per_m?: number; cache_read_per_m?: number; steps: { provider: string; model: string }[] }) =>
    request<{ id: string }>("POST", "/chains", input),
  updateChain: (id: string, patch: { name?: string; strategy?: string; fallback_provider?: string; fallback_model?: string; input_per_m?: number; output_per_m?: number; cache_write_per_m?: number; cache_read_per_m?: number; steps?: { provider: string; model: string }[] }) =>
    request<{ id: string }>("PATCH", `/chains/${id}`, patch),
```

- [ ] **Step 4: Update `chainUtils.ts`**

`makeDraftStep` takes only `provider`/`model` and always starts rates at 0:

```ts
export const makeDraftStep = (step?: {
  provider: string;
  model: string;
}): DraftChainStep => ({
  id: crypto.randomUUID(),
  provider: step?.provider ?? "",
  model: step?.model ?? "",
  inputPerM: 0,
  outputPerM: 0,
  cacheWritePerM: 0,
  cacheReadPerM: 0,
});
```

`toDraftSteps` stays as-is (it calls `makeDraftStep`), which drops step pricing.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd frontend && npm test`
Expected: PASS (5/5).

- [ ] **Step 6: Move the pricing UI to the chain level in `ChainEditor.tsx`**

Add chain-level state (replace the per-step `pricingOverrides`/`pricingDrafts` maps):

```tsx
  const [chainPrice, setChainPrice] = useState({ inputPerM: 0, outputPerM: 0, cacheWritePerM: 0, cacheReadPerM: 0 });
  const [priceOpen, setPriceOpen] = useState(false);
  const [priceDrafts, setPriceDrafts] = useState<Map<string, string>>(() => new Map());
```

In the hydrate effect, seed it from the chain:

```tsx
    setChainPrice({ inputPerM: existing.input_per_m ?? 0, outputPerM: existing.output_per_m ?? 0, cacheWritePerM: existing.cache_write_per_m ?? 0, cacheReadPerM: existing.cache_read_per_m ?? 0 });
    setPriceOpen((existing.input_per_m ?? 0) > 0 || (existing.output_per_m ?? 0) > 0 || (existing.cache_write_per_m ?? 0) > 0 || (existing.cache_read_per_m ?? 0) > 0);
```

Replace `needsPricing` and the validation clause:

```tsx
  const priced = chainPrice.inputPerM > 0 || chainPrice.outputPerM > 0 || chainPrice.cacheWritePerM > 0 || chainPrice.cacheReadPerM > 0;
  const needsPricing = priced && (chainPrice.inputPerM <= 0 || chainPrice.outputPerM <= 0);
```

and in `validationMessage` change the `needsPricing` text to `"Set both input and output price, or clear all price fields."`.

Add the chain-level rate updater (mirrors the old per-step one, no step id):

```tsx
  const updateChainRate = (field: "inputPerM" | "outputPerM" | "cacheWritePerM" | "cacheReadPerM", raw: string) => {
    const value = raw === "" ? 0 : Number(raw);
    const validValue = Number.isFinite(value) && value >= 0;
    setPriceDrafts((current) => {
      const next = new Map(current).set(field, raw);
      if (field === "inputPerM") {
        if (chainPrice.cacheWritePerM === 0 || chainPrice.cacheWritePerM === +(chainPrice.inputPerM * CACHE_WRITE_FACTOR).toFixed(8)) next.delete("cacheWritePerM");
        if (chainPrice.cacheReadPerM === 0 || chainPrice.cacheReadPerM === +(chainPrice.inputPerM * CACHE_READ_FACTOR).toFixed(8)) next.delete("cacheReadPerM");
      }
      return next;
    });
    setDirty(true);
    if (!validValue) return;
    setChainPrice((current) => {
      if (field === "inputPerM") return { ...current, inputPerM: value, ...deriveCacheRates(value, { cacheWritePerM: current.cacheWritePerM, cacheReadPerM: current.cacheReadPerM, prevInput: current.inputPerM }) };
      return { ...current, [field]: value };
    });
  };
  const rateDraft = (field: "inputPerM" | "outputPerM" | "cacheWritePerM" | "cacheReadPerM") => priceDrafts.get(field) ?? (chainPrice[field] ? String(chainPrice[field]) : "");
```

Update `routeChain` and the save payload to put the rates on the chain and steps with only provider/model:

```tsx
  const routeChain = useMemo(() => ({ id: existing?.id ?? "draft", name, strategy, steps: completeSteps.map((step, position) => ({ provider: step.provider, model: step.model, position } as ChainStep)), fallback_provider: fallbackEnabled ? fallback.provider : "", fallback_model: fallbackEnabled ? fallback.model : "" } as Chain), [completeSteps, existing?.id, fallback.model, fallback.provider, fallbackEnabled, name, strategy]);
```

```tsx
      const payload = { name: name.trim(), strategy, input_per_m: chainPrice.inputPerM, output_per_m: chainPrice.outputPerM, cache_write_per_m: chainPrice.cacheWritePerM, cache_read_per_m: chainPrice.cacheReadPerM, steps: completeSteps.map((step) => ({ provider: step.provider, model: step.model })), fallback_provider: fallbackEnabled ? fallback.provider : "", fallback_model: fallbackEnabled ? fallback.model : "" };
```

Remove `updateRate`, `togglePricing`, and the per-step `priced`/`pricingOpen`/pricing row from the step loop. Add one chain-level pricing `Card` (place it after the "Routing strategy" card) with a toggle and the four fields:

```tsx
        <Card className="p-5 sm:p-6">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div><h2 className="text-base font-semibold">Model pricing</h2><p className="mt-1 text-sm text-[var(--text-muted)]">One price for the whole chain model, applied to every route step. Leave blank to use the catalog price.</p></div>
            <button type="button" onClick={() => { setPriceOpen((open) => !open); setDirty(true); }} aria-expanded={priceOpen} className={`flex h-9 items-center gap-1 rounded-lg px-2 text-xs font-medium ${priced ? "text-accent-700 dark:text-accent-300" : "text-[var(--text-muted)]"} hover:bg-[var(--bg-elevated)]`}><DollarSign className="h-3.5 w-3.5" /><span>Pricing</span><ChevronDown className={`h-3.5 w-3.5 transition-transform ${priceOpen ? "rotate-180" : ""}`} /></button>
          </div>
          {priceOpen && <div className="mt-4 grid gap-2 border-t border-[var(--border)] pt-4 sm:grid-cols-4">
            <Field label="Input $/M"><Input type="number" min={0} step="0.01" value={rateDraft("inputPerM")} onChange={(event) => updateChainRate("inputPerM", event.target.value)} placeholder="0" /></Field>
            <Field label="Output $/M"><Input type="number" min={0} step="0.01" value={rateDraft("outputPerM")} onChange={(event) => updateChainRate("outputPerM", event.target.value)} placeholder="0" /></Field>
            <Field label={`Cache write $/M${chainPrice.cacheWritePerM === +(chainPrice.inputPerM * CACHE_WRITE_FACTOR).toFixed(8) ? " (auto)" : ""}`}><Input type="number" min={0} step="0.01" value={rateDraft("cacheWritePerM")} onChange={(event) => updateChainRate("cacheWritePerM", event.target.value)} placeholder="auto" /></Field>
            <Field label={`Cache read $/M${chainPrice.cacheReadPerM === +(chainPrice.inputPerM * CACHE_READ_FACTOR).toFixed(8) ? " (auto)" : ""}`}><Input type="number" min={0} step="0.01" value={rateDraft("cacheReadPerM")} onChange={(event) => updateChainRate("cacheReadPerM", event.target.value)} placeholder="auto" /></Field>
          </div>}
        </Card>
```

Remove the `pricingOverrides`/`pricingDrafts`/`pricingOpen`/`rateDraft(step, …)` usages from the step row markup (the step row keeps only grip, number, picker, move/remove buttons).

- [ ] **Step 7: Typecheck, test, build**

Run: `cd frontend && npm test && npm run typecheck && npm run build`
Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/lib/api.ts frontend/src/components/chains/chainUtils.ts frontend/src/components/chains/chainUtils.test.ts frontend/src/pages/ChainEditor.tsx
git commit -m "feat(ui): price the chain model, not each route step"
```

---

### Task 6: Verification pass on the merged result

**Files:** none (verification only)

- [ ] **Step 1: Full backend build, vet, tests**

Run: `cd backend && go build ./... && go vet ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 2: Frontend gates**

Run: `cd frontend && npm test && npm run typecheck && npm run build`
Expected: PASS.

- [ ] **Step 3: gofmt check on changed Go files**

Run: `cd backend && gofmt -l internal/store internal/dispatch internal/gateway internal/meter internal/pipeline`
Expected: no output.

- [ ] **Step 4: Confirm no stale references**

Run: `cd backend && rg -n "InputPerM|OutputPerM|CacheWritePerM|CacheReadPerM" internal/store/models.go`
Expected: only the four `Chain` fields appear; none under `ChainStep`.

- [ ] **Step 5: Report**

Summarize executed commands and results. Do not claim success for any command not actually run.
