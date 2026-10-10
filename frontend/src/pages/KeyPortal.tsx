import { useState, useEffect, useId, useMemo, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { fetchKeyUsage, fetchKeyUsageById, APIError, type KeyUsageData, type PortalRecentRequest } from "../lib/api";
import { useBranding } from "../contexts/BrandingContext";
import { AlertCircle, AlertTriangle, Key, Loader2, LogOut, RefreshCw, Send } from "lucide-react";
import { Button, Input, Select, Badge, Skeleton, TablePagination, useClientPagination } from "../components/ui";
import { BrandMark } from "../components/BrandMark";
import { ProviderLogo } from "../components/ProviderLogo";
import { ChartCard, TimeBars, dayLabel, type TimeSeriesDef } from "../components/charts/TimeSeries";
import { cn } from "@/lib/utils";

// Public key portal: a key holder opens /portal with their API key (or a
// shared portal ID) and sees that key's usage, spend and budget limits.
// No dashboard chrome; white-labelled through PortalBrandingProvider.

// Series colours: accent carries the data, a lighter accent shade separates
// the second stacked series. Charts take CSS colour strings, hence the vars.
const C_PRIMARY = "var(--color-accent-500)";
const C_SOFT = "var(--color-accent-300)";

const FOCUS_RING = "focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

// The portal API takes a day count; these are every range it supports.
const PERIODS = [
  { value: 7, label: "7d" },
  { value: 14, label: "14d" },
  { value: 30, label: "30d" },
  { value: 90, label: "90d" },
];

type PortalTab = "usage" | "playground";
const PORTAL_TABS: { value: PortalTab; label: string }[] = [
  { value: "usage", label: "Usage" },
  { value: "playground", label: "Playground" },
];
type DailyPoint = NonNullable<KeyUsageData["daily"]>[number];
type ModelRow = NonNullable<KeyUsageData["models"]>[number];
type Budget = KeyUsageData["budgets"][number];

export function KeyPortalPage() {
  const [params, setParams] = useSearchParams();
  const tabsId = useId();
  const activeId = params.get("id") || "";
  const activeKey = params.get("key") || "";
  const [apiKeyInput, setApiKeyInput] = useState(activeKey || activeId);
  const [selectedModel, setSelectedModel] = useState("");
  const [testPrompt, setTestPrompt] = useState("Say hello in one sentence");
  const [testResponse, setTestResponse] = useState<any>(null);
  const [isTesting, setIsTesting] = useState(false);
  const [days, setDays] = useState(30);

  const authValue = activeId || activeKey;
  const isIdMode = !!activeId;

  const handleLogin = (e: React.FormEvent) => {
    e.preventDefault();
    const val = apiKeyInput.trim();
    if (val) {
      if (val.startsWith("sk-")) setParams({ key: val });
      else setParams({ id: val });
    }
  };

  const handleLogout = () => {
    setParams({});
    setApiKeyInput("");
  };

  const { data, isLoading, isError, error, dataUpdatedAt, isFetching, refetch } = useQuery({
    queryKey: ["key-usage", authValue, isIdMode, days],
    queryFn: () => (isIdMode ? fetchKeyUsageById(authValue, days) : fetchKeyUsage(authValue, days)),
    enabled: !!authValue,
    retry: false,
    refetchInterval: 30000,
  });

  useEffect(() => {
    if (data?.allowed_models?.length && !selectedModel) {
      setSelectedModel(data.allowed_models[0]);
    }
  }, [data]);

  if (!authValue) {
    return <SignInScreen value={apiKeyInput} onChange={setApiKeyInput} onSubmit={handleLogin} />;
  }

  if (isLoading) {
    return (
      <PortalShell actions={<ForgetKeyButton onClick={handleLogout} />}>
        <PortalSkeleton />
      </PortalShell>
    );
  }

  if (isError) {
    let msg = "Authentication failed or server error.";
    if (error instanceof APIError) msg = error.message;
    return (
      <CenteredScreen>
        <div className="flex flex-col items-center text-center" role="alert">
          <AlertCircle className="h-6 w-6 text-bad" strokeWidth={1.75} aria-hidden="true" />
          <h1 className="mt-3 text-[15px] font-semibold tracking-[-0.01em] text-fg">Couldn't open this key</h1>
          <p className="mt-1 text-[13px] text-fg-muted">{msg}</p>
          <p className="mt-1 text-[13px] text-fg-muted">Check the key is still active, or ask for a new link.</p>
        </div>
        <div className="mt-6 flex gap-2">
          <Button variant="ghost" className="flex-1" onClick={() => refetch()}>
            Try again
          </Button>
          <Button className="flex-1" onClick={handleLogout}>
            Use a different key
          </Button>
        </div>
      </CenteredScreen>
    );
  }

  const d = data!;
  const canPlay = !isIdMode && !!activeKey && !!d.allowed_models && d.allowed_models.length > 0;
  const tab: PortalTab = canPlay && params.get("tab") === "playground" ? "playground" : "usage";
  const setTab = (t: PortalTab) =>
    setParams(
      (p) => {
        if (t === "usage") p.delete("tab");
        else p.set("tab", t);
        return p;
      },
      { replace: true },
    );

  const onTabKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (!["ArrowRight", "ArrowLeft", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const i = PORTAL_TABS.findIndex((t) => t.value === tab);
    const n = PORTAL_TABS.length;
    const next = event.key === "Home" ? 0 : event.key === "End" ? n - 1 : (i + (event.key === "ArrowRight" ? 1 : -1) + n) % n;
    setTab(PORTAL_TABS[next].value);
    document.getElementById(`${tabsId}-tab-${PORTAL_TABS[next].value}`)?.focus();
  };

  const body =
    tab === "playground" ? (
      <PlaygroundSection
        allowedModels={d.allowed_models}
        activeKey={activeKey}
        selectedModel={selectedModel}
        setSelectedModel={setSelectedModel}
        testPrompt={testPrompt}
        setTestPrompt={setTestPrompt}
        testResponse={testResponse}
        setTestResponse={setTestResponse}
        isTesting={isTesting}
        setIsTesting={setIsTesting}
      />
    ) : (
      <div className="space-y-5">
        <BudgetNotice budgets={d.budgets ?? []} />
        <KpiStrip d={d} />

        <div className="grid gap-5 xl:grid-cols-3">
          <TrendCard daily={d.daily ?? []} className="xl:col-span-2" />
          <LimitsCard budgets={d.budgets ?? []} allowedModels={d.allowed_models ?? []} />
        </div>

        <ModelsCard models={d.models ?? []} />
        <RecentRequestsCard recent={d.recent ?? []} />
      </div>
    );

  return (
    <PortalShell
      identity={<KeyIdentity secret={isIdMode ? d.key_id : maskKey(activeKey)} mode={isIdMode ? "Shared link" : "API key"} />}
      actions={
        <>
          <LiveIndicator updatedAt={dataUpdatedAt} refreshing={isFetching} />
          <ForgetKeyButton onClick={handleLogout} />
        </>
      }
    >
      <div className="mb-5 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <h1 className="min-w-0 truncate text-[22px] font-semibold tracking-[-0.02em] text-fg">{d.key_name}</h1>
        {tab === "usage" && (
          <Segmented
            label="Time range"
            value={days}
            onChange={setDays}
            options={PERIODS}
            mono
          />
        )}
      </div>

      {canPlay ? (
        <>
          <div className="mb-5 flex gap-1 border-b border-line" role="tablist" aria-label="Portal sections" onKeyDown={onTabKeyDown}>
            {PORTAL_TABS.map(({ value, label }) => {
              const active = tab === value;
              return (
                <button
                  key={value}
                  id={`${tabsId}-tab-${value}`}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  aria-controls={`${tabsId}-panel-${value}`}
                  tabIndex={active ? 0 : -1}
                  onClick={() => setTab(value)}
                  className={cn(
                    "relative -mb-px inline-flex items-center gap-1.5 px-3 py-2.5 text-[13px] font-medium transition-colors",
                    FOCUS_RING,
                    active ? "text-fg" : "text-fg-muted hover:text-fg",
                  )}
                >
                  {label}
                  {active && <span aria-hidden="true" className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-accent-500" />}
                </button>
              );
            })}
          </div>
          <div role="tabpanel" id={`${tabsId}-panel-${tab}`} aria-labelledby={`${tabsId}-tab-${tab}`}>
            {body}
          </div>
        </>
      ) : (
        body
      )}

      <footer className="mt-8 border-t border-line pt-4 text-[12px] text-fg-muted">
        Refreshes every 30 seconds · budgets reset at 00:00 UTC
      </footer>
    </PortalShell>
  );
}

// ── Shell: top bar + centred column ──────────────────────────────────────────

function PortalShell({ identity, actions, children }: { identity?: ReactNode; actions?: ReactNode; children: ReactNode }) {
  return (
    <div className="min-h-screen bg-canvas">
      <header className="sticky top-0 z-10 border-b border-line bg-surface">
        <div className="mx-auto flex h-14 max-w-[1200px] items-center gap-3 px-4 sm:px-6">
          <BrandLockup />
          <span className="hidden h-4 w-px bg-line sm:block" aria-hidden="true" />
          <span className="hidden text-[13px] text-fg-muted sm:inline">Usage portal</span>
          <div className="ml-auto flex min-w-0 items-center gap-2">
            {identity}
            {actions}
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-[1200px] px-4 py-6 sm:px-6">{children}</main>
    </div>
  );
}

// Custom logos render as-is; the built-in identity is the mark plus the name.
function BrandLockup({ large }: { large?: boolean }) {
  const { branding, logoSrc } = useBranding();
  const name = branding.name || "KeiRouter";
  if (branding.logo_url) {
    return <img src={logoSrc} alt={name} className={cn("w-auto object-contain", large ? "h-12 max-w-[200px]" : "h-6 max-w-[140px]")} />;
  }
  return (
    <span className="inline-flex shrink-0 items-center gap-2">
      <BrandMark size={large ? 26 : 22} />
      <span className={cn("font-semibold tracking-[-0.01em] text-fg", large ? "text-[16px]" : "text-[14px]")}>{name}</span>
    </span>
  );
}

function KeyIdentity({ secret, mode }: { secret: string; mode: string }) {
  return (
    <div className="hidden h-8 min-w-0 items-center overflow-hidden rounded-lg border border-line bg-surface md:inline-flex">
      <span className="flex h-full items-center gap-1.5 border-r border-line bg-subtle px-2.5 text-[12px] text-fg-muted">
        <Key className="h-3.5 w-3.5 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
        {mode}
      </span>
      <span className="truncate px-2.5 font-mono text-[12px] text-fg-muted">{secret}</span>
    </div>
  );
}

function ForgetKeyButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label="Forget key and sign out"
      className={cn(
        "inline-flex h-8 min-w-8 shrink-0 items-center justify-center gap-1.5 rounded-lg border border-line bg-surface px-2.5 text-[12.5px] font-medium text-fg transition-colors hover:border-line-strong hover:bg-hover",
        FOCUS_RING,
      )}
    >
      <LogOut className="h-3.5 w-3.5 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
      <span className="hidden sm:inline">Forget key</span>
    </button>
  );
}

function LiveIndicator({ updatedAt, refreshing }: { updatedAt?: number; refreshing?: boolean }) {
  const [, force] = useState(0);
  useEffect(() => {
    const t = setInterval(() => force((n) => n + 1), 15000);
    return () => clearInterval(t);
  }, []);
  const label = updatedAt ? relativeTime(updatedAt) : "just now";
  return (
    <span className="hidden h-8 items-center gap-1.5 rounded-lg border border-line px-2.5 text-[12px] font-medium text-fg-muted sm:inline-flex">
      {refreshing ? (
        <RefreshCw className="h-3 w-3 animate-spin text-fg-faint" aria-hidden="true" />
      ) : (
        <span className="live-dot h-1.5 w-1.5 rounded-full bg-ok" aria-hidden="true" />
      )}
      Updated {label}
    </span>
  );
}

function CenteredScreen({ children }: { children: ReactNode }) {
  return (
    <main className="flex min-h-screen items-center justify-center bg-canvas px-4">
      <div className="w-full max-w-sm rounded-2xl border border-line bg-surface p-6 shadow-[var(--shadow-pop)] sm:p-8">{children}</div>
    </main>
  );
}

// ── Sign-in ──────────────────────────────────────────────────────────────────

function SignInScreen({ value, onChange, onSubmit }: { value: string; onChange: (v: string) => void; onSubmit: (e: React.FormEvent) => void }) {
  const { branding } = useBranding();
  const uid = useId();
  const inputId = `${uid}-key`;
  const hintId = `${uid}-hint`;
  return (
    <CenteredScreen>
      <div className="mb-6 flex flex-col items-center text-center">
        <BrandLockup large />
        <h1 className="mt-4 text-lg font-semibold tracking-tight text-fg">View your key's usage</h1>
        {branding.tagline && <p className="mt-1 text-sm text-fg-muted">{branding.tagline}</p>}
      </div>
      <form onSubmit={onSubmit} className="space-y-4">
        <div className="space-y-1.5">
          <label htmlFor={inputId} className="block text-[12.5px] font-medium text-fg">
            API key or portal ID
          </label>
          <Input
            id={inputId}
            type="password"
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder="sk-… or key_…"
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
            required
            aria-required="true"
            aria-describedby={hintId}
            autoFocus
          />
          <p id={hintId} className="text-[12px] text-fg-muted">A portal ID is read-only. An API key also opens the playground.</p>
        </div>
        <Button type="submit" className="w-full" disabled={!value.trim()}>
          View usage
        </Button>
      </form>
    </CenteredScreen>
  );
}

// ── Controls ─────────────────────────────────────────────────────────────────

function Segmented<T extends string | number>({
  label,
  value,
  onChange,
  options,
  mono,
}: {
  label: string;
  value: T;
  onChange: (v: T) => void;
  options: { value: T; label: string }[];
  mono?: boolean;
}) {
  return (
    <div className="inline-flex h-8 shrink-0 items-center self-start rounded-xl border border-line bg-subtle p-0.5 sm:self-auto" role="radiogroup" aria-label={label}>
      {options.map((o) => {
        const active = o.value === value;
        return (
          <button
            key={String(o.value)}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => onChange(o.value)}
            className={cn(
              "h-full min-w-8 rounded-lg px-2.5 text-[12px] font-medium transition-colors",
              FOCUS_RING,
              mono && "font-mono",
              active ? "bg-surface text-fg ring-1 ring-line-strong" : "text-fg-muted hover:text-fg",
            )}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

// ── Card chrome ──────────────────────────────────────────────────────────────

function Panel({ title, count, className, children }: { title: string; count?: number; className?: string; children: ReactNode }) {
  const headingId = useId();
  return (
    <section aria-labelledby={headingId} className={cn("min-w-0 overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]", className)}>
      <div className="flex items-center justify-between gap-3 border-b border-line px-4 py-3">
        <h2 id={headingId} className="flex items-center gap-2 text-[13px] font-semibold tracking-[-0.005em] text-fg">
          {title}
          {count != null && <span className="rounded-md bg-subtle px-1.5 text-[11.5px] font-medium tabular-nums text-fg-muted">{count}</span>}
        </h2>
      </div>
      {children}
    </section>
  );
}

function PanelEmpty({ title, hint }: { title: string; hint?: string }) {
  return (
    <div className="flex flex-col items-center justify-center gap-1 px-6 py-10 text-center">
      <p className="text-[13px] font-medium text-fg">{title}</p>
      {hint && <p className="max-w-sm text-[12.5px] text-fg-muted">{hint}</p>}
    </div>
  );
}

// ── Budget alert banner ──────────────────────────────────────────────────────

function BudgetNotice({ budgets }: { budgets: Budget[] }) {
  const reached = budgets.filter((b) => budgetPct(b) >= 100);
  const near = budgets.filter((b) => b.alert && budgetPct(b) < 100);
  if (reached.length === 0 && near.length === 0) return null;
  const blocked = reached.length > 0;
  const list = (blocked ? reached : near).map((b) => `${periodLabel(b.period)} ${Math.round(budgetPct(b))}%`).join(" · ");
  return (
    <div role="status" className={cn("flex items-center gap-3 rounded-2xl border px-4 py-2.5 text-[13px]", blocked ? "border-bad/30 bg-bad/5" : "border-warn/30 bg-warn/5")}>
      <AlertTriangle className={cn("h-4 w-4 shrink-0", blocked ? "text-bad" : "text-warn")} strokeWidth={1.75} aria-hidden="true" />
      <p className="min-w-0 flex-1">
        <span className="font-medium text-fg">{blocked ? "Budget used up. Requests may be rejected until it resets." : "Budget alert threshold crossed."}</span>{" "}
        <span className="tabular-nums text-fg-muted">{list}</span>
      </p>
    </div>
  );
}

// ── KPI strip ────────────────────────────────────────────────────────────────

function KpiStrip({ d }: { d: KeyUsageData }) {
  const daily = d.daily ?? [];
  const t = useMemo(() => aggregate(daily), [daily]);
  const totalTokens = t.prompt + t.completion;

  const items: { label: string; value: string; sub?: string }[] = [
    { label: "Requests", value: fmtInt(t.requests) },
    { label: "Spend", value: fmtUSD(t.cost) },
    { label: "Tokens", value: fmtTokens(totalTokens), sub: `${fmtTokens(t.prompt)} in · ${fmtTokens(t.completion)} out` },
    { label: "This month", value: fmtUSD(d.current_period.cost_usd), sub: `${fmtInt(d.current_period.total_requests)} requests` },
  ];

  return (
    <section aria-label="Key metrics" className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <dl className="grid grid-cols-2 gap-px bg-line lg:grid-cols-4">
        {items.map((item) => (
          <div key={item.label} className="flex min-w-0 flex-col gap-1 bg-surface px-4 py-3">
            <dt className="text-[12px] font-medium text-fg-muted">{item.label}</dt>
            <dd className="whitespace-nowrap text-[22px] font-semibold leading-tight tracking-[-0.02em] tabular-nums text-fg">{item.value}</dd>
            {item.sub && <dd className="truncate text-[12px] tabular-nums text-fg-muted">{item.sub}</dd>}
          </div>
        ))}
      </dl>
    </section>
  );
}

// ── Usage trend ──────────────────────────────────────────────────────────────

type Metric = "tokens" | "requests" | "cost";
type TrendPoint = DailyPoint & { t: number };

const TREND_SERIES: Record<Metric, TimeSeriesDef<TrendPoint>[]> = {
  tokens: [
    { key: "in", label: "Input", color: C_PRIMARY, value: (p) => p.prompt_tokens },
    { key: "out", label: "Output", color: C_SOFT, value: (p) => p.completion_tokens },
  ],
  requests: [{ key: "req", label: "Requests", color: C_PRIMARY, value: (p) => p.requests }],
  cost: [{ key: "cost", label: "Spend", color: C_PRIMARY, value: (p) => p.cost_usd }],
};

const TREND_FORMAT: Record<Metric, (v: number) => string> = {
  tokens: fmtTokens,
  requests: fmtCompact,
  cost: fmtUSD,
};

function TrendCard({ daily, className }: { daily: DailyPoint[]; className?: string }) {
  const [metric, setMetric] = useState<Metric>("tokens");
  const chartData = useMemo<TrendPoint[]>(() => daily.map((dp) => ({ ...dp, t: parseDay(dp.date) })), [daily]);
  const series = TREND_SERIES[metric];

  return (
    <ChartCard
      title="Daily usage"
      legend={series.length > 1 ? series.map((s) => ({ label: s.label, color: s.color })) : undefined}
      action={
        <Segmented<Metric>
          label="Chart metric"
          value={metric}
          onChange={setMetric}
          options={[
            { value: "tokens", label: "Tokens" },
            { value: "requests", label: "Requests" },
            { value: "cost", label: "Spend" },
          ]}
        />
      }
      className={className}
    >
      <TimeBars
        data={chartData}
        x={(p) => p.t}
        series={series}
        format={TREND_FORMAT[metric]}
        xFormat={dayLabel}
        tooltipTime={(ms) => new Date(ms).toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" })}
        label={`Daily ${metric === "cost" ? "spend" : metric} for this key`}
        height={220}
      />
    </ChartCard>
  );
}

// ── Limits: budgets + allowed models ─────────────────────────────────────────

function LimitsCard({ budgets, allowedModels }: { budgets: Budget[]; allowedModels: string[] }) {
  const rows = budgets.flatMap((b, i) => {
    const out: { id: string; title: string; used: string; limit: string; left: string; pct: number; alert: boolean; period: string }[] = [];
    if (b.limit_tokens > 0) {
      out.push({
        id: `${i}-tokens`,
        title: `${periodLabel(b.period)} tokens`,
        used: fmtTokens(b.tokens_used),
        limit: fmtTokens(b.limit_tokens),
        left: `${fmtTokens(b.tokens_remaining)} left`,
        pct: b.tokens_pct_used,
        alert: b.alert,
        period: b.period,
      });
    }
    if (b.limit_usd > 0) {
      out.push({
        id: `${i}-usd`,
        title: `${periodLabel(b.period)} spend`,
        used: fmtUSD(b.spent_usd),
        limit: fmtUSD(b.limit_usd),
        left: `${fmtUSD(b.usd_remaining)} left`,
        pct: b.usd_pct_used,
        alert: b.alert,
        period: b.period,
      });
    }
    return out;
  });

  return (
    <Panel title="Limits" className="flex flex-col">
      {rows.length === 0 ? (
        <p className="px-4 py-3 text-[13px] text-fg-muted">No spend or token limit.</p>
      ) : (
        <ul className="divide-y divide-line">
          {rows.map((r) => {
            const pct = Math.min(Math.max(r.pct, 0), 100);
            const tone: "bad" | "warn" | "ok" = r.pct >= 100 ? "bad" : r.alert || r.pct > 80 ? "warn" : "ok";
            const reset = nextReset(r.period);
            const pctText = r.pct >= 100 ? "Limit reached" : `${r.pct.toFixed(r.pct < 10 ? 1 : 0)}%`;
            return (
              <li key={r.id} className="flex flex-col gap-1.5 px-4 py-3">
                <div className="flex items-baseline gap-2">
                  <span className="truncate text-[13px] font-medium text-fg">{r.title}</span>
                  <span className={cn("ml-auto text-[12.5px] font-medium tabular-nums", tone === "bad" ? "text-bad" : tone === "warn" ? "text-warn" : "text-fg")}>
                    {pctText}
                  </span>
                </div>
                <div
                  className="h-1.5 overflow-hidden rounded-full bg-track"
                  role="progressbar"
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-valuenow={Math.round(pct)}
                  aria-valuetext={`${r.used} of ${r.limit} · ${pctText}`}
                  aria-label={r.title}
                >
                  <div className={cn("h-full rounded-full", tone === "bad" ? "bg-bad" : tone === "warn" ? "bg-warn" : "bg-accent-500")} style={{ width: `${pct}%` }} />
                </div>
                <div className="flex justify-between gap-2 text-[12px] tabular-nums text-fg-muted">
                  <span className="truncate">
                    <span className="text-fg">{r.used}</span> of {r.limit} · {r.left}
                  </span>
                  <span className="shrink-0" title={reset ? reset.toLocaleString() : undefined}>
                    {reset ? `Resets ${relativeFuture(reset)}` : "Never resets"}
                  </span>
                </div>
              </li>
            );
          })}
        </ul>
      )}

      <div className="mt-auto border-t border-line px-4 py-3">
        <h3 className="text-[12.5px] font-medium text-fg">
          Allowed models
          {allowedModels.length > 0 && <span className="ml-1.5 font-normal tabular-nums text-fg-muted">{allowedModels.length}</span>}
        </h3>
        {allowedModels.length === 0 ? (
          <p className="mt-1 text-[12.5px] text-fg-muted">Every model is allowed.</p>
        ) : (
          <ul className="mt-2 flex max-h-40 flex-wrap gap-1.5 overflow-y-auto">
            {allowedModels.map((m) => (
              <li key={m} className="inline-flex h-6 items-center rounded-md border border-line bg-subtle px-2 font-mono text-[12px] text-fg">
                {m}
              </li>
            ))}
          </ul>
        )}
      </div>
    </Panel>
  );
}

// ── Models ───────────────────────────────────────────────────────────────────

function ModelsCard({ models }: { models: ModelRow[] }) {
  const sorted = useMemo(() => [...models].sort((a, b) => b.total_requests - a.total_requests), [models]);
  const totals = useMemo(
    () =>
      sorted.reduce(
        (acc, m) => ({
          requests: acc.requests + m.total_requests,
          prompt: acc.prompt + m.prompt_tokens,
          completion: acc.completion + m.completion_tokens,
          cost: acc.cost + m.cost_usd,
        }),
        { requests: 0, prompt: 0, completion: 0, cost: 0 },
      ),
    [sorted],
  );

  return (
    <Panel title="Models" count={sorted.length}>
      {sorted.length === 0 ? (
        <PanelEmpty title="No model usage in this period" />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[720px] text-[13px]">
            <thead>
              <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                <th scope="col" className="px-4 py-2 font-medium">Model</th>
                <th scope="col" className="w-[26%] px-4 py-2 font-medium">Requests</th>
                <th scope="col" className="px-4 py-2 text-right font-medium">Input</th>
                <th scope="col" className="px-4 py-2 text-right font-medium">Output</th>
                <th scope="col" className="px-4 py-2 text-right font-medium">Avg / request</th>
                <th scope="col" className="px-4 py-2 text-right font-medium">Cost</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {sorted.map((m, i) => {
                const share = totals.requests ? (m.total_requests / totals.requests) * 100 : 0;
                const avg = m.total_requests ? Math.round((m.prompt_tokens + m.completion_tokens) / m.total_requests) : 0;
                return (
                  <tr key={i} className="transition-colors hover:bg-hover">
                    <td className="px-4 py-2.5">
                      <ModelCell provider={m.provider} model={m.model} />
                    </td>
                    <td className="px-4 py-2.5">
                      <div className="flex items-center gap-2.5">
                        <span className="w-12 tabular-nums text-fg">{fmtInt(m.total_requests)}</span>
                        <div className="h-1.5 min-w-[48px] flex-1 overflow-hidden rounded-full bg-track" aria-hidden="true">
                          <div className="h-full rounded-full bg-accent-500" style={{ width: `${Math.max(share, 2)}%` }} />
                        </div>
                        <span className="w-9 text-right text-[12px] tabular-nums text-fg-muted">{share.toFixed(0)}%</span>
                      </div>
                    </td>
                    <td className="px-4 py-2.5 text-right tabular-nums text-fg-muted">{fmtTokens(m.prompt_tokens)}</td>
                    <td className="px-4 py-2.5 text-right tabular-nums text-fg-muted">{fmtTokens(m.completion_tokens)}</td>
                    <td className="px-4 py-2.5 text-right tabular-nums text-fg-muted">{fmtTokens(avg)}</td>
                    <td className={cn("px-4 py-2.5 text-right font-medium tabular-nums", m.cost_usd > 0 ? "text-fg" : "text-fg-muted")}>{fmtUSD(m.cost_usd)}</td>
                  </tr>
                );
              })}
            </tbody>
            {sorted.length > 1 && (
              <tfoot>
                <tr className="border-t border-line bg-subtle text-[12.5px]">
                  <th scope="row" className="px-4 py-2.5 text-left font-medium text-fg">Total</th>
                  <td className="px-4 py-2.5 font-medium tabular-nums text-fg">{fmtInt(totals.requests)}</td>
                  <td className="px-4 py-2.5 text-right tabular-nums text-fg-muted">{fmtTokens(totals.prompt)}</td>
                  <td className="px-4 py-2.5 text-right tabular-nums text-fg-muted">{fmtTokens(totals.completion)}</td>
                  <td className="px-4 py-2.5" />
                  <td className="px-4 py-2.5 text-right font-medium tabular-nums text-fg">{fmtUSD(totals.cost)}</td>
                </tr>
              </tfoot>
            )}
          </table>
        </div>
      )}
    </Panel>
  );
}

function ModelCell({ provider, model }: { provider: string; model: string }) {
  return (
    <span className="flex min-w-0 items-center gap-2.5">
      <ProviderLogo icon={`/providers/${provider}.png`} name={provider} size={20} />
      <span className="flex min-w-0 flex-col leading-tight">
        <span className="truncate font-mono text-[12.5px] text-fg" title={`${provider}/${model}`}>{model}</span>
        <span className="truncate text-[11.5px] capitalize text-fg-muted">{provider}</span>
      </span>
    </span>
  );
}

// ── Request log ──────────────────────────────────────────────────────────────

function RecentRequestsCard({ recent }: { recent: PortalRecentRequest[] }) {
  const { paged, page, pages, setPage, total } = useClientPagination(recent, 15);

  return (
    <Panel title="Recent requests" count={recent.length}>
      {recent.length === 0 ? (
        <PanelEmpty title="No requests in this period" />
      ) : (
        <>
          <div className="overflow-x-auto">
            <table className="w-full min-w-[820px] text-[12.5px]">
              <thead>
                <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                  <th scope="col" className="px-4 py-2 font-medium">Time</th>
                  <th scope="col" className="px-4 py-2 font-medium">Model</th>
                  <th scope="col" className="px-4 py-2 text-right font-medium">Tokens in → out</th>
                  <th scope="col" className="px-4 py-2 text-right font-medium">Cost</th>
                  <th scope="col" className="px-4 py-2 text-right font-medium">Latency</th>
                  <th scope="col" className="px-4 py-2 font-medium">Optimizations</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {paged.map((r) => {
                  const opts = r.optimizations ?? [];
                  const rel = relTime(r.created_at);
                  return (
                    <tr key={r.id} className="transition-colors hover:bg-hover">
                      <td className="whitespace-nowrap px-4 py-2" title={new Date(r.created_at).toLocaleString()}>
                        <span className="font-mono text-[12px] text-fg">{formatDateTime(r.created_at)}</span>
                        {rel !== "—" && <span className="ml-2 text-[12px] text-fg-muted">{rel} ago</span>}
                      </td>
                      <td className="max-w-[320px] px-4 py-2">
                        <ModelCell provider={r.provider} model={r.model} />
                      </td>
                      <td className="whitespace-nowrap px-4 py-2 text-right tabular-nums text-fg-muted">
                        {fmtTokens(r.prompt_tokens)} → {fmtTokens(r.completion_tokens)}
                      </td>
                      <td className="whitespace-nowrap px-4 py-2 text-right tabular-nums text-fg">{fmtUSD(r.cost_usd)}</td>
                      <td className="whitespace-nowrap px-4 py-2 text-right tabular-nums text-fg" title={r.ttft_ms ? `First token after ${r.ttft_ms}ms` : undefined}>
                        {r.latency_ms > 0 ? `${r.latency_ms}ms` : r.cache_hit ? <Badge>Cache</Badge> : <span className="text-fg-muted">—</span>}
                      </td>
                      <td className="px-4 py-2">
                        {opts.length > 0 ? (
                          <div className="flex flex-wrap items-center gap-1">
                            {opts.map((opt) => (
                              <Badge key={opt} title={optDetail(r, opt)}>{opt}</Badge>
                            ))}
                          </div>
                        ) : (
                          <span className="text-fg-muted">—</span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <TablePagination page={page} pages={pages} total={total} onPage={setPage} />
        </>
      )}
    </Panel>
  );
}

function optDetail(r: PortalRecentRequest, name: string): string {
  switch (name) {
    case "RTK":
      return r.slim_rules ? `RTK rules: ${r.slim_rules}${r.slim_tokens_saved ? ` (${fmtTokens(r.slim_tokens_saved)} tokens saved)` : ""}` : "RTK compression";
    case "Caveman":
      return "Caveman output compression";
    case "Terse":
      return "Terse output compression";
    case "Headroom":
      return r.headroom_tokens_saved ? `Headroom saved ${fmtTokens(r.headroom_tokens_saved)} tokens` : "Headroom compression";
    case "Ponytail":
      return "Ponytail injection";
    default:
      return name;
  }
}

// ── Playground ───────────────────────────────────────────────────────────────

function PlaygroundSection({
  allowedModels, activeKey, selectedModel, setSelectedModel, testPrompt, setTestPrompt,
  testResponse, setTestResponse, isTesting, setIsTesting,
}: {
  allowedModels: string[]; activeKey: string; selectedModel: string; setSelectedModel: (v: string) => void;
  testPrompt: string; setTestPrompt: (v: string) => void; testResponse: any; setTestResponse: (v: any) => void;
  isTesting: boolean; setIsTesting: (v: boolean) => void;
}) {
  const uid = useId();
  const send = () => {
    setIsTesting(true);
    setTestResponse(null);
    fetch("/v1/chat/completions", {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${activeKey}` },
      body: JSON.stringify({ model: selectedModel, messages: [{ role: "user", content: testPrompt }], stream: false }),
    })
      .then((r) => r.json())
      .then((json) => {
        if (json.error) setTestResponse({ error: json.error.message || "Request failed" });
        else setTestResponse(json);
      })
      .catch((err) => setTestResponse({ error: err.message }))
      .finally(() => setIsTesting(false));
  };

  return (
    <Panel title="Playground" className="max-w-3xl">
      <form
        className="space-y-4 p-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (selectedModel && testPrompt.trim() && !isTesting) send();
        }}
      >
        <div className="space-y-1.5">
          <label htmlFor={`${uid}-model`} className="block text-[12.5px] font-medium text-fg">Model</label>
          <Select id={`${uid}-model`} value={selectedModel} onChange={(e) => setSelectedModel(e.target.value)} className="h-9 font-mono">
            {allowedModels.map((m) => <option key={m} value={m}>{m}</option>)}
          </Select>
        </div>
        <div className="space-y-1.5">
          <label htmlFor={`${uid}-prompt`} className="block text-[12.5px] font-medium text-fg">Prompt</label>
          <textarea
            id={`${uid}-prompt`}
            value={testPrompt}
            onChange={(e) => setTestPrompt(e.target.value)}
            rows={4}
            required
            aria-describedby={`${uid}-note`}
            className={cn("w-full resize-y rounded-lg border border-input bg-surface px-3 py-2 text-[13px] text-fg transition-[border-color,box-shadow] placeholder:text-fg-faint hover:border-fg-faint focus:border-accent-500", FOCUS_RING)}
            placeholder="Enter your message…"
          />
        </div>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p id={`${uid}-note`} className="text-[12px] text-fg-muted">Counts toward this key's usage.</p>
          <Button type="submit" disabled={!selectedModel || !testPrompt.trim() || isTesting}>
            {isTesting ? <><Loader2 className="animate-spin" aria-hidden="true" /> Sending…</> : <><Send aria-hidden="true" /> Send request</>}
          </Button>
        </div>
      </form>

      <div role="status" aria-live="polite" aria-busy={isTesting}>
        {testResponse && (
          <div className="border-t border-line bg-subtle px-4 py-3">
            <div className="mb-2 flex items-center gap-2">
              <h3 className="text-[12.5px] font-medium text-fg">Response</h3>
              {testResponse.error ? <Badge tone="danger">Error</Badge> : testResponse.model && <span className="font-mono text-[12px] text-fg-muted">{testResponse.model}</span>}
            </div>
            {testResponse.error ? (
              <p className="text-[13px] text-bad">{testResponse.error}</p>
            ) : (
              <>
                <div className="whitespace-pre-wrap rounded-lg border border-line bg-surface px-3 py-2.5 text-[13px] leading-relaxed text-fg">
                  {testResponse.choices?.[0]?.message?.content || "No response content"}
                </div>
                {testResponse.usage && (
                  <p className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[12px] tabular-nums text-fg-muted">
                    <span>Prompt {testResponse.usage.prompt_tokens?.toLocaleString()}</span>
                    <span>Completion {testResponse.usage.completion_tokens?.toLocaleString()}</span>
                    <span>Total {testResponse.usage.total_tokens?.toLocaleString()} tokens</span>
                  </p>
                )}
              </>
            )}
          </div>
        )}
      </div>
    </Panel>
  );
}

// ── Loading ──────────────────────────────────────────────────────────────────

function PortalSkeleton() {
  return (
    <div className="space-y-5" aria-busy="true" aria-label="Loading usage">
      <Skeleton className="h-7 w-48" />
      <Skeleton className="h-[88px] w-full rounded-2xl" />
      <div className="grid gap-5 xl:grid-cols-3">
        <Skeleton className="h-[310px] rounded-2xl xl:col-span-2" />
        <Skeleton className="h-[310px] rounded-2xl" />
      </div>
      <Skeleton className="h-[240px] w-full rounded-2xl" />
    </div>
  );
}

// ── Utilities ────────────────────────────────────────────────────────────────

function aggregate(daily: DailyPoint[]) {
  return daily.reduce(
    (acc, dp) => ({
      requests: acc.requests + dp.requests,
      prompt: acc.prompt + dp.prompt_tokens,
      completion: acc.completion + dp.completion_tokens,
      cost: acc.cost + dp.cost_usd,
    }),
    { requests: 0, prompt: 0, completion: 0, cost: 0 },
  );
}

// Daily buckets arrive as YYYY-MM-DD; read them as local calendar days so the
// axis label never slips to the previous day west of UTC.
function parseDay(date: string): number {
  const [y, m, d] = date.split("-").map(Number);
  if (!y || !m || !d) return new Date(date).getTime();
  return new Date(y, m - 1, d).getTime();
}

function periodLabel(period: string): string {
  if (period === "total") return "All-time";
  return period.charAt(0).toUpperCase() + period.slice(1);
}

function budgetPct(b: Budget): number {
  return Math.max(b.limit_usd > 0 ? b.usd_pct_used : 0, b.limit_tokens > 0 ? b.tokens_pct_used : 0);
}

// Budget periods start at 00:00 UTC (daily), Monday 00:00 UTC (weekly) and the
// 1st of the month 00:00 UTC (monthly); "total" never resets.
function nextReset(period: string): Date | null {
  const now = new Date();
  const y = now.getUTCFullYear();
  const m = now.getUTCMonth();
  const d = now.getUTCDate();
  switch (period) {
    case "daily":
      return new Date(Date.UTC(y, m, d + 1));
    case "weekly": {
      const offset = (now.getUTCDay() + 6) % 7;
      return new Date(Date.UTC(y, m, d - offset + 7));
    }
    case "monthly":
      return new Date(Date.UTC(y, m + 1, 1));
    default:
      return null;
  }
}

function relativeFuture(at: Date): string {
  const ms = at.getTime() - Date.now();
  if (ms <= 0) return "now";
  const h = Math.floor(ms / 3_600_000);
  const m = Math.floor((ms % 3_600_000) / 60_000);
  if (h >= 48) return `in ${Math.round(h / 24)}d`;
  if (h > 0) return `in ${h}h ${m}m`;
  return `in ${Math.max(1, m)}m`;
}

function maskKey(key: string): string {
  if (key.length <= 12) return `${key.slice(0, 3)}…`;
  return `${key.slice(0, 6)}…${key.slice(-4)}`;
}

function fmtTokens(n: number): string {
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(1)}B`;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return Math.round(n).toLocaleString();
}

function fmtInt(n: number): string {
  return Math.round(n || 0).toLocaleString();
}

function fmtCompact(v: number): string {
  const n = v || 0;
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)}B`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(n >= 1e4 ? 0 : 1)}k`;
  return String(Math.round(n * 10) / 10);
}

// Two decimals for amounts a person reads as money; more precision only for
// sub-cent values, so a single request still shows a meaningful cost.
function fmtUSD(v: number): string {
  const n = v || 0;
  if (n === 0) return "$0.00";
  if (n < 0.01) return `$${n.toPrecision(2)}`;
  return `$${n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function relativeTime(ts: number): string {
  const s = Math.max(0, Math.floor((Date.now() - ts) / 1000));
  if (s < 10) return "just now";
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  return `${Math.floor(m / 60)}h ago`;
}

function relTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "—";
  const diff = Date.now() - t;
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.floor(h / 24)}d`;
}

function formatDateTime(iso: string): string {
  const t = new Date(iso);
  if (Number.isNaN(t.getTime())) return "—";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${t.getFullYear()}-${pad(t.getMonth() + 1)}-${pad(t.getDate())} ${pad(t.getHours())}:${pad(t.getMinutes())}`;
}
