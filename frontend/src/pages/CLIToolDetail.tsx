import { useId, useMemo, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { useParams, Link } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft, Copy, Check, ChevronRight, Loader2, RotateCcw, FileCog, AlertTriangle, RefreshCw,
} from "lucide-react";
import { api, type CLITool, type CLIToolsResponse } from "../lib/api";
import { cliConnection, cliToolMeta, detectedViaLabel } from "../lib/cliTools";
import { useToast } from "../components/Toast";
import { ProviderLogo } from "../components/ProviderLogo";
import { useModelCatalog } from "../components/ModelSelect";
import { useConfirm } from "../components/ui/confirm-dialog";
import { Badge, Button, Input, SegmentedControl, Skeleton, EmptyState } from "../components/ui";
import { HighlightedCode } from "../components/ui/code-block";
import { detectLanguage } from "../lib/highlight";
import { ICONS } from "../lib/icons";
import { cn } from "@/lib/utils";
import { dashboard } from "../lib/dashboardRoutes";

// Placeholders the backend puts in manual-setup snippets.
const KEY_PLACEHOLDER = "kr_YOUR_API_KEY";
const MODEL_PLACEHOLDER = "provider/model-id";

type KeyMode = "keep" | "new" | "paste";

export function CLIToolDetailPage() {
  const { toolId = "" } = useParams<{ toolId: string }>();
  const qc = useQueryClient();
  const toast = useToast();
  const askConfirm = useConfirm();

  const tools = useQuery({
    queryKey: ["cli-tools"],
    queryFn: () => api.cliTools(),
  });
  const catalog = useModelCatalog();

  const tool = tools.data?.tools.find((t) => t.id === toolId);
  const meta = cliToolMeta[toolId];
  const gatewayURL = tools.data?.base_url ?? "";
  const stored = tool?.settings;
  const canKeepKey = stored?.key_status === "valid";

  // Form state, seeded from what the tool's config currently holds so the
  // page shows (and by default re-writes) the saved connection.
  const [baseUrl, setBaseUrl] = useState("");
  const [model, setModel] = useState("");
  const [keyMode, setKeyMode] = useState<KeyMode>("new");
  const [pastedKey, setPastedKey] = useState("");
  const [keyName, setKeyName] = useState("");
  const [seededFor, setSeededFor] = useState("");
  if (tool && tools.data && seededFor !== tool.id) {
    setSeededFor(tool.id);
    // Only reuse stored values that belong to this gateway — an endpoint and
    // model from another service would be wrong to write back.
    const ours = stored?.endpoint_match ?? false;
    setBaseUrl(ours ? stored!.base_url : gatewayURL);
    setModel(ours ? (stored?.model ?? "") : "");
    setKeyMode(stored?.key_status === "valid" ? "keep" : "new");
    setPastedKey("");
    setKeyName(`${tool.name} (CLI)`);
  }

  // Swap the refreshed tool into the list cache: no full rescan needed.
  const applyTool = (next: CLITool) => {
    qc.setQueryData<CLIToolsResponse>(["cli-tools"], (prev) =>
      prev ? { ...prev, tools: prev.tools.map((t) => (t.id === next.id ? next : t)) } : prev,
    );
  };

  const rescan = useMutation({
    mutationFn: () => api.cliTool(toolId),
    onSuccess: applyTool,
    onError: (e: Error) => toast.error("Rescan failed", e.message),
  });

  const configureMut = useMutation({
    mutationFn: async () => {
      let apiKey: string | undefined;
      if (keyMode === "paste") apiKey = pastedKey.trim();
      if (keyMode === "new") {
        const created = await api.createKey(keyName.trim() || `${tool?.name ?? toolId} (CLI)`);
        apiKey = created.key;
        // Keep the plaintext so a failed write can be retried with the same
        // key instead of minting another one.
        setPastedKey(created.key);
        setKeyMode("paste");
        qc.invalidateQueries({ queryKey: ["api-keys"] });
      }
      return api.cliToolConfigure(toolId, { base_url: baseUrl.trim(), api_key: apiKey, model: model.trim() || undefined });
    },
    onSuccess: (res) => {
      applyTool(res.tool);
      setKeyMode("keep");
      setPastedKey("");
      setBaseUrl(res.tool.settings?.base_url ?? baseUrl);
      toast.success("Tool configured", `${res.tool.name} now routes through KeiRouter. Restart it to pick up the change.`);
    },
    onError: (e: Error) => toast.error("Tool configuration failed", e.message),
  });

  const removeMut = useMutation({
    mutationFn: () => api.cliToolRemove(toolId),
    onSuccess: (res) => {
      applyTool(res.tool);
      setKeyMode("new");
      toast.success("Config removed", `${res.tool.name} is disconnected from KeiRouter and will use its default endpoint.`);
    },
    onError: (e: Error) => toast.error("Config removal failed", e.message),
  });

  // Manual snippet with the form's values filled in. The backend renders it
  // for the gateway's own address, so that root is swapped for the chosen one.
  const snippetWithVars = useMemo(() => {
    let text = tool?.snippet ?? "";
    const root = baseUrl.trim().replace(/\/+$/, "").replace(/\/v1$/, "");
    if (gatewayURL && root) text = text.split(gatewayURL).join(root);
    if (keyMode === "paste" && pastedKey.trim()) text = text.split(KEY_PLACEHOLDER).join(pastedKey.trim());
    if (model.trim()) text = text.split(MODEL_PLACEHOLDER).join(model.trim());
    else if (tool && !tool.model_required) text = dropLinesWith(text, MODEL_PLACEHOLDER);
    return text;
  }, [tool, baseUrl, gatewayURL, keyMode, pastedKey, model]);
  const files = useMemo(() => splitSnippet(snippetWithVars), [snippetWithVars]);
  const envVars = useMemo(() => extractEnvVars(files), [files]);
  const modelListId = useId();

  if (tools.isLoading) {
    return (
      <div className="space-y-4" aria-busy="true" aria-label="Loading CLI tool">
        <Skeleton className="h-5 w-40" />
        <div className="flex items-center gap-3">
          <Skeleton className="h-10 w-10 rounded-lg" />
          <div className="space-y-2">
            <Skeleton className="h-5 w-48" />
            <Skeleton className="h-3.5 w-72" />
          </div>
        </div>
        <Skeleton className="h-96 w-full rounded-2xl" />
      </div>
    );
  }

  if (!tool) {
    return (
      <EmptyState
        variant="card"
        titleAs="h1"
        icon={ICONS.cliTools}
        title="This CLI tool doesn't exist"
        action={<Link to={dashboard("/cli-tools")} className="rounded-md text-[13px] font-medium text-link hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500">Back to CLI tools</Link>}
      />
    );
  }

  const conn = cliConnection(tool);
  const configTarget = tool.config_files?.length ? tool.config_files.join(", ") : tool.config_path || "its config file";
  const modelMissing = tool.model_required && !model.trim();
  const keyMissing = keyMode === "paste" && !pastedKey.trim();
  const pastedLooksMasked = keyMode === "paste" && pastedKey.includes("…");
  const busy = configureMut.isPending || removeMut.isPending;
  const canWrite = !!baseUrl.trim() && !modelMissing && !keyMissing && !pastedLooksMasked && !busy;
  const endpointDiffers = !!gatewayURL && normalizeRoot(baseUrl) !== normalizeRoot(gatewayURL);

  const applyConfig = async () => {
    const ok = await askConfirm({
      title: `Write ${tool.name} config?`,
      description: (
        <>
          KeiRouter writes {joinList(["the endpoint", keyMode === "new" ? "a new API key" : "the API key", model.trim() && "the model"])} into{" "}
          <span className="font-mono text-[12px] text-fg">{configTarget}</span>.
          Your other settings in those files are kept, but comments are not.{" "}
          {tool.configured
            ? "A copy of each file from before KeiRouter first edited it is kept next to it as .keirouter.bak."
            : "A copy of each existing file is saved next to it as .keirouter.bak first."}{" "}
          Restart {tool.name} afterwards to pick up the change.
        </>
      ),
      confirmLabel: keyMode === "new" ? "Create key and write" : "Write config",
    });
    if (ok) configureMut.mutate();
  };

  const resetConfig = async () => {
    const ok = await askConfirm({
      title: `Disconnect ${tool.name}?`,
      description: `The KeiRouter settings are removed from ${configTarget}; everything else in those files stays. ${tool.name} goes back to its default endpoint and requests stop routing through KeiRouter.`,
      confirmLabel: "Remove config",
      tone: "danger",
    });
    if (ok) removeMut.mutate();
  };

  const via = detectedViaLabel(tool);
  const keyOptions: { value: KeyMode; label: string }[] = [
    ...(canKeepKey ? [{ value: "keep" as const, label: "Keep current" }] : []),
    { value: "new", label: "Create new" },
    { value: "paste", label: "Paste key" },
  ];

  return (
    <>
      <nav aria-label="Breadcrumb" className="mb-3 text-[13px] text-fg-muted">
        <ol className="flex items-center gap-1.5">
          <li>
            <Link
              to={dashboard("/cli-tools")}
              className="inline-flex min-h-6 items-center gap-1.5 rounded-md hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
            >
              <ArrowLeft className="h-3.5 w-3.5" aria-hidden="true" />
              CLI tools
            </Link>
          </li>
          <li aria-hidden="true" className="text-fg-faint">
            /
          </li>
          <li className="truncate text-fg" aria-current="page">
            {tool.name}
          </li>
        </ol>
      </nav>

      <header className="mb-5 flex min-w-0 flex-wrap items-center gap-3">
        <ProviderLogo icon={meta?.image} name={tool.name} size={40} className="rounded-lg" />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <h1 className="text-[22px] font-semibold leading-tight tracking-[-0.02em] text-fg">{tool.name}</h1>
            <Badge tone={conn.tone}>{conn.label}</Badge>
          </div>
          <p className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[13px] text-fg-muted">
            <span>{meta?.description ?? tool.dialect}</span>
            {tool.version && (
              <>
                <span aria-hidden="true" className="text-fg-faint">
                  ·
                </span>
                <span className="font-mono text-[12.5px]">v{tool.version}</span>
              </>
            )}
          </p>
        </div>
        <Button variant="secondary" onClick={() => rescan.mutate()} disabled={rescan.isPending}>
          <RefreshCw className={cn(rescan.isPending && "animate-spin")} strokeWidth={1.75} aria-hidden="true" />
          {rescan.isPending ? "Scanning…" : "Rescan"}
        </Button>
      </header>

      {conn.kind === "attention" && (
        <div role="alert" className="mb-4 flex max-w-3xl items-start gap-2 rounded-xl border border-warn/30 bg-warn/10 px-3.5 py-2.5 text-[12.5px] leading-5 text-fg">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warn" strokeWidth={1.75} aria-hidden="true" />
          <span className="break-words">
            <span className="font-medium">{conn.label}.</span> {conn.detail}
          </span>
        </div>
      )}

      {tool.configured && stored && <CurrentConnection tool={tool} />}

      <ol className="max-w-3xl divide-y divide-line overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
        {/* 1. Install */}
        <Step n={1} done={tool.installed} title={`Install ${tool.name}`}>
          {tool.installed ? (
            <div className="space-y-1 text-[13px] text-fg-muted">
              <p className="flex items-center gap-1.5">
                <Check className="h-3.5 w-3.5 text-ok" aria-hidden="true" />
                Detected on this machine{via ? ` (${via})` : ""}.
              </p>
              {tool.binary_path && <p className="break-all font-mono text-[12px]">{tool.binary_path}</p>}
              {tool.extension && <p className="break-all font-mono text-[12px]">{tool.extension}</p>}
              {!tool.binary_path && !tool.extension && (
                <p className="text-[12.5px]">
                  Only its config file was found. If the CLI is installed, its binary isn&apos;t on this gateway&apos;s PATH — the
                  config still works.
                </p>
              )}
            </div>
          ) : (
            <div className="space-y-3">
              <p className="flex items-start gap-1.5 text-[13px] leading-5 text-fg">
                <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warn" strokeWidth={1.75} aria-hidden="true" />
                <span>Not detected here. Install it and rescan, or set it up by hand in step 4.</span>
              </p>
              {meta?.installCmd && <CodeWell code={meta.installCmd} label="Shell" copyLabel="install command" />}
            </div>
          )}
        </Step>

        {/* 2. Connection settings */}
        <Step n={2} title="Choose endpoint, key and model">
          <div className="max-w-xl space-y-4">
            <FieldRow
              id="cli-endpoint"
              label="Endpoint URL"
              required
              hint={
                endpointDiffers ? (
                  <>
                    This gateway is at <span className="font-mono">{gatewayURL}</span>.{" "}
                    <button type="button" className="font-medium text-link hover:underline" onClick={() => setBaseUrl(gatewayURL)}>
                      Use it
                    </button>
                  </>
                ) : (
                  "This gateway's address."
                )
              }
            >
              <Input
                id="cli-endpoint"
                value={baseUrl}
                onChange={(e) => setBaseUrl(e.target.value)}
                placeholder="http://localhost:20180"
                className="font-mono"
                aria-required="true"
                aria-describedby="cli-endpoint-hint"
              />
            </FieldRow>

            <div className="space-y-1.5">
              <span id="cli-key-label" className="block text-[12.5px] font-medium text-fg">
                API key
              </span>
              <SegmentedControl value={keyMode} onChange={setKeyMode} options={keyOptions} aria-labelledby="cli-key-label" />
              {keyMode === "keep" && stored && (
                <p className="text-[12px] leading-5 text-fg-muted">
                  Keeps <span className="font-mono text-fg">{stored.api_key_hint}</span>
                  {stored.key_name && <> ({stored.key_name})</>} already in the config.
                </p>
              )}
              {keyMode === "new" && (
                <div className="space-y-1.5">
                  <Input value={keyName} onChange={(e) => setKeyName(e.target.value)} aria-label="New key name" placeholder={`${tool.name} (CLI)`} />
                  <p className="text-[12px] leading-5 text-fg-muted">
                    A dedicated key is created when you write the config, so you can revoke this tool on its own.
                  </p>
                </div>
              )}
              {keyMode === "paste" && (
                <div className="space-y-1.5">
                  <Input
                    value={pastedKey}
                    onChange={(e) => setPastedKey(e.target.value)}
                    placeholder="kr_…"
                    aria-label="API key"
                    aria-invalid={pastedLooksMasked || undefined}
                    type="password"
                    autoComplete="off"
                    className="font-mono"
                  />
                  <p className={cn("text-[12px] leading-5", pastedLooksMasked ? "text-bad" : "text-fg-muted")}>
                    {pastedLooksMasked
                      ? "That's the masked form from the key list — paste the full key shown when it was created."
                      : "Paste the full key (it starts with kr_). It's checked against this gateway before anything is written."}
                  </p>
                </div>
              )}
            </div>

            <FieldRow
              id="cli-model"
              label="Default model"
              optional={!tool.model_required}
              required={tool.model_required}
              hint={
                tool.model_required
                  ? `${tool.name} needs a model to send requests. Use a provider/model id or chain:<name>.`
                  : "Leave empty to keep the tool's own default model."
              }
            >
              <Input
                id="cli-model"
                value={model}
                onChange={(e) => setModel(e.target.value)}
                placeholder="provider/model-id or chain:my-chain"
                className="font-mono"
                list={modelListId}
                autoComplete="off"
                aria-required={tool.model_required || undefined}
                aria-describedby="cli-model-hint"
              />
              <datalist id={modelListId}>
                {catalog.models.map((m, i) => (
                  <option key={`${m.providerName}:${m.id}:${i}`} value={m.id}>
                    {m.providerName}
                  </option>
                ))}
              </datalist>
            </FieldRow>
          </div>
        </Step>

        {/* 3. Apply */}
        <Step
          n={3}
          done={conn.kind === "connected"}
          title="Write the config"
          description={
            <>
              Updates <span className="break-all font-mono text-[12px] text-fg">{configTarget}</span>
            </>
          }
        >
          <div className="flex flex-wrap items-center justify-between gap-2">
            <Button onClick={applyConfig} disabled={!canWrite}>
              {configureMut.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <FileCog aria-hidden="true" />}
              {tool.configured ? "Rewrite config" : "Write config"}
            </Button>
            {tool.configured && (
              <Button variant="danger" onClick={resetConfig} disabled={busy}>
                {removeMut.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <RotateCcw aria-hidden="true" />}
                Remove config
              </Button>
            )}
          </div>
          {modelMissing && <p className="mt-2 text-[12px] text-fg-muted">Pick a default model first.</p>}

          <div role="status" className="empty:hidden">
            {configureMut.isSuccess && !configureMut.isPending && (
              <p className="mt-3 flex items-center gap-1.5 text-[12.5px] text-ok">
                <Check className="h-3.5 w-3.5" aria-hidden="true" />
                Configured. Restart {tool.name} to pick up the change.
              </p>
            )}
            {removeMut.isSuccess && !removeMut.isPending && (
              <p className="mt-3 flex items-center gap-1.5 text-[12.5px] text-ok">
                <Check className="h-3.5 w-3.5" aria-hidden="true" />
                Config removed.
              </p>
            )}
          </div>
          {(configureMut.isError || removeMut.isError) && (
            <p role="alert" className="mt-3 flex items-start gap-1.5 text-[12.5px] text-bad">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
              <span className="break-words">{((configureMut.error ?? removeMut.error) as Error)?.message}</span>
            </p>
          )}
        </Step>

        {/* 4. Manual */}
        <li className="px-4 py-4 sm:px-5">
          <details className="group" open={!tool.installed}>
            <summary className="flex cursor-pointer list-none items-center gap-3 rounded-md focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 [&::-webkit-details-marker]:hidden">
              <StepNumber n={4} />
              <h2 className="flex min-w-0 flex-1 items-center gap-1.5 text-[13px] font-semibold leading-5 text-fg">
                <span className="sr-only">Step 4: </span>
                Or set it up by hand
                <ChevronRight
                  className="h-3.5 w-3.5 text-fg-faint transition-transform group-open:rotate-90"
                  strokeWidth={1.75}
                  aria-hidden="true"
                />
              </h2>
            </summary>
            <div className="mt-3 space-y-4 sm:pl-10">
              {tool.instructions && <p className="text-[12.5px] leading-5 text-fg-muted">{tool.instructions}</p>}
              {keyMode !== "paste" && (
                <p className="text-[12.5px] leading-5 text-fg-muted">
                  Replace <span className="font-mono text-fg">{KEY_PLACEHOLDER}</span> with a full API key.
                </p>
              )}
              <SnippetWell files={files} fullText={snippetWithVars} />
              {envVars.length > 0 && <EnvTable vars={envVars} />}
            </div>
          </details>
        </li>
      </ol>
    </>
  );
}

// CurrentConnection shows what the tool's config holds right now, with a
// check or warning per field so a stale endpoint or dead key is obvious.
function CurrentConnection({ tool }: { tool: CLITool }) {
  const s = tool.settings!;
  const keyNote: Record<typeof s.key_status, string> = {
    valid: s.key_name ? `Valid · ${s.key_name}` : "Valid",
    invalid: "Not a key on this gateway",
    disabled: s.key_name ? `Disabled · ${s.key_name}` : "Disabled",
    missing: "No key stored",
  };
  return (
    <section aria-labelledby="cli-current-heading" className="mb-4 max-w-3xl rounded-2xl border border-line bg-surface px-4 py-3.5 shadow-[var(--shadow-card)] sm:px-5">
      <h2 id="cli-current-heading" className="text-[13px] font-semibold text-fg">
        Saved in the config
      </h2>
      <dl className="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-[12.5px]">
        <dt className="text-fg-muted">Endpoint</dt>
        <dd className="flex min-w-0 flex-wrap items-center gap-x-2">
          <span className="break-all font-mono text-[12px] text-fg">{s.base_url}</span>
          <StateNote ok={s.endpoint_match} text={s.endpoint_match ? "This gateway" : "Different address"} />
        </dd>
        <dt className="text-fg-muted">API key</dt>
        <dd className="flex min-w-0 flex-wrap items-center gap-x-2">
          {s.api_key_hint && <span className="font-mono text-[12px] text-fg">{s.api_key_hint}</span>}
          <StateNote ok={s.key_status === "valid"} text={keyNote[s.key_status]} />
        </dd>
        <dt className="text-fg-muted">Model</dt>
        <dd className="min-w-0 break-all font-mono text-[12px] text-fg">{s.model || <span className="font-sans text-fg-muted">Tool default</span>}</dd>
        <dt className="text-fg-muted">Files</dt>
        <dd className="min-w-0 space-y-0.5">
          {(tool.config_files?.length ? tool.config_files : [tool.config_path]).map((f) => (
            <span key={f} className="block break-all font-mono text-[12px] text-fg-muted">
              {f}
            </span>
          ))}
        </dd>
        {tool.backup_path && (
          <>
            <dt className="text-fg-muted">Backup</dt>
            <dd className="min-w-0 break-all font-mono text-[12px] text-fg-muted" title="The file as it was before KeiRouter first edited it">
              {tool.backup_path}
            </dd>
          </>
        )}
      </dl>
    </section>
  );
}

function StateNote({ ok, text }: { ok: boolean; text: string }) {
  return (
    <span className={cn("inline-flex items-center gap-1 text-[12px]", ok ? "text-ok" : "text-warn")}>
      {ok ? (
        <Check className="h-3.5 w-3.5" aria-hidden="true" />
      ) : (
        <AlertTriangle className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
      )}
      {text}
    </span>
  );
}

// joinList renders ["a", "b", "c"] as "a, b and c", skipping empty entries.
function joinList(items: (string | false | undefined)[]): string {
  const xs = items.filter(Boolean) as string[];
  return xs.length < 2 ? (xs[0] ?? "") : `${xs.slice(0, -1).join(", ")} and ${xs[xs.length - 1]}`;
}

function normalizeRoot(u: string): string {
  return u.trim().replace(/\/+$/, "").replace(/\/v1$/, "").toLowerCase();
}

// dropLinesWith removes snippet lines containing needle (an unfilled optional
// field) and the trailing comma that would leave behind in JSON.
function dropLinesWith(text: string, needle: string): string {
  const lines = text.split("\n");
  const out: string[] = [];
  for (const line of lines) {
    if (line.includes(needle)) continue;
    out.push(line);
  }
  for (let i = 0; i < out.length - 1; i++) {
    if (/^\s*[}\]]/.test(out[i + 1])) out[i] = out[i].replace(/,\s*$/, "");
  }
  return out.join("\n");
}

// ── Layout pieces ────────────────────────────────────────────────────────────

// StepNumber is a 28px tone tile with the step number; a finished step
// (tool installed / config written) turns into an "ok" tile.
function StepNumber({ n, done }: { n: number; done?: boolean }) {
  return (
    <span
      className={cn(
        "flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-[12px] font-semibold tabular-nums ring-1 ring-inset",
        done ? "bg-ok/10 text-ok ring-ok/20" : "bg-tone-soft text-tone ring-tone-ring",
      )}
      aria-hidden="true"
    >
      {n}
    </span>
  );
}

function Step({
  n,
  title,
  description,
  done,
  children,
}: {
  n: number;
  title: string;
  description?: ReactNode;
  done?: boolean;
  children: ReactNode;
}) {
  return (
    <li className="flex gap-3 px-4 py-4 sm:px-5">
      <StepNumber n={n} done={done} />
      <div className="min-w-0 flex-1">
        <h2 className="mt-1 text-[13px] font-semibold leading-5 text-fg">
          <span className="sr-only">
            Step {n}
            {done ? " (done)" : ""}:{" "}
          </span>
          {title}
        </h2>
        {description && <p className="mt-0.5 text-[12.5px] leading-5 text-fg-muted">{description}</p>}
        <div className="mt-3">{children}</div>
      </div>
    </li>
  );
}

function FieldRow({
  id,
  label,
  hint,
  optional,
  required,
  children,
}: {
  id: string;
  label: string;
  hint?: ReactNode;
  optional?: boolean;
  required?: boolean;
  children: ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label htmlFor={id} className="flex items-baseline justify-between text-[12.5px] font-medium text-fg">
        {label}
        {optional && <span className="text-[12px] font-normal text-fg-faint">Optional</span>}
        {required && <span className="text-[12px] font-normal text-fg-faint">Required</span>}
      </label>
      {children}
      {hint && (
        <p id={`${id}-hint`} className="text-[12px] leading-5 text-fg-muted">
          {hint}
        </p>
      )}
    </div>
  );
}

// ── Snippets ─────────────────────────────────────────────────────────────────

interface SnippetFile {
  path: string;
  notes: string[];
  body: string;
}

// splitSnippet turns the backend's multi-file snippet ("# ~/path" header lines
// followed by file contents) into one entry per file. A fully commented file
// (e.g. Codex's optional auth.json) is shown uncommented so it can be pasted,
// and leading comment lines become notes above the code.
function splitSnippet(snippet: string): SnippetFile[] {
  const sections: { path: string; lines: string[] }[] = [];
  let current: { path: string; lines: string[] } | null = null;
  for (const line of snippet.split("\n")) {
    const header = /^#\s+((?:~|\/)\S.*)$/.exec(line);
    if (header) {
      current = { path: header[1].trim(), lines: [] };
      sections.push(current);
      continue;
    }
    if (!current) {
      current = { path: "", lines: [] };
      sections.push(current);
    }
    current.lines.push(line);
  }

  return sections
    .map(({ path, lines }) => {
      while (lines.length && !lines[lines.length - 1].trim()) lines.pop();
      while (lines.length && !lines[0].trim()) lines.shift();
      const nonEmpty = lines.filter((l) => l.trim());
      let body = lines;
      const notes: string[] = [];
      if (nonEmpty.length > 0 && nonEmpty.every((l) => /^#( |$)/.test(l))) {
        body = lines.map((l) => l.replace(/^# ?/, ""));
      } else {
        while (body.length && /^#\s/.test(body[0])) {
          notes.push(body[0].replace(/^#\s+/, "").replace(/^\((.*)\)$/, "$1"));
          body = body.slice(1);
        }
      }
      return { path, notes, body: body.join("\n") };
    })
    .filter((s) => s.body.trim() || s.path);
}

interface EnvVar {
  name: string;
  value: string;
  file: string;
}

// extractEnvVars finds environment-variable style keys (UPPER_SNAKE) in the
// snippet files, whether set in a JSON "env" block or a KEY=value env file.
function extractEnvVars(files: SnippetFile[]): EnvVar[] {
  const out: EnvVar[] = [];
  const seen = new Set<string>();
  for (const f of files) {
    for (const line of f.body.split("\n")) {
      const m =
        /^\s*"([A-Z][A-Z0-9_]{2,})"\s*:\s*"([^"]*)"/.exec(line) ??
        /^\s*(?:export\s+)?([A-Z][A-Z0-9_]{2,})=(.*)$/.exec(line);
      if (!m) continue;
      const name = m[1];
      const value = m[2].trim().replace(/^"(.*)"$/, "$1");
      const id = `${f.path}:${name}`;
      if (seen.has(id)) continue;
      seen.add(id);
      out.push({ name, value, file: f.path });
    }
  }
  return out;
}

function basename(path: string): string {
  const parts = path.split("/");
  return parts[parts.length - 1] || path;
}

function SnippetWell({ files, fullText }: { files: SnippetFile[]; fullText: string }) {
  const [active, setActive] = useState(0);
  const baseId = useId();
  const idx = Math.min(active, Math.max(0, files.length - 1));
  const file = files[idx];
  if (!file) return null;
  const multi = files.length > 1;
  const tabId = (i: number) => `${baseId}-tab-${i}`;
  const panelId = `${baseId}-panel`;

  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (!["ArrowRight", "ArrowLeft", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const n = files.length;
    const next =
      event.key === "Home"
        ? 0
        : event.key === "End"
          ? n - 1
          : (idx + (event.key === "ArrowRight" ? 1 : -1) + n) % n;
    setActive(next);
    document.getElementById(tabId(next))?.focus();
  };

  return (
    <div className="overflow-hidden rounded-xl border border-line bg-subtle">
      {/* On phones the copy buttons drop under the file tabs instead of squeezing them. */}
      <div className="flex flex-wrap items-center justify-between gap-x-2 border-b border-line pl-1 pr-1.5">
        {multi ? (
          <div
            className="flex min-w-0 max-w-full gap-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
            role="tablist"
            aria-label="Config files"
            onKeyDown={onKeyDown}
          >
            {files.map((f, i) => {
              const on = i === idx;
              return (
                <button
                  key={`${f.path}-${i}`}
                  id={tabId(i)}
                  type="button"
                  role="tab"
                  aria-selected={on}
                  aria-controls={panelId}
                  tabIndex={on ? 0 : -1}
                  title={f.path}
                  onClick={() => setActive(i)}
                  className={cn(
                    "relative shrink-0 whitespace-nowrap px-2.5 py-2.5 font-mono text-[12px] transition-colors sm:py-2 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
                    on ? "text-fg" : "text-fg-muted hover:text-fg",
                  )}
                >
                  {f.path ? basename(f.path) : "Snippet"}
                  {on && <span aria-hidden="true" className="absolute inset-x-2 -bottom-px h-0.5 rounded-full bg-accent-500" />}
                </button>
              );
            })}
          </div>
        ) : (
          <span className="min-w-0 flex-1 truncate px-2.5 py-2 font-mono text-[12px] text-fg-muted" title={file.path || undefined}>
            {file.path || "Snippet"}
          </span>
        )}
        <div className="ml-auto flex shrink-0 items-center gap-2 sm:gap-1">
          {multi && <CopyButton text={fullText} label="Copy all" toastLabel="config files" />}
          <CopyButton text={file.body} label="Copy" toastLabel={file.path ? basename(file.path) : "snippet"} />
        </div>
      </div>
      <div
        id={panelId}
        role={multi ? "tabpanel" : "region"}
        aria-labelledby={multi ? tabId(idx) : undefined}
        aria-label={multi ? undefined : file.path || "Config snippet"}
        tabIndex={0}
        className="focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500"
      >
        {multi && file.path && (
          <p className="break-all border-b border-line px-3.5 py-1.5 font-mono text-[11.5px] text-fg-muted">{file.path}</p>
        )}
        {file.notes.length > 0 && (
          <p className="border-b border-line px-3.5 py-1.5 text-[12px] text-fg-muted">{file.notes.join(" ")}</p>
        )}
        <pre className="max-h-[60vh] overflow-auto bg-[var(--code-bg)] px-3.5 py-3 font-mono text-[12.5px] leading-[1.65] text-[var(--code-fg)] sm:max-h-[420px]">
          <code>
            <HighlightedCode code={file.body} language={detectLanguage(file.body, file.path)} />
          </code>
        </pre>
      </div>
    </div>
  );
}

function CodeWell({ code, label, copyLabel }: { code: string; label: string; copyLabel: string }) {
  return (
    <div className="overflow-hidden rounded-xl border border-line bg-subtle">
      <div className="flex items-center justify-between gap-2 border-b border-line pl-3.5 pr-1.5">
        <span className="py-1.5 text-[12px] text-fg-muted">{label}</span>
        <CopyButton text={code} label="Copy" toastLabel={copyLabel} />
      </div>
      <pre
        tabIndex={0}
        role="region"
        aria-label={`${label} command`}
        className="overflow-x-auto bg-[var(--code-bg)] px-3.5 py-2.5 font-mono text-[12.5px] leading-[1.65] text-[var(--code-fg)] focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500"
      >
        <code>
          <HighlightedCode code={code} language="bash" />
        </code>
      </pre>
    </div>
  );
}

function EnvTable({ vars }: { vars: EnvVar[] }) {
  return (
    <div className="overflow-hidden rounded-xl border border-line">
      {/* Phones: one stacked entry per variable, full value wraps instead of truncating. */}
      <ul className="divide-y divide-line md:hidden" aria-label="Environment variables in the config">
        {vars.map((v) => (
          <li key={`${v.file}:${v.name}`} className="flex items-start gap-2 py-2.5 pl-3.5 pr-1.5">
            <div className="min-w-0 flex-1">
              <p className="break-all font-mono text-[12px] font-medium text-fg">{v.name}</p>
              <p className="mt-0.5 break-all font-mono text-[12px] text-fg-muted">{v.value || "—"}</p>
              {v.file && <p className="mt-0.5 break-all font-mono text-[11.5px] text-fg-faint">{basename(v.file)}</p>}
            </div>
            <CopyButton text={v.value} toastLabel={`${v.name} value`} />
          </li>
        ))}
      </ul>
      <div className="hidden overflow-x-auto md:block">
        <table className="w-full min-w-[520px] text-[13px]">
          <caption className="sr-only">Environment variables in the config</caption>
          <thead>
            <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
              <th scope="col" className="px-3.5 py-2 font-medium">Environment variable</th>
              <th scope="col" className="px-3.5 py-2 font-medium">Value</th>
              <th scope="col" className="px-3.5 py-2 font-medium">File</th>
              <th scope="col" className="w-10 px-2 py-2">
                <span className="sr-only">Copy</span>
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {vars.map((v) => (
              <tr key={`${v.file}:${v.name}`} className="transition-colors hover:bg-hover">
                <td className="whitespace-nowrap px-3.5 py-2 font-mono text-[12px] text-fg">{v.name}</td>
                <td className="max-w-[260px] truncate px-3.5 py-2 font-mono text-[12px] text-fg-muted" title={v.value}>{v.value}</td>
                <td className="whitespace-nowrap px-3.5 py-2 font-mono text-[12px] text-fg-muted">{v.file ? basename(v.file) : "—"}</td>
                <td className="px-2 py-1.5 text-right">
                  <CopyButton text={v.value} toastLabel={`${v.name} value`} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function CopyButton({ text, label, toastLabel }: { text: string; label?: string; toastLabel: string }) {
  const toast = useToast();
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      toast.success("Copied", toastLabel);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      toast.error("Copy failed", "The browser blocked clipboard access.");
    }
  };
  return (
    <button
      type="button"
      onClick={copy}
      aria-label={label ? `${label} ${toastLabel}` : `Copy ${toastLabel}`}
      title={label ? undefined : `Copy ${toastLabel}`}
      className="inline-flex h-9 min-w-9 items-center justify-center gap-1.5 rounded-lg px-2 text-[12px] font-medium text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 sm:h-7 sm:min-w-7"
    >
      {copied ? (
        <Check className="h-3.5 w-3.5 text-ok" aria-hidden="true" />
      ) : (
        <Copy className="h-3.5 w-3.5 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
      )}
      {label && (copied ? "Copied" : label)}
    </button>
  );
}
