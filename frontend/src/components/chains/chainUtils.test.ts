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
