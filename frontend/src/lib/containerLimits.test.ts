import { test } from "node:test";
import assert from "node:assert/strict";
import { bytesToMemory, cpuToNano, memoryToBytes, nanoToCpu } from "./containerLimits.ts";

test("memory converts MB and GB to bytes and back", () => {
  assert.equal(memoryToBytes("512"), "536870912");
  assert.equal(memoryToBytes("128MB"), "134217728");
  assert.equal(memoryToBytes("1.5 GB"), "1610612736");
  assert.equal(memoryToBytes(""), "");
  assert.equal(memoryToBytes("0"), null);
  assert.equal(memoryToBytes("lots"), null);
  assert.equal(bytesToMemory("536870912"), "512");
  assert.equal(bytesToMemory(""), "");
});

test("cpu converts CPUs to nano-CPUs and back", () => {
  assert.equal(cpuToNano("0.5"), "500000000");
  assert.equal(cpuToNano("1 CPU"), "1000000000");
  assert.equal(cpuToNano(".25cpu"), "250000000");
  assert.equal(cpuToNano(""), "");
  assert.equal(cpuToNano("0"), null);
  assert.equal(cpuToNano("-1"), null);
  assert.equal(nanoToCpu("1000000000"), "1");
  assert.equal(nanoToCpu("500000000"), "0.5");
});
