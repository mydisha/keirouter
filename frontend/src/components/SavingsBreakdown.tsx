import { useState } from "react";
import { ChevronDown, FileText, Scissors } from "lucide-react";
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

// clientIcon maps a client id to an icon asset in /providers. Falls back to
// undefined so the avatar renders initials instead.
function clientIcon(id: string): string | undefined {
  const map: Record<string, string> = {
    "claude-code": "claude",
    "kilo-code": "kilocode",
    "roo-code": "roo",
    cursor: "cursor",
    codex: "codex",
    cline: "cline",
    copilot: "copilot",
    opencode: "opencode",
    droid: "droid",
    kiro: "kiro",
    qoder: "qoder",
    commandcode: "commandcode",
  };
  const file = map[id];
  return file ? `/providers/${file}.png` : undefined;
}

function ClientAvatar({ id, className = "h-6 w-6" }: { id: string; className?: string }) {
  const [errored, setErrored] = useState(false);
  const src = clientIcon(id);
  if (!src || errored) {
    const name = prettyClient(id);
    const initials = name.split(" ").slice(0, 2).map((w) => w[0]).join("").toUpperCase();
    return (
      <span className={`flex shrink-0 items-center justify-center rounded-md bg-[var(--bg-subtle)] text-[9px] font-bold text-[var(--text-muted)] ring-1 ring-[var(--border)] ${className}`}>
        {initials}
      </span>
    );
  }
  return (
    <span className={`flex shrink-0 items-center justify-center rounded-md bg-white p-0.5 ring-1 ring-black/5 dark:bg-black/20 dark:ring-white/10 ${className}`}>
      <img src={src} alt={prettyClient(id)} className="h-full w-full rounded object-contain" onError={() => setErrored(true)} />
    </span>
  );
}

export function TokenSavingsBreakdown({ savings, totalRequests, insights, period }: { savings: TokenSavings; totalRequests: number; insights: UsageInsights; period: string }) {
  const [expanded, setExpanded] = useState(false);
  const rules = (savings.rules || []).slice().sort((a, b) => b.bytes_saved - a.bytes_saved);
  const maxBytes = Math.max(...rules.map((r) => r.bytes_saved), 1);
  const totalCavemanPct = totalRequests > 0 ? ((savings.caveman_requests / totalRequests) * 100).toFixed(1) : "0";
  const totalTersePct = totalRequests > 0 ? ((savings.terse_requests / totalRequests) * 100).toFixed(0) : "0";
  const headroomTokensSaved = savings.headroom_tokens_saved;
  const ponytailRequests = savings.ponytail_requests;
  const totalPonytailPct = totalRequests > 0 ? ((ponytailRequests / totalRequests) * 100).toFixed(0) : "0";
  const hasSavings = savings.total_tokens_saved > 0 || savings.optimized_requests > 0 || savings.usd_saved > 0 || rules.length > 0;
  // USD savings and optimized request count are authoritative backend values.
  // They must not be reconstructed from overlapping optimizer activations or a
  // hard-coded blended token rate.
  const usdSaved = savings.usd_saved;
  const optimizedRequests = savings.optimized_requests;

  const badges: { label: string; pct: string; color: string }[] = [];
  if (savings.caveman_requests > 0) badges.push({ label: "CVMN", pct: totalCavemanPct, color: "#a855f7" });
  if (savings.terse_requests > 0) badges.push({ label: "TRSE", pct: totalTersePct, color: "#6366f1" });
  if (ponytailRequests > 0) badges.push({ label: "PONY", pct: totalPonytailPct, color: "#14b8a6" });

  return (
    <div className="rounded-xl border border-[var(--border)] bg-[var(--bg)] shadow-sm overflow-hidden">
      <div className="flex items-stretch bg-[var(--bg-subtle)]">
        <button
          type="button"
          aria-expanded={expanded}
          onClick={() => setExpanded((current) => !current)}
          className="flex min-w-0 flex-1 flex-wrap items-center gap-x-5 gap-y-2 px-5 py-3 text-left outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-400/60"
        >
          <span className="flex min-w-[180px] items-center gap-2">
            <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-emerald-500/15 text-ok">
              <Scissors className="h-3.5 w-3.5" />
            </span>
            <span>
              <span className="block text-sm font-semibold tracking-tight">Optimization</span>
              <span className="block text-[10px] text-[var(--text-muted)]">Open rules and client attribution</span>
            </span>
          </span>
          <span className="flex flex-1 flex-wrap items-center gap-x-5 gap-y-1 text-xs tabular-nums">
            <span><strong className="text-[var(--text)]">{fmtUSD(usdSaved)}</strong> <span className="text-[var(--text-muted)]">saved</span></span>
            <span><strong className="text-[var(--text)]">{fmtNum(savings.total_tokens_saved)}</strong> <span className="text-[var(--text-muted)]">tokens</span></span>
            <span><strong className="text-[var(--text)]">{fmtNum(optimizedRequests)}</strong> <span className="text-[var(--text-muted)]">optimized</span></span>
            <span className="hidden xl:inline"><strong className="text-[var(--text)]">{fmtBytes(savings.slim_bytes_saved)}</strong> <span className="text-[var(--text-muted)]">prompt reduced</span></span>
          </span>
          <ChevronDown className={`h-4 w-4 shrink-0 text-[var(--text-muted)] transition-transform ${expanded ? "rotate-180" : ""}`} />
        </button>
        <div className="flex shrink-0 items-center border-l border-[var(--border)] px-3">
          <SavingsCardShareButton insights={insights} period={period} />
        </div>
      </div>

      {expanded && <div className="border-t border-[var(--border)] p-5">
        <div className="mb-4 flex flex-wrap items-center gap-2 text-[11.5px] font-medium text-[var(--text-muted)]">
          {badges.map((badge) => (
            <span key={badge.label} className="flex items-center gap-1.5 rounded-full border border-[var(--border)] px-2 py-1">
              <span className="h-1.5 w-1.5 rounded-full" style={{ backgroundColor: badge.color }} />
              {badge.label} {badge.pct}%
            </span>
          ))}
          {savings.usd_saved_estimate && <span className="rounded-full border border-amber-300/60 px-2 py-1 text-warn">Estimated value</span>}
        </div>
        {/* Rules */}
        <div>
          <div className="mb-3 flex items-center gap-1.5 text-[11.5px] font-medium text-[var(--text-muted)]">
            <FileText className="h-3 w-3" /> Compression Rules
          </div>
          {rules.length === 0 ? (
            <div className="flex items-center justify-center rounded-lg border border-dashed border-[var(--border)] bg-[var(--bg-subtle)]/40 py-6 text-xs font-medium text-[var(--text-muted)]">
              {!hasSavings
                ? "No optimizations active for this period"
                : "Output optimizations active (no prompt savings yet)"}
            </div>
          ) : (
            <div className="space-y-2.5">
              {rules.map((r, i) => (
                <div key={r.rule} className="group flex items-center gap-3">
                  <div className="flex w-36 shrink-0 items-center gap-2">
                    <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-md bg-[var(--bg-subtle)] text-[10px] font-bold tabular-nums text-[var(--text-muted)]">{i + 1}</span>
                    <span className="truncate font-mono text-xs font-medium text-[var(--text)]" title={r.rule}>{r.rule}</span>
                  </div>
                  <div className="flex-1">
                    <div className="h-2 w-full overflow-hidden rounded-full bg-[var(--bg-subtle)]">
                      <div
                        className="h-full rounded-full transition-all"
                        style={{
                          width: `${Math.max(3, (r.bytes_saved / maxBytes) * 100)}%`,
                          background: "var(--color-accent-500)",
                        }}
                      />
                    </div>
                  </div>
                  <div className="w-20 text-right text-xs font-semibold tabular-nums text-[var(--text)]">{fmtBytes(r.bytes_saved)}</div>
                  <div className="hidden w-16 text-right text-[10px] font-medium tabular-nums text-[var(--text-muted)] sm:block">{fmtNum(r.tokens_saved)} tok</div>
                  <div className="w-10 text-right text-[10px] font-medium tabular-nums text-[var(--text-muted)]">{r.count}×</div>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Headroom / Ponytail chips */}
        {(headroomTokensSaved > 0 || ponytailRequests > 0) && (
          <div className="mt-5 flex flex-wrap gap-2">
            {headroomTokensSaved > 0 && (
              <div className="flex items-center gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)]/50 px-3 py-2">
                <span className="text-[11.5px] font-medium text-[var(--text-muted)]">Headroom</span>
                <span className="text-sm font-medium tabular-nums text-[var(--text)]">{fmtNum(headroomTokensSaved)}</span>
                <span className="text-[10px] text-[var(--text-muted)]">tokens</span>
              </div>
            )}
            {ponytailRequests > 0 && (
              <div className="flex items-center gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)]/50 px-3 py-2">
                <span className="text-[11.5px] font-medium text-[var(--text-muted)]">Ponytail</span>
                <span className="text-sm font-medium tabular-nums text-[var(--text)]">{fmtNum(ponytailRequests)}</span>
                <span className="text-[10px] text-[var(--text-muted)]">requests</span>
              </div>
            )}
          </div>
        )}

        <ClientBreakdown clients={savings.by_client || []} />
      </div>}
    </div>
  );
}

// ClientBreakdown shows which clients benefited from optimization, attributing
// token and estimated dollar savings to each. Generic across any client — it
// renders whatever the backend reports, never locked to specific tools.
function ClientBreakdown({ clients }: { clients: ClientSaving[] }) {
  if (clients.length === 0) return null;
  const sorted = clients.slice().sort((a, b) => b.tokens_saved - a.tokens_saved);
  const maxTokens = Math.max(...sorted.map((c) => c.tokens_saved), 1);
  return (
    <div className="mt-6 border-t border-[var(--border)] pt-5">
      <div className="mb-3 text-[11.5px] font-medium text-[var(--text-muted)]">
        Savings by Client
      </div>
      <div className="grid gap-2.5 sm:grid-cols-2">
        {sorted.map((c) => (
          <div key={c.client} className="flex items-center gap-3 rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] px-3 py-2.5">
            <ClientAvatar id={c.client} className="h-8 w-8" />
            <div className="min-w-0 flex-1">
              <div className="flex items-center justify-between gap-2">
                <span className="truncate text-xs font-semibold text-[var(--text)]" title={prettyClient(c.client)}>{prettyClient(c.client)}</span>
                <span className="shrink-0 text-xs font-semibold tabular-nums text-ok">{fmtUSD(c.usd_saved)}</span>
              </div>
              <div className="mt-1.5 h-1.5 w-full overflow-hidden rounded-full bg-[var(--bg-subtle)]">
                <div
                  className="h-full rounded-full"
                  style={{ width: `${Math.max(3, (c.tokens_saved / maxTokens) * 100)}%`, background: "var(--color-accent-500)" }}
                />
              </div>
              <div className="mt-1 flex items-center justify-between text-[10px] font-medium tabular-nums text-[var(--text-muted)]">
                <span>{fmtNum(c.tokens_saved)} tok saved</span>
                <span>{fmtNum(c.optimized_requests)} optimized / {fmtNum(c.requests)} total</span>
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
