import { useState } from "react";
import { CheckCircle2, XCircle } from "lucide-react";
import { api, type EndpointSettings } from "../../lib/api";
import { Badge, Button, Toggle } from "../../components/ui";
import { Code, FormField, Note, SettingRow, SettingsCard, ToggleRow, UnitInput, inputClass } from "./shared";

type TabProps = {
  local: EndpointSettings;
  update: (patch: Partial<EndpointSettings>) => void;
};

const isRoundRobin = (strategy: string) =>
  strategy === "round-robin" || strategy === "round_robin" || strategy === "smart-round-robin" || strategy === "smart_round_robin";

// ── Routing ─────────────────────────────────────────────────────────
export function RoutingTab({ local, update }: TabProps) {
  const accountsRR = isRoundRobin(local.routing_strategy);
  const chainsRR = isRoundRobin(local.combo_strategy);
  return (
    <SettingsCard
      title="Routing strategy"
      description="How requests are distributed across accounts and chains. Individual providers can override the account strategy on their Routing tab."
      footer={
        <div className="border-t border-line bg-subtle px-4 py-3 text-[12px] leading-5 text-fg-muted" aria-live="polite">
          <span className="font-medium text-fg">In effect: </span>
          {accountsRR
            ? `Distributing requests across all available accounts with ${local.sticky_limit || 3} calls per account.`
            : "Using accounts in priority order (Fill First)."}
          {chainsRR
            ? ` Chains rotate after ${local.combo_sticky_limit || 1} call${(local.combo_sticky_limit || 1) === 1 ? "" : "s"} per model.`
            : " Chains always start with their first model."}
        </div>
      }
    >
      <ToggleRow
        label="Provider group round robin"
        description="Cycle through accounts in the same provider/model group. Off uses accounts in priority order (fill first)."
      >
        <Toggle
          checked={accountsRR}
          onChange={() => update({ routing_strategy: isRoundRobin(local.routing_strategy) ? "fill-first" : "round-robin" })}
        />
      </ToggleRow>
      {accountsRR && (
        <SettingRow nested label="Provider sticky limit" description="Calls per account before switching. 1–10.">
          <UnitInput
            unit="calls"
            aria-label="Provider sticky limit"
            min={1}
            max={10}
            value={local.sticky_limit || 3}
            onChange={(e) => update({ sticky_limit: parseInt(e.target.value) || 3 })}
            className="w-28"
          />
        </SettingRow>
      )}

      <ToggleRow
        label="Chain round robin"
        description="Cycle through providers in a chain instead of always starting with the first."
      >
        <Toggle
          checked={chainsRR}
          onChange={() => update({ combo_strategy: isRoundRobin(local.combo_strategy) ? "fallback" : "round-robin" })}
        />
      </ToggleRow>
      {chainsRR && (
        <SettingRow nested label="Chain sticky limit" description="Calls per chain model before switching. 1–100.">
          <UnitInput
            unit="calls"
            aria-label="Chain sticky limit"
            min={1}
            max={100}
            value={local.combo_sticky_limit || 1}
            onChange={(e) => update({ combo_sticky_limit: parseInt(e.target.value) || 1 })}
            className="w-28"
          />
        </SettingRow>
      )}
    </SettingsCard>
  );
}

// ── Network ─────────────────────────────────────────────────────────
export function NetworkTab({ local, update }: TabProps) {
  return (
    <div className="space-y-4">
      <SettingsCard
        title="Timeouts"
        description="Upstream connection and streaming limits. Raise them for slow providers or reasoning models."
      >
        <SettingRow label="Connect timeout" description="Wait this long for the provider to start responding. Default 60 s.">
          <UnitInput
            unit="sec"
            aria-label="Connect timeout (sec)"
            min={5}
            max={300}
            value={Math.round((local.response_header_timeout_ms || 60000) / 1000)}
            onChange={(e) => {
              const sec = parseInt(e.target.value) || 60;
              update({ response_header_timeout_ms: sec * 1000 });
            }}
            placeholder="60"
          />
        </SettingRow>
        <SettingRow label="Stream stall timeout" description="Abort a stream that sends nothing for this long. Default 120 s.">
          <UnitInput
            unit="sec"
            aria-label="Stream stall timeout (sec)"
            min={10}
            max={600}
            value={Math.round((local.stream_stall_timeout_ms || 120000) / 1000)}
            onChange={(e) => {
              const sec = parseInt(e.target.value) || 120;
              update({ stream_stall_timeout_ms: sec * 1000 });
            }}
            placeholder="120"
          />
        </SettingRow>
        <SettingRow label="Request timeout" description="Upper limit for a whole request. Default 300 s (5 min).">
          <UnitInput
            unit="sec"
            aria-label="Request timeout (sec)"
            min={30}
            max={3600}
            value={Math.round((local.request_timeout_ms || 300000) / 1000)}
            onChange={(e) => {
              const sec = parseInt(e.target.value) || 300;
              update({ request_timeout_ms: sec * 1000 });
            }}
            placeholder="300"
          />
        </SettingRow>
      </SettingsCard>

      <SettingsCard title="Rate limits" description="Per-key RPM, TPM and concurrency limits come from each key's assigned plan.">
        <ToggleRow
          label="Enforce API key rate limits"
          description="Plan limits apply immediately. Blank or 0 plan values stay unlimited."
        >
          <Toggle checked={local.rate_limits_enabled !== false} onChange={(v) => update({ rate_limits_enabled: v })} />
        </ToggleRow>
      </SettingsCard>

      <ProxySettings local={local} update={update} />

      <SettingsCard title="Observability" description="What KeiRouter records for the logs view.">
        <ToggleRow label="Record request details" description="Store request details so they can be inspected in the logs view.">
          <Toggle checked={local.observability_enabled !== false} onChange={(v) => update({ observability_enabled: v })} />
        </ToggleRow>
      </SettingsCard>
    </div>
  );
}

function ProxySettings({ local, update }: TabProps) {
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
  const status = !proxyEnabled
    ? { label: "Inactive", tone: "neutral" as const }
    : hasURL
      ? { label: "Active", tone: "success" as const }
      : { label: "No URL", tone: "warning" as const };

  const detectedScheme = (() => {
    if (!local.outbound_proxy_url) return null;
    try {
      const s = new URL(local.outbound_proxy_url).protocol.replace(":", "").toLowerCase();
      if (["http", "https", "socks5"].includes(s)) return s;
    } catch {
      /* ignore */
    }
    return null;
  })();

  return (
    <SettingsCard
      title="Outbound proxy"
      description="Route provider outbound requests through an HTTP, HTTPS or SOCKS5 proxy."
      action={<Badge tone={status.tone}>{status.label}</Badge>}
    >
      <ToggleRow
        label="Use outbound proxy"
        description="Applies to all provider and OAuth requests when no per-account proxy is set."
      >
        <Toggle checked={proxyEnabled} onChange={(v) => update({ outbound_proxy_enabled: v })} />
      </ToggleRow>

      {proxyEnabled && (
        <>
          {!hasURL && (
            <div className="px-4 py-3">
              <Note tone="warn">Proxy is enabled but no URL is configured. Enter a proxy URL below.</Note>
            </div>
          )}
          <div className="grid grid-cols-1 gap-4 px-4 py-4 sm:grid-cols-2">
            <FormField
              label="Proxy URL"
              htmlFor="outbound-proxy-url"
              hint={
                <span className="flex items-center gap-2">
                  {detectedScheme && <Badge>{detectedScheme}</Badge>}
                  Supports http, https, socks5.
                </span>
              }
            >
              <input
                id="outbound-proxy-url"
                placeholder="http://127.0.0.1:7897"
                value={local.outbound_proxy_url}
                onChange={(e) => update({ outbound_proxy_url: e.target.value })}
                className={`${inputClass} font-mono`}
              />
            </FormField>
            <FormField
              label="No proxy"
              htmlFor="outbound-no-proxy"
              optional
              hint={
                <>
                  Comma-separated hostnames to bypass. Use <Code>*</Code> for all.
                </>
              }
            >
              <input
                id="outbound-no-proxy"
                placeholder="localhost,127.0.0.1"
                value={local.outbound_no_proxy}
                onChange={(e) => update({ outbound_no_proxy: e.target.value })}
                className={`${inputClass} font-mono`}
              />
            </FormField>
          </div>
          <SettingRow
            label="Test proxy"
            description={
              <>
                Sends a test request through the proxy URL above; reports latency and exit IP.
                {testResult && (
                  <span
                    className={`mt-1 flex items-center gap-1.5 ${testResult.ok ? "text-ok" : "text-bad"}`}
                    aria-live="polite"
                  >
                    {testResult.ok ? <CheckCircle2 className="h-3.5 w-3.5 shrink-0" /> : <XCircle className="h-3.5 w-3.5 shrink-0" />}
                    <span className="min-w-0 break-words">{testResult.text}</span>
                  </span>
                )}
              </>
            }
          >
            <Button variant="ghost" onClick={testProxy} disabled={testing || !hasURL}>
              {testing ? "Testing…" : "Test proxy URL"}
            </Button>
          </SettingRow>
        </>
      )}
    </SettingsCard>
  );
}
