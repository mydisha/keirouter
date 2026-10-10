import { useState, useMemo, useRef } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Network, Plus, Trash2, Play, Upload, Pencil, X, Check,
  ToggleLeft, ToggleRight, Loader2, CheckCircle2, XCircle, CircleDot,
  RefreshCw, AlertCircle, FileText, ExternalLink, Cloud, Zap, Globe,
} from "lucide-react";
import { api, type ProxyPool } from "../lib/api";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { parseProxies, runPool } from "../lib/bulk";
import {
  Card, Button, Input, Field, Badge, Spinner, EmptyState, Modal,
} from "../components/ui";

export function ProxyPoolsPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const pools = useQuery({
    queryKey: ["proxy-pools"],
    queryFn: () => api.listProxyPools(),
    refetchInterval: (query) => query.state.data?.pools.some((pool) => pool.test_status === "testing") ? 2000 : false,
  });

  const [showCreate, setShowCreate] = useState(false);
  const [showBatch, setShowBatch] = useState(false);
  const [showCloudflareDeploy, setShowCloudflareDeploy] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);

  // Selection state
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const remove = useMutation({
    mutationFn: (id: string) => api.deleteProxyPool(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["proxy-pools"] });
      toast.success("Pool deleted", "The proxy pool has been removed. Accounts bound to it will use direct connections.");
    },
    onError: (e: Error) => toast.error("Pool deletion failed", e.message),
  });

  const toggleActive = useMutation({
    mutationFn: ({ id, is_active }: { id: string; is_active: boolean }) =>
      api.updateProxyPool(id, { is_active }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["proxy-pools"] }),
  });

  const testPool = useMutation({
    mutationFn: (id: string) => api.testProxyPool(id),
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: ["proxy-pools"] });
      if (data.status === "active") {
        toast.success("Connectivity test passed", "Proxy is reachable and responding.");
      } else {
        toast.error("Connectivity test failed", data.error || `Proxy status: ${data.status}`);
      }
    },
    onError: (e: Error) => toast.error("Connectivity test failed", e.message),
  });

  const list = pools.data?.pools ?? [];
  const activeCount = list.filter((p) => p.is_active).length;

  const toggleSelect = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const selectAll = () => {
    if (selected.size === list.length) setSelected(new Set());
    else setSelected(new Set(list.map((p) => p.id)));
  };

  // Bulk health check
  const [checking, setChecking] = useState(false);
  const handleBulkTest = async () => {
    const ids = selected.size > 0 ? [...selected] : list.map((p) => p.id);
    setChecking(true);
    let passed = 0, failed = 0;
    for (const id of ids) {
      try {
        const r = await api.testProxyPool(id);
        if (r.status === "active") passed++; else failed++;
      } catch { failed++; }
    }
    setChecking(false);
    qc.invalidateQueries({ queryKey: ["proxy-pools"] });
    toast.success("Health check complete", `${passed} pool${passed !== 1 ? "s" : ""} reachable, ${failed} failed connectivity test.`);
  };

  return (
    <>
      <PageHeader
        title="Proxy Pools"
        icon={Network}
        description="Route upstream traffic through proxy pools for resilience and geo-distribution."
        action={
          <div className="flex items-center gap-2">
            <Button variant="ghost" onClick={() => setShowCloudflareDeploy(true)}>
              <Cloud className="h-4 w-4 text-orange-500" />
              Deploy Relay
            </Button>
            <Button variant="ghost" onClick={() => setShowBatch(!showBatch)}>
              <Upload className="h-4 w-4" />
              Batch Import
            </Button>
            <Button onClick={() => { setShowCreate(true); setEditingId(null); }}>
              <Plus className="h-4 w-4" />
              Add Proxy Pool
            </Button>
          </div>
        }
      />

      <div className="space-y-4">
        <CloudflareDeployModal open={showCloudflareDeploy} onClose={() => setShowCloudflareDeploy(false)} />
        {/* Create / Edit form */}
        {(showCreate || editingId) && (
          <PoolForm
            pool={editingId ? list.find((p) => p.id === editingId) : undefined}
            onClose={() => { setShowCreate(false); setEditingId(null); }}
          />
        )}

        {/* Batch import */}
        {showBatch && <BatchImport onClose={() => setShowBatch(false)} />}

        {/* Pool list */}
        <Card>
          {/* Summary bar */}
          <div className="flex items-center justify-between border-b border-[var(--border)] px-4 py-2.5">
            <div className="flex items-center gap-3">
              <button onClick={selectAll} className="flex items-center gap-2 text-xs text-[var(--text-muted)] hover:text-[var(--text)]">
                <input type="checkbox" checked={selected.size === list.length && list.length > 0} onChange={selectAll}
                  className="h-3.5 w-3.5 rounded border-[var(--border)] accent-accent-600" />
                Select all
              </button>
              <Badge tone="neutral">{list.length} total</Badge>
              <Badge tone="success">{activeCount} active</Badge>
            </div>
            {list.length > 0 && (
              <div className="flex items-center gap-1.5">
                <button onClick={handleBulkTest} disabled={checking}
                  className="flex h-8 items-center gap-1.5 rounded-lg border border-[var(--border)] px-2 text-xs text-[var(--text-muted)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] disabled:opacity-50">
                  {checking ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
                  Health Check
                </button>
                {selected.size > 0 && (
                  <>
                    <button onClick={() => { [...selected].forEach((id) => toggleActive.mutate({ id, is_active: true })); toast.success("Pools activated", `${selected.size} pool${selected.size !== 1 ? "s" : ""} enabled for upstream routing.`); }}
                      className="flex h-8 items-center gap-1 rounded-lg border border-emerald-500/30 px-2 text-xs text-ok hover:bg-emerald-500/10">
                      <ToggleRight className="h-3.5 w-3.5" /> Activate
                    </button>
                    <button onClick={() => { [...selected].forEach((id) => toggleActive.mutate({ id, is_active: false })); toast.success("Pools deactivated", `${selected.size} pool${selected.size !== 1 ? "s" : ""} disabled. Traffic will bypass them.`); }}
                      className="flex h-8 items-center gap-1 rounded-lg border border-amber-500/30 px-2 text-xs text-amber-500 hover:bg-amber-500/10">
                      <ToggleLeft className="h-3.5 w-3.5" /> Deactivate
                    </button>
                    <button onClick={() => { [...selected].forEach((id) => remove.mutate(id)); setSelected(new Set()); }}
                      className="flex h-8 items-center gap-1 rounded-lg border border-red-500/30 px-2 text-xs text-bad hover:bg-red-500/10">
                      <Trash2 className="h-3.5 w-3.5" /> Delete
                    </button>
                    <button onClick={() => setSelected(new Set())}
                      className="flex h-8 items-center rounded-lg px-2 text-xs text-[var(--text-muted)] hover:text-[var(--text)]">
                      Clear
                    </button>
                  </>
                )}
              </div>
            )}
          </div>

          {pools.isLoading ? (
            <div className="py-12 text-center"><Spinner /></div>
          ) : list.length === 0 ? (
            <EmptyState title="No proxy pool entries yet" hint="Add a proxy pool to route traffic through egress proxies." />
          ) : (
            <div className="divide-y divide-[var(--border)]">
              {list.map((pool) => (
                <PoolRow
                  key={pool.id}
                  pool={pool}
                  selected={selected.has(pool.id)}
                  onSelect={() => toggleSelect(pool.id)}
                  onEdit={() => { setEditingId(pool.id); setShowCreate(false); }}
                  onDelete={() => remove.mutate(pool.id)}
                  onTest={() => testPool.mutate(pool.id)}
                  onToggle={() => toggleActive.mutate({ id: pool.id, is_active: !pool.is_active })}
                  testing={testPool.isPending}
                />
              ))}
            </div>
          )}
        </Card>
      </div>
    </>
  );
}

// ─── Pool Row ────────────────────────────────────────────────────────────────

function PoolRow({ pool, selected, onSelect, onEdit, onDelete, onTest, onToggle, testing }: {
  pool: ProxyPool;
  selected: boolean;
  onSelect: () => void;
  onEdit: () => void;
  onDelete: () => void;
  onTest: () => void;
  onToggle: () => void;
  testing: boolean;
}) {
  return (
    <div className="flex items-center gap-3 px-4 py-3 transition-colors hover:bg-[var(--bg-subtle)]">
      {/* Checkbox */}
      <input type="checkbox" checked={selected} onChange={onSelect}
        className="h-3.5 w-3.5 shrink-0 rounded border-[var(--border)] accent-accent-600" />

      {/* Info */}
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-sm font-medium">{pool.name}</span>
          <StatusBadge status={pool.test_status} />
          {!pool.is_active && <Badge tone="neutral">inactive</Badge>}
          {pool.type === "cloudflare" && (
            <span className="inline-flex items-center gap-1 rounded-full bg-orange-100 px-2 py-0.5 text-[10px] font-medium text-orange-700 dark:bg-orange-900/30 dark:text-orange-400">
              <Cloud className="h-3 w-3" /> cloudflare relay
            </span>
          )}
          {pool.type === "vercel" && (
            <span className="inline-flex items-center gap-1 rounded-full bg-blue-100 px-2 py-0.5 text-[10px] font-medium text-blue-700 dark:bg-blue-900/30 dark:text-blue-400">
              <Zap className="h-3 w-3" /> vercel relay
            </span>
          )}
          {pool.type === "deno" && (
            <span className="inline-flex items-center gap-1 rounded-full bg-emerald-100 px-2 py-0.5 text-[10px] font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400">
              <Globe className="h-3 w-3" /> deno relay
            </span>
          )}
        </div>
        <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-[var(--text-muted)]">
          <span className="truncate font-mono">{maskUrl(pool.proxy_url)}</span>
          {pool.no_proxy && <span>no-proxy: {pool.no_proxy}</span>}
          {pool.last_tested && <span>tested {relTime(pool.last_tested)}</span>}
          {pool.last_error && (
            <span className={pool.test_status === "testing" ? "text-warn" : "text-bad"}>
              {pool.last_error}
            </span>
          )}
          {pool.strict && <span className="font-medium">strict</span>}
        </div>
      </div>

      {/* Actions */}
      <div className="flex shrink-0 items-center gap-0.5">
        <button onClick={onToggle} disabled={pool.test_status === "testing"}
          className="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-elevated)] hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-50"
          title={pool.test_status === "testing" ? "Waiting for relay readiness" : pool.is_active ? "Deactivate" : "Activate"}>
          {pool.is_active ? <ToggleRight className="h-4 w-4 text-ok" /> : <ToggleLeft className="h-4 w-4" />}
        </button>
        <button onClick={onTest} disabled={testing || pool.test_status === "testing"}
          className="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-elevated)] hover:text-[var(--text)]" title="Test">
          {testing ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />}
        </button>
        <button onClick={onEdit}
          className="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-elevated)] hover:text-[var(--text)]" title="Edit">
          <Pencil className="h-4 w-4" />
        </button>
        <button onClick={onDelete}
          className="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-red-500/10 hover:text-red-500" title="Delete">
          <Trash2 className="h-4 w-4" />
        </button>
      </div>
    </div>
  );
}

// ─── Pool Form (Create / Edit) ───────────────────────────────────────────────

function PoolForm({ pool, onClose }: { pool?: ProxyPool; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const isEdit = !!pool;

  const [name, setName] = useState(pool?.name ?? "");
  const [proxyUrl, setProxyUrl] = useState(pool?.proxy_url ?? "");
  const [noProxy, setNoProxy] = useState(pool?.no_proxy ?? "");
  const [strict, setStrict] = useState(pool?.strict ?? false);
  const [isActive, setIsActive] = useState(pool?.is_active ?? true);

  const create = useMutation({
    mutationFn: () => api.createProxyPool({ name, proxy_url: proxyUrl, no_proxy: noProxy || undefined, strict, is_active: isActive }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["proxy-pools"] });
      toast.success("Pool created", `Proxy pool "${name}" is ready. Bind it to accounts in provider settings.`);
      onClose();
    },
    onError: (e: Error) => toast.error("Pool creation failed", e.message),
  });

  const update = useMutation({
    mutationFn: () => api.updateProxyPool(pool!.id, { name, proxy_url: proxyUrl, no_proxy: noProxy, strict, is_active: isActive }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["proxy-pools"] });
      toast.success("Pool updated", `Proxy pool "${name}" configuration has been saved.`);
      onClose();
    },
    onError: (e: Error) => toast.error("Pool update failed", e.message),
  });

  const valid = name.trim() && proxyUrl.trim();

  return (
    <Modal
      open
      onClose={onClose}
      title={isEdit ? "Edit Proxy Pool" : "Add Proxy Pool"}
      subtitle={isEdit ? `Editing "${pool?.name}"` : "Configure a proxy endpoint for upstream routing."}
    >
      <div className="space-y-4 px-6 py-5">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field label="Name">
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="us-east-residential" />
          </Field>
          <Field label="Proxy URL">
            <Input value={proxyUrl} onChange={(e) => setProxyUrl(e.target.value)} placeholder="http://user:pass@host:port" className="font-mono" />
          </Field>
        </div>

        <Field label="No Proxy (comma-separated hosts to bypass)">
          <Input value={noProxy} onChange={(e) => setNoProxy(e.target.value)} placeholder="localhost,127.0.0.1,.internal" />
        </Field>

        <div className="flex items-center gap-6">
          <label className="flex items-center gap-2 text-xs">
            <input type="checkbox" checked={isActive} onChange={(e) => setIsActive(e.target.checked)}
              className="h-3.5 w-3.5 rounded border-[var(--border)] accent-accent-600" />
            Active
            <span className="text-[var(--text-muted)]">— inactive pools are ignored at runtime</span>
          </label>
          <label className="flex items-center gap-2 text-xs">
            <input type="checkbox" checked={strict} onChange={(e) => setStrict(e.target.checked)}
              className="h-3.5 w-3.5 rounded border-[var(--border)] accent-accent-600" />
            Strict mode
            <span className="text-[var(--text-muted)]">— fail if proxy unreachable</span>
          </label>
        </div>
        <div className="flex items-center gap-2 border-t border-[var(--border)] pt-4">
          <Button onClick={() => (isEdit ? update.mutate() : create.mutate())}
            disabled={!valid || create.isPending || update.isPending}>
            {(create.isPending || update.isPending) ? <Loader2 className="h-4 w-4 animate-spin" /> : <Check className="h-4 w-4" />}
            {isEdit ? "Save changes" : "Create pool"}
          </Button>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
        </div>
      </div>
    </Modal>
  );
}

function CloudflareDeployModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [accountID, setAccountID] = useState("");
  const [apiToken, setAPIToken] = useState("");
  const [projectName, setProjectName] = useState("");

  const deploy = useMutation({
    mutationFn: () => api.deployCloudflareRelay({
      account_id: accountID.trim(),
      api_token: apiToken.trim(),
      project_name: projectName.trim() || undefined,
    }),
    onSuccess: (result) => {
      qc.invalidateQueries({ queryKey: ["proxy-pools"] });
      toast.success("Relay deployment started", `${result.name} was deployed. Readiness is checked automatically.`);
      setAccountID("");
      setAPIToken("");
      setProjectName("");
      onClose();
    },
    onError: (error: Error) => toast.error("Relay deployment failed", error.message),
  });

  return (
    <Modal open={open} onClose={() => !deploy.isPending && onClose()} title="Deploy Cloudflare Relay">
      <div className="space-y-4 px-6 py-5">
        <div className="space-y-2 rounded-xl border border-orange-500/20 bg-orange-500/5 p-4 text-xs text-[var(--text-muted)]">
          <p className="font-semibold text-[var(--text)]">Cloudflare Workers edge relay</p>
          <p>Provider requests are forwarded through the deployed Worker and the resulting relay is added to this proxy pool automatically.</p>
          <ol className="list-decimal space-y-1 pl-4">
            <li>Create an API token with Account → Workers Scripts → Edit.</li>
            <li>Select the account that owns the Worker.</li>
            <li>Ensure the account has a workers.dev subdomain configured.</li>
          </ol>
        </div>
        <Field label="Account ID">
          <Input value={accountID} onChange={(event) => setAccountID(event.target.value)} placeholder="Cloudflare account ID" className="font-mono" />
        </Field>
        <Field label="API Token">
          <Input type="password" value={apiToken} onChange={(event) => setAPIToken(event.target.value)} placeholder="Workers Scripts: Edit token" className="font-mono" />
          <a
            href="https://dash.cloudflare.com/profile/api-tokens"
            target="_blank"
            rel="noopener noreferrer"
            className="mt-1.5 inline-flex items-center gap-1 text-xs font-medium text-orange-500 hover:underline"
          >
            Get Cloudflare API token <ExternalLink className="h-3 w-3" />
          </a>
        </Field>
        <Field label="Worker Name (optional)">
          <Input value={projectName} onChange={(event) => setProjectName(event.target.value.toLowerCase())} placeholder="my-relay" className="font-mono" />
        </Field>
        <div className="flex gap-2 border-t border-[var(--border)] pt-4">
          <Button onClick={() => deploy.mutate()} disabled={!accountID.trim() || !apiToken.trim() || deploy.isPending}>
            {deploy.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Cloud className="h-4 w-4" />}
            {deploy.isPending ? "Deploying…" : "Deploy Worker"}
          </Button>
          <Button variant="ghost" onClick={onClose} disabled={deploy.isPending}>Cancel</Button>
        </div>
      </div>
    </Modal>
  );
}

// ─── Batch Import ────────────────────────────────────────────────────────────

interface ProxyImportResult {
  index: number;
  label: string;
  status: "created" | "error";
  error?: string;
}

// proxyLabel derives a readable pool name from a proxy URL (host:port),
// falling back to the raw value when the URL can't be parsed.
function proxyLabel(url: string): string {
  try {
    const u = new URL(url);
    return u.port ? `${u.hostname}:${u.port}` : u.hostname;
  } catch {
    return url;
  }
}

const PROXY_IMPORT_CONCURRENCY = 6;

function BatchImport({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState("");
  const [text, setText] = useState("");
  const [importing, setImporting] = useState(false);
  const [results, setResults] = useState<ProxyImportResult[] | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const parsed = useMemo(() => parseProxies(text), [text]);
  const validCount = parsed.entries.length;

  const onFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    const content = await file.text();
    setText((prev) => (prev.trim() ? `${prev.replace(/\s+$/, "")}\n${content}` : content));
    e.target.value = "";
  };

  const handleImport = async () => {
    if (validCount === 0) return;
    setImporting(true);

    const entries = parsed.entries;
    const res = await runPool<typeof entries[number], ProxyImportResult>(
      entries,
      PROXY_IMPORT_CONCURRENCY,
      async (entry, i) => {
        const poolName =
          entry.name?.trim() ||
          (name.trim() ? `${name.trim()}-${i + 1}` : proxyLabel(entry.url));
        try {
          await api.createProxyPool({ name: poolName, proxy_url: entry.url });
          return { index: i, label: poolName, status: "created" as const };
        } catch (err) {
          return { index: i, label: poolName, status: "error" as const, error: (err as Error).message };
        }
      },
    );

    setImporting(false);
    setResults(res);
    qc.invalidateQueries({ queryKey: ["proxy-pools"] });
    const created = res.filter((r) => r.status === "created").length;
    const failed = res.length - created;
    if (failed === 0) {
      toast.success("Batch import complete", `${created} proxy pool${created !== 1 ? "s" : ""} created.`);
    } else {
      toast.error("Batch import finished with errors", `${created} created, ${failed} failed.`);
    }
  };

  return (
    <Card>
      <div className="flex items-center justify-between border-b border-[var(--border)] px-4 py-2.5">
        <h3 className="text-sm font-semibold">Batch Import</h3>
        <button onClick={onClose} className="rounded-lg p-1.5 text-[var(--text-muted)] hover:text-[var(--text)]">
          <X className="h-4 w-4" />
        </button>
      </div>

      {results ? (
        <div className="space-y-3 px-4 py-4">
          <div className="flex flex-wrap items-center gap-2">
            <Badge tone="success">{results.filter((r) => r.status === "created").length} created</Badge>
            {results.some((r) => r.status === "error") && (
              <Badge tone="danger">{results.filter((r) => r.status === "error").length} failed</Badge>
            )}
          </div>
          <div className="max-h-60 divide-y divide-[var(--border)] overflow-y-auto rounded-lg border border-[var(--border)]">
            {results.map((r) => (
              <div key={r.index} className="flex items-center gap-3 px-3 py-2 text-xs">
                {r.status === "created" ? (
                  <CheckCircle2 className="h-4 w-4 shrink-0 text-ok" />
                ) : (
                  <XCircle className="h-4 w-4 shrink-0 text-bad" />
                )}
                <span className="flex-1 truncate font-medium">{r.label}</span>
                {r.error && <span className="truncate text-[var(--text-muted)]" title={r.error}>{r.error}</span>}
              </div>
            ))}
          </div>
          <div className="flex items-center gap-2">
            <Button variant="ghost" onClick={() => { setResults(null); setText(""); }}>
              <Upload className="h-4 w-4" /> Import more
            </Button>
            <Button onClick={onClose}>
              <Check className="h-4 w-4" /> Done
            </Button>
          </div>
        </div>
      ) : (
        <div className="space-y-3 px-4 py-4">
          <p className="text-xs text-[var(--text-muted)]">
            Paste proxy URLs, one per line. Supports <code className="font-mono">protocol://user:pass@host:port</code> and{" "}
            <code className="font-mono">host:port:user:pass</code>. Add an optional name with{" "}
            <code className="font-mono">name,url</code>. Blank lines and <code className="font-mono">#</code> comments are ignored.
          </p>
          <Field label="Pool name prefix (optional)">
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="imported-pool" className="max-w-sm" />
          </Field>
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium">Proxies</span>
            <button
              type="button"
              onClick={() => fileRef.current?.click()}
              className="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border)] px-2 py-1 text-xs text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-subtle)] hover:text-[var(--text)]"
            >
              <FileText className="h-3.5 w-3.5" /> Load file
            </button>
            <input ref={fileRef} type="file" accept=".txt,.csv,text/plain,text/csv" className="hidden" onChange={onFile} />
          </div>
          <textarea value={text} onChange={(e) => setText(e.target.value)} rows={8}
            spellCheck={false}
            placeholder={"http://user:pass@host:port\nsocks5://host:port\nhost:port:user:pass\n# comment line"}
            className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-3 py-2 font-mono text-xs placeholder:text-[var(--text-muted)] focus:border-accent-400 focus:outline-none" />

          {text.trim() && (
            <div className="flex flex-wrap items-center gap-2 text-xs">
              <Badge tone={validCount > 0 ? "success" : "neutral"}>{validCount} ready</Badge>
              {parsed.duplicates > 0 && <Badge tone="warning">{parsed.duplicates} duplicate</Badge>}
              {parsed.errors.length > 0 && <Badge tone="danger">{parsed.errors.length} invalid</Badge>}
              {parsed.errors.slice(0, 3).map((err) => (
                <span key={err.line} className="inline-flex items-center gap-1 text-[var(--text-muted)]">
                  <AlertCircle className="h-3 w-3" /> line {err.line}: {err.message}
                </span>
              ))}
            </div>
          )}

          <div className="flex items-center gap-2">
            <Button onClick={handleImport} disabled={validCount === 0 || importing}>
              {importing ? <Loader2 className="h-4 w-4 animate-spin" /> : <Upload className="h-4 w-4" />}
              {importing ? "Importing…" : `Import ${validCount || ""}`.trim()}
            </Button>
            <Button variant="ghost" onClick={onClose}>Cancel</Button>
          </div>
        </div>
      )}
    </Card>
  );
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

function StatusBadge({ status }: { status: string }) {
  if (status === "testing") {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-amber-100 px-2 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
        <Loader2 className="h-3 w-3 animate-spin" /> deploying
      </span>
    );
  }
  if (status === "active") {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-emerald-100 px-2 py-0.5 text-[10px] font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400">
        <CheckCircle2 className="h-3 w-3" /> active
      </span>
    );
  }
  if (status === "error") {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-red-100 px-2 py-0.5 text-[10px] font-medium text-red-700 dark:bg-red-900/30 dark:text-red-400">
        <XCircle className="h-3 w-3" /> error
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-[var(--bg-subtle)] px-2 py-0.5 text-[10px] font-medium text-[var(--text-muted)]">
      <CircleDot className="h-3 w-3" /> unknown
    </span>
  );
}

function maskUrl(url: string): string {
  return url.replace(/\/\/[^@/]+@/, "//••••@");
}

function relTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "";
  const diff = Date.now() - t;
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}
