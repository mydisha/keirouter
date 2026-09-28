# Chain Model Pricing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an operator set per-million-token Input, Output, Cache write, and Cache read prices on each model step of a routing chain, and have those prices be the price used when a request is served through that chain step.

**Architecture:** Store the four rates on `chain_steps`. `store.ChainStep` carries them into `dispatch.TargetsFromChain` → `dispatch.Target` → the winning `Attempt.Target` → `meter.Event` → `Meter.Record`, which computes cost from a `Price{Source:"chain"}` instead of the catalog when a non-zero rate is present. The chain editor auto-derives cache write/read from Input (1.25x / 0.1x) and preserves manual overrides value-wise.

**Tech Stack:** Go 1.26 (module `github.com/mydisha/keirouter/backend`), SQLite (modernc) + Postgres, React 19 / TypeScript / Vite / Tailwind v4 / TanStack Query.

## Global Constraints

- All Go commands run from `/home/emalution/keirouter/backend` (repo-root `go build ./...` fails under `go.work`).
- Money math: rates are `float64`; final cost stays in nanodollars via `tokenCostNanos` (`pricing.go:226`) using `math.Round`, never float accumulation across per-request totals.
- Rate columns are `REAL` in SQLite and converted to `DOUBLE PRECISION` in Postgres (mirrors `0026_postgres_numeric_capacity.postgres.sql`).
- Cache factors: **write = 1.25 × Input**, **read = 0.1 × Input**.
- Override decision has no boolean column: a step price applies when **at least one** of the four rates is `> 0`; all-zero falls back to catalog (accepted limitation).
- Chain create/update must reject a step that supplies missing, negative, or non-finite rates (HTTP 400).
- The chain's final fallback target keeps catalog pricing (no rate columns).
- No changes to public landing / `provider_id=combo` listing, provider-account handling, or historical usage re-pricing.
- `docs/` is gitignored: use `git add -f` for spec/plan files.
- Do not commit unless the task's final step says so.

---

### Task 1: Persist step rates (migration + model + repo)

**Files:**
- Create: `backend/internal/store/migrations/0031_chain_step_pricing.sqlite.sql`
- Create: `backend/internal/store/migrations/0031_chain_step_pricing.postgres.sql`

> Migration runner note: `migrationVersion` strips `.sql` then `.postgres`/`.sqlite`,
> so a suffixless `.sql` and a `.postgres.sql` with the same number collapse to one
> version and only one would apply. Use the repo's 0028 split: a self-contained
> `.sqlite.sql` (adds `REAL` columns) and a self-contained `.postgres.sql`
> (adds columns AND converts them to `DOUBLE PRECISION`).
- Modify: `backend/internal/store/models.go:118-125` (`ChainStep`)
- Modify: `backend/internal/store/repo_budgets.go:143-149`, `:244-250`, `:254-276`

**Interfaces:**
- Consumes: nothing.
- Produces: `store.ChainStep.InputPerM`, `.OutputPerM`, `.CacheWritePerM`, `.CacheReadPerM float64`. `ChainRepo.Create`/`Update`/`steps` round-trip them.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/store/repo_chain_pricing_test.go`:

```go
package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChainStepPricingRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	now := time.Now().UTC()
	c := Chain{
		ID: "c-price", TenantID: DefaultTenantID, Name: "priced", Strategy: "priority",
		CreatedAt: now, UpdatedAt: now,
		Steps: []ChainStep{{
			ID: "s1", ChainID: "c-price", Position: 0, Provider: "openai", Model: "gpt-4o",
			InputPerM: 2.5, OutputPerM: 10, CacheWritePerM: 3.125, CacheReadPerM: 0.25, CreatedAt: now,
		}},
	}
	require.NoError(t, db.Chains().Create(ctx, c))

	got, err := db.Chains().Get(ctx, "c-price")
	require.NoError(t, err)
	require.Len(t, got.Steps, 1)
	require.Equal(t, 2.5, got.Steps[0].InputPerM)
	require.Equal(t, 10.0, got.Steps[0].OutputPerM)
	require.Equal(t, 3.125, got.Steps[0].CacheWritePerM)
	require.Equal(t, 0.25, got.Steps[0].CacheReadPerM)

	// Update path replaces steps and must keep rates.
	c.Steps[0].CacheReadPerM = 0.5
	require.NoError(t, db.Chains().Update(ctx, c))
	again, err := db.Chains().Get(ctx, "c-price")
	require.NoError(t, err)
	require.Equal(t, 0.5, again.Steps[0].CacheReadPerM)

	list, err := db.Chains().ListByTenant(ctx, DefaultTenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, 3.125, list[0].Steps[0].CacheWritePerM)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestChainStepPricingRoundTrip -v`
Expected: FAIL — unknown fields `InputPerM` etc. (compile error).

- [ ] **Step 3: Add the migration files**

`backend/internal/store/migrations/0031_chain_step_pricing.sqlite.sql`:

```sql
ALTER TABLE chain_steps ADD COLUMN input_per_m REAL NOT NULL DEFAULT 0;
ALTER TABLE chain_steps ADD COLUMN output_per_m REAL NOT NULL DEFAULT 0;
ALTER TABLE chain_steps ADD COLUMN cache_write_per_m REAL NOT NULL DEFAULT 0;
ALTER TABLE chain_steps ADD COLUMN cache_read_per_m REAL NOT NULL DEFAULT 0;
```

`backend/internal/store/migrations/0031_chain_step_pricing.postgres.sql`:

```sql
ALTER TABLE chain_steps ADD COLUMN input_per_m REAL NOT NULL DEFAULT 0;
ALTER TABLE chain_steps ADD COLUMN output_per_m REAL NOT NULL DEFAULT 0;
ALTER TABLE chain_steps ADD COLUMN cache_write_per_m REAL NOT NULL DEFAULT 0;
ALTER TABLE chain_steps ADD COLUMN cache_read_per_m REAL NOT NULL DEFAULT 0;
ALTER TABLE chain_steps ALTER COLUMN input_per_m TYPE DOUBLE PRECISION;
ALTER TABLE chain_steps ALTER COLUMN output_per_m TYPE DOUBLE PRECISION;
ALTER TABLE chain_steps ALTER COLUMN cache_write_per_m TYPE DOUBLE PRECISION;
ALTER TABLE chain_steps ALTER COLUMN cache_read_per_m TYPE DOUBLE PRECISION;
```

- [ ] **Step 4: Add fields to `ChainStep`**

In `backend/internal/store/models.go`, extend the struct:

```go
// ChainStep is one candidate target within a chain.
type ChainStep struct {
	ID        string
	ChainID   string
	Position  int
	Provider  string
	Model     string
	CreatedAt time.Time

	// Per-step price override. All zero = fall back to catalog pricing.
	InputPerM      float64
	OutputPerM     float64
	CacheWritePerM float64
	CacheReadPerM  float64
}
```

- [ ] **Step 5: Persist and read the columns**

In `backend/internal/store/repo_budgets.go`, replace the `INSERT INTO chain_steps` in both `Create` and `Update` with:

```go
sq := r.db.rebind(`INSERT INTO chain_steps (id, chain_id, position, provider, model,
	input_per_m, output_per_m, cache_write_per_m, cache_read_per_m, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
```

`:144` in `Create` (pass `s.CreatedAt`) and `:244` in `Update` (pass `time.Now()`):

```go
if _, err := tx.ExecContext(ctx, sq, s.ID, c.ID, s.Position, s.Provider, s.Model,
	s.InputPerM, s.OutputPerM, s.CacheWritePerM, s.CacheReadPerM, formatTime(s.CreatedAt)); err != nil {
```

And in `steps` (`:254`) select and scan the new columns:

```go
q := r.db.rebind(`SELECT id, chain_id, position, provider, model,
	input_per_m, output_per_m, cache_write_per_m, cache_read_per_m, created_at
	FROM chain_steps WHERE chain_id = ? ORDER BY position ASC`)
```

```go
if err := rows.Scan(&s.ID, &s.ChainID, &s.Position, &s.Provider, &s.Model,
	&s.InputPerM, &s.OutputPerM, &s.CacheWritePerM, &s.CacheReadPerM, &created); err != nil {
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestChainStepPricingRoundTrip -v`
Expected: PASS.

- [ ] **Step 7: Postgres round-trip**

Add a subtest to `backend/internal/store/postgres_integration_test.go` (same `TestPostgresCompatibility` function, gated by `KEIROUTER_TEST_POSTGRES_DSN`) covering `ChainStep` rates round-trip, mirroring the SQLite test above but using the PG `db` from the test fixture. Verify migration `0031` applies idempotently (run the test twice).

Run against a disposable Postgres (do NOT use the production sidecar, which has no host port):
`KEIROUTER_TEST_POSTGRES_DSN="postgres://keirouter:keirouter@127.0.0.1:55432/keirouter?sslmode=disable" go test ./internal/store/ -run TestPostgresCompatibility -v -count=1`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/store/migrations/0031_chain_step_pricing.sqlite.sql \
  backend/internal/store/migrations/0031_chain_step_pricing.postgres.sql \
  backend/internal/store/models.go backend/internal/store/repo_budgets.go \
  backend/internal/store/repo_chain_pricing_test.go \
  backend/internal/store/postgres_integration_test.go
git commit -m "feat(store): persist per-step chain pricing"
```

---

### Task 2: Carry rates through dispatch to the meter

**Files:**
- Modify: `backend/internal/dispatch/dispatch.go:82-86` (`Target`), `:1020-1027` (`TargetsFromChain`)
- Modify: `backend/internal/meter/meter.go:111-139` (`Event`), `:199-207` (`Record`)
- Test: `backend/internal/dispatch/dispatch_chain_pricing_test.go` (create)
- Test: `backend/internal/meter/meter_chain_test.go` (extend)

**Interfaces:**
- Consumes: `store.ChainStep.InputPerM/OutputPerM/CacheWritePerM/CacheReadPerM` (Task 1).
- Produces: `dispatch.Target.InputPerM`, `.OutputPerM`, `.CacheWritePerM`, `.CacheReadPerM float64`; `meter.Event.InputPerM`, `.OutputPerM`, `.CacheWritePerM`, `.CacheReadPerM float64`; `meter.Price` is already defined (`meter.go:23`).

- [ ] **Step 1: Write the failing dispatch test**

Create `backend/internal/dispatch/dispatch_chain_pricing_test.go`:

```go
package dispatch

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestTargetsFromChainCarriesPricing(t *testing.T) {
	chain := store.Chain{Steps: []store.ChainStep{
		{Provider: "openai", Model: "gpt-4o", InputPerM: 2.5, OutputPerM: 10, CacheWritePerM: 3.125, CacheReadPerM: 0.25},
	}}
	targets := TargetsFromChain(chain)
	require.Len(t, targets, 1)
	require.Equal(t, 2.5, targets[0].InputPerM)
	require.Equal(t, 10.0, targets[0].OutputPerM)
	require.Equal(t, 3.125, targets[0].CacheWritePerM)
	require.Equal(t, 0.25, targets[0].CacheReadPerM)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/dispatch/ -run TestTargetsFromChainCarriesPricing -v`
Expected: FAIL — `targets[0].InputPerM` undefined (compile error).

- [ ] **Step 3: Extend `dispatch.Target` and `TargetsFromChain`**

`Target` (`dispatch.go:82`):

```go
// Target is one candidate in a fallback chain.
type Target struct {
	Provider string
	Model    string

	// Optional per-step price override carried from the source chain. All zero
	// means the meter must resolve the catalog price instead.
	InputPerM      float64
	OutputPerM     float64
	CacheWritePerM float64
	CacheReadPerM  float64
}
```

`TargetsFromChain` (`dispatch.go:1020`):

```go
func TargetsFromChain(chain store.Chain) []Target {
	out := make([]Target, 0, len(chain.Steps))
	for _, s := range chain.Steps {
		out = append(out, Target{
			Provider: s.Provider, Model: s.Model,
			InputPerM: s.InputPerM, OutputPerM: s.OutputPerM,
			CacheWritePerM: s.CacheWritePerM, CacheReadPerM: s.CacheReadPerM,
		})
	}
	return out
}
```

- [ ] **Step 4: Run dispatch test to verify it passes**

Run: `go test ./internal/dispatch/ -run TestTargetsFromChainCarriesPricing -v`
Expected: PASS.

- [ ] **Step 5: Write the failing meter test**

Append to `backend/internal/meter/meter_chain_test.go`:

```go
func TestRecordUsesChainStepPrice(t *testing.T) {
	cap := &chainCaptureStore{}
	m := New(cap, nil, nil) // no catalog prices at all
	_, err := m.Record(context.Background(), Event{
		TenantID: store.DefaultTenantID, Provider: "openai", Model: "gpt-4o", ChainID: "c1",
		InputPerM: 2.5, OutputPerM: 10, CacheWritePerM: 3.125, CacheReadPerM: 0.25,
		Status: "success",
		Usage:  core.Usage{PromptTokens: 1000, CompletionTokens: 500, Source: core.UsageSourceProvider},
	})
	require.NoError(t, err)
	require.Equal(t, "chain", cap.last.PricingSource)
	// tokenCostNanos = tokens*rate*1000: 1000 tokens @ $2.5/M = 2,500,000 nanos;
	// 500 tokens @ $10/M = 5,000,000 nanos → 7,500,000 nanos total.
	require.Equal(t, int64(7_500_000), cap.last.CostNanos)
}

func TestRecordFallsBackToCatalogWhenNoChainPrice(t *testing.T) {
	cap := &chainCaptureStore{}
	m := New(cap, nil, map[string]Price{"openai/gpt-4o": {InputPerM: 1, OutputPerM: 2, Source: "catalog"}})
	_, err := m.Record(context.Background(), Event{
		TenantID: store.DefaultTenantID, Provider: "openai", Model: "gpt-4o", ChainID: "c1",
		Status: "success",
		Usage:  core.Usage{PromptTokens: 1000, CompletionTokens: 500, Source: core.UsageSourceProvider},
	})
	require.NoError(t, err)
	require.Equal(t, "catalog", cap.last.PricingSource)
	require.Equal(t, int64(2_000_000), cap.last.CostNanos) // 1,000,000 + 1,000,000 nanos
}
```

- [ ] **Step 6: Run meter tests to verify they fail**

Run: `go test ./internal/meter/ -run 'TestRecordUsesChainStepPrice|TestRecordFallsBackToCatalogWhenNoChainPrice' -v`
Expected: FAIL — `Event.InputPerM` undefined (compile error).

- [ ] **Step 7: Extend `meter.Event`**

In `backend/internal/meter/meter.go`, add to `Event` after `ChainID`:

```go
	// Optional per-step price override from the serving chain step. When any
	// rate is > 0 the meter prices from these instead of the catalog.
	InputPerM      float64
	OutputPerM     float64
	CacheWritePerM float64
	CacheReadPerM  float64
```

- [ ] **Step 8: Use chain rates in `Record`**

In `Meter.Record` (`meter.go:199-207`), replace the `CalculateCost` call branch:

```go
	var cost CostBreakdown
	if u.PromptTokens+u.CompletionTokens == 0 && !ev.CacheHit {
		cost.Pricing = PricingMatch{Status: "none", MatchKind: "none"}
	} else if price, ok := chainPrice(ev); ok {
		cost = calculateCostFromPrice(pricingMatch("chain", price, "chain", false), u, ev.CacheHit, savedTokens)
	} else {
		cost = m.CalculateCost(ev.Provider, ev.Model, u, ev.CacheHit, savedTokens)
	}
```

Add helpers to `pricing.go`:

```go
// chainPrice returns the operator-configured step price when present. All-zero
// rates mean "unset" and fall back to catalog resolution.
func chainPrice(ev Event) (Price, bool) {
	if ev.InputPerM <= 0 && ev.OutputPerM <= 0 && ev.CacheWritePerM <= 0 && ev.CacheReadPerM <= 0 {
		return Price{}, false
	}
	return Price{
		InputPerM: ev.InputPerM, OutputPerM: ev.OutputPerM,
		CacheWritePerM: ev.CacheWritePerM, CachedInputPerM: ev.CacheReadPerM,
		ReasoningPerM: ev.OutputPerM,
		Source: "chain",
	}, true
}

// calculateCostFromPrice runs the same token math as CalculateCost but with an
// already-resolved price match. It takes the full match so the catalog path
// keeps its provenance (key, match kind, estimated status) instead of both
// paths drifting.
func calculateCostFromPrice(match PricingMatch, uUsage core.Usage, cacheHit bool, savedInputTokens int) CostBreakdown {
	u := clampUsage(uUsage)
	out := CostBreakdown{Pricing: match}
	p := effectivePrice(match.Price, u.PromptTokens)
	out.InputRatePerM = p.InputPerM
	out.CachedRatePerM = p.CachedInputPerM
	if out.CachedRatePerM == 0 {
		out.CachedRatePerM = p.InputPerM
	}
	out.CacheWriteRatePerM = p.CacheWritePerM
	if out.CacheWriteRatePerM == 0 {
		out.CacheWriteRatePerM = p.InputPerM
	}
	out.OutputRatePerM = p.OutputPerM
	out.ReasoningRatePerM = p.ReasoningPerM
	if out.ReasoningRatePerM == 0 {
		out.ReasoningRatePerM = p.OutputPerM
	}
	standardInput := u.PromptTokens - u.CachedTokens - u.CacheWriteTokens
	normalOutput := u.CompletionTokens - u.ReasoningTokens
	out.InputCostNanos = tokenCostNanos(standardInput, out.InputRatePerM)
	out.CachedCostNanos = tokenCostNanos(u.CachedTokens, out.CachedRatePerM)
	out.CacheWriteCostNanos = tokenCostNanos(u.CacheWriteTokens, out.CacheWriteRatePerM)
	out.OutputCostNanos = tokenCostNanos(normalOutput, out.OutputRatePerM)
	out.ReasoningCostNanos = tokenCostNanos(u.ReasoningTokens, out.ReasoningRatePerM)
	retail := out.InputCostNanos + out.CachedCostNanos + out.CacheWriteCostNanos + out.OutputCostNanos + out.ReasoningCostNanos
	out.SavedCostNanos = tokenCostNanos(savedInputTokens, out.InputRatePerM)
	if cacheHit {
		out.AvoidedCostNanos = retail
	} else {
		out.CostNanos = retail
		out.CostMicros = int64(math.Round(float64(retail) / 1000))
	}
	return out
}
```

Note: `CalculateCost` in `pricing.go` should be refactored to call `calculateCostFromPrice(match, raw, cacheHit, savedInputTokens)` after `ResolvePrice`, so the two paths cannot drift. Keep the existing early return for `missing`/`none`:

```go
	out := CostBreakdown{Pricing: match}
	if match.Status == "missing" || match.Status == "none" {
		return out
	}
	return calculateCostFromPrice(match, raw, cacheHit, savedInputTokens)
```

- [ ] **Step 9: Run meter tests to verify they pass**

Run: `go test ./internal/meter/ -v`
Expected: PASS (including existing `meter_test.go` and `meter_chain_test.go`).

- [ ] **Step 10: Commit**

```bash
git add backend/internal/dispatch/dispatch.go backend/internal/dispatch/dispatch_chain_pricing_test.go \
  backend/internal/meter/meter.go backend/internal/meter/pricing.go backend/internal/meter/meter_chain_test.go
git commit -m "feat(meter): price requests from chain step rates"
```

---

### Task 3: Pipeline forwards the winning target's rates

**Files:**
- Modify: `backend/internal/pipeline/pipeline.go:1664-1682` (`recordOutcomeWithTTFT`)
- Test: `backend/internal/pipeline/pipeline_chain_pricing_test.go` (create)

**Interfaces:**
- Consumes: `dispatch.Target` rate fields and `meter.Event` rate fields (Task 2).
- Produces: no new symbols; verified behavior only.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/pipeline/pipeline_chain_pricing_test.go`. Use the existing pipeline test harness pattern in `backend/internal/pipeline/headroom_e2e_test.go` as the model for wiring a fake connector + capture meter; assert the persisted `UsageRecord.PricingSource == "chain"` and the recorded rates match the chain step.

```go
// Sketch: build a pipeline whose dispatcher returns a Target with
// InputPerM=2.5, OutputPerM=10, CacheWritePerM=3.125, CacheReadPerM=0.25 and a
// fake connector returning 1000 prompt / 500 completion tokens; then assert the
// meter-captured UsageRecord has PricingSource "chain" and CostNanos 7500.
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pipeline/ -run TestPipelineForwardsChainStepPrice -v`
Expected: FAIL — `PricingSource` is not `chain` (rates not forwarded yet).

- [ ] **Step 3: Forward rates**

In `recordOutcomeWithTTFT` (`pipeline.go:1664`), add to the `meter.Event` literal:

```go
		ChainID:         meta.ChainID,
		InputPerM:       attempt.Target.InputPerM,
		OutputPerM:      attempt.Target.OutputPerM,
		CacheWritePerM:  attempt.Target.CacheWritePerM,
		CacheReadPerM:   attempt.Target.CacheReadPerM,
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/pipeline/ -run TestPipelineForwardsChainStepPrice -v`
Expected: PASS.

- [ ] **Step 5: Run the full pipeline package**

Run: `go test ./internal/pipeline/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/pipeline/pipeline.go backend/internal/pipeline/pipeline_chain_pricing_test.go
git commit -m "feat(pipeline): forward chain step price to the meter"
```

---

### Task 4: Admin API accepts, validates, and returns step prices

**Files:**
- Modify: `backend/internal/gateway/admin.go:1783-1805` (`adminListChains`), `:1807-1868` (`adminCreateChain`), `:1878-1936` (`adminUpdateChain`)
- Create: `backend/internal/gateway/admin_chain_pricing_test.go`

**Interfaces:**
- Consumes: `store.ChainStep` rate fields (Task 1).
- Produces: JSON step shape `{provider, model, position, input_per_m, output_per_m, cache_write_per_m, cache_read_per_m}` on list; create/update accept the same four rate keys.

- [ ] **Step 1: Write the failing tests**

Create `backend/internal/gateway/admin_chain_pricing_test.go` using the E2E harness pattern (`gateway_e2e_test.go`). Cover:

```go
// 1. POST /admin/chains with a step missing input_per_m → 400.
// 2. POST /admin/chains with input_per_m = -1 → 400.
// 3. POST valid four rates → 201; GET /admin/chains returns them on the step.
// 4. PATCH /admin/chains/{id} with a step missing output_per_m → 400.
```

(Use the admin auth path already exercised by `admin_custom_providers_test.go`; reuse its login helper rather than inventing a new one. If no shared helper exists, drive `POST /api/auth/login` with the default password as the other admin tests do.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway/ -run TestAdminChainPricing -v`
Expected: FAIL (rates ignored / no 400).

- [ ] **Step 3: Add validation helper**

In `backend/internal/gateway/admin.go` (near `validateChainName`, `:3295`):

```go
// validRate reports whether r is a finite, non-negative price. NaN and Inf are
// rejected so a malformed rate can never poison cost math.
func validRate(r float64) bool {
	return r >= 0 && !math.IsNaN(r) && !math.IsInf(r, 0)
}
```

Add `"math"` to the imports if not present.

- [ ] **Step 4: Decode and validate on create**

In `adminCreateChain`, extend the `Steps` anonymous struct:

```go
		Steps            []struct {
			Provider       string  `json:"provider"`
			Model          string  `json:"model"`
			InputPerM      float64 `json:"input_per_m"`
			OutputPerM     float64 `json:"output_per_m"`
			CacheWritePerM float64 `json:"cache_write_per_m"`
			CacheReadPerM  float64 `json:"cache_read_per_m"`
		} `json:"steps"`
```

Inside the step loop (`:1853`), after the provider check, validate and persist:

```go
		if !validRate(st.InputPerM) || !validRate(st.OutputPerM) ||
			!validRate(st.CacheWritePerM) || !validRate(st.CacheReadPerM) {
			writeError(w, http.StatusBadRequest, "invalid step price: rates must be finite and non-negative")
			return
		}
		chain.Steps = append(chain.Steps, store.ChainStep{
			ID: uuid.NewString(), ChainID: chain.ID, Position: i,
			Provider: st.Provider, Model: st.Model, CreatedAt: now,
			InputPerM: st.InputPerM, OutputPerM: st.OutputPerM,
			CacheWritePerM: st.CacheWritePerM, CacheReadPerM: st.CacheReadPerM,
		})
```

- [ ] **Step 5: Decode and validate on update**

Mirror the create change in `adminUpdateChain`'s `Steps` pointer struct (`:1891`) and the replacement loop (`:1916-1929`), using the same `validRate` guard before assigning each `store.ChainStep`.

- [ ] **Step 6: Return rates on list**

In `adminListChains` (`:1793`):

```go
			steps = append(steps, map[string]any{
				"provider": st.Provider, "model": st.Model, "position": st.Position,
				"input_per_m": st.InputPerM, "output_per_m": st.OutputPerM,
				"cache_write_per_m": st.CacheWritePerM, "cache_read_per_m": st.CacheReadPerM,
			})
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/gateway/ -run 'TestAdminChainPricing|TestValidateChainName' -v`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/gateway/admin.go backend/internal/gateway/admin_chain_pricing_test.go
git commit -m "feat(gateway): accept and validate per-step chain pricing"
```

---

### Task 5: Frontend — API types + derivation helper

**Files:**
- Modify: `frontend/src/lib/api.ts:315-319` (`ChainStep`), `:1342-1345` (create/update signatures)
- Modify: `frontend/src/components/chains/chainUtils.ts:7-11`, `:46-53`
- Create: `frontend/src/components/chains/chainUtils.test.ts`

**Interfaces:**
- Consumes: admin JSON shape from Task 4.
- Produces: `ChainStep.input_per_m/output_per_m/cache_write_per_m/cache_read_per_m: number`; `DraftChainStep` rate fields; `deriveCacheRates(input, prev)` and `CACHE_WRITE_FACTOR`/`CACHE_READ_FACTOR` exports.

- [ ] **Step 1: Write the failing test**

Create `frontend/src/components/chains/chainUtils.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { deriveCacheRates } from "./chainUtils";

describe("deriveCacheRates", () => {
  it("derives write 1.25x and read 0.1x from input", () => {
    expect(deriveCacheRates(2, { cacheWritePerM: 0, cacheReadPerM: 0 }))
      .toEqual({ cacheWritePerM: 2.5, cacheReadPerM: 0.2 });
  });

  it("preserves a manual override that differs from the old derivation", () => {
    expect(deriveCacheRates(4, { cacheWritePerM: 9, cacheReadPerM: 0.2, prevInput: 2 }))
      .toEqual({ cacheWritePerM: 9, cacheReadPerM: 0.4 });
  });

  it("re-derives a field that still equals the old formula", () => {
    expect(deriveCacheRates(4, { cacheWritePerM: 2.5, cacheReadPerM: 0.2, prevInput: 2 }))
      .toEqual({ cacheWritePerM: 5, cacheReadPerM: 0.4 });
  });
});
```

(If the repo has no `vitest`, check `frontend/package.json` first; use whatever test runner the repo already uses. If none exists, make this a plain `assert`-based module evaluated in an existing node check rather than adding a dependency.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npx vitest run src/components/chains/chainUtils.test.ts` (adjust to the repo's runner)
Expected: FAIL — `deriveCacheRates` not exported.

- [ ] **Step 3: Add the helper**

In `frontend/src/components/chains/chainUtils.ts`:

```ts
export const CACHE_WRITE_FACTOR = 1.25;
export const CACHE_READ_FACTOR = 0.1;

// deriveCacheRates recomputes cache write/read from the input rate, but keeps a
// field the operator edited by hand. A field is "auto" when it still equals the
// formula result for the previous input; a manual override is left untouched.
export const deriveCacheRates = (
  input: number,
  prev: { cacheWritePerM: number; cacheReadPerM: number; prevInput?: number },
): { cacheWritePerM: number; cacheReadPerM: number } => {
  const oldInput = prev.prevInput ?? input;
  const autoWrite = prev.cacheWritePerM === +(oldInput * CACHE_WRITE_FACTOR).toFixed(8);
  const autoRead = prev.cacheReadPerM === +(oldInput * CACHE_READ_FACTOR).toFixed(8);
  return {
    cacheWritePerM: autoWrite ? +(input * CACHE_WRITE_FACTOR).toFixed(8) : prev.cacheWritePerM,
    cacheReadPerM: autoRead ? +(input * CACHE_READ_FACTOR).toFixed(8) : prev.cacheReadPerM,
  };
};
```

- [ ] **Step 4: Extend the draft + API types**

`chainUtils.ts` `DraftChainStep`:

```ts
export interface DraftChainStep {
  id: string;
  provider: string;
  model: string;
  inputPerM: number;
  outputPerM: number;
  cacheWritePerM: number;
  cacheReadPerM: number;
}
```

`makeDraftStep` seeds rates from the incoming step (default 0):

```ts
export const makeDraftStep = (step?: { provider: string; model: string; input_per_m?: number; output_per_m?: number; cache_write_per_m?: number; cache_read_per_m?: number }): DraftChainStep => ({
  id: crypto.randomUUID(),
  provider: step?.provider ?? "",
  model: step?.model ?? "",
  inputPerM: step?.input_per_m ?? 0,
  outputPerM: step?.output_per_m ?? 0,
  cacheWritePerM: step?.cache_write_per_m ?? 0,
  cacheReadPerM: step?.cache_read_per_m ?? 0,
});
```

`api.ts` `ChainStep` gains the four `number` fields, and the create/update step types become:

```ts
{ provider: string; model: string; input_per_m: number; output_per_m: number; cache_write_per_m: number; cache_read_per_m: number }
```

- [ ] **Step 5: Run test + typecheck**

Run: `cd frontend && npx vitest run src/components/chains/chainUtils.test.ts && npm run typecheck`
Expected: test PASS; typecheck may still FAIL on `ChainEditor.tsx` (fixed in Task 6).

- [ ] **Step 6: Commit**

```bash
git add frontend/src/lib/api.ts frontend/src/components/chains/chainUtils.ts frontend/src/components/chains/chainUtils.test.ts
git commit -m "feat(ui): chain step price types and cache derivation"
```

---

### Task 6: Frontend — Chain editor pricing fields

**Files:**
- Modify: `frontend/src/pages/ChainEditor.tsx:35`, `:50-66`, `:78`, `:93`
- Modify: `frontend/src/components/chains/ChainModelPicker.tsx` (only if a callback is needed; otherwise keep picker untouched)

**Interfaces:**
- Consumes: `DraftChainStep` rate fields and `deriveCacheRates` (Task 5).
- Produces: editor sends the four rates on save and blocks save when any complete step has all-zero rates.

- [ ] **Step 1: Wire rate editing**

In `ChainEditorPage`, add a step updater that recomputes cache fields (calling `deriveCacheRates`) when `inputPerM` changes, and extend `validationMessage` with a "needs pricing" branch:

```ts
const updateStep = (stepID: string, next: Partial<DraftChainStep>) => {
  setSteps((current) => current.map((step) =>
    step.id === stepID ? { ...step, ...next } : step));
  setDirty(true);
};
```

```ts
const needsPricing = completeSteps.some((step) =>
  step.inputPerM <= 0 && step.outputPerM <= 0 && step.cacheWritePerM <= 0 && step.cacheReadPerM <= 0);
const validationMessage =
  ... existing checks ...
  : needsPricing ? "Set input and output price for every route step."
  : ...;
```

- [ ] **Step 2: Render the four inputs**

Add a collapsible pricing row under each step (four `<Input type="number" min={0} step="0.01">`), with Cache write/read inputs labelled as auto-derived and an onChange for Input that applies `deriveCacheRates`. Follow the existing `CustomModelsSection` money-input pattern.

- [ ] **Step 3: Save the rates**

Update the `saveMutation` payload step mapping (`:65`) and the `routeChain` mapping (`:61`) to include the four rates:

```ts
steps: completeSteps.map((step) => ({
  provider: step.provider, model: step.model,
  input_per_m: step.inputPerM, output_per_m: step.outputPerM,
  cache_write_per_m: step.cacheWritePerM, cache_read_per_m: step.cacheReadPerM,
})),
```

- [ ] **Step 4: Typecheck + build**

Run: `cd frontend && npm run typecheck && npm run build`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/ChainEditor.tsx
git commit -m "feat(ui): per-step chain pricing controls"
```

---

### Task 7: Full verification

**Files:** none (verification only).

- [ ] **Step 1: Backend build + tests**

Run: `cd backend && go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 2: Frontend typecheck + build**

Run: `cd frontend && npm run typecheck && npm run build`
Expected: PASS.

- [ ] **Step 3: Live E2E against the local container (optional but preferred)**

Rebuild `docker compose -f compose.localhost.yaml up -d --build`, create a priced chain via the dashboard API, send a chat request through `chain:<name>`, and confirm the `usage_records` row for that request has `pricing_source='chain'` and the configured rates. Report the observed values.

- [ ] **Step 4: Documentation consistency**

Check `docs/superpowers/specs/2026-09-28-chain-model-pricing-design.md` against the implementation; update any drift. Confirm no README/architecture doc states that chain pricing is catalog-only.

---

## Self-Review

**Spec coverage:** data model → Task 1; dispatch flow → Task 2; cost → Task 2; pipeline wiring → Task 3; admin API validation → Task 4; override/derivation → Task 5; UI → Task 6; testing/verification → Tasks 1-7. Final fallback stays catalog (no task adds rates to it). All-zero = unpriced limitation implemented by `chainPrice` in Task 2.

**Placeholder scan:** Task 3 Step 1 and Task 4 Step 1 are described at sketch level because the exact harness wiring depends on the existing pipeline/gateway test fixtures; both name the existing files to copy from. The implementer must replace the sketch with real code before running. All other tasks contain complete code.

**Type consistency:** `input_per_m`/`output_per_m`/`cache_write_per_m`/`cache_read_per_m` (JSON + DB) map to Go `InputPerM`/`OutputPerM`/`CacheWritePerM`/`CacheReadPerM` and TS `inputPerM`/`outputPerM`/`cacheWritePerM`/`cacheReadPerM` consistently across tasks. `Price.CachedInputPerM` is the cache-read slot (existing name, deliberate).
