import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ExternalLink, Loader2 } from "lucide-react";
import { api, type DeviceCode, type OAuthProvider } from "../../lib/api";
import { useToast } from "../Toast";
import {
  ConnectDialog,
  ConnectError,
  Connected,
  DeviceWaiting,
  FormError,
  InlineCode,
  PrimaryAction,
  SecondaryAction,
  Starting,
  Steps,
  TextButton,
  TextField,
  useDevicePoll,
} from "./ConnectKit";

// redirectURIForProvider returns the OAuth callback the provider redirects to.
//
// It is always a localhost loopback: providers with desktop / installed-app
// OAuth clients (Google for gemini-cli and antigravity, etc.) only whitelist
// loopback redirects, so a public dashboard URL would fail with
// redirect_uri_mismatch. When the gateway runs next to the browser its
// loopback callback catches the redirect and notifies the dashboard; otherwise
// the user pastes the resulting URL. Fixed-port providers (Codex, xAI) mirror
// their CLI's exact http://host:port/path redirect.
function redirectURIForProvider(provider: OAuthProvider): string {
  if (provider.fixed_port && provider.callback_path) {
    const host = provider.loopback_host || "127.0.0.1";
    return `http://${host}:${provider.fixed_port}${provider.callback_path}`;
  }
  const appPort = window.location.port || (window.location.protocol === "https:" ? "443" : "80");
  return `http://localhost:${appPort}/oauth/callback`;
}

export function OAuthConnect({ provider, logo, onClose }: { provider: OAuthProvider; logo: { icon?: string; name: string }; onClose: () => void }) {
  return provider.flow === "device_code" ? (
    <OAuthDeviceConnect provider={provider} logo={logo} onClose={onClose} />
  ) : (
    <OAuthCodeConnect provider={provider} logo={logo} onClose={onClose} />
  );
}

// ── Authorization code (+PKCE) ──────────────────────────────────────────────

function OAuthCodeConnect({ provider, logo, onClose }: { provider: OAuthProvider; logo: { icon?: string; name: string }; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [phase, setPhase] = useState<"idle" | "waiting" | "manual" | "done">("idle");
  const [error, setError] = useState("");
  const [pasted, setPasted] = useState("");
  const [exchanging, setExchanging] = useState(false);
  const stateRef = useRef("");
  const popupRef = useRef<Window | null>(null);

  const finish = () => {
    // Close the popup from the opener side: its own window.close() is often
    // blocked after cross-origin redirects.
    if (popupRef.current && !popupRef.current.closed) {
      try {
        popupRef.current.close();
      } catch {
        /* ignore */
      }
    }
    popupRef.current = null;
    setPhase("done");
    qc.invalidateQueries({ queryKey: ["accounts"] });
    toast.success(`${provider.display_name} connected`, "The account is ready for routing.");
    window.setTimeout(onClose, 1100);
  };
  const finishRef = useRef(finish);
  finishRef.current = finish;

  // The callback page forwards either a raw code (exchanged here) or a
  // server-side result when the gateway already exchanged it.
  useEffect(() => {
    if (phase !== "waiting") return;
    const onMessage = async (e: MessageEvent) => {
      if (e.data?.type !== "oauth-callback") return;
      if (e.data.provider && e.data.provider !== provider.provider) return;
      if (e.data.code) {
        try {
          await api.oauthExchange(provider.provider, { code: e.data.code, state: e.data.state || stateRef.current });
          finishRef.current();
        } catch (err) {
          setError((err as Error).message);
          setPhase("idle");
        }
        return;
      }
      if (e.data.status === "success") finishRef.current();
      else {
        setError(e.data.message || "Connection failed.");
        setPhase("idle");
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [phase, provider.provider]);

  // Poll server-side completion in parallel: providers that set
  // Cross-Origin-Opener-Policy (OpenAI/Codex) sever window.opener during the
  // redirect, so the popup can never message us.
  useEffect(() => {
    if (phase !== "waiting" || !stateRef.current) return;
    let stopped = false;
    let timer = 0;
    let expired = 0;
    const tick = async () => {
      try {
        const res = await api.oauthCallbackStatus(provider.provider, stateRef.current);
        if (stopped) return;
        if (res.status === "success") return finishRef.current();
        if (res.status === "error") {
          setError(res.message || "Connection failed.");
          setPhase("idle");
          return;
        }
        // The session lives ~10 minutes; consecutive "expired" polls mean it is gone.
        if (res.status === "expired" && ++expired >= 3) {
          setError("The sign-in session expired. Start again.");
          setPhase("idle");
          return;
        }
        if (res.status === "pending") expired = 0;
      } catch {
        /* transient: the next tick retries */
      }
      if (!stopped) timer = window.setTimeout(tick, 2000);
    };
    timer = window.setTimeout(tick, 2000);
    return () => {
      stopped = true;
      window.clearTimeout(timer);
    };
  }, [phase, provider.provider]);

  const start = async () => {
    setError("");
    try {
      const res = await api.oauthAuthorize(provider.provider, redirectURIForProvider(provider));
      stateRef.current = res.state;
      popupRef.current = window.open(res.authorize_url, "_blank", "popup,width=560,height=760");
      setPhase("waiting");
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const submitManual = async () => {
    setError("");
    const input = pasted.trim();
    if (!input) return setError("Paste the full callback URL (or the code) from the other tab.");
    let code = input;
    let state = stateRef.current;
    if (input.includes("://") || input.includes("?") || input.includes("code=")) {
      try {
        const u = new URL(input.includes("://") ? input : `http://localhost/?${input.replace(/^\?/, "")}`);
        const err = u.searchParams.get("error");
        if (err) return setError(u.searchParams.get("error_description") || err);
        code = u.searchParams.get("code") || "";
        state = u.searchParams.get("state") || state;
      } catch {
        return setError("Couldn't read that URL. Paste the full address from the browser bar.");
      }
    }
    if (!code) return setError("No authorization code found. Paste the full callback URL.");
    setExchanging(true);
    try {
      await api.oauthExchange(provider.provider, { code, state });
      finish();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setExchanging(false);
    }
  };

  return (
    <ConnectDialog
      title={`Connect ${provider.display_name}`}
      logo={logo}
      onClose={onClose}
      onBack={phase === "manual" ? () => setPhase("waiting") : undefined}
    >
      {phase === "done" && <Connected name={provider.display_name} />}

      {phase === "idle" && (
        <div className="space-y-4">
          <Steps steps={[`Sign in to ${provider.display_name} in the window that opens`, "Approve access for KeiRouter", "This dialog closes on its own"]} />
          <FormError message={error} />
          <div className="flex justify-end">
            <PrimaryAction onClick={start}>
              <ExternalLink aria-hidden="true" />
              Open sign-in
            </PrimaryAction>
          </div>
        </div>
      )}

      {phase === "waiting" && (
        <div className="space-y-4">
          <div className="flex items-center gap-2.5 rounded-xl border border-line px-3.5 py-3">
            <Loader2 className="h-4 w-4 shrink-0 animate-spin text-fg-muted" aria-hidden="true" />
            <div className="min-w-0" role="status" aria-live="polite">
              <p className="text-[13px] font-medium text-fg">Waiting for sign-in</p>
              <p className="text-[12px] text-fg-muted">Finish in the {provider.display_name} window.</p>
            </div>
          </div>
          <FormError message={error} />
          <div className="flex flex-wrap items-center justify-between gap-2">
            <TextButton onClick={() => setPhase("manual")}>Page didn't load? Paste its URL</TextButton>
            <SecondaryAction onClick={start}>Reopen sign-in</SecondaryAction>
          </div>
        </div>
      )}

      {phase === "manual" && (
        <form
          className="space-y-3.5"
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            submitManual();
          }}
        >
          <Steps
            steps={[
              <>
                Approve access, even if the page ends on an unreachable <InlineCode>localhost</InlineCode> address
              </>,
              "Copy the full URL from that tab's address bar",
              "Paste it below",
            ]}
          />
          <TextField
            label="Callback URL"
            required
            value={pasted}
            onChange={(e) => {
              setPasted(e.target.value);
              setError("");
            }}
            placeholder="http://localhost:…/oauth/callback?code=…&state=…"
            className="font-mono text-[12px]"
            error={error || undefined}
          />
          <div className="flex justify-end">
            <PrimaryAction type="submit" busy={exchanging}>
              {exchanging ? "Connecting…" : "Complete connection"}
            </PrimaryAction>
          </div>
        </form>
      )}
    </ConnectDialog>
  );
}

// ── Device code ─────────────────────────────────────────────────────────────

function OAuthDeviceConnect({ provider, logo, onClose }: { provider: OAuthProvider; logo: { icon?: string; name: string }; onClose: () => void }) {
  const flow = useDevicePoll({
    providerName: provider.display_name,
    start: () => requestDeviceCode(provider.provider),
    poll: (code) => api.oauthPoll(provider.provider, code),
    onConnected: onClose,
  });
  return (
    <ConnectDialog title={`Connect ${provider.display_name}`} logo={logo} onClose={onClose}>
      {flow.status === "idle" && (
        <div className="space-y-4">
          <Steps steps={["Get a one-time code", `Enter it on ${provider.display_name}'s verification page`, "Approve access — this dialog finishes on its own"]} />
          <div className="flex justify-end">
            <PrimaryAction onClick={flow.start}>Get a device code</PrimaryAction>
          </div>
        </div>
      )}
      {(flow.status === "starting" || (flow.status === "waiting" && !flow.code)) && <Starting text="Requesting a device code…" />}
      {flow.status === "waiting" && flow.code && <DeviceWaiting code={flow.code} elapsed={flow.elapsed} />}
      {flow.status === "done" && <Connected name={provider.display_name} />}
      {flow.status === "error" && <ConnectError message={flow.error} onRetry={flow.start} onClose={onClose} />}
    </ConnectDialog>
  );
}

// requestDeviceCode handles the client-device-code variant: some providers
// (Qwen behind Alibaba Cloud WAF) block the gateway's TLS fingerprint, so the
// browser makes the upstream device-code request itself and hands the result
// back to the gateway.
async function requestDeviceCode(providerId: string): Promise<DeviceCode> {
  const res = await api.oauthDeviceCode(providerId);
  if (!res._client_device_code) return res;
  const params = new URLSearchParams({
    client_id: res._client_id!,
    scope: (res._scopes ?? []).join(" "),
    code_challenge: res._pkce_challenge!,
    code_challenge_method: res._pkce_method ?? "S256",
  });
  const upstream = await fetch(res._device_code_url!, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
    body: params.toString(),
  });
  if (!upstream.ok) throw new Error(`Device-code request failed (${upstream.status})`);
  if (!(upstream.headers.get("content-type") ?? "").includes("json")) {
    throw new Error("The provider returned an unexpected page (possibly a CAPTCHA). Try again later.");
  }
  const dc = await upstream.json();
  return api.oauthDeviceCodeSubmit(providerId, {
    nonce: res._pkce_nonce!,
    device_code: dc.device_code,
    user_code: dc.user_code ?? "",
    verification_uri: dc.verification_uri ?? "",
    verification_uri_complete: dc.verification_uri_complete ?? "",
    expires_in: dc.expires_in ?? 300,
    interval: dc.interval ?? 5,
  });
}
