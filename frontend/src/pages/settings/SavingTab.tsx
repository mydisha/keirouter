import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { CheckCircle2, ExternalLink, XCircle } from "lucide-react";
import { api, type EndpointSettings, type HeadroomTestResult } from "../../lib/api";
import { Button, SegmentedControl, Toggle } from "../../components/ui";
import { Code, Note, SettingRow, SettingsCard, ToggleRow, UnitInput, inputClass } from "./shared";

// ── Level options ───────────────────────────────────────────────────
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

// ── Validation ──────────────────────────────────────────────────────
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

// Wide segmented groups (caveman has six levels) scroll instead of
// overflowing on narrow screens.
function LevelControl({ children }: { children: React.ReactNode }) {
  return <div className="max-w-full overflow-x-auto">{children}</div>;
}

export function SavingTab({
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
      <SettingsCard
        title="Input compression"
        description="Shrinks what is sent to the model. Saves input tokens."
      >
        <ToggleRow
          label="RTK input compression"
          description="Shrinks bulky tool output (diffs, greps, listings, build logs) before it reaches the model. Safe by design — never corrupts content."
        >
          <Toggle checked={local.rtk_enabled} onChange={(v) => update({ rtk_enabled: v })} />
        </ToggleRow>
        {local.rtk_enabled && (
          <SettingRow nested label="Source code filter" description={rtkFilterHints[local.rtk_filter_level || "none"]}>
            <SegmentedControl
              value={local.rtk_filter_level || "none"}
              onChange={(v) => update({ rtk_filter_level: v })}
              options={rtkFilterOptions}
            />
          </SettingRow>
        )}
      </SettingsCard>

      <SettingsCard
        title="Output compression"
        description="Asks the model to answer more briefly. Saves output tokens. Caveman and terse both add a system instruction, so turning one on turns the other off."
      >
        <ToggleRow
          label="Caveman mode"
          description="Model answers in terse caveman style — keeps all technical substance, drops filler. Cuts output tokens 65–75%."
        >
          <Toggle
            checked={local.caveman_enabled}
            onChange={(v) => update({ caveman_enabled: v, ...(v ? { terse_enabled: false } : {}) })}
          />
        </ToggleRow>
        {local.caveman_enabled && (
          <SettingRow nested label="Compression level" description={cavemanHints[local.caveman_level]}>
            <LevelControl>
              <SegmentedControl
                value={local.caveman_level}
                onChange={(v) => update({ caveman_level: v })}
                options={cavemanOptions}
              />
            </LevelControl>
          </SettingRow>
        )}

        <ToggleRow
          label="Terse mode"
          description="KeiRouter's own concise-output instruction. An alternative to caveman."
        >
          <Toggle
            checked={local.terse_enabled}
            onChange={(v) => update({ terse_enabled: v, ...(v ? { caveman_enabled: false } : {}) })}
          />
        </ToggleRow>
        {local.terse_enabled && (
          <SettingRow nested label="Terse level" description={terseHints[local.terse_level]}>
            <SegmentedControl
              value={local.terse_level}
              onChange={(v) => update({ terse_level: v })}
              options={terseOptions}
            />
          </SettingRow>
        )}

        <ToggleRow
          label="Ponytail"
          description="Adds a lazy-senior-developer system prompt that biases the model toward minimal code. Layers on top of caveman or terse."
        >
          <Toggle checked={local.ponytail_enabled} onChange={(v) => saverUpdate({ ponytail_enabled: v })} />
        </ToggleRow>
        {local.ponytail_enabled && (
          <SettingRow
            nested
            label="Ponytail level"
            description={ponytailHints[local.ponytail_level]}
            error={saverErrors.ponytail_level}
          >
            <SegmentedControl
              value={local.ponytail_level}
              onChange={(v) => saverUpdate({ ponytail_level: v as "lite" | "full" | "ultra" })}
              options={ponytailOptions}
            />
          </SettingRow>
        )}
      </SettingsCard>

      <SettingsCard
        title="Headroom proxy"
        description="External input compression. Request messages go through a Headroom proxy before they reach the model."
      >
        <div className="px-4 py-3.5">
          <HeadroomAdvisory />
        </div>
        <ToggleRow
          label="Headroom input compression"
          description="Fail-open — any proxy error leaves the request untouched."
        >
          <Toggle checked={local.headroom_enabled} onChange={(v) => saverUpdate({ headroom_enabled: v })} />
        </ToggleRow>
        {local.headroom_enabled && (
          <>
            <SettingRow
              nested
              label="Proxy URL"
              description="Base URL of your Headroom proxy."
              error={saverErrors.headroom_url}
            >
              <input
                type="text"
                aria-label="Proxy URL"
                placeholder="https://headroom.example.com"
                value={local.headroom_url}
                onChange={(e) => saverUpdate({ headroom_url: e.target.value })}
                aria-invalid={!!saverErrors.headroom_url}
                className={`${inputClass} font-mono sm:w-80`}
              />
            </SettingRow>
            <ToggleRow nested label="Compress user messages" description="Also send user messages to the proxy for compression.">
              <Toggle
                checked={local.headroom_compress_user_messages}
                onChange={(v) => saverUpdate({ headroom_compress_user_messages: v })}
              />
            </ToggleRow>
            <SettingRow
              nested
              label="Timeout"
              description={`Give up and send the request uncompressed after this long. ${HEADROOM_TIMEOUT_MIN.toLocaleString()}–${HEADROOM_TIMEOUT_MAX.toLocaleString()} ms.`}
              error={saverErrors.headroom_timeout_ms}
            >
              <UnitInput
                unit="ms"
                aria-label="Timeout (ms)"
                min={1000}
                max={60000}
                value={Number.isFinite(local.headroom_timeout_ms) ? local.headroom_timeout_ms : ""}
                onChange={(e) => saverUpdate({ headroom_timeout_ms: e.target.valueAsNumber })}
                aria-invalid={!!saverErrors.headroom_timeout_ms}
              />
            </SettingRow>
            <HeadroomTestConnection url={local.headroom_url} timeoutMs={local.headroom_timeout_ms} />
          </>
        )}
        <HeadroomInstallHelp />
      </SettingsCard>
    </div>
  );
}

// HeadroomAdvisory sets expectations for the Headroom saver: it works best
// against a fast, local proxy. Large or first-seen ("cold") contexts can take
// many seconds for the proxy to compress, in which case Headroom fails open
// (request passes through uncompressed, so it records 0 savings). The instant
// local savers don't have this caveat.
function HeadroomAdvisory() {
  return (
    <Note>
      <span className="font-medium text-fg">Best with a fast, local proxy.</span> Headroom runs synchronously before
      each request. Large or first-seen contexts can take the proxy several seconds to compress (much longer on CPU-only
      machines). Past the timeout Headroom <span className="font-medium text-fg">fails open</span>: the request goes
      through uncompressed and records 0 savings. For consistent savings with no external dependency, rely on RTK,
      caveman/terse and Ponytail — they run instantly in-process.
    </Note>
  );
}

// HeadroomInstallHelp explains how to install and run a local Headroom proxy.
// Headroom is the open-source headroom-ai proxy; KeiRouter calls its
// /v1/compress endpoint. Shown inside the Headroom card so operators can get a
// proxy running before pointing KeiRouter at it.
function HeadroomInstallHelp() {
  return (
    <div className="bg-subtle px-4 py-3.5">
      <p className="text-[13px] font-medium text-fg">Don&apos;t have a Headroom proxy yet?</p>
      <p className="mt-0.5 text-[12px] leading-5 text-fg-muted">
        Headroom is a local, open-source compression proxy. The <Code>headroom</Code> CLI ships with the Python package
        (the npm package is a library only). Install it with pipx, then start it:
      </p>
      <pre className="mt-2 overflow-x-auto rounded-lg border border-line bg-surface px-3 py-2 font-mono text-[12px] leading-5 text-fg">
        <code>{`pipx install "headroom-ai[all]"   # needs Python 3.10+ (or: pip install --user)
pipx ensurepath                   # add headroom to PATH, then restart your shell
headroom proxy --port 8787
headroom doctor                   # verify it's working`}</code>
      </pre>
      <div className="mt-2 flex flex-wrap items-center justify-between gap-2 text-[12px] text-fg-muted">
        <span>
          Then set <span className="font-medium text-fg">Proxy URL</span> to <Code>http://localhost:8787</Code>.
        </span>
        <a
          href="https://github.com/headroomlabs-ai/headroom"
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-1 font-medium text-accent-500 hover:underline dark:text-accent-400"
        >
          Installation guide <ExternalLink className="h-3.5 w-3.5" strokeWidth={1.75} />
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
    <SettingRow
      nested
      label="Test connection"
      description={
        <>
          Checks that the proxy at the Proxy URL above responds on <Code>/v1/compress</Code>.
          {result && (
            <span className={`mt-1 flex items-center gap-1.5 ${result.ok ? "text-ok" : "text-bad"}`} aria-live="polite">
              {result.ok ? <CheckCircle2 className="h-3.5 w-3.5 shrink-0" /> : <XCircle className="h-3.5 w-3.5 shrink-0" />}
              <span className="min-w-0 break-words">{result.message}</span>
              {result.latency_ms > 0 && <span className="tabular-nums text-fg-muted">({result.latency_ms} ms)</span>}
            </span>
          )}
        </>
      }
    >
      <Button variant="ghost" disabled={disabled} onClick={() => test.mutate()}>
        {test.isPending ? "Testing…" : "Test connection"}
      </Button>
    </SettingRow>
  );
}
