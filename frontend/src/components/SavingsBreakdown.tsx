import { useId, useState } from "react";
import type { ClientSaving, TokenSavings, UsageInsights } from "../lib/api";
import type { LucideIcon } from "lucide-react";
import { ICONS } from "../lib/icons";
import { SavingsCardShareButton } from "./SavingsCard";
import { Kpi, SectionTitle } from "./ui";

function fmtNum(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return n.toLocaleString("en-US");
}

function fmtBytes(n: number): string {
  if (n >= 1_048_576) return `${(n / 1_048_576).toFixed(1)} MB`;
  if (n >= 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${n} B`;
}

function fmtUSD(n: number): string {
  if (n > 0 && n < 0.01) return "<$0.01";
  return `$${n.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

// prettyClient turns an internal client label into a readable name. Generic
// labels (any client detected from a User-Agent) pass through title-cased.
export function prettyClient(id: string): string {
  if (!id || id === "unknown") return "Unknown client";
  const known: Record<string, string> = {
    "claude-code": "Claude Code",
    "kilo-code": "Kilo Code",
    "roo-code": "Roo Code",
    cursor: "Cursor",
    codex: "Codex",
    "codex-cli": "Codex CLI",
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

// clientIcon maps a client id to an icon asset in /providers; undefined
// renders initials instead.
function clientIcon(id: string): string | undefined {
  const map: Record<string, string> = {
    "claude-code": "claude",
    "kilo-code": "kilocode",
    "roo-code": "roo",
    cursor: "cursor",
    codex: "codex",
    "codex-cli": "codex",
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

export function ClientAvatar({ id, size = 24 }: { id: string; size?: number }) {
  const [errored, setErrored] = useState(false);
  const src = clientIcon(id);
  const style = { width: size, height: size };
  if (!src || errored) {
    const initials = prettyClient(id).split(" ").slice(0, 2).map((w) => w[0]).join("").toUpperCase();
    return (
      <span style={style} className="flex shrink-0 items-center justify-center rounded-md border border-line bg-subtle font-mono text-[9px] font-medium text-fg-muted" aria-hidden="true">
        {initials}
      </span>
    );
  }
  return (
    <span style={style} className="flex shrink-0 items-center justify-center overflow-hidden rounded-md border border-line bg-white p-[2px]" aria-hidden="true">
      <img src={src} alt="" className="h-full w-full object-contain" onError={() => setErrored(true)} />
    </span>
  );
}

// TokenSavingsBreakdown explains what the optimizers did in the period: the
// value saved, which input-compression rules removed the most, which output
// shapers were active, and which clients benefited.
export function TokenSavingsBreakdown({ savings, totalRequests, insights, period }: { savings: TokenSavings; totalRequests: number; insights: UsageInsights; period: string }) {
  const rules = (savings.rules || []).slice().sort((a, b) => b.bytes_saved - a.bytes_saved);
  const maxBytes = Math.max(...rules.map((r) => r.bytes_saved), 1);
  const share = (n: number) => (totalRequests > 0 ? `${((n / totalRequests) * 100).toFixed(n / totalRequests < 0.1 ? 1 : 0)}%` : "0%");
  // USD savings and optimized request count are authoritative backend values;
  // they are never reconstructed from overlapping optimizer activations.
  const shapers = [
    { label: "Caveman", count: savings.caveman_requests, hint: "Terse output instruction" },
    { label: "Terse", count: savings.terse_requests, hint: "Concise-output directive" },
    { label: "Ponytail", count: savings.ponytail_requests, hint: "Output trimming" },
  ];
  const cells: { label: string; icon: LucideIcon; value: string; hint: string }[] = [
    { label: "Value saved", icon: ICONS.savings, value: fmtUSD(savings.usd_saved), hint: savings.usd_saved_estimate ? "Includes estimates" : "" },
    { label: "Tokens saved", icon: ICONS.tokens, value: fmtNum(savings.total_tokens_saved), hint: `${fmtNum(savings.slim_tokens_saved)} RTK · ${fmtNum(savings.headroom_tokens_saved)} Headroom` },
    { label: "Optimized requests", icon: ICONS.requests, value: fmtNum(savings.optimized_requests), hint: `${share(savings.optimized_requests)} of all requests` },
    { label: "Prompt reduced", icon: ICONS.prompt, value: fmtBytes(savings.slim_bytes_saved), hint: `${fmtNum(savings.saved_tokens_per_optimized_request)} tokens per request` },
  ];

  const titleId = useId();
  return (
    <section aria-labelledby={titleId} className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="border-b border-line px-4 py-3">
        <SectionTitle id={titleId} icon={ICONS.savings} title="Optimization" action={<SavingsCardShareButton insights={insights} period={period} />} />
      </div>
      {/* Kpi cells sit directly in this card's hairline grid (KpiGrid would add a second border). */}
      <dl className="grid grid-cols-1 gap-px border-b border-line bg-line sm:grid-cols-2 xl:grid-cols-4">
        {cells.map((c) => (
          <Kpi
            key={c.label}
            icon={c.icon}
            label={c.label}
            value={c.value}
            hint={c.hint ? <span title={c.hint}>{c.hint}</span> : undefined}
          />
        ))}
      </dl>

      <div className="grid gap-px bg-line lg:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)]">
        <div className="bg-surface px-4 py-4">
          <h3 className="mb-3 text-[12px] font-medium text-fg-muted">Compression rules · by bytes removed</h3>
          {rules.length === 0 ? (
            <p className="rounded-xl border border-dashed border-line-strong px-4 py-6 text-center text-[12.5px] text-fg-muted">
              {savings.slim_tokens_saved + savings.headroom_tokens_saved > 0
                ? "Prompts were compressed; no per-rule breakdown was recorded."
                : savings.optimized_requests > 0
                  ? "Only output shaping was active."
                  : "No compression rule fired in this period."}
            </p>
          ) : (
            <ul className="space-y-2.5">
              {rules.map((r) => (
                <li key={r.rule} className="grid grid-cols-[minmax(0,9rem)_1fr_auto] items-center gap-3 text-[12.5px]">
                  <span className="truncate font-mono text-fg" title={r.rule}>{r.rule}</span>
                  <span className="h-1.5 overflow-hidden rounded-full bg-track" aria-hidden="true">
                    <span className="block h-full rounded-full bg-accent-500" style={{ width: `${Math.max(3, (r.bytes_saved / maxBytes) * 100)}%` }} />
                  </span>
                  <span className="whitespace-nowrap text-right tabular-nums text-fg-muted">
                    <span className="text-fg">{fmtBytes(r.bytes_saved)}</span> · {fmtNum(r.tokens_saved)} tok · {r.count}×
                  </span>
                </li>
              ))}
            </ul>
          )}
        </div>
        <div className="bg-surface px-4 py-4">
          <h3 className="mb-3 text-[12px] font-medium text-fg-muted">Output shaping · share of requests</h3>
          <ul className="divide-y divide-line">
            {shapers.map((s) => (
              <li key={s.label} className="flex items-baseline justify-between gap-3 py-2 text-[13px] first:pt-0">
                <span>
                  <span className="text-fg">{s.label}</span> <span className="text-[12px] text-fg-faint">{s.hint}</span>
                </span>
                <span className="tabular-nums text-fg-muted">
                  {s.count ? (
                    <>
                      <span className="text-fg">{share(s.count)}</span> · {fmtNum(s.count)}
                    </>
                  ) : (
                    "Off"
                  )}
                </span>
              </li>
            ))}
          </ul>
        </div>
      </div>

      <ClientBreakdown clients={savings.by_client || []} />
    </section>
  );
}

// ClientBreakdown attributes savings to the calling clients the backend
// detected — generic across any client, never a fixed list.
function ClientBreakdown({ clients }: { clients: ClientSaving[] }) {
  if (clients.length === 0) return null;
  const sorted = clients.slice().sort((a, b) => b.tokens_saved - a.tokens_saved);
  const maxTokens = Math.max(...sorted.map((c) => c.tokens_saved), 1);
  return (
    <div className="border-t border-line px-4 py-4">
      <h3 className="mb-3 text-[12px] font-medium text-fg-muted">By client</h3>
      <ul className="grid gap-x-6 gap-y-3 sm:grid-cols-2">
        {sorted.map((c) => (
          <li key={c.client} className="flex items-center gap-3">
            <ClientAvatar id={c.client} />
            <div className="min-w-0 flex-1">
              <div className="flex items-baseline justify-between gap-2 text-[12.5px]">
                <span className="truncate font-medium text-fg">{prettyClient(c.client)}</span>
                <span className="shrink-0 tabular-nums text-fg">{fmtUSD(c.usd_saved)}</span>
              </div>
              <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-track" aria-hidden="true">
                <div className="h-full rounded-full bg-accent-500" style={{ width: `${Math.max(3, (c.tokens_saved / maxTokens) * 100)}%` }} />
              </div>
              <p className="mt-1 text-[11.5px] tabular-nums text-fg-faint">
                {fmtNum(c.tokens_saved)} tokens · {fmtNum(c.optimized_requests)} of {fmtNum(c.requests)} requests optimized
              </p>
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}
