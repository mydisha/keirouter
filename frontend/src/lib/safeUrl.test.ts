import { test } from "node:test";
import assert from "node:assert/strict";
import { safeHttpUrl } from "./safeUrl.ts";

test("keeps absolute http(s) URLs", () => {
  assert.equal(safeHttpUrl("https://t.me/x"), "https://t.me/x");
  assert.equal(safeHttpUrl("http://localhost:8080/cb"), "http://localhost:8080/cb");
});

test("refuses script and other schemes", () => {
  for (const bad of ["javascript:alert(1)", " JavaScript:alert(1)", "java\tscript:alert(1)", "data:text/html,<script>alert(1)</script>", "vbscript:x", "file:///etc/passwd"]) {
    assert.equal(safeHttpUrl(bad), null, bad);
  }
});

test("refuses relative, empty and malformed values", () => {
  for (const bad of ["", "/portal", "//evil.example", "not a url", undefined, null]) {
    assert.equal(safeHttpUrl(bad), null, String(bad));
  }
});
