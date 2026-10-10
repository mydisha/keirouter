import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { CheckCircle2, ExternalLink, Minimize2, RefreshCw, Scissors, XCircle } from "lucide-react";
import { ICONS } from "../../lib/icons";
import { api, type EndpointSettings, type HeadroomTestResult } from "../../lib/api";
import { Button } from "../../components/ui";
import {
  Code,
  Disclosure,
  Segmented,
  SettingRow,
  SettingsCard,
  ToggleRow,
  UnitInput,
  describedBy,
  inputClass,
  rowIds,
} from "./shared";

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
  return <div className="max-w-full overflow-x-auto p-0.5">{children}</div>;
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

  const rtkLevel = rowIds("rtk-level");
  const cavemanLevel = rowIds("caveman-level");
  const terseLevel = rowIds("terse-level");
  const ponytailLevel = rowIds("ponytail-level");

  return (
    <div className="space-y-4">
      <SettingsCard icon={Minimize2} title="Input compression" description="Saves input tokens">
        <ToggleRow label="RTK input compression"
          description="Shrinks bulky tool output before it reaches the model."
          info="Compacts diffs, greps, listings and build logs. Safe by design — it never corrupts content."
          checked={local.rtk_enabled}
          onChange={(v) => update({ rtk_enabled: v })}
        />
        {local.rtk_enabled && (
          <SettingRow
            nested
            controlId="rtk-level"
            labelable={false}
            label="Source code filter"
            description={rtkFilterHints[local.rtk_filter_level || "none"]}
          >
            <Segmented
              aria-labelledby={rtkLevel.label}
              aria-describedby={rtkLevel.desc}
              value={local.rtk_filter_level || "none"}
              onChange={(v) => update({ rtk_filter_level: v })}
              options={rtkFilterOptions}
            />
          </SettingRow>
        )}
      </SettingsCard>

      <SettingsCard icon={Scissors} title="Output compression" description="Caveman and terse can't run together">
        <ToggleRow label="Caveman mode"
          description="Terse caveman-style answers. Cuts output tokens 65–75%."
          info="Keeps all technical substance and drops filler. Turning it on turns terse mode off."
          checked={local.caveman_enabled}
          onChange={(v) => update({ caveman_enabled: v, ...(v ? { terse_enabled: false } : {}) })}
        />
        {local.caveman_enabled && (
          <SettingRow
            nested
            controlId="caveman-level"
            labelable={false}
            label="Compression level"
            description={cavemanHints[local.caveman_level]}
          >
            <LevelControl>
              <Segmented
                aria-labelledby={cavemanLevel.label}
                aria-describedby={cavemanLevel.desc}
                value={local.caveman_level}
                onChange={(v) => update({ caveman_level: v })}
                options={cavemanOptions}
              />
            </LevelControl>
          </SettingRow>
        )}

        <ToggleRow label="Terse mode"
          description="KeiRouter's own concise-output instruction."
          info="An alternative to caveman. Turning it on turns caveman mode off."
          checked={local.terse_enabled}
          onChange={(v) => update({ terse_enabled: v, ...(v ? { caveman_enabled: false } : {}) })}
        />
        {local.terse_enabled && (
          <SettingRow
            nested
            controlId="terse-level"
            labelable={false}
            label="Terse level"
            description={terseHints[local.terse_level]}
          >
            <Segmented
              aria-labelledby={terseLevel.label}
              aria-describedby={terseLevel.desc}
              value={local.terse_level}
              onChange={(v) => update({ terse_level: v })}
              options={terseOptions}
            />
          </SettingRow>
        )}

        <ToggleRow label="Ponytail"
          description="Biases the model toward minimal code changes."
          info="Adds a lazy-senior-developer system prompt. Layers on top of caveman or terse."
          checked={local.ponytail_enabled}
          onChange={(v) => saverUpdate({ ponytail_enabled: v })}
        />
        {local.ponytail_enabled && (
          <SettingRow
            nested
            controlId="ponytail-level"
            labelable={false}
            label="Ponytail level"
            description={ponytailHints[local.ponytail_level]}
            error={saverErrors.ponytail_level}
          >
            <Segmented
              aria-labelledby={ponytailLevel.label}
              aria-describedby={describedBy("ponytail-level", { error: saverErrors.ponytail_level })}
              value={local.ponytail_level}
              onChange={(v) => saverUpdate({ ponytail_level: v as "lite" | "full" | "ultra" })}
              options={ponytailOptions}
            />
          </SettingRow>
        )}
      </SettingsCard>

      <SettingsCard icon={ICONS.server} title="Headroom proxy" description="External input compression">
        <ToggleRow label="Headroom input compression"
          description="Fails open — proxy errors leave the request untouched."
          checked={local.headroom_enabled}
          onChange={(v) => saverUpdate({ headroom_enabled: v })}
        />
        {local.headroom_enabled && (
          <>
            <SettingRow nested controlId="headroom-url" label="Proxy URL" error={saverErrors.headroom_url}>
              <input
                id="headroom-url"
                type="text"
                placeholder="http://localhost:8787"
                value={local.headroom_url}
                onChange={(e) => saverUpdate({ headroom_url: e.target.value })}
                aria-invalid={!!saverErrors.headroom_url}
                aria-describedby={describedBy("headroom-url", { desc: false, error: saverErrors.headroom_url })}
                aria-required="true"
                className={`${inputClass} font-mono sm:w-80`}
              />
            </SettingRow>
            <ToggleRow nested label="Compress user messages"
              description="Also send user messages to the proxy."
              checked={local.headroom_compress_user_messages}
              onChange={(v) => saverUpdate({ headroom_compress_user_messages: v })}
            />
            <SettingRow
              nested
              controlId="headroom-timeout"
              label="Timeout"
              description={`Send uncompressed after this long. ${HEADROOM_TIMEOUT_MIN.toLocaleString()}–${HEADROOM_TIMEOUT_MAX.toLocaleString()} ms.`}
              error={saverErrors.headroom_timeout_ms}
            >
              <UnitInput
                id="headroom-timeout"
                unit="ms"
                min={1000}
                max={60000}
                value={Number.isFinite(local.headroom_timeout_ms) ? local.headroom_timeout_ms : ""}
                onChange={(e) => saverUpdate({ headroom_timeout_ms: e.target.valueAsNumber })}
                aria-invalid={!!saverErrors.headroom_timeout_ms}
                aria-describedby={describedBy("headroom-timeout", { error: saverErrors.headroom_timeout_ms })}
              />
            </SettingRow>
            <HeadroomTestConnection url={local.headroom_url} timeoutMs={local.headroom_timeout_ms} />
          </>
        )}
        <HeadroomHelp />
      </SettingsCard>
    </div>
  );
}

// HeadroomHelp keeps the advisory and install steps behind "Learn more".
// Headroom is the open-source headroom-ai proxy; KeiRouter calls its
// /v1/compress endpoint. It runs synchronously before each request, so large or
// first-seen ("cold") contexts can exceed the timeout, in which case Headroom
// fails open (request passes through uncompressed and records 0 savings).
function HeadroomHelp() {
  return (
    <div className="px-4 py-3">
      <Disclosure summary="How Headroom works and how to install it">
        <div className="space-y-3 text-[12px] leading-5 text-fg-muted">
          <p>
            <span className="font-medium text-fg">Best with a fast, local proxy.</span> Headroom runs before each request.
            Large or first-seen contexts can take several seconds to compress (longer on CPU-only machines). Past the
            timeout the request goes through uncompressed and records 0 savings. RTK, caveman, terse and Ponytail run
            instantly in-process.
          </p>
          <div>
            <p>
              Install the <Code>headroom</Code> CLI from the Python package (the npm package is a library only), then start
              it:
            </p>
            <pre
              tabIndex={0}
              aria-label="Headroom install commands"
              className="mt-2 overflow-x-auto rounded-lg border border-line bg-surface px-3 py-2 font-mono text-[12px] leading-5 text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
            >
              <code>{`pipx install "headroom-ai[all]"   # needs Python 3.10+ (or: pip install --user)
pipx ensurepath                   # add headroom to PATH, then restart your shell
headroom proxy --port 8787
headroom doctor                   # verify it's working`}</code>
            </pre>
          </div>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span>
              Then set <span className="font-medium text-fg">Proxy URL</span> to <Code>http://localhost:8787</Code>.
            </span>
            <a
              href="https://github.com/headroomlabs-ai/headroom"
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 font-medium text-link hover:underline"
            >
              Headroom installation guide
              <ExternalLink className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
              <span className="sr-only">(opens in a new tab)</span>
            </a>
          </div>
        </div>
      </Disclosure>
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
          Probes <Code>/v1/compress</Code> on the proxy URL.
          <span role="status" className="block">
            {result && (
              <span className={`mt-1 flex items-center gap-1.5 ${result.ok ? "text-ok" : "text-bad"}`}>
                {result.ok ? (
                  <CheckCircle2 className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                ) : (
                  <XCircle className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                )}
                <span className="sr-only">{result.ok ? "Connected:" : "Failed:"}</span>
                <span className="min-w-0 break-words">{result.message}</span>
                {result.latency_ms > 0 && <span className="tabular-nums text-fg-muted">({result.latency_ms} ms)</span>}
              </span>
            )}
          </span>
        </>
      }
    >
      <Button variant="ghost" disabled={disabled} onClick={() => test.mutate()}>
        <RefreshCw className={test.isPending ? "animate-spin" : undefined} aria-hidden="true" />
        {test.isPending ? "Testing…" : "Test connection"}
      </Button>
    </SettingRow>
  );
}
