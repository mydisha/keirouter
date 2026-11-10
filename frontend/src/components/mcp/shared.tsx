import { Badge } from "../ui";
import { ProviderLogo } from "../ProviderLogo";
import type { MCPCallStatus, MCPServer } from "../../lib/api";

/** Server status as one badge: disabled wins over the last sync result. */
export function ServerStatusBadge({ server }: { server: Pick<MCPServer, "enabled" | "health_status" | "health_error"> }) {
  if (!server.enabled) return <Badge tone="neutral">Disabled</Badge>;
  switch (server.health_status) {
    case "healthy":
      return <Badge tone="success">Healthy</Badge>;
    case "failing":
      return (
        <Badge tone="danger" title={server.health_error || undefined}>
          Failing
        </Badge>
      );
    default:
      return <Badge tone="neutral">Not checked</Badge>;
  }
}

const CALL_STATUS: Record<MCPCallStatus, { label: string; tone: "success" | "warning" | "danger" | "neutral" }> = {
  success: { label: "Success", tone: "success" },
  tool_error: { label: "Tool error", tone: "warning" },
  denied: { label: "Denied", tone: "warning" },
  blocked: { label: "Blocked", tone: "warning" },
  rate_limited: { label: "Rate limited", tone: "warning" },
  upstream_error: { label: "Upstream error", tone: "danger" },
  timeout: { label: "Timeout", tone: "danger" },
};

export const CALL_STATUS_OPTIONS = Object.entries(CALL_STATUS).map(([value, v]) => ({ value, label: v.label }));

export function CallStatusBadge({ status }: { status: MCPCallStatus }) {
  const s = CALL_STATUS[status] ?? { label: status, tone: "neutral" as const };
  return <Badge tone={s.tone}>{s.label}</Badge>;
}

/** The vendor mark for a preset-based server, a monogram otherwise. */
export function ServerMark({ server, size = 28 }: { server: Pick<MCPServer, "preset_id" | "slug" | "name">; size?: number }) {
  const id = server.preset_id || server.slug;
  return <ProviderLogo icon={`/providers/${id}.png`} name={server.name} size={size} />;
}

/** slugFromName turns "GitHub Enterprise" into "github-enterprise" (max 32). */
export function slugFromName(name: string): string {
  return name
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 32)
    .replace(/-+$/g, "");
}

export const SLUG_PATTERN = /^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$/;

/** fillPresetURL replaces {key} placeholders with the given values. Values
 *  are not encoded: select options can carry a path ("/readonly"), and text
 *  values are limited to PRESET_TEXT_VALUE before they get here. */
export function fillPresetURL(template: string, values: Record<string, string>): string {
  return template.replace(/\{([a-z][a-z0-9_]*)\}/g, (_, key: string) => values[key] ?? "");
}

/** The characters the server accepts in a text variable of a preset URL. */
export const PRESET_TEXT_VALUE = /^[A-Za-z0-9._-]+$/;

/** connectHint turns a failed connection test into the next thing to check,
 *  or undefined when the error already says it. */
export function connectHint(error: string | undefined): string | undefined {
  const e = (error ?? "").toLowerCase();
  if (e.includes("rejected the credentials")) return "Check the token, and that it may use this server.";
  if (e.includes("address blocked") || e.includes("multicast blocked")) return "KeiRouter does not connect to private or loopback addresses. Use the server's public URL.";
  if (e.includes("deadline exceeded") || e.includes("timeout") || e.includes("timed out")) return "The server did not answer in time. Check the URL, then try again.";
  if (e.includes("no such host") || e.includes("connection refused") || e.includes("network is unreachable")) return "KeiRouter could not reach the host. Check the URL.";
  if (/upstream http 404\b/.test(e)) return "Nothing answers at this path. Check the URL; MCP servers often end in /mcp.";
  return undefined;
}

export function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

type Schema = { type?: string | string[]; properties?: Record<string, Schema>; required?: string[]; default?: unknown; enum?: unknown[] };

/** skeletonArgs builds a starting JSON object for a tool's input schema:
 *  required properties first (all properties when none are required), each
 *  filled with its default, first enum value, or an empty value of its type. */
export function skeletonArgs(schema: unknown): Record<string, unknown> {
  const s = (schema ?? {}) as Schema;
  const props = s.properties ?? {};
  const required = (s.required ?? []).filter((k) => k in props);
  const keys = required.length > 0 ? required : Object.keys(props);
  const out: Record<string, unknown> = {};
  for (const k of keys) out[k] = emptyValue(props[k]);
  return out;
}

function emptyValue(p: Schema | undefined): unknown {
  if (!p) return null;
  if (p.default !== undefined) return p.default;
  if (p.enum && p.enum.length > 0) return p.enum[0];
  const t = Array.isArray(p.type) ? p.type.find((x) => x !== "null") : p.type;
  switch (t) {
    case "string":
      return "";
    case "integer":
    case "number":
      return 0;
    case "boolean":
      return false;
    case "array":
      return [];
    case "object":
      return skeletonArgs(p);
    default:
      return null;
  }
}
