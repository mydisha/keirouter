import { test } from "node:test";
import assert from "node:assert/strict";
import { formatCostUSD } from "./format.ts";

test("formatCostUSD keeps three significant digits for sub-cent costs", () => {
  assert.equal(formatCostUSD(0.0000121), "$0.0000121");
  assert.equal(formatCostUSD(0.0000077), "$0.0000077");
  assert.equal(formatCostUSD(0.0000001), "$0.0000001");
  assert.equal(formatCostUSD(0.00123456), "$0.00123");
});

test("formatCostUSD uses four decimals for cents and two for dollars", () => {
  assert.equal(formatCostUSD(0.0525), "$0.0525");
  assert.equal(formatCostUSD(12.3456), "$12.35");
  assert.equal(formatCostUSD(1234.5), "$1,234.50");
});

test("formatCostUSD handles zero, negatives and garbage", () => {
  assert.equal(formatCostUSD(0), "$0.00");
  assert.equal(formatCostUSD(-0.0000121), "-$0.0000121");
  assert.equal(formatCostUSD(Number.NaN), "$0.00");
});
