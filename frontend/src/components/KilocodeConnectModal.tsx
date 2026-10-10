import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ExternalLink, RefreshCw, CheckCircle2, X, AlertTriangle } from "lucide-react";
import { api, type DeviceCode } from "../lib/api";
import { Button, ErrorBanner } from "./ui";
import { useToast } from "./Toast";

// KilocodeConnectModal implements the KiloCode device-auth flow:
//   1. Backend requests a device code from api.kilo.ai.
//   2. User visits the verification URL and enters the user code.
//   3. We poll until the user authorizes and we get a token.
export function KilocodeConnectModal({ onClose }: { onClose: () => void }) {
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
      const res = await api.kilocodeDeviceStart();
      setDc(res);
      setStatus("waiting");
      timerRef.current = setInterval(() => setElapsed((e) => e + 1), 1000);
      poll(res.device_code, res.interval);
    } catch (e) {
      setError((e as Error).message);
      setStatus("error");
      toast.error("Couldn't start KiloCode authorization", (e as Error).message);
    }
  };

  const poll = (deviceCode: string, interval: number) => {
    pollRef.current = setTimeout(async () => {
      try {
        const res = await api.kilocodeDevicePoll(deviceCode);
        if (res.status === "complete") {
          setStatus("done");
          if (timerRef.current) clearInterval(timerRef.current);
          qc.invalidateQueries({ queryKey: ["accounts"] });
          toast.success("KiloCode connected", "Account added successfully.");
          setTimeout(onClose, 1400);
          return;
        }
        poll(deviceCode, res.slow_down ? interval + 5 : interval);
      } catch (e) {
        setError((e as Error).message);
        setStatus("error");
        if (timerRef.current) clearInterval(timerRef.current);
        toast.error("KiloCode authorization failed", (e as Error).message);
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
      aria-labelledby="kilocode-modal-title"
    >
      <div
        className="w-full max-w-md rounded-2xl border border-[var(--border)] bg-[var(--bg-elevated)] shadow-[var(--shadow-float)]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-[var(--border)] px-6 py-4">
          <h2 id="kilocode-modal-title" className="text-base font-semibold tracking-tight">Connect KiloCode</h2>
          <button
            onClick={onClose}
            aria-label="Close"
            className="flex h-9 w-9 items-center justify-center rounded-xl text-[var(--text-muted)] transition-colors hover:bg-ink-100 hover:text-[var(--text)] dark:hover:bg-ink-800 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/60"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="px-6 py-5">
          {status === "idle" && <KilocodeIdle onStart={start} />}
          {status === "starting" && <Spinner text="Requesting device code…" />}
          {status === "waiting" && dc && (
            <KilocodeWaiting dc={dc} elapsed={elapsed} formatElapsed={formatElapsed} />
          )}
          {status === "done" && <Done provider="KiloCode" />}
          {status === "error" && <ErrorPanel error={error} onRetry={start} onClose={onClose} />}
        </div>
      </div>
    </div>
  );
}

function KilocodeIdle({ onStart }: { onStart: () => void }) {
  return (
    <div className="space-y-5">
      <p className="text-sm text-[var(--text-muted)]">
        Connect your KiloCode account using device authorization. You'll get a code
        to enter on the KiloCode website.
      </p>
      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4">
        <h3 className="text-xs font-medium text-[var(--text-muted)] mb-3">
          How it works
        </h3>
        <ol className="space-y-2.5">
          <Step num={1} text="We request a device code from KiloCode" />
          <Step num={2} text="You visit the verification page and enter the code" />
          <Step num={3} text="Once approved, your token is encrypted and stored" />
        </ol>
      </div>
      <Button onClick={onStart} className="w-full">
        <ExternalLink className="h-4 w-4" />
        Start device authorization
      </Button>
    </div>
  );
}

function KilocodeWaiting({
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
      {/* User code display */}
      {dc.user_code && (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-4 text-center">
          <p className="text-xs text-[var(--text-muted)]">Your code</p>
          <p className="mt-1 font-mono text-2xl font-bold tracking-widest">{dc.user_code}</p>
        </div>
      )}

      {/* Progress */}
      <div className="flex items-center justify-between rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-3">
        <div className="flex items-center gap-2.5">
          <div className="h-8 w-8 rounded-full border-2 border-accent-500 border-t-transparent animate-spin" />
          <div>
            <p className="text-sm font-medium text-[var(--text)]">Waiting for approval</p>
            <p className="text-xs text-[var(--text-muted)]">Enter the code on the verification page</p>
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
          Open verification page
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

// ── Shared helpers (also used by CodebuddyConnectModal) ───────────────────────

export function Step({ num, text }: { num: number; text: string }) {
  return (
    <li className="flex items-start gap-2.5">
      <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-accent-100 text-[10px] font-bold text-accent-700 dark:bg-accent-800/40 dark:text-accent-200">
        {num}
      </span>
      <span className="text-sm text-[var(--text)]">{text}</span>
    </li>
  );
}

export function Spinner({ text }: { text: string }) {
  return (
    <div className="flex flex-col items-center gap-4 py-6">
      <div className="h-10 w-10 rounded-full border-2 border-accent-500 border-t-transparent animate-spin" />
      <p className="text-sm text-[var(--text-muted)]">{text}</p>
    </div>
  );
}

export function Done({ provider }: { provider: string }) {
  return (
    <div className="flex flex-col items-center gap-3 py-6">
      <div className="flex h-12 w-12 items-center justify-center rounded-full bg-accent-100 dark:bg-accent-800/40">
        <CheckCircle2 className="h-6 w-6 text-[color:var(--color-success)]" />
      </div>
      <p className="text-sm font-medium text-[var(--text)]">Connected!</p>
      <p className="text-xs text-[var(--text-muted)]">Refreshing {provider} accounts…</p>
    </div>
  );
}

export function ErrorPanel({
  error,
  onRetry,
  onClose,
}: {
  error: string;
  onRetry: () => void;
  onClose: () => void;
}) {
  return (
    <div className="space-y-4">
      <ErrorBanner message={error} />
      <div className="flex gap-3">
        <Button variant="ghost" className="flex-1" onClick={onRetry}>
          <RefreshCw className="h-4 w-4" />
          Retry
        </Button>
        <Button variant="ghost" className="flex-1" onClick={onClose}>
          Close
        </Button>
      </div>
    </div>
  );
}
