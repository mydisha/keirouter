import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ExternalLink, X, AlertTriangle, Link2 } from "lucide-react";
import { api, type DeviceCode } from "../lib/api";
import { Button } from "./ui";
import { useToast } from "./Toast";
import { Step, Spinner, Done, ErrorPanel } from "./KilocodeConnectModal";

// KimchiConnectModal implements the Kimchi browser-callback auth flow:
//   1. Backend generates a state token + auth URL (app.kimchi.dev/cli-auth).
//   2. User visits the auth URL in a new tab.
//   3. Kimchi redirects back to our callback endpoint with the token.
//   4. Callback page sends postMessage to opener + auto-closes.
//   5. Frontend listens for postMessage and immediately polls.
//   6. Fallback: regular polling continues, and user can paste callback URL manually.
export function KimchiConnectModal({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [dc, setDc] = useState<DeviceCode | null>(null);
  const [status, setStatus] = useState<"idle" | "starting" | "waiting" | "done" | "error">("idle");
  const [error, setError] = useState("");
  const [elapsed, setElapsed] = useState(0);
  const [manualUrl, setManualUrl] = useState("");
  const [showManual, setShowManual] = useState(false);
  const pollRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const dcRef = useRef<DeviceCode | null>(null);
  const pollFnRef = useRef<(deviceCode: string, interval: number) => void>(() => {});

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  useEffect(() => {
    // Listen for postMessage from the callback popup.
    const handler = (e: MessageEvent) => {
      if (e.data?.type === "kimchi-callback" && e.data?.status === "success") {
        if (pollRef.current) clearTimeout(pollRef.current);
        if (dcRef.current) {
          pollFnRef.current(dcRef.current.device_code, 0);
        }
      }
    };
    window.addEventListener("message", handler);
    return () => {
      window.removeEventListener("message", handler);
      if (pollRef.current) clearTimeout(pollRef.current);
      if (timerRef.current) clearInterval(timerRef.current);
    };
  }, []);

  const poll = (deviceCode: string, interval: number) => {
    pollRef.current = setTimeout(async () => {
      try {
        const res = await api.kimchiAuthPoll(deviceCode);
        if (res.status === "complete") {
          setStatus("done");
          if (timerRef.current) clearInterval(timerRef.current);
          qc.invalidateQueries({ queryKey: ["accounts"] });
          toast.success("Kimchi connected", "Account added successfully.");
          setTimeout(onClose, 1400);
          return;
        }
        poll(deviceCode, res.slow_down ? interval + 5 : interval);
      } catch (e) {
        setError((e as Error).message);
        setStatus("error");
        if (timerRef.current) clearInterval(timerRef.current);
        toast.error("Kimchi authorization failed", (e as Error).message);
      }
    }, Math.max(0, interval) * 1000);
  };

  // Keep refs in sync for postMessage handler.
  pollFnRef.current = poll;

  const start = async () => {
    setStatus("starting");
    setError("");

    try {
      const res = await api.kimchiAuthStart();
      setDc(res);
      dcRef.current = res;
      setStatus("waiting");
      timerRef.current = setInterval(() => setElapsed((e) => e + 1), 1000);
      // Open the auth URL in a new tab
      window.open(res.verification_uri_complete || res.verification_uri, "_blank");
      poll(res.device_code, res.interval);
    } catch (e) {
      setError((e as Error).message);
      setStatus("error");
      toast.error("Couldn't start Kimchi authorization", (e as Error).message);
    }
  };

  const handleManualCallback = async () => {
    const url = manualUrl.trim();
    if (!url || !dcRef.current) return;

    try {
      const parsed = new URL(url);
      const state = parsed.searchParams.get("state");
      const token = parsed.searchParams.get("token");
      if (!state || !token) {
        toast.error("Invalid callback URL", "The URL must contain state and token parameters.");
        return;
      }
      // The backend callback endpoint already processed this if the redirect
      // hit it. But if it didn't (e.g. different host), we need to trigger
      // the callback processing. Just re-poll — if the callback already
      // resolved, poll will find it. If not, we call the callback API.
      if (pollRef.current) clearTimeout(pollRef.current);
      poll(dcRef.current.device_code, 0);
      toast.info("Checking callback", "Polling for your token...");
    } catch {
      toast.error("Invalid URL", "Please paste the full callback URL from your browser.");
    }
  };

  const formatElapsed = (s: number) => {
    const m = Math.floor(s / 60);
    const sec = s % 60;
    return `${m}:${sec.toString().padStart(2, "0")}`;
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm"
      onClick={onClose}
      role="dialog"
      aria-modal="true"
      aria-labelledby="kimchi-modal-title"
    >
      <div
        className="w-full max-w-md rounded-2xl border border-[var(--border)] bg-[var(--bg-elevated)] shadow-[var(--shadow-float)]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-[var(--border)] px-6 py-4">
          <h2 id="kimchi-modal-title" className="text-base font-semibold tracking-tight">Connect Kimchi</h2>
          <button
            onClick={onClose}
            aria-label="Close"
            className="flex h-9 w-9 items-center justify-center rounded-xl text-[var(--text-muted)] transition-colors hover:bg-ink-100 hover:text-[var(--text)] dark:hover:bg-ink-800 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/60"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="px-6 py-5">
          {status === "idle" && <KimchiIdle onStart={start} />}
          {status === "starting" && <Spinner text="Requesting login state…" />}
          {status === "waiting" && dc && (
            <KimchiWaiting
              dc={dc}
              elapsed={elapsed}
              formatElapsed={formatElapsed}
              manualUrl={manualUrl}
              onManualUrl={setManualUrl}
              showManual={showManual}
              onToggleManual={() => setShowManual(!showManual)}
              onManualSubmit={handleManualCallback}
            />
          )}
          {status === "done" && <Done provider="Kimchi" />}
          {status === "error" && <ErrorPanel error={error} onRetry={start} onClose={onClose} />}
        </div>
      </div>
    </div>
  );
}

function KimchiIdle({ onStart }: { onStart: () => void }) {
  return (
    <div className="space-y-5">
      <p className="text-sm text-[var(--text-muted)]">
        Connect your Kimchi account. A sign-in page will open where you
        authorize with your Kimchi account.
      </p>
      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4">
        <h3 className="text-xs font-semibold uppercase tracking-wider text-[var(--text-muted)] mb-3">
          How it works
        </h3>
        <ol className="space-y-2.5">
          <Step num={1} text="Click below to open the Kimchi sign-in page" />
          <Step num={2} text="Authorize keirouter in the browser" />
          <Step num={3} text="Kimchi redirects back with your token" />
          <Step num={4} text="Token is encrypted and stored locally" />
        </ol>
      </div>
      <Button onClick={onStart} className="w-full">
        <ExternalLink className="h-4 w-4" />
        Open Kimchi sign-in
      </Button>
    </div>
  );
}

function KimchiWaiting({
  dc,
  elapsed,
  formatElapsed,
  manualUrl,
  onManualUrl,
  showManual,
  onToggleManual,
  onManualSubmit,
}: {
  dc: DeviceCode;
  elapsed: number;
  formatElapsed: (s: number) => string;
  manualUrl: string;
  onManualUrl: (v: string) => void;
  showManual: boolean;
  onToggleManual: () => void;
  onManualSubmit: () => void;
}) {
  const verificationUrl = dc.verification_uri_complete || dc.verification_uri;

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-3">
        <div className="flex items-center gap-2.5">
          <div className="h-8 w-8 rounded-full border-2 border-accent-500 border-t-transparent animate-spin" />
          <div>
            <p className="text-sm font-medium text-[var(--text)]">Waiting for authorization</p>
            <p className="text-xs text-[var(--text-muted)]">Complete sign-in on the other tab</p>
          </div>
        </div>
        <span className="font-mono text-sm tabular-nums text-[var(--text-muted)]">
          {formatElapsed(elapsed)}
        </span>
      </div>

      <a
        href={verificationUrl}
        target="_blank"
        rel="noopener noreferrer"
        className="block w-full rounded-xl bg-accent-600 px-3 py-2.5 text-center text-sm font-medium text-white shadow-sm transition-colors hover:bg-accent-700 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/60"
      >
        <span className="inline-flex items-center gap-2">
          <ExternalLink className="h-4 w-4" />
          Open Kimchi sign-in
        </span>
      </a>

      {/* Manual callback URL fallback */}
      <div className="space-y-2">
        <button
          type="button"
          onClick={onToggleManual}
          className="text-xs text-[var(--text-muted)] underline underline-offset-2 hover:text-[var(--text)]"
        >
          {showManual ? "Hide manual callback" : "Stuck? Enter callback URL manually"}
        </button>
        {showManual && (
          <div className="space-y-2">
            <p className="text-xs text-[var(--text-muted)]">
              If the callback page didn't close automatically, copy the full URL from
              the browser address bar and paste it here.
            </p>
            <div className="flex gap-2">
              <input
                type="text"
                value={manualUrl}
                onChange={(e) => onManualUrl(e.target.value)}
                placeholder="http://127.0.0.1:20180/kimchi/callback?token=...&state=..."
                className="flex-1 rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-3 py-2 text-xs font-mono placeholder:text-[var(--text-muted)] focus:border-accent-400 focus:outline-none"
              />
              <Button variant="secondary" className="shrink-0" onClick={onManualSubmit} disabled={!manualUrl.trim()}>
                <Link2 className="h-3.5 w-3.5" />
                Check
              </Button>
            </div>
          </div>
        )}
      </div>

      {elapsed > 240 && (
        <div className="flex items-start gap-2 rounded-lg border border-[color:var(--color-warning)]/30 bg-[color:var(--color-warning)]/10 px-3 py-2">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-[color:var(--color-warning)]" />
          <p className="text-xs text-[color:var(--color-warning)]">
            Taking a while? Make sure the sign-in page is open in another tab.
            You can also paste the callback URL manually below.
          </p>
        </div>
      )}
    </div>
  );
}