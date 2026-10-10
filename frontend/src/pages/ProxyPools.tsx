import { useState, useMemo, useRef, useId, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Plus, Trash2, Play, Upload, Pencil, X, Check, Loader2, RefreshCw, AlertCircle,
  FileText, ExternalLink, Cloud, MoreHorizontal, Power, PowerOff,
} from "lucide-react";
import { api, type Account, type ProxyPool } from "../lib/api";
import { cn } from "@/lib/utils";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { parseProxies, runPool } from "../lib/bulk";
import { Button, Input, Badge, Modal, Skeleton, Toggle, ErrorBanner } from "../components/ui";
import { useConfirm } from "../components/ui/confirm-dialog";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger,
} from "../components/ui/dropdown-menu";

// ── Local primitives (match Keys.tsx / ConnectKit field styling) ─────────────

type FieldIds = { id: string; "aria-describedby"?: string };

// FormField wires a visible label, "Optional" marker and one-line hint to
// its control. Fields without the marker are required.
function FormField({
  label,
  hint,
  optional,
  action,
  children,
}: {
  label: string;
  hint?: ReactNode;
  optional?: boolean;
  action?: ReactNode;
  children: (ids: FieldIds) => ReactNode;
}) {
  const id = useId();
  const hintId = `${id}-hint`;
  return (
    <div className="space-y-1.5">
      <div className="flex min-h-6 items-center justify-between gap-2">
        <label htmlFor={id} className="text-[12.5px] font-medium text-fg">
          {label}
        </label>
        {optional && <span className="text-[12px] text-fg-muted">Optional</span>}
        {action}
      </div>
      {children({ id, "aria-describedby": hint ? hintId : undefined })}
      {hint && (
        <p id={hintId} className="text-[12px] text-fg-muted">
          {hint}
        </p>
      )}
    </div>
  );
}

function DialogFooter({ children }: { children: ReactNode }) {
  return <div className="flex flex-wrap items-center justify-end gap-2 rounded-b-2xl border-t border-line bg-subtle px-5 py-3">{children}</div>;
}

const checkboxClass = "h-4 w-4 rounded border-input accent-accent-500";

const textareaClass =
  "w-full rounded-lg border border-input bg-surface px-3 py-2 font-mono text-[12.5px] leading-5 text-fg placeholder:text-fg-faint hover:border-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

const code = "font-mono text-[12px] text-fg";

// Count is a status number with a coloured dot; the word carries the meaning.
function Count({ n, label, dot }: { n: number; label: string; dot?: string }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      {dot && <span className={cn("h-1.5 w-1.5 rounded-full", dot)} aria-hidden="true" />}
      <span className="font-medium tabular-nums text-fg">{n.toLocaleString("en-US")}</span>
      <span className="text-fg-muted">{label}</span>
    </span>
  );
}

// ── Page ─────────────────────────────────────────────────────────────────────

export function ProxyPoolsPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const askConfirm = useConfirm();
  const pools = useQuery({
    queryKey: ["proxy-pools"],
    queryFn: () => api.listProxyPools(),
    refetchInterval: (query) => query.state.data?.pools.some((pool) => pool.test_status === "testing") ? 2000 : false,
  });
  // Read-only: shows which accounts route through each pool.
  const accounts = useQuery({ queryKey: ["accounts"], queryFn: () => api.listAccounts(), staleTime: 30_000 });

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
      toast.success("Pool deleted", "Accounts bound to it now connect directly.");
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
  const reachableCount = list.filter((p) => p.test_status === "active").length;
  const failedCount = list.filter((p) => p.test_status === "error").length;
  const untestedCount = list.length - reachableCount - failedCount;

  const usage = useMemo(() => {
    const byPool = new Map<string, Account[]>();
    for (const a of accounts.data?.accounts ?? []) {
      if (!a.proxy_pool_id) continue;
      const arr = byPool.get(a.proxy_pool_id) ?? [];
      arr.push(a);
      byPool.set(a.proxy_pool_id, arr);
    }
    return byPool;
  }, [accounts.data]);

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
    toast.success("Health check complete", `${passed} reachable, ${failed} failed.`);
  };

  const bulkActivate = () => {
    [...selected].forEach((id) => toggleActive.mutate({ id, is_active: true }));
    toast.success("Pools enabled", `${selected.size} pool${selected.size !== 1 ? "s" : ""} now route upstream traffic.`);
  };
  const bulkDeactivate = () => {
    [...selected].forEach((id) => toggleActive.mutate({ id, is_active: false }));
    toast.success("Pools disabled", `${selected.size} pool${selected.size !== 1 ? "s" : ""} disabled. Traffic bypasses them.`);
  };
  const bulkDelete = async () => {
    const n = selected.size;
    const ok = await askConfirm({
      title: `Delete ${n} proxy pool${n !== 1 ? "s" : ""}?`,
      description: "Accounts bound to them fall back to direct connections immediately. This cannot be undone.",
      confirmLabel: "Delete",
      tone: "danger",
    });
    if (!ok) return;
    [...selected].forEach((id) => remove.mutate(id));
    setSelected(new Set());
  };
  const deleteOne = async (pool: ProxyPool) => {
    const bound = usage.get(pool.id)?.length ?? 0;
    const ok = await askConfirm({
      title: `Delete ${pool.name}?`,
      description: bound > 0
        ? `${bound} account${bound !== 1 ? "s" : ""} route through this pool and will fall back to direct connections immediately. This cannot be undone.`
        : "The pool is removed permanently. This cannot be undone.",
      confirmLabel: "Delete pool",
      tone: "danger",
    });
    if (ok) remove.mutate(pool.id);
  };

  const openCreate = () => { setShowCreate(true); setEditingId(null); };
  const allSelected = selected.size === list.length && list.length > 0;
  const someSelected = selected.size > 0 && !allSelected;

  return (
    <>
      <PageHeader
        title="Proxy pools"
        description="Route provider traffic through proxies or edge relays."
        action={
          <>
            <Button variant="ghost" onClick={() => setShowCloudflareDeploy(true)}>
              <Cloud className="text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
              Deploy relay
            </Button>
            <Button variant="ghost" onClick={() => setShowBatch(true)}>
              <Upload className="text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
              Import
            </Button>
            <Button onClick={openCreate}>
              <Plus aria-hidden="true" />
              Add pool
            </Button>
          </>
        }
      />

      <CloudflareDeployModal open={showCloudflareDeploy} onClose={() => setShowCloudflareDeploy(false)} />

      {(showCreate || editingId) && (
        <PoolForm
          pool={editingId ? list.find((p) => p.id === editingId) : undefined}
          onClose={() => { setShowCreate(false); setEditingId(null); }}
        />
      )}

      {showBatch && <BatchImport onClose={() => setShowBatch(false)} />}

      {pools.isLoading ? (
        <div aria-busy="true" aria-label="Loading proxy pools">
          <Skeleton className="h-80 w-full rounded-2xl" />
        </div>
      ) : pools.isError ? (
        <ErrorBanner message={`Couldn't load proxy pools. ${pools.error instanceof Error ? pools.error.message : ""} Reload the page to try again.`.replace(/\s+/g, " ").trim()} />
      ) : list.length === 0 ? (
        <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
          <h2 className="text-[14px] font-medium text-fg">No proxy pools yet</h2>
          <p className="mx-auto mt-1 max-w-md text-[13px] text-fg-muted">
            Without a pool, every account connects to its provider directly.
          </p>
          <Button className="mt-4" onClick={openCreate}>
            <Plus aria-hidden="true" />
            Add first pool
          </Button>
        </div>
      ) : (
        <section aria-label="Proxy pools" className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
          <div className="flex min-h-12 flex-wrap items-center gap-x-4 gap-y-2 border-b border-line px-4 py-2">
            <p role="status" className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[12.5px]">
              {selected.size > 0 ? (
                <span className="text-[13px] font-medium text-fg">{selected.size} selected</span>
              ) : (
                <>
                  <Count n={list.length} label={list.length === 1 ? "pool" : "pools"} />
                  <Count n={reachableCount} label="reachable" dot="bg-ok" />
                  {failedCount > 0 && <Count n={failedCount} label="failed" dot="bg-bad" />}
                  {untestedCount > 0 && <Count n={untestedCount} label="untested" dot="bg-fg-faint" />}
                </>
              )}
            </p>
            <div className="ml-auto flex flex-wrap items-center gap-1.5">
              {selected.size > 0 ? (
                <>
                  <Button variant="ghost" onClick={handleBulkTest} disabled={checking}>
                    {checking ? <Loader2 className="animate-spin" aria-hidden="true" /> : <RefreshCw className="text-fg-faint" strokeWidth={1.75} aria-hidden="true" />}
                    {checking ? "Testing…" : `Test ${selected.size}`}
                  </Button>
                  <Button variant="ghost" onClick={bulkActivate}>
                    <Power className="text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
                    Enable
                  </Button>
                  <Button variant="ghost" onClick={bulkDeactivate}>
                    <PowerOff className="text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
                    Disable
                  </Button>
                  <Button variant="danger" onClick={bulkDelete}>
                    <Trash2 aria-hidden="true" />
                    Delete {selected.size}
                  </Button>
                  <button
                    type="button"
                    onClick={() => setSelected(new Set())}
                    aria-label="Clear selection"
                    className="flex h-9 w-9 items-center justify-center rounded-lg text-fg-muted hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
                  >
                    <X className="h-4 w-4" aria-hidden="true" />
                  </button>
                </>
              ) : (
                <Button variant="ghost" onClick={handleBulkTest} disabled={checking}>
                  {checking ? <Loader2 className="animate-spin" aria-hidden="true" /> : <RefreshCw className="text-fg-faint" strokeWidth={1.75} aria-hidden="true" />}
                  {checking ? "Testing…" : "Test all"}
                </Button>
              )}
            </div>
          </div>

          <div className="overflow-x-auto">
            <table className="w-full min-w-[860px] text-[13px]">
              <thead>
                <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                  <th scope="col" className="w-10 py-2 pl-3 pr-1">
                    <span className="flex h-6 w-6 items-center justify-center">
                      <input
                        type="checkbox"
                        checked={allSelected}
                        ref={(el) => { if (el) el.indeterminate = someSelected; }}
                        onChange={selectAll}
                        className={checkboxClass}
                        aria-label={`Select all ${list.length} pools`}
                      />
                    </span>
                  </th>
                  <th scope="col" className="px-2 py-2 font-medium">Name</th>
                  <th scope="col" className="px-4 py-2 font-medium">Proxy URL</th>
                  <th scope="col" className="px-4 py-2 font-medium">Status</th>
                  <th scope="col" className="px-4 py-2 font-medium">Used by</th>
                  <th scope="col" className="px-4 py-2 font-medium">Enabled</th>
                  <th scope="col" className="w-10 px-2 py-2"><span className="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {list.map((pool) => (
                  <PoolRow
                    key={pool.id}
                    pool={pool}
                    accounts={usage.get(pool.id) ?? []}
                    selected={selected.has(pool.id)}
                    onSelect={() => toggleSelect(pool.id)}
                    onEdit={() => { setEditingId(pool.id); setShowCreate(false); }}
                    onDelete={() => deleteOne(pool)}
                    onTest={() => testPool.mutate(pool.id)}
                    onToggle={() => toggleActive.mutate({ id: pool.id, is_active: !pool.is_active })}
                    testing={testPool.isPending}
                    testingThis={testPool.isPending && testPool.variables === pool.id}
                  />
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}
    </>
  );
}

// ─── Pool Row ────────────────────────────────────────────────────────────────

const RELAY_LABELS: Record<string, string> = {
  cloudflare: "Cloudflare relay",
  vercel: "Vercel relay",
  deno: "Deno relay",
};

function poolKind(pool: ProxyPool): string {
  if (RELAY_LABELS[pool.type]) return RELAY_LABELS[pool.type];
  const scheme = /^([a-z0-9+.-]+):\/\//i.exec(pool.proxy_url)?.[1];
  return scheme ? scheme.toLowerCase() : "http";
}

function PoolRow({ pool, accounts, selected, onSelect, onEdit, onDelete, onTest, onToggle, testing, testingThis }: {
  pool: ProxyPool;
  accounts: Account[];
  selected: boolean;
  onSelect: () => void;
  onEdit: () => void;
  onDelete: () => void;
  onTest: () => void;
  onToggle: () => void;
  testing: boolean;
  testingThis: boolean;
}) {
  const deploying = pool.test_status === "testing";
  const providers = [...new Set(accounts.map((a) => a.provider))];

  return (
    <tr className={cn("transition-colors", selected ? "bg-accent-500/5" : "hover:bg-hover", !pool.is_active && !selected && "text-fg-muted")}>
      <td className="py-2 pl-3 pr-1 align-top">
        <span className="flex h-6 w-6 items-center justify-center">
          <input
            type="checkbox"
            checked={selected}
            onChange={onSelect}
            className={checkboxClass}
            aria-label={`Select ${pool.name}`}
          />
        </span>
      </td>
      <td className="max-w-[220px] px-2 py-2.5 align-top">
        <button
          type="button"
          onClick={onEdit}
          className={cn(
            "block max-w-full truncate rounded-sm text-left font-medium hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
            pool.is_active ? "text-fg" : "text-fg-muted",
          )}
          title={pool.name}
        >
          {pool.name}
        </button>
        <div className="mt-1 flex flex-wrap items-center gap-1">
          <Badge>{poolKind(pool)}</Badge>
          {pool.strict && <Badge title="Requests fail instead of connecting directly when the proxy is unreachable">Strict</Badge>}
        </div>
      </td>
      <td className="max-w-[280px] px-4 py-2.5 align-top">
        <span className="block truncate font-mono text-[12.5px] text-fg" title={maskUrl(pool.proxy_url)}>{maskUrl(pool.proxy_url)}</span>
        {pool.no_proxy && (
          <span className="mt-0.5 block truncate text-[12px] text-fg-muted" title={pool.no_proxy}>
            Bypass: <span className="font-mono">{pool.no_proxy}</span>
          </span>
        )}
      </td>
      <td className="max-w-[240px] px-4 py-2.5 align-top">
        <span className="flex flex-wrap items-center gap-x-1.5">
          <PoolStatus status={testingThis ? "checking" : pool.test_status} />
          {!testingThis && pool.last_tested && (
            <span className="whitespace-nowrap text-[12px] tabular-nums text-fg-muted" title={new Date(pool.last_tested).toLocaleString()}>
              · {relTime(pool.last_tested)}
            </span>
          )}
        </span>
        {pool.last_error && !testingThis && (
          <span className={cn("mt-0.5 block truncate text-[12px]", deploying ? "text-warn" : "text-bad")} title={pool.last_error}>
            {pool.last_error}
          </span>
        )}
      </td>
      <td className="max-w-[200px] px-4 py-2.5 align-top">
        {accounts.length === 0 ? (
          <span className="text-fg-muted">No accounts</span>
        ) : (
          <>
            <span className="tabular-nums text-fg" title={accounts.map((a) => `${a.provider} · ${a.label}`).join("\n")}>
              {accounts.length} account{accounts.length !== 1 ? "s" : ""}
            </span>
            <span className="mt-0.5 block truncate text-[12px]">
              {providers.map((p, i) => (
                <span key={p}>
                  {i > 0 && <span className="text-fg-muted">, </span>}
                  <Link to={`/providers/${p}`} className="text-link hover:underline">{p}</Link>
                </span>
              ))}
            </span>
          </>
        )}
      </td>
      <td className="px-4 py-2.5 align-top">
        <span className="inline-flex items-center gap-2" title={deploying ? "Waiting for relay readiness" : undefined}>
          <Toggle
            checked={pool.is_active}
            onChange={() => { if (!deploying) onToggle(); }}
            disabled={deploying}
            label={`Enable ${pool.name}`}
          />
          {deploying && <span className="text-[12px] text-fg-muted">Deploying</span>}
        </span>
      </td>
      <td className="px-2 py-2 align-top">
        <DropdownMenu>
          <DropdownMenuTrigger
            aria-label={`Actions for ${pool.name}`}
            className="flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
          >
            <MoreHorizontal className="h-4 w-4" aria-hidden="true" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={onTest} disabled={testing || deploying}>
              <Play />
              Test connectivity
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onEdit}>
              <Pencil />
              Edit pool
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem tone="danger" onSelect={onDelete}>
              <Trash2 />
              Delete pool
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </td>
    </tr>
  );
}

// ─── Pool Form (Create / Edit) ───────────────────────────────────────────────

function SwitchRow({ title, description, checked, onChange }: { title: string; description: string; checked: boolean; onChange: (v: boolean) => void }) {
  const id = useId();
  return (
    <div className="flex items-center justify-between gap-4 px-3 py-2.5">
      <div className="min-w-0">
        <p id={`${id}-title`} className="text-[13px] font-medium text-fg">{title}</p>
        <p id={`${id}-desc`} className="mt-0.5 text-[12px] text-fg-muted">{description}</p>
      </div>
      <Toggle checked={checked} onChange={onChange} aria-labelledby={`${id}-title`} aria-describedby={`${id}-desc`} />
    </div>
  );
}

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
      toast.success("Pool created", `"${name}" is ready. Bind it to accounts in provider settings.`);
      onClose();
    },
    onError: (e: Error) => toast.error("Pool creation failed", e.message),
  });

  const update = useMutation({
    mutationFn: () => api.updateProxyPool(pool!.id, { name, proxy_url: proxyUrl, no_proxy: noProxy, strict, is_active: isActive }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["proxy-pools"] });
      toast.success("Pool updated", `"${name}" has been saved.`);
      onClose();
    },
    onError: (e: Error) => toast.error("Pool update failed", e.message),
  });

  const valid = name.trim() && proxyUrl.trim();
  const pending = create.isPending || update.isPending;

  return (
    <Modal
      open
      onClose={onClose}
      title={isEdit ? "Edit proxy pool" : "Add proxy pool"}
      subtitle={isEdit ? "Changes apply to every bound account" : undefined}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!valid || pending) return;
          if (isEdit) update.mutate();
          else create.mutate();
        }}
      >
        <div className="space-y-4 px-5 py-4">
          <FormField label="Name">
            {(ids) => <Input {...ids} required value={name} onChange={(e) => setName(e.target.value)} placeholder="us-east-residential" />}
          </FormField>
          <FormField label="Proxy URL" hint="HTTP, HTTPS or SOCKS5, credentials inline">
            {(ids) => (
              <Input {...ids} required value={proxyUrl} onChange={(e) => setProxyUrl(e.target.value)} placeholder="http://user:pass@host:port" className="font-mono" spellCheck={false} />
            )}
          </FormField>
          <FormField label="Bypass hosts" optional hint="Comma separated; these connect directly">
            {(ids) => (
              <Input {...ids} value={noProxy} onChange={(e) => setNoProxy(e.target.value)} placeholder="localhost,127.0.0.1,.internal" className="font-mono" spellCheck={false} />
            )}
          </FormField>
          <div className="divide-y divide-line rounded-lg border border-line">
            <SwitchRow
              title="Enabled"
              description="When off, bound accounts connect directly"
              checked={isActive}
              onChange={setIsActive}
            />
            <SwitchRow
              title="Strict mode"
              description="Fail requests instead of connecting directly"
              checked={strict}
              onChange={setStrict}
            />
          </div>
        </div>
        <DialogFooter>
          <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
          <Button type="submit" disabled={!valid || pending}>
            {pending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Check aria-hidden="true" />}
            {isEdit ? "Save changes" : "Create pool"}
          </Button>
        </DialogFooter>
      </form>
    </Modal>
  );
}

// Step is one numbered instruction in the Cloudflare deploy dialog.
function Step({ n, title, children }: { n: number; title: ReactNode; children?: ReactNode }) {
  return (
    <li className="flex gap-3">
      <span
        aria-hidden="true"
        className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-line-strong text-[12px] font-medium tabular-nums text-fg-muted"
      >
        {n}
      </span>
      <div className="min-w-0 flex-1 space-y-2 pt-0.5">
        <p className="text-[13px] text-fg">{title}</p>
        {children}
      </div>
    </li>
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

  const canDeploy = !!accountID.trim() && !!apiToken.trim() && !deploy.isPending;

  return (
    <Modal
      open={open}
      onClose={() => !deploy.isPending && onClose()}
      title="Deploy Cloudflare relay"
      subtitle="Deploys a Worker and adds it as a pool"
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (canDeploy) deploy.mutate();
        }}
      >
        <ol className="space-y-4 px-5 py-4">
          <Step n={1} title={<>Make sure the account has a <span className={code}>workers.dev</span> subdomain.</>} />
          <Step
            n={2}
            title={
              <>
                Create a token with <span className="font-medium">Workers Scripts: Edit</span>.{" "}
                <a
                  href="https://dash.cloudflare.com/profile/api-tokens"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1 font-medium text-link hover:underline"
                >
                  Open Cloudflare
                  <ExternalLink className="h-3 w-3" aria-hidden="true" />
                  <span className="sr-only">(opens in a new tab)</span>
                </a>
              </>
            }
          >
            <FormField label="API token" hint="Used only for this deployment">
              {(ids) => (
                <Input {...ids} required type="password" autoComplete="off" value={apiToken} onChange={(event) => setAPIToken(event.target.value)} className="font-mono" />
              )}
            </FormField>
          </Step>
          <Step n={3} title="Paste the account ID from the dashboard overview.">
            <FormField label="Account ID">
              {(ids) => (
                <Input {...ids} required value={accountID} onChange={(event) => setAccountID(event.target.value)} className="font-mono" spellCheck={false} />
              )}
            </FormField>
          </Step>
          <Step n={4} title="Name the Worker, or leave it blank to generate one.">
            <FormField label="Worker name" optional>
              {(ids) => (
                <Input {...ids} value={projectName} onChange={(event) => setProjectName(event.target.value.toLowerCase())} placeholder="my-relay" className="font-mono" spellCheck={false} />
              )}
            </FormField>
          </Step>
        </ol>
        <DialogFooter>
          <Button type="button" variant="ghost" onClick={onClose} disabled={deploy.isPending}>Cancel</Button>
          <Button type="submit" disabled={!canDeploy}>
            {deploy.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Cloud aria-hidden="true" />}
            {deploy.isPending ? "Deploying…" : "Deploy Worker"}
          </Button>
        </DialogFooter>
      </form>
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

  const createdCount = results?.filter((r) => r.status === "created").length ?? 0;
  const failedCount = results?.filter((r) => r.status === "error").length ?? 0;

  return (
    <Modal
      open
      onClose={() => !importing && onClose()}
      title="Import proxies"
      subtitle="Each line becomes its own pool"
      maxWidth="max-w-2xl"
    >
      {results ? (
        <>
          <div className="space-y-3 px-5 py-4">
            <p role="status" className="flex flex-wrap items-center gap-4 text-[13px]">
              <Count n={createdCount} label="created" dot="bg-ok" />
              {failedCount > 0 && <Count n={failedCount} label="failed" dot="bg-bad" />}
            </p>
            <div className="max-h-72 overflow-y-auto rounded-lg border border-line">
              <table className="w-full text-[13px]">
                <thead>
                  <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                    <th scope="col" className="px-3 py-2 font-medium">Pool</th>
                    <th scope="col" className="px-3 py-2 font-medium">Result</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {results.map((r) => (
                    <tr key={r.index}>
                      <td className="max-w-[260px] truncate px-3 py-2 font-mono text-[12.5px] text-fg" title={r.label}>{r.label}</td>
                      <td className="max-w-[320px] px-3 py-2">
                        <span className="inline-flex max-w-full items-center gap-1.5">
                          <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", r.status === "created" ? "bg-ok" : "bg-bad")} aria-hidden="true" />
                          {r.status === "created" ? (
                            <span className="text-fg">Created</span>
                          ) : (
                            <span className="truncate text-bad" title={r.error}>{r.error || "Failed"}</span>
                          )}
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
          <DialogFooter>
            <Button variant="ghost" onClick={() => { setResults(null); setText(""); }}>
              <Upload className="text-fg-faint" strokeWidth={1.75} aria-hidden="true" /> Import more
            </Button>
            <Button onClick={onClose}>
              <Check aria-hidden="true" /> Done
            </Button>
          </DialogFooter>
        </>
      ) : (
        <>
          <div className="space-y-4 px-5 py-4">
            <FormField
              label="Proxy list"
              hint={<>One per line. Prefix <code className={code}>name,</code> to name a line; <code className={code}>#</code> lines are ignored.</>}
              action={
                <button
                  type="button"
                  onClick={() => fileRef.current?.click()}
                  className="inline-flex h-7 items-center gap-1.5 rounded-lg border border-line bg-surface px-2 text-[12px] font-medium text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
                >
                  <FileText className="h-3.5 w-3.5 text-fg-faint" strokeWidth={1.75} aria-hidden="true" /> Load file
                </button>
              }
            >
              {(ids) => (
                <textarea
                  {...ids}
                  value={text}
                  onChange={(e) => setText(e.target.value)}
                  rows={9}
                  spellCheck={false}
                  placeholder={"http://user:pass@host:port\nsocks5://host:port\nhost:port:user:pass\n# comment line"}
                  className={textareaClass}
                />
              )}
            </FormField>
            <input ref={fileRef} type="file" accept=".txt,.csv,text/plain,text/csv" className="hidden" tabIndex={-1} aria-hidden="true" onChange={onFile} />

            <div role="status" aria-live="polite">
              {text.trim() && (
                <div className="space-y-1.5 rounded-lg border border-line bg-subtle px-3 py-2.5 text-[12.5px]">
                  <p className="flex flex-wrap items-center gap-4">
                    <Count n={validCount} label="ready" dot={validCount > 0 ? "bg-ok" : "bg-fg-faint"} />
                    {parsed.duplicates > 0 && <Count n={parsed.duplicates} label="duplicate, skipped" dot="bg-warn" />}
                    {parsed.errors.length > 0 && <Count n={parsed.errors.length} label="invalid" dot="bg-bad" />}
                  </p>
                  {parsed.errors.slice(0, 3).map((err) => (
                    <p key={err.line} className="flex items-center gap-1.5 text-[12px] text-bad">
                      <AlertCircle className="h-3.5 w-3.5 shrink-0" strokeWidth={1.75} aria-hidden="true" />
                      <span className="tabular-nums">Line {err.line}:</span> {err.message}
                    </p>
                  ))}
                </div>
              )}
            </div>

            <FormField label="Name prefix" optional hint="Unnamed lines become prefix-1, prefix-2…; otherwise host:port">
              {(ids) => <Input {...ids} value={name} onChange={(e) => setName(e.target.value)} placeholder="imported-pool" className="max-w-sm" />}
            </FormField>
          </div>
          <DialogFooter>
            <Button variant="ghost" onClick={onClose} disabled={importing}>Cancel</Button>
            <Button onClick={handleImport} disabled={validCount === 0 || importing}>
              {importing ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Upload aria-hidden="true" />}
              {importing ? "Importing…" : `Import ${validCount || ""}`.trim()}
            </Button>
          </DialogFooter>
        </>
      )}
    </Modal>
  );
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

const STATUS_META: Record<string, { dot: string; label: string }> = {
  checking: { dot: "bg-fg-faint", label: "Testing…" },
  testing: { dot: "bg-warn", label: "Deploying" },
  active: { dot: "bg-ok", label: "Reachable" },
  error: { dot: "bg-bad", label: "Failed" },
};

function PoolStatus({ status }: { status: string }) {
  const meta = STATUS_META[status] ?? { dot: "bg-fg-faint", label: "Untested" };
  const busy = status === "checking" || status === "testing";
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
      {busy ? (
        <Loader2 className={cn("h-3 w-3 animate-spin", status === "testing" ? "text-warn" : "text-fg-faint")} aria-hidden="true" />
      ) : (
        <span className={cn("h-1.5 w-1.5 rounded-full", meta.dot)} aria-hidden="true" />
      )}
      <span className={cn("text-[13px]", status === "error" ? "text-bad" : status === "active" ? "text-fg" : "text-fg-muted")}>{meta.label}</span>
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
