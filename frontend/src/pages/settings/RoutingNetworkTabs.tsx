import { useEffect, useState } from "react";
import { CheckCircle2, RefreshCw, XCircle } from "lucide-react";
import { ICONS } from "../../lib/icons";
import { api, type EndpointSettings } from "../../lib/api";
import { Badge, Button } from "../../components/ui";
import { Code, FormField, Note, NumberUnitInput, Segmented, SettingRow, SettingsCard, ToggleRow, inputClass, rowIds } from "./shared";

type TabProps = {
  local: EndpointSettings;
  update: (patch: Partial<EndpointSettings>) => void;
};

type AccountStrategy = "fill-first" | "round-robin" | "smart-round-robin";

const ACCOUNT_STRATEGIES: { value: AccountStrategy; label: string }[] = [
  { value: "fill-first", label: "Fill first" },
  { value: "round-robin", label: "Round robin" },
  { value: "smart-round-robin", label: "Smart round robin" },
];

const ACCOUNT_STRATEGY_HINTS: Record<AccountStrategy, string> = {
  "fill-first": "Use accounts in priority order. The next one takes over only when the first is unavailable.",
  "round-robin": "Rotate accounts every few calls, regardless of conversation.",
  "smart-round-robin": "Rotate new conversations across accounts, but keep each conversation on one account so prompt caching keeps working.",
};

// accountStrategy maps legacy spellings (round_robin, fallback, …) onto the
// three values the server stores.
const accountStrategy = (raw: string): AccountStrategy => {
  const s = (raw || "").trim().toLowerCase().replace(/_/g, "-");
  if (s === "round-robin" || s === "roundrobin") return "round-robin";
  if (s === "smart-round-robin" || s === "smartroundrobin" || s === "smart") return "smart-round-robin";
  return "fill-first";
};

const isRoundRobin = (strategy: string) => {
  const s = (strategy || "").trim().toLowerCase().replace(/_/g, "-");
  return s === "round-robin" || s === "roundrobin";
};

// ── Routing ─────────────────────────────────────────────────────────
export function RoutingTab({ local, update }: TabProps) {
  const mode = accountStrategy(local.routing_strategy);
  const strategyIds = rowIds("account-strategy");
  const chainsRR = isRoundRobin(local.combo_strategy);
  return (
    <SettingsCard icon={ICONS.route} title="Routing strategy" description="Providers can override this on their Routing tab">
      <SettingRow
        stack
        labelable={false}
        controlId="account-strategy"
        label="Accounts in a provider group"
        description={ACCOUNT_STRATEGY_HINTS[mode]}
      >
        <Segmented
          aria-labelledby={strategyIds.label}
          aria-describedby={strategyIds.desc}
          value={mode}
          onChange={(v) => update({ routing_strategy: v })}
          options={ACCOUNT_STRATEGIES}
        />
      </SettingRow>
      {mode !== "fill-first" && (
        <SettingRow
          nested
          controlId="provider-sticky"
          label="Provider sticky limit"
          description={
            mode === "smart-round-robin"
              ? "Calls per account before a new conversation moves to the next one. 1–100."
              : "Calls per account before switching. 1–100."
          }
        >
          <NumberUnitInput
            id="provider-sticky"
            unit="calls"
            aria-describedby={rowIds("provider-sticky").desc}
            min={1}
            max={100}
            value={local.sticky_limit || 3}
            onCommit={(n) => update({ sticky_limit: n })}
            className="w-28"
          />
        </SettingRow>
      )}

      <ToggleRow label="Chain round robin"
        description="Rotate the models of priority chains. Cost and latency chains keep their order, and a chain's fallback model always stays last."
        checked={chainsRR}
        onChange={(v) => update({ combo_strategy: v ? "round-robin" : "fallback" })}
      />
      {chainsRR && (
        <SettingRow nested controlId="chain-sticky" label="Chain sticky limit" description="Calls per chain model before switching. 1–100.">
          <NumberUnitInput
            id="chain-sticky"
            unit="calls"
            aria-describedby={rowIds("chain-sticky").desc}
            min={1}
            max={100}
            value={local.combo_sticky_limit || 1}
            onCommit={(n) => update({ combo_sticky_limit: n })}
            className="w-28"
          />
        </SettingRow>
      )}
    </SettingsCard>
  );
}

// ── Network ─────────────────────────────────────────────────────────
const PROXY_SCHEMES = ["http", "https", "socks5", "socks5h"];

// proxyURLError mirrors the server's syntax check so a half-typed URL is
// flagged inline instead of being saved and breaking live traffic.
const proxyURLError = (raw: string): string | undefined => {
  if (!raw) return undefined;
  if (!raw.includes("://")) return "Add a scheme, e.g. http://proxy.example.com:8080 or socks5://…";
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return "Use scheme://host:port, e.g. http://proxy.example.com:8080";
  }
  const scheme = u.protocol.replace(":", "").toLowerCase();
  if (!PROXY_SCHEMES.includes(scheme)) return `Unsupported scheme "${scheme}". Use http, https, socks5 or socks5h.`;
  if (!u.hostname) return "The proxy URL needs a host.";
  return undefined;
};

// normalizeNoProxy trims entries and drops empty ones: " a, ,b " → "a,b".
const normalizeNoProxy = (raw: string) =>
  raw
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean)
    .join(",");

export function NetworkTab({ local, update }: TabProps) {
  return (
    <div className="space-y-4">
      <SettingsCard icon={ICONS.latency} title="Timeouts" description="Raise these for slow or reasoning models">
        <SettingRow controlId="connect-timeout" label="First response timeout"
          description="How long a streaming provider may take to start responding. TCP connect and TLS are capped at 10 s separately. Default 60 s.">
          <NumberUnitInput
            id="connect-timeout"
            unit="sec"
            aria-describedby={rowIds("connect-timeout").desc}
            min={5}
            max={300}
            value={Math.round((local.response_header_timeout_ms || 60000) / 1000)}
            onCommit={(sec) => update({ response_header_timeout_ms: sec * 1000 })}
            placeholder="60"
          />
        </SettingRow>
        <SettingRow controlId="stall-timeout" label="Stream stall timeout" description="Abort a stream that sends nothing for this long. Default 300 s.">
          <NumberUnitInput
            id="stall-timeout"
            unit="sec"
            aria-describedby={rowIds("stall-timeout").desc}
            min={5}
            max={600}
            value={Math.round((local.stream_stall_timeout_ms || 300000) / 1000)}
            onCommit={(sec) => update({ stream_stall_timeout_ms: sec * 1000 })}
            placeholder="300"
          />
        </SettingRow>
        <SettingRow controlId="request-timeout" label="Request timeout"
          description="Upper limit for a non-streaming request, fallbacks included. Streams are bounded by the stall timeout. Default 300 s.">
          <NumberUnitInput
            id="request-timeout"
            unit="sec"
            aria-describedby={rowIds("request-timeout").desc}
            min={30}
            max={3600}
            value={Math.round((local.request_timeout_ms || 300000) / 1000)}
            onCommit={(sec) => update({ request_timeout_ms: sec * 1000 })}
            placeholder="300"
          />
        </SettingRow>
      </SettingsCard>

      <ProxySettings local={local} update={update} />

      <SettingsCard icon={ICONS.quota} title="Limits and logging">
        <ToggleRow label="Enforce API key rate limits"
          description="Apply each key's plan RPM, TPM and concurrency limits."
          info="Changes apply immediately. Blank or 0 plan values stay unlimited."
          checked={local.rate_limits_enabled !== false}
          onChange={(v) => update({ rate_limits_enabled: v })}
        />
        {local.rate_limits_enabled !== false && (
          <>
            <ToggleRow nested label="Correct TPM with actual usage"
              description="Charge the tokens a response really used instead of the up-front estimate."
              info="TPM reserves prompt + max_tokens before the call. With this on, the reservation is corrected once the response finishes, so a large max_tokens no longer burns the budget."
              checked={local.tpm_reconcile_enabled !== false}
              onChange={(v) => update({ tpm_reconcile_enabled: v })}
            />
            <SettingRow nested controlId="tpm-output-floor" label="Output reserve without max_tokens"
              description="Tokens reserved for the answer when a request sets no max_tokens. 0 reserves none.">
              <NumberUnitInput
                id="tpm-output-floor"
                unit="tok"
                aria-describedby={rowIds("tpm-output-floor").desc}
                min={0}
                max={1000000}
                value={local.tpm_output_floor ?? 0}
                onCommit={(n) => update({ tpm_output_floor: n })}
                placeholder="0"
              />
            </SettingRow>
            <SettingRow nested controlId="tpm-output-cap" label="Max output reserve"
              description="Caps the max_tokens part of the reservation, e.g. 8192 for clients that always ask for 64k. 0 means no cap."
              info="Best used with usage correction on, so the real output is still charged after the response.">
              <NumberUnitInput
                id="tpm-output-cap"
                unit="tok"
                aria-describedby={rowIds("tpm-output-cap").desc}
                min={0}
                max={10000000}
                value={local.tpm_output_reserve_cap ?? 0}
                onCommit={(n) => update({ tpm_output_reserve_cap: n })}
                placeholder="0"
              />
            </SettingRow>
            <SettingRow nested controlId="concurrency-retry" label="Concurrency retry hint"
              description="Retry-After sent when a key is at its concurrency limit. 1–60 s.">
              <NumberUnitInput
                id="concurrency-retry"
                unit="sec"
                aria-describedby={rowIds("concurrency-retry").desc}
                min={1}
                max={60}
                value={Math.round((local.concurrency_retry_after_ms ?? 1000) / 1000)}
                onCommit={(sec) => update({ concurrency_retry_after_ms: sec * 1000 })}
                placeholder="1"
              />
            </SettingRow>
          </>
        )}
        <SettingRow controlId="auth-failures" label="Invalid API key limit"
          description="After this many invalid keys from one IP, refuse that IP's invalid keys for the rest of the minute. 0 turns it off."
          info="Applies even when key rate limits are off. Valid keys are never refused, and local (loopback) clients are not counted.">
          <NumberUnitInput
            id="auth-failures"
            unit="/min"
            aria-describedby={rowIds("auth-failures").desc}
            min={0}
            max={10000}
            value={local.auth_failures_per_minute ?? 20}
            onCommit={(n) => update({ auth_failures_per_minute: n })}
            placeholder="20"
          />
        </SettingRow>
        <SettingRow controlId="login-attempts" label="Dashboard login attempts"
          description="Login attempts allowed per IP each minute. 0 turns it off.">
          <NumberUnitInput
            id="login-attempts"
            unit="/min"
            aria-describedby={rowIds("login-attempts").desc}
            min={0}
            max={1000}
            value={local.login_attempts_per_minute ?? 5}
            onCommit={(n) => update({ login_attempts_per_minute: n })}
            placeholder="5"
          />
        </SettingRow>
        <ToggleRow label="Record request details"
          description="Store request details for the logs view."
          checked={local.observability_enabled !== false}
          onChange={(v) => update({ observability_enabled: v })}
        />
      </SettingsCard>
    </div>
  );
}

function ProxySettings({ local, update }: TabProps) {
  const [testResult, setTestResult] = useState<{ ok: boolean; text: string } | null>(null);
  const [testing, setTesting] = useState(false);
  // The URL and bypass list are edited as drafts and saved on blur/Enter: the
  // proxy applies to live traffic the moment it is saved, so a half-typed URL
  // must never reach the server.
  const [urlDraft, setUrlDraft] = useState(local.outbound_proxy_url);
  const [noProxyDraft, setNoProxyDraft] = useState(local.outbound_no_proxy);
  const [urlError, setUrlError] = useState<string | undefined>();
  useEffect(() => setUrlDraft(local.outbound_proxy_url), [local.outbound_proxy_url]);
  useEffect(() => setNoProxyDraft(local.outbound_no_proxy), [local.outbound_no_proxy]);

  const commitURL = () => {
    const next = urlDraft.trim();
    const err = proxyURLError(next);
    setUrlError(err);
    if (err || next === local.outbound_proxy_url) return;
    setTestResult(null);
    update({ outbound_proxy_url: next });
  };
  const commitNoProxy = () => {
    const next = normalizeNoProxy(noProxyDraft);
    setNoProxyDraft(next);
    if (next !== local.outbound_no_proxy) update({ outbound_no_proxy: next });
  };
  const blurOnEnter = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") e.currentTarget.blur();
  };

  // Test what is typed, so a proxy can be checked before it is saved.
  const testURL = urlDraft.trim();
  const testProxy = async () => {
    const err = proxyURLError(testURL);
    if (!testURL || err) {
      setUrlError(err);
      return;
    }
    setTesting(true);
    setTestResult(null);
    try {
      const res = await api.testProxy(testURL);
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
  const status = !proxyEnabled
    ? { label: "Disabled", tone: "neutral" as const }
    : hasURL
      ? { label: "Active", tone: "success" as const }
      : { label: "Not configured", tone: "warning" as const };

  const detectedScheme = (() => {
    if (!testURL) return null;
    try {
      const s = new URL(testURL).protocol.replace(":", "").toLowerCase();
      if (PROXY_SCHEMES.includes(s)) return s;
    } catch {
      /* ignore */
    }
    return null;
  })();

  return (
    <SettingsCard
      icon={ICONS.network}
      title="Outbound proxy"
      description="HTTP, HTTPS or SOCKS5 proxy for provider requests"
      action={<Badge tone={status.tone}>{status.label}</Badge>}
    >
      <ToggleRow label="Use outbound proxy"
        description="Applies to provider requests, OAuth sign-ins and token refreshes for accounts without their own proxy pool."
        checked={proxyEnabled}
        onChange={(v) => update({ outbound_proxy_enabled: v })}
      />

      {proxyEnabled && (
        <>
          {!hasURL && (
            <div className="px-4 py-3">
              <Note tone="warn">The proxy is on but has no URL, so requests go out directly. Enter one below.</Note>
            </div>
          )}
          <div className="grid grid-cols-1 gap-4 px-4 py-4 sm:grid-cols-2">
            <FormField
              label="Proxy URL"
              htmlFor="outbound-proxy-url"
              error={urlError}
              hint={
                <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
                  {detectedScheme && <Badge>{detectedScheme}</Badge>}
                  http, https, socks5 or socks5h. Saved when you leave the field.
                </span>
              }
            >
              <input
                id="outbound-proxy-url"
                placeholder="http://proxy.example.com:8080"
                value={urlDraft}
                onChange={(e) => {
                  setUrlDraft(e.target.value);
                  if (urlError) setUrlError(undefined);
                }}
                onBlur={commitURL}
                onKeyDown={blurOnEnter}
                spellCheck={false}
                autoComplete="off"
                className={`${inputClass} font-mono`}
              />
            </FormField>
            <FormField
              label="No proxy"
              htmlFor="outbound-no-proxy"
              optional
              hint={
                <>
                  Comma-separated. <Code>example.com</Code> includes subdomains, <Code>.example.com</Code> only
                  subdomains, plus CIDRs like <Code>10.0.0.0/8</Code>. <Code>*</Code> bypasses all.
                </>
              }
            >
              <input
                id="outbound-no-proxy"
                placeholder="localhost,127.0.0.1"
                value={noProxyDraft}
                onChange={(e) => setNoProxyDraft(e.target.value)}
                onBlur={commitNoProxy}
                onKeyDown={blurOnEnter}
                spellCheck={false}
                autoComplete="off"
                className={`${inputClass} font-mono`}
              />
            </FormField>
          </div>
          <SettingRow
            label="Test proxy"
            description={
              <>
                Reports latency and exit IP for the URL above.
                <span role="status" className="block">
                  {testResult && (
                    <span className={`mt-1 flex items-center gap-1.5 ${testResult.ok ? "text-ok" : "text-bad"}`}>
                      {testResult.ok ? (
                        <CheckCircle2 className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                      ) : (
                        <XCircle className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                      )}
                      <span className="min-w-0 [overflow-wrap:anywhere]">{testResult.text}</span>
                    </span>
                  )}
                </span>
              </>
            }
          >
            <Button variant="ghost" onClick={testProxy} disabled={testing || !testURL}>
              <RefreshCw className={testing ? "animate-spin" : undefined} aria-hidden="true" />
              {testing ? "Testing…" : "Test proxy"}
            </Button>
          </SettingRow>
        </>
      )}
    </SettingsCard>
  );
}
