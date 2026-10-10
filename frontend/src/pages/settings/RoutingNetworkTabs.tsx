import { useState } from "react";
import { CheckCircle2, RefreshCw, XCircle } from "lucide-react";
import { ICONS } from "../../lib/icons";
import { api, type EndpointSettings } from "../../lib/api";
import { Badge, Button } from "../../components/ui";
import { Code, FormField, Note, SettingRow, SettingsCard, ToggleRow, UnitInput, inputClass, rowIds } from "./shared";

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
    <SettingsCard icon={ICONS.route} title="Routing strategy" description="Providers can override this on their Routing tab">
      <ToggleRow label="Provider group round robin"
        description="Rotate accounts in the same provider/model group. Off: use them in priority order (fill first)."
        checked={accountsRR}
        onChange={() => update({ routing_strategy: isRoundRobin(local.routing_strategy) ? "fill-first" : "round-robin" })}
      />
      {accountsRR && (
        <SettingRow nested controlId="provider-sticky" label="Provider sticky limit" description="Calls per account before switching. 1–10.">
          <UnitInput
            id="provider-sticky"
            unit="calls"
            aria-describedby={rowIds("provider-sticky").desc}
            min={1}
            max={10}
            value={local.sticky_limit || 3}
            onChange={(e) => update({ sticky_limit: parseInt(e.target.value) || 3 })}
            className="w-28"
          />
        </SettingRow>
      )}

      <ToggleRow label="Chain round robin"
        description="Rotate providers in a chain. Off: always start with the first."
        checked={chainsRR}
        onChange={() => update({ combo_strategy: isRoundRobin(local.combo_strategy) ? "fallback" : "round-robin" })}
      />
      {chainsRR && (
        <SettingRow nested controlId="chain-sticky" label="Chain sticky limit" description="Calls per chain model before switching. 1–100.">
          <UnitInput
            id="chain-sticky"
            unit="calls"
            aria-describedby={rowIds("chain-sticky").desc}
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
      <SettingsCard icon={ICONS.latency} title="Timeouts" description="Raise these for slow or reasoning models">
        <SettingRow controlId="connect-timeout" label="Connect timeout" description="Wait for a streaming provider to send its first bytes. Non-streaming calls use the request timeout. Default 60 s.">
          <UnitInput
            id="connect-timeout"
            unit="sec"
            aria-describedby={rowIds("connect-timeout").desc}
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
        <SettingRow controlId="stall-timeout" label="Stream stall timeout" description="Abort a stream that sends nothing for this long. Default 120 s.">
          <UnitInput
            id="stall-timeout"
            unit="sec"
            aria-describedby={rowIds("stall-timeout").desc}
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
        <SettingRow controlId="request-timeout" label="Request timeout" description="Upper limit for a whole request. Default 300 s.">
          <UnitInput
            id="request-timeout"
            unit="sec"
            aria-describedby={rowIds("request-timeout").desc}
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

      <ProxySettings local={local} update={update} />

      <SettingsCard icon={ICONS.quota} title="Limits and logging">
        <ToggleRow label="Enforce API key rate limits"
          description="Apply each key's plan RPM, TPM and concurrency limits."
          info="Changes apply immediately. Blank or 0 plan values stay unlimited."
          checked={local.rate_limits_enabled !== false}
          onChange={(v) => update({ rate_limits_enabled: v })}
        />
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
    ? { label: "Disabled", tone: "neutral" as const }
    : hasURL
      ? { label: "Active", tone: "success" as const }
      : { label: "Not configured", tone: "warning" as const };

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
      icon={ICONS.network}
      title="Outbound proxy"
      description="HTTP, HTTPS or SOCKS5 proxy for provider requests"
      action={<Badge tone={status.tone}>{status.label}</Badge>}
    >
      <ToggleRow label="Use outbound proxy"
        description="Applies to provider and OAuth requests without a per-account proxy."
        checked={proxyEnabled}
        onChange={(v) => update({ outbound_proxy_enabled: v })}
      />

      {proxyEnabled && (
        <>
          {!hasURL && (
            <div className="px-4 py-3">
              <Note tone="warn">The proxy is on but has no URL. Enter one below.</Note>
            </div>
          )}
          <div className="grid grid-cols-1 gap-4 px-4 py-4 sm:grid-cols-2">
            <FormField
              label="Proxy URL"
              htmlFor="outbound-proxy-url"
              hint={
                <span className="flex items-center gap-2">
                  {detectedScheme && <Badge>{detectedScheme}</Badge>}
                  http, https or socks5
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
                  Comma-separated hosts to bypass. <Code>*</Code> bypasses all.
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
                Reports latency and exit IP.
                <span role="status" className="block">
                  {testResult && (
                    <span className={`mt-1 flex items-center gap-1.5 ${testResult.ok ? "text-ok" : "text-bad"}`}>
                      {testResult.ok ? (
                        <CheckCircle2 className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                      ) : (
                        <XCircle className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                      )}
                      <span className="min-w-0 break-words">{testResult.text}</span>
                    </span>
                  )}
                </span>
              </>
            }
          >
            <Button variant="ghost" onClick={testProxy} disabled={testing || !hasURL}>
              <RefreshCw className={testing ? "animate-spin" : undefined} aria-hidden="true" />
              {testing ? "Testing…" : "Test proxy"}
            </Button>
          </SettingRow>
        </>
      )}
    </SettingsCard>
  );
}
