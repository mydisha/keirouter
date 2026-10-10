import { useId, useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertCircle, Check, CheckCircle, CheckCircle2, FileText, Layers, Upload, XCircle } from "lucide-react";
import { api, type AccountInput, type BulkAccountResult, type Provider } from "../../lib/api";
import { parseKeys } from "../../lib/bulk";
import { cn } from "@/lib/utils";
import { useToast } from "../Toast";
import {
  Advanced,
  ConnectDialog,
  ExternalTextLink,
  FormError,
  InlineCode,
  PrimaryAction,
  SecondaryAction,
  SelectField,
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
      steps={[
        <>
          Create a token with the <InlineCode>Workers AI</InlineCode> template at{" "}
          <ExternalTextLink href="https://dash.cloudflare.com/profile/api-tokens">dash.cloudflare.com</ExternalTextLink>
        </>,
        <>
          Copy your Account ID from the <ExternalTextLink href="https://dash.cloudflare.com">dashboard</ExternalTextLink> sidebar
        </>,
      ]}
    />
  );
}

function RegionSelect({ provider, value, onChange }: { provider: Provider; value: string; onChange: (v: string) => void }) {
  return (
    <SelectField label="Region" value={value} onChange={(e) => onChange(e.target.value)}>
      {(provider.regions ?? []).map((r) => (
        <option key={r.id} value={r.id}>
          {r.label}
        </option>
      ))}
    </SelectField>
  );
}

// ── Single key ──────────────────────────────────────────────────────────────

export function ApiKeyConnect({ provider, onClose }: { provider: Provider; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const rules = keyFormRules(provider);
  const formId = useId();
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
  // Required-field errors appear only after a submit attempt.
  const [showErrors, setShowErrors] = useState(false);
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

  // Same completeness rules as before, broken out per field so each missing
  // value can be reported next to its input.
  const missing = {
    apiKey: !(rules.isNoAuth || rules.keyOptional || !!form.apiKey.trim()),
    accountID: rules.isCloudflare && !form.accountID.trim(),
    azureEndpoint: rules.isAzure && !form.azureEndpoint.trim(),
    azureDeployment: rules.isAzure && !form.azureDeployment.trim(),
    baseURL: rules.requiresBaseURL && !form.baseURL.trim(),
  };
  const complete = !Object.values(missing).some(Boolean);
  const err = (k: keyof typeof missing, text: string) => (showErrors && missing[k] ? text : undefined);

  const submit = (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (create.isPending) return;
    if (!complete) {
      const formEl = e.currentTarget;
      setShowErrors(true);
      window.requestAnimationFrame(() => formEl.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
      return;
    }
    create.mutate();
  };

  const runCheck = async () => {
    setCheck({ status: "checking" });
    try {
      const res = await api.validateKey(input());
      setCheck({ status: res.status === "ok" ? "ok" : "error", message: res.message });
    } catch (e) {
      setCheck({ status: "error", message: (e as Error).message });
    }
  };

  const keyHint = provider.api_key_url ? (
    <>
      Get one at <ExternalTextLink href={provider.api_key_url}>{hostOf(provider.api_key_url)}</ExternalTextLink>
    </>
  ) : undefined;

  const labelField = <TextField label="Label" optional value={form.label} onChange={set("label")} placeholder="personal" />;
  const optionalBaseURL = !rules.isAzure && !rules.hasRegions && !rules.inheritsBaseURL && !rules.isNoAuth && !rules.requiresBaseURL;

  return (
    <ConnectDialog
      title={rules.isNoAuth ? `Enable ${provider.display_name}` : `Add ${provider.display_name} API key`}
      description={rules.isNoAuth ? "No credentials needed." : undefined}
      logo={{ icon: provider.icon, name: provider.display_name }}
      onClose={onClose}
      footer={
        <>
          {!rules.isNoAuth && (
            <SecondaryAction onClick={runCheck} disabled={!complete || check.status === "checking"}>
              <CheckCircle className={cn(check.status === "checking" && "animate-pulse")} aria-hidden="true" />
              {check.status === "checking" ? "Testing…" : "Test key"}
            </SecondaryAction>
          )}
          <PrimaryAction type="submit" form={formId} busy={create.isPending}>
            {create.isPending ? "Adding…" : rules.isNoAuth ? "Enable" : "Add account"}
          </PrimaryAction>
        </>
      }
    >
      <form id={formId} className="space-y-3.5" noValidate onSubmit={submit}>
        {rules.isCloudflare && <CloudflareSteps />}
        {!rules.isNoAuth && (
          <TextField
            label="API key"
            optional={rules.keyOptional}
            required={!rules.keyOptional}
            type="password"
            autoComplete="off"
            value={form.apiKey}
            onChange={set("apiKey")}
            placeholder={rules.keyPlaceholder}
            className="font-mono"
            hint={keyHint}
            error={err("apiKey", "Enter the API key.")}
          />
        )}
        {rules.isCloudflare && (
          <TextField
            label="Account ID"
            required
            value={form.accountID}
            onChange={set("accountID")}
            placeholder="a1b2c3d4e5f6…"
            className="font-mono"
            error={err("accountID", "Enter your Cloudflare Account ID.")}
          />
        )}
        {rules.isAzure && (
          <>
            <TextField
              label="Endpoint"
              required
              value={form.azureEndpoint}
              onChange={set("azureEndpoint")}
              placeholder="https://your-resource.openai.azure.com"
              className="font-mono"
              error={err("azureEndpoint", "Enter the Azure resource endpoint.")}
            />
            <TextField
              label="Deployment name"
              required
              value={form.azureDeployment}
              onChange={set("azureDeployment")}
              placeholder="gpt-4o"
              className="font-mono"
              error={err("azureDeployment", "Enter the deployment name.")}
            />
          </>
        )}
        {rules.hasRegions && !rules.isAzure && <RegionSelect provider={provider} value={form.region} onChange={(v) => setForm((f) => ({ ...f, region: v }))} />}
        {rules.requiresBaseURL && !rules.isAzure && !rules.hasRegions && (
          <TextField
            label="Base URL"
            required
            value={form.baseURL}
            onChange={set("baseURL")}
            placeholder="https://…/v1"
            className="font-mono"
            hint="The compatible endpoint this key belongs to"
            error={err("baseURL", "Enter the endpoint's base URL.")}
          />
        )}
        {rules.inheritsBaseURL && !rules.isAzure && !rules.hasRegions && (
          <p className="text-[12.5px] text-fg-muted">
            Uses <InlineCode>{provider.base_url}</InlineCode>
          </p>
        )}

        {rules.isNoAuth ? (
          labelField
        ) : (
          <Advanced>
            {labelField}
            {optionalBaseURL && (
              <TextField label="Base URL" optional value={form.baseURL} onChange={set("baseURL")} placeholder="https://…/v1" className="font-mono" hint="Only for a proxy or self-hosted endpoint" />
            )}
            {rules.isAzure && (
              <div className="grid gap-3.5 sm:grid-cols-2">
                <TextField label="API version" value={form.azureAPIVersion} onChange={set("azureAPIVersion")} className="font-mono" />
                <TextField label="Organization" optional value={form.azureOrganization} onChange={set("azureOrganization")} placeholder="org_…" className="font-mono" />
              </div>
            )}
          </Advanced>
        )}

        <div role="status" aria-live="polite">
          {check.status === "ok" && (
            <p className="flex items-center gap-1.5 text-[12.5px] font-medium text-ok">
              <CheckCircle2 className="h-4 w-4" aria-hidden="true" />
              The key works.
            </p>
          )}
        </div>
        {check.status === "error" && <FormError message={check.message || "The key was rejected. Check it and try again."} />}
        <FormError message={create.error?.message} />
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
  const textId = useId();
  const validateId = useId();

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
              <Layers aria-hidden="true" />
              Import more
            </SecondaryAction>
            <PrimaryAction onClick={onClose}>
              <Check aria-hidden="true" />
              Done
            </PrimaryAction>
          </>
        }
      >
        <ul className="divide-y divide-line overflow-hidden rounded-xl border border-line">
          {results.map((r) => (
            <li key={r.index} className="flex items-start gap-3 px-3 py-2 text-[12.5px]">
              {r.status === "created" ? (
                <CheckCircle className="mt-0.5 h-4 w-4 shrink-0 text-ok" aria-hidden="true" />
              ) : r.status === "skipped" ? (
                <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-warn" aria-hidden="true" />
              ) : (
                <XCircle className="mt-0.5 h-4 w-4 shrink-0 text-bad" aria-hidden="true" />
              )}
              <span className="sr-only">{r.status === "created" ? "Added:" : r.status === "skipped" ? "Skipped:" : "Failed:"}</span>
              <span className="w-9 shrink-0 font-mono text-fg-faint">#{r.index + 1}</span>
              <span className="min-w-0 flex-1">
                <span className="block truncate font-medium text-fg">{r.label || "Unlabeled"}</span>
                {r.error && <span className="block break-words text-fg-muted">{r.error}</span>}
              </span>
            </li>
          ))}
        </ul>
      </ConnectDialog>
    );
  }

  const keysLabel = rules.isQoder ? "Personal Access Tokens" : "API keys";
  return (
    <ConnectDialog
      title={`Import ${provider.display_name} ${rules.isQoder ? "tokens" : "API keys"}`}
      logo={{ icon: provider.icon, name: provider.display_name }}
      width="lg"
      onClose={onClose}
      footer={
        <>
          <SecondaryAction onClick={onClose}>Cancel</SecondaryAction>
          <PrimaryAction onClick={() => importMut.mutate()} disabled={!canImport} busy={importMut.isPending}>
            {!importMut.isPending && <Upload aria-hidden="true" />}
            {importMut.isPending ? "Importing…" : ready ? `Import ${ready} ${noun}${ready === 1 ? "" : "s"}` : "Import"}
          </PrimaryAction>
        </>
      }
    >
      <div className="space-y-3.5">
        {rules.isCloudflare && <CloudflareSteps />}
        {rules.hasRegions && <RegionSelect provider={provider} value={region} onChange={setRegion} />}
        {rules.isCloudflare && (
          <TextField label="Account ID" required value={accountID} onChange={(e) => setAccountID(e.target.value)} placeholder="a1b2c3d4e5f6…" className="font-mono" hint={`Applies to every ${noun}`} />
        )}
        {showBaseURL && (
          <TextField
            label="Base URL"
            optional={!rules.requiresBaseURL}
            required={rules.requiresBaseURL}
            value={baseURL}
            onChange={(e) => setBaseURL(e.target.value)}
            placeholder="https://…/v1"
            className="font-mono"
            hint={`Applies to every ${noun}`}
          />
        )}
        {rules.inheritsBaseURL && (
          <p className="text-[12.5px] text-fg-muted">
            Uses <InlineCode>{provider.base_url}</InlineCode>
          </p>
        )}

        <div className="space-y-1.5">
          <div className="flex items-center justify-between">
            <label htmlFor={textId} className="text-[12.5px] font-medium text-fg">
              {keysLabel}
            </label>
            <button
              type="button"
              onClick={() => fileRef.current?.click()}
              className="inline-flex h-7 items-center gap-1.5 rounded-lg border border-line px-2 text-[12px] text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
            >
              <FileText className="h-3.5 w-3.5" aria-hidden="true" />
              Load file
            </button>
            <input ref={fileRef} type="file" accept=".txt,.csv,text/plain,text/csv" className="hidden" tabIndex={-1} aria-hidden="true" onChange={onFile} />
          </div>
          <TextAreaField
            id={textId}
            value={text}
            onChange={(e) => setText(e.target.value)}
            rows={8}
            placeholder={`${rules.keyPlaceholder}\nlabel-2, ${rules.keyPlaceholder}\n# lines starting with # are ignored`}
            hint={
              <>
                One per line, optionally <InlineCode>label, {noun}</InlineCode>. Lines starting with <InlineCode>#</InlineCode> are skipped.
              </>
            }
          />
        </div>

        <p className="flex min-h-5 flex-wrap items-center gap-x-3 gap-y-1 text-[12.5px]" role="status" aria-live="polite">
          {text.trim() && (
            <>
              <span className={cn("font-medium", ready ? "text-ok" : "text-fg-muted")}>{ready} ready</span>
              {parsed.duplicates > 0 && <span className="text-warn">{parsed.duplicates} duplicate</span>}
              {parsed.errors.length > 0 && <span className="text-bad">{parsed.errors.length} invalid</span>}
              {parsed.errors.slice(0, 2).map((err) => (
                <span key={err.line} className="text-fg-muted">
                  line {err.line}: {err.message}
                </span>
              ))}
            </>
          )}
        </p>

        <div className="flex items-start gap-2.5">
          <input
            id={validateId}
            type="checkbox"
            checked={validate}
            onChange={(e) => setValidate(e.target.checked)}
            aria-describedby={`${validateId}-hint`}
            className="mt-0.5 h-4 w-4 shrink-0 rounded border-input accent-accent-500"
          />
          <div className="text-[12.5px] leading-5">
            <label htmlFor={validateId} className="font-medium text-fg">
              Test each {noun} before saving
            </label>
            <p id={`${validateId}-hint`} className="text-fg-muted">
              Slower, and may hit the provider's rate limits
            </p>
          </div>
        </div>
      </div>
    </ConnectDialog>
  );
}
