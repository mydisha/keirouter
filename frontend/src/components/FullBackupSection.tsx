import { useRef, useState } from "react";
import { DatabaseBackup, Download, Upload } from "lucide-react";
import { api } from "../lib/api";
import { Button, Card, Field, Input, Modal, SectionHeader } from "./ui";
import { useToast } from "./Toast";

const MIN_PASSPHRASE = 8;

/**
 * Full router backup & restore: every table (chains, providers, accounts, API
 * keys, portal users, plans, budgets, top-ups, usage history, settings) in one
 * passphrase-protected file, for moving KeiRouter to another server.
 */
export function FullBackupSection() {
  const toast = useToast();
  const fileRef = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);

  const [backupOpen, setBackupOpen] = useState(false);
  const [backupPass, setBackupPass] = useState("");
  const [backupConfirm, setBackupConfirm] = useState("");

  const [restoreFile, setRestoreFile] = useState<File | null>(null);
  const [restorePass, setRestorePass] = useState("");
  const [restoreConfirm, setRestoreConfirm] = useState("");
  const [restoreError, setRestoreError] = useState<string | null>(null);

  const closeBackup = () => {
    setBackupOpen(false);
    setBackupPass("");
    setBackupConfirm("");
  };
  const closeRestore = () => {
    setRestoreFile(null);
    setRestorePass("");
    setRestoreConfirm("");
    setRestoreError(null);
    if (fileRef.current) fileRef.current.value = "";
  };

  const backupInvalid = backupPass.trim().length < MIN_PASSPHRASE || backupPass !== backupConfirm;

  const runBackup = async () => {
    setBusy(true);
    try {
      const blob = await api.fullBackup(backupPass.trim());
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `keirouter-backup-${new Date().toISOString().replace(/[:.]/g, "-")}.json`;
      a.click();
      URL.revokeObjectURL(url);
      toast.success("Backup downloaded", "Keep the file and its passphrase safe: the passphrase is required to restore.");
      closeBackup();
    } catch (e) {
      toast.error("Backup failed", (e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const runRestore = async () => {
    if (!restoreFile) return;
    setBusy(true);
    setRestoreError(null);
    try {
      const r = await api.fullRestore(restoreFile, restorePass.trim());
      toast.success(
        "Restore complete",
        `${r.rows.toLocaleString()} rows across ${r.tables} tables restored from ${new Date(r.backup_created_at).toLocaleString()}. Restart KeiRouter now.`,
      );
      closeRestore();
    } catch (e) {
      setRestoreError((e as Error).message || "Restore failed.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <SectionHeader
        title="Full router backup"
        description="Everything needed to move KeiRouter to another server: chains, providers and their credentials, user API keys, portal users, plans, budgets, top-ups, balances and usage history, and settings."
        icon={DatabaseBackup}
        iconTone="neutral"
      />
      <div className="space-y-3 border-t border-[var(--border)] px-6 py-4">
        <ul className="list-disc space-y-1 pl-5 text-xs leading-relaxed text-[var(--text-muted)]">
          <li>User API keys keep working after restore; spent and remaining balances are identical.</li>
          <li>Credentials are re-encrypted with your passphrase, so the new server may use a different master key.</li>
          <li>Restore <strong>replaces all data</strong> on this server in one step; a wrong passphrase or bad file changes nothing.</li>
          <li>Works across SQLite and Postgres. Restart KeiRouter after a restore.</li>
        </ul>
        <div className="flex flex-wrap gap-2">
          <Button onClick={() => setBackupOpen(true)} disabled={busy}>
            <Download className="h-4 w-4" />
            Download full backup
          </Button>
          <Button variant="ghost" onClick={() => fileRef.current?.click()} disabled={busy}>
            <Upload className="h-4 w-4" />
            Restore full backup
          </Button>
          <input
            ref={fileRef}
            type="file"
            accept=".json,application/json"
            className="hidden"
            onChange={(e) => setRestoreFile(e.target.files?.[0] ?? null)}
          />
        </div>
      </div>

      <Modal open={backupOpen} onClose={() => (busy ? null : closeBackup())} title="Download full backup" subtitle="Protect the backup with a passphrase." maxWidth="max-w-md">
        <div className="space-y-4 px-6 py-5">
          <Field label="Passphrase">
            <Input type="password" autoFocus value={backupPass} onChange={(e) => setBackupPass(e.target.value)} placeholder={`At least ${MIN_PASSPHRASE} characters`} />
          </Field>
          <Field label="Confirm passphrase">
            <Input type="password" value={backupConfirm} onChange={(e) => setBackupConfirm(e.target.value)} />
          </Field>
          {backupConfirm.length > 0 && backupPass !== backupConfirm && (
            <p className="text-xs text-[color:var(--color-danger)]">Passphrases do not match.</p>
          )}
          <p className="text-xs text-[var(--text-muted)]">
            Without this passphrase the backup cannot be restored. The file still contains API-key hashes and usage data: store it privately.
          </p>
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={closeBackup} disabled={busy}>Cancel</Button>
            <Button onClick={runBackup} disabled={busy || backupInvalid}>{busy ? "Preparing…" : "Download"}</Button>
          </div>
        </div>
      </Modal>

      <Modal open={restoreFile !== null} onClose={() => (busy ? null : closeRestore())} title="Restore full backup" subtitle={restoreFile?.name} maxWidth="max-w-md">
        <div className="space-y-4 px-6 py-5">
          <p className="rounded-xl border border-[color:var(--color-danger)]/40 bg-[color:var(--color-danger)]/5 p-3 text-xs leading-relaxed text-[var(--text)]">
            All current data on this server (chains, providers, keys, users, balances, usage, settings) will be <strong>replaced</strong> by the backup.
            The admin password also becomes the one from the backup.
          </p>
          <Field label="Backup passphrase">
            <Input type="password" autoFocus value={restorePass} onChange={(e) => setRestorePass(e.target.value)} />
          </Field>
          <Field label='Type "RESTORE" to confirm'>
            <Input value={restoreConfirm} onChange={(e) => setRestoreConfirm(e.target.value)} />
          </Field>
          {restoreError && <p className="text-xs text-[color:var(--color-danger)]">{restoreError}</p>}
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={closeRestore} disabled={busy}>Cancel</Button>
            <Button onClick={runRestore} disabled={busy || !restorePass.trim() || restoreConfirm !== "RESTORE"}>
              {busy ? "Restoring…" : "Replace all data"}
            </Button>
          </div>
        </div>
      </Modal>
    </Card>
  );
}
