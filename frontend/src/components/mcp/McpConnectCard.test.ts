import { describe, expect, it } from "vitest";
import { mcpEndpoint, snippetFor } from "./McpConnectCard";

describe("mcpEndpoint", () => {
  it("uses the origin of the API base URL", () => {
    expect(mcpEndpoint("http://127.0.0.1:20180/v1")).toBe("http://127.0.0.1:20180/mcp");
    expect(mcpEndpoint("https://kei.example.com/v1")).toBe("https://kei.example.com/mcp");
  });
});

describe("snippetFor", () => {
  const url = "https://kei.example.com/mcp";
  it("puts the endpoint and a bearer key placeholder in every client snippet", () => {
    for (const c of ["claude", "cursor", "vscode", "curl"] as const) {
      const s = snippetFor(c, url);
      expect(s.code).toContain(url);
      expect(s.code).toContain("Bearer <YOUR_KEIROUTER_API_KEY>");
    }
  });
  it("produces valid JSON for JSON clients", () => {
    expect(() => JSON.parse(snippetFor("cursor", url).code)).not.toThrow();
    expect(JSON.parse(snippetFor("vscode", url).code).servers.keirouter.type).toBe("http");
  });
  it("reads the Codex key from an environment variable", () => {
    expect(snippetFor("codex", url).code).toContain('bearer_token_env_var = "KEIROUTER_API_KEY"');
  });
});
