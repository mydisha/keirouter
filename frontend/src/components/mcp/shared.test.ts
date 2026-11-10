import { describe, expect, it } from "vitest";
import { connectHint, fillPresetURL, PRESET_TEXT_VALUE, SLUG_PATTERN, skeletonArgs, slugFromName } from "./shared";

describe("slugFromName", () => {
  it("lowercases and joins words with hyphens", () => {
    expect(slugFromName("GitHub Enterprise")).toBe("github-enterprise");
    expect(slugFromName("  Internal  docs (EU) ")).toBe("internal-docs-eu");
  });
  it("drops accents and symbols, never ends with a hyphen", () => {
    expect(slugFromName("Café__Tools!")).toBe("cafe-tools");
    expect(slugFromName("a".repeat(31) + " b")).toBe("a".repeat(31));
  });
  it("always produces a slug the server accepts, or an empty one", () => {
    for (const name of ["GitHub", "x", "日本語", "----", "Linear (read only)", "A".repeat(80)]) {
      const slug = slugFromName(name);
      expect(slug === "" || SLUG_PATTERN.test(slug)).toBe(true);
    }
  });
});

describe("fillPresetURL", () => {
  it("fills placeholders, keeping option values that carry a path", () => {
    expect(fillPresetURL("https://mcp.{site}/v1/mcp", { site: "datadoghq.eu" })).toBe("https://mcp.datadoghq.eu/v1/mcp");
    expect(fillPresetURL("https://mcp.linear.app/mcp{access}", { access: "/readonly" })).toBe("https://mcp.linear.app/mcp/readonly");
    expect(fillPresetURL("https://x/mcp?ref={project_ref}&read_only={read_only}", { project_ref: "abc", read_only: "true" })).toBe(
      "https://x/mcp?ref=abc&read_only=true",
    );
  });
  it("text values the server would reject are caught before filling", () => {
    expect(PRESET_TEXT_VALUE.test("abc-123.x_y")).toBe(true);
    expect(PRESET_TEXT_VALUE.test("a b")).toBe(false);
    expect(PRESET_TEXT_VALUE.test("../x")).toBe(false);
  });
  it("leaves an empty value for a missing variable", () => {
    expect(fillPresetURL("https://api.githubcopilot.com/mcp/{access}", {})).toBe("https://api.githubcopilot.com/mcp/");
  });
});

describe("skeletonArgs", () => {
  it("fills required properties with empty values of their type", () => {
    expect(
      skeletonArgs({
        type: "object",
        properties: { q: { type: "string" }, limit: { type: "integer", default: 10 }, mode: { enum: ["fast", "slow"] }, verbose: { type: "boolean" } },
        required: ["q", "limit", "mode"],
      }),
    ).toEqual({ q: "", limit: 10, mode: "fast" });
  });
  it("uses every property when none is required, recursing into objects", () => {
    expect(skeletonArgs({ properties: { opts: { type: "object", properties: { dry: { type: ["boolean", "null"] } } }, tags: { type: "array" } } })).toEqual({
      opts: { dry: false },
      tags: [],
    });
  });
  it("tolerates missing or odd schemas", () => {
    expect(skeletonArgs(undefined)).toEqual({});
    expect(skeletonArgs({ type: "object" })).toEqual({});
    expect(skeletonArgs({ required: ["ghost"], properties: {} })).toEqual({});
  });
});

describe("connectHint", () => {
  it("points at the token when the server rejects the credentials", () => {
    expect(connectHint("probe: upstream rejected the credentials (HTTP 401)")).toMatch(/token/);
  });
  it("explains the private address block, timeouts, unreachable hosts and wrong paths", () => {
    expect(connectHint("dial tcp: private network address blocked")).toMatch(/public URL/);
    expect(connectHint("context deadline exceeded")).toMatch(/in time/);
    expect(connectHint("dial tcp: lookup mcp.exmple.com: no such host")).toMatch(/reach the host/);
    expect(connectHint("upstream HTTP 404: not found")).toMatch(/\/mcp/);
  });
  it("adds nothing when the error already says what to do, or there is none", () => {
    expect(connectHint("server lists 900 tools; the limit is 500")).toBeUndefined();
    expect(connectHint("upstream HTTP 4040: odd")).toBeUndefined();
    expect(connectHint(undefined)).toBeUndefined();
    expect(connectHint("")).toBeUndefined();
  });
});
