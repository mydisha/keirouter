import { Scissors } from "lucide-react";
import type { ClientSaving, TokenSavings, UsageInsights } from "../lib/api";
import { SavingsCardShareButton } from "./SavingsCard";

function fmtNum(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return n.toLocaleString();
}

function fmtBytes(n: number): string {
  if (n >= 1_048_576) return `${(n / 1_048_576).toFixed(1)} MB`;
  if (n >= 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${n} B`;
}

function fmtUSD(n: number): string {
  if (n > 0 && n < 0.01) return "<$0.01";
  return `$${n.toFixed(2)}`;
}

// prettyClient turns an internal client label into a readable name. Generic
// labels (any client detected from a User-Agent) pass through title-cased.
function prettyClient(id: string): string {
  if (!id || id === "unknown") return "Unknown";
  const known: Record<string, string> = {
    "claude-code": "Claude Code",
    "kilo-code": "Kilo Code",
    "roo-code": "Roo Code",
    cursor: "Cursor",
    codex: "Codex",
    cline: "Cline",
    copilot: "Copilot",
    opencode: "OpenCode",
    droid: "Droid",
    aider: "Aider",
  };
  if (known[id]) return known[id];
  return id
    .replace(/[-_.]+/g, " ")
    .replace(/\bsdk\b/i, "SDK")
    .split(" ")
    .map((w) => (w ? w[0].toUpperCase() + w.slice(1) : w))
    .join(" ");
}

export function TokenSavingsBreakdown({ savings, totalRequests, insights, period }: { savings: TokenSavings; totalRequests: number; insights: UsageInsights; period: string }) {
  const rules = savings.rules || [];
  const headroomTransforms = savings.headroom_transforms || [];
  const maxBytes = Math.max(...rules.map((r) => r.bytes_saved), 1);
  const totalCavemanPct = totalRequests > 0 ? ((savings.caveman_requests / totalRequests) * 100).toFixed(1) : "0";
  const totalTersePct = totalRequests > 0 ? ((savings.terse_requests / totalRequests) * 100).toFixed(0) : "0";
  const totalHeadroomPct = totalRequests > 0 ? ((savings.headroom_requests / totalRequests) * 100).toFixed(1) : "0";
  const hasSavings = savings.slim_bytes_saved > 0 || (savings.headroom_tokens_saved ?? 0) > 0 || savings.caveman_requests > 0 || savings.terse_requests > 0 || rules.length > 0 || headroomTransforms.length > 0;
  // Prefer the backend's blended USD estimate; fall back to a rough $3/M rate
  // for older payloads that predate the usd_saved field.
  const usdSaved = savings.usd_saved ?? ((savings.slim_tokens_saved + (savings.headroom_tokens_saved ?? 0)) / 1_000_000) * 3;

  return (
    <div className="rounded-xl border border-[var(--border)] bg-[var(--bg)] shadow-sm overflow-hidden">
      <div className="flex items-center justify-between border-b border-[var(--border)] px-5 py-3 bg-[var(--bg-subtle)]">
        <div className="flex items-center gap-2">
          <Scissors className="h-4 w-4 text-[var(--text-muted)]" />
          <h3 className="text-sm font-semibold tracking-tight">Optimization Engine</h3>
        </div>
        <div className="flex items-center gap-4">
          <div className="flex items-center gap-3 text-[10px] font-bold uppercase tracking-wider text-[var(--text-muted)]">
          {savings.headroom_requests > 0 && (
            <span className="flex items-center gap-1.5">
              <span className="h-1.5 w-1.5 rounded-full bg-amber-500" />
              HDRM {totalHeadroomPct}%
            </span>
          )}
          {savings.caveman_requests > 0 && (
            <span className="flex items-center gap-1.5">
              <span className="h-1.5 w-1.5 rounded-full bg-purple-500" />
              CVMN {totalCavemanPct}%
            </span>
          )}
          {savings.terse_requests > 0 && (
            <span className="flex items-center gap-1.5">
              <span className="h-1.5 w-1.5 rounded-full bg-indigo-500" />
              TRSE {totalTersePct}%
            </span>
          )}
          </div>
          <SavingsCardShareButton insights={insights} period={period} />
        </div>
      </div>
      <div className="p-5">
        <div className="space-y-4">
          {rules.length === 0 ? (
            <div className="flex items-center justify-center py-6 text-xs font-medium text-[var(--text-muted)]">
              {!hasSavings 
                ? "No optimizations active for this period" 
                : "Output optimizations active (no prompt savings yet)"}
            </div>
          ) : (
            rules.map((r) => (
              <div key={r.rule} className="flex items-center gap-4">
                <div className="w-32 shrink-0 text-xs font-mono font-medium text-[var(--text)] truncate" title={r.rule}>{r.rule}</div>
                <div className="flex-1">
                  <div className="h-1.5 w-full overflow-hidden rounded-full bg-[var(--bg-subtle)]">
                    <div
                      className="h-full rounded-full bg-[var(--text)] transition-all"
                      style={{ width: `${Math.max(2, (r.bytes_saved / maxBytes) * 100)}%` }}
                    />
                  </div>
                </div>
                <div className="w-24 text-right text-xs font-medium tabular-nums text-[var(--text)]">
                  {fmtBytes(r.bytes_saved)}
                </div>
                <div className="w-20 text-right text-[10px] font-medium tabular-nums text-[var(--text-muted)] uppercase">
                  {fmtNum(r.tokens_saved)} tok
                </div>
                <div className="w-12 text-right text-[10px] font-medium tabular-nums text-[var(--text-muted)]">
                  {r.count}×
                </div>
              </div>
            ))
          )}
        </div>
        <ClientBreakdown clients={savings.by_client || []} />
        <div className="mt-6 flex items-center justify-between border-t border-[var(--border)] pt-4">
          <div className="flex flex-col">
            <span className="text-[10px] font-bold uppercase tracking-wider text-[var(--text-muted)] mb-1">Total Savings</span>
            <span className="text-lg font-light text-[var(--text)] tabular-nums">{fmtBytes(savings.slim_bytes_saved)} <span className="text-xs text-[var(--text-muted)] font-medium ml-1">({fmtNum(savings.slim_tokens_saved)} tokens)</span></span>
          </div>
          <div className="flex flex-col text-right">
            <span className="text-[10px] font-bold uppercase tracking-wider text-[var(--text-muted)] mb-1">Est. Value Saved</span>
            <span className="text-lg font-light text-[var(--text)] tabular-nums">{fmtUSD(usdSaved)}</span>
            <span className="text-[10px] font-medium text-[var(--text-muted)]">estimated</span>
          </div>
        </div>
      </div>
    </div>
  );
}

// ClientBreakdown shows which clients benefited from optimization, attributing
// token and estimated dollar savings to each. Generic across any client — it
// renders whatever the backend reports, never locked to specific tools.
function ClientBreakdown({ clients }: { clients: ClientSaving[] }) {
  if (clients.length === 0) return null;
  const maxTokens = Math.max(...clients.map((c) => c.tokens_saved), 1);
  return (
    <div className="mt-6 border-t border-[var(--border)] pt-4">
      <div className="mb-3 text-[10px] font-bold uppercase tracking-wider text-[var(--text-muted)]">
        Savings by Client
      </div>
      <div className="space-y-3">
        {clients.map((c) => (
          <div key={c.client} className="flex items-center gap-4">
            <div className="w-32 shrink-0 truncate text-xs font-medium text-[var(--text)]" title={prettyClient(c.client)}>
              {prettyClient(c.client)}
            </div>
            <div className="flex-1">
              <div className="h-1.5 w-full overflow-hidden rounded-full bg-[var(--bg-subtle)]">
                <div
                  className="h-full rounded-full bg-[var(--text)] transition-all"
                  style={{ width: `${Math.max(2, (c.tokens_saved / maxTokens) * 100)}%` }}
                />
              </div>
            </div>
            <div className="w-20 text-right text-[10px] font-medium uppercase tabular-nums text-[var(--text-muted)]">
              {fmtNum(c.tokens_saved)} tok
            </div>
            <div className="w-16 text-right text-xs font-medium tabular-nums text-[var(--text)]">
              {fmtUSD(c.usd_saved)}
            </div>
            <div className="w-12 text-right text-[10px] font-medium tabular-nums text-[var(--text-muted)]">
              {c.requests}×
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
