import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { X, FileUp } from "lucide-react";
import { api } from "../lib/api";
import { Button, Input, Field, ErrorBanner } from "./ui";
import { useToast } from "./Toast";
import { Done } from "./KilocodeConnectModal";

export function CommandCodeConnectModal({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState(false);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  const submit = async () => {
    if (!token.trim()) {
      setError("Please enter a token");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await api.commandcodeImport(token.trim());
      setDone(true);
      qc.invalidateQueries({ queryKey: ["accounts"] });
      toast.success("Command Code connected", "Token imported successfully.");
      setTimeout(onClose, 1200);
    } catch (e) {
      setError((e as Error).message);
      toast.error("Command Code import failed", (e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm"
      onClick={onClose}
      role="dialog"
      aria-modal="true"
      aria-labelledby="commandcode-modal-title"
    >
      <div
        className="w-full max-w-md rounded-2xl border border-[var(--border)] bg-[var(--bg-elevated)] shadow-[var(--shadow-float)]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-[var(--border)] px-6 py-4">
          <div className="flex items-center gap-3">
            <div className="flex h-9 w-9 items-center justify-center rounded-xl border border-line bg-subtle text-fg-muted">
              <FileUp className="h-[18px] w-[18px]" />
            </div>
            <h2 id="commandcode-modal-title" className="text-base font-semibold tracking-tight">Connect Command Code</h2>
          </div>
          <button
            onClick={onClose}
            aria-label="Close"
            className="flex h-9 w-9 items-center justify-center rounded-xl text-[var(--text-muted)] transition-colors hover:bg-ink-100 hover:text-[var(--text)] dark:hover:bg-ink-800 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/60"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="px-6 py-5">
          {done ? (
            <Done provider="Command Code" />
          ) : (
            <div className="space-y-4">
              <p className="text-sm text-[var(--text-muted)]">
                Import your Command Code token from the CLI or generate an API key
                from the studio. CLI subscriptions (Go, Pro, Max, Ultra) are supported.
              </p>

              <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4">
                <h3 className="text-xs font-medium text-[var(--text-muted)] mb-3">
                  Option A: CLI token
                </h3>
                <ol className="space-y-2.5">
                  <li className="flex items-start gap-2.5">
                    <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-accent-100 text-[10px] font-bold text-accent-700 dark:bg-accent-800/40 dark:text-accent-200">1</span>
                    <span className="text-sm text-[var(--text)]">
                      Run <code className="rounded bg-[var(--bg-elevated)] px-1.5 py-0.5 font-mono text-xs">cmd login</code> in your terminal
                    </span>
                  </li>
                  <li className="flex items-start gap-2.5">
                    <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-accent-100 text-[10px] font-bold text-accent-700 dark:bg-accent-800/40 dark:text-accent-200">2</span>
                    <span className="text-sm text-[var(--text)]">
                      Copy the token from <code className="rounded bg-[var(--bg-elevated)] px-1.5 py-0.5 font-mono text-xs">~/.commandcode/auth.json</code>
                    </span>
                  </li>
                </ol>
              </div>

              <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4">
                <h3 className="text-xs font-medium text-[var(--text-muted)] mb-3">
                  Option B: API key
                </h3>
                <ol className="space-y-2.5">
                  <li className="flex items-start gap-2.5">
                    <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-accent-100 text-[10px] font-bold text-accent-700 dark:bg-accent-800/40 dark:text-accent-200">1</span>
                    <span className="text-sm text-[var(--text)]">
                      Go to{" "}
                      <a href="https://commandcode.ai/studio" target="_blank" rel="noopener noreferrer" className="text-accent-600 underline underline-offset-2">
                        commandcode.ai/studio
                      </a>
                    </span>
                  </li>
                  <li className="flex items-start gap-2.5">
                    <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-accent-100 text-[10px] font-bold text-accent-700 dark:bg-accent-800/40 dark:text-accent-200">2</span>
                    <span className="text-sm text-[var(--text)]">Generate and copy an API key</span>
                  </li>
                </ol>
              </div>

              <Field label="Token / API Key">
                <Input
                  value={token}
                  onChange={(e) => setToken(e.target.value)}
                  placeholder="Paste your Command Code token or API key…"
                  className="font-mono"
                />
              </Field>

              {error && <ErrorBanner message={error} />}

              <Button className="w-full" onClick={submit} disabled={busy || !token.trim()}>
                {busy ? "Importing…" : "Import Token"}
              </Button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
