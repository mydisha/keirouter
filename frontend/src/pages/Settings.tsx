import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Sparkles, Zap, MessageSquare, Layers, Route, Wifi, Monitor, Database, Clock,
  ArrowUpCircle, CheckCircle2, ExternalLink,
  Gauge, Eye, EyeOff, KeyRound, Download, Upload, ShieldCheck, Info,
  Palette, Shield, Cpu,
} from "lucide-react";
import { api, type EndpointSettings, type BrandingSettings } from "../lib/api";
import { ChangelogMarkdown } from "../components/ChangelogMarkdown";
import { PALETTES, getPaletteScales } from "../lib/palettes";
import { applyShadeScale, generateShades } from "../lib/color-utils";
import { PageHeader } from "../components/Layout";
import { useUpdateInfo } from "../components/UpdateNotification";
import { useToast } from "../components/Toast";
import {
  Card, SectionHeader, Spinner, Toggle, SegmentedControl, ErrorBanner, Button, Input, Field,
  TabBar, Modal,
} from "../components/ui";

// ── Tab definitions ─────────────────────────────────────────────────
type SettingsTab = "saving" | "routing" | "network" | "branding" | "system";

const settingsTabs = [
  { value: "saving" as const, label: "Token Saving", icon: Zap },
  { value: "routing" as const, label: "Routing", icon: Route },
  { value: "network" as const, label: "Network", icon: Gauge },
  { value: "branding" as const, label: "Branding", icon: Palette },
  { value: "system" as const, label: "System", icon: Database },
];

function useHashTab(defaultTab: SettingsTab): [SettingsTab, (t: SettingsTab) => void] {
  const validTabs: SettingsTab[] = settingsTabs.map((t) => t.value);

  const readHash = (): SettingsTab => {
    const hash = window.location.hash.replace("#", "");
    return validTabs.includes(hash as SettingsTab) ? (hash as SettingsTab) : defaultTab;
  };

  const [tab, setTabState] = useState<SettingsTab>(readHash);

  useEffect(() => {
    const onHash = () => setTabState(readHash);
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);

  const setTab = (t: SettingsTab) => {
    window.history.replaceState(null, "", `#${t}`);
    setTabState(t);
  };

  return [tab, setTab];
}

// ── Caveman / Terse options ─────────────────────────────────────────
const cavemanOptions = [
  { value: "lite", label: "Gentle" },
  { value: "full", label: "Balanced" },
  { value: "ultra", label: "Strong" },
  { value: "wenyan-lite", label: "Wenyan" },
  { value: "wenyan-full", label: "Wenyan Full" },
  { value: "wenyan-ultra", label: "Wenyan Ultra" },
];
const cavemanHints: Record<string, string> = {
  lite: "Drop filler, keep full sentences.",
  full: "Terse caveman style, fragments OK.",
  ultra: "Maximum compression, telegraphic.",
  "wenyan-lite": "Semi-classical, concise phrasing.",
  "wenyan-full": "Full 文言文 style, 80-90% reduction.",
  "wenyan-ultra": "Extreme abbreviation + classical feel.",
};

const terseOptions = [
  { value: "light", label: "Gentle" },
  { value: "medium", label: "Balanced" },
  { value: "aggressive", label: "Strong" },
];
const terseHints: Record<string, string> = {
  light: "Trim pleasantries.",
  medium: "Bullets, minimal prose.",
  aggressive: "Bare technical minimum.",
};

const rtkFilterOptions = [
  { value: "none", label: "Off" },
  { value: "minimal", label: "Minimal" },
  { value: "aggressive", label: "Aggressive" },
];
const rtkFilterHints: Record<string, string> = {
  none: "No source code comment stripping.",
  minimal: "Strip comments and docstrings only.",
  aggressive: "Strip comments, docstrings, blank lines, and trailing whitespace.",
};

const isRoundRobin = (strategy: string) =>
  strategy === "round-robin" || strategy === "round_robin" || strategy === "smart-round-robin" || strategy === "smart_round_robin";

// ── Page ────────────────────────────────────────────────────────────
export function SettingsPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const settings = useQuery({ queryKey: ["endpoint-settings"], queryFn: () => api.endpointSettings() });
  const [local, setLocal] = useState<EndpointSettings | null>(null);
  const [tab, setTab] = useHashTab("saving");

  useEffect(() => {
    if (settings.data) setLocal(settings.data);
  }, [settings.data]);

  const save = useMutation({
    mutationFn: (patch: Partial<EndpointSettings>) => api.updateEndpointSettings(patch),
    onSuccess: (data) => {
      setLocal(data);
      qc.setQueryData(["endpoint-settings"], data);
    },
    onError: (e) => toast.error("Settings save failed", (e as Error).message),
  });

  const update = (patch: Partial<EndpointSettings>) => {
    if (local) setLocal({ ...local, ...patch });
    save.mutate(patch);
  };

  return (
    <>
      <PageHeader
        title="Settings"
        icon={Sparkles}
        description="Configure token saving, routing, network, and more."
      />

      {settings.isLoading || !local ? (
        <Spinner />
      ) : (
        <>
          <TabBar tabs={settingsTabs} active={tab} onChange={setTab} />

          <div className="mt-6">
            {tab === "saving" && <SavingTab local={local} update={update} />}
            {tab === "routing" && <RoutingTab local={local} update={update} />}
            {tab === "network" && <NetworkTab local={local} update={update} />}
            {tab === "branding" && <BrandingTab />}
            {tab === "system" && <SystemTab />}
          </div>

          {save.isError && (
            <div className="mt-4">
              <ErrorBanner message={`Failed to save: ${(save.error as Error)?.message ?? "unknown error"}`} />
            </div>
          )}
        </>
      )}
    </>
  );
}

// ── Token Saving Tab ────────────────────────────────────────────────
function SavingTab({
  local,
  update,
}: {
  local: EndpointSettings;
  update: (patch: Partial<EndpointSettings>) => void;
}) {
  return (
    <div className="space-y-4">
      <Card>
        <SectionHeader
          title="Headroom context compression"
          description="Compresses conversation context through an optional Headroom proxy. Start the proxy separately with `make headroom` or Docker, then enable it here. Disables RTK when enabled."
          icon={Cpu}
        />
        <div className="flex items-center justify-between border-t border-[var(--border)] px-6 py-4">
          <span className="text-sm font-medium">Enable Headroom compression</span>
          <Toggle checked={local.headroom_enabled} onChange={(v) => update({ headroom_enabled: v, ...(v ? { rtk_enabled: false } : {}) })} />
        </div>
        {local.headroom_enabled && (
          <>
            <div className="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--border)] px-6 py-4">
              <div>
                <p className="text-sm font-medium">Proxy URL</p>
                <p className="mt-0.5 text-xs text-[var(--text-muted)]">Use http://127.0.0.1:8787 for local dev, or http://headroom:8787 for a Docker sidecar.</p>
              </div>
              <Input
                type="text"
                value={local.headroom_base_url || "http://127.0.0.1:8787"}
                onChange={(e) => update({ headroom_base_url: e.target.value })}
                className="w-64 font-mono text-sm"
                placeholder="http://127.0.0.1:8787"
              />
            </div>
            <div className="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--border)] px-6 py-4">
              <div>
                <p className="text-sm font-medium">Token budget</p>
                <p className="mt-0.5 text-xs text-[var(--text-muted)]">Target token count after compression</p>
              </div>
              <Input
                type="number"
                min={1000}
                max={2000000}
                value={local.headroom_token_budget || 100000}
                onChange={(e) => update({ headroom_token_budget: parseInt(e.target.value) || 100000 })}
                className="w-28 text-center"
              />
            </div>
            <div className="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--border)] px-6 py-4">
              <div>
                <p className="text-sm font-medium">Output shaper</p>
                <p className="mt-0.5 text-xs text-[var(--text-muted)]">Headroom also steers output verbosity (replaces Caveman/Terse)</p>
              </div>
              <Toggle
                checked={local.headroom_output_shaper}
                onChange={(v) => update({ headroom_output_shaper: v, ...(v ? { caveman_enabled: false, terse_enabled: false } : {}) })}
              />
            </div>
          </>
        )}
      </Card>

      <Card>
        <SectionHeader
          title="RTK input compression"
          description="Compresses bulky tool outputs (diffs, greps, listings, build logs) before they reach the model. Saves input tokens. Safe by design — never corrupts content."
          icon={Zap}
        />
        <div className="flex items-center justify-between border-t border-[var(--border)] px-6 py-4">
          <span className="text-sm font-medium">Enable RTK token saver</span>
          <Toggle checked={local.rtk_enabled} onChange={(v) => update({ rtk_enabled: v, ...(v ? { headroom_enabled: false } : {}) })} />
        </div>
        {local.rtk_enabled && (
          <div className="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--border)] px-6 py-4">
            <div>
              <p className="text-sm font-medium">Source code filter</p>
              <p className="mt-0.5 text-xs text-[var(--text-muted)]">{rtkFilterHints[local.rtk_filter_level || "none"]}</p>
            </div>
            <SegmentedControl
              value={local.rtk_filter_level || "none"}
              onChange={(v) => update({ rtk_filter_level: v })}
              options={rtkFilterOptions}
            />
          </div>
        )}
      </Card>

      <Card>
        <SectionHeader
          title="Caveman output compression"
          description="Instructs the model to answer tersely (caveman style) — keeps all technical substance, drops filler. Cuts output tokens 65-75%."
          icon={MessageSquare}
        />
        <div className="flex items-center justify-between border-t border-[var(--border)] px-6 py-4">
          <span className="text-sm font-medium">Enable caveman mode</span>
          <Toggle checked={local.caveman_enabled} onChange={(v) => update({ caveman_enabled: v, ...(v ? { terse_enabled: false } : {}) })} />
        </div>
        {local.caveman_enabled && (
          <div className="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--border)] px-6 py-4">
            <div>
              <p className="text-sm font-medium">Compression level</p>
              <p className="mt-0.5 text-xs text-[var(--text-muted)]">{cavemanHints[local.caveman_level]}</p>
            </div>
            <SegmentedControl
              value={local.caveman_level}
              onChange={(v) => update({ caveman_level: v })}
              options={cavemanOptions}
            />
          </div>
        )}
      </Card>

      <Card>
        <SectionHeader
          title="Terse mode (alternative)"
          description="KeiRouter's own concise-output directive. An alternative to caveman; both inject a system instruction, so pick one."
          icon={Layers}
          iconTone="neutral"
        />
        <div className="flex items-center justify-between border-t border-[var(--border)] px-6 py-4">
          <span className="text-sm font-medium">Enable terse mode</span>
          <Toggle checked={local.terse_enabled} onChange={(v) => update({ terse_enabled: v, ...(v ? { caveman_enabled: false } : {}) })} />
        </div>
        {local.terse_enabled && (
          <div className="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--border)] px-6 py-4">
            <div>
              <p className="text-sm font-medium">Terse level</p>
              <p className="mt-0.5 text-xs text-[var(--text-muted)]">{terseHints[local.terse_level]}</p>
            </div>
            <SegmentedControl
              value={local.terse_level}
              onChange={(v) => update({ terse_level: v })}
              options={terseOptions}
            />
          </div>
        )}
      </Card>
    </div>
  );
}

// ── Routing Tab ─────────────────────────────────────────────────────
function RoutingTab({
  local,
  update,
}: {
  local: EndpointSettings;
  update: (patch: Partial<EndpointSettings>) => void;
}) {
  return (
    <Card>
      <SectionHeader
        title="Routing Strategy"
        description="Control how requests are distributed across accounts and chains."
        icon={Route}
      />
      <div className="divide-y divide-[var(--border)] border-t border-[var(--border)]">
        <div className="flex items-center justify-between gap-4 px-6 py-4">
          <div>
            <p className="text-sm font-medium">Provider group round robin</p>
            <p className="mt-0.5 text-xs text-[var(--text-muted)]">Cycle through accounts in the same provider/model group</p>
          </div>
          <Toggle
            checked={isRoundRobin(local.routing_strategy)}
            onChange={() => update({ routing_strategy: isRoundRobin(local.routing_strategy) ? "fill-first" : "round-robin" })}
          />
        </div>

        {isRoundRobin(local.routing_strategy) && (
          <div className="flex items-center justify-between gap-4 px-6 py-4">
            <div>
              <p className="text-sm font-medium">Provider sticky limit</p>
              <p className="mt-0.5 text-xs text-[var(--text-muted)]">Calls per account before switching</p>
            </div>
            <Input
              type="number"
              min={1}
              max={10}
              value={local.sticky_limit || 3}
              onChange={(e) => update({ sticky_limit: parseInt(e.target.value) || 3 })}
              className="w-20 text-center"
            />
          </div>
        )}

        <div className="flex items-center justify-between gap-4 px-6 py-4">
          <div>
            <p className="text-sm font-medium">Chain round robin</p>
            <p className="mt-0.5 text-xs text-[var(--text-muted)]">Cycle through providers in chains instead of always starting with first</p>
          </div>
          <Toggle
            checked={isRoundRobin(local.combo_strategy)}
            onChange={() => update({ combo_strategy: isRoundRobin(local.combo_strategy) ? "fallback" : "round-robin" })}
          />
        </div>

        {isRoundRobin(local.combo_strategy) && (
          <div className="flex items-center justify-between gap-4 px-6 py-4">
            <div>
              <p className="text-sm font-medium">Chain sticky limit</p>
              <p className="mt-0.5 text-xs text-[var(--text-muted)]">Calls per chain model before switching</p>
            </div>
            <Input
              type="number"
              min={1}
              max={100}
              value={local.combo_sticky_limit || 1}
              onChange={(e) => update({ combo_sticky_limit: parseInt(e.target.value) || 1 })}
              className="w-20 text-center"
            />
          </div>
        )}

        <div className="px-6 py-3">
          <p className="text-xs text-[var(--text-muted)] italic">
            {isRoundRobin(local.routing_strategy)
              ? `Distributing requests across all available accounts with ${local.sticky_limit || 3} calls per account.`
              : "Using accounts in priority order (Fill First)."}
            {isRoundRobin(local.combo_strategy)
              ? ` Chains rotate after ${local.combo_sticky_limit || 1} call${(local.combo_sticky_limit || 1) === 1 ? "" : "s"} per model.`
              : " Chains always start with their first model."}
          </p>
        </div>
      </div>
    </Card>
  );
}

// ── Network Tab ─────────────────────────────────────────────────────
function NetworkTab({
  local,
  update,
}: {
  local: EndpointSettings;
  update: (patch: Partial<EndpointSettings>) => void;
}) {
  return (
    <div className="space-y-4">
      {/* Timeout grid */}
      <Card>
        <SectionHeader
          title="Timeouts"
          description="Fine-tune upstream connection and streaming timeouts. Increase for slow providers or reasoning models."
          icon={Clock}
        />
        <div className="grid grid-cols-1 gap-4 border-t border-[var(--border)] px-6 py-5 sm:grid-cols-3">
          <Field label="Connect timeout (sec)">
            <Input
              type="number"
              min={5}
              max={300}
              value={Math.round((local.response_header_timeout_ms || 60000) / 1000)}
              onChange={(e) => {
                const sec = parseInt(e.target.value) || 60;
                update({ response_header_timeout_ms: sec * 1000 });
              }}
              placeholder="60"
              className="text-center"
            />
            <p className="mt-1 text-xs text-[var(--text-muted)]">Default: 60s</p>
          </Field>
          <Field label="Stream stall timeout (sec)">
            <Input
              type="number"
              min={10}
              max={600}
              value={Math.round((local.stream_stall_timeout_ms || 120000) / 1000)}
              onChange={(e) => {
                const sec = parseInt(e.target.value) || 120;
                update({ stream_stall_timeout_ms: sec * 1000 });
              }}
              placeholder="120"
              className="text-center"
            />
            <p className="mt-1 text-xs text-[var(--text-muted)]">Default: 120s</p>
          </Field>
          <Field label="Request timeout (sec)">
            <Input
              type="number"
              min={30}
              max={3600}
              value={Math.round((local.request_timeout_ms || 300000) / 1000)}
              onChange={(e) => {
                const sec = parseInt(e.target.value) || 300;
                update({ request_timeout_ms: sec * 1000 });
              }}
              placeholder="300"
              className="text-center"
            />
            <p className="mt-1 text-xs text-[var(--text-muted)]">Default: 300s (5 min)</p>
          </Field>
        </div>
      </Card>

      {/* Rate limiting */}
      <Card>
        <SectionHeader
          title="Rate Limits"
          description="Enable per-key RPM, TPM, and concurrency limits from assigned plans."
          icon={Shield}
        />
        <div className="flex items-center justify-between border-t border-[var(--border)] px-6 py-4">
          <div>
            <p className="text-sm font-medium">Enforce API key rate limits</p>
            <p className="mt-0.5 text-xs text-[var(--text-muted)]">
              When enabled, plan limits are enforced immediately. Blank or 0 plan values remain unlimited.
            </p>
          </div>
          <Toggle
            checked={local.rate_limits_enabled !== false}
            onChange={(v) => update({ rate_limits_enabled: v })}
          />
        </div>
      </Card>

      {/* Proxy */}
      <ProxySettings local={local} update={update} />

      {/* Observability */}
      <Card>
        <SectionHeader
          title="Observability"
          description="Record request details for inspection in the logs view."
          icon={Monitor}
        />
        <div className="flex items-center justify-between border-t border-[var(--border)] px-6 py-4">
          <span className="text-sm font-medium">Enable request detail recording</span>
          <Toggle
            checked={local.observability_enabled !== false}
            onChange={(v) => update({ observability_enabled: v })}
          />
        </div>
      </Card>
    </div>
  );
}

function ProxySettings({
  local,
  update,
}: {
  local: EndpointSettings;
  update: (patch: Partial<EndpointSettings>) => void;
}) {
  const [testResult, setTestResult] = useState<{ ok: boolean; text: string } | null>(null);
  const [testing, setTesting] = useState(false);

  const testProxy = async () => {
    if (!local.outbound_proxy_url) return;
    setTesting(true);
    setTestResult(null);
    try {
      const res = await api.testProxy(local.outbound_proxy_url);
      if (res.ok) {
        const ip = res.exitIP ? ` — exit IP: ${res.exitIP}` : "";
        setTestResult({ ok: true, text: `Proxy OK (${res.elapsedMs}ms)${ip}` });
      } else {
        setTestResult({ ok: false, text: `Failed: ${res.error || `HTTP ${res.status}`}` });
      }
    } catch (e) {
      setTestResult({ ok: false, text: (e as Error).message });
    } finally {
      setTesting(false);
    }
  };

  const proxyEnabled = local.outbound_proxy_enabled;
  const hasURL = !!local.outbound_proxy_url;
  const statusLabel = !proxyEnabled
    ? "Inactive"
    : hasURL
      ? "Active"
      : "No URL";
  const statusTone = !proxyEnabled
    ? "bg-[var(--bg-subtle)] text-[var(--text-muted)]"
    : hasURL
      ? "bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300"
      : "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-200";

  const detectedScheme = (() => {
    if (!local.outbound_proxy_url) return null;
    try {
      const s = new URL(local.outbound_proxy_url).protocol.replace(":", "").toLowerCase();
      if (["http", "https", "socks5"].includes(s)) return s;
    } catch { /* ignore */ }
    return null;
  })();

  return (
    <Card>
      <SectionHeader
        title="Network Proxy"
        description="Route all provider outbound requests through an HTTP/HTTPS/SOCKS5 proxy."
        icon={Wifi}
      />
      <div className="divide-y divide-[var(--border)] border-t border-[var(--border)]">
        <div className="flex items-center justify-between gap-4 px-6 py-4">
          <div className="flex items-center gap-3">
            <div>
              <p className="text-sm font-medium">Outbound Proxy</p>
              <p className="mt-0.5 text-xs text-[var(--text-muted)]">Applies to all provider and OAuth requests when no per-account proxy is set.</p>
            </div>
            <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-semibold ${statusTone}`}>
              {statusLabel}
            </span>
          </div>
          <Toggle
            checked={proxyEnabled}
            onChange={(v) => update({ outbound_proxy_enabled: v })}
          />
        </div>

        {proxyEnabled && (
          <>
            {proxyEnabled && !hasURL && (
              <div className="flex items-center gap-2 bg-amber-50 px-6 py-2.5 text-xs text-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
                <Info className="h-3.5 w-3.5 shrink-0" />
                <span>Proxy is enabled but no URL is configured. Enter a proxy URL below.</span>
              </div>
            )}
            <div className="px-6 py-4">
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <Field label="Proxy URL">
                  <Input
                    placeholder="http://127.0.0.1:7897"
                    value={local.outbound_proxy_url}
                    onChange={(e) => update({ outbound_proxy_url: e.target.value })}
                  />
                  <div className="mt-1 flex items-center gap-2">
                    {detectedScheme && (
                      <span className="inline-flex items-center rounded bg-[var(--bg-subtle)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--text-muted)]">
                        {detectedScheme.toUpperCase()}
                      </span>
                    )}
                    <p className="text-xs text-[var(--text-muted)]">Supports http, https, socks5.</p>
                  </div>
                </Field>
                <Field label="No Proxy">
                  <Input
                    placeholder="localhost,127.0.0.1"
                    value={local.outbound_no_proxy}
                    onChange={(e) => update({ outbound_no_proxy: e.target.value })}
                  />
                  <p className="mt-1 text-xs text-[var(--text-muted)]">Comma-separated hostnames to bypass. Use <code className="rounded bg-[var(--bg-subtle)] px-1">*</code> for all.</p>
                </Field>
              </div>
            </div>
            <div className="flex items-center gap-3 px-6 py-4">
              <Button variant="ghost" onClick={testProxy} disabled={testing || !hasURL}>
                {testing ? "Testing…" : "Test proxy URL"}
              </Button>
              {testResult && (
                <span className={`text-xs ${testResult.ok ? "text-green-600 dark:text-green-400" : "text-[color:var(--color-danger)]"}`}>
                  {testResult.text}
                </span>
              )}
            </div>
          </>
        )}
      </div>
    </Card>
  );
}

// ── Branding Tab ────────────────────────────────────────────────────
function fileToDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result as string);
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}

function ImageUploadField({
  label,
  hint,
  value,
  onChange,
  accept,
}: {
  label: string;
  hint: string;
  value: string;
  onChange: (dataUrl: string) => void;
  accept: string;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [dragOver, setDragOver] = useState(false);

  const handleFile = async (file: File) => {
    if (!file.type.startsWith("image/")) return;
    const dataUrl = await fileToDataUrl(file);
    onChange(dataUrl);
  };

  const handleDrop = async (e: React.DragEvent) => {
    e.preventDefault();
    setDragOver(false);
    const file = e.dataTransfer.files?.[0];
    if (file) await handleFile(file);
  };

  const handleInputChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) await handleFile(file);
    // Reset so the same file can be re-selected
    if (inputRef.current) inputRef.current.value = "";
  };

  return (
    <div>
      <span className="text-xs font-medium text-[var(--text-muted)]">{label}</span>
      <div className="mt-1.5 flex items-start gap-4">
        {/* Preview */}
        <div className="flex h-16 w-16 shrink-0 items-center justify-center rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)]">
          {value ? (
            <img src={value} alt={label} className="h-full w-full rounded-xl object-contain p-1" />
          ) : (
            <Palette className="h-6 w-6 text-[var(--text-muted)]" />
          )}
        </div>

        {/* Drop zone / upload */}
        <div className="min-w-0 flex-1">
          <div
            onDragOver={(e) => { e.preventDefault(); setDragOver(true); }}
            onDragLeave={() => setDragOver(false)}
            onDrop={handleDrop}
            onClick={() => inputRef.current?.click()}
            className={`flex cursor-pointer flex-col items-center justify-center gap-1 rounded-xl border-2 border-dashed px-4 py-3 transition-colors ${
              dragOver
                ? "border-accent-400 bg-accent-50 dark:bg-accent-900/20"
                : "border-[var(--border)] hover:border-accent-300 hover:bg-[var(--bg-subtle)]"
            }`}
          >
            <div className="flex items-center gap-2 text-sm font-medium text-[var(--text-muted)]">
              <Upload className="h-4 w-4" />
              <span>{value ? "Replace image" : "Upload image"}</span>
            </div>
            <p className="text-xs text-[var(--text-muted)]">PNG, SVG, ICO — drag & drop or click</p>
          </div>
          {value && (
            <button
              type="button"
              onClick={() => onChange("")}
              className="mt-1.5 text-xs text-[color:var(--color-danger)] hover:underline"
            >
              Remove
            </button>
          )}
          <p className="mt-1 text-xs text-[var(--text-muted)]">{hint}</p>
          <input
            ref={inputRef}
            type="file"
            accept={accept}
            className="hidden"
            onChange={handleInputChange}
          />
        </div>
      </div>
    </div>
  );
}

function ColorPaletteField({
  value,
  onChange,
}: {
  value: string;
  onChange: (id: string) => void;
}) {
  return (
    <div className="px-6 py-5">
      <p className="text-xs font-medium text-[var(--text-muted)] mb-1">Color Theme</p>
      <p className="text-xs text-[var(--text-muted)] mb-3">
        Choose a color palette for the entire dashboard. This changes the accent and highlight colors across all UI elements.
      </p>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-5">
        {PALETTES.map((palette) => {
          const selected = palette.id === value;
          const accentScale = generateShades(palette.accent);
          const secondaryScale = generateShades(palette.secondary);
          return (
            <button
              key={palette.id}
              type="button"
              onClick={() => onChange(palette.id)}
              className={`group relative flex flex-col items-center gap-2 rounded-xl border-2 p-3 transition-all ${
                selected
                  ? "border-[var(--text)] bg-[var(--bg-elevated)] shadow-[var(--shadow-pop)]"
                  : "border-[var(--border)] bg-[var(--bg-subtle)] hover:border-[var(--border-strong)] hover:bg-[var(--bg-elevated)]"
              }`}
            >
              {/* Color swatches */}
              <div className="flex items-center gap-1.5">
                <div className="flex -space-x-1">
                  {[accentScale[100], accentScale[300], accentScale[500], accentScale[700], accentScale[900]].map(
                    (color, i) => (
                      <div
                        key={i}
                        className="h-4 w-4 rounded-full border border-white/50 first:rounded-l-full last:rounded-r-full"
                        style={{ backgroundColor: color }}
                      />
                    ),
                  )}
                </div>
              </div>
              <div className="flex items-center gap-1.5">
                <div className="flex -space-x-1">
                  {[secondaryScale[100], secondaryScale[300], secondaryScale[500], secondaryScale[700], secondaryScale[900]].map(
                    (color, i) => (
                      <div
                        key={i}
                        className="h-4 w-4 rounded-full border border-white/50 first:rounded-l-full last:rounded-r-full"
                        style={{ backgroundColor: color }}
                      />
                    ),
                  )}
                </div>
              </div>
              {/* Label */}
              <span className={`text-[11px] font-medium leading-tight text-center ${
                selected ? "text-[var(--text)]" : "text-[var(--text-muted)]"
              }`}>
                {palette.name}
              </span>
              {/* Selected checkmark */}
              {selected && (
                <div className="absolute -top-1.5 -right-1.5 flex h-5 w-5 items-center justify-center rounded-full bg-[var(--text)] text-[var(--bg)]">
                  <CheckCircle2 className="h-3 w-3" />
                </div>
              )}
            </button>
          );
        })}
      </div>
    </div>
  );
}

function BrandingTab() {
  const qc = useQueryClient();
  const toast = useToast();
  const branding = useQuery({ queryKey: ["branding"], queryFn: () => api.branding() });
  const [local, setLocal] = useState<BrandingSettings | null>(null);

  useEffect(() => {
    if (branding.data) setLocal(branding.data);
  }, [branding.data]);

  const save = useMutation({
    mutationFn: (patch: Partial<BrandingSettings>) => api.updateBranding(patch),
    onSuccess: (data) => {
      setLocal(data);
      qc.setQueryData(["branding"], data);
      qc.invalidateQueries({ queryKey: ["portal-branding"] });
      toast.success("Branding updated", `Display name set to "${data.name}". Refresh to see changes.`);
    },
    onError: (e) => toast.error("Branding save failed", (e as Error).message),
  });

  const update = (patch: Partial<BrandingSettings>) => {
    if (local) setLocal({ ...local, ...patch });
  };

  const handleSave = () => {
    if (local) save.mutate(local);
  };

  if (branding.isLoading || !local) return <Spinner />;

  const previewLogo = local.logo_url || "/keirouter-logo.png";

  return (
    <Card>
      <SectionHeader
        title="White-Label Branding"
        description="Customize the dashboard name, logo, and favicon. Changes apply to both the admin dashboard and the public Usage Dashboard."
        icon={Palette}
      />
      <div className="divide-y divide-[var(--border)] border-t border-[var(--border)]">
        {/* Two-column grid: name + tagline */}
        <div className="px-6 py-5">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Display Name">
              <Input
                value={local.name}
                onChange={(e) => update({ name: e.target.value })}
                placeholder="KeiRouter"
              />
              <p className="mt-1 text-xs text-[var(--text-muted)]">
                Shown in sidebar, tab title, login screen, and Usage Dashboard.
              </p>
            </Field>
            <Field label="Portal Tagline">
              <Input
                value={local.tagline}
                onChange={(e) => update({ tagline: e.target.value })}
                placeholder="Enter your API Key to view usage."
              />
              <p className="mt-1 text-xs text-[var(--text-muted)]">
                Optional message on the Usage Dashboard login screen.
              </p>
            </Field>
          </div>
        </div>

        {/* Image uploads: logo + favicon */}
        <div className="px-6 py-5">
          <div className="grid grid-cols-1 gap-6 sm:grid-cols-2">
            <ImageUploadField
              label="Logo"
              hint="SVG or PNG recommended. Leave empty for default."
              value={local.logo_url}
              onChange={(dataUrl) => update({ logo_url: dataUrl })}
              accept="image/png,image/svg+xml,image/*"
            />
            <ImageUploadField
              label="Favicon"
              hint="PNG or ICO recommended. Leave empty for default."
              value={local.favicon_url}
              onChange={(dataUrl) => update({ favicon_url: dataUrl })}
              accept="image/png,image/x-icon,image/*"
            />
          </div>
        </div>

        {/* Color palette */}
        <ColorPaletteField
          value={local.color_palette || "sage-terra"}
          onChange={(id) => {
            update({ color_palette: id });
            // Live preview: apply palette immediately to <html>
            const root = document.documentElement;
            const scales = getPaletteScales(id);
            applyShadeScale(root, "accent", scales.accent);
            applyShadeScale(root, "secondary", scales.secondary);
          }}
        />

        {/* Preview */}
        <div className="px-6 py-5">
          <p className="text-xs font-medium text-[var(--text-muted)] mb-3">Preview</p>
          <div className="flex items-center gap-4 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4">
            <img src={previewLogo} alt={local.name || "Logo"} className="h-10 w-10 object-contain" />
            <div>
              <p className="text-sm font-semibold text-[var(--text)]">{local.name || "KeiRouter"}</p>
              {local.tagline && <p className="text-xs text-[var(--text-muted)]">{local.tagline}</p>}
            </div>
          </div>
        </div>

        <div className="flex items-center justify-end gap-3 px-6 py-4">
          {save.isError && (
            <span className="text-xs text-[color:var(--color-danger)]">{(save.error as Error)?.message}</span>
          )}
          <Button onClick={handleSave} disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save Branding"}
          </Button>
        </div>
      </div>
    </Card>
  );
}

// ── System Tab ──────────────────────────────────────────────────────
function SystemTab() {
  return (
    <div className="space-y-4">
      <DatabaseSettings />
      <UpdatesSettings />
    </div>
  );
}

// ── Sub-components (shared) ─────────────────────────────────────────

function PassphraseInput({
  id,
  value,
  onChange,
  show,
  onToggleShow,
  placeholder,
  autoFocus,
  ariaInvalid,
}: {
  id: string;
  value: string;
  onChange: (v: string) => void;
  show: boolean;
  onToggleShow: () => void;
  placeholder?: string;
  autoFocus?: boolean;
  ariaInvalid?: boolean;
}) {
  return (
    <div className="relative">
      <Input
        id={id}
        type={show ? "text" : "password"}
        value={value}
        autoFocus={autoFocus}
        autoComplete="new-password"
        spellCheck={false}
        placeholder={placeholder}
        aria-invalid={ariaInvalid}
        onChange={(e) => onChange(e.target.value)}
        className="pr-11"
      />
      <button
        type="button"
        onClick={onToggleShow}
        aria-label={show ? "Hide passphrase" : "Show passphrase"}
        className="absolute inset-y-0 right-0 flex w-10 items-center justify-center text-[var(--text-muted)] transition-colors hover:text-[var(--text)] focus:outline-none focus-visible:text-[var(--text)]"
      >
        {show ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
      </button>
    </div>
  );
}

function strengthOf(pass: string): { label: string; tone: "muted" | "weak" | "ok" | "strong"; pct: number } {
  if (!pass) return { label: "No passphrase — local backup", tone: "muted", pct: 0 };
  let score = 0;
  if (pass.length >= 8) score++;
  if (pass.length >= 14) score++;
  if (/[A-Z]/.test(pass) && /[a-z]/.test(pass)) score++;
  if (/\d/.test(pass)) score++;
  if (/[^A-Za-z0-9]/.test(pass)) score++;
  if (score <= 2) return { label: "Weak — short or simple", tone: "weak", pct: 33 };
  if (score <= 3) return { label: "OK — could be stronger", tone: "ok", pct: 66 };
  return { label: "Strong", tone: "strong", pct: 100 };
}

function DatabaseSettings() {
  const toast = useToast();
  const importRef = useRef<HTMLInputElement>(null);
  const sqliteImportRef = useRef<HTMLInputElement>(null);
  const sqlite = useQuery({ queryKey: ["sqlite-status"], queryFn: () => api.sqliteStatus() });
  const [loading, setLoading] = useState(false);

  const [exportOpen, setExportOpen] = useState(false);
  const [usePortable, setUsePortable] = useState(false);
  const [exportPass, setExportPass] = useState("");
  const [exportConfirm, setExportConfirm] = useState("");
  const [showExportPass, setShowExportPass] = useState(false);

  const [importOpen, setImportOpen] = useState(false);
  const [pendingPayload, setPendingPayload] = useState<Record<string, unknown> | null>(null);
  const [importPass, setImportPass] = useState("");
  const [showImportPass, setShowImportPass] = useState(false);
  const [importError, setImportError] = useState<string | null>(null);

  const [sqliteRestoreOpen, setSQLiteRestoreOpen] = useState(false);
  const [pendingSQLiteFile, setPendingSQLiteFile] = useState<File | null>(null);
  const [sqliteRestoreError, setSQLiteRestoreError] = useState<string | null>(null);

  const resetExport = () => {
    setExportOpen(false);
    setUsePortable(false);
    setExportPass("");
    setExportConfirm("");
    setShowExportPass(false);
  };

  const resetImport = () => {
    setImportOpen(false);
    setPendingPayload(null);
    setImportPass("");
    setShowImportPass(false);
    setImportError(null);
    if (importRef.current) importRef.current.value = "";
  };

  const resetSQLiteRestore = () => {
    setSQLiteRestoreOpen(false);
    setPendingSQLiteFile(null);
    setSQLiteRestoreError(null);
    if (sqliteImportRef.current) sqliteImportRef.current.value = "";
  };

  const downloadBackup = async (pass: string | undefined) => {
    setLoading(true);
    try {
      const data = await api.exportDatabase(pass || undefined);
      const content = JSON.stringify(data, null, 2);
      const blob = new Blob([content], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      const stamp = new Date().toISOString().replace(/[.:]/g, "-");
      a.href = url;
      a.download = `keirouter-backup${pass ? "-portable" : ""}-${stamp}.json`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      toast.success(
        "Backup downloaded",
        pass
          ? "Portable backup saved. Keep the passphrase safe — it is required to import on another machine."
          : "Local backup saved. It only restores on this machine's master key.",
      );
      resetExport();
    } catch (e) {
      toast.error("Export failed", (e as Error).message);
    } finally {
      setLoading(false);
    }
  };

  const downloadSQLiteBackup = async () => {
    setLoading(true);
    try {
      const blob = await api.backupSQLite();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      const stamp = new Date().toISOString().replace(/[.:]/g, "-");
      a.href = url;
      a.download = `keirouter-sqlite-${stamp}.db`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      toast.success("SQLite backup downloaded", "Database file snapshot saved as .db.");
    } catch (e) {
      toast.error("SQLite backup failed", (e as Error).message);
    } finally {
      setLoading(false);
    }
  };

  const handleExportSubmit = () => {
    if (usePortable) {
      const p = exportPass.trim();
      if (!p) return;
      if (p !== exportConfirm.trim()) return;
      void downloadBackup(p);
    } else {
      void downloadBackup(undefined);
    }
  };

  const handleFilePicked = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;

    try {
      const raw = await file.text();
      const payload = JSON.parse(raw);

      if (payload && (payload as { portable?: boolean }).portable === true) {
        setPendingPayload(payload);
        setImportPass("");
        setShowImportPass(false);
        setImportError(null);
        setImportOpen(true);
        return;
      }

      setLoading(true);
      const result = await api.importDatabase(payload);
      toast.success("Import complete", `${result.imported} records restored. Existing data was merged or updated.`);
      if (importRef.current) importRef.current.value = "";
    } catch (e) {
      toast.error("Import failed", (e as Error).message);
      if (importRef.current) importRef.current.value = "";
    } finally {
      setLoading(false);
    }
  };

  const handleSQLiteFilePicked = (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;
    setPendingSQLiteFile(file);
    setSQLiteRestoreError(null);
    setSQLiteRestoreOpen(true);
  };

  const submitSQLiteRestore = async () => {
    if (!pendingSQLiteFile) {
      setSQLiteRestoreError("No SQLite backup selected.");
      return;
    }
    setLoading(true);
    setSQLiteRestoreError(null);
    try {
      const result = await api.restoreSQLite(pendingSQLiteFile);
      toast.success(
        "SQLite restore staged",
        `Safety backup created at ${result.safety_backup}. Restart KeiRouter to load restored database.`,
      );
      resetSQLiteRestore();
    } catch (e) {
      setSQLiteRestoreError((e as Error).message || "Restore failed.");
    } finally {
      setLoading(false);
    }
  };

  const submitImportWithPass = async () => {
    const p = importPass.trim();
    if (!p) {
      setImportError("Passphrase required for portable backups.");
      return;
    }
    if (!pendingPayload) {
      setImportError("No backup loaded. Select a file first.");
      return;
    }
    setImportError(null);
    setLoading(true);
    try {
      const result = await api.importDatabase(pendingPayload, p);
      toast.success("Import complete", `${result.imported} records restored. Existing data was merged or updated.`);
      resetImport();
    } catch (e) {
      setImportError((e as Error).message || "Import failed. Wrong passphrase?");
    } finally {
      setLoading(false);
    }
  };

  const exportStrength = strengthOf(exportPass);
  const exportMismatch =
    usePortable && exportConfirm.length > 0 && exportPass !== exportConfirm;
  const exportDisabled =
    loading ||
    (usePortable && (!exportPass.trim() || exportPass !== exportConfirm));
  const sqliteAvailable = sqlite.data?.available === true;

  return (
    <>
      <Card>
        <SectionHeader
          title="Configuration backup"
          description="Export or import KeiRouter configuration as JSON. Portable mode re-keys credentials with a passphrase."
          icon={Database}
        />
        <div className="flex flex-wrap items-center gap-3 border-t border-[var(--border)] px-6 py-4">
          <Button variant="ghost" onClick={() => setExportOpen(true)} disabled={loading}>
            <Download className="h-4 w-4" />
            Download JSON backup
          </Button>
          <Button variant="ghost" onClick={() => importRef.current?.click()} disabled={loading}>
            <Upload className="h-4 w-4" />
            Import JSON backup
          </Button>
          <input
            ref={importRef}
            type="file"
            accept="application/json,.json"
            className="hidden"
            onChange={handleFilePicked}
          />
        </div>
      </Card>

      <Card>
        <SectionHeader
          title="SQLite database file"
          description="Download or restore the raw SQLite database. Only available when database.driver is sqlite."
          icon={Shield}
          iconTone="neutral"
        />
        <div className="border-t border-[var(--border)] px-6 py-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <span
                  className={`inline-flex items-center rounded-md px-2 py-1 text-xs font-semibold ${
                    sqliteAvailable
                      ? "bg-accent-100 text-accent-700 dark:bg-accent-900/40 dark:text-accent-200"
                      : "bg-ink-100 text-[var(--text-muted)] dark:bg-ink-800"
                  }`}
                >
                  {sqlite.isLoading ? "Checking…" : sqliteAvailable ? "SQLite active" : "Unavailable"}
                </span>
                {sqlite.data?.dialect && (
                  <span className="font-mono text-xs text-[var(--text-muted)]">
                    driver={sqlite.data.dialect}
                  </span>
                )}
              </div>
              <p className="mt-2 max-w-2xl text-xs leading-relaxed text-[var(--text-muted)]">
                {sqliteAvailable
                  ? "Backup uses SQLite VACUUM INTO for a consistent .db snapshot. Restore validates integrity and creates a safety copy before replacement."
                  : "Only available for SQLite connections. Postgres and in-memory databases are not eligible for raw database-file backup."}
              </p>
              {sqlite.data?.path && (
                <p className="mt-2 truncate font-mono text-[11px] text-[var(--text-muted)]">
                  {sqlite.data.path}
                </p>
              )}
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <Button variant="ghost" onClick={downloadSQLiteBackup} disabled={loading || !sqliteAvailable}>
                <Download className="h-4 w-4" />
                Download .db
              </Button>
              <Button variant="ghost" onClick={() => sqliteImportRef.current?.click()} disabled={loading || !sqliteAvailable}>
                <Upload className="h-4 w-4" />
                Restore .db
              </Button>
              <input
                ref={sqliteImportRef}
                type="file"
                accept=".db,.sqlite,.sqlite3,application/vnd.sqlite3,application/octet-stream"
                className="hidden"
                onChange={handleSQLiteFilePicked}
              />
            </div>
          </div>
        </div>
      </Card>

      <Modal
        open={exportOpen}
        onClose={() => (loading ? null : resetExport())}
        title="Download backup"
        subtitle="Choose how credentials are encrypted in the export file."
        maxWidth="max-w-md"
      >
        <div className="max-h-[55vh] space-y-5 overflow-y-auto px-6 py-5">
          <div className="space-y-2">
            <label className="flex cursor-pointer items-start gap-3 rounded-xl border border-[var(--border)] px-4 py-3 transition-colors hover:bg-ink-50 dark:hover:bg-ink-900/40">
              <input
                type="radio"
                name="export-mode"
                checked={!usePortable}
                onChange={() => setUsePortable(false)}
                className="mt-1 h-4 w-4 accent-secondary-600"
              />
              <div className="flex-1">
                <div className="flex items-center gap-2 text-sm font-medium text-[var(--text)]">
                  <ShieldCheck className="h-4 w-4 text-accent-600 dark:text-accent-300" />
                  Local backup
                </div>
                <p className="mt-0.5 text-xs leading-relaxed text-[var(--text-muted)]">
                  Tied to this machine's master key. Only restores on this install. No passphrase needed.
                </p>
              </div>
            </label>

            <label className="flex cursor-pointer items-start gap-3 rounded-xl border border-[var(--border)] px-4 py-3 transition-colors hover:bg-ink-50 dark:hover:bg-ink-900/40">
              <input
                type="radio"
                name="export-mode"
                checked={usePortable}
                onChange={() => setUsePortable(true)}
                className="mt-1 h-4 w-4 accent-secondary-600"
              />
              <div className="flex-1">
                <div className="flex items-center gap-2 text-sm font-medium text-[var(--text)]">
                  <KeyRound className="h-4 w-4 text-secondary-600 dark:text-secondary-300" />
                  Portable backup
                </div>
                <p className="mt-0.5 text-xs leading-relaxed text-[var(--text-muted)]">
                  Re-keys credentials to a passphrase so you can restore on another machine.
                </p>
              </div>
            </label>
          </div>

          {usePortable && (
            <div className="space-y-3 rounded-xl border border-[var(--border)] bg-[var(--bg)] p-4">
              <Field label="Passphrase">
                <PassphraseInput
                  id="export-pass"
                  value={exportPass}
                  onChange={setExportPass}
                  show={showExportPass}
                  onToggleShow={() => setShowExportPass((s) => !s)}
                  placeholder="At least 8 characters"
                  autoFocus
                />
                <div className="mt-2">
                  <div className="h-1.5 w-full overflow-hidden rounded-full bg-ink-200 dark:bg-ink-800">
                    <div
                      className={`h-full transition-all duration-300 ${
                        exportStrength.tone === "strong"
                          ? "bg-accent-500"
                          : exportStrength.tone === "ok"
                            ? "bg-amber-500"
                            : exportStrength.tone === "weak"
                              ? "bg-[color:var(--color-danger)]"
                              : "bg-transparent"
                      }`}
                      style={{ width: `${exportStrength.pct}%` }}
                    />
                  </div>
                  <p className="mt-1 text-[11px] font-medium text-[var(--text-muted)]">
                    {exportStrength.label}
                  </p>
                </div>
              </Field>

              <Field label="Confirm passphrase">
                <PassphraseInput
                  id="export-confirm"
                  value={exportConfirm}
                  onChange={setExportConfirm}
                  show={showExportPass}
                  onToggleShow={() => setShowExportPass((s) => !s)}
                  placeholder="Re-enter passphrase"
                  ariaInvalid={exportMismatch}
                />
                {exportMismatch && (
                  <p className="mt-1 text-xs text-[color:var(--color-danger)]">
                    Passphrases do not match.
                  </p>
                )}
              </Field>

              <div className="flex items-start gap-2 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
                <Info className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                <span>Store this passphrase safely. Without it the backup cannot be restored — there is no recovery.</span>
              </div>
            </div>
          )}
        </div>

        <div className="flex items-center justify-end gap-2 border-t border-[var(--border)] px-6 py-4">
          <Button variant="ghost" onClick={resetExport} disabled={loading}>
            Cancel
          </Button>
          <Button variant="primary" onClick={handleExportSubmit} disabled={exportDisabled}>
            <Download className="h-4 w-4" />
            {loading ? "Preparing…" : "Download"}
          </Button>
        </div>
      </Modal>

      <Modal
        open={importOpen}
        onClose={() => (loading ? null : resetImport())}
        title="Portable backup detected"
        subtitle="Enter the passphrase that was used when this backup was exported."
      >
        <div className="space-y-4 px-6 py-5">
          <Field label="Passphrase">
            <PassphraseInput
              id="import-pass"
              value={importPass}
              onChange={(v) => {
                setImportPass(v);
                if (importError) setImportError(null);
              }}
              show={showImportPass}
              onToggleShow={() => setShowImportPass((s) => !s)}
              placeholder="Passphrase from export"
              autoFocus
              ariaInvalid={!!importError}
            />
          </Field>

          {importError && <ErrorBanner message={importError} />}

          <div className="flex items-start gap-2 rounded-lg bg-ink-100 px-3 py-2 text-xs text-[var(--text-muted)] dark:bg-ink-800/60">
            <Info className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            <span>Existing data is merged or updated. The import will not delete records that aren't in the backup.</span>
          </div>
        </div>

        <div className="flex items-center justify-end gap-2 border-t border-[var(--border)] px-6 py-4">
          <Button variant="ghost" onClick={resetImport} disabled={loading}>
            Cancel
          </Button>
          <Button
            variant="primary"
            onClick={submitImportWithPass}
            disabled={loading || !importPass.trim()}
          >
            <Upload className="h-4 w-4" />
            {loading ? "Importing…" : "Restore"}
          </Button>
        </div>
      </Modal>

      <Modal
        open={sqliteRestoreOpen}
        onClose={() => (loading ? null : resetSQLiteRestore())}
        title="Restore SQLite database"
        subtitle="This replaces the active SQLite database file after validation."
      >
        <div className="space-y-4 px-6 py-5">
          <div className="rounded-xl border border-[var(--border)] bg-[var(--bg)] p-4">
            <p className="text-sm font-semibold text-[var(--text)]">
              {pendingSQLiteFile?.name ?? "No file selected"}
            </p>
            <p className="mt-1 text-xs leading-relaxed text-[var(--text-muted)]">
              KeiRouter creates a safety backup before replacing the database. Restart required after restore because current DB connections still point at the old file.
            </p>
          </div>

          {sqliteRestoreError && <ErrorBanner message={sqliteRestoreError} />}

          <div className="flex items-start gap-2 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
            <Info className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            <span>Use only trusted KeiRouter SQLite backups. Restoring wrong data may break providers, keys, usage records, and settings.</span>
          </div>
        </div>

        <div className="flex items-center justify-end gap-2 border-t border-[var(--border)] px-6 py-4">
          <Button variant="ghost" onClick={resetSQLiteRestore} disabled={loading}>
            Cancel
          </Button>
          <Button variant="primary" onClick={submitSQLiteRestore} disabled={loading || !pendingSQLiteFile}>
            <Upload className="h-4 w-4" />
            {loading ? "Restoring…" : "Restore database"}
          </Button>
        </div>
      </Modal>
    </>
  );
}

function UpdatesSettings() {
  const { data, isLoading, isError } = useUpdateInfo();

  const publishedLabel = data?.published_at
    ? new Date(data.published_at).toLocaleDateString(undefined, {
        year: "numeric",
        month: "short",
        day: "numeric",
      })
    : "";

  return (
    <Card>
      <SectionHeader
        title="Updates"
        description="Check for new KeiRouter releases and read the latest changelog."
        icon={ArrowUpCircle}
      />
      <div className="border-t border-[var(--border)] px-6 py-4">
        {isLoading ? (
          <Spinner />
        ) : isError || !data || !data.checked ? (
          <p className="text-sm text-[var(--text-muted)]">
            Could not reach GitHub to check for updates. Current version:{" "}
            <span className="font-mono text-[var(--text)]">{data?.current ?? "dev"}</span>
          </p>
        ) : (
          <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
              <div>
                <p className="text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                  Current
                </p>
                <p className="font-mono text-sm">{data.current}</p>
              </div>
              <div>
                <p className="text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                  Latest
                </p>
                <p className="font-mono text-sm">{data.latest || "—"}</p>
              </div>
              <div className="ml-auto">
                {data.update_available ? (
                  <span className="inline-flex items-center gap-1.5 rounded-full bg-accent-100 px-3 py-1 text-xs font-medium text-accent-700 dark:bg-accent-900/40 dark:text-accent-200">
                    <ArrowUpCircle className="h-3.5 w-3.5" />
                    Update available
                  </span>
                ) : (
                  <span className="inline-flex items-center gap-1.5 rounded-full bg-green-100 px-3 py-1 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-300">
                    <CheckCircle2 className="h-3.5 w-3.5" />
                    Up to date
                  </span>
                )}
              </div>
            </div>

            {data.update_available && data.changelog && (
              <div>
                <div className="mb-1 flex items-center justify-between">
                  <p className="text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                    Changelog{publishedLabel ? ` · ${publishedLabel}` : ""}
                  </p>
                  {data.html_url && (
                    <a
                      href={data.html_url}
                      target="_blank"
                      rel="noreferrer noopener"
                      className="flex items-center gap-1 text-xs text-accent-600 hover:underline dark:text-accent-300"
                    >
                      View on GitHub
                      <ExternalLink className="h-3 w-3" />
                    </a>
                  )}
                </div>
                <div className="max-h-80 overflow-y-auto rounded-lg border border-[var(--border)] bg-[var(--bg)] p-4">
                  <ChangelogMarkdown changelog={data.changelog} />
                </div>
              </div>
            )}
          </div>
        )}
      </div>
    </Card>
  );
}