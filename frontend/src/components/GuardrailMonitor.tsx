import { useMemo, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import {
  Ban,
  EyeOff,
  Fingerprint,
  Gavel,
  MessageSquareWarning,
  NotebookPen,
  Scale,
  ShieldAlert,
  ShieldCheck,
  Tags,
  TriangleAlert,
  X,
  type LucideIcon,
} from "lucide-react";
import { api, type GuardrailLogFilter, type GuardrailStats, type GuardrailTargetStat } from "../lib/api";
import { REPORT_PERIODS, type ReportPeriod } from "../lib/periods";
import { compactNumber, formatSpendUSD, formatTokens } from "../lib/format";
import { cn } from "@/lib/utils";
import { ICONS } from "../lib/icons";
import { ChartCard, TimeBars, clockLabel, dayLabel } from "./charts/TimeSeries";
import { useScopeTargetLabels } from "./ScopeIDSelector";
import { ErrorCard, EmptyState, Kpi, KpiGrid, SectionTitle, SegmentedControl, Skeleton } from "./ui";
import { dashboard } from "../lib/dashboardRoutes";

// Guardrail monitoring: what share of traffic the guardrails blocked, masked
// or warned on, which detectors and entities drove it, and what they cost in
// latency and LLM judge spend. Request counts are per request (one usage row
// each), so they are true rates; detector rows come from the audit log, where
// one request can produce several decisions.

const FOCUS_RING = "focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

// Series colours are CSS variables so they follow the theme.
const C = {
  blocked: "var(--status-bad)",
  masked: "var(--color-accent-500)",
  warned: "var(--status-warn)",
  logOnly: "var(--color-chart-5)",
};

const BUCKETS: Record<ReportPeriod, number> = { today: 24, "24h": 24, "7d": 42, "30d": 30, "90d": 45 };

const PERIOD_TEXT: Record<ReportPeriod, string> = {
  today: "today",
  "24h": "in the last 24 hours",
  "7d": "in the last 7 days",
  "30d": "in the last 30 days",
  "90d": "in the last 90 days",
};

const DETECTOR_META: Record<string, { label: string; icon: LucideIcon }> = {
  pii: { label: "PII", icon: Fingerprint },
  injection: { label: "Injection", icon: ShieldAlert },
  topics: { label: "Topics", icon: Tags },
  toxicity: { label: "Toxicity", icon: MessageSquareWarning },
  bias: { label: "Bias", icon: Scale },
};

const ACTION_LABEL: Record<string, string> = {
  block: "Block",
  mask: "Mask",
  warn: "Warn",
  log_only: "Log only",
};

const ACTION_COLOR: Record<string, string> = {
  block: C.blocked,
  mask: C.masked,
  warn: C.warned,
  log_only: C.logOnly,
};

export function isReportPeriod(v: string | null): v is ReportPeriod {
  return !!v && REPORT_PERIODS.some((p) => p.value === v);
}

const pct = (n: number) => {
  if (n <= 0) return "0%";
  if (n < 0.001) return "<0.1%";
  return `${(n * 100).toFixed(n < 0.1 ? 1 : 0)}%`;
};

const fmtMs = (v: number) => (v < 1000 ? `${Math.round(v)} ms` : `${(v / 1000).toFixed(2)} s`);

export function useGuardrailStats(period: ReportPeriod, filter: { api_key_id?: string; model?: string } = {}) {
  return useQuery({
    queryKey: ["guardrail-stats", period, filter.api_key_id ?? "", filter.model ?? ""],
    queryFn: () => api.guardrailStats(period, { buckets: BUCKETS[period], ...filter }),
    staleTime: 15_000,
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  });
}

// ── Full overview tab ────────────────────────────────────────────────────────

export function GuardrailOverview({
  period,
  onPeriod,
  filter,
  onClearFilter,
  onDrill,
}: {
  period: ReportPeriod;
  onPeriod: (p: ReportPeriod) => void;
  /** Narrows every number to one API key or model. */
  filter: { api_key_id?: string; model?: string };
  onClearFilter: (key: "api_key_id" | "model") => void;
  /** Opens the audit log with these filters. */
  onDrill: (f: GuardrailLogFilter) => void;
}) {
  const stats = useGuardrailStats(period, filter);
  const keyLabel = useScopeTargetLabels(["apikey"]);
  const drill = (f: GuardrailLogFilter) => onDrill({ ...filter, ...f, period });

  const chips = [
    filter.api_key_id && { key: "api_key_id" as const, label: `Key: ${keyLabel("apikey", filter.api_key_id) ?? filter.api_key_id}` },
    filter.model && { key: "model" as const, label: `Model: ${filter.model}` },
  ].filter(Boolean) as { key: "api_key_id" | "model"; label: string }[];

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-2">
          {chips.map((c) => (
            <span key={c.key} className="inline-flex h-7 items-center gap-1 rounded-lg border border-line bg-subtle pl-2.5 pr-1 text-[12px] font-medium text-fg">
              {c.label}
              <button
                type="button"
                onClick={() => onClearFilter(c.key)}
                aria-label={`Remove filter ${c.label}`}
                className={cn("inline-flex h-5 w-5 items-center justify-center rounded text-fg-faint hover:bg-hover hover:text-fg", FOCUS_RING)}
              >
                <X className="h-3.5 w-3.5" strokeWidth={2} aria-hidden="true" />
              </button>
            </span>
          ))}
        </div>
        <SegmentedControl
          label="Time range"
          value={period}
          onChange={onPeriod}
          options={REPORT_PERIODS.map((p) => ({ value: p.value, label: p.label }))}
        />
      </div>

      {stats.isLoading ? (
        <OverviewSkeleton />
      ) : stats.isError || !stats.data ? (
        <ErrorCard message={`Couldn't load guardrail stats. ${stats.error instanceof Error ? stats.error.message : ""}`.trim()} />
      ) : (
        <OverviewBody stats={stats.data} period={period} keyLabel={(id) => keyLabel("apikey", id)} onDrill={drill} />
      )}
    </div>
  );
}

function OverviewSkeleton() {
  return (
    <div className="space-y-4" aria-busy="true" aria-label="Loading guardrail stats">
      <Skeleton className="h-24 w-full rounded-2xl" />
      <Skeleton className="h-64 w-full rounded-2xl" />
      <div className="grid gap-4 lg:grid-cols-2">
        <Skeleton className="h-56 w-full rounded-2xl" />
        <Skeleton className="h-56 w-full rounded-2xl" />
      </div>
    </div>
  );
}

function OverviewBody({
  stats,
  period,
  keyLabel,
  onDrill,
}: {
  stats: GuardrailStats;
  period: ReportPeriod;
  keyLabel: (id: string) => string | undefined;
  onDrill: (f: GuardrailLogFilter) => void;
}) {
  const t = stats.totals;
  const dq = stats.data_quality;

  if (t.requests === 0 && stats.judge.calls === 0) {
    return (
      <EmptyState
        variant="card"
        icon={ICONS.guardrails}
        title={`No traffic ${PERIOD_TEXT[period]}`}
        hint="Guardrail activity appears here once requests flow through the gateway."
      />
    );
  }

  return (
    <>
      <DataQualityNotes stats={stats} />

      <KpiGrid cols={6} label="Guardrail summary">
        <Kpi
          icon={ShieldCheck}
          label="Checked"
          value={compactNumber(t.evaluated)}
          hint={`of ${compactNumber(t.requests)} requests (${pct(t.requests ? t.evaluated / t.requests : 0)})`}
        />
        <Kpi
          icon={Ban}
          label="Blocked"
          value={compactNumber(t.blocked)}
          tone={t.blocked > 0 ? "bad" : "section"}
          hint={`${pct(t.block_rate)} · ${t.blocked_inbound} in · ${t.blocked_outbound} out`}
        />
        <Kpi icon={EyeOff} label="Masked" value={compactNumber(t.masked)} hint={`${pct(t.mask_rate)} of requests`} />
        <Kpi
          icon={TriangleAlert}
          label="Warned"
          value={compactNumber(t.warned)}
          tone={t.warned > 0 ? "warn" : "section"}
          hint={`${pct(t.warn_rate)} · ${compactNumber(t.log_only)} log only`}
        />
        <Kpi
          icon={ICONS.latency}
          label="Overhead p95"
          value={t.evaluated ? fmtMs(t.overhead_p95_ms) : "—"}
          hint={t.evaluated ? `p50 ${fmtMs(t.overhead_p50_ms)} · avg ${fmtMs(t.overhead_avg_ms)}` : "No checked requests"}
        />
        <Kpi
          icon={Gavel}
          label="LLM judge"
          value={formatSpendUSD(stats.judge.cost_nanos / 1e9)}
          hint={`${compactNumber(stats.judge.calls)} calls · ${formatTokens(stats.judge.tokens)} tokens`}
        />
      </KpiGrid>

      {t.blocked_mid_stream > 0 && (
        <p role="status" className="flex items-start gap-2 rounded-xl border border-warn/30 bg-warn/5 px-3.5 py-2.5 text-[12.5px] text-fg">
          <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0 text-warn" strokeWidth={1.75} aria-hidden="true" />
          <span>
            <span className="font-medium">{t.blocked_mid_stream.toLocaleString()} streamed {t.blocked_mid_stream === 1 ? "answer was" : "answers were"} cut mid-stream.</span>{" "}
            <span className="text-fg-muted">The client had already received part of the response before the block.</span>
          </span>
        </p>
      )}

      <TrendCard stats={stats} period={period} />

      <div className="grid gap-4 lg:grid-cols-2">
        <DetectorCard rows={stats.by_detector} onDrill={onDrill} />
        <ReasonsCard rows={stats.top_reasons} onDrill={onDrill} />
        <TargetCard
          title="API keys most affected"
          icon={ICONS.keys}
          rows={stats.top_api_keys}
          label={(k) => (k ? keyLabel(k) ?? k : "No key")}
          onSelect={(k) => onDrill({ api_key_id: k || undefined })}
        />
        <TargetCard
          title="Models most affected"
          icon={ICONS.model}
          rows={stats.top_models}
          label={(k) => k || "Unknown"}
          onSelect={(k) => onDrill({ model: k })}
        />
        <EntitiesCard rows={stats.top_entities} sampled={dq.entities_sampled} className="lg:col-span-2" />
      </div>
    </>
  );
}

function DataQualityNotes({ stats }: { stats: GuardrailStats }) {
  const dq = stats.data_quality;
  const notes: ReactNode[] = [];
  if (dq.audit_dropped > 0) {
    notes.push(
      <>
        <span className="font-medium">{dq.audit_dropped.toLocaleString()} audit rows were dropped</span> since the gateway started because the
        audit buffer was full. Detector breakdowns undercount; request totals are unaffected.
      </>,
    );
  }
  if (dq.logs_truncated) {
    notes.push(
      <>
        Audit logs are kept for <span className="font-medium">{dq.log_retention_days} days</span>, so detector and entity breakdowns only cover
        that part of this range.
      </>,
    );
  }
  if (!notes.length) return null;
  return (
    <div role="status" className="space-y-1.5 rounded-xl border border-line bg-subtle px-3.5 py-2.5 text-[12.5px] text-fg-muted">
      {notes.map((n, i) => (
        <p key={i} className="flex items-start gap-2">
          <NotebookPen className="mt-0.5 h-3.5 w-3.5 shrink-0 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
          <span>{n}</span>
        </p>
      ))}
    </div>
  );
}

function TrendCard({ stats, period }: { stats: GuardrailStats; period: ReportPeriod }) {
  // The API returns only non-empty buckets; lay them onto the full axis.
  const data = useMemo(() => {
    const start = new Date(stats.since).getTime();
    const slot = Math.max(60_000, Math.floor((Date.now() - start) / stats.buckets));
    const byBucket = new Map(stats.timeline.map((b) => [b.bucket, b]));
    return Array.from({ length: stats.buckets }, (_, i) => {
      const b = byBucket.get(i);
      return {
        start: start + i * slot,
        requests: b?.requests ?? 0,
        blocked: b?.blocked ?? 0,
        masked: b?.masked ?? 0,
        warned: b?.warned ?? 0,
        logOnly: b?.log_only ?? 0,
      };
    });
  }, [stats]);
  const long = period === "7d" || period === "30d" || period === "90d";
  const t = stats.totals;
  return (
    <ChartCard
      icon={ICONS.trend}
      title="Guardrail actions over time"
      subtitle={`${(t.blocked + t.masked + t.warned + t.log_only).toLocaleString()} of ${t.requests.toLocaleString()} requests affected`}
      legend={[
        { label: "Blocked", color: C.blocked },
        { label: "Masked", color: C.masked },
        { label: "Warned", color: C.warned },
        { label: "Log only", color: C.logOnly },
      ]}
    >
      <TimeBars
        data={data}
        x={(b) => b.start}
        xFormat={long ? dayLabel : clockLabel}
        tooltipTime={(ms) =>
          new Date(ms).toLocaleString(undefined, long ? { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" } : { hour: "2-digit", minute: "2-digit" })
        }
        label="Requests the guardrails acted on, over time"
        series={[
          { key: "blocked", label: "Blocked", color: C.blocked, value: (b) => b.blocked },
          { key: "masked", label: "Masked", color: C.masked, value: (b) => b.masked },
          { key: "warned", label: "Warned", color: C.warned, value: (b) => b.warned },
          { key: "logOnly", label: "Log only", color: C.logOnly, value: (b) => b.logOnly },
        ]}
      />
    </ChartCard>
  );
}

// Card chrome for the breakdown lists.
function ListCard({ title, icon, subtitle, className, children }: { title: string; icon: LucideIcon; subtitle?: ReactNode; className?: string; children: ReactNode }) {
  return (
    <section className={cn("min-w-0 overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]", className)}>
      <div className="border-b border-line px-4 py-3">
        <SectionTitle icon={icon} title={title} subtitle={subtitle} />
      </div>
      {children}
    </section>
  );
}

function EmptyList({ text }: { text: string }) {
  return <p className="px-4 py-8 text-center text-[12.5px] text-fg-muted">{text}</p>;
}

const rowButton = cn("flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors hover:bg-hover", FOCUS_RING, "focus-visible:ring-inset");

function ActionDot({ action }: { action: string }) {
  return <span className="h-2 w-2 shrink-0 rounded-[2px]" style={{ background: ACTION_COLOR[action] ?? C.logOnly }} aria-hidden="true" />;
}

function DetectorCard({ rows, onDrill }: { rows: GuardrailStats["by_detector"]; onDrill: (f: GuardrailLogFilter) => void }) {
  const peak = Math.max(1, ...rows.map((r) => r.requests));
  return (
    <ListCard title="By detector" icon={ICONS.guardrails} subtitle="Requests per detector, direction and action">
      {rows.length === 0 ? (
        <EmptyList text="No detector fired in this range." />
      ) : (
        <ul className="divide-y divide-line">
          {rows.map((r) => {
            const meta = DETECTOR_META[r.detector];
            const Icon = meta?.icon ?? ICONS.guardrails;
            return (
              <li key={`${r.detector}-${r.direction}-${r.action}`}>
                <button
                  type="button"
                  className={rowButton}
                  onClick={() => onDrill({ detector: r.detector, action: r.action, direction: r.direction })}
                  aria-label={`${meta?.label ?? r.detector} ${r.direction} ${ACTION_LABEL[r.action] ?? r.action}: ${r.requests} requests. Open in audit log`}
                >
                  <Icon className="h-4 w-4 shrink-0 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center gap-2 text-[13px] text-fg">
                      <span className="font-medium">{meta?.label ?? r.detector}</span>
                      <span className="text-[12px] text-fg-muted">{r.direction === "inbound" ? "prompt" : "response"}</span>
                    </span>
                    <span className="mt-1 block h-1 overflow-hidden rounded-full bg-subtle" aria-hidden="true">
                      <span className="block h-full rounded-full" style={{ width: `${(r.requests / peak) * 100}%`, background: ACTION_COLOR[r.action] ?? C.logOnly }} />
                    </span>
                  </span>
                  <span className="inline-flex w-20 shrink-0 items-center gap-1.5 text-[12px] text-fg-muted">
                    <ActionDot action={r.action} />
                    {ACTION_LABEL[r.action] ?? r.action}
                  </span>
                  <span className="w-16 shrink-0 text-right text-[13px] font-medium tabular-nums text-fg" title={`${r.decisions.toLocaleString()} decisions`}>
                    {r.requests.toLocaleString()}
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </ListCard>
  );
}

function ReasonsCard({ rows, onDrill }: { rows: GuardrailStats["top_reasons"]; onDrill: (f: GuardrailLogFilter) => void }) {
  return (
    <ListCard title="Top reasons" icon={ICONS.checklist} subtitle="Most frequent decision reasons">
      {rows.length === 0 ? (
        <EmptyList text="No decisions in this range." />
      ) : (
        <ul className="divide-y divide-line">
          {rows.map((r) => (
            <li key={`${r.detector}-${r.action}-${r.reason}`}>
              <button type="button" className={rowButton} onClick={() => onDrill({ detector: r.detector, action: r.action })}>
                <ActionDot action={r.action} />
                <span className="min-w-0 flex-1 truncate text-[13px] text-fg" title={r.reason}>
                  {r.reason}
                </span>
                <span className="w-16 shrink-0 text-right text-[13px] font-medium tabular-nums text-fg">{r.requests.toLocaleString()}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </ListCard>
  );
}

function TargetCard({
  title,
  icon,
  rows,
  label,
  onSelect,
}: {
  title: string;
  icon: LucideIcon;
  rows: GuardrailTargetStat[];
  label: (key: string) => string;
  onSelect: (key: string) => void;
}) {
  return (
    <ListCard title={title} icon={icon} subtitle="Ranked by blocked, then masked requests">
      {rows.length === 0 ? (
        <EmptyList text="Nothing was blocked, masked or warned on." />
      ) : (
        <table className="w-full table-fixed text-[13px]">
            <thead>
              <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                <th scope="col" className="px-4 py-2 font-medium">Name</th>
                <th scope="col" className="w-[4.5rem] px-2 py-2 text-right font-medium">Blocked</th>
                <th scope="col" className="w-[4.5rem] px-2 py-2 text-right font-medium">Masked</th>
                <th scope="col" className="w-24 whitespace-nowrap px-4 py-2 text-right font-medium">Block rate</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {rows.map((r) => (
                <tr key={r.key} className="transition-colors hover:bg-hover">
                  <td className="px-4 py-2">
                    <button
                      type="button"
                      onClick={() => onSelect(r.key)}
                      className={cn("block max-w-full truncate rounded text-left font-medium text-link hover:underline", FOCUS_RING)}
                      title={`Open ${label(r.key)} in audit log`}
                    >
                      {label(r.key)}
                    </button>
                  </td>
                  <td className={cn("px-2 py-2 text-right tabular-nums", r.blocked > 0 ? "font-medium text-bad" : "text-fg-muted")}>{r.blocked.toLocaleString()}</td>
                  <td className="px-2 py-2 text-right tabular-nums text-fg-muted">{r.masked.toLocaleString()}</td>
                  <td className="px-4 py-2 text-right tabular-nums text-fg" title={`${r.blocked.toLocaleString()} of ${r.requests.toLocaleString()} requests`}>
                    {pct(r.requests ? r.blocked / r.requests : 0)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
      )}
    </ListCard>
  );
}

function EntitiesCard({ rows, sampled, className }: { rows: GuardrailStats["top_entities"]; sampled: boolean; className?: string }) {
  const peak = Math.max(1, ...rows.map((r) => r.count));
  return (
    <ListCard
      title="Detected entities"
      icon={Fingerprint}
      subtitle={sampled ? "Findings in the most recent 5,000 audit rows" : "Findings across every decision"}
      className={className}
    >
      {rows.length === 0 ? (
        <EmptyList text="No findings in this range." />
      ) : (
        <ul className="grid gap-x-6 gap-y-2.5 px-4 py-3 sm:grid-cols-2">
          {rows.map((r) => (
            <li key={r.entity} className="min-w-0">
              <div className="flex items-baseline justify-between gap-3 text-[12.5px]">
                <span className="truncate font-mono text-fg" title={r.entity}>{r.entity}</span>
                <span className="shrink-0 tabular-nums text-fg-muted">{r.count.toLocaleString()}</span>
              </div>
              <div className="mt-1 h-1 overflow-hidden rounded-full bg-subtle" aria-hidden="true">
                <div className="h-full rounded-full bg-accent-500" style={{ width: `${(r.count / peak) * 100}%` }} />
              </div>
            </li>
          ))}
        </ul>
      )}
    </ListCard>
  );
}

// ── Compact views ────────────────────────────────────────────────────────────

/** Guardrail impact on one API key, for the key's Guardrails tab. */
export function GuardrailKeyStats({ apiKeyId }: { apiKeyId: string }) {
  const stats = useGuardrailStats("7d", { api_key_id: apiKeyId });
  const t = stats.data?.totals;
  return (
    <section className="space-y-3">
      <SectionTitle
        icon={ICONS.trend}
        title="Last 7 days"
        subtitle="What the guardrails did to this key's traffic"
        action={
          <Link to={dashboard(`/guardrails?api_key_id=${encodeURIComponent(apiKeyId)}&period=7d`)} className={cn("rounded text-[12.5px] font-medium text-link hover:underline", FOCUS_RING)}>
            Open monitoring
          </Link>
        }
      />
      {stats.isLoading ? (
        <Skeleton className="h-20 w-full rounded-2xl" />
      ) : !t ? (
        <ErrorCard message="Couldn't load guardrail stats for this key." />
      ) : (
        <KpiGrid cols={4} label="Guardrail impact on this key">
          <Kpi icon={Ban} label="Blocked" value={compactNumber(t.blocked)} tone={t.blocked > 0 ? "bad" : "section"} hint={`${pct(t.block_rate)} of ${compactNumber(t.requests)} requests`} />
          <Kpi icon={EyeOff} label="Masked" value={compactNumber(t.masked)} hint={pct(t.mask_rate)} />
          <Kpi icon={TriangleAlert} label="Warned" value={compactNumber(t.warned + t.log_only)} hint={`${compactNumber(t.log_only)} log only`} />
          <Kpi icon={ICONS.latency} label="Overhead p95" value={t.evaluated ? fmtMs(t.overhead_p95_ms) : "—"} hint={t.evaluated ? `avg ${fmtMs(t.overhead_avg_ms)}` : "No checked requests"} />
        </KpiGrid>
      )}
    </section>
  );
}

/** One-line guardrail summary for the Overview page; hidden until guardrails act. */
export function GuardrailsNotice({ period, periodText }: { period: ReportPeriod; periodText: string }) {
  const stats = useGuardrailStats(period);
  const t = stats.data?.totals;
  if (!t || t.blocked + t.masked + t.warned === 0) return null;
  const parts = [
    t.blocked > 0 && `blocked ${t.blocked.toLocaleString()} (${pct(t.block_rate)})`,
    t.masked > 0 && `masked ${t.masked.toLocaleString()}`,
    t.warned > 0 && `warned on ${t.warned.toLocaleString()}`,
  ].filter(Boolean) as string[];
  const summary = parts.length > 1 ? `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}` : parts[0];
  return (
    <div role="status" className="mb-6 flex items-center gap-3 rounded-2xl border border-line bg-surface px-4 py-2.5 text-[13px] shadow-[var(--shadow-card)]">
      <ICONS.guardrails className={cn("h-4 w-4 shrink-0", t.blocked > 0 ? "text-bad" : "text-tone")} strokeWidth={1.75} aria-hidden="true" />
      <p className="min-w-0 flex-1 truncate">
        <span className="font-medium text-fg">Guardrails {summary} requests</span> <span className="text-fg-muted">{periodText.toLowerCase()}.</span>
      </p>
      <Link to={dashboard(`/guardrails?period=${period}`)} className={cn("inline-flex min-h-6 shrink-0 items-center rounded-md text-[12px] font-medium text-link hover:underline", FOCUS_RING)}>
        View details
      </Link>
    </div>
  );
}
