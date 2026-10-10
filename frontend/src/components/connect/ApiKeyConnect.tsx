import { useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertCircle, Check, CheckCircle, CheckCircle2, FileText, Layers, Upload, XCircle } from "lucide-react";
import { api, type AccountInput, type BulkAccountResult, type Provider } from "../../lib/api";
import { parseKeys } from "../../lib/bulk";
import { cn } from "@/lib/utils";
import { useToast } from "../Toast";
import {
  ConnectDialog,
  ExternalTextLink,
  FormError,
  InlineCode,
  PrimaryAction,
  SecondaryAction,
  Steps,
  TextAreaField,
  TextField,
} from "./ConnectKit";

// Rules that decide which fields a provider's API-key form needs. Shared by
// the single-key and bulk dialogs so the two never disagree.
function keyFormRules(provider: Provider) {
  const supportsApiKey = provider.auth_modes.includes("api_key") || provider.auth_kind === "api_key";
  const supportsNone = provider.auth_modes.includes("none") || provider.auth_kind === "none";
  const isAzure = provider.id === "azure";
  const isCloudflare = provider.id === "cloudflare-ai";
  const isQoder = provider.id === "qoder";
  const hasRegions = (provider.regions?.length ?? 0) > 0;
  // User-created custom provider instances carry their own base URL, so their
  // accounts inherit it; only the built-in generic gateways need one per key.
  const inheritsBaseURL = !!provider.custom && !!provider.base_url;
  const requiresBaseURL = !inheritsBaseURL && (provider.id === "custom-openai" || provider.id === "custom-anthropic");
  const keyPlaceholder = isCloudflare ? "Cloudflare API token" : isQoder ? "pt-…" : provider.id === "xai" ? "xai-…" : "sk-…";
  return {
    isNoAuth: supportsNone && !supportsApiKey,
    keyOptional: supportsNone && supportsApiKey,
    isAzure,
    isCloudflare,
    isQoder,
    hasRegions,
    inheritsBaseURL,
    requiresBaseURL,
    keyPlaceholder,
  };
}

function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

function CloudflareSteps() {
  return (
    <Steps
      title="Cloudflare Workers AI setup"
      steps={[
        <>
          Create an API token at <ExternalTextLink href="https://dash.cloudflare.com/profile/api-tokens">dash.cloudflare.com</ExternalTextLink> with the{" "}
          <InlineCode>Workers AI</InlineCode> template.
        </>,
        <>
          Copy your Account ID from the right sidebar of the <ExternalTextLink href="https://dash.cloudflare.com">Cloudflare dashboard</ExternalTextLink>.
        </>,
      ]}
    />
  );
}

function RegionSelect({ provider, value, onChange }: { provider: Provider; value: string; onChange: (v: string) => void }) {
  return (
    <label className="block space-y-1.5">
      <span className="block text-[12.5px] font-medium text-fg">Region</span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="h-9 w-full rounded-lg border border-line bg-surface px-2.5 text-[13px] text-fg focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/25"
      >
        {(provider.regions ?? []).map((r) => (
          <option key={r.id} value={r.id}>
            {r.label}
          </option>
        ))}
      </select>
    </label>
  );
}

// ── Single key ──────────────────────────────────────────────────────────────

export function ApiKeyConnect({ provider, onClose }: { provider: Provider; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const rules = keyFormRules(provider);
  const [form, setForm] = useState({
    label: "",
    apiKey: "",
    baseURL: "",
    region: provider.default_region ?? provider.regions?.[0]?.id ?? "",
    accountID: "",
    azureEndpoint: "",
    azureDeployment: "",
    azureAPIVersion: "2024-10-01-preview",
    azureOrganization: "",
  });
  const [check, setCheck] = useState<{ status: "idle" | "checking" | "ok" | "error"; message?: string }>({ status: "idle" });
  const set = (key: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => {
    setForm((f) => ({ ...f, [key]: e.target.value }));
    setCheck({ status: "idle" });
  };

  const input = (): AccountInput => ({
    provider: provider.id,
    label: form.label,
    api_key: form.apiKey || undefined,
    base_url: form.baseURL || undefined,
    region: rules.hasRegions ? form.region : undefined,
    account_id: form.accountID || undefined,
    azure_endpoint: form.azureEndpoint || undefined,
    azure_deployment: form.azureDeployment || undefined,
    azure_api_version: form.azureAPIVersion || undefined,
    azure_organization: form.azureOrganization || undefined,
  });

  const create = useMutation({
    mutationFn: () => api.createAccount(input()),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["accounts"] });
      toast.success("Account connected", "Credentials are encrypted and the account is ready for routing.");
      onClose();
    },
  });

  const complete =
    (rules.isNoAuth || rules.keyOptional || !!form.apiKey.trim()) &&
    (!rules.isCloudflare || !!form.accountID.trim()) &&
    (!rules.isAzure || (!!form.azureEndpoint.trim() && !!form.azureDeployment.trim())) &&
    (!rules.requiresBaseURL || !!form.baseURL.trim());

  const runCheck = async () => {
    setCheck({ status: "checking" });
    try {
      const res = await api.validateKey(input());
      setCheck({ status: res.status === "ok" ? "ok" : "error", message: res.message });
    } catch (e) {
      setCheck({ status: "error", message: (e as Error).message });
    }
  };

  return (
    <ConnectDialog
      title={rules.isNoAuth ? `Enable ${provider.display_name}` : `Add ${provider.display_name} API key`}
      description={rules.isNoAuth ? "No credentials needed — this creates an account so KeiRouter can route to it." : "The key is encrypted at rest and never shown again."}
      logo={{ icon: provider.icon, name: provider.display_name }}
      onClose={onClose}
      footer={
        <>
          {!rules.isNoAuth && (
            <SecondaryAction onClick={runCheck} disabled={!complete || check.status === "checking"}>
              <CheckCircle className={cn(check.status === "checking" && "animate-pulse")} />
              {check.status === "checking" ? "Checking…" : "Test key"}
            </SecondaryAction>
          )}
          <PrimaryAction type="submit" busy={create.isPending} disabled={!complete} onClick={() => create.mutate()}>
            {create.isPending ? "Adding…" : rules.isNoAuth ? "Enable" : "Add account"}
          </PrimaryAction>
        </>
      }
    >
      <form
        className="space-y-3.5"
        onSubmit={(e) => {
          e.preventDefault();
          if (complete) create.mutate();
        }}
      >
        {!rules.isNoAuth && (
          <TextField
            label="API key"
            optional={rules.keyOptional}
            type="password"
            autoComplete="off"
            value={form.apiKey}
            onChange={set("apiKey")}
            placeholder={rules.keyPlaceholder}
            className="font-mono"
            hint={provider.api_key_url ? <>Create one at <ExternalTextLink href={provider.api_key_url}>{hostOf(provider.api_key_url)}</ExternalTextLink>.</> : undefined}
          />
        )}
        <TextField label="Label" optional value={form.label} onChange={set("label")} placeholder="personal" hint="Shown in routing and usage, e.g. the team or person who owns the key." />
        {rules.isCloudflare && (
          <>
            <CloudflareSteps />
            <TextField label="Account ID" value={form.accountID} onChange={set("accountID")} placeholder="a1b2c3d4e5f6…" className="font-mono" />
          </>
        )}
        {rules.isAzure ? (
          <div className="space-y-3.5 rounded-xl border border-line bg-subtle p-3.5">
            <TextField label="Endpoint" value={form.azureEndpoint} onChange={set("azureEndpoint")} placeholder="https://your-resource.openai.azure.com" className="font-mono" />
            <TextField label="Deployment name" value={form.azureDeployment} onChange={set("azureDeployment")} placeholder="gpt-4o" className="font-mono" />
            <div className="grid gap-3.5 sm:grid-cols-2">
              <TextField label="API version" value={form.azureAPIVersion} onChange={set("azureAPIVersion")} className="font-mono" />
              <TextField label="Organization" optional value={form.azureOrganization} onChange={set("azureOrganization")} placeholder="org_…" className="font-mono" />
            </div>
          </div>
        ) : rules.hasRegions ? (
          <RegionSelect provider={provider} value={form.region} onChange={(v) => setForm((f) => ({ ...f, region: v }))} />
        ) : rules.inheritsBaseURL ? (
          <div className="space-y-1.5">
            <span className="block text-[12.5px] font-medium text-fg">Base URL</span>
            <p className="truncate rounded-lg border border-line bg-subtle px-3 py-2 font-mono text-[12px] text-fg-muted" title={provider.base_url}>
              {provider.base_url}
            </p>
            <p className="text-[12px] text-fg-muted">Inherited from this custom provider.</p>
          </div>
        ) : (
          !rules.isNoAuth && (
            <TextField
              label="Base URL"
              optional={!rules.requiresBaseURL}
              value={form.baseURL}
              onChange={set("baseURL")}
              placeholder="https://…/v1"
              className="font-mono"
              hint={rules.requiresBaseURL ? "The OpenAI- or Anthropic-compatible endpoint this key belongs to." : "Only for a proxy or self-hosted endpoint."}
            />
          )
        )}

        {check.status === "ok" && (
          <p role="status" className="flex items-center gap-1.5 text-[12.5px] font-medium text-ok">
            <CheckCircle2 className="h-4 w-4" />
            The key works.
          </p>
        )}
        {check.status === "error" && <FormError message={check.message || "The key was rejected."} />}
        <FormError message={create.error?.message} />
        {/* Enter submits from any field. */}
        <button type="submit" className="hidden" aria-hidden="true" tabIndex={-1} />
      </form>
    </ConnectDialog>
  );
}

// ── Bulk import ─────────────────────────────────────────────────────────────

// BulkKeyImport adds many keys at once. Shared settings (region, Cloudflare
// account, base URL) apply to every key; only the key and an optional inline
// label / base URL vary per line. Parsing is live, files can be loaded, and
// the backend's per-row outcome is shown afterwards.
export function BulkKeyImport({ provider, onClose }: { provider: Provider; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const rules = keyFormRules(provider);
  const [text, setText] = useState("");
  const [validate, setValidate] = useState(false);
  const [baseURL, setBaseURL] = useState(provider.base_url ?? "");
  const [region, setRegion] = useState(provider.default_region ?? provider.regions?.[0]?.id ?? "");
  const [accountID, setAccountID] = useState("");
  const [results, setResults] = useState<BulkAccountResult[] | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const showBaseURL = !rules.hasRegions && !rules.isCloudflare && !rules.inheritsBaseURL && !rules.isQoder;
  const noun = rules.isQoder ? "token" : "key";
  const parsed = useMemo(() => parseKeys(text), [text]);
  const ready = parsed.entries.length;

  const importMut = useMutation({
    mutationFn: () =>
      api.bulkCreateAccounts({
        provider: provider.id,
        base_url: showBaseURL && baseURL.trim() ? baseURL.trim() : undefined,
        region: rules.hasRegions ? region : undefined,
        account_id: rules.isCloudflare ? accountID.trim() : undefined,
        validate,
        items: parsed.entries.map((e) => ({ label: e.label || undefined, api_key: e.apiKey, base_url: e.baseURL })),
      }),
    onSuccess: (res) => {
      setResults(res.results);
      qc.invalidateQueries({ queryKey: ["accounts"] });
      if (res.failed === 0) toast.success("Import complete", `${res.created} ${noun}${res.created === 1 ? "" : "s"} added${res.skipped ? `, ${res.skipped} duplicate skipped` : ""}.`);
      else toast.error("Import finished with errors", `${res.created} added, ${res.failed} failed${res.skipped ? `, ${res.skipped} skipped` : ""}.`);
    },
    onError: (e: Error) => toast.error("Import failed", e.message),
  });

  const canImport = ready > 0 && !importMut.isPending && (!rules.requiresBaseURL || !!baseURL.trim()) && (!rules.isCloudflare || !!accountID.trim());

  const onFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    const content = await file.text();
    setText((prev) => (prev.trim() ? `${prev.replace(/\s+$/, "")}\n${content}` : content));
    e.target.value = "";
  };

  if (results) {
    const created = results.filter((r) => r.status === "created").length;
    const skipped = results.filter((r) => r.status === "skipped").length;
    const failed = results.filter((r) => r.status === "error").length;
    return (
      <ConnectDialog
        title="Import results"
        description={`${created} added${skipped ? ` · ${skipped} skipped` : ""}${failed ? ` · ${failed} failed` : ""}`}
        logo={{ icon: provider.icon, name: provider.display_name }}
        width="lg"
        onClose={onClose}
        footer={
          <>
            <SecondaryAction
              onClick={() => {
                setResults(null);
                setText("");
              }}
            >
              <Layers />
              Import more
            </SecondaryAction>
            <PrimaryAction onClick={onClose}>
              <Check />
              Done
            </PrimaryAction>
          </>
        }
      >
        <ul className="divide-y divide-line overflow-hidden rounded-xl border border-line">
          {results.map((r) => (
            <li key={r.index} className="flex items-center gap-3 px-3 py-2 text-[12.5px]">
              {r.status === "created" ? (
                <CheckCircle className="h-4 w-4 shrink-0 text-ok" />
              ) : r.status === "skipped" ? (
                <AlertCircle className="h-4 w-4 shrink-0 text-warn" />
              ) : (
                <XCircle className="h-4 w-4 shrink-0 text-bad" />
              )}
              <span className="w-9 shrink-0 font-mono text-fg-faint">#{r.index + 1}</span>
              <span className="min-w-0 flex-1 truncate font-medium text-fg">{r.label || "Unlabeled"}</span>
              {r.error && (
                <span className="max-w-[55%] truncate text-fg-muted" title={r.error}>
                  {r.error}
                </span>
              )}
            </li>
          ))}
        </ul>
      </ConnectDialog>
    );
  }

  return (
    <ConnectDialog
      title={`Import ${provider.display_name} ${rules.isQoder ? "tokens" : "API keys"}`}
      description={`Paste one ${noun} per line, or load a .txt / .csv file.`}
      logo={{ icon: provider.icon, name: provider.display_name }}
      width="lg"
      onClose={onClose}
      footer={
        <>
          <SecondaryAction onClick={onClose}>Cancel</SecondaryAction>
          <PrimaryAction onClick={() => importMut.mutate()} disabled={!canImport} busy={importMut.isPending}>
            {!importMut.isPending && <Upload />}
            {importMut.isPending ? "Importing…" : ready ? `Import ${ready} ${noun}${ready === 1 ? "" : "s"}` : "Import"}
          </PrimaryAction>
        </>
      }
    >
      <div className="space-y-3.5">
        {(rules.hasRegions || rules.isCloudflare || showBaseURL) && (
          <div className="space-y-3.5 rounded-xl border border-line bg-subtle p-3.5">
            <p className="text-[12px] font-medium text-fg-muted">Applied to every {noun}</p>
            {rules.hasRegions && <RegionSelect provider={provider} value={region} onChange={setRegion} />}
            {rules.isCloudflare && (
              <>
                <CloudflareSteps />
                <TextField label="Account ID" value={accountID} onChange={(e) => setAccountID(e.target.value)} placeholder="a1b2c3d4e5f6…" className="font-mono" />
              </>
            )}
            {showBaseURL && (
              <TextField
                label="Base URL"
                optional={!rules.requiresBaseURL}
                value={baseURL}
                onChange={(e) => setBaseURL(e.target.value)}
                placeholder="https://…/v1"
                className="font-mono"
              />
            )}
          </div>
        )}
        {rules.inheritsBaseURL && (
          <p className="text-[12.5px] text-fg-muted">
            Keys use this provider's base URL <InlineCode>{provider.base_url}</InlineCode>.
          </p>
        )}

        <div className="space-y-1.5">
          <div className="flex items-center justify-between">
            <span className="text-[12.5px] font-medium text-fg">{rules.isQoder ? "Personal Access Tokens" : "API keys"}</span>
            <button
              type="button"
              onClick={() => fileRef.current?.click()}
              className="inline-flex h-7 items-center gap-1.5 rounded-lg border border-line px-2 text-[12px] text-fg-muted transition-colors hover:bg-hover hover:text-fg"
            >
              <FileText className="h-3.5 w-3.5" />
              Load file
            </button>
            <input ref={fileRef} type="file" accept=".txt,.csv,text/plain,text/csv" className="hidden" onChange={onFile} />
          </div>
          <TextAreaField
            aria-label={rules.isQoder ? "Personal Access Tokens" : "API keys"}
            value={text}
            onChange={(e) => setText(e.target.value)}
            rows={8}
            placeholder={`${rules.keyPlaceholder}\nlabel-2, ${rules.keyPlaceholder}\n# lines starting with # are ignored`}
            hint={
              <>
                One {noun} per line. Optional label: <InlineCode>label, {noun}</InlineCode>. Blank lines and <InlineCode>#</InlineCode> comments are ignored.
              </>
            }
          />
        </div>

        {text.trim() && (
          <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[12.5px]" aria-live="polite">
            <span className={cn("font-medium", ready ? "text-ok" : "text-fg-muted")}>{ready} ready</span>
            {parsed.duplicates > 0 && <span className="text-warn">{parsed.duplicates} duplicate</span>}
            {parsed.errors.length > 0 && <span className="text-bad">{parsed.errors.length} invalid</span>}
            {parsed.errors.slice(0, 2).map((err) => (
              <span key={err.line} className="text-fg-muted">
                line {err.line}: {err.message}
              </span>
            ))}
          </p>
        )}

        <label className="flex cursor-pointer items-start gap-2.5 rounded-xl border border-line px-3.5 py-3">
          <input
            type="checkbox"
            checked={validate}
            onChange={(e) => setValidate(e.target.checked)}
            className="mt-0.5 h-4 w-4 rounded border-line accent-[var(--color-accent-500)]"
          />
          <span className="text-[12.5px] leading-5 text-fg-muted">
            <span className="font-medium text-fg">Test each {noun} before saving.</span> Slower for large batches and may hit the provider's rate limits.
          </span>
        </label>
      </div>
    </ConnectDialog>
  );
}
