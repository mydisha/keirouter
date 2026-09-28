# Chain model pricing (per-step price override) — Design

Date: 2026-09-28
Branch: feat/chains-model-pricing

## Problem

A routing chain is a named, ordered list of `(provider, model)` steps
(`store.ChainStep`, `models.go:118`). Today the cost of a request served through a
chain is computed from the **built-in catalog** price of whichever step's
provider/model won (`meter.CalculateCost`, `pricing.go:265`). The operator cannot
say "a request through this chain costs X" — the price is always the catalog's.

The operator wants to set the price used when a model is served inside a chain,
per step: Input, Output, Cache write, Cache read. When a client sends a request
that resolves to a chain, the cost must follow the price configured on the chain
step that actually served the request.

## Goal

On each chain step, the operator can set four per-million-token rates:

- Input
- Output
- Cache write
- Cache read

When such a price is set, it is the price used for cost accounting of requests
served by that chain step. Cache write and cache read are auto-derived from Input
on input (write = 1.25x, read = 0.1x) but remain user-overridable.

Non-goals:

- No change to the public landing / `provider_id=combo` listing behavior.
- No change to provider-account handling, dispatch strategy selection, or
  fallback ordering.
- No retroactive re-pricing of historical `usage_records` rows.
- No new pricing UI outside the chain editor.
- The chain's optional **final fallback** target keeps catalog pricing (it is not
  part of the priced step list).

## Decisions

1. **Price lives on the chain step and flows through dispatch to the meter.**
   Four rate columns are added to `chain_steps`. `store.ChainStep` carries them,
   `dispatch.TargetsFromChain` copies them onto `dispatch.Target`, the pipeline
   copies the winning target's rates into `meter.Event`, and `Meter.Record`
   passes them to `CalculateCost`. This keeps prices always-fresh (chains are read
   per-request by `resolveTargets`, `resolve.go:99`) with no reload hook.

2. **A chain-step price overrides the catalog for that request, and only that
   request.** When a step's rates are present (non-zero), they are used verbatim;
   `PricingSource` is recorded as `chain`. When absent, the existing catalog
   resolution runs unchanged, so legacy chains keep working.

3. **Money precision follows the existing custom-model convention.** Rates are
   `float64`, stored as `REAL` in SQLite and converted to `DOUBLE PRECISION` for
   Postgres in the existing `0026`-style Postgres migration. Final cost stays in
   nanodollars via `tokenCostNanos` (`pricing.go:226`) with `math.Round`, never
   float accumulation.

4. **Cache derivation is UI-side and override is value-detected.** The backend
   stores the four final rates. The editor derives cache write/read from Input and
   only recomputes an auto-derived field when it still equals the previous
   formula result; a manually edited value is preserved. No boolean flag column.

5. **A step must supply all four rates to be saved.** Chain create/update
   validates that every step supplies four finite, non-negative rates. This makes
   "step carries a price" a guaranteed invariant for new writes. Legacy chains
   (saved before this feature) keep catalog pricing until the operator re-saves
   them; the editor surfaces this as "needs pricing".

   To keep the runtime override decision unambiguous without an extra flag
   column, the override applies when **at least one** of the four rates is `> 0`;
   an all-zero step falls back to catalog — exactly like a legacy row. An
   explicitly all-zero (free) step is therefore indistinguishable from "unpriced"
   and uses catalog pricing; this is an accepted limitation, documented below.

## Architecture

### Data model

`chain_steps` gains four columns (migration `0031_chain_step_pricing.sql` +
Postgres counterpart):

```
input_per_m        REAL NOT NULL DEFAULT 0
output_per_m       REAL NOT NULL DEFAULT 0
cache_write_per_m  REAL NOT NULL DEFAULT 0
cache_read_per_m   REAL NOT NULL DEFAULT 0
```

`store.ChainStep` gains `InputPerM`, `OutputPerM`, `CacheWritePerM`,
`CacheReadPerM float64`.
`store.ChainCreate`/`ChainUpdate` step inputs gain the same four fields.

### Dispatch

`dispatch.Target` gains the four rates. `TargetsFromChain` (`dispatch.go:1021`)
copies them. Rates are inert for ordering except when helpful; provider/model
remain the routing identity. Round-robin rotation already permutes `Target`
values, so rates travel with their target automatically.

### Cost

`meter.Event` gains the four step rates plus a flag/marker indicating they came
from a chain. In `Meter.Record` (`meter.go:206`), when chain rates are present,
cost is computed from a `Price{InputPerM, OutputPerM, CacheWritePerM,
CachedInputPerM, ReasoningPerM=OutputPerM, Source:"chain"}` instead of
`ResolvePrice`. The existing snapshot fields (`InputRatePerM`, …,
`PricingSource`) record what was charged. Cache read maps to the existing
`CachedInputPerM` slot; cache write to `CacheWritePerM`.

### API (admin)

`adminCreateChain` / `adminUpdateChain` (`admin.go:1807`, `1878`) decode four
rates per step, validate finite/non-negative, persist them. `adminListChains`
(`admin.go:1783`) returns them on each step. Reject with 400 on missing/invalid
step price.

### Frontend

`ChainEditor.tsx` step rows gain an expandable pricing section with four numeric
inputs. Typing Input recomputes Cache write/read unless the operator has edited
them. `chainUtils.ts` gains the draft shape fields and the derivation helper;
`api.ts` `ChainStep` gains the four fields. Save includes them.

## Data flow

```
client model "chain:name"
  → resolveTargets → chainResult → TargetsFromChain (rates attached)
  → dispatch.Plan / rotation (rates travel with Target)
  → winning Attempt.Target
  → pipeline.recordOutcomeWithTTFT → meter.Event{ChainID, target rates}
  → Meter.Record → CalculateCost(chain rates if present else catalog)
  → usage_records snapshot (rates + costs)
```

## Error handling

- Invalid/negative/non-finite step price → HTTP 400, chain not saved.
- Missing step price on create/update → HTTP 400 with a clear message.
- Legacy chain with zero rates → routes normally, priced from catalog.
- An all-zero step price is treated as "not set" and prices from catalog (see
  decision 5) — the override only engages when some rate is `> 0`.

## Testing

- Store: migration applies; create/update/get/list round-trip the four rates on
  SQLite, and the same on Postgres (extend `postgres_integration_test.go`).
- Meter: `CalculateCost` with chain rates prices from them and records
  `PricingSource="chain"`; absent rates still use catalog; nanodollar math is
  exact for known token/rate combinations.
- Gateway: create/update rejects a step with missing/negative rate; list returns
  rates.
- Frontend: derivation helper unit test (auto-fill, override preserved); tsc.

## Remaining unknowns

- Whether `dispatch.Target` gaining fields breaks any equality/map keying that
  assumes it is comparable — to confirm during implementation (`accountRotationKey`
  uses provider/model only, `dispatch.go:870`).
- Exact UI placement/wording to be settled by the implementation plan.
