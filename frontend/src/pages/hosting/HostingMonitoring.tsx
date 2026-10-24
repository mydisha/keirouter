import { useMemo, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  AlertTriangle,
  Boxes,
  Check,
  ChevronDown,
  Cpu,
  HardDrive,
  ListChecks,
  Loader2,
  MemoryStick,
  Plus,
  RefreshCw,
  Server,
  Settings2,
  ShieldAlert,
  Trash2,
} from "lucide-react";
import { cn } from "@/lib/utils";
import {
  api,
  type HostingContainerMetric,
  type HostingContainerMetrics,
  type HostingMonitorTarget,
  type HostingMonitorTargetInput,
  type HostingServerMetric,
  type HostingServerMetrics,
} from "../../lib/api";
import { useToast } from "../../components/Toast";
import { TimeLines, clockLabel } from "../../components/charts/TimeSeries";
import {
  Badge,
  Button,
  EmptyState,
  ErrorBanner,
  Field,
  Input,
  LoadingRegion,
  SegmentedControl,
  Skeleton,
  Sparkline,
} from "../../components/ui";
import { Panel, TH, THEAD, errorText } from "../payments/shared";
import { QK_HOSTING, STATUS_LABEL, STATUS_TONE, nodeName, num, pct, uptimeID } from "./shared";

const REFRESH_MS = 30_000;
const MAX_TARGETS = 10;

// Same pairing as the System page: machine series in the brand blue, the
// second series in the orange alt token (both themed in index.css).
const C_CPU = "var(--color-accent-500)";
const C_RAM = "var(--series-alt)";

// Meter severity: the fill turns warning, then danger, past these levels.
const WARN_AT = 75;
const BAD_AT = 90;

export function MonitoringTab() {
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: QK_HOSTING.monitorSettings, queryFn: api.getHostingMonitoring });
  const servers = useQuery({
    queryKey: QK_HOSTING.monitorServers,
    queryFn: () => api.hostingServerMetrics(60),
    refetchInterval: REFRESH_MS,
  });
  const containers = useQuery({
    queryKey: QK_HOSTING.monitorContainers,
    queryFn: () => api.hostingContainerMetrics(30),
    refetchInterval: REFRESH_MS,
  });

  const targets = settings.data?.targets ?? [];
  const updatedAt = Math.max(servers.dataUpdatedAt, containers.dataUpdatedAt);
  const fetching = servers.isFetching || containers.isFetching;
  const refresh = () => {
    qc.invalidateQueries({ queryKey: QK_HOSTING.monitorServers });
    qc.invalidateQueries({ queryKey: QK_HOSTING.monitorContainers });
  };

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-[12.5px] text-fg-muted" aria-live="polite">
          {updatedAt > 0 ? (
            <>
              Last updated <span className="tabular-nums text-fg">{new Date(updatedAt).toLocaleTimeString("en-GB", { hour12: false })}</span> · refreshes
              every 30 seconds
            </>
          ) : (
            "Data comes from Dokploy monitoring every 30 seconds."
          )}
        </p>
        <Button variant="secondary" onClick={refresh} disabled={fetching}>
          <RefreshCw className={cn(fetching && "animate-spin")} aria-hidden="true" />
          Refresh
        </Button>
      </div>

      <ServersSection query={servers} hasTargets={targets.length > 0} settingsLoading={settings.isLoading} />
      <ContainersSection query={containers} />
      <MonitorSettingsPanel query={settings} />
    </div>
  );
}

// ─── Servers ────────────────────────────────────────────────────────────────

function ServersSection({
  query,
  hasTargets,
  settingsLoading,
}: {
  query: { data?: { servers: HostingServerMetrics[] }; isLoading: boolean; isError: boolean; error: unknown };
  hasTargets: boolean;
  settingsLoading: boolean;
}) {
  if (query.isLoading) {
    return (
      <LoadingRegion label="Loading server metrics" className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Skeleton className="h-[360px] w-full rounded-2xl" />
        <Skeleton className="hidden h-[360px] w-full rounded-2xl lg:block" />
      </LoadingRegion>
    );
  }
  const list = query.data?.servers ?? [];
  if (!query.data) return <ErrorBanner message={`Couldn't load server metrics. ${errorText(query.error)}`} />;
  if (list.length === 0) {
    return (
      <EmptyState
        variant="card"
        icon={Server}
        title={hasTargets || settingsLoading ? "No server data yet" : "No monitored servers yet"}
        hint="Add a Dokploy monitoring URL and token in Monitoring settings below."
      />
    );
  }
  return (
    <div className="space-y-2">
      {query.isError && <ErrorBanner message={`Couldn't refresh the data. The numbers below may be out of date. ${errorText(query.error)}`} />}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {list.map((s) => (
          <ServerCard key={s.id} s={s} />
        ))}
      </div>
    </div>
  );
}

function sampleTime(ts: string, i: number): number {
  const t = Date.parse(ts);
  return Number.isNaN(t) ? i : t;
}

function ServerCard({ s }: { s: HostingServerMetrics }) {
  const last: HostingServerMetric | undefined = s.samples[s.samples.length - 1];
  const points = useMemo(() => s.samples.map((m, i) => ({ t: sampleTime(m.timestamp, i), cpu: m.cpu, mem: m.memUsed })), [s.samples]);
  const platform = last ? [last.distro || last.os, last.cpuModel, last.cpuCores ? `${last.cpuCores} cores` : ""].filter(Boolean).join(" · ") : "";
  const diskUsedGB = last ? (last.totalDisk * last.diskUsed) / 100 : 0;

  return (
    <Panel
      icon={Server}
      title={s.name}
      subtitle={
        last ? (
          <span className="tabular-nums [overflow-wrap:anywhere]">
            {platform}
            {last.uptime > 0 && ` · up ${uptimeID(last.uptime)}`}
          </span>
        ) : undefined
      }
      action={s.instances ? <Badge tone="accent">Customer server</Badge> : undefined}
    >
      {s.error && (
        <div className="px-4 pt-3">
          <ErrorBanner message={`Couldn't read metrics: ${s.error}`} />
        </div>
      )}
      {!last ? (
        !s.error && <EmptyState icon={Activity} size="sm" title="No samples yet" hint="Dokploy hasn't sent monitoring data for this server yet." />
      ) : (
        <>
          <div className="grid grid-cols-1 divide-y divide-line sm:grid-cols-3 sm:divide-x sm:divide-y-0">
            <Meter icon={Cpu} label="CPU" value={last.cpu} detail={last.cpuCores ? `${last.cpuCores} cores` : ""} />
            <Meter icon={MemoryStick} label="RAM" value={last.memUsed} detail={`${num(last.memUsedGB)} / ${num(last.memTotal)} GB`} />
            <Meter icon={HardDrive} label="Disk" value={last.diskUsed} detail={`${num(diskUsedGB)} / ${num(last.totalDisk)} GB`} />
          </div>
          <div className="border-t border-line px-4 pb-3 pt-3">
            <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
              <p className="text-[12px] font-medium text-fg-muted">CPU and RAM, last {points.length} samples</p>
              <ul className="flex gap-3 text-[12px] text-fg-muted" aria-label="Chart legend">
                <li className="inline-flex items-center gap-1.5">
                  <span className="h-2 w-2 rounded-[2px]" style={{ background: C_CPU }} aria-hidden="true" />
                  CPU
                </li>
                <li className="inline-flex items-center gap-1.5">
                  <span className="h-2 w-2 rounded-[2px]" style={{ background: C_RAM }} aria-hidden="true" />
                  RAM
                </li>
              </ul>
            </div>
            {points.length < 2 ? (
              <p className="py-6 text-center text-[12.5px] text-fg-muted">The chart appears once there are at least two samples.</p>
            ) : (
              <TimeLines
                data={points}
                x={(p) => p.t}
                max={100}
                height={120}
                format={(v) => pct(v, 0)}
                tooltipTime={(ms) => new Date(ms).toLocaleTimeString("en-GB", { hour12: false })}
                xFormat={clockLabel}
                label={`CPU and RAM usage on ${s.name}`}
                series={[
                  // TimeLines breaks the line at 0; keep idle samples on the baseline.
                  { key: "cpu", label: "CPU", color: C_CPU, value: (p) => Math.max(p.cpu, 0.01) },
                  { key: "mem", label: "RAM", color: C_RAM, value: (p) => Math.max(p.mem, 0.01) },
                ]}
              />
            )}
            {(last.networkIn > 0 || last.networkOut > 0) && (
              <p className="mt-2 text-[12px] tabular-nums text-fg-faint">
                Network since boot: in {formatMB(last.networkIn)} · out {formatMB(last.networkOut)}
              </p>
            )}
          </div>
        </>
      )}
    </Panel>
  );
}

function formatMB(mb: number): string {
  return mb >= 1024 ? `${num(mb / 1024)} GB` : `${num(mb)} MB`;
}

function level(v: number): "ok" | "warn" | "bad" {
  return v >= BAD_AT ? "bad" : v >= WARN_AT ? "warn" : "ok";
}

// Meter is one resource: the headline percent, a bar on a fixed 0–100 track,
// and the absolute amount. Severity shows as colour plus an icon and text.
function Meter({ icon: Icon, label, value, detail }: { icon: typeof Cpu; label: string; value: number; detail: string }) {
  const lvl = level(value);
  const clamped = Math.max(0, Math.min(100, value));
  return (
    <div className="px-4 py-3.5">
      <div className="flex items-baseline justify-between gap-2">
        <span className="inline-flex items-center gap-1.5 text-[12.5px] font-medium text-fg-muted">
          <Icon className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
          {label}
        </span>
        <span className={cn("inline-flex items-center gap-1 text-[18px] font-semibold leading-none tabular-nums", lvl === "bad" ? "text-bad" : lvl === "warn" ? "text-warn" : "text-fg")}>
          {lvl !== "ok" && <AlertTriangle className="h-3.5 w-3.5" strokeWidth={2} aria-hidden="true" />}
          {pct(value)}
          {lvl !== "ok" && <span className="sr-only">({lvl === "bad" ? "nearly full" : "high"})</span>}
        </span>
      </div>
      <div
        className="mt-2.5 h-2 overflow-hidden rounded-full bg-track"
        role="meter"
        aria-label={`${label} used`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(clamped)}
      >
        <span
          className={cn("block h-full rounded-full", lvl === "bad" ? "bg-bad" : lvl === "warn" ? "bg-warn" : "bg-accent-500")}
          style={{ width: clamped > 0 ? `max(3px, ${clamped}%)` : 0 }}
        />
      </div>
      {detail && <p className="mt-1.5 text-[12px] tabular-nums text-fg-faint">{detail}</p>}
    </div>
  );
}

// ─── Containers ─────────────────────────────────────────────────────────────

type SortKey = "cpu" | "mem";

function ContainersSection({
  query,
}: {
  query: { data?: { containers: HostingContainerMetrics[]; error?: string }; isLoading: boolean; isError: boolean; error: unknown };
}) {
  const [sort, setSort] = useState<SortKey>("cpu");
  const nodes = useQuery({ queryKey: QK_HOSTING.nodes, queryFn: api.listHostingNodes });
  const data = query.data;
  const rows = useMemo(() => {
    const list = (data?.containers ?? []).map((c) => ({ c, last: c.samples[c.samples.length - 1] as HostingContainerMetric | undefined }));
    const key = (r: (typeof list)[number]) => (r.last ? (sort === "cpu" ? r.last.CPU : r.last.Memory.percentage) : -1);
    return list.sort((a, b) => key(b) - key(a));
  }, [data, sort]);

  return (
    <Panel
      icon={Boxes}
      title="Customer containers"
      subtitle="Resource usage per hosting instance, highest first."
      action={
        <SegmentedControl<SortKey>
          label="Sort by"
          value={sort}
          onChange={setSort}
          options={[
            { value: "cpu", label: "CPU" },
            { value: "mem", label: "Memory" },
          ]}
        />
      }
    >
      {query.isLoading ? (
        <LoadingRegion label="Loading container metrics" className="space-y-2 p-4">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </LoadingRegion>
      ) : !data ? (
        <div className="p-4">
          <ErrorBanner message={`Couldn't load container metrics. ${errorText(query.error)}`} />
        </div>
      ) : (
        <>
          {(data.error || query.isError) && (
            <div className="px-4 pt-3">
              <ErrorBanner message={data.error ? containersError(data.error) : `Couldn't refresh the data. ${errorText(query.error)}`} />
            </div>
          )}
          {rows.length === 0 ? (
            <EmptyState
              icon={Boxes}
              size="sm"
              title="No container data yet"
              hint={
                <>
                  Containers show up only if Dokploy monitors them. Add <code className="font-mono text-fg">nr-</code> to Services to include in
                  Dokploy's Monitoring settings, and mark the server that runs customer containers.
                </>
              }
            />
          ) : (
            <div className="overflow-x-auto" role="region" aria-label="Container usage table" tabIndex={0}>
              <table className="w-full min-w-[1000px] text-[13px]">
                <thead>
                  <tr className={THEAD}>
                    <th scope="col" className={TH}>Instance</th>
                    <th scope="col" className={TH}>Node</th>
                    <th scope="col" className={cn(TH, "text-right")} aria-sort={sort === "cpu" ? "descending" : undefined}>CPU</th>
                    <th scope="col" className={TH}>
                      <span className="sr-only">CPU trend</span>
                    </th>
                    <th scope="col" className={cn(TH, "text-right")} aria-sort={sort === "mem" ? "descending" : undefined}>Memory</th>
                    <th scope="col" className={cn(TH, "text-right")}>Network in / out</th>
                    <th scope="col" className={cn(TH, "text-right")}>Block IO read / write</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {rows.map(({ c, last }) => (
                    <tr key={c.instance_id} className="align-top transition-colors hover:bg-hover">
                      <td className="px-4 py-2.5">
                        <p className="break-all font-mono text-[12px] text-fg">{c.fqdn || c.app_name || c.instance_id}</p>
                        <div className="mt-0.5 flex flex-wrap items-center gap-1.5">
                          {c.app_name && c.fqdn && <span className="font-mono text-[11px] text-fg-muted">{c.app_name}</span>}
                          {c.status && c.status !== "active" && <Badge tone={STATUS_TONE[c.status] ?? "neutral"}>{STATUS_LABEL[c.status] ?? c.status}</Badge>}
                        </div>
                        {c.error && <p className="mt-1 max-w-[320px] break-words text-[11.5px] text-bad">{containerRowError(c.error)}</p>}
                      </td>
                      <td className="px-4 py-2.5">
                        <p className="text-fg">{nodeName(nodes.data?.nodes, c.node_id) || "-"}</p>
                        {c.server && <p className="text-[11.5px] text-fg-muted">Monitoring: {c.server}</p>}
                      </td>
                      {last ? (
                        <>
                          <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums">
                            <span className={cn(level(last.CPU) === "bad" ? "text-bad" : level(last.CPU) === "warn" ? "text-warn" : "text-fg")}>{pct(last.CPU)}</span>
                          </td>
                          <td className="px-2 py-2.5">
                            <Sparkline values={c.samples.map((m) => m.CPU)} />
                          </td>
                          <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums">
                            {num(last.Memory.used)} {last.Memory.usedUnit} / {num(last.Memory.total)} {last.Memory.totalUnit}
                            <p className="text-[11.5px] text-fg-muted">{pct(last.Memory.percentage)}</p>
                          </td>
                          <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums text-fg-muted">
                            {num(last.Network.input)} {last.Network.inputUnit} / {num(last.Network.output)} {last.Network.outputUnit}
                          </td>
                          <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums text-fg-muted">
                            {num(last.BlockIO.read)} {last.BlockIO.readUnit} / {num(last.BlockIO.write)} {last.BlockIO.writeUnit}
                          </td>
                        </>
                      ) : (
                        <td colSpan={5} className="px-4 py-2.5 text-[12.5px] text-fg-muted">
                          {c.error ? "-" : "No samples yet. Check that this container is in Services to include."}
                        </td>
                      )}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </Panel>
  );
}

function containerRowError(msg: string): string {
  if (msg.includes("no monitoring server is set for this node")) {
    return "This node has no monitoring server. Pick one for the node in Hosting settings, or mark a customer server in Monitoring settings.";
  }
  return msg;
}

function containersError(msg: string): string {
  if (msg.includes("no monitoring server is marked")) {
    return "No server is marked as the customer server. Mark one in Monitoring settings.";
  }
  return `Couldn't read container metrics: ${msg}`;
}

// ─── Monitoring settings ────────────────────────────────────────────────────

interface Draft {
  key: string;
  id?: string;
  name: string;
  url: string;
  token: string;
  instances: boolean;
  hasToken: boolean;
  tokenMasked: string;
  origUrl: string;
}

let draftSeq = 0;
const toDraft = (t: HostingMonitorTarget): Draft => ({
  key: t.id,
  id: t.id,
  name: t.name,
  url: t.url,
  token: "",
  instances: t.instances,
  hasToken: t.has_token,
  tokenMasked: t.token_masked,
  origUrl: t.url,
});

function MonitorSettingsPanel({
  query,
}: {
  query: { data?: { targets: HostingMonitorTarget[] }; isLoading: boolean; isError: boolean; error: unknown };
}) {
  const [open, setOpen] = useState<boolean | null>(null);
  // Open by default while nothing is configured yet.
  const expanded = open ?? (!!query.data && query.data.targets.length === 0);
  const bodyId = "hosting-monitor-settings";

  return (
    <Panel
      icon={Settings2}
      title="Monitoring settings"
      subtitle="Monitored Dokploy servers and their metrics tokens."
      action={
        <Button variant="ghost" onClick={() => setOpen(!expanded)} aria-expanded={expanded} aria-controls={bodyId}>
          {expanded ? "Close" : "Open"}
          <ChevronDown className={cn("transition-transform", expanded && "rotate-180")} aria-hidden="true" />
        </Button>
      }
    >
      <div id={bodyId} hidden={!expanded}>
        {query.isLoading ? (
          <LoadingRegion label="Loading monitoring settings" className="p-4">
            <Skeleton className="h-[160px] w-full rounded-xl" />
          </LoadingRegion>
        ) : !query.data ? (
          <div className="p-4">
            <ErrorBanner message={`Couldn't load monitoring settings. ${errorText(query.error)}`} />
          </div>
        ) : (
          <TargetsForm key={JSON.stringify(query.data.targets)} targets={query.data.targets} />
        )}
        <SetupHelp />
      </div>
    </Panel>
  );
}

function TargetsForm({ targets }: { targets: HostingMonitorTarget[] }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [rows, setRows] = useState<Draft[]>(() => targets.map(toDraft));
  const [touched, setTouched] = useState(false);

  const update = (key: string, patch: Partial<Draft>) =>
    setRows((rs) =>
      rs.map((r) => {
        if (r.key === key) return { ...r, ...patch };
        // Only one target may hold customer containers.
        if (patch.instances) return { ...r, instances: false };
        return r;
      }),
    );
  const add = () =>
    setRows((rs) => [
      ...rs,
      { key: `new-${++draftSeq}`, name: "", url: "", token: "", instances: rs.length === 0, hasToken: false, tokenMasked: "", origUrl: "" },
    ]);
  const remove = (key: string) => setRows((rs) => rs.filter((r) => r.key !== key));

  const errorsOf = (r: Draft) => {
    const url = r.url.trim();
    const urlChanged = !!r.id && url.replace(/\/+$/, "") !== r.origUrl.replace(/\/+$/, "");
    return {
      name: !r.name.trim() ? "Enter a server name." : null,
      url: !url ? "Enter the monitoring URL." : !/^https?:\/\/\S+$/i.test(url) ? "URL must start with http:// or https://." : null,
      token:
        r.token.trim() === "" && (!r.hasToken || urlChanged)
          ? urlChanged && r.hasToken
            ? "The URL changed. Enter the token again for the new URL."
            : "Enter the metrics token."
          : null,
    };
  };
  const invalid = rows.some((r) => Object.values(errorsOf(r)).some(Boolean));

  const save = useMutation({
    mutationFn: () =>
      api.putHostingMonitoring(
        rows.map(
          (r): HostingMonitorTargetInput => ({
            ...(r.id ? { id: r.id } : {}),
            name: r.name.trim(),
            url: r.url.trim(),
            ...(r.token.trim() ? { token: r.token.trim() } : {}),
            instances: r.instances,
          }),
        ),
      ),
    onSuccess: (data) => {
      qc.setQueryData(QK_HOSTING.monitorSettings, data);
      qc.invalidateQueries({ queryKey: QK_HOSTING.monitorServers });
      qc.invalidateQueries({ queryKey: QK_HOSTING.monitorContainers });
      toast.success("Monitoring settings saved");
    },
  });

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (invalid || save.isPending) return;
    save.mutate();
  };

  return (
    <form onSubmit={onSubmit} noValidate>
      <div className="space-y-3 px-4 py-4">
        {rows.length === 0 && (
          <p className="rounded-xl border border-dashed border-line-strong px-4 py-6 text-center text-[13px] text-fg-muted">
            No servers yet. Add a Dokploy server that has monitoring turned on.
          </p>
        )}
        {rows.map((r, i) => {
          const err = touched ? errorsOf(r) : { name: null, url: null, token: null };
          const insecure = /^http:\/\//i.test(r.url.trim());
          return (
            <fieldset key={r.key} className="rounded-xl border border-line bg-subtle/60 p-3.5">
              <legend className="sr-only">Server {i + 1}</legend>
              <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-[12.5px] font-medium text-fg">{r.name.trim() || `Server ${i + 1}`}</span>
                  {insecure && (
                    <Badge tone="warning" title="Use HTTPS or restrict the monitoring port with a firewall">
                      <ShieldAlert className="h-3 w-3" aria-hidden="true" />
                      Token sent unencrypted
                    </Badge>
                  )}
                </div>
                <Button type="button" variant="ghost" onClick={() => remove(r.key)} aria-label={`Delete ${r.name.trim() || `server ${i + 1}`}`} title="Delete server">
                  <Trash2 aria-hidden="true" />
                </Button>
              </div>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
                <Field label="Name" error={err.name}>
                  <Input value={r.name} onChange={(e) => update(r.key, { name: e.target.value })} placeholder="Customer VPS 1" autoComplete="off" />
                </Field>
                <Field label="URL monitoring" error={err.url}>
                  <Input
                    type="url"
                    value={r.url}
                    onChange={(e) => update(r.key, { url: e.target.value })}
                    placeholder="http://203.0.113.10:4500"
                    autoComplete="off"
                    spellCheck={false}
                    className="font-mono"
                  />
                </Field>
                <Field label="Token metrics" error={err.token} hint={!err.token && r.hasToken ? "Leave empty to keep the saved token." : undefined}>
                  <Input
                    type="password"
                    value={r.token}
                    onChange={(e) => update(r.key, { token: e.target.value })}
                    placeholder={r.hasToken ? r.tokenMasked : "Paste token"}
                    autoComplete="new-password"
                    spellCheck={false}
                    className="font-mono"
                  />
                </Field>
              </div>
              <label className="mt-3 inline-flex cursor-pointer items-center gap-2 text-[12.5px] text-fg">
                <input
                  type="radio"
                  name="hosting-monitor-instances"
                  checked={r.instances}
                  onChange={() => update(r.key, { instances: true })}
                  className="h-4 w-4 accent-[var(--color-accent-500)]"
                />
                Customer server
                <span className="text-fg-muted">(runs hosting containers)</span>
              </label>
            </fieldset>
          );
        })}
        {save.isError && <ErrorBanner message={`Couldn't save monitoring settings. ${errorText(save.error)}`} />}
      </div>
      <div className="flex flex-col gap-2 border-t border-line bg-subtle px-4 py-2.5 sm:flex-row sm:items-center sm:justify-end">
        <Button type="button" variant="ghost" className="sm:mr-auto" onClick={add} disabled={rows.length >= MAX_TARGETS}>
          <Plus aria-hidden="true" />
          Add server
          {rows.length >= MAX_TARGETS && <span className="text-fg-faint">(max {MAX_TARGETS})</span>}
        </Button>
        {rows.some((r) => r.instances) || rows.length === 0 ? null : (
          <span className="text-[12px] text-fg-muted">Mark one customer server to show container metrics.</span>
        )}
        <Button type="submit" disabled={(touched && invalid) || save.isPending}>
          {save.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Check aria-hidden="true" />}
          Save
        </Button>
      </div>
    </form>
  );
}

function SetupHelp() {
  return (
    <div className="border-t border-line px-4 py-4">
      <p className="mb-2 inline-flex items-center gap-2 text-[12.5px] font-medium text-fg">
        <ListChecks className="h-4 w-4 text-tone" strokeWidth={1.75} aria-hidden="true" />
        Setup
      </p>
      <ol className="list-decimal space-y-1.5 pl-5 text-[13px] text-fg-muted">
        <li>
          Turn on Monitoring in Dokploy (<span className="text-fg">Web Server</span> or <span className="text-fg">Servers → Monitoring</span>), then note the port
          (default <span className="font-mono text-fg">4500</span>) and the metrics token.
        </li>
        <li>
          In <span className="text-fg">Services to include</span>, add <code className="font-mono text-fg">nr-</code> so every hosting container is
          monitored. Any service whose name contains that text matches.
        </li>
        <li>
          Open the monitoring port only to the KeiRouter server IP with a firewall. Without HTTPS, the token travels over plain HTTP.
        </li>
        <li>Enter the URL and token here, then mark the server that runs customer containers.</li>
      </ol>
    </div>
  );
}
