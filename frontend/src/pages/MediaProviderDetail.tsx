import { useEffect, useMemo, useState, type ChangeEvent, type ReactNode } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  AudioLines,
  Boxes,
  Check,
  Clock3,
  Copy,
  Download,
  ExternalLink,
  FileAudio,
  Globe,
  Image,
  Loader2,
  Mic,
  MoreHorizontal,
  Play,
  Plug,
  Search,
  Trash2,
} from "lucide-react";
import { api, type Account, type Provider, type ProviderModel } from "../lib/api";
import { cn } from "@/lib/utils";
import { ProviderLogo } from "../components/ProviderLogo";
import { useToast } from "../components/Toast";
import { useConfirm } from "../components/ui/confirm-dialog";
import {
  Badge,
  Button,
  ErrorBanner,
  Input,
  Modal,
  SegmentedControl,
  Select,
  Skeleton,
  TablePagination,
  Toggle,
  useClientPagination,
} from "../components/ui";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../components/ui/dropdown-menu";

type Tab = "accounts" | "models" | "playground";
type Capability = "embedding" | "image" | "tts" | "stt" | "search" | "fetch";

const kindMeta: Record<Capability, { label: string; short: string; icon: typeof Image }> = {
  embedding: { label: "Embeddings", short: "Embeddings", icon: Boxes },
  image: { label: "Image generation", short: "Image", icon: Image },
  tts: { label: "Text-to-speech", short: "Text-to-speech", icon: AudioLines },
  stt: { label: "Speech-to-text", short: "Speech-to-text", icon: Mic },
  search: { label: "Web search", short: "Web search", icon: Search },
  fetch: { label: "Web fetch", short: "Web fetch", icon: Globe },
};
const CAPABILITIES = Object.keys(kindMeta) as Capability[];

function isCapability(k: string | undefined): k is Capability {
  return !!k && k in kindMeta;
}

export function MediaProviderDetailPage() {
  const { kind, id } = useParams<{ kind: string; id: string }>();
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const [params, setParams] = useSearchParams();
  const [addOpen, setAddOpen] = useState(false);

  const providers = useQuery({ queryKey: ["providers"], queryFn: () => api.providers() });
  const accounts = useQuery({ queryKey: ["accounts"], queryFn: () => api.listAccounts() });
  const models = useQuery({
    queryKey: ["provider-models", id],
    queryFn: () => api.providerModels(id!),
    enabled: !!id,
    staleTime: 60_000,
  });

  const provider = providers.data?.providers.find((p) => p.id === id);
  const myAccounts = (accounts.data?.accounts ?? []).filter((a) => a.provider === id);
  const modelList = models.data?.models ?? [];

  const remove = useMutation({
    mutationFn: (accountId: string) => api.deleteAccount(accountId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["accounts"] });
      toast.success("Account removed", "The upstream credential has been deleted and encrypted secrets purged.");
    },
    onError: (e: Error) => toast.error("Account removal failed", e.message),
  });

  const toggleAccount = useMutation({
    mutationFn: ({ accId, disabled }: { accId: string; disabled: boolean }) =>
      api.updateAccount(accId, { disabled }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["accounts"] }),
  });

  const metaKind: Capability = isCapability(kind) ? kind : "embedding";
  const meta = kindMeta[metaKind];
  // Search and fetch providers have no model catalog worth listing.
  const showModels = kind !== "search" && kind !== "fetch";
  const rawTab = params.get("tab") as Tab | null;
  const tab: Tab = rawTab === "playground" || (rawTab === "models" && showModels) ? rawTab : "accounts";
  const setTab = (t: Tab) =>
    setParams((p) => {
      if (t === "accounts") p.delete("tab");
      else p.set("tab", t);
      return p;
    }, { replace: true });

  if (providers.isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-5 w-40" />
        <Skeleton className="h-14 w-full rounded-2xl" />
        <Skeleton className="h-64 w-full rounded-2xl" />
      </div>
    );
  }

  if (!provider) {
    return (
      <div className="rounded-2xl border border-line bg-surface px-6 py-12 text-center">
        <p className="text-[13px] font-medium text-fg">This provider doesn't exist.</p>
        <Link to={`/media/${kind}`} className="mt-2 inline-block text-[13px] font-medium text-accent-500 hover:underline dark:text-accent-400">
          Back to {meta.label.toLowerCase()}
        </Link>
      </div>
    );
  }

  const isNoAuth = provider.auth_kind === "none" || provider.auth_modes.includes("none");
  const connectLabel = isNoAuth ? "Connect" : "Add account";
  const activeAccounts = myAccounts.filter((a) => !a.disabled).length;
  const canPlay = provider.drivable && myAccounts.length > 0;

  const removeOne = async (a: Account) => {
    const ok = await confirm({
      title: `Remove ${a.label || provider.display_name}?`,
      description: `Requests for ${provider.display_name} stop using this account and its encrypted credentials are purged. This cannot be undone.`,
      confirmLabel: "Remove account",
      tone: "danger",
    });
    if (ok) remove.mutate(a.id);
  };

  const tabs: [Tab, string, number | null][] = [
    ["accounts", "Accounts", myAccounts.length],
    ...(showModels ? ([["models", "Models", modelList.length]] as [Tab, string, number | null][]) : []),
    ["playground", "Playground", null],
  ];

  return (
    <>
      <nav aria-label="Breadcrumb" className="mb-3 flex items-center gap-1.5 text-[13px] text-fg-muted">
        <Link to={`/media/${kind}`} className="inline-flex items-center gap-1.5 rounded-md hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40">
          <ArrowLeft className="h-3.5 w-3.5" />
          {meta.label}
        </Link>
        <span aria-hidden="true" className="text-fg-faint">/</span>
        <span className="truncate text-fg">{provider.display_name}</span>
      </nav>

      <header className="mb-5 flex flex-wrap items-start justify-between gap-x-6 gap-y-3">
        <div className="flex min-w-0 items-center gap-3">
          <ProviderLogo icon={provider.icon} name={provider.display_name} size={40} className="rounded-lg" />
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="text-[22px] font-semibold leading-tight tracking-[-0.02em] text-fg">{provider.display_name}</h1>
              {!provider.drivable && <Badge tone="neutral">Coming soon</Badge>}
              {isNoAuth && <Badge tone="success">No credentials</Badge>}
            </div>
            <p className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[13px] text-fg-muted">
              <span className="font-mono text-[12.5px]">{provider.id}</span>
              <Dot />
              <span>{meta.label}</span>
              <Dot />
              <span>
                {myAccounts.length === 0 ? "No accounts yet" : `${activeAccounts} of ${myAccounts.length} account${myAccounts.length === 1 ? "" : "s"} active`}
              </span>
              {showModels && (
                <>
                  <Dot />
                  <span>
                    {modelList.length} model{modelList.length === 1 ? "" : "s"}
                  </span>
                </>
              )}
            </p>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {provider.api_key_url && (
            <a
              href={provider.api_key_url}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex min-h-9 items-center justify-center gap-1.5 rounded-lg border border-line-strong bg-surface px-3 py-1.5 text-[13px] font-medium text-fg transition-colors hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
            >
              Get API key
              <ExternalLink className="h-4 w-4 text-fg-faint" strokeWidth={1.75} />
            </a>
          )}
          {provider.drivable && (
            <Button onClick={() => setAddOpen(true)}>
              <Plug />
              {connectLabel}
            </Button>
          )}
        </div>
      </header>

      <div className="mb-5 flex gap-1 border-b border-line" role="tablist" aria-label={`${provider.display_name} sections`}>
        {tabs.map(([value, label, count]) => (
          <button
            key={value}
            type="button"
            role="tab"
            aria-selected={tab === value}
            onClick={() => setTab(value)}
            className={cn(
              "relative -mb-px inline-flex items-center gap-1.5 px-3 py-2.5 text-[13px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40",
              tab === value ? "text-fg" : "text-fg-muted hover:text-fg",
            )}
          >
            {label}
            {count != null && <span className="rounded-md bg-subtle px-1.5 text-[11.5px] tabular-nums text-fg-muted">{count}</span>}
            {tab === value && <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-accent-500" />}
          </button>
        ))}
      </div>

      {tab === "accounts" && (
        <AccountsPanel
          provider={provider}
          accounts={myAccounts}
          loading={accounts.isLoading}
          connectLabel={connectLabel}
          onConnect={() => setAddOpen(true)}
          onToggle={(a) => toggleAccount.mutate({ accId: a.id, disabled: !a.disabled })}
          onRemove={removeOne}
        />
      )}
      {tab === "models" && showModels && <ModelsPanel provider={provider} models={modelList} loading={models.isLoading} label={meta.label} />}
      {tab === "playground" &&
        (canPlay ? (
          <Playground provider={provider} initial={metaKind} models={modelList} />
        ) : (
          <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
            <p className="text-[14px] font-medium text-fg">{provider.drivable ? "Connect an account to use the playground" : "The playground isn't available yet"}</p>
            <p className="mx-auto mt-1 max-w-md text-[13px] text-fg-muted">
              {provider.drivable
                ? `Requests are sent through KeiRouter using one of your ${provider.display_name} accounts, so at least one is needed.`
                : `Routing to ${provider.display_name} is not supported yet, so there is nothing to try.`}
            </p>
            {provider.drivable && (
              <Button className="mt-4" onClick={() => setAddOpen(true)}>
                <Plug />
                {connectLabel}
              </Button>
            )}
          </div>
        ))}

      {provider.drivable && <AddAccountDialog provider={provider} open={addOpen} onClose={() => setAddOpen(false)} />}
    </>
  );
}

function Dot() {
  return (
    <span aria-hidden="true" className="text-fg-faint">
      ·
    </span>
  );
}

// ── Form primitives ─────────────────────────────────────────────────────────

function FormField({ label, optional, hint, children }: { label: string; optional?: boolean; hint?: ReactNode; children: ReactNode }) {
  return (
    <label className="block space-y-1.5">
      <span className="flex items-baseline justify-between text-[12.5px] font-medium text-fg">
        {label}
        {optional && <span className="text-[12px] font-normal text-fg-faint">Optional</span>}
      </span>
      {children}
      {hint && <span className="block text-[12px] leading-5 text-fg-muted">{hint}</span>}
    </label>
  );
}

const textareaClass =
  "w-full rounded-lg border border-line bg-surface px-3 py-2 text-[13px] leading-5 text-fg placeholder:text-fg-faint transition-colors hover:border-line-strong focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/25";

function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

// ── Add account ─────────────────────────────────────────────────────────────

const EMPTY_FORM = {
  label: "",
  apiKey: "",
  baseURL: "",
  region: "",
  accountID: "",
  azureEndpoint: "",
  azureDeployment: "",
  azureAPIVersion: "2024-10-01-preview",
  azureOrganization: "",
};
type AccountForm = typeof EMPTY_FORM;

// AddAccountDialog stays mounted while closed so a half-filled form survives
// closing and reopening; it resets only after a successful connect.
function AddAccountDialog({ provider, open, onClose }: { provider: Provider; open: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [form, setForm] = useState<AccountForm>(EMPTY_FORM);
  const [error, setError] = useState("");
  const set = (key: keyof AccountForm) => (e: ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setForm((f) => ({ ...f, [key]: e.target.value }));

  const create = useMutation({
    mutationFn: () => api.createAccount({
      provider: provider.id,
      label: form.label,
      api_key: form.apiKey || undefined,
      base_url: form.baseURL || undefined,
      region: provider.regions?.length ? form.region || provider.default_region : undefined,
      account_id: form.accountID || undefined,
      azure_endpoint: form.azureEndpoint || undefined,
      azure_deployment: form.azureDeployment || undefined,
      azure_api_version: form.azureAPIVersion || undefined,
      azure_organization: form.azureOrganization || undefined,
    }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["accounts"] });
      // Region is intentionally kept, matching the previous inline form.
      setForm((f) => ({ ...EMPTY_FORM, region: f.region }));
      setError("");
      toast.success("Account connected", "Upstream credentials saved and encrypted. The account is ready for routing.");
      onClose();
    },
    onError: (e: Error) => {
      setError(e.message);
      toast.error("Account connection failed", e.message);
    },
  });

  const hasRegions = (provider.regions?.length ?? 0) > 0;
  const isNoAuth = provider.auth_kind === "none" || provider.auth_modes.includes("none");
  const isAzure = provider.id === "azure";
  const isCloudflare = provider.id === "cloudflare-ai";
  const requiresBaseURL = provider.id === "custom-openai" || provider.id === "custom-anthropic";
  const canSubmit =
    (isNoAuth || !!form.apiKey.trim()) &&
    (!isCloudflare || !!form.accountID.trim()) &&
    (!isAzure || (!!form.azureEndpoint.trim() && !!form.azureDeployment.trim())) &&
    (!requiresBaseURL || !!form.baseURL.trim());

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={isNoAuth ? `Connect ${provider.display_name}` : `Add ${provider.display_name} account`}
      subtitle={isNoAuth ? "No credentials needed — this creates an account so KeiRouter can route to it." : "The key is encrypted at rest and never shown again."}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (canSubmit) create.mutate();
        }}
      >
        <div className="space-y-3.5 px-5 py-4">
          {!isNoAuth && (
            <FormField
              label="API key"
              hint={provider.api_key_url ? (
                <>
                  Create one at{" "}
                  <a href={provider.api_key_url} target="_blank" rel="noopener noreferrer" className="font-medium text-accent-500 hover:underline dark:text-accent-400">
                    {hostOf(provider.api_key_url)}
                  </a>
                  .
                </>
              ) : undefined}
            >
              <Input value={form.apiKey} onChange={set("apiKey")} placeholder="sk-..." type="password" autoComplete="off" className="font-mono" />
            </FormField>
          )}
          <FormField label="Label" optional hint="Shown in routing and usage, e.g. the team or person who owns the key.">
            <Input value={form.label} onChange={set("label")} placeholder="my-key" />
          </FormField>
          {isCloudflare && (
            <FormField label="Account ID">
              <Input value={form.accountID} onChange={set("accountID")} placeholder="abc123def456..." className="font-mono" />
            </FormField>
          )}
          {isAzure ? (
            <div className="space-y-3.5 rounded-xl border border-line bg-subtle p-3.5">
              <FormField label="Azure endpoint">
                <Input value={form.azureEndpoint} onChange={set("azureEndpoint")} placeholder="https://resource.openai.azure.com" className="font-mono" />
              </FormField>
              <div className="grid gap-3.5 sm:grid-cols-2">
                <FormField label="Deployment">
                  <Input value={form.azureDeployment} onChange={set("azureDeployment")} placeholder="gpt-4o" className="font-mono" />
                </FormField>
                <FormField label="API version">
                  <Input value={form.azureAPIVersion} onChange={set("azureAPIVersion")} placeholder="2024-10-01-preview" className="font-mono" />
                </FormField>
              </div>
              <FormField label="Organization">
                <Input value={form.azureOrganization} onChange={set("azureOrganization")} placeholder="org_..." className="font-mono" />
              </FormField>
            </div>
          ) : hasRegions ? (
            <FormField label="Region">
              <Select value={form.region || provider.default_region || ""} onChange={set("region")}>
                {(provider.regions ?? []).map((r) => (
                  <option key={r.id} value={r.id}>{r.label}</option>
                ))}
              </Select>
            </FormField>
          ) : (
            <FormField label="Base URL" optional={!requiresBaseURL} hint="Only needed for custom or self-hosted endpoints.">
              <Input value={form.baseURL} onChange={set("baseURL")} placeholder="for custom endpoints" className="font-mono" />
            </FormField>
          )}
          {error && <ErrorBanner message={error} />}
        </div>
        <div className="flex items-center justify-end gap-2 border-t border-line bg-subtle px-5 py-3">
          <Button type="button" variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" disabled={create.isPending || !canSubmit}>
            {create.isPending && <Loader2 className="animate-spin" />}
            {create.isPending ? "Adding…" : isNoAuth ? "Connect" : "Add account"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}

// ── Accounts ────────────────────────────────────────────────────────────────

function AccountsPanel({
  provider,
  accounts,
  loading,
  connectLabel,
  onConnect,
  onToggle,
  onRemove,
}: {
  provider: Provider;
  accounts: Account[];
  loading: boolean;
  connectLabel: string;
  onConnect: () => void;
  onToggle: (a: Account) => void;
  onRemove: (a: Account) => void;
}) {
  if (loading) return <Skeleton className="h-48 w-full rounded-2xl" />;

  if (accounts.length === 0) {
    return (
      <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
        <p className="text-[14px] font-medium text-fg">No {provider.display_name} accounts yet</p>
        <p className="mx-auto mt-1 max-w-md text-[13px] text-fg-muted">
          {provider.drivable
            ? "Add one to start routing. With several accounts KeiRouter rotates between them and fails over when one is rate limited."
            : "Routing to this provider is not available yet, so accounts can't be added."}
        </p>
        {provider.drivable && (
          <Button className="mt-4" onClick={onConnect}>
            <Plug />
            {connectLabel}
          </Button>
        )}
      </div>
    );
  }

  return (
    <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="flex min-h-11 items-center border-b border-line px-4 py-2 text-[12.5px] text-fg-muted">
        {accounts.length} account{accounts.length === 1 ? "" : "s"} · paused accounts receive no traffic
      </div>
      <ul className="divide-y divide-line">
        {accounts.map((a) => {
          const name = a.label || a.provider;
          return (
            <li key={a.id} className={cn("flex items-center gap-3 px-4 py-2.5 transition-colors hover:bg-hover/60", a.disabled && "bg-subtle/60")}>
              <span
                className={cn("h-2 w-2 shrink-0 rounded-full", a.needs_reconnect ? "bg-warn" : a.disabled ? "bg-fg-faint" : "bg-ok")}
                role="img"
                aria-label={a.needs_reconnect ? "Needs reconnect" : a.disabled ? "Paused" : "Active"}
              />
              <div className="min-w-0 flex-1">
                <p className="truncate text-[13px] font-medium text-fg">{name}</p>
                <div className="mt-0.5 flex flex-wrap items-center gap-1.5 text-[12px] text-fg-muted">
                  <span>{a.auth_kind === "oauth" ? "Signed in" : a.auth_kind === "none" ? "No credentials" : "API key"}</span>
                  {a.disabled && <Badge tone="neutral">Paused</Badge>}
                  {a.needs_reconnect && <Badge tone="warning">Reconnect needed</Badge>}
                </div>
              </div>
              <span className="inline-flex" aria-label={`${a.disabled ? "Resume" : "Pause"} ${name}`}>
                <Toggle checked={!a.disabled} onChange={() => onToggle(a)} />
              </span>
              <DropdownMenu>
                <DropdownMenuTrigger
                  aria-label={`Actions for ${name}`}
                  className="flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40"
                >
                  <MoreHorizontal className="h-4 w-4" />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem tone="danger" onSelect={() => onRemove(a)}>
                    <Trash2 />
                    Remove account
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

// ── Models ──────────────────────────────────────────────────────────────────

const MODELS_PER_PAGE = 12;

function ModelsPanel({ provider, models, loading, label }: { provider: Provider; models: ProviderModel[]; loading: boolean; label: string }) {
  const [modelSearchQuery, setModelSearchQuery] = useState("");

  const filteredModels = useMemo(() => {
    if (!modelSearchQuery.trim()) return models;
    const lowerQ = modelSearchQuery.toLowerCase();
    return models.filter((m) =>
      m.id.toLowerCase().includes(lowerQ) ||
      (m.name && m.name.toLowerCase().includes(lowerQ)),
    );
  }, [models, modelSearchQuery]);

  const { page, pages, paged, setPage, total } = useClientPagination(filteredModels, MODELS_PER_PAGE);
  useEffect(() => setPage(1), [modelSearchQuery, setPage]);

  if (loading) return <Skeleton className="h-72 w-full rounded-2xl" />;

  if (models.length === 0) {
    return (
      <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
        <p className="text-[13px] font-medium text-fg">No models listed</p>
        <p className="mt-1 text-[12.5px] text-fg-muted">{provider.display_name} doesn't publish a {label.toLowerCase()} model catalog. You can still route by model id.</p>
      </div>
    );
  }

  return (
    <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">
        <div className="relative w-full sm:w-72">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} />
          <input
            type="search"
            aria-label="Search media models"
            placeholder="Filter models"
            value={modelSearchQuery}
            onChange={(event) => setModelSearchQuery(event.target.value)}
            className="h-8 w-full rounded-lg border border-line bg-surface pl-8 pr-3 text-[13px] text-fg placeholder:text-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/25"
          />
        </div>
        <span className="ml-auto text-[12.5px] tabular-nums text-fg-faint">
          {filteredModels.length === models.length ? `${models.length} models` : `${filteredModels.length} of ${models.length} shown`}
        </span>
      </div>
      {filteredModels.length === 0 ? (
        <p className="px-6 py-10 text-center text-[13px] text-fg-muted">No models match “{modelSearchQuery}”.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[600px] text-[13px]">
            <thead>
              <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                <th className="px-4 py-2 font-medium">Model</th>
                <th className="px-4 py-2 font-medium">Kind</th>
                <th className="px-4 py-2 font-medium">Route as</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {paged.map((m) => (
                <ModelRow key={m.id} model={m} route={`${provider.id}/${m.id}`} />
              ))}
            </tbody>
          </table>
        </div>
      )}
      <TablePagination page={page} pages={pages} total={total} onPage={setPage} />
    </div>
  );
}

function ModelRow({ model: m, route }: { model: ProviderModel; route: string }) {
  const toast = useToast();
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(route);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1300);
      toast.success("Model name copied", route);
    } catch {
      toast.error("Couldn't copy", "Your browser blocked clipboard access.");
    }
  };
  return (
    <tr className="transition-colors hover:bg-hover/60">
      <td className="max-w-[340px] px-4 py-2">
        <p className="truncate font-medium text-fg" title={m.name || m.id}>{m.name || m.id}</p>
        {m.name && m.name !== m.id && <p className="truncate font-mono text-[11.5px] text-fg-faint">{m.id}</p>}
      </td>
      <td className="px-4 py-2 text-[12.5px] text-fg-muted">{m.kind || "—"}</td>
      <td className="max-w-[320px] px-4 py-2">
        <button type="button" onClick={copy} className="group inline-flex max-w-full items-center gap-1.5 font-mono text-[12px] text-fg-muted hover:text-fg" title="Copy model name">
          <span className="truncate">{route}</span>
          {copied ? <Check className="h-3.5 w-3.5 shrink-0 text-ok" /> : <Copy className="h-3.5 w-3.5 shrink-0 opacity-0 transition-opacity group-hover:opacity-100" />}
        </button>
      </td>
    </tr>
  );
}

// ── Playground ──────────────────────────────────────────────────────────────

type ModelOption = { id: string };

function Playground({ provider, initial, models }: { provider: Provider; initial: Capability; models: ModelOption[] }) {
  // Offer every media capability this provider serves; the page's own kind
  // is always included and selected first.
  const options = useMemo(
    () => CAPABILITIES.filter((c) => c === initial || provider.service_kinds.includes(c)),
    [initial, provider.service_kinds],
  );
  const [cap, setCap] = useState<Capability>(initial);
  useEffect(() => setCap(initial), [initial]);

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        {options.length > 1 ? (
          <SegmentedControl value={cap} onChange={setCap} options={options.map((c) => ({ value: c, label: kindMeta[c].short }))} />
        ) : (
          <p className="text-[13px] font-medium text-fg">{kindMeta[cap].label}</p>
        )}
        <p className="text-[12.5px] text-fg-muted">Requests go through KeiRouter's local endpoint and count toward usage.</p>
      </div>
      {cap === "embedding" && <EmbeddingPlayground key={cap} provider={provider} models={models} />}
      {cap === "image" && <ImagePlayground key={cap} provider={provider} models={models} />}
      {cap === "tts" && <TtsPlayground key={cap} provider={provider} models={models} />}
      {cap === "stt" && <SttPlayground key={cap} provider={provider} models={models} />}
      {cap === "search" && <SearchPlayground key={cap} provider={provider} />}
      {cap === "fetch" && <FetchPlayground key={cap} provider={provider} />}
    </div>
  );
}

// useRunner holds the request lifecycle shared by every capability: loading,
// error, result and how long the request took.
type RunState<T> = { loading: boolean; error: string; result: T | null; ms: number | null };

function useRunner<T>() {
  const [state, setState] = useState<RunState<T>>({ loading: false, error: "", result: null, ms: null });
  const run = async (fn: () => Promise<T | null>) => {
    setState({ loading: true, error: "", result: null, ms: null });
    const started = performance.now();
    try {
      const result = await fn();
      setState({ loading: false, error: "", result, ms: performance.now() - started });
    } catch (e) {
      setState({ loading: false, error: (e as Error).message, result: null, ms: performance.now() - started });
    }
  };
  return [state, run] as const;
}

function fmtMs(ms: number): string {
  return ms < 1000 ? `${Math.round(ms)} ms` : `${(ms / 1000).toFixed(2)} s`;
}

function PlaygroundLayout({
  title,
  description,
  form,
  action,
  state,
  idleHint,
  icon: Icon,
  children,
}: {
  title: string;
  description: string;
  form: ReactNode;
  action: ReactNode;
  state: RunState<unknown>;
  idleHint: string;
  icon: typeof Image;
  children: ReactNode;
}) {
  const done = state.ms !== null;
  return (
    <div className="grid gap-4 lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
      <section className="flex flex-col overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
        <div className="border-b border-line px-4 py-3">
          <h2 className="text-[13px] font-semibold text-fg">{title}</h2>
          <p className="text-[12px] text-fg-muted">{description}</p>
        </div>
        <div className="flex-1 space-y-3.5 px-4 py-4">{form}</div>
        <div className="flex items-center gap-2 border-t border-line bg-subtle px-4 py-3">{action}</div>
      </section>

      <section className="flex min-h-[280px] flex-col overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]" aria-live="polite">
        <div className="flex items-center justify-between gap-3 border-b border-line px-4 py-3">
          <h2 className="text-[13px] font-semibold text-fg">Response</h2>
          <div className="flex items-center gap-2 text-[12px] text-fg-muted">
            {state.loading && (
              <span className="inline-flex items-center gap-1.5">
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                Waiting for the provider
              </span>
            )}
            {done && (state.error ? <Badge tone="danger">Failed</Badge> : <Badge tone="success">Success</Badge>)}
            {done && (
              <span className="inline-flex items-center gap-1 tabular-nums" title="Request time">
                <Clock3 className="h-3.5 w-3.5 text-fg-faint" strokeWidth={1.75} />
                {fmtMs(state.ms!)}
              </span>
            )}
          </div>
        </div>
        <div className="flex flex-1 flex-col p-4">
          {state.error ? (
            <ErrorBanner message={state.error} />
          ) : state.loading ? (
            <div className="space-y-2">
              <Skeleton className="h-4 w-1/2" />
              <Skeleton className="h-4 w-3/4" />
              <Skeleton className="h-4 w-2/3" />
            </div>
          ) : done ? (
            children
          ) : (
            <div className="flex flex-1 flex-col items-center justify-center gap-2 py-8 text-center">
              <Icon className="h-4 w-4 text-fg-faint" strokeWidth={1.75} />
              <p className="max-w-xs text-[12.5px] text-fg-muted">{idleHint}</p>
            </div>
          )}
        </div>
      </section>
    </div>
  );
}

function RunButton({ onClick, disabled, loading, idle, busy }: { onClick: () => void; disabled: boolean; loading: boolean; idle: string; busy: string }) {
  return (
    <Button onClick={onClick} disabled={disabled}>
      {loading ? <Loader2 className="animate-spin" /> : <Play />}
      {loading ? busy : idle}
    </Button>
  );
}

function JsonBlock({ value, limit }: { value: unknown; limit?: number }) {
  const text = JSON.stringify(value, null, 2);
  return (
    <pre className="max-h-[420px] flex-1 overflow-auto rounded-lg border border-line bg-subtle p-3 font-mono text-[12px] leading-5 text-fg">
      {limit ? text.slice(0, limit) : text}
    </pre>
  );
}

function ModelField({ models, value, onChange, placeholder = "model id" }: { models: ModelOption[]; value: string; onChange: (v: string) => void; placeholder?: string }) {
  return (
    <FormField label="Model">
      {models.length > 1 ? (
        <Select value={value} onChange={(e) => onChange(e.target.value)} className="font-mono">
          {models.map((m) => <option key={m.id} value={m.id}>{m.id}</option>)}
        </Select>
      ) : (
        <Input value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} className="font-mono" />
      )}
    </FormField>
  );
}

function CopyButton({ text, label }: { text: string; label: string }) {
  const toast = useToast();
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
      toast.success("Copied", label);
    } catch {
      toast.error("Couldn't copy", "Your browser blocked clipboard access.");
    }
  };
  return (
    <Button variant="ghost" onClick={copy} aria-label={label} title={label} className="px-2">
      {copied ? <Check className="text-ok" /> : <Copy />}
    </Button>
  );
}

// ── Embeddings

function EmbeddingPlayground({ provider, models }: { provider: Provider; models: ModelOption[] }) {
  const [model, setModel] = useState(models[0]?.id ?? "");
  const [input, setInput] = useState("Hello world");
  const [state, run] = useRunner<unknown>();

  const submit = () =>
    run(async () => {
      const resp = await fetch("/v1/embeddings", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ model: `${provider.id}/${model}`, input }),
      });
      const data = await resp.json();
      if (!resp.ok) throw new Error(data.error?.message || JSON.stringify(data));
      return data;
    });

  const curlSnippet = `curl -X POST http://localhost:20180/v1/embeddings \\
  -H "Content-Type: application/json" \\
  -d '{"model":"${provider.id}/${model}","input":"${input}"}'`;

  const vectors = (state.result as { data?: { embedding?: unknown[] }[] } | null)?.data;
  const dims = Array.isArray(vectors?.[0]?.embedding) ? vectors![0].embedding!.length : null;

  return (
    <PlaygroundLayout
      title="Embeddings"
      description="Send text and get embedding vectors."
      icon={Boxes}
      idleHint="Run a request to see the returned vectors."
      state={state}
      form={
        <>
          <ModelField models={models} value={model} onChange={setModel} />
          <FormField label="Input text">
            <textarea value={input} onChange={(e) => setInput(e.target.value)} placeholder="Text to embed" rows={4} className={textareaClass} />
          </FormField>
        </>
      }
      action={
        <>
          <RunButton onClick={submit} disabled={state.loading || !model} loading={state.loading} idle="Run" busy="Running…" />
          <CopyButton text={curlSnippet} label="Copy as cURL" />
        </>
      }
    >
      <div className="flex flex-1 flex-col gap-3">
        {dims !== null && (
          <p className="text-[12.5px] text-fg-muted">
            <span className="tabular-nums text-fg">{vectors!.length}</span> vector{vectors!.length === 1 ? "" : "s"} ·{" "}
            <span className="tabular-nums text-fg">{dims.toLocaleString()}</span> dimensions
          </p>
        )}
        <JsonBlock value={state.result} limit={2000} />
      </div>
    </PlaygroundLayout>
  );
}

// ── Image

function ImagePlayground({ provider, models }: { provider: Provider; models: ModelOption[] }) {
  const [model, setModel] = useState(models[0]?.id ?? "");
  const [prompt, setPrompt] = useState("A cute cat wearing a hat");
  const [size, setSize] = useState("1024x1024");
  const [state, run] = useRunner<string>();

  const submit = () =>
    run(async () => {
      const resp = await fetch("/v1/images/generations", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ model: `${provider.id}/${model}`, prompt, size }),
      });
      const contentType = resp.headers.get("content-type") || "";
      if (contentType.includes("image")) {
        const blob = await resp.blob();
        return URL.createObjectURL(blob);
      }
      const data = await resp.json();
      if (!resp.ok) throw new Error(data.error?.message || JSON.stringify(data));
      if (data.data?.[0]?.b64_json) return `data:image/png;base64,${data.data[0].b64_json}`;
      if (data.data?.[0]?.url) return data.data[0].url as string;
      return null;
    });

  return (
    <PlaygroundLayout
      title="Image generation"
      description="Generate images from text prompts."
      icon={Image}
      idleHint="Generated images appear here."
      state={state}
      form={
        <>
          <div className="grid gap-3.5 sm:grid-cols-2">
            <ModelField models={models} value={model} onChange={setModel} />
            <FormField label="Size">
              <Select value={size} onChange={(e) => setSize(e.target.value)} className="tabular-nums">
                <option value="256x256">256×256</option>
                <option value="512x512">512×512</option>
                <option value="1024x1024">1024×1024</option>
                <option value="1792x1024">1792×1024</option>
                <option value="1024x1792">1024×1792</option>
              </Select>
            </FormField>
          </div>
          <FormField label="Prompt">
            <textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder="Describe the image" rows={4} className={textareaClass} />
          </FormField>
        </>
      }
      action={<RunButton onClick={submit} disabled={state.loading || !model} loading={state.loading} idle="Generate" busy="Generating…" />}
    >
      {state.result ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-3">
          <img src={state.result} alt="Generated" className="max-h-[420px] max-w-full rounded-lg border border-line object-contain" />
          <a href={state.result} download="image.png" className="inline-flex items-center gap-1.5 text-[12.5px] font-medium text-accent-500 hover:underline dark:text-accent-400">
            <Download className="h-3.5 w-3.5" />
            Download
          </a>
        </div>
      ) : (
        <p className="text-[12.5px] text-fg-muted">The response didn't include an image.</p>
      )}
    </PlaygroundLayout>
  );
}

// ── Text-to-speech

function TtsPlayground({ provider, models }: { provider: Provider; models: ModelOption[] }) {
  const [model, setModel] = useState(models[0]?.id ?? "");
  const [text, setText] = useState("Hello, this is a test of text to speech.");
  const [voice, setVoice] = useState("");
  const [state, run] = useRunner<string>();

  const submit = () =>
    run(async () => {
      const body: Record<string, string> = { model: `${provider.id}/${model || provider.id}`, input: text };
      if (voice) body.voice = voice;
      const resp = await fetch("/v1/audio/speech", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({}));
        throw new Error(data.error?.message || `HTTP ${resp.status}`);
      }
      const blob = await resp.blob();
      return URL.createObjectURL(blob);
    });

  return (
    <PlaygroundLayout
      title="Text-to-speech"
      description="Convert text to audio."
      icon={AudioLines}
      idleHint="Synthesized audio plays here."
      state={state}
      form={
        <>
          <div className="grid gap-3.5 sm:grid-cols-2">
            <FormField label="Model">
              <Input value={model} onChange={(e) => setModel(e.target.value)} placeholder={provider.id} className="font-mono" />
            </FormField>
            <FormField label="Voice" optional>
              <Input value={voice} onChange={(e) => setVoice(e.target.value)} placeholder="e.g. alloy" />
            </FormField>
          </div>
          <FormField label="Text">
            <textarea value={text} onChange={(e) => setText(e.target.value)} rows={4} className={textareaClass} />
          </FormField>
        </>
      }
      action={<RunButton onClick={submit} disabled={state.loading || !text} loading={state.loading} idle="Speak" busy="Synthesizing…" />}
    >
      {state.result && (
        <div className="flex flex-1 flex-col justify-center gap-3">
          <audio controls src={state.result} className="w-full" />
          <a href={state.result} download="speech.mp3" className="inline-flex items-center gap-1.5 self-start text-[12.5px] font-medium text-accent-500 hover:underline dark:text-accent-400">
            <Download className="h-3.5 w-3.5" />
            Download
          </a>
        </div>
      )}
    </PlaygroundLayout>
  );
}

// ── Speech-to-text

function SttPlayground({ provider, models }: { provider: Provider; models: ModelOption[] }) {
  const [model, setModel] = useState(models[0]?.id ?? "");
  const [file, setFile] = useState<File | null>(null);
  const [state, run] = useRunner<unknown>();

  const submit = () => {
    if (!file) return;
    run(async () => {
      const form = new FormData();
      form.append("file", file);
      form.append("model", `${provider.id}/${model || provider.id}`);
      const resp = await fetch("/v1/audio/transcriptions", { method: "POST", body: form });
      const data = await resp.json();
      if (!resp.ok) throw new Error(data.error?.message || JSON.stringify(data));
      return data;
    });
  };

  const transcript = (state.result as { text?: unknown } | null)?.text;

  return (
    <PlaygroundLayout
      title="Speech-to-text"
      description="Transcribe audio files."
      icon={Mic}
      idleHint="The transcript appears here."
      state={state}
      form={
        <>
          <FormField label="Model">
            <Input value={model} onChange={(e) => setModel(e.target.value)} placeholder={provider.id} className="font-mono" />
          </FormField>
          <FormField label="Audio file" hint="Any format the provider accepts, e.g. mp3, wav, m4a or webm.">
            <span className="flex h-9 cursor-pointer items-center gap-2 rounded-lg border border-dashed border-line-strong bg-surface px-3 text-[13px] transition-colors hover:bg-hover focus-within:border-accent-500 focus-within:ring-2 focus-within:ring-accent-500/25">
              <FileAudio className="h-4 w-4 shrink-0 text-fg-faint" strokeWidth={1.75} />
              <span className={cn("min-w-0 flex-1 truncate", file ? "text-fg" : "text-fg-faint")}>{file ? file.name : "Choose an audio file"}</span>
              {file && <span className="shrink-0 text-[12px] tabular-nums text-fg-faint">{(file.size / 1024).toFixed(0)} KB</span>}
              <input type="file" accept="audio/*" onChange={(e) => setFile(e.target.files?.[0] ?? null)} className="sr-only" />
            </span>
          </FormField>
        </>
      }
      action={<RunButton onClick={submit} disabled={state.loading || !file} loading={state.loading} idle="Transcribe" busy="Transcribing…" />}
    >
      <div className="flex flex-1 flex-col gap-3">
        {typeof transcript === "string" && (
          <div>
            <p className="mb-1.5 text-[12px] font-medium text-fg-muted">Transcript</p>
            <p className="whitespace-pre-wrap rounded-lg border border-line bg-surface p-3 text-[13px] leading-6 text-fg">{transcript || "—"}</p>
          </div>
        )}
        <div className="flex flex-1 flex-col">
          {typeof transcript === "string" && <p className="mb-1.5 text-[12px] font-medium text-fg-muted">Raw response</p>}
          <JsonBlock value={state.result} />
        </div>
      </div>
    </PlaygroundLayout>
  );
}

// ── Web search

function SearchPlayground({ provider }: { provider: Provider }) {
  const [query, setQuery] = useState("What is the weather today?");
  const [state, run] = useRunner<unknown>();

  const submit = () =>
    run(async () => {
      const resp = await fetch("/v1/search", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ model: `${provider.id}/search`, query }),
      });
      const data = await resp.json();
      if (!resp.ok) throw new Error(data.error?.message || JSON.stringify(data));
      return data;
    });

  return (
    <PlaygroundLayout
      title="Web search"
      description="Search the web."
      icon={Search}
      idleHint="Search results appear here as JSON."
      state={state}
      form={
        <FormField label="Query">
          <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Search query" />
        </FormField>
      }
      action={<RunButton onClick={submit} disabled={state.loading || !query} loading={state.loading} idle="Search" busy="Searching…" />}
    >
      <JsonBlock value={state.result} limit={3000} />
    </PlaygroundLayout>
  );
}

// ── Web fetch

function FetchPlayground({ provider }: { provider: Provider }) {
  const [url, setUrl] = useState("https://example.com");
  const [state, run] = useRunner<unknown>();

  const submit = () =>
    run(async () => {
      const resp = await fetch("/v1/web/fetch", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ model: `${provider.id}/fetch`, url }),
      });
      const data = await resp.json();
      if (!resp.ok) throw new Error(data.error?.message || JSON.stringify(data));
      return data;
    });

  return (
    <PlaygroundLayout
      title="Web fetch"
      description="Fetch and extract web page content."
      icon={Globe}
      idleHint="Extracted page content appears here as JSON."
      state={state}
      form={
        <FormField label="URL">
          <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://example.com" className="font-mono" />
        </FormField>
      }
      action={<RunButton onClick={submit} disabled={state.loading || !url} loading={state.loading} idle="Fetch" busy="Fetching…" />}
    >
      <JsonBlock value={state.result} limit={3000} />
    </PlaygroundLayout>
  );
}
