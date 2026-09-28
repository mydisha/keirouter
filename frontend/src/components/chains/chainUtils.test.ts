import { test } from "node:test";
import assert from "node:assert/strict";
import { deriveCacheRates } from "./chainUtils.ts";

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
