import { useEffect, useRef, useState, useMemo } from "react";
import { useParams, Link, useNavigate } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Plus, Trash2, Plug, X, Zap, ArrowUp, ArrowDown, CheckCircle, ToggleLeft, ToggleRight, Search, Route, AlertCircle, AlertTriangle, RefreshCw, Globe, Copy, Check, Upload, Loader2, XCircle, Layers, FileText, Download, ChevronDown, Clock3, Package } from "lucide-react";
import { api, type DeviceCode, type OAuthProvider, type Provider, type Account, type ProxyPool, type UpstreamQuota, type ProviderRoutingSettings, type BulkAccountResult } from "../lib/api";
import { KiroConnectModal } from "../components/KiroConnectModal";
import { QoderConnectModal } from "../components/QoderConnectModal";
import { KilocodeConnectModal } from "../components/KilocodeConnectModal";
import { CodebuddyConnectModal } from "../components/CodebuddyConnectModal";
import { KimchiConnectModal } from "../components/KimchiConnectModal";
import { CursorConnectModal } from "../components/CursorConnectModal";
import { CommandCodeConnectModal } from "../components/CommandCodeConnectModal";
import { CustomModelsSection } from "../components/CustomModelsSection";
import { useToast } from "../components/Toast";
import { parseKeys } from "../lib/bulk";

import {
  Card,
  CardHeader,
  Button,
  Input,
  Field,
  Badge,
  Spinner,
  EmptyState,
  ErrorBanner,
  Modal,
  TablePagination,
  useClientPagination,
} from "../components/ui";

// redirectURIForProvider returns the OAuth callback the provider redirects to
// after sign-in.
//
// We always use a localhost loopback redirect. Providers that use desktop /
// installed-app OAuth clients (Google for gemini-cli & antigravity, etc.) only
// whitelist loopback redirect URIs — a public dashboard URL would be rejected
// with redirect_uri_mismatch. When the gateway is co-located with the browser
// the gateway's loopback callback catches the redirect and notifies the dash via
// postMessage; otherwise the user falls back to pasting the resulting URL.
//
// Fixed-port providers (Codex, xAI) mirror their CLI's loopback flow and require
// an exact http://host:port/path redirect their OAuth client whitelists.
function redirectURIForProvider(provider: OAuthProvider): string {
  if (provider.fixed_port && provider.callback_path) {
    const host = provider.loopback_host || "127.0.0.1";
    return `http://${host}:${provider.fixed_port}${provider.callback_path}`;
  }
  const appPort = window.location.port || (window.location.protocol === "https:" ? "443" : "80");
  return `http://localhost:${appPort}/oauth/callback`;
}

export function ProviderDetailPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();

  const providers = useQuery({ queryKey: ["providers"], queryFn: () => api.providers() });
  const accounts = useQuery({ queryKey: ["accounts"], queryFn: () => api.listAccounts() });
  const oauthProviders = useQuery({ queryKey: ["oauth-providers"], queryFn: () => api.oauthProviders() });
  const pools = useQuery({ queryKey: ["proxy-pools"], queryFn: () => api.listProxyPools() });
  const routing = useQuery({
    queryKey: ["provider-routing", id],
    queryFn: () => api.providerRouting(id!),
    enabled: !!id,
  });
  const disabledModels = useQuery({
    queryKey: ["disabled-models", id],
    queryFn: () => api.listDisabledModels(id!),
    enabled: !!id,
  });
  const models = useQuery({
    queryKey: ["provider-models", id],
    queryFn: () => api.providerModels(id!),
    enabled: !!id,
    staleTime: 60_000,
  });

  const importModelsMut = useMutation({
    mutationFn: () => api.importModels(id!),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: ["provider-models", id] });
      qc.invalidateQueries({ queryKey: ["custom-models", id] });
      const msg =
        res.imported > 0
          ? `Imported ${res.imported} model${res.imported === 1 ? "" : "s"} from /models${res.skipped ? ` (${res.skipped} already present)` : ""}.`
          : res.total > 0
            ? `No new models — all ${res.total} were already registered.`
            : "No models returned by /models.";
      toast.success("Import complete", msg);
    },
    onError: (e: Error) => toast.error("Couldn't import models", e.message),
  });

  const provider = providers.data?.providers.find((p) => p.id === id);
  const oauthProvider = oauthProviders.data?.providers.find((p) => p.provider === id);
  const myAccounts = (accounts.data?.accounts ?? []).filter((a) => a.provider === id);

  const [label, setLabel] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [baseURL, setBaseURL] = useState("");
  const [region, setRegion] = useState("");
  const [accountID, setAccountID] = useState("");
  const [azureEndpoint, setAzureEndpoint] = useState("");
  const [azureDeployment, setAzureDeployment] = useState("");
  const [azureAPIVersion, setAzureAPIVersion] = useState("2024-10-01-preview");
  const [azureOrganization, setAzureOrganization] = useState("");
  const [error, setError] = useState("");
  const [oauthOpen, setOauthOpen] = useState(false);
  const [kiroOpen, setKiroOpen] = useState(false);
  const [qoderOpen, setQoderOpen] = useState(false);
  const [kilocodeOpen, setKilocodeOpen] = useState(false);
  const [codebuddyOpen, setCodebuddyOpen] = useState(false);
  const [kimchiOpen, setKimchiOpen] = useState(false);
  const [cursorOpen, setCursorOpen] = useState(false);
  const [commandcodeOpen, setCommandcodeOpen] = useState(false);
  const [addKeyOpen, setAddKeyOpen] = useState(false);
  const [bulkOpen, setBulkOpen] = useState(false);
  const [deleteProviderOpen, setDeleteProviderOpen] = useState(false);

  // Model search and pagination
  const [modelSearchQuery, setModelSearchQuery] = useState("");
  const [modelPage, setModelPage] = useState(1);
  const MODELS_PER_PAGE = 12;

  // Multi-select state for bulk enable/disable. Holds the ids of selected
  // models; selection persists across pagination and search changes.
  const [selectedModelIds, setSelectedModelIds] = useState<Set<string>>(new Set());

  const toggleModelSelection = (id: string) => {
    setSelectedModelIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  };

  const filteredModels = useMemo(() => {
    const all = models.data?.models ?? [];
    if (all.length === 0) return [];
    if (!modelSearchQuery.trim()) return all;
    const lowerQ = modelSearchQuery.toLowerCase();
    return all.filter(m =>
      m.id.toLowerCase().includes(lowerQ) ||
      (m.name && m.name.toLowerCase().includes(lowerQ)) ||
      (m.kind && m.kind.toLowerCase().includes(lowerQ))
    );
  }, [models.data?.models, modelSearchQuery]);

  useEffect(() => {
    setModelPage(1);
  }, [modelSearchQuery]);

  const totalModelPages = Math.ceil(filteredModels.length / MODELS_PER_PAGE);
  const paginatedModels = filteredModels.slice(
    (modelPage - 1) * MODELS_PER_PAGE,
    modelPage * MODELS_PER_PAGE
  );
  const modelList = models.data?.models ?? [];

  // Set default region when provider loads.
  useEffect(() => {
    if (provider?.default_region && !region) {
      setRegion(provider.default_region);
    }
  }, [provider, region]);

  const hasRegions = (provider?.regions?.length ?? 0) > 0;

  const create = useMutation({
    mutationFn: () =>
      api.createAccount({
        provider: id!,
        label,
        api_key: apiKey,
        base_url: baseURL || undefined,
        region: hasRegions ? region : undefined,
        account_id: accountID || undefined,
        azure_endpoint: azureEndpoint || undefined,
        azure_deployment: azureDeployment || undefined,
        azure_api_version: azureAPIVersion || undefined,
        azure_organization: azureOrganization || undefined,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["accounts"] });
      setLabel("");
      setApiKey("");
      setBaseURL("");
      setAccountID("");
      setAzureEndpoint("");
      setAzureDeployment("");
      setAzureAPIVersion("2024-10-01-preview");
      setAzureOrganization("");
      setError("");
      setAddKeyOpen(false);
      toast.success("Account connected", `Upstream credentials saved and encrypted. The account is ready for routing.`);
    },
    onError: (e: Error) => {
      setError(e.message);
      toast.error("Account connection failed", e.message);
    },
  });

  const remove = useMutation({
    mutationFn: (accountId: string) => api.deleteAccount(accountId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["accounts"] });
      toast.success("Account removed", "The upstream credential has been deleted and encrypted secrets purged.");
    },
    onError: (e: Error) => toast.error("Account removal failed", e.message),
  });

  const updateAccount = useMutation({
    mutationFn: ({ id: accId, patch }: { id: string; patch: { label?: string; priority?: number; disabled?: boolean; proxy_pool_id?: string } }) =>
      api.updateAccount(accId, patch),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["accounts"] }),
    onError: (e: Error) => toast.error("Account update failed", e.message),
  });

  // Per-account connection test results. Each entry holds the latest status
  // for an account: testing (in flight), ok, or error (with the upstream
  // message). Drives the inline status badge in each account row.
  const [testResults, setTestResults] = useState<Record<string, { status: "testing" | "ok" | "error"; message?: string }>>({});
  const [testingAll, setTestingAll] = useState(false);

  // runTest probes a single account's credentials and records the result.
  // Returns true when the credential is valid. On failure, refetches the
  // account list so server-side state changes (e.g. needs_reconnect) appear
  // immediately.
  const runTest = async (accountId: string): Promise<boolean> => {
    setTestResults((prev) => ({ ...prev, [accountId]: { status: "testing" } }));
    try {
      const res = await api.testAccount(accountId);
      const ok = res.status === "ok";
      setTestResults((prev) => ({ ...prev, [accountId]: { status: ok ? "ok" : "error", message: res.message } }));
      if (!ok) {
        // Refetch accounts so needs_reconnect flag is picked up.
        qc.invalidateQueries({ queryKey: ["accounts"] });
      }
      return ok;
    } catch (e) {
      setTestResults((prev) => ({ ...prev, [accountId]: { status: "error", message: (e as Error).message } }));
      qc.invalidateQueries({ queryKey: ["accounts"] });
      return false;
    }
  };

  const disableModelsMut = useMutation({
    mutationFn: (ids: string[]) => api.disableModels(id!, ids),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["disabled-models", id] });
      toast.success("Models disabled", "Selected models will be excluded from routing until re-enabled.");
    },
    onError: (e: Error) => toast.error("Model disable failed", e.message),
  });

  const enableModelsMut = useMutation({
    mutationFn: (ids: string[]) => api.enableModels(id!, ids),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["disabled-models", id] });
      toast.success("Models re-enabled", "Selected models are available for routing again.");
    },
    onError: (e: Error) => toast.error("Couldn't enable models", e.message),
  });

  const updateRouting = useMutation({
    mutationFn: (patch: Partial<ProviderRoutingSettings>) => api.updateProviderRouting(id!, patch),
    onSuccess: (data) => {
      qc.setQueryData(["provider-routing", id], data);
      toast.success("Routing updated", "Account routing strategy for this provider was saved.");
    },
    onError: (e: Error) => toast.error("Routing update failed", e.message),
  });

  // Multi-select for connected accounts: enables bulk enable / disable / delete.
  const [selectedAccountIds, setSelectedAccountIds] = useState<Set<string>>(new Set());
  // Controls the bulk-delete confirmation dialog (replaces the native confirm()).
  const [bulkDeleteConfirmOpen, setBulkDeleteConfirmOpen] = useState(false);
  const toggleAccountSelection = (accId: string) =>
    setSelectedAccountIds((prev) => {
      const next = new Set(prev);
      if (next.has(accId)) next.delete(accId);
      else next.add(accId);
      return next;
    });
  const clearAccountSelection = () => setSelectedAccountIds(new Set());

  const bulkUpdateAccounts = useMutation({
    mutationFn: async ({ ids, disabled }: { ids: string[]; disabled: boolean }) => {
      await Promise.all(ids.map((accId) => api.updateAccount(accId, { disabled })));
    },
    onSuccess: (_, { ids, disabled }) => {
      qc.invalidateQueries({ queryKey: ["accounts"] });
      clearAccountSelection();
      toast.success(
        `${ids.length} account${ids.length > 1 ? "s" : ""} ${disabled ? "disabled" : "enabled"}`,
        disabled ? "Selected accounts are paused and excluded from routing." : "Selected accounts are active again.",
      );
    },
    onError: (e: Error) => toast.error("Bulk update failed", e.message),
  });

  const bulkDeleteAccounts = useMutation({
    mutationFn: async (ids: string[]) => {
      await Promise.all(ids.map((accId) => api.deleteAccount(accId)));
    },
    onSuccess: (_, ids) => {
      qc.invalidateQueries({ queryKey: ["accounts"] });
      clearAccountSelection();
      setBulkDeleteConfirmOpen(false);
      toast.success(`${ids.length} account${ids.length > 1 ? "s" : ""} removed`, "Encrypted secrets have been purged.");
    },
    onError: (e: Error) => toast.error("Bulk removal failed", e.message),
  });

  const deleteProviderMut = useMutation({
    mutationFn: () => api.deleteCustomProvider(id!),
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: ["providers"] });
      qc.invalidateQueries({ queryKey: ["accounts"] });
      const detail = data?.accounts_disabled
        ? `${provider?.display_name} has been removed. ${data.accounts_disabled} bound account(s) disabled.`
        : `${provider?.display_name} has been removed.`;
      toast.success("Provider deleted", detail);
      navigate("/providers");
    },
    onError: (e: Error) => toast.error("Failed to delete provider", e.message),
  });

  // Sort accounts by priority for display, then keep the visible list bounded.
  // Selection remains global so bulk actions can span multiple pages.
  const sortedAccounts = [...myAccounts].sort((a, b) => a.priority - b.priority);
  const ACCOUNTS_PER_PAGE = 8;
  const accountPagination = useClientPagination(sortedAccounts, ACCOUNTS_PER_PAGE);
  const { page: accountPage, pages: accountPages, paged: paginatedAccounts, setPage: setAccountPage } = accountPagination;
  const accountPageStart = (accountPage - 1) * ACCOUNTS_PER_PAGE;
  const disabledModelIds = new Set(disabledModels.data?.ids ?? []);

  useEffect(() => {
    setAccountPage(1);
  }, [id, setAccountPage]);

  // Derived selection state (scoped globally for actions, locally for the
  // current-page selection control).
  const selectedList = sortedAccounts.filter((account) => selectedAccountIds.has(account.id));
  const selectedOnPage = paginatedAccounts.filter((account) => selectedAccountIds.has(account.id));
  const allPageAccountsSelected = paginatedAccounts.length > 0 && selectedOnPage.length === paginatedAccounts.length;
  const somePageAccountsSelected = selectedOnPage.length > 0 && !allPageAccountsSelected;
  const bulkBusy = bulkUpdateAccounts.isPending || bulkDeleteAccounts.isPending;

  const toggleSelectPageAccounts = () => {
    setSelectedAccountIds((previous) => {
      const next = new Set(previous);
      if (allPageAccountsSelected) paginatedAccounts.forEach((account) => next.delete(account.id));
      else paginatedAccounts.forEach((account) => next.add(account.id));
      return next;
    });
  };
  const handleBulkDisable = () => {
    const ids = selectedList.filter((a) => !a.disabled).map((a) => a.id);
    if (ids.length) bulkUpdateAccounts.mutate({ ids, disabled: true });
  };
  const handleBulkEnable = () => {
    const ids = selectedList.filter((a) => a.disabled).map((a) => a.id);
    if (ids.length) bulkUpdateAccounts.mutate({ ids, disabled: false });
  };
  const handleBulkDeleteAccounts = () => {
    if (!selectedList.length) return;
    setBulkDeleteConfirmOpen(true);
  };
  const confirmBulkDeleteAccounts = () => {
    const ids = selectedList.map((a) => a.id);
    if (!ids.length) return;
    bulkDeleteAccounts.mutate(ids);
  };

  // runTestAll tests every account sequentially (one at a time), updating each
  // row's status as it goes, then summarizes the outcome. Failures don't stop
  // the run — every account is checked.
  const runTestAll = async () => {
    if (testingAll || sortedAccounts.length === 0) return;
    setTestingAll(true);
    let ok = 0;
    let failed = 0;
    for (const a of sortedAccounts) {
      const success = await runTest(a.id);
      if (success) ok++;
      else failed++;
    }
    setTestingAll(false);
    if (failed === 0) {
      toast.success("All accounts verified", `${ok} account${ok === 1 ? "" : "s"} passed the connection test.`);
    } else {
      toast.error("Some checks failed", `${ok} ok, ${failed} failed.`);
    }
  };

  const moveAccount = (accId: string, direction: "up" | "down") => {
    const idx = sortedAccounts.findIndex((a) => a.id === accId);
    if (idx < 0) return;
    const target = direction === "up" ? idx - 1 : idx + 1;
    if (target < 0 || target >= sortedAccounts.length) return;
    const swapFrom = sortedAccounts[idx];
    const swapTo = sortedAccounts[target];
    // Optimistically swap priorities in the query cache for instant UI.
    qc.setQueryData<{ accounts: Account[] }>(["accounts"], (old) => {
      if (!old) return old;
      return {
        accounts: old.accounts.map((a) => {
          if (a.id === swapFrom.id) return { ...a, priority: swapTo.priority };
          if (a.id === swapTo.id) return { ...a, priority: swapFrom.priority };
          return a;
        }),
      };
    });
    // Persist both swaps to backend, refetch on settle.
    updateAccount.mutate({ id: swapFrom.id, patch: { priority: swapTo.priority } });
    updateAccount.mutate({ id: swapTo.id, patch: { priority: swapFrom.priority } }, {
      onSettled: () => qc.invalidateQueries({ queryKey: ["accounts"] }),
    });
  };

  if (providers.isLoading) return <Spinner />;
  if (!provider) {
    return (
      <Card className="px-6 py-12 text-center">
        <p className="text-sm text-[var(--text-muted)]">Provider not found.</p>
        <Link to="/providers" className="mt-3 inline-block text-sm font-medium text-accent-600">
          Back to Providers
        </Link>
      </Card>
    );
  }

  const isKiro = provider.id === "kiro";
  const isQoder = provider.id === "qoder";
  const isKilocode = provider.id === "kilocode";
  const isCodebuddy = provider.id === "codebuddy";
  const isKimchi = provider.id === "kimchi";
  const isCursor = provider.id === "cursor";
  const isCommandCode = provider.id === "commandcode";
  const hasCustomModal = isKiro || isQoder || isKilocode || isCodebuddy || isKimchi || isCursor || isCommandCode;
  const supportsManualConnect = !hasCustomModal && (
    provider.auth_modes.includes("api_key") ||
    provider.auth_modes.includes("none") ||
    !oauthProvider
  );
  // Bulk key upload applies to providers authenticated by API key. It is hidden
  // for Azure (each key needs its own endpoint + deployment, so there is no
  // shared config to bulk against) and for no-auth providers (nothing to bulk).
  const providerSupportsApiKey = provider.auth_modes.includes("api_key") || provider.auth_kind === "api_key";
  const supportsBulkUpload = supportsManualConnect && providerSupportsApiKey && provider.id !== "azure";
  const enabledAccounts = myAccounts.filter((account) => !account.disabled).length;
  const activeModelCount = Math.max(0, modelList.length - disabledModelIds.size);
  const hasPrimaryConnect = hasCustomModal || !!oauthProvider || supportsManualConnect;
  const hasAlternativeManualConnect = !hasCustomModal && !!oauthProvider && supportsManualConnect;
  const primaryConnectLabel = hasCustomModal
    ? `Connect ${provider.display_name}`
    : oauthProvider
      ? `Connect ${provider.display_name}`
      : provider.auth_kind === "none"
        ? "Enable provider"
        : "Add API key";
  const openPrimaryConnect = () => {
    if (isKiro) setKiroOpen(true);
    else if (isQoder) setQoderOpen(true);
    else if (isKilocode) setKilocodeOpen(true);
    else if (isCodebuddy) setCodebuddyOpen(true);
    else if (isKimchi) setKimchiOpen(true);
    else if (isCursor) setCursorOpen(true);
    else if (isCommandCode) setCommandcodeOpen(true);
    else if (oauthProvider) setOauthOpen(true);
    else if (supportsManualConnect) setAddKeyOpen(true);
  };

  return (
    <>
      <nav aria-label="Breadcrumb" className="mb-4 flex items-center gap-2 text-sm text-[var(--text-muted)]">
        <Link
          to="/providers"
          className="inline-flex min-h-9 items-center gap-2 rounded-lg px-1 font-medium transition-colors hover:text-[var(--text)] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50"
        >
          <ArrowLeft className="h-4 w-4" />
          Providers
        </Link>
        <span aria-hidden="true" className="text-[var(--border-strong)]">/</span>
        <span className="truncate text-[var(--text)]">{provider.display_name}</span>
      </nav>

      <Card className="mb-8">
        <div className="flex flex-col gap-6 p-5 sm:p-6 lg:flex-row lg:items-start lg:justify-between">
          <div className="flex min-w-0 items-start gap-4 sm:gap-5">
            <div className="rounded-2xl border border-[var(--border)] bg-[var(--bg-subtle)] p-2 shadow-sm">
              <ProviderIcon provider={provider} size={56} />
            </div>
            <div className="min-w-0 pt-0.5">
              <div className="flex flex-wrap items-center gap-2">
                <h1 className="font-display text-2xl font-semibold tracking-tight sm:text-3xl">
                  {provider.display_name}
                </h1>
                {provider.deprecated && <Badge tone="warning">Limited support</Badge>}
                {provider.auth_kind === "none" && <Badge tone="success">No credentials required</Badge>}
              </div>
              <p className="mt-1.5 max-w-2xl text-sm leading-6 text-[var(--text-muted)]">
                Manage credentials, routing behavior, and the model catalog used for requests to this provider.
              </p>
              <div className="mt-3 flex flex-wrap gap-1.5">
                {(provider.service_kinds ?? []).map((kind) => (
                  <Badge key={kind} tone="accent">{kind}</Badge>
                ))}
                <Badge tone="neutral">{provider.auth_kind === "none" ? "Public endpoint" : provider.auth_kind === "oauth" ? "OAuth" : "API key"}</Badge>
                {provider.custom && <Badge tone="secondary">Custom provider</Badge>}
              </div>
              {provider.custom && provider.base_url && (
                <div className="mt-3">
                  <BaseURLDisplay baseURL={provider.base_url} dialect={provider.dialect} />
                </div>
              )}
            </div>
          </div>

          <div className="flex shrink-0 flex-wrap items-center gap-2 lg:justify-end">
            {hasAlternativeManualConnect && (
              <Button variant="ghost" onClick={() => setAddKeyOpen(true)}>
                <Plus className="h-4 w-4" />
                Add API key
              </Button>
            )}
            {hasPrimaryConnect && (
              <Button onClick={openPrimaryConnect}>
                <Plug className="h-4 w-4" />
                {primaryConnectLabel}
              </Button>
            )}
            {provider.custom && (
              <Button
                variant="danger"
                onClick={() => setDeleteProviderOpen(true)}
                title="Permanently delete this custom provider"
              >
                <Trash2 className="h-4 w-4" />
                Delete
              </Button>
            )}
          </div>
        </div>

        <div className="grid grid-cols-1 border-t border-[var(--border)] bg-[var(--bg-subtle)] sm:grid-cols-3 sm:divide-x sm:divide-[var(--border)]">
          <div className="px-5 py-4 sm:px-6">
            <p className="text-xs font-medium uppercase tracking-wider text-[var(--text-muted)]">Connected accounts</p>
            <p className="mt-1 text-xl font-semibold tabular-nums">{myAccounts.length}</p>
          </div>
          <div className="border-t border-[var(--border)] px-5 py-4 sm:border-t-0 sm:px-6">
            <p className="text-xs font-medium uppercase tracking-wider text-[var(--text-muted)]">Enabled accounts</p>
            <p className="mt-1 text-xl font-semibold tabular-nums">{enabledAccounts}</p>
          </div>
          <div className="border-t border-[var(--border)] px-5 py-4 sm:border-t-0 sm:px-6">
            <p className="text-xs font-medium uppercase tracking-wider text-[var(--text-muted)]">Enabled models</p>
            <p className="mt-1 text-xl font-semibold tabular-nums">{models.isLoading ? "—" : activeModelCount}</p>
          </div>
        </div>

        {provider.deprecated && provider.notice && (
          <div className="flex items-start gap-3 border-t border-[color:var(--color-warning)]/25 bg-[color:var(--color-warning)]/8 px-5 py-4 text-sm leading-6 text-[color:var(--color-warning)] sm:px-6">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
            <span>{provider.notice}</span>
          </div>
        )}
      </Card>

      <div className="space-y-6">
        {routing.data && (
          <Card>
            <CardHeader
              title="Routing policy"
              description="Choose how requests are distributed across healthy accounts for this provider."
            />
            <RoutingControls
              settings={routing.data}
              saving={updateRouting.isPending}
              onUpdate={(patch) => updateRouting.mutate(patch)}
            />
          </Card>
        )}

        <Card>
          <CardHeader
            title="Connected accounts"
            description="Connected upstream credentials and their routing configuration."
            action={
              <div className="flex flex-wrap items-center justify-end gap-2">
                {myAccounts.length > 0 && (
                  <Button
                    variant="ghost"
                    onClick={runTestAll}
                    disabled={testingAll}
                  >
                    {testingAll ? <Loader2 className="h-4 w-4 animate-spin" /> : <CheckCircle className="h-4 w-4" />}
                    {testingAll
                      ? `Testing ${Object.values(testResults).filter((result) => result.status !== "testing").length}/${myAccounts.length}`
                      : "Test all"}
                  </Button>
                )}
                {supportsBulkUpload && (
                  <Button variant="ghost" onClick={() => setBulkOpen(true)}>
                    <Layers className="h-4 w-4" />
                    Import keys
                  </Button>
                )}
              </div>
            }
          />
          {myAccounts.some((a) => a.needs_reconnect) && (
            <div className="flex items-start gap-2.5 border-t border-[color:var(--color-warning)]/25 bg-[color:var(--color-warning)]/8 px-6 py-3 text-xs leading-relaxed text-[color:var(--color-warning)]">
              <RefreshCw className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>
                One or more accounts have a revoked OAuth token and cannot be refreshed.
                Delete the affected account and reconnect to restore access.
              </span>
            </div>
          )}
          {accounts.isLoading ? (
            <Spinner />
          ) : !myAccounts.length ? (
            <EmptyState
              title="No accounts yet"
              hint="Add an account to start routing through this provider."
            />
          ) : (
            <>
              {selectedList.length > 0 ? (
                <div className="sticky top-0 z-10 flex flex-wrap items-center gap-2 border-t border-accent-200 bg-accent-50 px-4 py-3 shadow-sm dark:border-accent-800 dark:bg-accent-900/30 sm:px-5">
                  <label className="flex cursor-pointer items-center gap-2 text-sm font-medium text-accent-800 dark:text-accent-200">
                    <input
                      type="checkbox"
                      aria-label="Select accounts on this page"
                      className="h-4 w-4 rounded border-[var(--border)] accent-[var(--color-accent-500)]"
                      checked={allPageAccountsSelected}
                      ref={(element) => { if (element) element.indeterminate = somePageAccountsSelected; }}
                      onChange={toggleSelectPageAccounts}
                    />
                    {selectedList.length} selected
                  </label>
                  <div className="hidden flex-1 sm:block" />
                  <Button variant="ghost" onClick={handleBulkEnable} disabled={bulkBusy}>
                    <ToggleRight className="h-4 w-4 text-emerald-600" />
                    Enable
                  </Button>
                  <Button variant="ghost" onClick={handleBulkDisable} disabled={bulkBusy}>
                    <ToggleLeft className="h-4 w-4" />
                    Disable
                  </Button>
                  <Button variant="danger" onClick={handleBulkDeleteAccounts} disabled={bulkBusy}>
                    <Trash2 className="h-4 w-4" />
                    Delete
                  </Button>
                  <Button variant="ghost" onClick={clearAccountSelection} disabled={bulkBusy}>
                    Clear
                  </Button>
                </div>
              ) : (
                <div className="flex items-center justify-between border-t border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-2.5 sm:px-5">
                  <span className="text-xs text-[var(--text-muted)]">
                    {sortedAccounts.length} connected {sortedAccounts.length === 1 ? "account" : "accounts"}
                  </span>
                  <label className="flex cursor-pointer items-center gap-2 rounded-lg px-2 py-1.5 text-xs font-medium text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-elevated)] hover:text-[var(--text)]">
                    <input
                      type="checkbox"
                      aria-label="Select accounts on this page"
                      className="h-4 w-4 rounded border-[var(--border)] accent-[var(--color-accent-500)]"
                      checked={allPageAccountsSelected}
                      onChange={toggleSelectPageAccounts}
                    />
                    Select page
                  </label>
                </div>
              )}
              <div className="divide-y divide-[var(--border)]">
                {paginatedAccounts.map((account, pageIndex) => (
                  <AccountRow
                    key={account.id}
                    account={account}
                    index={accountPageStart + pageIndex}
                    total={sortedAccounts.length}
                    pools={pools.data?.pools ?? []}
                    selected={selectedAccountIds.has(account.id)}
                    onToggleSelect={() => toggleAccountSelection(account.id)}
                    onDelete={() => remove.mutate(account.id)}
                    onMoveUp={() => moveAccount(account.id, "up")}
                    onMoveDown={() => moveAccount(account.id, "down")}
                    onTest={() => runTest(account.id)}
                    onUpdateProxy={(patch) => updateAccount.mutate({ id: account.id, patch })}
                    testResult={testResults[account.id]}
                    disabledByBatch={testingAll}
                  />
                ))}
              </div>
              <TablePagination
                page={accountPage}
                pages={accountPages}
                total={sortedAccounts.length}
                onPage={setAccountPage}
              />
            </>
          )}
        </Card>

        {/* Available Models */}
        {models.data && (
          <Card>
            <CardHeader
              title="Model catalog"
              description={`${activeModelCount} of ${modelList.length} model${modelList.length === 1 ? "" : "s"} enabled in this catalog.`}
              action={
                provider?.custom ? (
                  <Button
                    variant="secondary"
                    disabled={importModelsMut.isPending}
                    onClick={() => importModelsMut.mutate()}
                    title="Fetch the upstream /models listing and register each model"
                  >
                    {importModelsMut.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Download className="h-4 w-4" />}
                    {importModelsMut.isPending ? "Fetching models…" : "Sync from /models"}
                  </Button>
                ) : undefined
              }
            />
            {modelList.length > 0 && (
            <div className="flex flex-col gap-3 border-t border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-4 sm:px-6 lg:flex-row lg:items-center lg:justify-between">
              <div className="relative w-full lg:max-w-md">
                <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--text-muted)]" />
                <Input
                  aria-label="Search provider models"
                  placeholder="Search by model name, ID, or capability…"
                  value={modelSearchQuery}
                  onChange={(event) => setModelSearchQuery(event.target.value)}
                  className="pl-10"
                />
              </div>
              <div className="flex min-h-10 flex-wrap items-center gap-2">
                <label className="flex cursor-pointer items-center gap-2 rounded-lg px-2 py-2 text-sm text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-elevated)] hover:text-[var(--text)]">
                  <input
                    type="checkbox"
                    className="h-4 w-4 rounded border-[var(--border)] accent-[var(--color-accent-500)]"
                    checked={filteredModels.length > 0 && filteredModels.every((model) => selectedModelIds.has(model.id))}
                    ref={(element) => {
                      if (element) {
                        const someSelected = filteredModels.some((model) => selectedModelIds.has(model.id));
                        const allSelected = filteredModels.length > 0 && filteredModels.every((model) => selectedModelIds.has(model.id));
                        element.indeterminate = someSelected && !allSelected;
                      }
                    }}
                    onChange={(event) => {
                      setSelectedModelIds((previous) => {
                        const next = new Set(previous);
                        if (event.target.checked) filteredModels.forEach((model) => next.add(model.id));
                        else filteredModels.forEach((model) => next.delete(model.id));
                        return next;
                      });
                    }}
                  />
                  Select all
                </label>
                {selectedModelIds.size > 0 && (
                  <div className="flex flex-wrap items-center gap-2 rounded-xl border border-accent-200 bg-accent-50 p-1.5 dark:border-accent-800 dark:bg-accent-900/30">
                    <span className="px-1.5 text-xs font-medium text-accent-800 dark:text-accent-200">
                      {selectedModelIds.size} selected
                    </span>
                    <Button
                      variant="ghost"
                      onClick={() => enableModelsMut.mutate([...selectedModelIds])}
                      disabled={enableModelsMut.isPending}
                    >
                      <ToggleRight className="h-4 w-4 text-emerald-600" />
                      Enable
                    </Button>
                    <Button
                      variant="ghost"
                      onClick={() => disableModelsMut.mutate([...selectedModelIds])}
                      disabled={disableModelsMut.isPending}
                    >
                      <ToggleLeft className="h-4 w-4" />
                      Disable
                    </Button>
                    <Button variant="ghost" onClick={() => setSelectedModelIds(new Set())}>
                      Clear
                    </Button>
                  </div>
                )}
              </div>
            </div>
            )}
            {filteredModels.length === 0 ? (
              <div className="px-6 py-12 text-center text-sm text-[var(--text-muted)] border-t border-[var(--border)]">
                {modelList.length === 0 ? (
                  <div className="flex flex-col items-center gap-3">
                    <span>No models configured yet.</span>
                    {provider?.custom && (
                      <Button
                        variant="secondary"
                        className="h-8 px-3 text-xs"
                        disabled={importModelsMut.isPending}
                        onClick={() => importModelsMut.mutate()}
                      >
                        <Download className="h-3.5 w-3.5" />
                        {importModelsMut.isPending ? "Fetching…" : "Fetch from /models"}
                      </Button>
                    )}
                  </div>
                ) : (
                  <>No models found matching "{modelSearchQuery}"</>
                )}
              </div>
            ) : (
              <div className="grid grid-cols-1 gap-3 border-t border-[var(--border)] bg-[var(--bg-subtle)] p-4 sm:grid-cols-2 sm:p-5 xl:grid-cols-3">
                {paginatedModels.map((m) => (
                  <ModelCell
                    key={m.id}
                    model={m}
                    provider={provider}
                    disabled={disabledModelIds.has(m.id)}
                    selected={selectedModelIds.has(m.id)}
                    onToggleSelect={() => toggleModelSelection(m.id)}
                    onToggleDisable={() => {
                      if (disabledModelIds.has(m.id)) {
                        enableModelsMut.mutate([m.id]);
                      } else {
                        disableModelsMut.mutate([m.id]);
                      }
                    }}
                  />
                ))}
              </div>
            )}
            {totalModelPages > 0 && (
              <div className="flex items-center justify-between rounded-b-2xl border-t border-[var(--border)] bg-[var(--bg-subtle)] px-6 py-3">
                <span className="text-xs text-[var(--text-muted)]">
                  Showing {(modelPage - 1) * MODELS_PER_PAGE + 1} to {Math.min(modelPage * MODELS_PER_PAGE, filteredModels.length)} of {filteredModels.length} models
                </span>
                <div className="flex items-center gap-1">
                  <Button
                    variant="ghost"
                    className="h-8 px-2 text-xs"
                    disabled={modelPage === 1}
                    onClick={() => setModelPage((p) => p - 1)}
                  >
                    Previous
                  </Button>
                  <Button
                    variant="ghost"
                    className="h-8 px-2 text-xs"
                    disabled={modelPage === totalModelPages}
                    onClick={() => setModelPage((p) => p + 1)}
                  >
                    Next
                  </Button>
                </div>
              </div>
            )}
          </Card>
        )}

        {/* User-registered custom models (separate from the catalog list). */}
        <CustomModelsSection provider={provider} />
      </div>

      {oauthOpen && oauthProvider && (

        <ConnectModal provider={oauthProvider} onClose={() => setOauthOpen(false)} />
      )}
      {kiroOpen && <KiroConnectModal onClose={() => setKiroOpen(false)} />}
      {qoderOpen && <QoderConnectModal onClose={() => setQoderOpen(false)} />}
      {kilocodeOpen && <KilocodeConnectModal onClose={() => setKilocodeOpen(false)} />}
      {codebuddyOpen && <CodebuddyConnectModal onClose={() => setCodebuddyOpen(false)} />}
      {kimchiOpen && <KimchiConnectModal onClose={() => setKimchiOpen(false)} />}
      {cursorOpen && <CursorConnectModal onClose={() => setCursorOpen(false)} />}
      {commandcodeOpen && <CommandCodeConnectModal onClose={() => setCommandcodeOpen(false)} />}
      {addKeyOpen && (
        <AddApiKeyModal
          provider={provider}
          hasRegions={hasRegions}
          label={label}
          apiKey={apiKey}
          baseURL={baseURL}
          region={region}
          accountID={accountID}
          azureEndpoint={azureEndpoint}
          azureDeployment={azureDeployment}
          azureAPIVersion={azureAPIVersion}
          azureOrganization={azureOrganization}
          error={error}
          pending={create.isPending}
          onLabel={setLabel}
          onApiKey={setApiKey}
          onBaseURL={setBaseURL}
          onRegion={setRegion}
          onAccountID={setAccountID}
          onAzureEndpoint={setAzureEndpoint}
          onAzureDeployment={setAzureDeployment}
          onAzureAPIVersion={setAzureAPIVersion}
          onAzureOrganization={setAzureOrganization}
          onSubmit={() => create.mutate()}
          onClose={() => { setAddKeyOpen(false); setError(""); }}
        />
      )}
      {bulkOpen && (
        <BulkAddKeysModal provider={provider} onClose={() => setBulkOpen(false)} />
      )}
      <Modal
        open={bulkDeleteConfirmOpen}
        onClose={() => { if (!bulkDeleteAccounts.isPending) setBulkDeleteConfirmOpen(false); }}
        title="Delete selected accounts?"
        subtitle={`${selectedList.length} account${selectedList.length > 1 ? "s" : ""} on ${label} will be removed.`}
        maxWidth="max-w-md"
      >
        <div className="space-y-4 px-6 py-5">
          <div className="flex items-start gap-3 rounded-xl border border-[color:var(--color-danger)]/30 bg-[color:var(--color-danger)]/10 px-3.5 py-3">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-[color:var(--color-danger)]" strokeWidth={2} />
            <div className="text-sm leading-snug text-[color:var(--color-danger)]">
              This permanently purges each account's encrypted secrets and removes them from routing.
              <span className="font-semibold"> This action cannot be undone.</span>
            </div>
          </div>
          {selectedList.length > 0 && (
            <ul className="max-h-40 space-y-1 overflow-y-auto rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] px-3.5 py-2.5">
              {selectedList.map((a) => (
                <li key={a.id} className="flex items-center gap-2 text-sm text-[var(--text)]">
                  <Trash2 className="h-3.5 w-3.5 shrink-0 text-[var(--text-muted)]" />
                  <span className="truncate">{a.label || a.id}</span>
                </li>
              ))}
            </ul>
          )}
          <div className="flex justify-end gap-2">
            <Button
              variant="ghost"
              onClick={() => setBulkDeleteConfirmOpen(false)}
              disabled={bulkDeleteAccounts.isPending}
            >
              Cancel
            </Button>
            <Button
              variant="danger"
              onClick={confirmBulkDeleteAccounts}
              disabled={bulkDeleteAccounts.isPending}
            >
              {bulkDeleteAccounts.isPending ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <Trash2 className="h-3.5 w-3.5" />
              )}
              Delete {selectedList.length} account{selectedList.length > 1 ? "s" : ""}
            </Button>
          </div>
        </div>
      </Modal>

      {/* Delete custom provider confirmation */}
      <Modal
        open={deleteProviderOpen}
        onClose={() => { if (!deleteProviderMut.isPending) setDeleteProviderOpen(false); }}
        title={`Delete ${provider.display_name}?`}
        subtitle="This removes the custom provider, its custom models, and disables all bound accounts."
        maxWidth="max-w-md"
      >
        <div className="space-y-4 px-6 py-5">
          <div className="flex items-start gap-3 rounded-xl border border-[color:var(--color-danger)]/30 bg-[color:var(--color-danger)]/10 px-3.5 py-3">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-[color:var(--color-danger)]" strokeWidth={2} />
            <div className="text-sm leading-snug text-[color:var(--color-danger)]">
              Its accounts and custom models will no longer be routable.
              <span className="font-semibold"> This action cannot be undone.</span>
            </div>
          </div>
          <div className="flex justify-end gap-2">
            <Button
              variant="ghost"
              onClick={() => setDeleteProviderOpen(false)}
              disabled={deleteProviderMut.isPending}
            >
              Cancel
            </Button>
            <Button
              variant="danger"
              onClick={() => deleteProviderMut.mutate()}
              disabled={deleteProviderMut.isPending}
            >
              {deleteProviderMut.isPending ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <Trash2 className="h-3.5 w-3.5" />
              )}
              Delete provider
            </Button>
          </div>
        </div>
      </Modal>
    </>
  );
}

// BaseURLDisplay shows the upstream base URL for a user-defined custom
// provider (OpenAI- or Anthropic-compatible) on the provider detail header,
// with a one-click copy affordance. Hidden for built-in providers whose base
// URL is fixed and not user-configurable.
function BaseURLDisplay({ baseURL, dialect }: { baseURL: string; dialect?: string }) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(baseURL);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard may be unavailable (insecure context); silently ignore.
    }
  };

  const dialectLabel =
    dialect === "anthropic"
      ? "Anthropic-compatible"
      : dialect === "openai"
        ? "OpenAI-compatible"
        : dialect;

  return (
    <div className="mt-3 flex flex-wrap items-center gap-2">
      <div className="inline-flex max-w-full items-center gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] px-3 py-1.5">
        <Globe className="h-3.5 w-3.5 shrink-0 text-[var(--text-muted)]" />
        <span className="text-xs font-medium text-[var(--text-muted)]">Base URL</span>
        <code className="truncate font-mono text-xs text-[var(--text)]" title={baseURL}>
          {baseURL}
        </code>
        <button
          type="button"
          onClick={copy}
          title="Copy base URL"
          className="shrink-0 rounded-md p-1 text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-elevated)] hover:text-[var(--text)]"
        >
          {copied ? (
            <Check className="h-3.5 w-3.5 text-[color:var(--color-success)]" />
          ) : (
            <Copy className="h-3.5 w-3.5" />
          )}
        </button>
      </div>
      {dialectLabel && (
        <Badge tone="neutral">{dialectLabel}</Badge>
      )}
    </div>
  );
}

const routingOptions = [
  { value: "inherit", label: "Inherit" },
  { value: "fill-first", label: "Fill first" },
  { value: "round-robin", label: "Round robin" },
  { value: "smart-round-robin", label: "Smart" },
];

function RoutingControls({
  settings,
  saving,
  onUpdate,
}: {
  settings: ProviderRoutingSettings;
  saving: boolean;
  onUpdate: (patch: Partial<ProviderRoutingSettings>) => void;
}) {
  const mode = settings?.routing_strategy || "inherit";
  const stickyLimit = settings?.sticky_limit || 3;
  const ttlHours = Math.max(1, Math.round((settings?.affinity_ttl_minutes || 1440) / 60));
  const rotatesAccounts = mode === "round-robin" || mode === "smart-round-robin";

  const routingDescription = mode === "inherit"
    ? "Use the router-wide account strategy."
    : mode === "fill-first"
      ? "Keep using the highest-priority healthy account until it is unavailable."
      : mode === "round-robin"
        ? "Rotate across healthy accounts after the configured request window."
        : "Preserve client affinity while balancing traffic across healthy accounts.";

  return (
    <div className="grid gap-4 p-5 sm:p-6 lg:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_minmax(0,1fr)]">
      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4">
        <label htmlFor="provider-routing-strategy" className="flex items-center gap-2 text-sm font-semibold">
          <Route className="h-4 w-4 text-accent-600" />
          Distribution strategy
        </label>
        <p className="mt-1 min-h-10 text-xs leading-5 text-[var(--text-muted)]">{routingDescription}</p>
        <select
          id="provider-routing-strategy"
          value={mode}
          disabled={saving}
          onChange={(event) => onUpdate({ routing_strategy: event.target.value })}
          className="mt-3 h-10 w-full rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] px-3 text-sm text-[var(--text)] outline-none transition-colors focus:border-accent-400 focus-visible:ring-2 focus-visible:ring-accent-400/40 disabled:cursor-not-allowed disabled:opacity-60"
        >
          {routingOptions.map((option) => (
            <option key={option.value} value={option.value}>{option.label}</option>
          ))}
        </select>
      </div>

      <div className={`rounded-xl border border-[var(--border)] p-4 ${rotatesAccounts ? "bg-[var(--bg-subtle)]" : "bg-[var(--bg-subtle)]/50 opacity-60"}`}>
        <label htmlFor="provider-sticky-limit" className="text-sm font-semibold">Requests per account</label>
        <p className="mt-1 min-h-10 text-xs leading-5 text-[var(--text-muted)]">Requests kept on one account before rotating.</p>
        <Input
          id="provider-sticky-limit"
          type="number"
          min={1}
          max={100}
          value={stickyLimit}
          disabled={saving || !rotatesAccounts}
          onChange={(event) => onUpdate({ sticky_limit: parseInt(event.target.value, 10) || 1 })}
          className="mt-3"
        />
      </div>

      <div className={`rounded-xl border border-[var(--border)] p-4 ${mode === "smart-round-robin" ? "bg-[var(--bg-subtle)]" : "bg-[var(--bg-subtle)]/50 opacity-60"}`}>
        <label htmlFor="provider-affinity-ttl" className="text-sm font-semibold">Affinity lifetime</label>
        <p className="mt-1 min-h-10 text-xs leading-5 text-[var(--text-muted)]">Hours before a client can be assigned to another account.</p>
        <div className="relative mt-3">
          <Input
            id="provider-affinity-ttl"
            type="number"
            min={1}
            max={168}
            value={ttlHours}
            disabled={saving || mode !== "smart-round-robin"}
            onChange={(event) => onUpdate({ affinity_ttl_minutes: (parseInt(event.target.value, 10) || 1) * 60 })}
            className="pr-14"
          />
          <span className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-xs text-[var(--text-muted)]">hours</span>
        </div>
      </div>
    </div>
  );
}

function AccountRow({
  account: a,
  index,
  total,
  pools,
  selected,
  onToggleSelect,
  onDelete,
  onMoveUp,
  onMoveDown,
  onTest,
  onUpdateProxy,
  testResult,
  disabledByBatch,
}: {
  account: Account;
  index: number;
  total: number;
  pools: ProxyPool[];
  selected?: boolean;
  onToggleSelect?: () => void;
  onDelete: () => void;
  onMoveUp: () => void;
  onMoveDown: () => void;
  onTest: () => void;
  onUpdateProxy: (patch: { priority?: number; proxy_pool_id?: string; disabled?: boolean }) => void;
  testResult?: { status: "testing" | "ok" | "error"; message?: string };
  disabledByBatch?: boolean;
}) {
  const testing = testResult?.status === "testing";
  const [localPriority, setLocalPriority] = useState(a.priority);
  const priorityRef = useRef(a.priority);

  // Keep local priority in sync when account data changes from server.
  if (a.priority !== priorityRef.current) {
    priorityRef.current = a.priority;
    setLocalPriority(a.priority);
  }

  const commitPriority = () => {
    const val = localPriority;
    if (!isNaN(val) && val >= 0 && val !== a.priority) {
      onUpdateProxy({ priority: val });
    }
  };

  const boundPool = pools.find((p) => p.id === a.proxy_pool_id);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const supportsKiroQuota = a.provider === "kiro";
  const hasExpandableDetails = supportsKiroQuota || a.provider === "codex";
  const accountQuota = useQuery({
    queryKey: ["account-quota", a.id],
    queryFn: () => api.accountQuota(a.id),
    enabled: supportsKiroQuota && detailsOpen && !a.disabled,
    staleTime: 60_000,
    retry: 1,
  });
  const quotaRows = accountQuota.data?.quotas ?? [];

  return (
    <div className={`px-3 py-2.5 transition-colors sm:px-4 ${a.disabled ? "bg-[var(--bg-subtle)]/50" : ""} ${selected ? "bg-accent-50/70 dark:bg-accent-900/15" : "hover:bg-[var(--bg-subtle)]/50"}`}>
      <div className="flex flex-wrap items-center gap-2">
        {onToggleSelect && (
          <input
            type="checkbox"
            checked={!!selected}
            onChange={onToggleSelect}
            aria-label={`Select ${a.label || a.provider}`}
            className="h-4 w-4 shrink-0 rounded border-[var(--border)] accent-[var(--color-accent-500)]"
          />
        )}

        <div className="min-w-36 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="truncate text-sm font-semibold" title={a.label || a.provider}>{a.label || a.provider}</span>
            <Badge tone="neutral">{a.auth_kind === "oauth" ? "OAuth" : "API key"}</Badge>
            {a.disabled && <Badge tone="danger">Disabled</Badge>}
            {a.needs_reconnect && (
              <Badge tone="warning" title="The OAuth token was revoked. Delete this account and reconnect.">
                <RefreshCw className="h-3 w-3" />
                Reconnect
              </Badge>
            )}
            {testResult?.status === "ok" && <Badge tone="success">Verified</Badge>}
            {testResult?.status === "error" && <Badge tone="danger" title={testResult.message}>Test failed</Badge>}
            {testResult?.status === "testing" && <Badge tone="neutral">Testing…</Badge>}
          </div>
        </div>

        <div className="order-3 flex w-full items-center gap-2 pl-6 lg:order-none lg:w-auto lg:pl-0">
          <div className="inline-flex shrink-0 items-center overflow-hidden rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)]" title="Routing priority">
            <button
              type="button"
              onClick={onMoveUp}
              disabled={index === 0}
              className="flex h-10 w-9 items-center justify-center text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-subtle)] disabled:cursor-not-allowed disabled:opacity-25"
              aria-label="Move account up"
            >
              <ArrowUp className="h-3.5 w-3.5" />
            </button>
            <input
              type="number"
              value={localPriority}
              onChange={(event) => {
                const value = parseInt(event.target.value, 10);
                if (!isNaN(value) && value >= 0) setLocalPriority(value);
              }}
              onBlur={commitPriority}
              onKeyDown={(event) => event.key === "Enter" && (event.target as HTMLInputElement).blur()}
              aria-label={`Priority for ${a.label || a.provider}`}
              className="h-10 w-10 border-x border-[var(--border)] bg-transparent text-center text-xs font-semibold text-[var(--text)] focus:bg-[var(--bg)] focus:outline-none [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
              min={0}
              max={999}
            />
            <button
              type="button"
              onClick={onMoveDown}
              disabled={index === total - 1}
              className="flex h-10 w-9 items-center justify-center text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-subtle)] disabled:cursor-not-allowed disabled:opacity-25"
              aria-label="Move account down"
            >
              <ArrowDown className="h-3.5 w-3.5" />
            </button>
          </div>

          <select
            value={a.proxy_pool_id || ""}
            onChange={(event) => onUpdateProxy({ proxy_pool_id: event.target.value || "" })}
            aria-label={`Proxy for ${a.label || a.provider}`}
            className="h-10 min-w-0 flex-1 rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-2 text-xs focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/40 lg:w-40 lg:flex-none"
          >
            <option value="">Direct connection</option>
            {pools.map((pool) => (
              <option key={pool.id} value={pool.id}>
                {pool.name}{!pool.is_active ? " (inactive)" : ""}
              </option>
            ))}
          </select>
          {boundPool && (
            <span className="hidden xl:inline-flex">
              <Badge tone={boundPool.test_status === "active" ? "success" : boundPool.test_status === "error" ? "danger" : "neutral"}>
                {boundPool.test_status === "active" ? "Proxy healthy" : boundPool.test_status === "error" ? "Proxy error" : "Proxy unknown"}
              </Badge>
            </span>
          )}
        </div>

        <div className="flex shrink-0 items-center gap-0.5">
          <button
            type="button"
            onClick={onTest}
            disabled={testing || disabledByBatch}
            className="flex h-10 w-10 items-center justify-center rounded-lg text-[var(--text-muted)] transition-[transform,background-color,color] duration-150 hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] active:scale-[0.96] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50 disabled:cursor-not-allowed disabled:opacity-40 disabled:active:scale-100"
            title="Test account connection"
            aria-label={`Test ${a.label || a.provider}`}
          >
            {testing ? <Loader2 className="h-4 w-4 animate-spin" /> : <CheckCircle className="h-4 w-4" />}
          </button>
          <button
            type="button"
            onClick={() => onUpdateProxy({ disabled: !a.disabled })}
            className="flex h-10 w-10 items-center justify-center rounded-lg text-[var(--text-muted)] transition-[transform,background-color,color] duration-150 hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] active:scale-[0.96] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50"
            title={a.disabled ? "Enable account" : "Disable account"}
            aria-label={a.disabled ? `Enable ${a.label || a.provider}` : `Disable ${a.label || a.provider}`}
          >
            {a.disabled ? <ToggleLeft className="h-4 w-4" /> : <ToggleRight className="h-4 w-4 text-emerald-600" />}
          </button>
          <button
            type="button"
            onClick={onDelete}
            className="flex h-10 w-10 items-center justify-center rounded-lg text-[var(--text-muted)] transition-[transform,background-color,color] duration-150 hover:bg-[color:var(--color-danger)]/10 hover:text-[color:var(--color-danger)] active:scale-[0.96] focus:outline-none focus-visible:ring-2 focus-visible:ring-[color:var(--color-danger)]/40"
            title="Delete account"
            aria-label={`Delete ${a.label || a.provider}`}
          >
            <Trash2 className="h-4 w-4" />
          </button>
        </div>
      </div>

      {testResult?.status === "error" && testResult.message && (
        <div className="ml-6 mt-2 flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-3 py-2 dark:border-red-900/40 dark:bg-red-900/15">
          <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-red-500 dark:text-red-400" />
          <p className="break-words text-xs leading-5 text-red-700 dark:text-red-300">{testResult.message}</p>
        </div>
      )}

      {hasExpandableDetails && (
        <button
          type="button"
          onClick={() => setDetailsOpen((open) => !open)}
          aria-expanded={detailsOpen}
          className="ml-6 mt-1 inline-flex h-10 items-center gap-1.5 rounded-lg px-2 text-xs font-medium text-[var(--text-muted)] transition-[transform,background-color,color] duration-150 hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] active:scale-[0.96] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50"
        >
          <Zap className="h-3.5 w-3.5" />
          {a.provider === "codex"
            ? "Usage & reset limits"
            : accountQuota.data?.plan_name || "Package & usage"}
          <ChevronDown className={`h-3.5 w-3.5 transition-transform ${detailsOpen ? "rotate-180" : ""}`} />
        </button>
      )}

      {detailsOpen && (
        <div className="ml-6 mt-2">
          {supportsKiroQuota && (
            <KiroQuotaPanel
              loading={accountQuota.isLoading || accountQuota.isFetching}
              error={accountQuota.error instanceof Error ? accountQuota.error.message : ""}
              planName={accountQuota.data?.plan_name}
              message={accountQuota.data?.message}
              quotas={quotaRows}
              disabled={a.disabled}
              onRefresh={() => accountQuota.refetch()}
            />
          )}
          {a.provider === "codex" && <CodexResetCreditsSection accountId={a.id} />}
        </div>
      )}
    </div>
  );
}

function KiroQuotaPanel({
  loading,
  error,
  planName,
  message,
  quotas,
  disabled,
  onRefresh,
}: {
  loading: boolean;
  error: string;
  planName?: string;
  message?: string;
  quotas: UpstreamQuota[];
  disabled: boolean;
  onRefresh: () => void;
}) {
  return (
    <div className="rounded-xl bg-[var(--bg-subtle)] p-3 shadow-[inset_0_0_0_1px_var(--border)] sm:p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2.5">
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-[var(--bg-elevated)] text-accent-700 shadow-sm dark:text-accent-300">
            <Package className="h-4 w-4" />
          </div>
          <div className="min-w-0">
            <p className="truncate text-sm font-semibold text-[var(--text)]">{planName || "Kiro package"}</p>
            <p className="mt-0.5 text-xs text-[var(--text-muted)]">Live allowance from this account</p>
          </div>
        </div>
        <button
          type="button"
          onClick={onRefresh}
          disabled={loading || disabled}
          className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-[var(--text-muted)] transition-[transform,background-color,color] duration-150 hover:bg-[var(--bg-elevated)] hover:text-[var(--text)] active:scale-[0.96] disabled:cursor-not-allowed disabled:opacity-40 disabled:active:scale-100"
          aria-label="Refresh Kiro usage"
          title="Refresh usage"
        >
          <RefreshCw className={`h-4 w-4 ${loading ? "animate-spin" : ""}`} />
        </button>
      </div>

      {disabled ? (
        <p className="mt-3 text-xs text-[var(--text-muted)]">Enable this account to refresh its package usage.</p>
      ) : loading && quotas.length === 0 ? (
        <div className="mt-4 space-y-3" aria-label="Loading Kiro usage">
          {[0, 1].map((item) => (
            <div key={item} className="space-y-2">
              <div className="skeleton h-3 w-2/5 rounded" />
              <div className="skeleton h-1.5 w-full rounded-full" />
            </div>
          ))}
        </div>
      ) : error ? (
        <div className="mt-3 flex items-start gap-2 rounded-lg bg-[color:var(--color-danger)]/8 px-3 py-2 text-xs text-[color:var(--color-danger)] shadow-[inset_0_0_0_1px_color-mix(in_srgb,var(--color-danger)_20%,transparent)]">
          <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span>{error}</span>
        </div>
      ) : quotas.length > 0 ? (
        <div className="mt-4 grid gap-3 lg:grid-cols-2">
          {quotas.map((quota) => (
            <QuotaBarInline key={quota.resource_type} quota={quota} />
          ))}
        </div>
      ) : (
        <p className="mt-3 text-xs text-[var(--text-muted)]">{message || "Kiro did not report a usage allowance for this account."}</p>
      )}
      {message && quotas.length > 0 && <p className="mt-3 text-xs text-[var(--text-muted)]">{message}</p>}
    </div>
  );
}

function QuotaBarInline({ quota: q }: { quota: UpstreamQuota }) {
  const pct = q.limit > 0 ? Math.min(100, Math.round((q.used / q.limit) * 100)) : 0;
  const remainingPct = q.limit > 0 ? Math.round((q.remaining / q.limit) * 100) : 0;
  const tone =
    remainingPct < 30
      ? "bg-[color:var(--color-danger)]"
      : remainingPct < 70
        ? "bg-[color:var(--color-warning)]"
        : "bg-accent-500";
  const label = q.resource_type
    .toLowerCase()
    .replace(/_/g, " ")
    .replace(/\b\w/g, (c) => c.toUpperCase());

  const resetDate = q.reset_at
    ? /^\d+$/.test(q.reset_at)
      ? new Date(Number(q.reset_at) * (Number(q.reset_at) > 10_000_000_000 ? 1 : 1000))
      : new Date(q.reset_at)
    : null;
  const resetLabel = resetDate && !isNaN(resetDate.getTime())
    ? resetDate.toLocaleDateString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })
    : null;

  return (
    <div className="rounded-lg bg-[var(--bg-elevated)] p-3 shadow-sm">
      <div className="mb-2 flex items-center justify-between gap-3 text-[11px]">
        <span className="font-medium text-[var(--text)]">{label}</span>
        <span className="shrink-0 font-semibold tabular-nums text-[var(--text)]">{q.remaining.toLocaleString()} left</span>
      </div>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-[var(--bg-subtle)]">
        <div className={`h-full rounded-full ${tone}`} style={{ width: `${pct}%` }} />
      </div>
      <div className="mt-2 flex flex-wrap items-center justify-between gap-1 text-[10px] text-[var(--text-muted)]">
        <span className="tabular-nums">{q.used.toLocaleString()} of {q.limit.toLocaleString()} used</span>
        {resetLabel && <span className="inline-flex items-center gap-1"><Clock3 className="h-3 w-3" />Resets {resetLabel}</span>}
      </div>
    </div>
  );
}

// BulkAddKeysModal imports many API keys for a provider in one shot. Shared
// provider config (base URL, region, Cloudflare account) is entered once and
// applied to every key; only the key (and an optional inline label / base URL)
// varies per line. The standardized paste format is parsed live with a preview,
// keys can be loaded from a .txt/.csv file, and the backend returns a per-row
// outcome that is rendered after import.
function BulkAddKeysModal({ provider, onClose }: { provider: Provider; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [text, setText] = useState("");
  const [validate, setValidate] = useState(false);
  const [baseURL, setBaseURL] = useState(provider.base_url ?? "");
  const [region, setRegion] = useState(provider.default_region ?? "");
  const [accountID, setAccountID] = useState("");
  const [results, setResults] = useState<BulkAccountResult[] | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const isCloudflare = provider.id === "cloudflare-ai";
  const hasRegions = (provider.regions?.length ?? 0) > 0;
  // User-created custom provider instances already define a base URL, so keys
  // inherit it — no shared base URL input is needed here.
  const inheritsBaseURL = !!provider.custom && !!provider.base_url;
  // Only the built-in generic gateways lack their own base URL and require one.
  const requiresBaseURL =
    !inheritsBaseURL && (provider.id === "custom-openai" || provider.id === "custom-anthropic");
  // Generic providers expose an optional shared base URL; region/Cloudflare
  // providers use their own dedicated control, and inherited-base-URL custom
  // instances need no input at all.
  const showBaseURL = !hasRegions && !isCloudflare && !inheritsBaseURL;
  const keyPlaceholder = provider.id === "xai" ? "xai-..." : "sk-...";

  const parsed = useMemo(() => parseKeys(text), [text]);
  const validCount = parsed.entries.length;

  const importMut = useMutation({
    mutationFn: () =>
      api.bulkCreateAccounts({
        provider: provider.id,
        base_url: showBaseURL && baseURL.trim() ? baseURL.trim() : undefined,
        region: hasRegions ? region : undefined,
        account_id: isCloudflare ? accountID.trim() : undefined,
        validate,
        items: parsed.entries.map((e) => ({
          label: e.label || undefined,
          api_key: e.apiKey,
          base_url: e.baseURL,
        })),
      }),
    onSuccess: (res) => {
      setResults(res.results);
      qc.invalidateQueries({ queryKey: ["accounts"] });
      if (res.failed === 0) {
        toast.success(
          "Bulk import complete",
          `${res.created} key${res.created === 1 ? "" : "s"} added${res.skipped ? `, ${res.skipped} duplicate skipped` : ""}.`,
        );
      } else {
        toast.error(
          "Bulk import finished with errors",
          `${res.created} added, ${res.failed} failed${res.skipped ? `, ${res.skipped} skipped` : ""}.`,
        );
      }
    },
    onError: (e: Error) => toast.error("Bulk import failed", e.message),
  });

  const canImport =
    validCount > 0 &&
    !importMut.isPending &&
    (!requiresBaseURL || !!baseURL.trim()) &&
    (!isCloudflare || !!accountID.trim());
  const onFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    const content = await file.text();
    setText((prev) => (prev.trim() ? `${prev.replace(/\s+$/, "")}\n${content}` : content));
    e.target.value = "";
  };

  const reset = () => {
    setResults(null);
    setText("");
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={`Bulk add API keys — ${provider.display_name}`}
      subtitle="Paste one key per line, or load a .txt/.csv file."
      maxWidth="max-w-2xl"
    >
      {results ? (
        <BulkResultsView
          results={results}
          onClose={onClose}
          onAgain={reset}
        />
      ) : (
        <div className="space-y-4 px-6 py-5">
          {/* Shared provider config applied to every key. */}
          {(showBaseURL || hasRegions || isCloudflare) && (
            <div className="space-y-3 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4">
              <p className="text-xs font-medium text-[var(--text-muted)]">Shared settings (applied to every key)</p>
              {hasRegions && (
                <Field label="Region">
                  <select
                    value={region}
                    onChange={(e) => setRegion(e.target.value)}
                    className="w-full rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] px-3 py-2 text-sm focus:border-accent-400 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/40"
                  >
                    {(provider.regions ?? []).map((r) => (
                      <option key={r.id} value={r.id}>
                        {r.label}
                      </option>
                    ))}
                  </select>
                </Field>
              )}
              {isCloudflare && (
                <div className="space-y-3">
                  <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] p-3 space-y-2">
                    <p className="text-xs font-medium text-[var(--text-muted)]">Cloudflare Workers AI setup</p>
                    <ol className="space-y-1 text-[11px] text-[var(--text-muted)]">
                      <li className="flex gap-1.5">
                        <span className="font-medium text-[var(--text)]">1.</span>
                        <span>
                          Create an API token at{" "}
                          <a href="https://dash.cloudflare.com/profile/api-tokens" target="_blank" rel="noopener noreferrer" className="text-accent-600 hover:underline dark:text-accent-400">
                            dash.cloudflare.com
                          </a>{" "}
                          — use the <code className="rounded bg-[var(--bg-subtle)] px-1 py-0.5 text-[10px]">Workers AI</code> template
                        </span>
                      </li>
                      <li className="flex gap-1.5">
                        <span className="font-medium text-[var(--text)]">2.</span>
                        <span>
                          Copy your Account ID from the{" "}
                          <a href="https://dash.cloudflare.com" target="_blank" rel="noopener noreferrer" className="text-accent-600 hover:underline dark:text-accent-400">
                            Cloudflare dashboard
                          </a>{" "}
                          right sidebar
                        </span>
                      </li>
                    </ol>
                  </div>
                  <Field label="Account ID">
                    <Input value={accountID} onChange={(e) => setAccountID(e.target.value)} placeholder="e.g. a1b2c3d4e5f6..." required />
                  </Field>
                </div>
              )}
              {showBaseURL && (
                <Field label={requiresBaseURL ? "Base URL" : "Base URL (optional)"}>
                  <Input
                    value={baseURL}
                    onChange={(e) => setBaseURL(e.target.value)}
                    placeholder="for custom endpoints"
                    required={requiresBaseURL}
                  />
                </Field>
              )}
            </div>
          )}

          {inheritsBaseURL && (
            <p className="text-xs text-[var(--text-muted)]">
              Keys use this provider's base URL{" "}
              <code className="font-mono text-[var(--text)]">{provider.base_url}</code>. Change it in
              the provider settings.
            </p>
          )}

          <div className="space-y-1.5">
            <div className="flex items-center justify-between">
              <label className="text-sm font-medium">API keys</label>
              <button
                type="button"
                onClick={() => fileRef.current?.click()}
                className="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border)] px-2 py-1 text-xs text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-subtle)] hover:text-[var(--text)]"
              >
                <FileText className="h-3.5 w-3.5" />
                Load file
              </button>
              <input ref={fileRef} type="file" accept=".txt,.csv,text/plain,text/csv" className="hidden" onChange={onFile} />
            </div>
            <textarea
              value={text}
              onChange={(e) => setText(e.target.value)}
              rows={8}
              spellCheck={false}
              placeholder={`${keyPlaceholder}\nlabel-2, ${keyPlaceholder}\n# lines starting with # are comments`}
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-3 py-2 font-mono text-xs placeholder:text-[var(--text-muted)] focus:border-accent-400 focus:outline-none"
            />
            <p className="text-[11px] leading-relaxed text-[var(--text-muted)]">
              One key per line. Optional inline label: <code className="font-mono">label,key</code>. Blank lines and{" "}
              <code className="font-mono">#</code> comments are ignored.
            </p>
          </div>

          {/* Live parse preview. */}
          {text.trim() && (
            <div className="flex flex-wrap items-center gap-2 text-xs">
              <Badge tone={validCount > 0 ? "success" : "neutral"}>{validCount} ready</Badge>
              {parsed.duplicates > 0 && <Badge tone="warning">{parsed.duplicates} duplicate</Badge>}
              {parsed.errors.length > 0 && <Badge tone="danger">{parsed.errors.length} invalid</Badge>}
              {parsed.errors.slice(0, 3).map((err) => (
                <span key={err.line} className="text-[var(--text-muted)]">
                  line {err.line}: {err.message}
                </span>
              ))}
            </div>
          )}

          <label className="flex cursor-pointer items-start gap-2.5 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-3">
            <input
              type="checkbox"
              checked={validate}
              onChange={(e) => setValidate(e.target.checked)}
              className="mt-0.5 h-3.5 w-3.5 rounded border-[var(--border)] accent-[var(--color-accent-500)]"
            />
            <span className="text-xs leading-relaxed text-[var(--text-muted)]">
              <span className="font-medium text-[var(--text)]">Validate each key against the upstream</span> before saving.
              Slower for large batches and may hit provider rate limits. Off by default.
            </span>
          </label>

          <div className="flex gap-3">
            <Button type="button" variant="ghost" onClick={onClose} className="flex-1">
              Cancel
            </Button>
            <Button type="button" onClick={() => importMut.mutate()} disabled={!canImport} className="flex-1">
              {importMut.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Upload className="h-4 w-4" />}
              {importMut.isPending ? "Importing…" : `Import ${validCount || ""}`.trim()}
            </Button>
          </div>
        </div>
      )}
    </Modal>
  );
}

// BulkResultsView renders the per-row outcome of a bulk import.
function BulkResultsView({
  results,
  onClose,
  onAgain,
}: {
  results: BulkAccountResult[];
  onClose: () => void;
  onAgain: () => void;
}) {
  const created = results.filter((r) => r.status === "created").length;
  const skipped = results.filter((r) => r.status === "skipped").length;
  const failed = results.filter((r) => r.status === "error").length;

  return (
    <div className="space-y-4 px-6 py-5">
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone="success">{created} added</Badge>
        {skipped > 0 && <Badge tone="warning">{skipped} skipped</Badge>}
        {failed > 0 && <Badge tone="danger">{failed} failed</Badge>}
      </div>
      <div className="max-h-72 divide-y divide-[var(--border)] overflow-y-auto rounded-xl border border-[var(--border)]">
        {results.map((r) => (
          <div key={r.index} className="flex items-center gap-3 px-3 py-2 text-xs">
            {r.status === "created" ? (
              <CheckCircle className="h-4 w-4 shrink-0 text-emerald-500" />
            ) : r.status === "skipped" ? (
              <AlertCircle className="h-4 w-4 shrink-0 text-amber-500" />
            ) : (
              <XCircle className="h-4 w-4 shrink-0 text-red-500" />
            )}
            <span className="w-10 shrink-0 text-[var(--text-muted)]">#{r.index + 1}</span>
            <span className="flex-1 truncate font-medium">{r.label || "(unlabeled)"}</span>
            {r.error && <span className="truncate text-[var(--text-muted)]" title={r.error}>{r.error}</span>}
          </div>
        ))}
      </div>
      <div className="flex gap-3">
        <Button type="button" variant="ghost" onClick={onAgain} className="flex-1">
          <Layers className="h-4 w-4" />
          Import more
        </Button>
        <Button type="button" onClick={onClose} className="flex-1">
          <Check className="h-4 w-4" />
          Done
        </Button>
      </div>
    </div>
  );
}

function AddApiKeyModal({
  provider,
  hasRegions,
  label,
  apiKey,
  baseURL,
  region,
  accountID,
  azureEndpoint,
  azureDeployment,
  azureAPIVersion,
  azureOrganization,
  error,
  pending,
  onLabel,
  onApiKey,
  onBaseURL,
  onRegion,
  onAccountID,
  onAzureEndpoint,
  onAzureDeployment,
  onAzureAPIVersion,
  onAzureOrganization,
  onSubmit,
  onClose,
}: {
  provider: Provider;
  hasRegions: boolean;
  label: string;
  apiKey: string;
  baseURL: string;
  region: string;
  accountID: string;
  azureEndpoint: string;
  azureDeployment: string;
  azureAPIVersion: string;
  azureOrganization: string;
  error: string;
  pending: boolean;
  onLabel: (v: string) => void;
  onApiKey: (v: string) => void;
  onBaseURL: (v: string) => void;
  onRegion: (v: string) => void;
  onAccountID: (v: string) => void;
  onAzureEndpoint: (v: string) => void;
  onAzureDeployment: (v: string) => void;
  onAzureAPIVersion: (v: string) => void;
  onAzureOrganization: (v: string) => void;
  onSubmit: () => void;
  onClose: () => void;
}) {
  const [checkStatus, setCheckStatus] = useState<"idle" | "ok" | "error">("idle");
  const [checkMsg, setCheckMsg] = useState("");
  const [checking, setChecking] = useState(false);
  const supportsApiKey = provider.auth_modes.includes("api_key") || provider.auth_kind === "api_key";
  const supportsNone = provider.auth_modes.includes("none") || provider.auth_kind === "none";
  // Hide the API-key field only when API key auth is not supported at all.
  const isNoAuth = supportsNone && !supportsApiKey;
  // When both modes are offered, the API key is optional.
  const apiKeyOptional = supportsNone && supportsApiKey;
  const isAzure = provider.id === "azure";
  const isCloudflare = provider.id === "cloudflare-ai";
  // A user-created custom provider instance carries its own base URL (set when
  // the provider was created), so its accounts inherit it — asking for a base
  // URL again per API key is redundant and confusing.
  const inheritsBaseURL = !!provider.custom && !!provider.base_url;
  // The built-in generic gateways ("custom-openai"/"custom-anthropic") have no
  // base URL of their own, so each account must still supply one.
  const requiresBaseURL =
    !inheritsBaseURL && (provider.id === "custom-openai" || provider.id === "custom-anthropic");
  const credentialLabel = isNoAuth ? "Connection" : apiKeyOptional ? "API key (optional)" : "API key";
  const canSubmit =
    !pending &&
    (isNoAuth || apiKeyOptional || !!apiKey.trim()) &&
    (!isCloudflare || !!accountID.trim()) &&
    (!isAzure || (!!azureEndpoint.trim() && !!azureDeployment.trim())) &&
    (!requiresBaseURL || !!baseURL.trim());

  const handleCheck = async () => {
    if (!canSubmit && !isNoAuth) return;
    setChecking(true);
    setCheckStatus("idle");
    setCheckMsg("");
    try {
      const res = await api.validateKey({
        provider: provider.id,
        label,
        api_key: apiKey || undefined,
        base_url: baseURL || undefined,
        region: hasRegions ? region : undefined,
        account_id: accountID || undefined,
        azure_endpoint: azureEndpoint || undefined,
        azure_deployment: azureDeployment || undefined,
        azure_api_version: azureAPIVersion || undefined,
        azure_organization: azureOrganization || undefined,
      });
      setCheckStatus(res.status === "ok" ? "ok" : "error");
      setCheckMsg(res.message || "");
    } catch (e) {
      setCheckStatus("error");
      setCheckMsg((e as Error).message);
    } finally {
      setChecking(false);
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        className="w-full max-w-md rounded-2xl border border-[var(--border)] bg-[var(--bg-elevated)] shadow-[var(--shadow-float)] overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-[var(--border)] px-6 py-4">
          <h2 className="text-sm font-semibold">Add API key — {provider.display_name}</h2>
          <button
            onClick={onClose}
            className="flex h-8 w-8 items-center justify-center rounded-lg text-[var(--text-muted)] transition-colors hover:bg-ink-100 hover:text-[var(--text)] dark:hover:bg-ink-800"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <form
          className="space-y-4 px-6 py-5"
          onSubmit={(e) => {
            e.preventDefault();
            if (canSubmit) onSubmit();
          }}
        >
          <Field label="Label">
            <Input value={label} onChange={(e) => onLabel(e.target.value)} placeholder="personal" />
          </Field>
          {!isNoAuth && (
            <Field label={credentialLabel}>
              <Input
                type="password"
                value={apiKey}
                onChange={(e) => { onApiKey(e.target.value); setCheckStatus("idle"); }}
                placeholder={isCloudflare ? "CF API token (v1.0-...)" : provider.id === "xai" ? "xai-..." : "sk-..."}
                required={!apiKeyOptional}
              />
            </Field>
          )}
          {isCloudflare && (
            <div className="space-y-3">
              <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4 space-y-2">
                <p className="text-xs font-medium text-[var(--text-muted)]">Cloudflare Workers AI setup</p>
                <ol className="space-y-1.5 text-xs text-[var(--text-muted)]">
                  <li className="flex gap-1.5">
                    <span className="font-medium text-[var(--text)]">1.</span>
                    <span>
                      Create an API token at{" "}
                      <a href="https://dash.cloudflare.com/profile/api-tokens" target="_blank" rel="noopener noreferrer" className="text-accent-600 hover:underline dark:text-accent-400">
                        dash.cloudflare.com
                      </a>{" "}
                      — use the <code className="rounded bg-[var(--bg-elevated)] px-1 py-0.5 text-[10px]">Workers AI</code> template
                    </span>
                  </li>
                  <li className="flex gap-1.5">
                    <span className="font-medium text-[var(--text)]">2.</span>
                    <span>
                      Copy your Account ID from the{" "}
                      <a href="https://dash.cloudflare.com" target="_blank" rel="noopener noreferrer" className="text-accent-600 hover:underline dark:text-accent-400">
                        Cloudflare dashboard
                      </a>{" "}
                      right sidebar
                    </span>
                  </li>
                </ol>
              </div>
              <Field label="Account ID">
                <Input
                  value={accountID}
                  onChange={(e) => { onAccountID(e.target.value); setCheckStatus("idle"); }}
                  placeholder="e.g. a1b2c3d4e5f6..."
                  required
                />
              </Field>
            </div>
          )}
          {isAzure ? (
            <div className="space-y-3 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4">
              <Field label="Azure endpoint">
                <Input
                  value={azureEndpoint}
                  onChange={(e) => { onAzureEndpoint(e.target.value); setCheckStatus("idle"); }}
                  placeholder="https://your-resource.openai.azure.com"
                  required
                />
              </Field>
              <Field label="Deployment name">
                <Input
                  value={azureDeployment}
                  onChange={(e) => { onAzureDeployment(e.target.value); setCheckStatus("idle"); }}
                  placeholder="gpt-4o"
                  required
                />
              </Field>
              <Field label="API version">
                <Input
                  value={azureAPIVersion}
                  onChange={(e) => { onAzureAPIVersion(e.target.value); setCheckStatus("idle"); }}
                  placeholder="2024-10-01-preview"
                />
              </Field>
              <Field label="Organization (optional)">
                <Input
                  value={azureOrganization}
                  onChange={(e) => { onAzureOrganization(e.target.value); setCheckStatus("idle"); }}
                  placeholder="org_..."
                />
              </Field>
            </div>
          ) : hasRegions ? (
            <Field label="Region">
              <select
                value={region}
                onChange={(e) => onRegion(e.target.value)}
                className="w-full rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] px-3 py-2 text-sm focus:border-accent-400 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/40"
              >
                {(provider.regions ?? []).map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.label}
                  </option>
                ))}
              </select>
            </Field>
          ) : inheritsBaseURL ? (
            <Field label="Base URL">
              <div className="flex items-center rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] px-3 py-2">
                <code className="truncate font-mono text-xs text-[var(--text-muted)]" title={provider.base_url}>
                  {provider.base_url}
                </code>
              </div>
              <p className="mt-1 text-xs text-[var(--text-muted)]">
                Inherited from this provider. Change it in the provider settings.
              </p>
            </Field>
          ) : (
            <Field label={requiresBaseURL ? "Base URL" : "Base URL (optional)"}>
              <Input
                value={baseURL}
                onChange={(e) => onBaseURL(e.target.value)}
                placeholder="for custom endpoints"
                required={requiresBaseURL}
              />
            </Field>
          )}

          {checkStatus === "ok" && (
            <div className="flex items-center gap-2 rounded-lg border border-accent-300 bg-accent-50 px-3 py-2 text-sm text-accent-700 dark:border-accent-700 dark:bg-accent-900/30 dark:text-accent-200">
              <CheckCircle className="h-4 w-4 shrink-0" />
              Key is valid
            </div>
          )}
          {checkStatus === "error" && (
            <ErrorBanner message={checkMsg || "Key validation failed"} />
          )}
          {error && <ErrorBanner message={error} />}

          <div className="flex gap-3">
            <Button type="button" variant="ghost" onClick={handleCheck} disabled={checking || !canSubmit} className="flex-1">
              <CheckCircle className={`h-4 w-4 ${checking ? "animate-pulse" : ""}`} />
              {checking ? "Checking…" : "Check"}
            </Button>
            <Button type="submit" disabled={!canSubmit} className="flex-1">
              <Plus className="h-4 w-4" />
              {pending ? "Adding…" : isNoAuth ? "Connect" : "Add account"}
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}

// ---- OAuth connect modal (reused flow) --------------------------------------

function ConnectModal({ provider, onClose }: { provider: OAuthProvider; onClose: () => void }) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        className="w-full max-w-md rounded-2xl border border-[var(--border)] bg-[var(--bg-elevated)] shadow-[var(--shadow-float)]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-[var(--border)] px-6 py-4">
          <h2 className="text-sm font-semibold">Connect {provider.display_name}</h2>
          <button
            onClick={onClose}
            className="flex h-8 w-8 items-center justify-center rounded-lg text-[var(--text-muted)] transition-colors hover:bg-ink-100 hover:text-[var(--text)] dark:hover:bg-ink-800"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        {provider.flow === "device_code" ? (
          <DeviceFlow provider={provider} onClose={onClose} />
        ) : (
          <AuthCodeFlow provider={provider} onClose={onClose} />
        )}
      </div>
    </div>
  );
}

function AuthCodeFlow({ provider, onClose }: { provider: OAuthProvider; onClose: () => void }) {
  const qc = useQueryClient();
  const [waiting, setWaiting] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState(false);
  // Manual-paste mode is used on public hosts where the provider's loopback
  // redirect can't reach the gateway. The user pastes the callback URL back.
  const [manual, setManual] = useState(false);
  const [pasted, setPasted] = useState("");
  const [exchanging, setExchanging] = useState(false);
  const stateRef = useRef("");
  const popupRef = useRef<Window | null>(null);

  const finishSuccess = () => {
    // Close the OAuth popup from the opener side (the popup's own
    // window.close() may be blocked after cross-origin redirects).
    if (popupRef.current && !popupRef.current.closed) {
      try { popupRef.current.close(); } catch { /* ignore */ }
      popupRef.current = null;
    }
    setDone(true);
    qc.invalidateQueries({ queryKey: ["accounts"] });
    setTimeout(onClose, 1500);
  };

  // Listen for the postMessage from the gateway callback page. The popup may
  // forward either a raw code (we exchange it here) or a server-side result
  // status/message (embedded mode already exchanged).
  useEffect(() => {
    if (!waiting) return;
    const handler = async (e: MessageEvent) => {
      if (e.data?.type !== "oauth-callback") return;
      if (e.data.provider && e.data.provider !== provider.provider) return;
      if (e.data.code) {
        try {
          await api.oauthExchange(provider.provider, {
            code: e.data.code,
            state: e.data.state || stateRef.current,
          });
          finishSuccess();
        } catch (err) {
          setError((err as Error).message);
          setWaiting(false);
        }
        return;
      }
      if (e.data.status === "success") {
        finishSuccess();
      } else {
        setError(e.data.message || "Connection failed.");
        setWaiting(false);
      }
    };
    window.addEventListener("message", handler);
    return () => window.removeEventListener("message", handler);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [waiting, provider.provider]);

  const start = async () => {
    setError("");
    try {
      const res = await api.oauthAuthorize(provider.provider, redirectURIForProvider(provider));
      stateRef.current = res.state;
      popupRef.current = window.open(res.authorize_url, "_blank", "popup,width=560,height=760");
      // Always attempt the seamless flow. Whenever the gateway is co-located
      // with the browser, its loopback callback catches the redirect and
      // notifies us via postMessage — regardless of the dashboard's hostname.
      // Manual paste stays available as a one-click fallback for truly remote
      // setups where the loopback can't reach the gateway.
      setWaiting(true);
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const submitManual = async () => {
    setError("");
    const input = pasted.trim();
    if (!input) {
      setError("Paste the full callback URL (or the code) from the other tab.");
      return;
    }
    let code = input;
    let state = stateRef.current;
    // Accept either a pasted callback URL or a bare code value.
    if (input.includes("://") || input.includes("?") || input.includes("code=")) {
      try {
        const u = new URL(input.includes("://") ? input : `http://localhost/?${input.replace(/^\?/, "")}`);
        const err = u.searchParams.get("error");
        if (err) {
          setError(u.searchParams.get("error_description") || err);
          return;
        }
        code = u.searchParams.get("code") || "";
        state = u.searchParams.get("state") || state;
      } catch {
        setError("Could not parse the callback URL. Paste the full URL from the address bar.");
        return;
      }
    }
    if (!code) {
      setError("No authorization code found. Paste the full callback URL.");
      return;
    }
    setExchanging(true);
    try {
      await api.oauthExchange(provider.provider, { code, state });
      finishSuccess();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setExchanging(false);
    }
  };

  if (done) return <div className="px-6 py-6 text-sm">Connected. Refreshing accounts…</div>;

  if (manual) {
    return (
      <div className="space-y-4 px-6 py-5">
        <p className="text-sm text-[var(--text-muted)]">
          Sign in with {provider.display_name} in the other tab. After approving,
          your browser lands on a <code>localhost</code> page that can't load —
          copy that page's full URL from the address bar and paste it here.
        </p>
        <Field label="Callback URL">
          <Input
            value={pasted}
            onChange={(e) => { setPasted(e.target.value); setError(""); }}
            placeholder="http://localhost:.../oauth/callback?code=...&state=..."
          />
        </Field>
        <Button onClick={submitManual} disabled={exchanging} className="w-full">
          {exchanging ? "Connecting…" : "Complete connection"}
        </Button>
        {error && <p className="text-xs text-[color:var(--color-danger)]">{error}</p>}
      </div>
    );
  }

  return (
    <div className="space-y-4 px-6 py-5">
      {!waiting ? (
        <>
          <p className="text-sm text-[var(--text-muted)]">
            Click the button below to sign in with {provider.display_name}. A
            new tab will open for authentication.
          </p>
          <Button onClick={start} className="w-full">
            Open sign-in
          </Button>
        </>
      ) : (
        <div className="flex flex-col items-center gap-3 py-4">
          <Spinner />
          <p className="text-sm text-[var(--text-muted)]">
            Waiting for sign-in to complete…
          </p>
          <p className="text-xs text-[var(--text-muted)]">
            Complete the sign-in in the other tab. This will close
            automatically.
          </p>
          <button
            type="button"
            onClick={() => { setWaiting(false); setManual(true); }}
            className="text-xs text-[var(--text-muted)] underline underline-offset-2 hover:text-[var(--text)]"
          >
            Stuck? Enter the code manually
          </button>
        </div>
      )}
      {error && <p className="text-xs text-[color:var(--color-danger)]">{error}</p>}
    </div>
  );
}

function DeviceFlow({ provider, onClose }: { provider: OAuthProvider; onClose: () => void }) {
  const qc = useQueryClient();
  const [dc, setDc] = useState<DeviceCode | null>(null);
  const [status, setStatus] = useState<"idle" | "waiting" | "done" | "error">("idle");
  const [error, setError] = useState("");
  const pollRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    return () => {
      if (pollRef.current) clearTimeout(pollRef.current);
    };
  }, []);

  const start = async () => {
    setError("");
    try {
      const res = await api.oauthDeviceCode(provider.provider);

      // ClientDeviceCode flow: the browser must make the upstream device-code
      // HTTP request itself (the Go backend is blocked by TLS-fingerprinting
      // WAFs such as Alibaba Cloud WAF used by Qwen).
      if (res._client_device_code) {
        const params = new URLSearchParams({
          client_id: res._client_id!,
          scope: (res._scopes ?? []).join(" "),
          code_challenge: res._pkce_challenge!,
          code_challenge_method: res._pkce_method ?? "S256",
        });
        const upstream = await fetch(res._device_code_url!, {
          method: "POST",
          headers: {
            "Content-Type": "application/x-www-form-urlencoded",
            Accept: "application/json",
          },
          body: params.toString(),
        });
        if (!upstream.ok) {
          throw new Error(`Device-code request failed (${upstream.status})`);
        }
        const ct = upstream.headers.get("content-type") ?? "";
        if (!ct.includes("json")) {
          throw new Error(
            "Provider returned an unexpected response (possibly a CAPTCHA page). Please try again later.",
          );
        }
        const dcData = await upstream.json();
        const submitted = await api.oauthDeviceCodeSubmit(provider.provider, {
          nonce: res._pkce_nonce!,
          device_code: dcData.device_code,
          user_code: dcData.user_code ?? "",
          verification_uri: dcData.verification_uri ?? "",
          verification_uri_complete: dcData.verification_uri_complete ?? "",
          expires_in: dcData.expires_in ?? 300,
          interval: dcData.interval ?? 5,
        });
        setDc(submitted);
        setStatus("waiting");
        poll(submitted.device_code, submitted.interval);
        return;
      }

      setDc(res);
      setStatus("waiting");
      poll(res.device_code, res.interval);
    } catch (e) {
      setError((e as Error).message);
      setStatus("error");
    }
  };

  const poll = (deviceCode: string, interval: number) => {
    pollRef.current = setTimeout(async () => {
      try {
        const res = await api.oauthPoll(provider.provider, deviceCode);
        if (res.status === "complete") {
          setStatus("done");
          qc.invalidateQueries({ queryKey: ["accounts"] });
          setTimeout(onClose, 1200);
          return;
        }
        poll(deviceCode, res.slow_down ? interval + 5 : interval);
      } catch (e) {
        setError((e as Error).message);
        setStatus("error");
      }
    }, Math.max(1, interval) * 1000);
  };

  if (status === "done") return <div className="px-6 py-6 text-sm">Connected. Refreshing accounts…</div>;

  return (
    <div className="space-y-4 px-6 py-5">
      {!dc ? (
        <>
          <p className="text-sm text-[var(--text-muted)]">
            A device code will be generated. Enter it on the provider's verification page to authorize.
          </p>
          <Button onClick={start} className="w-full">
            Generate device code
          </Button>
        </>
      ) : (
        <>
          <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-4 text-center">
            <p className="text-xs text-[var(--text-muted)]">Your code</p>
            <p className="mt-1 font-mono text-2xl font-bold tracking-widest">{dc.user_code}</p>
          </div>
          <a
            href={dc.verification_uri_complete || dc.verification_uri}
            target="_blank"
            rel="noopener noreferrer"
            className="block w-full rounded-xl bg-accent-600 px-3 py-2 text-center text-sm font-medium text-white shadow-sm transition-colors hover:bg-accent-700"
          >
            Open verification page
          </a>
          <p className="text-center text-xs text-[var(--text-muted)]">
            {status === "waiting" ? "Waiting for you to authorize…" : ""}
          </p>
        </>
      )}
      {error && <p className="text-xs text-[color:var(--color-danger)]">{error}</p>}
    </div>
  );
}

// ---- Codex reset credits section --------------------------------------------

function CodexResetCreditsSection({ accountId }: { accountId: string }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [confirmingReset, setConfirmingReset] = useState(false);
  const details = useQuery({
    queryKey: ["codex-usage-details", accountId],
    queryFn: () => api.codexUsageDetails(accountId),
    staleTime: 30_000,
    retry: 1,
  });
  const consumeCredit = useMutation({
    mutationFn: (creditId?: string) => api.codexConsumeCredit(accountId, creditId),
    onSuccess: async (result) => {
      if (result.ok) {
        toast.success("Usage limits reset", "A reset credit was used successfully.");
        setConfirmingReset(false);
        await details.refetch();
        qc.invalidateQueries({ queryKey: ["quota"] });
      } else if (result.no_credit) {
        toast.error("No credits available", result.message || "No reset credits remaining");
      } else {
        toast.error("Usage limits were not reset", result.message || "The reset request was not accepted.");
      }
    },
    onError: (error: Error) => toast.error("Usage limits were not reset", error.message),
  });

  const data = details.data;
  const availableCount = data?.reset_credits?.available_count ?? data?.usage_data?.reset_credits_available ?? 0;
  const availableCredits = data?.reset_credits?.credits.filter((credit) => credit.status === "available") ?? [];
  const firstAvailableCredit = availableCredits[0];
  const soonestExpiry = availableCredits
    .map((credit) => credit.expires_at)
    .filter((value): value is string => !!value)
    .sort((left, right) => new Date(left).getTime() - new Date(right).getTime())[0];

  return (
    <div className="rounded-xl bg-[var(--bg-subtle)] p-3 shadow-[inset_0_0_0_1px_var(--border)] sm:p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-sm font-semibold text-[var(--text)]">Usage limits</h3>
            {data?.usage_data?.plan_type && (
              <Badge tone={data.usage_data.limit_reached ? "danger" : "accent"}>{data.usage_data.plan_type}</Badge>
            )}
          </div>
          <p className="mt-0.5 text-xs text-[var(--text-muted)]">Codex allowance and earned resets for this account</p>
        </div>
        <button
          type="button"
          onClick={() => details.refetch()}
          disabled={details.isFetching}
          className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-[var(--text-muted)] transition-[transform,background-color,color] duration-150 hover:bg-[var(--bg-elevated)] hover:text-[var(--text)] active:scale-[0.96] disabled:cursor-not-allowed disabled:opacity-40 disabled:active:scale-100"
          aria-label="Refresh Codex usage"
          title="Refresh usage"
        >
          <RefreshCw className={`h-4 w-4 ${details.isFetching ? "animate-spin" : ""}`} />
        </button>
      </div>

      {details.isLoading ? (
        <div className="mt-4 grid gap-3 sm:grid-cols-2" aria-label="Loading Codex usage">
          {[0, 1].map((item) => <div key={item} className="skeleton h-28 rounded-lg" />)}
        </div>
      ) : details.error ? (
        <div className="mt-3 flex items-start gap-2 rounded-lg bg-[color:var(--color-danger)]/8 px-3 py-2 text-xs text-[color:var(--color-danger)]">
          <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span>{details.error instanceof Error ? details.error.message : "Could not load Codex usage."}</span>
        </div>
      ) : data ? (
        <div className="mt-4 space-y-3">
          {data.error && (
            <div className="flex items-start gap-2 rounded-lg bg-[color:var(--color-warning)]/10 px-3 py-2 text-xs text-[color:var(--color-warning)]">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>{data.error}</span>
            </div>
          )}

          {data.usage_data && (
            <div className="grid gap-3 sm:grid-cols-2">
              <CodexLimitWindow
                label="5-hour limit"
                usedPercent={data.usage_data.primary_used_percent}
                resetAt={data.usage_data.primary_reset_at}
              />
              <CodexLimitWindow
                label="Weekly limit"
                usedPercent={data.usage_data.secondary_used_percent}
                resetAt={data.usage_data.secondary_reset_at}
              />
            </div>
          )}

          <div className="flex flex-col gap-3 rounded-lg bg-[var(--bg-elevated)] p-3 shadow-sm sm:flex-row sm:items-center sm:justify-between">
            <div className="flex min-w-0 items-center gap-5">
              <div>
                <p className="text-[10px] font-medium uppercase tracking-wider text-[var(--text-muted)]">Credits</p>
                <p className="mt-0.5 text-sm font-semibold tabular-nums text-[var(--text)]">
                  {data.usage_data?.unlimited ? "Unlimited" : data.usage_data?.credits_balance || "0"}
                </p>
              </div>
              <div className="h-8 w-px bg-[var(--border)]" />
              <div>
                <p className="text-[10px] font-medium uppercase tracking-wider text-[var(--text-muted)]">Earned resets</p>
                <p className="mt-0.5 text-sm font-semibold tabular-nums text-[var(--text)]">{availableCount} available</p>
              </div>
              {soonestExpiry && (
                <p className="hidden text-xs text-[var(--text-muted)] lg:block">Next expiry {new Date(soonestExpiry).toLocaleDateString()}</p>
              )}
            </div>

            {availableCount > 0 && (
              confirmingReset ? (
                <div className="flex shrink-0 items-center gap-2">
                  <span className="text-xs font-medium text-[var(--text-muted)]">Use 1 credit?</span>
                  <Button
                    variant="secondary"
                    className="min-h-10 px-3 py-1.5 text-xs"
                    disabled={consumeCredit.isPending}
                    onClick={() => consumeCredit.mutate(firstAvailableCredit?.id)}
                  >
                    {consumeCredit.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : "Confirm reset"}
                  </Button>
                  <Button variant="ghost" className="min-h-10 px-3 py-1.5 text-xs" disabled={consumeCredit.isPending} onClick={() => setConfirmingReset(false)}>
                    Cancel
                  </Button>
                </div>
              ) : (
                <Button variant="ghost" className="min-h-10 shrink-0 px-3 py-1.5 text-xs" onClick={() => setConfirmingReset(true)}>
                  <RefreshCw className="h-3.5 w-3.5" />
                  Reset limits
                </Button>
              )
            )}
          </div>
          {soonestExpiry && <p className="text-[10px] text-[var(--text-muted)] lg:hidden">Next reset credit expires {new Date(soonestExpiry).toLocaleDateString()}.</p>}
          {availableCount === 0 && <p className="text-[10px] text-[var(--text-muted)]">No earned reset credits are currently available.</p>}
        </div>
      ) : null}
    </div>
  );
}

function CodexLimitWindow({ label, usedPercent, resetAt }: { label: string; usedPercent: number; resetAt: number }) {
  const used = Math.min(100, Math.max(0, usedPercent));
  const remaining = Math.max(0, 100 - used);
  const tone = used >= 80
    ? "bg-[color:var(--color-danger)]"
    : used >= 50
      ? "bg-[color:var(--color-warning)]"
      : "bg-accent-500";

  return (
    <div className="rounded-lg bg-[var(--bg-elevated)] p-3 shadow-sm">
      <div className="flex items-start justify-between gap-3">
        <div>
          <p className="text-xs font-medium text-[var(--text)]">{label}</p>
          <p className="mt-1 text-[10px] text-[var(--text-muted)]">{resetAt > 0 ? `Resets ${new Date(resetAt * 1000).toLocaleString()}` : "Reset time unavailable"}</p>
        </div>
        <div className="text-right">
          <p className="text-sm font-semibold tabular-nums text-[var(--text)]">{remaining}% left</p>
          <p className="text-[10px] tabular-nums text-[var(--text-muted)]">{used}% used</p>
        </div>
      </div>
      <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-[var(--bg-subtle)]" aria-label={`${label}: ${used}% used`}>
        <div className={`h-full rounded-full ${tone}`} style={{ width: `${used}%` }} />
      </div>
    </div>
  );
}

// ModelCell renders a single model in a structural hairline grid.
function ModelCell({
  model,
  provider,
  disabled,
  selected,
  onToggleSelect,
  onToggleDisable,
}: {
  model: { id: string; name: string; kind: string };
  provider: Provider;
  disabled?: boolean;
  selected?: boolean;
  onToggleSelect?: () => void;
  onToggleDisable?: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const fullModel = `${provider.alias || provider.id}/${model.id}`;

  const handleCopy = () => {
    navigator.clipboard.writeText(fullModel);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <article
      className={`group relative flex min-h-44 flex-col rounded-xl border bg-[var(--bg-elevated)] p-4 shadow-sm transition-[transform,border-color,box-shadow] duration-200 hover:-translate-y-0.5 hover:border-[var(--border-strong)] hover:shadow-[var(--shadow-card)] ${
        disabled ? "border-[var(--border)] opacity-70" : "border-[var(--border)]"
      } ${selected ? "border-accent-400 ring-2 ring-accent-400/20" : ""}`}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          {onToggleSelect && (
            <input
              type="checkbox"
              className="h-4 w-4 shrink-0 rounded border-[var(--border)] accent-[var(--color-accent-500)]"
              checked={!!selected}
              onChange={onToggleSelect}
              aria-label={`Select ${model.name || model.id}`}
            />
          )}
          <Badge tone={disabled ? "neutral" : "success"}>
            {disabled ? "Disabled" : "Enabled"}
          </Badge>
        </div>
        <Badge tone="neutral">{model.kind || "Model"}</Badge>
      </div>

      <div className="mt-5 min-w-0 flex-1">
        <h3 className="truncate text-sm font-semibold" title={model.name || model.id}>
          {model.name || model.id}
        </h3>
        <code
          className="mt-2 block truncate rounded-lg bg-[var(--bg-subtle)] px-2.5 py-2 font-mono text-xs text-[var(--text-muted)]"
          title={fullModel}
        >
          {fullModel}
        </code>
      </div>

      <div className="mt-4 flex items-center justify-between border-t border-[var(--border)] pt-3">
        <span className="text-xs text-[var(--text-muted)]">{disabled ? "Excluded from routing" : "Enabled in catalog"}</span>
        <div className="flex items-center gap-1">
          {onToggleDisable && (
            <button
              type="button"
              onClick={onToggleDisable}
              className="flex h-9 w-9 items-center justify-center rounded-lg text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50"
              title={disabled ? "Enable model" : "Disable model"}
              aria-label={disabled ? `Enable ${model.name || model.id}` : `Disable ${model.name || model.id}`}
            >
              {disabled ? <ToggleLeft className="h-4 w-4" /> : <ToggleRight className="h-4 w-4 text-emerald-600" />}
            </button>
          )}
          <button
            type="button"
            onClick={handleCopy}
            className="flex h-9 w-9 items-center justify-center rounded-lg text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50"
            title="Copy model path"
            aria-label={`Copy model path ${fullModel}`}
          >
            {copied ? <CheckCircle className="h-4 w-4 text-emerald-600" /> : <Copy className="h-4 w-4" />}
          </button>
        </div>
      </div>
    </article>
  );
}

// ProviderIcon renders the provider PNG with a colored fallback initial.
function ProviderIcon({ provider: p, size = 40 }: { provider: Provider; size?: number }) {
  const [errored, setErrored] = useState(false);
  const dim = { width: size, height: size };
  if (errored || !p.icon) {
    return (
      <div
        className="flex shrink-0 items-center justify-center rounded-2xl text-lg font-bold text-white"
        style={{ ...dim, backgroundColor: p.color || "var(--text-muted)" }}
      >
        {p.display_name.slice(0, 1).toUpperCase()}
      </div>
    );
  }
  return (
    <img
      src={p.icon}
      alt={p.display_name}
      onError={() => setErrored(true)}
      className="shrink-0 rounded-2xl object-contain"
      style={dim}
    />
  );
}
