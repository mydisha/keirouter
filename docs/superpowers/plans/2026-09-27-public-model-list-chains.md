# Public Model List Shows Chains Only — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the public landing list (`/v1/public/models` + `/v1/public/overview`) advertise only the operator-defined routing chains, one entry per chain, with exact per-chain usage.

**Architecture:** Add a `chain_id` column to `usage_records`, propagate `meta.ChainID` (already set at the gateway edge) through the meter's single terminal persistence path, then rewrite `publicModelRows` to iterate chains instead of the model catalogue.

**Tech Stack:** Go 1.26, SQLite + Postgres (portable SQL), chi router, testify.

## Global Constraints

- Go commands MUST run from `/home/emalution/keirouter/backend` (repo-root `go build ./...` fails under `go.work`).
- Migrations are single-file, dialect-neutral where possible; `ALTER TABLE ... ADD COLUMN ... NOT NULL DEFAULT ''` is portable. Runner tolerates "column already exists".
- Do NOT change `/v1/models`, `/v1/models/{kind}`, `/v1/models/info`, pricing tables, dispatch, or the admin UI.
- Public payload shapes stay identical (`publicApi.ts` unchanged).
- Empty-step chains are skipped. Legacy rows (`chain_id=''`) are never attributed. No backfill.
- Run `gofmt` before every commit.

---

### Task 1: Add `chain_id` to `usage_records` (migration + store model)

**Files:**
- Create: `backend/internal/store/migrations/0030_usage_chain_id.sql`
- Modify: `backend/internal/store/models.go` (UsageRecord struct, ~line 130-160)
- Modify: `backend/internal/store/repo_usage.go:65-132` (`usageColumns`, `usageArgs`)
- Test: `backend/internal/store/repo_usage_chain_test.go`

**Interfaces:**
- Produces: `UsageRecord.ChainID string`; DB column `usage_records.chain_id TEXT NOT NULL DEFAULT ''`.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/store/repo_usage_chain_test.go`:

```go
package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUsageRecordPersistsChainID(t *testing.T) {
	db := newTestDB(t) // existing helper in store_test.go
	ctx := context.Background()

	require.NoError(t, db.Usage().Record(ctx, UsageRecord{
		ID: "u1", TenantID: DefaultTenantID, Provider: "openai", Model: "gpt-4o",
		ChainID: "chain-1", Status: "success", CreatedAt: time.Now().UTC(),
	}))

	var got string
	q := db.rebind(`SELECT chain_id FROM usage_records WHERE id = ?`)
	require.NoError(t, db.sql.QueryRowContext(ctx, q, "u1").Scan(&got))
	require.Equal(t, "chain-1", got)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestUsageRecordPersistsChainID -v`
Expected: FAIL — `unknown field ChainID` / `no such column: chain_id`.

- [ ] **Step 3: Add the migration**

Create `backend/internal/store/migrations/0030_usage_chain_id.sql`:

```sql
-- Attribute usage to the routing chain that served it (empty = direct target).
ALTER TABLE usage_records ADD COLUMN chain_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_usage_tenant_chain ON usage_records(tenant_id, chain_id);
```

- [ ] **Step 4: Add the struct field and wire the columns**

In `backend/internal/store/models.go`, add to `UsageRecord` (near `TenantID`/`ProjectID`):

```go
	ChainID   string
```

In `repo_usage.go`:
- Append `chain_id` to `usageColumns` (before `created_at`, matching arg order).
- Add `u.ChainID` to `usageArgs` at the same position.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestUsageRecordPersistsChainID -v`
Expected: PASS.

- [ ] **Step 6: Run the full store package**

Run: `go test ./internal/store/`
Expected: PASS (no other INSERT queries broke).

- [ ] **Step 7: Commit**

```bash
gofmt -w internal/store/models.go internal/store/repo_usage.go internal/store/repo_usage_chain_test.go
git add internal/store/
git commit -m "feat(store): add chain_id to usage_records"
```

---

### Task 2: Propagate `chain_id` through the meter and pipeline

**Files:**
- Modify: `backend/internal/meter/meter.go` (Event struct ~112-138; Record ~216-240)
- Modify: `backend/internal/pipeline/pipeline.go:1664` (`recordOutcomeWithTTFT`)
- Test: `backend/internal/meter/meter_chain_test.go`

**Interfaces:**
- Consumes: `UsageRecord.ChainID` from Task 1.
- Produces: `meter.Event.ChainID string`; usage rows recorded by the pipeline carry `meta.ChainID`.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/meter/meter_chain_test.go` (mirror an existing meter test's DB construction; adjust the recording sink if the meter takes a repo interface):

```go
package meter

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// captureStore records the last UsageRecord the meter persisted.
type captureStore struct{ last store.UsageRecord }

func (c *captureStore) Record(_ context.Context, u store.UsageRecord) error {
	c.last = u
	return nil
}
func (c *captureStore) RecordBatch(_ context.Context, rs []store.UsageRecord) error {
	if len(rs) > 0 {
		c.last = rs[len(rs)-1]
	}
	return nil
}

func TestRecordPersistsChainID(t *testing.T) {
	cap := &captureStore{}
	m := New(cap, nil, nil)
	_, err := m.Record(context.Background(), Event{
		TenantID: store.DefaultTenantID, Provider: "openai", Model: "gpt-4o",
		ChainID: "chain-9", Status: "success",
		Usage: core.Usage{PromptTokens: 1, CompletionTokens: 1, Source: core.UsageSourceProvider},
	})
	require.NoError(t, err)
	require.Equal(t, "chain-9", cap.last.ChainID)
}
```

This needs no DB and no forward dependency on Task 3. The `UsageStore` interface (`meter.go:44`) is already the exact surface.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/meter/ -run TestRecordPersistsChainID -v`
Expected: FAIL — `unknown field ChainID`.

- [ ] **Step 3: Add `ChainID` to `meter.Event` and copy it**

In `meter.go`: add `ChainID string` to `Event`; in `Record`, set `ChainID: ev.ChainID` in the `store.UsageRecord{...}` literal.

- [ ] **Step 4: Set `ChainID` from metadata in the pipeline**

In `pipeline.go`, inside `recordOutcomeWithTTFT` (the `ev := meter.Event{...}` literal at ~1664), add:

```go
		ChainID:         meta.ChainID,
```

- [ ] **Step 5: Run the meter test**

Run: `go test ./internal/meter/ -run TestRecordPersistsChainID -v`
Expected: PASS.

- [ ] **Step 6: Run affected packages**

Run: `go test ./internal/meter/ ./internal/pipeline/ ./internal/gateway/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
gofmt -w internal/meter/meter.go internal/meter/meter_chain_test.go internal/pipeline/pipeline.go
git add internal/meter/ internal/pipeline/
git commit -m "feat(meter): persist chain_id from request metadata"
```

---

### Task 3: `ChainUsageAccurate` repo method

**Files:**
- Modify: `backend/internal/store/repo_usage_accuracy.go` (append)
- Test: `backend/internal/store/repo_usage_chain_test.go` (add)

**Interfaces:**
- Consumes: `chain_id` column from Task 1.
- Produces:

```go
type AccurateChainUsage struct {
	ChainID          string
	TotalRequests    int64
	PromptTokens     int64
	CompletionTokens int64
	DistinctKeys     int
}
func (r *UsageRepo) ChainUsageAccurate(ctx context.Context, tenantID string, since time.Time) (map[string]AccurateChainUsage, error)
```

- [ ] **Step 1: Write the failing test**

Add to `repo_usage_chain_test.go`:

```go
func TestChainUsageAccurateGroupsAndSkipsEmpty(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	require.NoError(t, db.Usage().RecordBatch(ctx, []UsageRecord{
		{ID: "a", TenantID: DefaultTenantID, APIKeyID: "k1", Provider: "openai", Model: "gpt-4o", ChainID: "c1", Status: "success", PromptTokens: 100, CompletionTokens: 20, CreatedAt: now},
		{ID: "b", TenantID: DefaultTenantID, APIKeyID: "k2", Provider: "openai", Model: "gpt-4o", ChainID: "c1", Status: "success", PromptTokens: 100, CompletionTokens: 20, CreatedAt: now},
		{ID: "d", TenantID: DefaultTenantID, APIKeyID: "k1", Provider: "openai", Model: "gpt-4o", ChainID: "", Status: "success", PromptTokens: 5, CompletionTokens: 5, CreatedAt: now},
	}))

	got, err := db.Usage().ChainUsageAccurate(ctx, DefaultTenantID, time.Time{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	c1 := got["c1"]
	require.Equal(t, int64(2), c1.TotalRequests)
	require.Equal(t, int64(200), c1.PromptTokens)
	require.Equal(t, int64(40), c1.CompletionTokens)
	require.Equal(t, 2, c1.DistinctKeys)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestChainUsageAccurateGroupsAndSkipsEmpty -v`
Expected: FAIL — `ChainUsageAccurate undefined`.

- [ ] **Step 3: Implement the method**

Append to `repo_usage_accuracy.go`:

```go
// AccurateChainUsage is per-chain aggregate usage. Only rows with a non-empty
// chain_id are counted; direct provider/model requests are excluded.
type AccurateChainUsage struct {
	ChainID          string
	TotalRequests    int64
	PromptTokens     int64
	CompletionTokens int64
	DistinctKeys     int
}

// ChainUsageAccurate aggregates usage by routing chain since the given time.
// Returns a map keyed by chain id. Public-safe: counts only, no identities.
func (r *UsageRepo) ChainUsageAccurate(ctx context.Context, tenantID string, since time.Time) (map[string]AccurateChainUsage, error) {
	q := r.db.rebind(`SELECT chain_id, COUNT(*),
			COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0),
			COUNT(DISTINCT api_key_id)
		FROM usage_records
		WHERE tenant_id=? AND created_at>=? AND chain_id<>''
		GROUP BY chain_id`)
	rows, err := r.db.sql.QueryContext(ctx, q, tenantID, formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("store: chain usage: %w", err)
	}
	defer rows.Close()
	out := map[string]AccurateChainUsage{}
	for rows.Next() {
		var u AccurateChainUsage
		if err := rows.Scan(&u.ChainID, &u.TotalRequests, &u.PromptTokens, &u.CompletionTokens, &u.DistinctKeys); err != nil {
			return nil, err
		}
		out[u.ChainID] = u
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestChainUsageAccurateGroupsAndSkipsEmpty -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/store/repo_usage_accuracy.go internal/store/repo_usage_chain_test.go
git add internal/store/
git commit -m "feat(store): ChainUsageAccurate per-chain aggregate"
```

---

### Task 4: Rewrite `publicModelRows` chain-driven

**Files:**
- Modify: `backend/internal/gateway/public.go:42-147`
- Test: `backend/internal/gateway/public_handlers_test.go` (add cases)

**Interfaces:**
- Consumes: `ChainUsageAccurate` (Task 3), `db.Chains()` (`ChainRepo.ListByTenant`), `connectors.ModelPriceByProviderModel`, `capabilityPayload`.
- Produces: `/v1/public/models` and `model_count` list chains only.

- [ ] **Step 1: Write the failing test**

Add to `public_handlers_test.go`:

```go
func TestPublicModelsListsChainsOnly(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	require.NoError(t, db.Chains().Create(ctx, store.Chain{
		ID: "chain-1", TenantID: store.DefaultTenantID, Name: "deepseek-v4.1-flash",
		Strategy: "priority",
		Steps:    []store.ChainStep{{Provider: "openai", Model: "gpt-4o", Position: 0}},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	rec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/public/models", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var payload struct {
		Models []struct {
			Name       string `json:"name"`
			ModelID    string `json:"model_id"`
			ProviderID string `json:"provider_id"`
		} `json:"models"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Models, 1, "only chains are listed")
	require.Equal(t, "deepseek-v4.1-flash", payload.Models[0].Name)
	require.Equal(t, "combo", payload.Models[0].ProviderID)

	// Catalog ids must not leak in.
	require.NotContains(t, rec.Body.String(), "openai/gpt-4o")
}

func TestPublicModelsSkipsEmptyStepChain(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	require.NoError(t, db.Chains().Create(context.Background(), store.Chain{
		ID: "chain-empty", TenantID: store.DefaultTenantID, Name: "empty",
		Strategy: "priority", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))
	rec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/public/models", nil))
	var payload struct {
		Models []json.RawMessage `json:"models"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Empty(t, payload.Models)
}

func TestPublicModelsAttributesChainUsage(t *testing.T) {
	db, gw := newPublicTestGatewayWithDB(t)
	ctx := context.Background()
	require.NoError(t, db.Chains().Create(ctx, store.Chain{
		ID: "c1", TenantID: store.DefaultTenantID, Name: "combo-a", Strategy: "priority",
		Steps:    []store.ChainStep{{Provider: "openai", Model: "gpt-4o", Position: 0}},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))
	require.NoError(t, db.Usage().RecordBatch(ctx, []store.UsageRecord{
		{ID: "r1", TenantID: store.DefaultTenantID, APIKeyID: "k1", Provider: "openai", Model: "gpt-4o", ChainID: "c1", Status: "success", PromptTokens: 100, CompletionTokens: 20, CreatedAt: time.Now().UTC()},
		{ID: "r2", TenantID: store.DefaultTenantID, APIKeyID: "k1", Provider: "openai", Model: "gpt-4o", ChainID: "", Status: "success", PromptTokens: 500, CompletionTokens: 500, CreatedAt: time.Now().UTC()},
	}))

	rec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/public/models", nil))
	var payload struct {
		Models []struct {
			ModelID string `json:"model_id"`
			Usage   struct {
				Requests int64 `json:"requests"`
				Tokens   int64 `json:"tokens"`
			} `json:"usage"`
		} `json:"models"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Models, 1)
	require.Equal(t, int64(1), payload.Models[0].Usage.Requests, "direct-target row must not count")
	require.Equal(t, int64(120), payload.Models[0].Usage.Tokens)
}
```

Also update the two existing tests that assume a catalogue:
- `TestPublicOverviewEmptyDBIsZeroed`: change `require.Positive(t, overview.ModelCount)` to `require.Zero(t, overview.ModelCount)` (no chains seeded).
- `TestPublicModelsEmptyDBListsCatalogue`: rename intent to chains — with no chains seeded, `payload.Models` must be empty. Change `require.NotEmpty` to `require.Empty`.
- `TestPublicModelsHidesProviderSecrets`: seed a chain and set `ChainID` on the records so the entry still appears; keep the secret-absence assertions.
- `TestPublicOverviewAllTimeAndModelCount`: `model_count` equals number of seeded chains (seed one) rather than non-empty catalogue.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway/ -run 'TestPublicModels' -v`
Expected: FAIL — catalog models still listed / `ChainUsageAccurate` unused.

- [ ] **Step 3: Rewrite `publicModelRows`**

Replace the catalogue loop in `public.go` with:

```go
func (s *Server) publicModelRows(ctx context.Context) ([]publicModelRow, error) {
	chains, err := s.chains.ListByTenant(ctx, adminTenant)
	if err != nil {
		return nil, err
	}
	usage, err := s.usage.ChainUsageAccurate(ctx, adminTenant, time.Time{})
	if err != nil {
		return nil, err
	}
	rows := make([]publicModelRow, 0, len(chains))
	for _, c := range chains {
		if len(c.Steps) == 0 {
			continue
		}
		first := c.Steps[0]
		price, _ := connectors.ModelPriceByProviderModel(first.Provider, first.Model)
		u := usage[c.ID]
		rows = append(rows, publicModelRow{
			Name:       c.Name,
			ModelID:    c.Name,
			Provider:   "combo",
			ProviderID: "combo",
			InputPerM:  price.InputPerM,
			OutputPerM: price.OutputPerM,
			CachedPerM: price.CachedInputPerM,
			CacheWrite: price.CacheWritePerM,
			Requests:   u.TotalRequests,
			Tokens:     u.PromptTokens + u.CompletionTokens,
			Users:      u.DistinctKeys,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Requests != rows[j].Requests {
			return rows[i].Requests > rows[j].Requests
		}
		return rows[i].Name < rows[j].Name
	})
	return rows, nil
}
```

Notes:
- `capabilityPayload(m.ProviderID, m.ModelID, core.ServiceLLM)` in `publicModels` currently receives `"combo"`/chain-name; keep the call (it returns an empty capability set for an unknown id — same as untracked custom models today). Do NOT special-case it.
- `s.chains` is always wired (`server.go:54`, `Deps.Chains`). In `newPublicTestGatewayWithDB`, add `Chains: db.Chains()` to the `New(Deps{...})` call so the test gateway has it.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/gateway/ -run 'TestPublic' -v`
Expected: PASS.

- [ ] **Step 5: Run the full gateway package**

Run: `go test ./internal/gateway/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/gateway/public.go internal/gateway/public_handlers_test.go
git add internal/gateway/
git commit -m "feat(public): list routing chains only on public landing API"
```

---

### Task 5: Full verification + live E2E

**Files:** none modified.

- [ ] **Step 1: Build and test everything**

Run (from `backend/`): `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 2: Frontend typecheck (payload unchanged, but confirm)**

Run (from `frontend/`): `npm run typecheck`
Expected: PASS.

- [ ] **Step 3: Rebuild + recreate the container**

Run (from repo root): `docker compose -f compose.localhost.yaml up -d --build`
Expected: image built, `keirouter-localhost-keirouter-1` recreated/started.

- [ ] **Step 4: Live E2E**

```bash
curl -s http://127.0.0.1:20180/v1/public/models | python3 -m json.tool
curl -s http://127.0.0.1:20180/v1/public/overview
```
Expected: `/v1/public/models` returns exactly one entry named `deepseek-v4.1-flash`
(provider_id `combo`); `model_count == 1`. No catalog model ids.

- [ ] **Step 5: Update the stale landing spec**

In `docs/superpowers/specs/2026-09-26-public-landing-page-design.md`, replace the
"Catalog = actively-used models only." line (and the `GET /v1/public/models`
bullet) with: "Catalog = operator-defined routing chains only; each entry is one
chain (provider `combo`)." Commit with `-f` (docs/ is gitignored):

```bash
git add -f docs/superpowers/specs/2026-09-26-public-landing-page-design.md
git commit -m "docs: public catalog is chains-only"
```

---

## Self-Review

- **Spec coverage:** entry=chain (T4), price=first step (T4), usage exact per chain (T1-T4), legacy skipped (T3 `chain_id<>''`), empty-step skipped (T4), model_count=chain count (T4), no `/v1/models` change (constraints), doc update (T5). Covered.
- **Placeholder scan:** each code step has full code. T2 test references "as the other meter tests do" — intentional because the meter test harness must be reused verbatim; the implementer must open `meter_test.go` for the exact sink.
- **Type consistency:** `ChainUsageAccurate` field names used in T4 (`TotalRequests`, `PromptTokens`, `CompletionTokens`, `DistinctKeys`) match T3. `UsageRecord.ChainID` consistent T1→T2→T3→T4.
- **Known test churn (T4 Step 1):** four existing public tests encode the old catalogue behavior and must be updated; called out explicitly so they are not "fixed" by weakening assertions.
