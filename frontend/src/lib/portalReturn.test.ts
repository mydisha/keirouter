import { test } from "node:test";
import assert from "node:assert/strict";
import { isPortalReturnPath } from "./portalReturn.ts";

test("keeps same-site portal paths", () => {
  assert.ok(isPortalReturnPath("/portal/hosting?region=eu"));
  assert.ok(isPortalReturnPath("/portal/topup"));
});

test("refuses anything that could leave the portal", () => {
  for (const bad of ["/portal", "//evil.example/portal/x", "https://evil.example/portal/x", "/portal/../admin", "/portal/x?next=//evil", "/portal/auth/google/start", "/ahoirilaila/hosting", "/portal/x#y", "/portal/x?a=<b>"]) {
    assert.equal(isPortalReturnPath(bad), false, bad);
  }
});
