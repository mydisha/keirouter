import { useEffect, useRef, useState, useMemo } from "react";
import { useParams, Link, useNavigate } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Plus, Trash2, Plug, X, Zap, ArrowUp, ArrowDown, CheckCircle, ToggleLeft, ToggleRight, Search, Route, AlertCircle, AlertTriangle, RefreshCw, Globe, Copy, Check, Upload, Loader2, XCircle, Layers, FileText, Download } from "lucide-react";
import { api, type DeviceCode, type OAuthProvider, type Provider, type Account, type ProxyPool, type UpstreamQuota, type ProviderRoutingSettings, type BulkAccountResult, type QuotaAccount } from "../lib/api";
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
  const bulkQuota = useQuery({
    queryKey: ["quota", "today", id],
    queryFn: () => api.quotaByProvider(id!),
    enabled: !!id,
    staleTime: 60_000,
  });
  const quotaMap: Record<string, QuotaAccount> = Object.fromEntries(
    (bulkQuota.data?.accounts ?? []).map((a) => [a.id, a]),
  );
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

  // Sort accounts by priority for display.
  const sortedAccounts = [...myAccounts].sort((a, b) => a.priority - b.priority);
  const disabledModelIds = new Set(disabledModels.data?.ids ?? []);

  // Derived selection state (scoped to this provider's accounts).
  const selectedList = sortedAccounts.filter((a) => selectedAccountIds.has(a.id));
  const allAccountsSelected = sortedAccounts.length > 0 && selectedList.length === sortedAccounts.length;
  const someAccountsSelected = selectedList.length > 0 && !allAccountsSelected;
  const bulkBusy = bulkUpdateAccounts.isPending || bulkDeleteAccounts.isPending;

  const toggleSelectAllAccounts = () => {
    if (allAccountsSelected) clearAccountSelection();
    else setSelectedAccountIds(new Set(sortedAccounts.map((a) => a.id)));
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

  return (
    <>
      <Link
        to="/providers"
        className="mb-5 inline-flex items-center gap-2 text-sm font-medium text-[var(--text-muted)] transition-colors hover:text-[var(--text)]"
      >
        <ArrowLeft className="h-4 w-4" />
        Back to Providers
      </Link>

      <header className="mb-7 flex items-start gap-4">
        <ProviderIcon provider={provider} size={56} />
        <div className="min-w-0 flex-1">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <h1 className="font-display text-3xl font-semibold tracking-tight">{provider.display_name}</h1>
              <p className="mt-1 text-sm text-[var(--text-muted)]">
                {myAccounts.length} connected {myAccounts.length === 1 ? "account" : "accounts"}
              </p>
            </div>
            {provider.custom && (
              <Button
                variant="ghost"
                className="h-8 shrink-0 px-3 text-xs text-[color:var(--color-danger)] hover:bg-[color:var(--color-danger)]/10"
                onClick={() => setDeleteProviderOpen(true)}
                title="Delete this custom provider"
              >
                <Trash2 className="h-3.5 w-3.5" />
                Delete provider
              </Button>
            )}
          </div>
          <div className="mt-2 flex flex-wrap gap-1">
            {(provider.service_kinds ?? []).map((k) => (
              <Badge key={k} tone="accent">
                {k}
              </Badge>
            ))}
            {provider.deprecated && (
              <Badge tone="warning" title={provider.notice || "Account may be restricted"}>
                <AlertTriangle className="mr-1 h-3 w-3" />
                unofficial
              </Badge>
            )}
            {provider.auth_kind === "none" && (
              <Badge tone="accent">free</Badge>
            )}
          </div>
          {provider.custom && provider.base_url && (
            <BaseURLDisplay baseURL={provider.base_url} dialect={provider.dialect} />
          )}
        </div>
      </header>

      {provider.deprecated && provider.notice && (
        <div className="mb-4 flex items-start gap-2.5 rounded-lg border border-[color:var(--color-warning)]/25 bg-[color:var(--color-warning)]/8 px-4 py-3 text-xs leading-relaxed text-[color:var(--color-warning)]">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span>{provider.notice}</span>
        </div>
      )}

      <div className="space-y-6">
        <Card>
          <CardHeader
            title="Connected accounts"
            action={
              <div className="flex items-center gap-2">
                {myAccounts.length > 0 && (
                  <Button
                    variant="ghost"
                    className="h-8 px-3 text-xs"
                    onClick={runTestAll}
                    disabled={testingAll}
                  >
                    <CheckCircle className={`h-3.5 w-3.5 ${testingAll ? "animate-pulse" : ""}`} />
                    {testingAll
                      ? `Testing ${Object.values(testResults).filter((r) => r.status !== "testing").length}/${myAccounts.length}`
                      : "Test all"}
                  </Button>
                )}
                {isKiro && (
                  <Button variant="ghost" className="h-8 px-3 text-xs" onClick={() => setKiroOpen(true)}>
                    <Plug className="h-3.5 w-3.5" />
                    Connect Kiro
                  </Button>
                )}
                {isQoder && (
                  <Button variant="ghost" className="h-8 px-3 text-xs" onClick={() => setQoderOpen(true)}>
                    <Plug className="h-3.5 w-3.5" />
                    Connect Qoder
                  </Button>
                )}
                {isKilocode && (
                  <Button variant="ghost" className="h-8 px-3 text-xs" onClick={() => setKilocodeOpen(true)}>
                    <Plug className="h-3.5 w-3.5" />
                    Connect Kilo Code
                  </Button>
                )}
                {isKimchi && (
                  <Button variant="ghost" className="h-8 px-3 text-xs" onClick={() => setKimchiOpen(true)}>
                    <Plus className="h-3.5 w-3.5" />
                    Connect Kimchi
                  </Button>
                )}
                {isCodebuddy && (
                  <Button variant="ghost" className="h-8 px-3 text-xs" onClick={() => setCodebuddyOpen(true)}>
                    <Plug className="h-3.5 w-3.5" />
                    Connect CodeBuddy
                  </Button>
                )}
                {isCursor && (
                  <Button variant="ghost" className="h-8 px-3 text-xs" onClick={() => setCursorOpen(true)}>
                    <Plug className="h-3.5 w-3.5" />
                    Connect Cursor
                  </Button>
                )}
                {isCommandCode && (
                  <Button variant="ghost" className="h-8 px-3 text-xs" onClick={() => setCommandcodeOpen(true)}>
                    <Plug className="h-3.5 w-3.5" />
                    Connect CLI
                  </Button>
                )}
                {!hasCustomModal && oauthProvider && (
                  <Button variant="ghost" className="h-8 px-3 text-xs" onClick={() => setOauthOpen(true)}>
                    <Plug className="h-3.5 w-3.5" />
                    Connect {provider.display_name}
                  </Button>
                )}
                {supportsManualConnect && (
                  <Button variant="ghost" className="h-8 px-3 text-xs" onClick={() => setAddKeyOpen(true)}>
                    <Plus className="h-3.5 w-3.5" />
                    {provider.auth_kind === "none" ? "Connect" : "Add API key"}
                  </Button>
                )}
                {supportsBulkUpload && (
                  <Button variant="ghost" className="h-8 px-3 text-xs" onClick={() => setBulkOpen(true)}>
                    <Layers className="h-3.5 w-3.5" />
                    Bulk add
                  </Button>
                )}
              </div>
            }
          />
          {routing.data && (
            <RoutingControls
              settings={routing.data}
              saving={updateRouting.isPending}
              onUpdate={(patch) => updateRouting.mutate(patch)}
            />
          )}
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
              <div className="flex flex-wrap items-center gap-2 border-t border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-2.5">
                <label className="flex cursor-pointer items-center gap-2 text-xs text-[var(--text-muted)]">
                  <input
                    type="checkbox"
                    className="h-3.5 w-3.5 rounded border-[var(--border)] accent-[var(--color-accent-500)]"
                    checked={allAccountsSelected}
                    ref={(el) => { if (el) el.indeterminate = someAccountsSelected; }}
                    onChange={toggleSelectAllAccounts}
                  />
                  Select all
                </label>
                {selectedList.length > 0 ? (
                  <>
                    <span className="text-xs text-[var(--text-muted)]">{selectedList.length} selected</span>
                    <div className="flex-1" />
                    <Button variant="ghost" className="h-7 px-2 text-xs" onClick={handleBulkEnable} disabled={bulkBusy}>
                      <ToggleRight className="h-3.5 w-3.5 text-emerald-500" />
                      Enable
                    </Button>
                    <Button variant="ghost" className="h-7 px-2 text-xs" onClick={handleBulkDisable} disabled={bulkBusy}>
                      <ToggleLeft className="h-3.5 w-3.5" />
                      Disable
                    </Button>
                    <Button variant="ghost" className="h-7 px-2 text-xs" onClick={handleBulkDeleteAccounts} disabled={bulkBusy}>
                      <Trash2 className="h-3.5 w-3.5 text-red-500" />
                      Delete
                    </Button>
                    <Button variant="ghost" className="h-7 px-2 text-xs" onClick={clearAccountSelection} disabled={bulkBusy}>
                      Clear
                    </Button>
                  </>
                ) : (
                  <span className="text-xs text-[var(--text-muted)]">Select accounts for bulk actions</span>
                )}
              </div>
              <div className="divide-y divide-[var(--border)]">
                {sortedAccounts.map((a, i) => (
                  <AccountRow
                    key={a.id}
                    account={a}
                    index={i}
                    total={sortedAccounts.length}
                    pools={pools.data?.pools ?? []}
                    selected={selectedAccountIds.has(a.id)}
                    onToggleSelect={() => toggleAccountSelection(a.id)}
                    onDelete={() => remove.mutate(a.id)}
                    onMoveUp={() => moveAccount(a.id, "up")}
                    onMoveDown={() => moveAccount(a.id, "down")}
                    onTest={() => runTest(a.id)}
                    onUpdateProxy={(patch) => updateAccount.mutate({ id: a.id, patch })}
                    testResult={testResults[a.id]}
                    disabledByBatch={testingAll}
                    quotaData={quotaMap[a.id]}
                  />
                ))}
              </div>
            </>
          )}
        </Card>

        {/* Available Models */}
        {models.data && (
          <Card>
            <CardHeader
              title="Available Models"
              description={`${modelList.length} model${modelList.length === 1 ? "" : "s"} configured for this provider.`}
              action={
                provider?.custom ? (
                  <Button
                    variant="secondary"
                    className="h-8 px-3 text-xs"
                    disabled={importModelsMut.isPending}
                    onClick={() => importModelsMut.mutate()}
                    title="Fetch the upstream /models listing and register each model"
                  >
                    <Download className="h-3.5 w-3.5" />
                    {importModelsMut.isPending ? "Fetching…" : "Fetch from /models"}
                  </Button>
                ) : undefined
              }
            />
            {modelList.length > 0 && (
            <div className="flex flex-col gap-3 border-t border-[var(--border)] bg-[var(--bg-subtle)] px-6 py-3 sm:flex-row sm:items-center sm:justify-between">
              <div className="relative w-full max-w-sm">
                <Search className="absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--text-muted)]" />
                <Input
                  placeholder="Search models..."
                  value={modelSearchQuery}
                  onChange={(e) => setModelSearchQuery(e.target.value)}
                  className="pl-9 h-8 text-sm"
                />
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <label className="flex cursor-pointer items-center gap-1.5 text-xs text-[var(--text-muted)]">
                  <input
                    type="checkbox"
                    className="h-3.5 w-3.5 rounded border-[var(--border)] accent-[var(--color-accent-500)]"
                    checked={filteredModels.length > 0 && filteredModels.every((m) => selectedModelIds.has(m.id))}
                    ref={(el) => {
                      if (el) {
                        const someSelected = filteredModels.some((m) => selectedModelIds.has(m.id));
                        const allSelected = filteredModels.length > 0 && filteredModels.every((m) => selectedModelIds.has(m.id));
                        el.indeterminate = someSelected && !allSelected;
                      }
                    }}
                    onChange={(e) => {
                      setSelectedModelIds((prev) => {
                        const next = new Set(prev);
                        if (e.target.checked) {
                          filteredModels.forEach((m) => next.add(m.id));
                        } else {
                          filteredModels.forEach((m) => next.delete(m.id));
                        }
                        return next;
                      });
                    }}
                  />
                  Select all
                </label>
                {selectedModelIds.size > 0 && (
                  <span className="text-xs text-[var(--text-muted)]">{selectedModelIds.size} selected</span>
                )}
                <Button
                  variant="ghost"
                  className="h-8 px-3 text-xs"
                  onClick={() => enableModelsMut.mutate([...selectedModelIds])}
                  disabled={enableModelsMut.isPending || selectedModelIds.size === 0}
                >
                  <ToggleRight className="h-3.5 w-3.5 text-accent-500" />
                  Enable
                </Button>
                <Button
                  variant="ghost"
                  className="h-8 px-3 text-xs"
                  onClick={() => disableModelsMut.mutate([...selectedModelIds])}
                  disabled={disableModelsMut.isPending || selectedModelIds.size === 0}
                >
                  <ToggleLeft className="h-3.5 w-3.5 text-[var(--text-muted)]" />
                  Disable
                </Button>
                {selectedModelIds.size > 0 && (
                  <Button
                    variant="ghost"
                    className="h-8 px-2 text-xs"
                    onClick={() => setSelectedModelIds(new Set())}
                  >
                    Clear
                  </Button>
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
              <div className={`grid grid-cols-1 gap-px overflow-hidden border-t border-[var(--border)] bg-[var(--border)] sm:grid-cols-2 lg:grid-cols-3 ${totalModelPages <= 1 ? "rounded-b-2xl" : ""}`}>
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

  return (
    <div className="border-t border-[var(--border)] bg-[var(--bg-subtle)] px-6 py-3">
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex items-center gap-2">
          <Route className="h-3.5 w-3.5 text-[var(--text-muted)]" />
          <span className="text-xs font-medium text-[var(--text-muted)]">Routing</span>
        </div>
        <select
          value={mode}
          disabled={saving}
          onChange={(e) => onUpdate({ routing_strategy: e.target.value })}
          className="h-7 rounded-md border border-[var(--border)] bg-[var(--bg)] px-2 text-xs text-[var(--text)] outline-none focus:border-[var(--color-accent-500)] focus:ring-1 focus:ring-[var(--color-accent-500)]/30"
        >
          {routingOptions.map((o) => (
            <option key={o.value} value={o.value}>{o.label}</option>
          ))}
        </select>

        {rotatesAccounts && (
          <>
            <span className="text-[var(--border)]">·</span>
            <div className="flex items-center gap-1.5">
              <span className="text-xs text-[var(--text-muted)]">Sticky</span>
              <Input
                type="number"
                min={1}
                max={100}
                value={stickyLimit}
                disabled={saving}
                onChange={(e) => onUpdate({ sticky_limit: parseInt(e.target.value, 10) || 1 })}
                className="h-7 w-16 text-center text-xs"
              />
            </div>
          </>
        )}

        {mode === "smart-round-robin" && (
          <>
            <span className="text-[var(--border)]">·</span>
            <div className="flex items-center gap-1.5">
              <span className="text-xs text-[var(--text-muted)]">Affinity TTL</span>
              <Input
                type="number"
                min={1}
                max={168}
                value={ttlHours}
                disabled={saving}
                onChange={(e) => onUpdate({ affinity_ttl_minutes: (parseInt(e.target.value, 10) || 1) * 60 })}
                className="h-7 w-16 text-center text-xs"
              />
              <span className="text-xs text-[var(--text-muted)]">h</span>
            </div>
          </>
        )}
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
  quotaData,
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
  quotaData?: QuotaAccount;
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

  const hasQuota = !!quotaData?.upstream_quotas && quotaData.upstream_quotas.length > 0;
  const boundPool = pools.find((p) => p.id === a.proxy_pool_id);

  return (
    <div className={`px-4 py-3 ${a.disabled ? "opacity-60" : ""} ${selected ? "bg-accent-50/50 dark:bg-accent-900/10" : ""}`}>
      {/* Header row */}
      <div className="flex items-center justify-between gap-3">
        {onToggleSelect && (
          <input
            type="checkbox"
            checked={!!selected}
            onChange={onToggleSelect}
            aria-label={`Select ${a.label || a.provider}`}
            className="h-3.5 w-3.5 shrink-0 rounded border-[var(--border)] accent-[var(--color-accent-500)]"
          />
        )}
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-sm font-medium">{a.label || a.provider}</span>
            <Badge tone="neutral">{a.auth_kind === "oauth" ? "OAuth" : "API Key"}</Badge>
            {a.disabled && <Badge tone="danger">disabled</Badge>}
            {a.needs_reconnect && (
              <span
                className="inline-flex items-center gap-1 rounded-full bg-amber-100 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-400"
                title="The OAuth refresh token was revoked by the provider. Delete this account and reconnect."
              >
                <RefreshCw className="h-3 w-3" />
                reconnect required
              </span>
            )}
            {testResult?.status === "ok" && (
              <span className="inline-flex items-center gap-1 rounded-full bg-emerald-100 px-1.5 py-0.5 text-[10px] font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400">
                ✓ ok
              </span>
            )}
            {testResult?.status === "error" && (
              <span
                className="inline-flex items-center gap-1 rounded-full bg-red-100 px-1.5 py-0.5 text-[10px] font-medium text-red-700 dark:bg-red-900/30 dark:text-red-400"
                title={testResult.message}
              >
                ✗ {testResult.message ? "failed" : "error"}
              </span>
            )}
            {testResult?.status === "testing" && (
              <span className="inline-flex items-center gap-1 rounded-full bg-[var(--bg-subtle)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--text-muted)]">
                testing…
              </span>
            )}
          </div>
          {testResult?.status === "error" && testResult.message && (
            <div className="mt-2 flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-3 py-2 dark:border-red-900/40 dark:bg-red-900/15">
              <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-red-500 dark:text-red-400" />
              <div className="min-w-0">
                <p className="text-[11px] font-medium text-red-700 dark:text-red-300">Connection failed</p>
                <p className="mt-0.5 break-words text-[11px] leading-relaxed text-red-600/90 dark:text-red-400/90">
                  {testResult.message}
                </p>
              </div>
            </div>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-0.5">
          <button onClick={onTest} disabled={testing || disabledByBatch}
            className="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] disabled:opacity-40" title="Test credentials">
            <CheckCircle className={`h-4 w-4 ${testing ? "animate-pulse" : ""}`} />
          </button>
          <button onClick={() => onUpdateProxy({ disabled: !a.disabled })}
            className="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text)]"
            title={a.disabled ? "Enable" : "Disable"}>
            {a.disabled ? <ToggleLeft className="h-4 w-4" /> : <ToggleRight className="h-4 w-4 text-emerald-500 dark:text-emerald-400" />}
          </button>
          <button onClick={onDelete}
            className="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-red-500/10 hover:text-red-500" title="Delete">
            <Trash2 className="h-4 w-4" />
          </button>
        </div>
      </div>

      {/* Settings row: Priority + Proxy Pool */}
      <div className="mt-2 flex flex-wrap items-center gap-3">
        {/* Priority */}
        <div className="flex items-center gap-1.5">
          <span className="text-[11px] text-[var(--text-muted)]">Priority:</span>
          <div className="inline-flex items-center overflow-hidden rounded-md border border-[var(--border)] bg-[var(--bg-subtle)]">
            <button onClick={onMoveUp} disabled={index === 0}
              className="flex h-6 w-6 items-center justify-center text-[var(--text-muted)] hover:bg-[var(--bg-elevated)] disabled:opacity-25 disabled:cursor-not-allowed transition-colors">
              <ArrowUp className="h-3 w-3" />
            </button>
            <input
              type="number"
              value={localPriority}
              onChange={(e) => {
                const val = parseInt(e.target.value, 10);
                if (!isNaN(val) && val >= 0) setLocalPriority(val);
              }}
              onBlur={commitPriority}
              onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
              className="h-6 w-10 border-x border-[var(--border)] bg-transparent text-center text-xs font-medium text-[var(--text)] focus:outline-none focus:bg-[var(--bg)] [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
              min={0}
              max={999}
            />
            <button onClick={onMoveDown} disabled={index === total - 1}
              className="flex h-6 w-6 items-center justify-center text-[var(--text-muted)] hover:bg-[var(--bg-elevated)] disabled:opacity-25 disabled:cursor-not-allowed transition-colors">
              <ArrowDown className="h-3 w-3" />
            </button>
          </div>
        </div>

        {/* Proxy Pool */}
        <div className="flex items-center gap-1.5">
          <span className="text-[11px] text-[var(--text-muted)]">Proxy:</span>
          <select
            value={a.proxy_pool_id || ""}
            onChange={(e) => onUpdateProxy({ proxy_pool_id: e.target.value || "" })}
            className="h-6 rounded-md border border-[var(--border)] bg-[var(--bg-subtle)] pl-1.5 pr-6 text-xs focus:border-accent-500 focus:outline-none"
          >
            <option value="">Direct (no proxy)</option>
            {pools.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}{!p.is_active ? " (inactive)" : ""}
              </option>
            ))}
          </select>
          {boundPool && (
            <span className={`inline-flex items-center gap-1 rounded-full px-1.5 py-0.5 text-[10px] font-medium ${
              boundPool.test_status === "active"
                ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400"
                : boundPool.test_status === "error"
                  ? "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400"
                  : "bg-[var(--bg-subtle)] text-[var(--text-muted)]"
            }`}>
              {boundPool.test_status === "active" ? "✓" : boundPool.test_status === "error" ? "✗" : "?"}
              {boundPool.type !== "http" && ` ${boundPool.type}`}
            </span>
          )}
        </div>
      </div>

      {/* Quota / credit info */}
      {hasQuota && quotaData && (
        <div className="mt-2.5 rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] px-3 py-2.5">
          <div className="mb-2 flex items-center gap-2">
            <Zap className="h-3.5 w-3.5 text-[var(--text-muted)]" />
            <span className="text-xs font-medium">
              {quotaData.plan_name ? `${quotaData.plan_name} — Credits` : "Credits & Quota"}
            </span>
          </div>
          {quotaData.upstream_quotas && (
            <div className="space-y-2">
              {quotaData.upstream_quotas.map((q) => (
                <QuotaBarInline key={q.resource_type} quota={q} />
              ))}
            </div>
          )}
        </div>
      )}
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

  const resetDate = q.reset_at ? new Date(q.reset_at) : null;
  const resetLabel = resetDate && !isNaN(resetDate.getTime())
    ? resetDate.toLocaleDateString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })
    : null;

  return (
    <div>
      <div className="mb-1 flex items-center justify-between text-[11px]">
        <span className="font-medium text-[var(--text)]">{label}</span>
        <div className="flex items-center gap-2">
          {resetLabel && (
            <span className="text-[10px] text-[var(--text-muted)]">resets {resetLabel}</span>
          )}
          <span className="tabular-nums">
            {q.used.toLocaleString()} / {q.limit.toLocaleString()}
            <span className="ml-1 text-[var(--text-muted)]">({q.remaining.toLocaleString()} left)</span>
          </span>
        </div>
      </div>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-[var(--bg-subtle)]">
        <div className={`h-full rounded-full ${tone}`} style={{ width: `${Math.max(2, pct)}%` }} />
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
  const isCustom = provider.id === "custom-openai" || provider.id === "custom-anthropic" || !!provider.custom;
  const hasRegions = (provider.regions?.length ?? 0) > 0;
  const requiresBaseURL = isCustom;
  // Generic providers expose an optional shared base URL; region/Cloudflare
  // providers use their own dedicated control instead.
  const showBaseURL = !hasRegions && !isCloudflare;
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
  const requiresBaseURL = provider.id === "custom-openai" || provider.id === "custom-anthropic";
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

// ---- Account quota card -----------------------------------------------------


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
    <div className={`group relative flex flex-col justify-between bg-[var(--bg-elevated)] p-4 transition-all hover:bg-[var(--bg-subtle)] ${disabled ? "opacity-50 grayscale" : ""} ${selected ? "ring-1 ring-inset ring-accent-500/60" : ""}`}>
      <div className="mb-3 flex items-start justify-between">
        <div className="flex items-center gap-2">
          {onToggleSelect && (
            <input
              type="checkbox"
              className="h-3.5 w-3.5 rounded border-[var(--border)] accent-[var(--color-accent-500)]"
              checked={!!selected}
              onChange={onToggleSelect}
              title="Select model"
            />
          )}
          <div className={`h-1.5 w-1.5 rounded-full ${disabled ? "bg-ink-400 dark:bg-ink-600" : "bg-accent-500 shadow-[0_0_8px_var(--color-accent-500)]"}`} />
          <span className="text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
            {model.kind || "Model"}
          </span>
        </div>
        <div className="flex items-center gap-0.5">
          {onToggleDisable && (
            <button
              onClick={onToggleDisable}
              className="flex h-7 w-7 items-center justify-center rounded bg-transparent text-[var(--text-muted)] transition-colors hover:bg-ink-100 hover:text-[var(--text)] dark:hover:bg-ink-800"
              title={disabled ? "Enable model" : "Disable model"}
            >
              {disabled ? <ToggleLeft className="h-4 w-4" /> : <ToggleRight className="h-4 w-4 text-accent-500" />}
            </button>
          )}
          <button
            onClick={handleCopy}
            className="flex h-7 w-7 items-center justify-center rounded bg-transparent text-[var(--text-muted)] opacity-100 transition-all hover:bg-ink-100 hover:text-[var(--text)] dark:hover:bg-ink-800 sm:opacity-0 sm:group-hover:opacity-100"
            title="Copy model path"
          >
            {copied ? (
              <CheckCircle className="h-3.5 w-3.5 text-green-500" />
            ) : (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect width="14" height="14" x="8" y="8" rx="2" ry="2" /><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" /></svg>
            )}
          </button>
        </div>
      </div>
      <div>
        <code className="block truncate font-mono text-xs text-[var(--text)] tracking-tight" title={fullModel}>
          {fullModel}
        </code>
        {model.name && model.name !== model.id && (
          <span className="mt-1 block truncate text-[10px] text-[var(--text-muted)]" title={model.name}>
            {model.name}
          </span>
        )}
      </div>
    </div>
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
