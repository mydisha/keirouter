import { useId, useState } from "react";
import { Link } from "react-router-dom";
import { SectionTitle, SegmentedControl } from "../ui";
import { CodeBlock } from "../ui/code-block";
import { ICONS } from "../../lib/icons";

type Client = "claude" | "cursor" | "vscode" | "codex" | "curl";

const CLIENTS: { value: Client; label: string }[] = [
  { value: "claude", label: "Claude Code" },
  { value: "cursor", label: "Cursor" },
  { value: "vscode", label: "VS Code" },
  { value: "codex", label: "Codex" },
  { value: "curl", label: "curl" },
];

const KEY = "<YOUR_KEIROUTER_API_KEY>";

/** mcpEndpoint turns the API base URL (".../v1") into the MCP endpoint. */
export function mcpEndpoint(apiBase: string): string {
  try {
    const u = new URL(apiBase);
    return `${u.origin}/mcp`;
  } catch {
    return `${window.location.origin}/mcp`;
  }
}

export function snippetFor(client: Client, url: string): { code: string; language: "bash" | "json" | "toml"; note: string } {
  switch (client) {
    case "claude":
      return {
        language: "bash",
        code: `claude mcp add --transport http keirouter ${url} \\\n  --header "Authorization: Bearer ${KEY}"`,
        note: "Then run /mcp in Claude Code to see the tools.",
      };
    case "cursor":
      return {
        language: "json",
        code: JSON.stringify({ mcpServers: { keirouter: { url, headers: { Authorization: `Bearer ${KEY}` } } } }, null, 2),
        note: "Add to ~/.cursor/mcp.json, or .cursor/mcp.json in a project.",
      };
    case "vscode":
      return {
        language: "json",
        code: JSON.stringify({ servers: { keirouter: { type: "http", url, headers: { Authorization: `Bearer ${KEY}` } } } }, null, 2),
        note: "Add to .vscode/mcp.json in the workspace.",
      };
    case "codex":
      return {
        language: "toml",
        code: `[mcp_servers.keirouter]\nurl = "${url}"\nbearer_token_env_var = "KEIROUTER_API_KEY"`,
        note: "Add to ~/.codex/config.toml and export KEIROUTER_API_KEY with your key.",
      };
    default:
      return {
        language: "bash",
        code: `curl ${url} \\\n  -H "Authorization: Bearer ${KEY}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'`,
        note: "Lists the tools this key may call.",
      };
  }
}

/** McpConnectCard shows how to point MCP clients at the gateway. */
export function McpConnectCard({ apiBase }: { apiBase: string }) {
  const headingId = useId();
  const [client, setClient] = useState<Client>("claude");
  const url = mcpEndpoint(apiBase);
  const s = snippetFor(client, url);
  return (
    <section aria-labelledby={headingId} className="overflow-hidden rounded-2xl border border-line bg-surface shadow-paper">
      <div className="border-b border-line px-4 py-3">
        <SectionTitle
          icon={ICONS.mcpServers}
          id={headingId}
          title="MCP tools"
          subtitle={
            <>
              One endpoint for the tools of every{" "}
              <Link to="/mcp-servers" className="text-link hover:underline">
                MCP server
              </Link>{" "}
              you added, with the same API keys.
            </>
          }
        />
      </div>
      <div className="space-y-3 px-4 py-4">
        <CodeBlock code={url} language="text" title="MCP endpoint" copyToast="MCP endpoint copied" />
        <div className="max-w-full overflow-x-auto">
          <SegmentedControl value={client} onChange={setClient} options={CLIENTS} label="Client" />
        </div>
        <CodeBlock code={s.code} language={s.language} copyToast="Snippet copied" />
        <p className="text-[12.5px] leading-5 text-fg-muted">
          {s.note} Clients see tools as <span className="font-mono">slug__tool</span>; append <span className="font-mono">/slug</span> to the URL to expose one server with its own tool names.
        </p>
      </div>
    </section>
  );
}
