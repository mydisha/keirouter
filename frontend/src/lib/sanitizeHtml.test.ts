import { test } from "node:test";
import assert from "node:assert/strict";
import { sanitizeHtml, safeHref } from "./sanitizeHtml.ts";

test("keeps allowlisted tags", () => {
  assert.equal(sanitizeHtml("<b>hi</b> <em>there</em>"), "<b>hi</b> <em>there</em>");
});

test("strips script contents", () => {
  assert.equal(sanitizeHtml("<script>alert(1)</script>ok"), "ok");
});

test("strips event handler attributes", () => {
  assert.equal(sanitizeHtml('<b onmouseover="x()">hi</b>'), "<b>hi</b>");
});

test("drops javascript: href", () => {
  assert.equal(sanitizeHtml('<a href="javascript:alert(1)">x</a>'), "<a>x</a>");
});

test("keeps https href with rel/target", () => {
  assert.equal(
    sanitizeHtml('<a href="https://x.dev">x</a>'),
    '<a href="https://x.dev" rel="noopener noreferrer" target="_blank">x</a>',
  );
});

test("unwraps disallowed elements", () => {
  assert.equal(sanitizeHtml("<div>hi</div>"), "hi");
});

test("drops script with attributes and keeps following text", () => {
  assert.equal(sanitizeHtml('<script type="text/javascript">evil()</script>safe'), "safe");
});

test("escapes stray angle brackets in text", () => {
  assert.equal(sanitizeHtml("a < b & c"), "a &lt; b &amp; c");
});

test("strips attribute injection on allowed tags", () => {
  assert.equal(sanitizeHtml('<b style="color:red" data-x="1">hi</b>'), "<b>hi</b>");
});

test("keeps mailto href", () => {
  assert.equal(
    sanitizeHtml('<a href="mailto:x@y.dev">mail</a>'),
    '<a href="mailto:x@y.dev" rel="noopener noreferrer" target="_blank">mail</a>',
  );
});

test("safeHref fails closed on unsafe and relative URLs", () => {
  assert.equal(safeHref("javascript:alert(1)"), null);
  assert.equal(safeHref("https://x.dev"), "https://x.dev");
  assert.equal(safeHref("/relative"), null);
});
