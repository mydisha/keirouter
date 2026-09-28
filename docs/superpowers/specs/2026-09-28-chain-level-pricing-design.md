# Chain-Level Model Pricing — Design

Date: 2026-09-28

## Problem

The just-merged per-step chain pricing feature puts prices on each
`chain_steps` row. That is the wrong level. A chain is the *model* the user
calls (`chain:<name>` / bare name). Its steps are only routing candidates —
fallback, round-robin, latency, cost ordered. The user sees one model and
expects one price, independent of which internal route happens to serve the
request.

Pricing must therefore live on the **chain**, not on the step.

## Decisions (approved)

1. **Chain-level only.** One set of four rates per chain. Steps become pure
   routing (provider + model).
2. **Remove the per-step pricing columns and code** from the previous change.
3. **Blank/zero chain price falls back to the catalog price** of the model
   that actually served the request (current behavior when unset).
4. **Discard existing per-step price data.** Migration drops the step columns;
   any prices saved on steps are not carried up.
5. **Step rate fields are removed from the API payload.** Steps carry only
   `provider` and `model`; unknown fields continue to be rejected.

## Data model

`store.Chain` gains four fields:

```go
InputPerM      float64
OutputPerM     float64
CacheWritePerM float64
CacheReadPerM  float64
```

`store.ChainStep` loses `InputPerM`, `OutputPerM`, `CacheWritePerM`,
`CacheReadPerM`.

All-zero on the chain means "unpriced" → catalog fallback. This is the same
accepted limitation as before: `0/0/0/0` cannot be distinguished from an
explicit free price.

### Migration `0032_chain_pricing` (two files, dialect split)

`0032_chain_pricing.sqlite.sql`:

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

`0032_chain_pricing.postgres.sql`: identical but `DOUBLE PRECISION`.

The dropped SQLite columns are plain, un-indexed, not part of any constraint,
so `DROP COLUMN` is valid in modernc.org/sqlite v1.34.x.

## Data flow

`dispatch.TargetsFromChain(chain)` already flattens a chain into `[]Target`.
Change it to copy the **chain's** rates onto **every** emitted target:

```go
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

Everything downstream already works and is unchanged:
`Target → dispatch.Attempt.Target → meter.Event → chainPrice() →
calculateCostFromPrice`. Mixing chain pricing with mixed steps is therefore
automatic: whichever step wins, the same chain rate is applied.

No change needed in `meter`, `pipeline`, or `dispatch` selection logic.

## API (`gateway/admin.go`)

- `adminCreateChain` / `adminUpdateChain`: the 4 rates move from the per-step
  struct to the top-level chain body.
- Validation is unchanged in spirit: if **any** rate is set (`> 0`), then
  `input_per_m` AND `output_per_m` must both be `> 0`; otherwise HTTP 400.
  A fully-zero set means "unpriced" (catalog fallback). Cache rates may be 0
  even when input/output are set.
- The per-step struct no longer accepts rate fields. Because `decodeJSON`
  uses `DisallowUnknownFields`, a step payload containing the old rate fields
  is rejected with HTTP 400 — an explicit breaking change.
- `adminListChains`: each chain returns the 4 rates; steps no longer do.
- Backup export / import (`admin.go` ~2800/2955): the 4 rates are written and
  read on the chain object, not per step.

`validRate` (finite, non-negative) stays as-is.

## UI (`ChainEditor.tsx`, `chainUtils.ts`, `api.ts`, `chainUtils.test.ts`)

- The pricing row moves out of each step and onto the chain form: one
  Input / Output / Cache-write / Cache-read group for the whole chain.
- `api.ts` `Chain` type gains the 4 rates; `ChainStep` loses them; create and
  update payload types follow.
- `chainUtils.ts` `deriveCacheRates`, `CACHE_WRITE_FACTOR`, `CACHE_READ_FACTOR`
  stay (same auto-derivation, now applied once at chain level).
- The per-field string-draft input handling and the `(auto)` label move to the
  chain-level control.
- `ChainEditor` save payload sends the 4 rates at the chain level; steps send
  provider + model only.

## Testing

Reuse the existing tests, retargeted to the chain:

- `store/repo_chain_pricing_test.go` + `postgres_integration_test.go`:
  round-trip the 4 rates on `ChainRepo` create/update/list.
- `dispatch/dispatch_chain_pricing_test.go`: `TargetsFromChain` copies the
  chain's rates onto every target.
- `meter/meter_chain_test.go`: cost computed from chain rates; all-four-rate
  split; `PricingSource == "chain"`.
- `pipeline/pipeline_chain_pricing_test.go`: rates reach `meter.Event`.
- `gateway/admin_chain_pricing_test.go`: chain-level create/update validation
  (zero output → 400), `input/output <= 0` rejection, and export/import
  round-trip.
- `frontend` `chainUtils.test.ts` `npm test` unchanged in intent.

## Non-goals

- Per-step or per-route price overrides (explicitly rejected).
- Migrating existing per-step prices upward.
- Changing `orderStepsByCost`, which still orders by catalog price.

## Risks

- Breaking API change for any client sending per-step rates. Accepted; the
  only known client is our own dashboard.
- `DROP COLUMN` on SQLite runs inside a transactional migration; verified on
  both dialects before merge.
