import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Sparkles, Zap, MessageSquare, Layers, Route, Wifi, Monitor, Database, Clock,
  ArrowUpCircle, CheckCircle2, ExternalLink, XCircle, Terminal, RefreshCw,
  Gauge, Eye, EyeOff, KeyRound, Download, Upload, ShieldCheck, Info,
  Palette, Shield, Bell, CircleDollarSign,
} from "lucide-react";
import { api, type EndpointSettings, type BrandingSettings, type HeadroomTestResult, type ForeignImportResult, type N9routerImportOptions, type N9routerAnalyzeResult, type LandingNotification, type CurrencyStatus } from "../lib/api";
import { sanitizeHtml } from "../lib/sanitizeHtml";
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
type SettingsTab = "saving" | "routing" | "network" | "branding" | "import-export" | "system" | "notifications" | "currency";

const settingsTabs = [
  { value: "saving" as const, label: "Token Saving", icon: Zap },
  { value: "routing" as const, label: "Routing", icon: Route },
  { value: "network" as const, label: "Network", icon: Gauge },
  { value: "branding" as const, label: "Branding", icon: Palette },
  { value: "import-export" as const, label: "Import / Export", icon: Database },
  { value: "system" as const, label: "System", icon: ArrowUpCircle },
  { value: "notifications" as const, label: "Notification", icon: Bell },
  { value: "currency" as const, label: "Currency", icon: CircleDollarSign },
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

const ponytailOptions = [
  { value: "lite", label: "Lite" },
  { value: "full", label: "Full" },
  { value: "ultra", label: "Ultra" },
];
const ponytailHints: Record<string, string> = {
  lite: "Light nudge toward minimal code.",
  full: "Balanced lazy-senior-dev bias.",
  ultra: "Maximum bias toward the smallest change.",
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
            {tab === "saving" && <SavingTab local={local} update={update} setLocal={setLocal} />}
            {tab === "routing" && <RoutingTab local={local} update={update} />}
            {tab === "network" && <NetworkTab local={local} update={update} />}
            {tab === "branding" && <BrandingTab />}
            {tab === "import-export" && <ImportExportTab />}
            {tab === "system" && <SystemTab />}
            {tab === "notifications" && <NotificationTab />}
            {tab === "currency" && <CurrencyTab />}
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
const HEADROOM_TIMEOUT_MIN = 1000;
const HEADROOM_TIMEOUT_MAX = 60000;
const PONYTAIL_LEVELS = ["lite", "full", "ultra"] as const;

const SAVER_VALIDATION_MESSAGES = {
  headroomUrl: "Proxy URL is required when Headroom is enabled.",
  headroomTimeout: `Timeout must be a whole number between ${HEADROOM_TIMEOUT_MIN} and ${HEADROOM_TIMEOUT_MAX} ms.`,
  ponytailLevel: `Ponytail level must be one of: ${PONYTAIL_LEVELS.join(", ")}.`,
} as const;

type SaverErrors = {
  headroom_url?: string;
  headroom_timeout_ms?: string;
  ponytail_level?: string;
};

// validateSaverSettings derives client-side validation errors for the
// Headroom/Ponytail controls. Checks only apply
// while the relevant saver is enabled, mirroring the visible controls.
function validateSaverSettings(s: EndpointSettings): SaverErrors {
  const errors: SaverErrors = {};
  if (s.headroom_enabled && !(s.headroom_url ?? "").trim()) {
    errors.headroom_url = SAVER_VALIDATION_MESSAGES.headroomUrl;
  }
  if (s.headroom_enabled) {
    const t = s.headroom_timeout_ms;
    if (!Number.isInteger(t) || t < HEADROOM_TIMEOUT_MIN || t > HEADROOM_TIMEOUT_MAX) {
      errors.headroom_timeout_ms = SAVER_VALIDATION_MESSAGES.headroomTimeout;
    }
  }
  if (s.ponytail_enabled && !PONYTAIL_LEVELS.includes(s.ponytail_level as (typeof PONYTAIL_LEVELS)[number])) {
    errors.ponytail_level = SAVER_VALIDATION_MESSAGES.ponytailLevel;
  }
  return errors;
}

function saverPatch(s: EndpointSettings): Partial<EndpointSettings> {
  return {
    headroom_enabled: s.headroom_enabled,
    headroom_url: s.headroom_url,
    headroom_compress_user_messages: s.headroom_compress_user_messages,
    headroom_timeout_ms: s.headroom_timeout_ms,
    ponytail_enabled: s.ponytail_enabled,
    ponytail_level: s.ponytail_level,
  };
}

// HeadroomAdvisory sets expectations for the Headroom saver: it works best
// against a fast, local proxy. Large or first-seen ("cold") contexts can take
// many seconds for the proxy to compress, in which case Headroom fails open
// (request passes through uncompressed, so it records 0 savings). The instant
// local savers don't have this caveat.
function HeadroomAdvisory() {
  return (
    <div className="border-t border-[var(--border)] px-6 py-4">
      <div className="flex gap-2.5 rounded-xl border border-[var(--border)] bg-[var(--bg)] p-3">
        <Info className="mt-0.5 h-4 w-4 shrink-0 text-[var(--text-muted)]" />
        <div className="text-xs leading-relaxed text-[var(--text-muted)]">
          <span className="font-medium text-[var(--text)]">Best for a fast, local proxy.</span>{" "}
          Headroom is called synchronously before each request. Large or first-seen
          contexts can take the proxy several seconds (sometimes much longer on CPU-only
          machines) to compress — when that exceeds the timeout below, Headroom{" "}
          <span className="font-medium text-[var(--text)]">fails open</span> (the request
          goes through uncompressed and records 0 savings). For consistent savings without
          an external dependency, rely on{" "}
          <span className="font-medium text-[var(--text)]">RTK</span> +{" "}
          <span className="font-medium text-[var(--text)]">Caveman/Terse</span> +{" "}
          <span className="font-medium text-[var(--text)]">Ponytail</span>, which run
          instantly in-process.
        </div>
      </div>
    </div>
  );
}

// HeadroomInstallHelp explains how to install and run a local Headroom proxy.
// Headroom is the open-source headroom-ai proxy; KeiRouter calls its
// /v1/compress endpoint. Shown inside the Headroom card so operators can get a
// proxy running before pointing KeiRouter at it.
function HeadroomInstallHelp() {
  return (
    <div className="border-t border-[var(--border)] px-6 py-4">
      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg)] p-4">
        <div className="flex items-center gap-2 text-sm font-medium">
          <Terminal className="h-4 w-4 text-[var(--text-muted)]" />
          Don&apos;t have a Headroom proxy yet?
        </div>
        <p className="mt-1 text-xs text-[var(--text-muted)]">
          Headroom is a local, open-source compression proxy. The{" "}
          <code className="rounded bg-[var(--bg-elevated)] px-1 py-0.5">headroom</code> CLI ships with the
          Python package (the npm package is a library only). Install it with pipx, then start it:
        </p>
        <pre className="mt-2 overflow-x-auto rounded-lg bg-[var(--bg-elevated)] px-3 py-2 text-xs leading-relaxed text-[var(--text)]">
          <code>{`pipx install "headroom-ai[all]"   # needs Python 3.10+ (or: pip install --user)
pipx ensurepath                   # add headroom to PATH, then restart your shell
headroom proxy --port 8787
headroom doctor                   # verify it's working`}</code>
        </pre>
        <p className="mt-2 text-xs text-[var(--text-muted)]">
          Then set <span className="font-medium text-[var(--text)]">Proxy URL</span> to{" "}
          <code className="rounded bg-[var(--bg-elevated)] px-1 py-0.5">http://localhost:8787</code>.
        </p>
        <a
          href="https://github.com/headroomlabs-ai/headroom"
          target="_blank"
          rel="noreferrer"
          className="mt-2 inline-flex items-center gap-1 text-xs font-medium text-secondary-600 hover:underline dark:text-secondary-400"
        >
          Installation guide <ExternalLink className="h-3 w-3" />
        </a>
      </div>
    </div>
  );
}

// HeadroomTestConnection validates that the configured proxy is actually
// running by probing its /v1/compress endpoint via the backend. The backend
// returns a masked endpoint and never leaks credentials.
function HeadroomTestConnection({ url, timeoutMs }: { url: string; timeoutMs: number }) {
  const [result, setResult] = useState<HeadroomTestResult | null>(null);
  const test = useMutation({
    mutationFn: () => api.testHeadroom({ url, timeout_ms: timeoutMs }),
    onSuccess: setResult,
    onError: (e) =>
      setResult({ ok: false, reachable: false, status: 0, latency_ms: 0, endpoint: "", message: (e as Error).message }),
  });
  const disabled = !url.trim() || test.isPending;
  return (
    <div className="border-t border-[var(--border)] px-6 py-4">
      <div className="flex flex-wrap items-center gap-3">
        <Button variant="ghost" disabled={disabled} onClick={() => test.mutate()}>
          {test.isPending ? "Testing…" : "Test connection"}
        </Button>
        {result && (
          <span
            className={`inline-flex items-center gap-1.5 text-xs ${
              result.ok ? "text-[color:var(--color-success)]" : "text-[color:var(--color-danger)]"
            }`}
          >
            {result.ok ? <CheckCircle2 className="h-4 w-4" /> : <XCircle className="h-4 w-4" />}
            {result.message}
            {result.latency_ms > 0 && <span className="text-[var(--text-muted)]">({result.latency_ms} ms)</span>}
          </span>
        )}
      </div>
      <p className="mt-2 text-xs text-[var(--text-muted)]">
        Checks that the proxy at the Proxy URL above responds on <code>/v1/compress</code>.
      </p>
    </div>
  );
}

function SavingTab({
  local,
  update,
  setLocal,
}: {
  local: EndpointSettings;
  update: (patch: Partial<EndpointSettings>) => void;
  setLocal: React.Dispatch<React.SetStateAction<EndpointSettings | null>>;
}) {
  const [saverErrors, setSaverErrors] = useState<SaverErrors>({});

  // saverUpdate validates the merged Headroom/Ponytail state before persisting.
  // Invalid values are reflected locally (so the operator sees what they typed)
  // and surfaced as inline errors, but they are NOT persisted.
  const saverUpdate = (patch: Partial<EndpointSettings>) => {
    const next = { ...local, ...patch };
    const errors = validateSaverSettings(next);
    setSaverErrors(errors);
    if (Object.keys(errors).length === 0) {
      update(saverPatch(next));
    } else {
      setLocal(next);
    }
  };

  return (
    <div className="space-y-4">
      <Card>
        <SectionHeader
          title="RTK input compression"
          description="Compresses bulky tool outputs (diffs, greps, listings, build logs) before they reach the model. Saves input tokens. Safe by design — never corrupts content."
          icon={Zap}
        />
        <div className="flex items-center justify-between border-t border-[var(--border)] px-6 py-4">
          <span className="text-sm font-medium">Enable RTK token saver</span>
          <Toggle checked={local.rtk_enabled} onChange={(v) => update({ rtk_enabled: v })} />
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

      <Card>
        <SectionHeader
          title="Headroom input compression"
          description="Compresses request messages through an external Headroom proxy before they reach the model. Fail-open — any proxy error leaves the request untouched."
          icon={Zap}
          iconTone="neutral"
        />
        <HeadroomAdvisory />
        <div className="flex items-center justify-between border-t border-[var(--border)] px-6 py-4">
          <span className="text-sm font-medium">Enable Headroom</span>
          <Toggle checked={local.headroom_enabled} onChange={(v) => saverUpdate({ headroom_enabled: v })} />
        </div>
        {local.headroom_enabled && (
          <>
            <div className="border-t border-[var(--border)] px-6 py-4">
              <Field label="Proxy URL">
                <Input
                  type="text"
                  placeholder="https://headroom.example.com"
                  value={local.headroom_url}
                  onChange={(e) => saverUpdate({ headroom_url: e.target.value })}
                  aria-invalid={!!saverErrors.headroom_url}
                />
                {saverErrors.headroom_url && (
                  <p className="text-xs text-[color:var(--color-danger)]">{saverErrors.headroom_url}</p>
                )}
              </Field>
            </div>
            <div className="flex items-center justify-between border-t border-[var(--border)] px-6 py-4">
              <div>
                <p className="text-sm font-medium">Compress user messages</p>
                <p className="mt-0.5 text-xs text-[var(--text-muted)]">Also send user messages to the proxy for compression.</p>
              </div>
              <Toggle
                checked={local.headroom_compress_user_messages}
                onChange={(v) => saverUpdate({ headroom_compress_user_messages: v })}
              />
            </div>
            <div className="border-t border-[var(--border)] px-6 py-4">
              <Field label="Timeout (ms)">
                <Input
                  type="number"
                  min={1000}
                  max={60000}
                  value={Number.isFinite(local.headroom_timeout_ms) ? local.headroom_timeout_ms : ""}
                  onChange={(e) => saverUpdate({ headroom_timeout_ms: e.target.valueAsNumber })}
                  aria-invalid={!!saverErrors.headroom_timeout_ms}
                />
                {saverErrors.headroom_timeout_ms && (
                  <p className="text-xs text-[color:var(--color-danger)]">{saverErrors.headroom_timeout_ms}</p>
                )}
              </Field>
            </div>
            <HeadroomTestConnection url={local.headroom_url} timeoutMs={local.headroom_timeout_ms} />
          </>
        )}
        <HeadroomInstallHelp />
      </Card>

      <Card>
        <SectionHeader
          title="Ponytail output compression"
          description="Injects a lazy-senior-developer system prompt that biases the model toward minimal code. Layers on top of Terse or Caveman."
          icon={MessageSquare}
          iconTone="neutral"
        />
        <div className="flex items-center justify-between border-t border-[var(--border)] px-6 py-4">
          <span className="text-sm font-medium">Enable Ponytail</span>
          <Toggle checked={local.ponytail_enabled} onChange={(v) => saverUpdate({ ponytail_enabled: v })} />
        </div>
        {local.ponytail_enabled && (
          <div className="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--border)] px-6 py-4">
            <div>
              <p className="text-sm font-medium">Ponytail level</p>
              <p className="mt-0.5 text-xs text-[var(--text-muted)]">{ponytailHints[local.ponytail_level]}</p>
              {saverErrors.ponytail_level && (
                <p className="mt-0.5 text-xs text-[color:var(--color-danger)]">{saverErrors.ponytail_level}</p>
              )}
            </div>
            <SegmentedControl
              value={local.ponytail_level}
              onChange={(v) => saverUpdate({ ponytail_level: v as "lite" | "full" | "ultra" })}
              options={ponytailOptions}
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

        {/* API key prefix */}
        <div className="px-6 py-5">
          <Field label="API Key Prefix">
            <Input
              value={local.api_key_prefix}
              onChange={(e) => update({ api_key_prefix: e.target.value })}
              placeholder="kr"
            />
            <p className="mt-1 text-xs text-[var(--text-muted)]">
              Used for newly created keys. Enter <code>tkr</code> or <code>tkr_</code> → new keys
              look like <code>tkr_xxxx</code>. Existing keys are unaffected.
            </p>
          </Field>
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

// ── Notification Tab ────────────────────────────────────────────────
const MAX_NOTIFICATIONS = 5;

function NotificationTab() {
  const qc = useQueryClient();
  const toast = useToast();
  const list = useQuery({ queryKey: ["landing-notifications"], queryFn: () => api.notifications() });
  const save = useMutation({
    mutationFn: (items: LandingNotification[]) => api.updateNotifications(items),
    onSuccess: (data) => {
      qc.setQueryData(["landing-notifications"], data);
      qc.invalidateQueries({ queryKey: ["public-notifications"] });
      toast.success("Notifications saved");
    },
    onError: (e) => toast.error("Save failed", (e as Error).message),
  });

  const [editing, setEditing] = useState<{ index: number; item: LandingNotification } | null>(null);
  const items = list.data?.notifications ?? [];

  const persist = (next: LandingNotification[]) => save.mutate(next);

  const openAdd = () => {
    setEditing({ index: -1, item: { id: "", title: "", body: "", tag: "", href: "" } });
  };
  const openEdit = (index: number) => setEditing({ index, item: { ...items[index] } });

  const commit = () => {
    if (!editing) return;
    const it = editing.item;
    if (!it.title.trim()) return;
    const next = editing.index < 0 ? [...items, it] : items.map((x, i) => (i === editing.index ? it : x));
    persist(next);
    setEditing(null);
  };

  const remove = (index: number) => {
    if (!confirm("Delete this notification?")) return;
    persist(items.filter((_, i) => i !== index));
  };

  if (list.isLoading) return <Spinner />;
  if (list.isError) {
    return (
      <ErrorBanner
        message={`Failed to load notifications: ${(list.error as Error)?.message ?? "unknown error"}`}
      />
    );
  }

  return (
    <Card>
      <SectionHeader
        title="Landing page notifications"
        description={`Announcements shown in the landing page bell. Max ${MAX_NOTIFICATIONS}. Title and body support limited HTML (bold, italic, links).`}
        icon={Bell}
      />
      <div className="divide-y divide-[var(--border)] border-t border-[var(--border)]">
        {items.length === 0 && (
          <p className="px-6 py-8 text-center text-sm text-[var(--text-muted)]">No notifications yet.</p>
        )}
        {items.map((n, i) => (
          <div key={n.id || i} className="flex items-start justify-between gap-4 px-6 py-4">
            <div className="min-w-0">
              <p className="flex items-center gap-2 text-sm font-medium">
                {n.title}
                {n.tag && (
                  <span className="rounded-full bg-[var(--bg-subtle)] px-2 py-0.5 text-[10px] font-semibold uppercase text-[var(--text-muted)]">{n.tag}</span>
                )}
              </p>
              <p
                className="mt-0.5 truncate text-xs text-[var(--text-muted)] [&_a]:underline"
                dangerouslySetInnerHTML={{ __html: sanitizeHtml(n.body) }}
              />
            </div>
            <div className="flex shrink-0 gap-2">
              <Button variant="ghost" onClick={() => openEdit(i)}>Edit</Button>
              <Button variant="ghost" onClick={() => remove(i)}>Delete</Button>
            </div>
          </div>
        ))}
        <div className="flex justify-end px-6 py-4">
          <Button onClick={openAdd} disabled={items.length >= MAX_NOTIFICATIONS || save.isPending}>
            Add notification
          </Button>
        </div>
      </div>

      <Modal
        open={editing !== null}
        onClose={() => setEditing(null)}
        title={editing && editing.index < 0 ? "Add notification" : "Edit notification"}
      >
        {editing && (
          <>
            <div className="space-y-4 px-6 py-5">
              <Field label="Title (HTML allowed)">
                <Input value={editing.item.title} onChange={(e) => setEditing({ ...editing, item: { ...editing.item, title: e.target.value } })} />
              </Field>
              <Field label="Tag (optional)">
                <Input value={editing.item.tag ?? ""} onChange={(e) => setEditing({ ...editing, item: { ...editing.item, tag: e.target.value } })} />
              </Field>
              <Field label="Body (HTML allowed)">
                <Input value={editing.item.body} onChange={(e) => setEditing({ ...editing, item: { ...editing.item, body: e.target.value } })} />
              </Field>
              <Field label="Link (optional)">
                <Input value={editing.item.href ?? ""} onChange={(e) => setEditing({ ...editing, item: { ...editing.item, href: e.target.value } })} placeholder="https://…" />
              </Field>
              <div>
                <p className="mb-1 text-xs font-medium text-[var(--text-muted)]">Preview</p>
                <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-3 text-sm">
                  <p className="font-medium [&_a]:underline" dangerouslySetInnerHTML={{ __html: sanitizeHtml(editing.item.title) }} />
                  <p className="mt-0.5 text-xs text-[var(--text-muted)] [&_a]:underline" dangerouslySetInnerHTML={{ __html: sanitizeHtml(editing.item.body) }} />
                </div>
              </div>
            </div>
            <div className="flex justify-end gap-2 border-t border-[var(--border)] px-6 py-4">
              <Button variant="ghost" onClick={() => setEditing(null)}>Cancel</Button>
              <Button onClick={commit} disabled={!editing.item.title.trim() || save.isPending}>
                {save.isPending ? "Saving…" : "Save"}
              </Button>
            </div>
          </>
        )}
      </Modal>
    </Card>
  );
}

// ── Currency Tab ────────────────────────────────────────────────────
function CurrencyTab() {
  const qc = useQueryClient();
  const toast = useToast();
  const status = useQuery({ queryKey: ["currency-settings"], queryFn: () => api.currencySettings() });
  const [local, setLocal] = useState<CurrencyStatus["config"] | null>(null);

  useEffect(() => {
    if (status.data) setLocal(status.data.config);
  }, [status.data]);

  const save = useMutation({
    mutationFn: (patch: Partial<CurrencyStatus["config"]>) => api.updateCurrencySettings(patch),
    onSuccess: (data) => {
      setLocal(data.config);
      qc.setQueryData(["currency-settings"], data);
      toast.success("Currency settings saved");
    },
    onError: (e) => toast.error("Save failed", (e as Error).message),
  });

  const refresh = useMutation({
    mutationFn: () => api.refreshCurrency(),
    onSuccess: (data) => {
      setLocal(data.config);
      qc.setQueryData(["currency-settings"], data);
      if (data.last_error) toast.error("Refresh failed", data.last_error);
      else toast.success("Rate updated", `1 USD = ${data.effective_rate} IDR`);
    },
    onError: (e) => toast.error("Refresh failed", (e as Error).message),
  });

  const update = (patch: Partial<CurrencyStatus["config"]>) => {
    if (!local) return;
    const next = { ...local, ...patch };
    setLocal(next);
    save.mutate(patch);
  };

  if (status.isLoading || !local) return <Spinner />;
  if (status.isError) {
    return <ErrorBanner message={`Failed to load currency settings: ${(status.error as Error)?.message ?? "unknown error"}`} />;
  }

  const st = status.data!;
  const sourceLabel: Record<string, string> = {
    api: "Live API",
    override: "Manual override",
    none: "No rate yet",
  };

  return (
    <Card>
      <SectionHeader
        title="Currency"
        description="USD→IDR rate used to convert IDR top-ups into USD credit. Auto-refreshes on an interval, with an optional manual override."
        icon={CircleDollarSign}
      />
      <div className="divide-y divide-[var(--border)] border-t border-[var(--border)]">
        <div className="px-6 py-5">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <div className="flex items-center justify-between">
                <span className="text-sm font-medium">Auto refresh</span>
                <Toggle checked={local.auto_refresh_enabled} onChange={(v) => update({ auto_refresh_enabled: v })} />
              </div>
              <p className="mt-1 text-xs text-[var(--text-muted)]">Fetch the rate automatically in the background.</p>
            </div>
            <Field label="Refresh interval (hours)">
              <Input
                type="number"
                min={1}
                value={local.refresh_interval_h}
                onChange={(e) => update({ refresh_interval_h: Number(e.target.value) || 1 })}
              />
              <p className="mt-1 text-xs text-[var(--text-muted)]">Default 24. The upstream rate updates about once a day.</p>
            </Field>
          </div>
        </div>

        <div className="px-6 py-5">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <div className="flex items-center justify-between">
                <span className="text-sm font-medium">Override rate</span>
                <Toggle checked={local.override_enabled} onChange={(v) => update({ override_enabled: v })} />
              </div>
              <p className="mt-1 text-xs text-[var(--text-muted)]">While on, the API rate is ignored entirely.</p>
            </div>
            <Field label="IDR per 1 USD">
              <Input
                type="number"
                min={0}
                step="0.01"
                disabled={!local.override_enabled}
                value={local.override_rate}
                onChange={(e) => update({ override_rate: Number(e.target.value) || 0 })}
              />
            </Field>
          </div>
        </div>

        <div className="px-6 py-5">
          <Field label="Source URL">
            <Input value={local.source_url} onChange={(e) => update({ source_url: e.target.value })} />
          </Field>
        </div>

        <div className="flex flex-wrap items-center justify-between gap-3 px-6 py-5">
          <div className="text-sm">
            <p className="font-medium">
              1 USD = {st.effective_rate > 0 ? `${st.effective_rate} IDR` : "—"}
            </p>
            <p className="mt-0.5 text-xs text-[var(--text-muted)]">
              Source: {sourceLabel[st.source] ?? st.source}
              {st.fetched_at ? ` · updated ${new Date(st.fetched_at).toLocaleString()}` : ""}
            </p>
            {st.last_error && <p className="mt-1 text-xs text-red-500">Last error: {st.last_error}</p>}
          </div>
          <Button onClick={() => refresh.mutate()} disabled={refresh.isPending}>
            {refresh.isPending ? "Refreshing…" : "Refresh now"}
          </Button>
        </div>
      </div>
    </Card>
  );
}

// ── Import / Export Tab ─────────────────────────────────────────────
function ImportExportTab() {
  return (
    <div className="space-y-4">
      <ForeignImportSettings />
      <DatabaseSettings />
    </div>
  );
}

// ── System Tab ──────────────────────────────────────────────────────
function SystemTab() {
  return (
    <div className="space-y-4">
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

function ForeignImportSettings() {
  const toast = useToast();
  const import9rRef = useRef<HTMLInputElement>(null);
  const importOmniRef = useRef<HTMLInputElement>(null);
  const importSqliteRef = useRef<HTMLInputElement>(null);
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<ForeignImportResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [analyze, setAnalyze] = useState<N9routerAnalyzeResult | null>(null);
  const [pendingSqliteFile, setPendingSqliteFile] = useState<File | null>(null);
  const [sqliteOptions, setSqliteOptions] = useState<N9routerImportOptions>({
    usage: true,
    providers: true,
    api_keys: true,
    proxy_pools: true,
    chains: true,
    settings: true,
    password: true,
    mode: "merge",
  });
  const setSection = (key: keyof N9routerImportOptions, v: boolean) =>
    setSqliteOptions((o) => ({ ...o, [key]: v }));

  const runImport = async (source: "9router" | "omniroute", file: File) => {
    setLoading(true);
    setError(null);
    setResult(null);
    try {
      const raw = await file.text();
      const config = JSON.parse(raw);
      const res = await api.importForeignConfig(source, config);
      setResult(res);
      const parts: string[] = [];
      if (res.accounts) parts.push(`${res.accounts} account${res.accounts === 1 ? "" : "s"}`);
      if (res.custom_providers) parts.push(`${res.custom_providers} provider${res.custom_providers === 1 ? "" : "s"}`);
      if (res.api_keys) parts.push(`${res.api_keys} key${res.api_keys === 1 ? "" : "s"}`);
      if (res.chains) parts.push(`${res.chains} chain${res.chains === 1 ? "" : "s"}`);
      if (res.aliases) parts.push(`${res.aliases} alias${res.aliases === 1 ? "" : "es"}`);
      if (res.proxy_pools) parts.push(`${res.proxy_pools} pool${res.proxy_pools === 1 ? "" : "s"}`);
      const summary = parts.length ? parts.join(", ") : "nothing";
      toast.success(
        `${source === "9router" ? "9router" : "OmniRoute"} import complete`,
        `${res.imported} record${res.imported === 1 ? "" : "s"} imported (${summary}).${res.skipped ? ` ${res.skipped} skipped.` : ""}`,
      );
    } catch (e) {
      setError((e as Error).message || "Import failed.");
      toast.error("Import failed", (e as Error).message);
    } finally {
      setLoading(false);
    }
  };

  const handle9rFile = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) void runImport("9router", file);
    if (import9rRef.current) import9rRef.current.value = "";
  };

  const handleOmniFile = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) void runImport("omniroute", file);
    if (importOmniRef.current) importOmniRef.current.value = "";
  };

  const handleSqliteFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    setLoading(true);
    setError(null);
    setResult(null);
    setAnalyze(null);
    try {
      const counts = await api.analyze9routerSQLite(file);
      setAnalyze(counts);
      setPendingSqliteFile(file);
    } catch (err) {
      setError((err as Error).message || "Analyze failed.");
      toast.error("Analyze failed", (err as Error).message);
    } finally {
      setLoading(false);
      if (importSqliteRef.current) importSqliteRef.current.value = "";
    }
  };

  const runSqliteImport = async () => {
    if (!pendingSqliteFile) return;
    setLoading(true);
    setError(null);
    setResult(null);
    try {
      const res = await api.import9routerSQLite(pendingSqliteFile, sqliteOptions);
      setResult(res);
      const parts: string[] = [];
      if (res.accounts) parts.push(`${res.accounts} account${res.accounts === 1 ? "" : "s"}`);
      if (res.custom_providers) parts.push(`${res.custom_providers} provider${res.custom_providers === 1 ? "" : "s"}`);
      if (res.api_keys) parts.push(`${res.api_keys} key${res.api_keys === 1 ? "" : "s"}`);
      if (res.chains) parts.push(`${res.chains} chain${res.chains === 1 ? "" : "s"}`);
      if (res.aliases) parts.push(`${res.aliases} alias${res.aliases === 1 ? "" : "es"}`);
      if (res.proxy_pools) parts.push(`${res.proxy_pools} pool${res.proxy_pools === 1 ? "" : "s"}`);
      if (res.usage_records) parts.push(`${res.usage_records} usage record${res.usage_records === 1 ? "" : "s"}`);
      const summary = parts.length ? parts.join(", ") : "nothing";
      toast.success(
        "9router SQLite import complete",
        `${res.imported} record${res.imported === 1 ? "" : "s"} imported (${summary}).${res.skipped ? ` ${res.skipped} skipped.` : ""}`,
      );
      setPendingSqliteFile(null);
      setAnalyze(null);
    } catch (err) {
      setError((err as Error).message || "Import failed.");
      toast.error("Import failed", (err as Error).message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <Card>
      <SectionHeader
        title="Import from other routers"
        description="Migrate providers, keys, and routing chains from a 9router or OmniRoute backup. Imports are additive — existing data is kept."
        icon={Upload}
      />
      <div className="grid gap-4 border-t border-[var(--border)] px-6 py-4 sm:grid-cols-2">
        {/* 9router */}
        <div className="flex flex-col gap-3 rounded-xl border border-[var(--border)] bg-[var(--bg)] p-4">
          <div className="flex items-center gap-2">
            <span className="inline-flex h-7 w-7 items-center justify-center rounded-md bg-accent-100 text-xs font-bold text-accent-700 dark:bg-accent-900/40 dark:text-accent-200">
              9R
            </span>
            <div>
              <p className="text-sm font-semibold text-[var(--text)]">9router backup</p>
              <p className="text-[11px] text-[var(--text-muted)]">Full credential transfer</p>
            </div>
          </div>
          <p className="text-xs leading-relaxed text-[var(--text-muted)]">
            Imports provider connections (API keys &amp; OAuth tokens re-sealed), custom provider nodes, API keys
            (re-hashed — same key string keeps working), combos (→ chains), proxy pools, and model aliases.
          </p>
          <Button variant="ghost" onClick={() => import9rRef.current?.click()} disabled={loading} className="w-full">
            <Upload className="h-4 w-4" />
            Select 9router backup JSON
          </Button>
          <input
            ref={import9rRef}
            type="file"
            accept="application/json,.json"
            className="hidden"
            onChange={handle9rFile}
          />
        </div>

        {/* OmniRoute */}
        <div className="flex flex-col gap-3 rounded-xl border border-[var(--border)] bg-[var(--bg)] p-4">
          <div className="flex items-center gap-2">
            <span className="inline-flex h-7 w-7 items-center justify-center rounded-md bg-secondary-100 text-xs font-bold text-secondary-700 dark:bg-secondary-900/40 dark:text-secondary-200">
              OR
            </span>
            <div>
              <p className="text-sm font-semibold text-[var(--text)]">OmniRoute backup</p>
              <p className="text-[11px] text-[var(--text-muted)]">Structural import (creds redacted)</p>
            </div>
          </div>
          <p className="text-xs leading-relaxed text-[var(--text-muted)]">
            OmniRoute JSON exports redact credentials, so accounts are imported as disabled stubs — re-authenticate after
            import. Custom provider nodes, combos (→ chains), proxy pools, and aliases transfer fully. API keys must be
            re-created.
          </p>
          <Button variant="ghost" onClick={() => importOmniRef.current?.click()} disabled={loading} className="w-full">
            <Upload className="h-4 w-4" />
            Select OmniRoute backup JSON
          </Button>
          <input
            ref={importOmniRef}
            type="file"
            accept="application/json,.json"
            className="hidden"
            onChange={handleOmniFile}
          />
        </div>

        {/* 9router SQLite direct import */}
        <div className="flex flex-col gap-3 rounded-xl border border-[var(--border)] bg-[var(--bg)] p-4 sm:col-span-2">
          <div className="flex items-center gap-2">
            <span className="inline-flex h-7 w-7 items-center justify-center rounded-md bg-accent-100 text-xs font-bold text-accent-700 dark:bg-accent-900/40 dark:text-accent-200">
              DB
            </span>
            <div>
              <p className="text-sm font-semibold text-[var(--text)]">9router SQLite database</p>
              <p className="text-[11px] text-[var(--text-muted)]">Full import incl. usage history &amp; settings</p>
            </div>
          </div>

          {!analyze && !pendingSqliteFile && (
            <>
              <p className="text-xs leading-relaxed text-[var(--text-muted)]">
                Upload 9router's <code>data.sqlite</code> directly. Imports everything the JSON backup does, plus usage
                history, token saver settings, routing strategy, and the dashboard password. Analyzes the file first
                so you can choose exactly which sections to import.
              </p>
              <Button variant="ghost" onClick={() => importSqliteRef.current?.click()} disabled={loading} className="w-full">
                <Upload className="h-4 w-4" />
                Select 9router data.sqlite
              </Button>
              <input
                ref={importSqliteRef}
                type="file"
                accept=".sqlite,.db,application/vnd.sqlite3,application/x-sqlite3"
                className="hidden"
                onChange={handleSqliteFile}
              />
            </>
          )}

          {analyze && (
            <div className="space-y-4">
              {/* Per-table row counts from the uploaded file */}
              <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-3">
                <p className="mb-2 text-xs font-semibold uppercase text-[var(--text-muted)]">Detected in file</p>
                <div className="flex flex-wrap gap-x-5 gap-y-1 text-xs text-[var(--text)]">
                  {analyze.providerConnections != null && <span>{analyze.providerConnections} providers/accounts</span>}
                  {analyze.providerNodes != null && <span>{analyze.providerNodes} custom nodes</span>}
                  {analyze.apiKeys != null && <span>{analyze.apiKeys} API keys</span>}
                  {analyze.combos != null && <span>{analyze.combos} chains</span>}
                  {analyze.proxyPools != null && <span>{analyze.proxyPools} proxy pools</span>}
                  {analyze.usageHistory != null && <span>{analyze.usageHistory.toLocaleString()} usage records</span>}
                </div>
              </div>

              {/* Section checkboxes */}
              <div className="space-y-2">
                <p className="text-xs font-semibold uppercase text-[var(--text-muted)]">Sections to import</p>
                <label className="flex items-start gap-2 text-xs text-[var(--text)]">
                  <input type="checkbox" checked={sqliteOptions.usage} onChange={(e) => setSection("usage", e.target.checked)} className="mt-0.5" />
                  <span>Usage records <span className="text-[var(--text-muted)]">— token usage, costs, model stats</span></span>
                </label>
                <label className="flex items-start gap-2 text-xs text-[var(--text)]">
                  <input type="checkbox" checked={sqliteOptions.providers} onChange={(e) => setSection("providers", e.target.checked)} className="mt-0.5" />
                  <span>Providers &amp; accounts <span className="text-[var(--text-muted)]">— connections, custom nodes, credentials re-encrypted</span></span>
                </label>
                <label className="flex items-start gap-2 text-xs text-[var(--text)]">
                  <input type="checkbox" checked={sqliteOptions.api_keys} onChange={(e) => setSection("api_keys", e.target.checked)} className="mt-0.5" />
                  <span>API keys <span className="text-[var(--text-muted)]">— re-hashed; same key strings keep working</span></span>
                </label>
                <label className="flex items-start gap-2 text-xs text-[var(--text)]">
                  <input type="checkbox" checked={sqliteOptions.proxy_pools} onChange={(e) => setSection("proxy_pools", e.target.checked)} className="mt-0.5" />
                  <span>Proxy pools <span className="text-[var(--text-muted)]">— Cloudflare / HTTP proxy configs</span></span>
                </label>
                <label className="flex items-start gap-2 text-xs text-[var(--text)]">
                  <input type="checkbox" checked={sqliteOptions.chains} onChange={(e) => setSection("chains", e.target.checked)} className="mt-0.5" />
                  <span>Routing chains <span className="text-[var(--text-muted)]">— combos → chains (fallback/RR strategies)</span></span>
                </label>
                <label className="flex items-start gap-2 text-xs text-[var(--text)]">
                  <input type="checkbox" checked={sqliteOptions.settings} onChange={(e) => setSection("settings", e.target.checked)} className="mt-0.5" />
                  <span>Settings <span className="text-[var(--text-muted)]">— token saver (RTK/Caveman/Ponytail), routing strategy</span></span>
                </label>
                <label className="flex items-start gap-2 text-xs text-[var(--text)]">
                  <input type="checkbox" checked={sqliteOptions.password} onChange={(e) => setSection("password", e.target.checked)} className="mt-0.5" />
                  <span>Dashboard password <span className="text-[var(--text-muted)]">— import 9router's bcrypt hash (triggers re-login)</span></span>
                </label>
              </div>

              {/* Mode radio */}
              <div className="space-y-2">
                <p className="text-xs font-semibold uppercase text-[var(--text-muted)]">Import mode</p>
                <label className="flex items-start gap-2 text-xs text-[var(--text)]">
                  <input type="radio" name="n9mode" value="merge" checked={sqliteOptions.mode === "merge"} onChange={() => setSqliteOptions((o) => ({ ...o, mode: "merge" }))} className="mt-0.5" />
                  <span>Merge <span className="text-[var(--text-muted)]">— add new rows, skip existing (safe, repeatable)</span></span>
                </label>
                <label className="flex items-start gap-2 text-xs text-[var(--text)]">
                  <input type="radio" name="n9mode" value="overwrite" checked={sqliteOptions.mode === "overwrite"} onChange={() => setSqliteOptions((o) => ({ ...o, mode: "overwrite" }))} className="mt-0.5" />
                  <span>Overwrite <span className="text-[var(--text-muted)]">— remove previous 9router imports, then re-import (clean sync)</span></span>
                </label>
                <label className="flex items-start gap-2 text-xs text-[var(--text)]">
                  <input type="radio" name="n9mode" value="wipe" checked={sqliteOptions.mode === "wipe"} onChange={() => setSqliteOptions((o) => ({ ...o, mode: "wipe" }))} className="mt-0.5" />
                  <span>Wipe &amp; replace <span className="text-[var(--text-muted)] font-medium text-red-500">— DESTROYS all selected data including KeiRouter-native rows</span></span>
                </label>
                {sqliteOptions.mode === "wipe" && (
                  <p className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-[11px] text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300">
                    ⚠️ Wipe mode deletes ALL rows in the selected sections, not just previously imported ones. A safety
                    backup is created automatically before any deletion.
                  </p>
                )}
              </div>

              {/* Action buttons */}
              <div className="flex gap-2">
                <Button
                  variant="primary"
                  onClick={runSqliteImport}
                  disabled={loading || !Object.values(sqliteOptions).some((v) => typeof v === "boolean" && v)}
                  className="w-full"
                >
                  {loading ? "Importing…" : "Run import"}
                </Button>
                <Button
                  variant="ghost"
                  onClick={() => { setAnalyze(null); setPendingSqliteFile(null); }}
                  disabled={loading}
                >
                  Cancel
                </Button>
              </div>
            </div>
          )}
        </div>
      </div>

      {error && (
        <div className="border-t border-[var(--border)] px-6 pb-4">
          <ErrorBanner message={error} />
        </div>
      )}

      {result && (
        <div className="space-y-3 border-t border-[var(--border)] px-6 py-4">
          <div className="flex flex-wrap gap-x-6 gap-y-2 text-xs">
            <Stat label="Accounts" value={result.accounts} />
            <Stat label="Custom providers" value={result.custom_providers} />
            <Stat label="API keys" value={result.api_keys} />
            <Stat label="Chains" value={result.chains} />
            <Stat label="Aliases" value={result.aliases} />
            <Stat label="Proxy pools" value={result.proxy_pools} />
            <Stat label="Skipped" value={result.skipped} muted />
          </div>
          {result.errors && result.errors.length > 0 && (
            <details className="rounded-lg border border-[var(--border)] bg-[var(--bg)] px-3 py-2">
              <summary className="cursor-pointer text-xs font-medium text-[var(--text-muted)]">
                {result.errors.length} warning{result.errors.length === 1 ? "" : "s"}
              </summary>
              <ul className="mt-2 max-h-40 space-y-1 overflow-y-auto text-[11px] text-[var(--text-muted)]">
                {result.errors.map((e, i) => (
                  <li key={i} className="font-mono break-all">{e}</li>
                ))}
              </ul>
            </details>
          )}
        </div>
      )}
    </Card>
  );
}

function Stat({ label, value, muted }: { label: string; value: number; muted?: boolean }) {
  return (
    <div>
      <span className="text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">{label}</span>{" "}
      <span className={`font-mono text-sm ${muted ? "text-[var(--text-muted)]" : "text-[var(--text)]"}`}>{value}</span>
    </div>
  );
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
  const qc = useQueryClient();
  const toast = useToast();
  const [checking, setChecking] = useState(false);

  const checkNow = async () => {
    setChecking(true);
    try {
      const fresh = await api.updateCheck(true);
      qc.setQueryData(["update-check"], fresh);
      if (!fresh.checked) {
        toast.error("Update check failed", "Could not reach GitHub. Try again later.");
      } else if (fresh.update_available) {
        toast.success("Update available", `${fresh.latest} is ready to install.`);
      } else {
        toast.success("Up to date", `You're running the latest version (${fresh.current}).`);
      }
    } catch (e) {
      toast.error("Update check failed", (e as Error).message);
    } finally {
      setChecking(false);
    }
  };

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
        action={
          <Button variant="ghost" className="h-8 px-3 text-xs" onClick={checkNow} disabled={checking}>
            <RefreshCw className={`h-3.5 w-3.5 ${checking ? "animate-spin" : ""}`} />
            {checking ? "Checking…" : "Check now"}
          </Button>
        }
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