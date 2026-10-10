import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { X, FileUp } from "lucide-react";
import { api } from "../lib/api";
import { Button, Input, Field, ErrorBanner } from "./ui";
import { useToast } from "./Toast";
import { Done } from "./KilocodeConnectModal";

// CursorConnectModal takes a token exported from the Cursor IDE and validates
// it via the backend /cursor/import endpoint. Cursor doesn't have a public
// OAuth flow, so users copy the token from their Cursor app settings.
export function CursorConnectModal({ onClose }: { onClose: () => void }) {
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
      setError("Please enter a token from Cursor IDE");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await api.cursorImport(token.trim());
      setDone(true);
      qc.invalidateQueries({ queryKey: ["accounts"] });
      toast.success("Cursor connected", "Token imported successfully.");
      setTimeout(onClose, 1200);
    } catch (e) {
      setError((e as Error).message);
      toast.error("Cursor import failed", (e as Error).message);
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
      aria-labelledby="cursor-modal-title"
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
            <h2 id="cursor-modal-title" className="text-base font-semibold tracking-tight">Connect Cursor</h2>
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
            <Done provider="Cursor" />
          ) : (
            <div className="space-y-4">
              <p className="text-sm text-[var(--text-muted)]">
                Paste the access token from your Cursor IDE. You can find it in
                the Cursor settings under your account section.
              </p>

              <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4">
                <h3 className="text-xs font-medium text-[var(--text-muted)] mb-3">
                  How to get the token
                </h3>
                <ol className="space-y-2.5">
                  <li className="flex items-start gap-2.5">
                    <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-accent-100 text-[10px] font-bold text-accent-700 dark:bg-accent-800/40 dark:text-accent-200">1</span>
                    <span className="text-sm text-[var(--text)]">Open Cursor IDE settings</span>
                  </li>
                  <li className="flex items-start gap-2.5">
                    <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-accent-100 text-[10px] font-bold text-accent-700 dark:bg-accent-800/40 dark:text-accent-200">2</span>
                    <span className="text-sm text-[var(--text)]">Navigate to your account section</span>
                  </li>
                  <li className="flex items-start gap-2.5">
                    <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-accent-100 text-[10px] font-bold text-accent-700 dark:bg-accent-800/40 dark:text-accent-200">3</span>
                    <span className="text-sm text-[var(--text)]">Copy the access token</span>
                  </li>
                </ol>
              </div>

              <Field label="Access Token">
                <Input
                  value={token}
                  onChange={(e) => setToken(e.target.value)}
                  placeholder="Paste your Cursor access token…"
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
