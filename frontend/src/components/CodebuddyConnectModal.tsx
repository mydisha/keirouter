import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ExternalLink, X, AlertTriangle } from "lucide-react";
import { api, type DeviceCode } from "../lib/api";
import { Button } from "./ui";
import { useToast } from "./Toast";
import { Step, Spinner, Done, ErrorPanel } from "./KilocodeConnectModal";

// CodebuddyConnectModal implements the CodeBuddy browser-poll auth flow:
//   1. Backend requests a login state + auth URL from copilot.tencent.com.
//   2. User visits the auth URL in a new tab.
//   3. We poll until the user authorizes and we get a token.
export function CodebuddyConnectModal({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [dc, setDc] = useState<DeviceCode | null>(null);
  const [status, setStatus] = useState<"idle" | "starting" | "waiting" | "done" | "error">("idle");
  const [error, setError] = useState("");
  const [elapsed, setElapsed] = useState(0);
  const pollRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  useEffect(() => {
    return () => {
      if (pollRef.current) clearTimeout(pollRef.current);
      if (timerRef.current) clearInterval(timerRef.current);
    };
  }, []);

  const start = async () => {
    setStatus("starting");
    setError("");

    try {
      const res = await api.codebuddyAuthStart();
      setDc(res);
      setStatus("waiting");
      timerRef.current = setInterval(() => setElapsed((e) => e + 1), 1000);
      // Auto-open the auth URL in a new tab (mirrors Kimchi flow).
      window.open(res.verification_uri_complete || res.verification_uri, "_blank");
      poll(res.device_code, res.interval);
    } catch (e) {
      setError((e as Error).message);
      setStatus("error");
      toast.error("Couldn't start CodeBuddy authorization", (e as Error).message);
    }
  };

  const poll = (deviceCode: string, interval: number) => {
    pollRef.current = setTimeout(async () => {
      try {
        const res = await api.codebuddyAuthPoll(deviceCode);
        if (res.status === "complete") {
          setStatus("done");
          if (timerRef.current) clearInterval(timerRef.current);
          qc.invalidateQueries({ queryKey: ["accounts"] });
          toast.success("CodeBuddy connected", "Account added successfully.");
          setTimeout(onClose, 1400);
          return;
        }
        poll(deviceCode, res.slow_down ? interval + 5 : interval);
      } catch (e) {
        setError((e as Error).message);
        setStatus("error");
        if (timerRef.current) clearInterval(timerRef.current);
        toast.error("CodeBuddy authorization failed", (e as Error).message);
      }
    }, Math.max(1, interval) * 1000);
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
      aria-labelledby="codebuddy-modal-title"
    >
      <div
        className="w-full max-w-md rounded-2xl border border-[var(--border)] bg-[var(--bg-elevated)] shadow-[var(--shadow-float)]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-[var(--border)] px-6 py-4">
          <h2 id="codebuddy-modal-title" className="text-base font-semibold tracking-tight">Connect CodeBuddy</h2>
          <button
            onClick={onClose}
            aria-label="Close"
            className="flex h-9 w-9 items-center justify-center rounded-xl text-[var(--text-muted)] transition-colors hover:bg-ink-100 hover:text-[var(--text)] dark:hover:bg-ink-800 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/60"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="px-6 py-5">
          {status === "idle" && <CodebuddyIdle onStart={start} />}
          {status === "starting" && <Spinner text="Requesting login state…" />}
          {status === "waiting" && dc && (
            <CodebuddyWaiting dc={dc} elapsed={elapsed} formatElapsed={formatElapsed} />
          )}
          {status === "done" && <Done provider="CodeBuddy" />}
          {status === "error" && <ErrorPanel error={error} onRetry={start} onClose={onClose} />}
        </div>
      </div>
    </div>
  );
}

function CodebuddyIdle({ onStart }: { onStart: () => void }) {
  return (
    <div className="space-y-5">
      <p className="text-sm text-[var(--text-muted)]">
        Connect your CodeBuddy (Tencent) account. A sign-in page will open
        where you authorize with your Tencent account.
      </p>
      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4">
        <h3 className="text-xs font-semibold uppercase tracking-wider text-[var(--text-muted)] mb-3">
          How it works
        </h3>
        <ol className="space-y-2.5">
          <Step num={1} text="We request a login session from CodeBuddy" />
          <Step num={2} text="A sign-in page opens for you to authorize" />
          <Step num={3} text="Once approved, your token is encrypted and stored" />
        </ol>
      </div>
      <Button onClick={onStart} className="w-full">
        <ExternalLink className="h-4 w-4" />
        Open CodeBuddy sign-in
      </Button>
    </div>
  );
}

function CodebuddyWaiting({
  dc,
  elapsed,
  formatElapsed,
}: {
  dc: DeviceCode;
  elapsed: number;
  formatElapsed: (s: number) => string;
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
          Open CodeBuddy sign-in
        </span>
      </a>

      {elapsed > 240 && (
        <div className="flex items-start gap-2 rounded-lg border border-[color:var(--color-warning)]/30 bg-[color:var(--color-warning)]/10 px-3 py-2">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-[color:var(--color-warning)]" />
          <p className="text-xs text-[color:var(--color-warning)]">
            Taking a while? Make sure the page is open in another tab.
          </p>
        </div>
      )}
    </div>
  );
}
