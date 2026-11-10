import { describe, expect, it } from "vitest";
import { markFor, markKey } from "./marks";
import catalog from "../../../../backend/internal/mcp/catalog.go?raw";

// The built-in MCP presets live in the backend catalog; the dashboard draws
// each one's mark from its id, so a preset without a mark shows a monogram.
const presetIDs = [...catalog.matchAll(/^\s*ID:\s*"([a-z0-9-]+)"/gm)].map((m) => m[1]);

describe("MCP preset marks", () => {
  it("finds the presets in the backend catalog", () => {
    expect(presetIDs.length).toBeGreaterThanOrEqual(13);
  });
  it.each(presetIDs)("%s has a vendored mark", (id) => {
    expect(markFor(id)).toBeDefined();
  });
});

describe("markKey", () => {
  it("takes the id from an icon path", () => {
    expect(markKey("/providers/context7.png")).toBe("context7");
    expect(markKey(undefined)).toBeUndefined();
  });
});
