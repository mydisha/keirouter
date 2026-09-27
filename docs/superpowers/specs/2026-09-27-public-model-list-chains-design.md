# Public model list shows chains only — Design

Date: 2026-09-27
Branch: fix/model-list-public

## Problem

The public landing page (`/`) and its API (`GET /v1/public/models`) list **every
priced or used catalog model** — currently 457 entries — plus roll-up totals on
`GET /v1/public/overview`. The operator wants the public surface to advertise
**only the models that are actually offered**, i.e. the routing chains the
operator has deliberately created.

Live example: one chain `deepseek-v4.1-flash` (step target
`custom-openai-9router` / `nuta/deepseek-v4.1-flash`) exists, but the public list
shows the raw step target plus the other 456 catalog models. It should show one
entry: `deepseek-v4.1-flash`.

## Goal

On the public landing + public API, each list entry represents **one routing
chain** (a "combo" virtual model), named by its chain name. Catalog models that
are not referenced as chain steps are not listed.

Non-goals:
- The authenticated `/v1/models`, `/v1/models/{kind}`, `/v1/models/info`
  endpoints keep their current behavior (chains **plus** catalog models).
- No change to pricing tables, dispatch, routing, or the admin UI.
- No chain discovery from usage history.

## Decisions

1. **Entry identity = chain.** Display name is the chain `name`; provider label
   is `combo`, provider id `combo` (mirrors the existing `/v1/models` combo
   convention, `models.go:42-52`).
2. **Price = first step's catalog price.** The first (lowest-position) step's
   resolved `(provider, model)` supplies `ModelPriceByProviderModel`. If the
   model is untracked (e.g. a custom provider), price is `0/0` — same as the
   catalog path does today for such models.
3. **Capabilities = first step's.** `capabilityPayload(firstStepProvider,
   firstStepModel, ServiceLLM)`.
4. **Usage = exact per-chain.** Sum `requests`/`tokens` and count distinct API
   keys over `usage_records` rows whose `chain_id` equals the chain id.
5. **Legacy rows are not attributed.** Pre-migration rows have an empty
   `chain_id`; they are excluded from every chain's count. Chain usage starts
   from zero and accrues from the next request onward. No backfill.
6. **Empty-step chains are skipped.** A chain with no steps cannot resolve
   (`resolve.go:98`), so it is not advertised.
7. **`model_count` = number of listed chains.** `total_requests` /
   `total_tokens` / `success` / `failed` on the overview remain all-time totals
   over all requests; only the model list narrows.

## Data model change

`usage_records` currently stores only the resolved step `provider` + `model`
(`0001_init.sql:87`, `repo_usage.go:65`). A request routed through a chain is
indistinguishable from a direct `provider/model` request. To attribute usage to
chains exactly, add a nullable-by-default `chain_id` column.

Migration `0030_usage_chain_id.sql` (dialect-neutral, single file — `ALTER TABLE
... ADD COLUMN ... NOT NULL DEFAULT ''` is portable across SQLite and Postgres;
the runner tolerates "column already exists", `migrate.go:126-139`):

```sql
ALTER TABLE usage_records ADD COLUMN chain_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_usage_tenant_chain ON usage_records(tenant_id, chain_id);
```

No backfill. Existing rows read `''` (direct target), which is correct per
decision 5.

## Write path

`core.RequestMetadata.ChainID` already exists (`request.go:113-116`) and is set
by the gateway edge for chain-routed requests (`handlers.go:221`,
`gemini.go:81`) from `resolveResult.PlanOpts.ChainID` (`resolve.go:111`). It is
currently used only for guardrail chain-scope lookup.

Propagate it to persistence:

- `store.UsageRecord.ChainID string` (`store/models.go`), included in
  `usageColumns` and `usageArgs`.
- `meter.Event.ChainID string` (`meter/meter.go`), copied onto the
  `store.UsageRecord`.
- `pipeline.recordOutcomeWithTTFT` (`pipeline.go:1650`) — the single terminal
  persistence path — sets `ChainID: meta.ChainID`. Direct targets leave it
  empty.

## Read path

New repo method on `UsageRepo`:

```go
type AccurateChainUsage struct {
    ChainID               string
    TotalRequests         int64
    PromptTokens          int64
    CompletionTokens      int64
    DistinctKeys          int
}
func (r *UsageRepo) ChainUsageAccurate(ctx context.Context, tenantID string, since time.Time) (map[string]AccurateChainUsage, error)
```

`SELECT chain_id, COUNT(*), SUM(prompt_tokens), SUM(completion_tokens),
COUNT(DISTINCT api_key_id) FROM usage_records WHERE tenant_id=? AND
created_at>=? AND chain_id<>'' GROUP BY chain_id`

`publicModelRows` (`public.go:61`) is rewritten:

1. `ListByTenant(ctx, adminTenant)` → chains.
2. `ChainUsageAccurate(ctx, adminTenant, time.Time{})` → per-chain usage.
3. For each chain with `len(Steps) > 0`: first step → price + capabilities;
   build a `publicModelRow{Name: chain.Name, ModelID: chain.Name, Provider:
   "combo", ProviderID: "combo", ...usage...}`.
4. Sort requests desc, then name.

The `connectors.ModelsByKind` / `ModelPriceByProviderModel` catalogue loop and
`ByModelAccurate`/`DistinctKeysPerModel` calls are removed from this function.

`publicModels` and `publicOverview` keep their payload shapes, so the frontend
(`publicApi.ts`, `PublicLanding.tsx`) is unchanged.

## Cache

`publicModelRows` feeds both `public-overview` and `public-models`; both remain
cached via `insightsCache` as today. No cache-key change.

## Tests

Store (`store` package):
- Migration applies and `chain_id` is queryable.
- `UsageRecord` round-trips `chain_id`.
- `ChainUsageAccurate` groups by chain, sums tokens, counts distinct keys, and
  excludes rows with empty `chain_id`.

Gateway (`gateway` package, TDD — failing first):
- `/v1/public/models` with exactly one seeded chain returns exactly that chain
  (name = chain name, `provider_id == "combo"`), and no catalog model ids.
- `model_count` on `/v1/public/overview` equals the chain count.
- Usage recorded with `chain_id` set appears on that chain's entry; usage with
  empty `chain_id` does not.
- A chain with no steps is not listed.

Live E2E (Docker, `keirouter-localhost`):
- With the existing `deepseek-v4.1-flash` chain, `/v1/public/models` returns
  exactly that one entry and `/v1/public/overview.model_count == 1`.

## Migration / rollback

Additive column with a default; safe on SQLite and Postgres. Rollback is a
no-op for behavior (old rows stay `''`); the column may remain unused.

## Documentation

`docs/superpowers/specs/2026-09-26-public-landing-page-design.md` §"Catalog =
actively-used models only" becomes stale. The landing design intent changes to
"catalog = operator-defined chains". Update that line (and the `GET
/v1/public/models` bullet) to match, or note the supersession. `README` /
config docs do not describe the public catalog and need no change.
