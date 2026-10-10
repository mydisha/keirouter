import { useRef, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArchiveRestore, DatabaseBackup, Download, Eye, EyeOff, Import, KeyRound, ShieldCheck, Upload } from "lucide-react";
import { cn } from "@/lib/utils";
import {
  api,
  type ForeignImportResult,
  type N9routerAnalyzeResult,
  type N9routerImportOptions,
} from "../../lib/api";
import { useToast } from "../../components/Toast";
import { useConfirm } from "../../components/ui/confirm-dialog";
import { Badge, Button, ErrorBanner, Input, Modal } from "../../components/ui";
import { Code, Note, SettingRow, SettingsCard } from "./shared";

// ── Import / Export tab ─────────────────────────────────────────────
export function ImportExportTab() {
  // One busy flag for every action that touches the database, so a restore
  // can't start while a backup is downloading (and vice versa).
  const [dbLoading, setDbLoading] = useState(false);
  return (
    <div className="space-y-4">
      <DatabaseSettings loading={dbLoading} setLoading={setDbLoading} />
      <DangerZone loading={dbLoading} setLoading={setDbLoading} />
      <ForeignImportSettings />
    </div>
  );
}

// ── Small pieces ────────────────────────────────────────────────────
function PassphraseInput({
  id,
  value,
  onChange,
  show,
  onToggleShow,
  placeholder,
  autoFocus,
  ariaInvalid,
  ariaDescribedBy,
}: {
  id: string;
  value: string;
  onChange: (v: string) => void;
  show: boolean;
  onToggleShow: () => void;
  placeholder?: string;
  autoFocus?: boolean;
  ariaInvalid?: boolean;
  ariaDescribedBy?: string;
}) {
  return (
    <div className="relative">
      <Input
        id={id}
        type={show ? "text" : "password"}
        value={value}
        autoFocus={autoFocus}
        autoComplete="new-password"
        spellCheck={false}
        placeholder={placeholder}
        aria-invalid={ariaInvalid}
        aria-describedby={ariaDescribedBy}
        aria-required="true"
        onChange={(e) => onChange(e.target.value)}
        className="h-9 pr-11"
      />
      <button
        type="button"
        onClick={onToggleShow}
        aria-label={show ? "Hide passphrase" : "Show passphrase"}
        aria-pressed={show}
        className="absolute inset-y-0 right-0 flex w-10 items-center justify-center rounded-r-lg text-fg-faint transition-colors hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
      >
        {show ? (
          <EyeOff className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
        ) : (
          <Eye className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
        )}
      </button>
    </div>
  );
}

function strengthOf(pass: string): { label: string; tone: "muted" | "weak" | "ok" | "strong"; pct: number } {
  if (!pass) return { label: "No passphrase — local backup", tone: "muted", pct: 0 };
  let score = 0;
  if (pass.length >= 8) score++;
  if (pass.length >= 14) score++;
  if (/[A-Z]/.test(pass) && /[a-z]/.test(pass)) score++;
  if (/\d/.test(pass)) score++;
  if (/[^A-Za-z0-9]/.test(pass)) score++;
  if (score <= 2) return { label: "Weak — short or simple", tone: "weak", pct: 33 };
  if (score <= 3) return { label: "OK — could be stronger", tone: "ok", pct: 66 };
  return { label: "Strong", tone: "strong", pct: 100 };
}

// StatStrip is the KPI-strip pattern: hairline grid of label/value cells.
function StatStrip({ items }: { items: { label: string; value: number; muted?: boolean }[] }) {
  return (
    <div className="overflow-hidden rounded-lg border border-line">
      <div className="grid grid-cols-2 gap-px bg-line sm:grid-cols-4">
        {items.map((s) => (
          <div key={s.label} className="bg-surface px-3 py-2">
            <p className="text-[12px] text-fg-muted">{s.label}</p>
            <p className={cn("mt-0.5 text-[15px] font-semibold tabular-nums", s.muted ? "text-fg-muted" : "text-fg")}>
              {s.value.toLocaleString()}
            </p>
          </div>
        ))}
      </div>
    </div>
  );
}

function CheckOption({
  checked,
  onChange,
  label,
  hint,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label: string;
  hint: string;
}) {
  return (
    <label className="flex cursor-pointer items-start gap-2.5 rounded-lg px-2 py-1.5 transition-colors hover:bg-hover">
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="mt-0.5 h-4 w-4 shrink-0 accent-accent-500"
      />
      <span className="min-w-0">
        <span className="block text-[13px] text-fg">{label}</span>
        <span className="block text-[12px] leading-5 text-fg-muted">{hint}</span>
      </span>
    </label>
  );
}

function RadioCard({
  name,
  checked,
  onChange,
  title,
  body,
  icon,
  danger,
}: {
  name: string;
  checked: boolean;
  onChange: () => void;
  title: string;
  body: ReactNode;
  icon?: ReactNode;
  danger?: boolean;
}) {
  return (
    <label
      className={cn(
        "flex cursor-pointer items-start gap-3 rounded-lg border px-3.5 py-3 transition-colors has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-accent-500",
        checked
          ? danger
            ? "border-bad/50 bg-bad/5"
            : "border-accent-500/30 bg-accent-500/10"
          : "border-line hover:border-line-strong hover:bg-hover",
      )}
    >
      <input type="radio" name={name} checked={checked} onChange={onChange} className="sr-only" />
      <span
        className={cn(
          "mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border",
          checked ? (danger ? "border-bad" : "border-accent-500") : "border-input",
        )}
        aria-hidden="true"
      >
        {checked && <span className={cn("h-2 w-2 rounded-full", danger ? "bg-bad" : "bg-accent-500")} />}
      </span>
      <span className="min-w-0">
        <span className="flex items-center gap-1.5 text-[13px] font-medium text-fg">
          {icon}
          {title}
        </span>
        <span className="mt-0.5 block text-[12px] leading-5 text-fg-muted">{body}</span>
      </span>
    </label>
  );
}

function plural(n: number, one: string, many = `${one}s`) {
  return `${n} ${n === 1 ? one : many}`;
}

// ── Import from other routers ───────────────────────────────────────
function ForeignImportSettings() {
  const toast = useToast();
  const confirm = useConfirm();
  const import9rRef = useRef<HTMLInputElement>(null);
  const importOmniRef = useRef<HTMLInputElement>(null);
  const importSqliteRef = useRef<HTMLInputElement>(null);
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<ForeignImportResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [analyze, setAnalyze] = useState<N9routerAnalyzeResult | null>(null);
  const [pendingSqliteFile, setPendingSqliteFile] = useState<File | null>(null);
  const [sqliteOptions, setSqliteOptions] = useState<N9routerImportOptions>({
    usage: true,
    providers: true,
    api_keys: true,
    proxy_pools: true,
    chains: true,
    settings: true,
    password: true,
    mode: "merge",
  });
  const setSection = (key: keyof N9routerImportOptions, v: boolean) =>
    setSqliteOptions((o) => ({ ...o, [key]: v }));

  const runImport = async (source: "9router" | "omniroute", file: File) => {
    setLoading(true);
    setError(null);
    setResult(null);
    try {
      const raw = await file.text();
      const config = JSON.parse(raw);
      const res = await api.importForeignConfig(source, config);
      setResult(res);
      const parts: string[] = [];
      if (res.accounts) parts.push(`${res.accounts} account${res.accounts === 1 ? "" : "s"}`);
      if (res.custom_providers) parts.push(`${res.custom_providers} provider${res.custom_providers === 1 ? "" : "s"}`);
      if (res.api_keys) parts.push(`${res.api_keys} key${res.api_keys === 1 ? "" : "s"}`);
      if (res.chains) parts.push(`${res.chains} chain${res.chains === 1 ? "" : "s"}`);
      if (res.aliases) parts.push(`${res.aliases} alias${res.aliases === 1 ? "" : "es"}`);
      if (res.proxy_pools) parts.push(`${res.proxy_pools} pool${res.proxy_pools === 1 ? "" : "s"}`);
      const summary = parts.length ? parts.join(", ") : "nothing";
      toast.success(
        `${source === "9router" ? "9router" : "OmniRoute"} import complete`,
        `${res.imported} record${res.imported === 1 ? "" : "s"} imported (${summary}).${res.skipped ? ` ${res.skipped} skipped.` : ""}`,
      );
    } catch (e) {
      setError((e as Error).message || "Import failed.");
      toast.error("Import failed", (e as Error).message);
    } finally {
      setLoading(false);
    }
  };

  const handle9rFile = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) void runImport("9router", file);
    if (import9rRef.current) import9rRef.current.value = "";
  };

  const handleOmniFile = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) void runImport("omniroute", file);
    if (importOmniRef.current) importOmniRef.current.value = "";
  };

  const handleSqliteFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    setLoading(true);
    setError(null);
    setResult(null);
    setAnalyze(null);
    try {
      const counts = await api.analyze9routerSQLite(file);
      setAnalyze(counts);
      setPendingSqliteFile(file);
    } catch (err) {
      setError((err as Error).message || "Analyze failed.");
      toast.error("Analyze failed", (err as Error).message);
    } finally {
      setLoading(false);
      if (importSqliteRef.current) importSqliteRef.current.value = "";
    }
  };

  // confirmSqliteImport asks before any import that removes or replaces data:
  // overwrite/wipe modes, or replacing the dashboard password.
  const confirmSqliteImport = async () => {
    const { mode, password } = sqliteOptions;
    if (mode === "merge" && !password) return true;
    const consequences: string[] = [];
    if (mode === "overwrite") {
      consequences.push("Rows from earlier 9router imports in the selected sections are removed, then re-imported from this file.");
    }
    if (mode === "wipe") {
      consequences.push(
        "Every row in the selected sections is deleted — including data created in KeiRouter — and replaced with this file's contents. A safety backup is created first.",
      );
    }
    if (password) {
      consequences.push("The dashboard password is replaced with 9router's, and you will need to sign in again.");
    }
    return confirm({
      title:
        mode === "wipe" ? "Wipe and replace data?" : mode === "overwrite" ? "Overwrite previous 9router imports?" : "Replace the dashboard password?",
      description: consequences.join(" "),
      confirmLabel: mode === "wipe" ? "Wipe and import" : mode === "overwrite" ? "Overwrite and import" : "Import",
      tone: mode === "merge" ? "default" : "danger",
    });
  };

  const runSqliteImport = async () => {
    if (!pendingSqliteFile) return;
    if (!(await confirmSqliteImport())) return;
    setLoading(true);
    setError(null);
    setResult(null);
    try {
      const res = await api.import9routerSQLite(pendingSqliteFile, sqliteOptions);
      setResult(res);
      const parts: string[] = [];
      if (res.accounts) parts.push(`${res.accounts} account${res.accounts === 1 ? "" : "s"}`);
      if (res.custom_providers) parts.push(`${res.custom_providers} provider${res.custom_providers === 1 ? "" : "s"}`);
      if (res.api_keys) parts.push(`${res.api_keys} key${res.api_keys === 1 ? "" : "s"}`);
      if (res.chains) parts.push(`${res.chains} chain${res.chains === 1 ? "" : "s"}`);
      if (res.aliases) parts.push(`${res.aliases} alias${res.aliases === 1 ? "" : "es"}`);
      if (res.proxy_pools) parts.push(`${res.proxy_pools} pool${res.proxy_pools === 1 ? "" : "s"}`);
      if (res.usage_records) parts.push(`${res.usage_records} usage record${res.usage_records === 1 ? "" : "s"}`);
      const summary = parts.length ? parts.join(", ") : "nothing";
      toast.success(
        "9router SQLite import complete",
        `${res.imported} record${res.imported === 1 ? "" : "s"} imported (${summary}).${res.skipped ? ` ${res.skipped} skipped.` : ""}`,
      );
      setPendingSqliteFile(null);
      setAnalyze(null);
    } catch (err) {
      setError((err as Error).message || "Import failed.");
      toast.error("Import failed", (err as Error).message);
    } finally {
      setLoading(false);
    }
  };

  const detected = analyze
    ? (
        [
          ["Providers/accounts", analyze.providerConnections],
          ["Custom nodes", analyze.providerNodes],
          ["API keys", analyze.apiKeys],
          ["Chains", analyze.combos],
          ["Proxy pools", analyze.proxyPools],
          ["Usage records", analyze.usageHistory],
        ] as [string, number | undefined][]
      )
        .filter(([, v]) => v != null)
        .map(([label, value]) => ({ label, value: value as number }))
    : [];

  // Only show counts that carry information; "Skipped" always shows.
  const resultItems = result
    ? [
        { label: "Accounts", value: result.accounts },
        { label: "Custom providers", value: result.custom_providers },
        { label: "API keys", value: result.api_keys },
        { label: "Chains", value: result.chains },
        { label: "Aliases", value: result.aliases },
        { label: "Proxy pools", value: result.proxy_pools },
        ...(result.usage_records != null ? [{ label: "Usage records", value: result.usage_records }] : []),
      ].filter((i) => i.value > 0)
    : [];

  return (
    <SettingsCard icon={Import} title="Import from other routers" description="JSON imports keep existing data" busy={loading}>
      <SettingRow
        label={
          <>
            9router backup <Badge>Full credential transfer</Badge>
          </>
        }
        description="Providers, keys, chains, proxy pools and aliases."
        infoLabel="About 9router backup imports"
        info="Provider connections (API keys and OAuth tokens re-sealed), custom provider nodes, API keys (re-hashed, so the same key string keeps working), combos as chains, proxy pools and model aliases."
      >
        <Button variant="ghost" onClick={() => import9rRef.current?.click()} disabled={loading}>
          <Upload className="text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
          Select 9router JSON
        </Button>
        <input ref={import9rRef} type="file" accept="application/json,.json" className="hidden" tabIndex={-1} aria-hidden="true" onChange={handle9rFile} />
      </SettingRow>

      <SettingRow
        label={
          <>
            OmniRoute backup <Badge>Credentials redacted</Badge>
          </>
        }
        description="Accounts arrive disabled. Re-authenticate after import."
        infoLabel="About OmniRoute backup imports"
        info="OmniRoute exports redact credentials, so accounts arrive as disabled stubs. Custom provider nodes, combos (as chains), proxy pools and aliases transfer fully. API keys must be re-created."
      >
        <Button variant="ghost" onClick={() => importOmniRef.current?.click()} disabled={loading}>
          <Upload className="text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
          Select OmniRoute JSON
        </Button>
        <input ref={importOmniRef} type="file" accept="application/json,.json" className="hidden" tabIndex={-1} aria-hidden="true" onChange={handleOmniFile} />
      </SettingRow>

      <SettingRow
        label={
          <>
            9router database <Badge>Includes usage history</Badge>
          </>
        }
        description={
          analyze ? (
            <>
              Analyzed <span className="font-mono text-fg">{pendingSqliteFile?.name}</span>. Choose what to import below.
            </>
          ) : (
            <>
              Upload <Code>data.sqlite</Code>. You pick sections before anything is imported.
            </>
          )
        }
        infoLabel="About 9router database imports"
        info="Imports everything the JSON backup does, plus usage history, token saver settings, routing strategy and the dashboard password."
      >
        {!analyze && !pendingSqliteFile && (
          <>
            <Button variant="ghost" onClick={() => importSqliteRef.current?.click()} disabled={loading}>
              <Upload className="text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
              Select data.sqlite
            </Button>
            <input
              ref={importSqliteRef}
              type="file"
              accept=".sqlite,.db,application/vnd.sqlite3,application/x-sqlite3"
              className="hidden"
              tabIndex={-1}
              aria-hidden="true"
              onChange={handleSqliteFile}
            />
          </>
        )}
      </SettingRow>

      {analyze && (
        <div className="space-y-4 bg-subtle px-4 py-4">
          {detected.length > 0 && (
            <div>
              <h3 className="mb-1.5 text-[12.5px] font-medium text-fg">Found in file</h3>
              <StatStrip items={detected} />
            </div>
          )}

          <fieldset>
            <legend className="mb-1 text-[12.5px] font-medium text-fg">Sections to import</legend>
            <div className="-mx-2 grid gap-0.5 sm:grid-cols-2">
              <CheckOption checked={sqliteOptions.usage} onChange={(v) => setSection("usage", v)} label="Usage records" hint="Token usage, costs, model stats" />
              <CheckOption checked={sqliteOptions.providers} onChange={(v) => setSection("providers", v)} label="Providers & accounts" hint="Credentials are re-encrypted" />
              <CheckOption checked={sqliteOptions.api_keys} onChange={(v) => setSection("api_keys", v)} label="API keys" hint="Same key strings keep working" />
              <CheckOption checked={sqliteOptions.proxy_pools} onChange={(v) => setSection("proxy_pools", v)} label="Proxy pools" hint="Cloudflare and HTTP proxies" />
              <CheckOption checked={sqliteOptions.chains} onChange={(v) => setSection("chains", v)} label="Routing chains" hint="Combos become chains" />
              <CheckOption checked={sqliteOptions.settings} onChange={(v) => setSection("settings", v)} label="Settings" hint="Token saver and routing strategy" />
              <CheckOption checked={sqliteOptions.password} onChange={(v) => setSection("password", v)} label="Dashboard password" hint="Replaces yours; you sign in again" />
            </div>
          </fieldset>

          <fieldset>
            <legend className="mb-1.5 text-[12.5px] font-medium text-fg">Import mode</legend>
            <div className="grid gap-2 sm:grid-cols-3">
              <RadioCard
                name="n9mode"
                checked={sqliteOptions.mode === "merge"}
                onChange={() => setSqliteOptions((o) => ({ ...o, mode: "merge" }))}
                title="Merge"
                body="Add new rows, skip existing."
              />
              <RadioCard
                name="n9mode"
                checked={sqliteOptions.mode === "overwrite"}
                onChange={() => setSqliteOptions((o) => ({ ...o, mode: "overwrite" }))}
                title="Overwrite"
                body="Replace earlier 9router imports."
              />
              <RadioCard
                name="n9mode"
                danger
                checked={sqliteOptions.mode === "wipe"}
                onChange={() => setSqliteOptions((o) => ({ ...o, mode: "wipe" }))}
                title="Wipe & replace"
                body={<span className="text-bad">Deletes all selected data, including KeiRouter&apos;s own.</span>}
              />
            </div>
            {sqliteOptions.mode === "wipe" && (
              <Note tone="bad" className="mt-2">
                Deletes every row in the selected sections, not just imported ones. A safety backup is created first.
              </Note>
            )}
          </fieldset>

          <div className="flex justify-end gap-2">
            <Button
              variant="ghost"
              onClick={() => {
                setAnalyze(null);
                setPendingSqliteFile(null);
              }}
              disabled={loading}
            >
              Cancel
            </Button>
            <Button
              variant={sqliteOptions.mode === "wipe" ? "danger" : "primary"}
              onClick={runSqliteImport}
              disabled={loading || !Object.values(sqliteOptions).some((v) => typeof v === "boolean" && v)}
            >
              {loading ? "Importing…" : "Run import"}
            </Button>
          </div>
        </div>
      )}

      {error && (
        <div className="px-4 py-3">
          <ErrorBanner message={error} />
        </div>
      )}

      {result && (
        <div className="space-y-3 px-4 py-4">
          <h3 className="text-[12.5px] font-medium text-fg">Last import</h3>
          {resultItems.length > 0 ? (
            <StatStrip items={[...resultItems, { label: "Skipped", value: result.skipped, muted: true }]} />
          ) : (
            <p className="text-[12.5px] text-fg-muted">
              Nothing new to import{result.skipped ? ` · ${plural(result.skipped, "record")} skipped` : ""}.
            </p>
          )}
          {result.errors && result.errors.length > 0 && (
            <details className="rounded-lg border border-line bg-subtle px-3 py-2">
              <summary className="cursor-pointer rounded-md text-[12px] font-medium text-fg-muted focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500">
                {plural(result.errors.length, "warning")}
              </summary>
              <ul className="mt-2 max-h-40 space-y-1 overflow-y-auto text-[12px] text-fg-muted">
                {result.errors.map((e, i) => (
                  <li key={i} className="break-all font-mono">
                    {e}
                  </li>
                ))}
              </ul>
            </details>
          )}
        </div>
      )}
    </SettingsCard>
  );
}

// ── KeiRouter backups ───────────────────────────────────────────────
type DbBusy = { loading: boolean; setLoading: (v: boolean) => void };

function useSqliteStatus() {
  return useQuery({ queryKey: ["sqlite-status"], queryFn: () => api.sqliteStatus() });
}

function DatabaseSettings({ loading, setLoading }: DbBusy) {
  const toast = useToast();
  const confirm = useConfirm();
  const importRef = useRef<HTMLInputElement>(null);
  const sqlite = useSqliteStatus();

  const [exportOpen, setExportOpen] = useState(false);
  const [usePortable, setUsePortable] = useState(false);
  const [exportPass, setExportPass] = useState("");
  const [exportConfirm, setExportConfirm] = useState("");
  const [showExportPass, setShowExportPass] = useState(false);

  const [importOpen, setImportOpen] = useState(false);
  const [pendingPayload, setPendingPayload] = useState<Record<string, unknown> | null>(null);
  const [importPass, setImportPass] = useState("");
  const [showImportPass, setShowImportPass] = useState(false);
  const [importError, setImportError] = useState<string | null>(null);

  const resetExport = () => {
    setExportOpen(false);
    setUsePortable(false);
    setExportPass("");
    setExportConfirm("");
    setShowExportPass(false);
  };

  const resetImport = () => {
    setImportOpen(false);
    setPendingPayload(null);
    setImportPass("");
    setShowImportPass(false);
    setImportError(null);
    if (importRef.current) importRef.current.value = "";
  };

  const downloadBackup = async (pass: string | undefined) => {
    setLoading(true);
    try {
      const data = await api.exportDatabase(pass || undefined);
      const content = JSON.stringify(data, null, 2);
      const blob = new Blob([content], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      const stamp = new Date().toISOString().replace(/[.:]/g, "-");
      a.href = url;
      a.download = `keirouter-backup${pass ? "-portable" : ""}-${stamp}.json`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      toast.success(
        "Backup downloaded",
        pass
          ? "Portable backup saved. Keep the passphrase safe — it is required to import on another machine."
          : "Local backup saved. It only restores on this machine's master key.",
      );
      resetExport();
    } catch (e) {
      toast.error("Export failed", (e as Error).message);
    } finally {
      setLoading(false);
    }
  };

  const downloadSQLiteBackup = async () => {
    setLoading(true);
    try {
      const blob = await api.backupSQLite();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      const stamp = new Date().toISOString().replace(/[.:]/g, "-");
      a.href = url;
      a.download = `keirouter-sqlite-${stamp}.db`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      toast.success("SQLite backup downloaded", "Database file snapshot saved as .db.");
    } catch (e) {
      toast.error("SQLite backup failed", (e as Error).message);
    } finally {
      setLoading(false);
    }
  };

  const handleExportSubmit = () => {
    if (usePortable) {
      const p = exportPass.trim();
      if (!p) return;
      if (p !== exportConfirm.trim()) return;
      void downloadBackup(p);
    } else {
      void downloadBackup(undefined);
    }
  };

  const handleFilePicked = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;

    try {
      const raw = await file.text();
      const payload = JSON.parse(raw);

      if (payload && (payload as { portable?: boolean }).portable === true) {
        setPendingPayload(payload);
        setImportPass("");
        setShowImportPass(false);
        setImportError(null);
        setImportOpen(true);
        return;
      }

      // Local backups apply straight away, so confirm the overwrite first.
      const ok = await confirm({
        title: "Restore this backup?",
        description: (
          <>
            Records in <span className="font-mono text-fg">{file.name}</span> are merged into this install. Matching
            records are overwritten with the backup&apos;s values; records that aren&apos;t in the backup are kept.
          </>
        ),
        confirmLabel: "Restore backup",
      });
      if (!ok) {
        if (importRef.current) importRef.current.value = "";
        return;
      }

      setLoading(true);
      const result = await api.importDatabase(payload);
      toast.success("Import complete", `${result.imported} records restored. Existing data was merged or updated.`);
      if (importRef.current) importRef.current.value = "";
    } catch (e) {
      toast.error("Import failed", (e as Error).message);
      if (importRef.current) importRef.current.value = "";
    } finally {
      setLoading(false);
    }
  };

  const submitImportWithPass = async () => {
    const p = importPass.trim();
    if (!p) {
      setImportError("Enter the passphrase used when this backup was exported.");
      return;
    }
    if (!pendingPayload) {
      setImportError("No backup loaded. Select a file first.");
      return;
    }
    setImportError(null);
    setLoading(true);
    try {
      const result = await api.importDatabase(pendingPayload, p);
      toast.success("Import complete", `${result.imported} records restored. Existing data was merged or updated.`);
      resetImport();
    } catch (e) {
      setImportError((e as Error).message || "Import failed. Check the passphrase and try again.");
    } finally {
      setLoading(false);
    }
  };

  const exportStrength = strengthOf(exportPass);
  const exportMismatch = usePortable && exportConfirm.length > 0 && exportPass !== exportConfirm;
  const exportDisabled = loading || (usePortable && (!exportPass.trim() || exportPass !== exportConfirm));
  const sqliteAvailable = sqlite.data?.available === true;

  return (
    <>
      <SettingsCard icon={DatabaseBackup} title="Backups" busy={loading}>
        <SettingRow label="Download configuration" description="JSON file. Local, or portable with a passphrase.">
          <Button variant="ghost" onClick={() => setExportOpen(true)} disabled={loading}>
            <Download className="text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
            Download backup
          </Button>
        </SettingRow>
        <SettingRow label="Restore configuration" description="Merges a JSON backup. Nothing is deleted.">
          <Button variant="ghost" onClick={() => importRef.current?.click()} disabled={loading}>
            <Upload className="text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
            Restore backup
          </Button>
          <input
            ref={importRef}
            type="file"
            accept="application/json,.json"
            className="hidden"
            tabIndex={-1}
            aria-hidden="true"
            onChange={handleFilePicked}
          />
        </SettingRow>
        <SettingRow
          label={
            <>
              Database snapshot
              <Badge tone={sqliteAvailable ? "success" : "neutral"}>
                {sqlite.isLoading ? "Checking…" : sqliteAvailable ? "SQLite active" : "Unavailable"}
              </Badge>
            </>
          }
          description={
            sqlite.data?.path ? (
              <span className="block truncate font-mono" title={sqlite.data.path}>
                {sqlite.data.path}
              </span>
            ) : !sqlite.isLoading && !sqliteAvailable ? (
              "Only available with SQLite storage."
            ) : undefined
          }
          infoLabel="About database snapshots"
          info={
            <>
              Raw <span className="font-mono">.db</span> copy made with SQLite VACUUM INTO.
              {sqlite.data?.dialect ? ` Driver: ${sqlite.data.dialect}.` : ""} Postgres and in-memory databases aren&apos;t
              eligible.
            </>
          }
        >
          <Button variant="ghost" onClick={downloadSQLiteBackup} disabled={loading || !sqliteAvailable}>
            <Download className="text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
            Download .db
          </Button>
        </SettingRow>
      </SettingsCard>

      <Modal
        open={exportOpen}
        onClose={() => (loading ? null : resetExport())}
        title="Download backup"
        subtitle="Choose how credentials are encrypted."
        maxWidth="max-w-md"
      >
        <div className="max-h-[55vh] space-y-4 overflow-y-auto px-5 py-4">
          <fieldset className="space-y-2">
            <legend className="sr-only">Backup type</legend>
            <RadioCard
              name="export-mode"
              checked={!usePortable}
              onChange={() => setUsePortable(false)}
              icon={<ShieldCheck className="h-4 w-4 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />}
              title="Local backup"
              body="Restores only on this install. No passphrase."
            />
            <RadioCard
              name="export-mode"
              checked={usePortable}
              onChange={() => setUsePortable(true)}
              icon={<KeyRound className="h-4 w-4 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />}
              title="Portable backup"
              body="Passphrase-protected. Restores on any machine."
            />
          </fieldset>

          {usePortable && (
            <div className="space-y-3">
              <div className="space-y-1.5">
                <label htmlFor="export-pass" className="flex items-baseline justify-between text-[12.5px] font-medium text-fg">
                  Passphrase
                  <span className="text-[12px] font-normal text-fg-faint">Required</span>
                </label>
                <PassphraseInput
                  id="export-pass"
                  value={exportPass}
                  onChange={setExportPass}
                  show={showExportPass}
                  onToggleShow={() => setShowExportPass((s) => !s)}
                  placeholder="At least 8 characters"
                  autoFocus
                  ariaDescribedBy="export-pass-strength"
                />
                <div className="pt-1">
                  <div className="h-1 w-full overflow-hidden rounded-full bg-track" aria-hidden="true">
                    <div
                      className={cn(
                        "h-full transition-all duration-300",
                        exportStrength.tone === "strong"
                          ? "bg-ok"
                          : exportStrength.tone === "ok"
                            ? "bg-warn"
                            : exportStrength.tone === "weak"
                              ? "bg-bad"
                              : "bg-transparent",
                      )}
                      style={{ width: `${exportStrength.pct}%` }}
                    />
                  </div>
                  <p id="export-pass-strength" className="mt-1 text-[12px] text-fg-muted" aria-live="polite">
                    {exportStrength.label}
                  </p>
                </div>
              </div>

              <div className="space-y-1.5">
                <label htmlFor="export-confirm" className="flex items-baseline justify-between text-[12.5px] font-medium text-fg">
                  Confirm passphrase
                  <span className="text-[12px] font-normal text-fg-faint">Required</span>
                </label>
                <PassphraseInput
                  id="export-confirm"
                  value={exportConfirm}
                  onChange={setExportConfirm}
                  show={showExportPass}
                  onToggleShow={() => setShowExportPass((s) => !s)}
                  placeholder="Re-enter passphrase"
                  ariaInvalid={exportMismatch}
                  ariaDescribedBy={exportMismatch ? "export-confirm-error" : undefined}
                />
                {exportMismatch && (
                  <p id="export-confirm-error" role="alert" className="text-[12px] leading-5 text-bad">
                    Passphrases don&apos;t match. Re-enter the same passphrase.
                  </p>
                )}
              </div>

              <Note tone="warn">Store this passphrase safely. Without it the backup can&apos;t be restored.</Note>
            </div>
          )}
        </div>

        <div className="flex items-center justify-end gap-2 border-t border-line bg-subtle px-5 py-3">
          <Button variant="ghost" onClick={resetExport} disabled={loading}>
            Cancel
          </Button>
          <Button variant="primary" onClick={handleExportSubmit} disabled={exportDisabled}>
            <Download aria-hidden="true" />
            {loading ? "Preparing…" : "Download"}
          </Button>
        </div>
      </Modal>

      <Modal
        open={importOpen}
        onClose={() => (loading ? null : resetImport())}
        title="Portable backup detected"
        subtitle="Enter the passphrase used to export it."
      >
        <div className="space-y-4 px-5 py-4">
          <div className="space-y-1.5">
            <label htmlFor="import-pass" className="flex items-baseline justify-between text-[12.5px] font-medium text-fg">
              Passphrase
              <span className="text-[12px] font-normal text-fg-faint">Required</span>
            </label>
            <PassphraseInput
              id="import-pass"
              value={importPass}
              onChange={(v) => {
                setImportPass(v);
                if (importError) setImportError(null);
              }}
              show={showImportPass}
              onToggleShow={() => setShowImportPass((s) => !s)}
              placeholder="Passphrase from export"
              autoFocus
              ariaInvalid={!!importError}
              ariaDescribedBy={importError ? "import-pass-error" : "import-pass-hint"}
            />
            {importError ? (
              <p id="import-pass-error" role="alert" className="text-[12px] leading-5 text-bad">
                {importError}
              </p>
            ) : (
              <p id="import-pass-hint" className="text-[12px] leading-5 text-fg-muted">
                Matching records take the backup&apos;s values. Nothing is deleted.
              </p>
            )}
          </div>
        </div>

        <div className="flex items-center justify-end gap-2 border-t border-line bg-subtle px-5 py-3">
          <Button variant="ghost" onClick={resetImport} disabled={loading}>
            Cancel
          </Button>
          <Button variant="primary" onClick={submitImportWithPass} disabled={loading || !importPass.trim()}>
            <Upload aria-hidden="true" />
            {loading ? "Restoring…" : "Restore"}
          </Button>
        </div>
      </Modal>
    </>
  );
}

// ── Danger zone ─────────────────────────────────────────────────────
// Replacing the active SQLite database is the one irreversible action on
// this tab, so it lives apart from the everyday backup rows.
function DangerZone({ loading, setLoading }: DbBusy) {
  const toast = useToast();
  const confirm = useConfirm();
  const sqliteImportRef = useRef<HTMLInputElement>(null);
  const sqlite = useSqliteStatus();
  const [sqliteRestoreError, setSQLiteRestoreError] = useState<string | null>(null);
  const sqliteAvailable = sqlite.data?.available === true;

  const submitSQLiteRestore = async (file: File | null) => {
    if (!file) {
      setSQLiteRestoreError("No SQLite backup selected.");
      return;
    }
    setLoading(true);
    setSQLiteRestoreError(null);
    try {
      const result = await api.restoreSQLite(file);
      toast.success(
        "SQLite restore staged",
        `Safety backup created at ${result.safety_backup}. Restart KeiRouter to load restored database.`,
      );
    } catch (e) {
      const message = (e as Error).message || "Restore failed.";
      setSQLiteRestoreError(message);
      toast.error("SQLite restore failed", message);
    } finally {
      setLoading(false);
      if (sqliteImportRef.current) sqliteImportRef.current.value = "";
    }
  };

  const handleSQLiteFilePicked = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;
    setSQLiteRestoreError(null);
    const ok = await confirm({
      title: "Replace the active database?",
      description: (
        <>
          <span className="font-mono text-fg">{file.name}</span> replaces the active SQLite database after an integrity
          check. Providers, keys, usage records and settings switch to the backup&apos;s contents. KeiRouter saves a safety
          copy first, and you must restart it to load the restored file. Only use trusted KeiRouter SQLite backups.
        </>
      ),
      confirmLabel: "Restore database",
      tone: "danger",
    });
    if (!ok) {
      if (sqliteImportRef.current) sqliteImportRef.current.value = "";
      return;
    }
    void submitSQLiteRestore(file);
  };

  return (
    <SettingsCard icon={ArchiveRestore} title="Restore database" tone="danger" busy={loading}>
      <SettingRow
        label="Restore from SQLite backup"
        description={
          sqliteAvailable || sqlite.isLoading
            ? "Upload a .db or .sqlite backup. Current data is replaced; restart required."
            : "Only available with SQLite storage."
        }
        info="KeiRouter checks the file's integrity and saves a safety copy of the current database before replacing it."
      >
        <Button variant="danger" onClick={() => sqliteImportRef.current?.click()} disabled={loading || !sqliteAvailable}>
          <Upload strokeWidth={1.75} aria-hidden="true" />
          Restore .db / .sqlite
        </Button>
        <input
          ref={sqliteImportRef}
          type="file"
          accept=".db,.sqlite,.sqlite3,application/vnd.sqlite3,application/octet-stream"
          className="hidden"
          tabIndex={-1}
          aria-hidden="true"
          onChange={handleSQLiteFilePicked}
        />
      </SettingRow>
      {sqliteRestoreError && (
        <div className="px-4 py-3">
          <ErrorBanner message={sqliteRestoreError} />
        </div>
      )}
    </SettingsCard>
  );
}
