import { useEffect, useId, useMemo, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { useMutation } from "@tanstack/react-query";
import { AlertCircle, AlertTriangle, CheckCircle2, ChevronDown, ExternalLink, Plus, X } from "lucide-react";
import { api, type MCPAuthType, type MCPPreset, type MCPServer, type MCPServerInput, type MCPTestResult } from "../../lib/api";
import { cn } from "@/lib/utils";
import { formatMs } from "../../lib/format";
import { Button, ErrorBanner, Field, Input, Select, Textarea } from "../ui";
import { connectHint, fillPresetURL, hostOf, PRESET_TEXT_VALUE, SLUG_PATTERN, slugFromName } from "./shared";

const TOOL_PREVIEW = 8;

/**
 * TestResult reports a connection test: what answered, how fast, and the
 * tools clients will get, or what failed and what to check. A result whose
 * settings were edited afterwards says so instead of vouching for them.
 */
function TestResult({ result, host, stale }: { result: MCPTestResult; host: string; stale: boolean }) {
  const tools = result.tools ?? [];
  const hidden = tools.filter((t) => t.invalid_reason);
  const usable = tools.filter((t) => !t.invalid_reason);
  const hint = result.ok ? undefined : connectHint(result.error);

  return (
    <section aria-label="Connection test" className="overflow-hidden rounded-lg border border-line bg-surface text-[13px]">
      <div className={cn("flex items-start gap-2.5 px-3 py-2.5", result.ok ? "bg-ok-soft" : "bg-bad-soft")}>
        {result.ok ? (
          <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-ok" aria-hidden="true" />
        ) : (
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-bad" aria-hidden="true" />
        )}
        <div className="min-w-0 flex-1">
          <p className="font-medium text-fg">
            {result.ok ? "Connected" : "Could not connect"}
            {host && <span className="font-normal text-fg-muted"> to {host}</span>}
          </p>
          {!result.ok && result.error && <p className="mt-0.5 break-words text-[12.5px] leading-5 text-fg">{result.error}</p>}
          {hint && <p className="mt-0.5 text-[12.5px] leading-5 text-fg-muted">{hint}</p>}
        </div>
      </div>

      {result.ok && (
        <>
          <dl className="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] divide-x divide-line border-t border-line">
            <div className="min-w-0 px-3 py-2">
              <dt className="text-[12px] text-fg-muted">Tools</dt>
              <dd className="font-mono tnum text-fg">{usable.length}</dd>
            </div>
            <div className="min-w-0 px-3 py-2">
              <dt className="text-[12px] text-fg-muted">Protocol</dt>
              <dd className="whitespace-nowrap font-mono text-fg">{result.protocol_version || "n/a"}</dd>
            </div>
            <div className="min-w-0 px-3 py-2">
              <dt className="text-[12px] text-fg-muted">Latency</dt>
              <dd className="font-mono tnum text-fg">{formatMs(result.latency_ms)}</dd>
            </div>
          </dl>
          {usable.length > 0 && (
            <ul aria-label="Tools found" className="flex flex-wrap gap-1.5 border-t border-line px-3 py-2.5">
              {usable.slice(0, TOOL_PREVIEW).map((t) => (
                <li key={t.name} title={t.description || undefined} className="max-w-full truncate rounded-md bg-subtle px-1.5 py-0.5 font-mono text-[12px] text-fg-muted">
                  {t.name}
                </li>
              ))}
              {usable.length > TOOL_PREVIEW && <li className="px-1 py-0.5 text-[12px] text-fg-muted">+{usable.length - TOOL_PREVIEW} more</li>}
            </ul>
          )}
          {usable.length === 0 && hidden.length === 0 && (
            <p className="border-t border-line px-3 py-2.5 text-[12.5px] text-fg-muted">The server lists no tools yet. Clients will see none until it does.</p>
          )}
          {hidden.length > 0 && (
            <div className="flex items-start gap-2 border-t border-line px-3 py-2.5 text-[12.5px] leading-5">
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warn" aria-hidden="true" />
              <p className="min-w-0 text-fg-muted">
                <span className="text-fg">
                  {hidden.length} tool{hidden.length === 1 ? "" : "s"} hidden
                </span>{" "}
                for an invalid header annotation: <span className="break-all font-mono">{hidden.map((t) => t.name).join(", ")}</span>
              </p>
            </div>
          )}
        </>
      )}

      {stale && (
        <p className="flex items-start gap-2 border-t border-line bg-warn-soft px-3 py-2 text-[12.5px] leading-5 text-fg">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warn" aria-hidden="true" />
          Settings changed since this test. Test again to check them.
        </p>
      )}
    </section>
  );
}

const AUTH_LABELS: Record<MCPAuthType, string> = {
  none: "No authentication",
  bearer: "Bearer token",
  header: "API key in a header",
  basic: "Username and password (Basic)",
};

const FOCUS_RING = "focus:outline-none focus-visible:ring-2 focus-visible:ring-ring";

type HeaderRow = { name: string; value: string };

export interface ServerFormProps {
  /** create: a new server, from a preset or custom. edit: an existing one. */
  mode: "create" | "edit";
  preset?: MCPPreset;
  server?: MCPServer;
  /** Keys in "every server" mode; shown before a new server is saved. */
  keysWithAllAccess?: number;
  submitLabel: string;
  onSubmit: (input: MCPServerInput) => Promise<unknown>;
  submitting: boolean;
  onCancel?: () => void;
}

/**
 * ServerForm collects an MCP server's connection settings. A preset fixes the
 * URL template and the auth types it accepts; the operator fills the
 * template's variables and the secret. In edit mode secrets are never shown:
 * leaving the secret empty keeps the stored one.
 */
export function ServerForm({ mode, preset, server, keysWithAllAccess, submitLabel, onSubmit, submitting, onCancel }: ServerFormProps) {
  const uid = useId();
  const editing = mode === "edit" && !!server;
  const [name, setName] = useState(server?.name ?? preset?.name ?? "");
  const [slug, setSlug] = useState(server?.slug ?? slugFromName(preset?.id ?? preset?.name ?? ""));
  const [slugTouched, setSlugTouched] = useState(!!preset || editing);
  const [description, setDescription] = useState(server?.description ?? "");
  const [vars, setVars] = useState<Record<string, string>>(() =>
    Object.fromEntries((preset?.vars ?? []).map((v) => [v.key, v.default ?? (v.type === "bool" ? "false" : v.type === "select" ? v.options?.[0]?.value ?? "" : "")])),
  );
  const [url, setUrl] = useState(server?.url ?? "");
  const [authType, setAuthType] = useState<MCPAuthType>(server?.auth_type ?? preset?.auth_types[0] ?? "none");
  const [authHeader, setAuthHeader] = useState(server?.auth_header || preset?.auth_header || "");
  const [username, setUsername] = useState(server?.username ?? "");
  const [secret, setSecret] = useState("");
  const [replaceSecret, setReplaceSecret] = useState(!editing || !server?.has_secret);
  const [headers, setHeaders] = useState<HeaderRow[]>([]);
  const [replaceHeaders, setReplaceHeaders] = useState(!editing || (server?.static_header_names.length ?? 0) === 0);
  const [forward, setForward] = useState((server?.forward_headers ?? []).join(", "));
  const [timeoutS, setTimeoutS] = useState(String(Math.round((server?.timeout_ms ?? 60000) / 1000)));
  const [advanced, setAdvanced] = useState(editing && ((server?.static_header_names.length ?? 0) > 0 || (server?.forward_headers.length ?? 0) > 0));
  const [submitted, setSubmitted] = useState(false);
  const [test, setTest] = useState<{ result: MCPTestResult; signature: string } | null>(null);
  // The result lands at the end of a long form, under the sticky footer:
  // bring each new one into view.
  const testRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (test) testRef.current?.scrollIntoView({ block: "nearest" });
  }, [test]);

  // A preset that is not fixed to one server URL lets the operator fill its
  // variables; the URL is derived from them.
  const presetURL = preset && !editing ? fillPresetURL(preset.url, vars) : "";
  const effectiveURL = preset && !editing ? presetURL : url.trim();
  const authChoices: MCPAuthType[] = preset ? preset.auth_types : ["none", "bearer", "header", "basic"];
  const secretLabel = preset?.secret_label ?? (authType === "header" ? "API key" : authType === "basic" ? "Password or token" : "Token");
  const secretOptional = editing ? true : !!preset?.secret_optional || authType === "none";

  const errors = useMemo(() => {
    const e: Record<string, string> = {};
    if (!name.trim()) e.name = "Give the server a name.";
    if (!editing && !SLUG_PATTERN.test(slug)) e.slug = "Use 1–32 lowercase letters, digits or hyphens, not starting or ending with a hyphen.";
    for (const v of preset && !editing ? preset.vars ?? [] : []) {
      const val = vars[v.key] ?? "";
      if (v.type === "text" && (v.required || val) && !PRESET_TEXT_VALUE.test(val)) e[`var-${v.key}`] = `Enter the ${v.label.toLowerCase()} (letters, digits, dot, dash or underscore).`;
    }
    if (!preset || editing) {
      try {
        const u = new URL(effectiveURL);
        if (u.protocol !== "https:" && u.protocol !== "http:") e.url = "Use an http or https URL.";
      } catch {
        e.url = "Enter the server's full URL, for example https://mcp.example.com/mcp.";
      }
    }
    if (authType === "header" && !/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(authHeader)) e.authHeader = "Enter the header name, for example x-api-key.";
    if (authType === "basic" && !username.trim()) e.username = "Basic authentication needs a username.";
    if (authType !== "none" && replaceSecret && !secretOptional && !secret) e.secret = `${preset?.name ?? "This server"} needs a ${secretLabel.toLowerCase()}.`;
    const t = Number(timeoutS);
    if (!Number.isFinite(t) || t < 1 || t > 600) e.timeout = "Use 1 to 600 seconds.";
    if (replaceHeaders) {
      for (const [i, h] of headers.entries()) {
        if (h.name && !/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(h.name)) e[`header-${i}`] = `"${h.name}" is not a valid header name.`;
      }
    }
    return e;
  }, [name, slug, editing, preset, vars, effectiveURL, authType, authHeader, username, replaceSecret, secretOptional, secret, secretLabel, timeoutS, replaceHeaders, headers]);
  const valid = Object.keys(errors).length === 0;
  const show = (key: string) => (submitted ? errors[key] : undefined);

  const buildInput = (): MCPServerInput => {
    const input: MCPServerInput = {
      name: name.trim(),
      description: description.trim(),
      url: effectiveURL,
      auth_type: authType,
      auth_header: authType === "header" ? authHeader.trim() : "",
      timeout_ms: Math.round(Number(timeoutS) * 1000),
      forward_headers: forward
        .split(",")
        .map((h) => h.trim())
        .filter(Boolean),
    };
    if (!editing) {
      input.slug = slug;
      if (preset) input.preset_id = preset.id;
    }
    if (authType === "basic") input.username = username.trim();
    if (authType !== "none" && replaceSecret) input.secret = secret;
    if (authType === "none" && editing && server?.has_secret) input.secret = "";
    if (replaceHeaders) {
      input.static_headers = Object.fromEntries(headers.filter((h) => h.name.trim()).map((h) => [h.name.trim(), h.value]));
    }
    return input;
  };

  // The settings a result was measured with: editing any of them after a test
  // marks the result as out of date instead of leaving it to vouch for them.
  // Name, description and timeout do not change what the test sees.
  const { name: _n, description: _d, timeout_ms: _t, ...connection } = buildInput();
  const signature = JSON.stringify(connection);
  const testConn = useMutation({
    mutationFn: (_signature: string) => {
      const input = buildInput();
      if (editing && server) input.server_id = server.id;
      return api.testMcpServer(input);
    },
    onSuccess: (res, sig) => setTest({ result: res, signature: sig }),
    onError: (e: Error, sig) => setTest({ result: { ok: false, error: e.message, latency_ms: 0 }, signature: sig }),
  });

  const submit = (ev: FormEvent) => {
    ev.preventDefault();
    setSubmitted(true);
    if (!valid) return;
    void onSubmit(buildInput());
  };

  const exposedPreview = slug ? `${slug}__tool_name` : "slug__tool_name";

  return (
    <form onSubmit={submit} noValidate className="flex min-h-0 flex-col">
      <div className="space-y-4 px-5 py-4">
        {preset?.notes && !editing && <p className="rounded-lg bg-subtle px-3 py-2 text-[12.5px] leading-5 text-fg-muted">{preset.notes}</p>}

        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Name" required error={show("name")}>
            <Input
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                if (!slugTouched) setSlug(slugFromName(e.target.value));
              }}
              placeholder="GitHub"
              autoFocus={!editing}
              maxLength={80}
            />
          </Field>
          {editing ? (
            <Field label="Slug" hint="Clients see tools as this slug plus the tool name. It cannot change.">
              <Input value={server?.slug ?? ""} readOnly className="font-mono" />
            </Field>
          ) : (
            <Field label="Slug" required error={show("slug")} hint={<>Tools appear as <span className="font-mono">{exposedPreview}</span>. Short slugs keep names under client limits.</>}>
              <Input
                value={slug}
                onChange={(e) => {
                  setSlug(e.target.value.toLowerCase());
                  setSlugTouched(true);
                }}
                className="font-mono"
                maxLength={32}
                spellCheck={false}
                autoCapitalize="off"
              />
            </Field>
          )}
        </div>

        {preset && !editing ? (
          <>
            {(preset.vars ?? []).map((v) => (
              <Field key={v.key} label={v.label} required={v.required} hint={v.hint} error={show(`var-${v.key}`)}>
                {v.type === "select" ? (
                  <Select value={vars[v.key] ?? ""} onChange={(e) => setVars({ ...vars, [v.key]: e.target.value })}>
                    {v.options?.map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.label}
                      </option>
                    ))}
                  </Select>
                ) : v.type === "bool" ? (
                  <Select value={vars[v.key] ?? "false"} onChange={(e) => setVars({ ...vars, [v.key]: e.target.value })}>
                    <option value="true">Yes</option>
                    <option value="false">No</option>
                  </Select>
                ) : (
                  <Input value={vars[v.key] ?? ""} onChange={(e) => setVars({ ...vars, [v.key]: e.target.value.trim() })} className="font-mono" spellCheck={false} />
                )}
              </Field>
            ))}
            <div className="space-y-1">
              <p className="text-[12.5px] font-medium text-fg">Server URL</p>
              <p className="break-all rounded-lg border border-line bg-subtle px-3 py-2 font-mono text-[12.5px] text-fg-muted">{presetURL}</p>
            </div>
          </>
        ) : (
          <Field label="Server URL" required error={show("url")} hint="The server's Streamable HTTP endpoint.">
            <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://mcp.example.com/mcp" className="font-mono" spellCheck={false} inputMode="url" />
          </Field>
        )}

        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Authentication">
            <Select value={authType} onChange={(e) => setAuthType(e.target.value as MCPAuthType)} disabled={authChoices.length === 1}>
              {authChoices.map((a) => (
                <option key={a} value={a}>
                  {AUTH_LABELS[a]}
                </option>
              ))}
            </Select>
          </Field>
          {authType === "header" && (
            <Field label="Header name" required error={show("authHeader")}>
              <Input value={authHeader} onChange={(e) => setAuthHeader(e.target.value.trim())} placeholder="x-api-key" className="font-mono" readOnly={!!preset?.auth_header} />
            </Field>
          )}
          {authType === "basic" && (
            <Field label="Username" required error={show("username")}>
              <Input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" />
            </Field>
          )}
        </div>

        {authType !== "none" &&
          (replaceSecret ? (
            <Field
              label={secretLabel}
              required={!secretOptional}
              optional={secretOptional && !editing}
              error={show("secret")}
              hint={
                <>
                  {editing && server?.has_secret ? "Leave empty to keep the saved one. " : ""}
                  Every API key that may use this server shares this credential; prefer a service account or a read-only token.
                  {preset?.secret_help_url && (
                    <>
                      {" "}
                      <a href={preset.secret_help_url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-0.5 text-link hover:underline">
                        Create one
                        <ExternalLink className="h-3 w-3" aria-hidden="true" />
                      </a>
                    </>
                  )}
                </>
              }
            >
              <Input type="password" value={secret} onChange={(e) => setSecret(e.target.value)} autoComplete="new-password" spellCheck={false} />
            </Field>
          ) : (
            <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-line px-3 py-2">
              <span className="text-[13px] text-fg-muted">{secretLabel} saved</span>
              <Button type="button" variant="ghost" size="sm" onClick={() => setReplaceSecret(true)}>
                Replace
              </Button>
            </div>
          ))}

        <Field label="Description" optional>
          <Textarea value={description} onChange={(e) => setDescription(e.target.value)} rows={2} maxLength={500} placeholder={preset?.description} />
        </Field>

        <div className="rounded-lg border border-line">
          <button
            type="button"
            onClick={() => setAdvanced((v) => !v)}
            aria-expanded={advanced}
            aria-controls={`${uid}-advanced`}
            className={cn("flex w-full items-center justify-between gap-2 rounded-lg px-3 py-2.5 text-left text-[13px] font-medium text-fg hover:bg-hover", FOCUS_RING)}
          >
            Advanced · headers and timeout
            <ChevronDown className={cn("h-4 w-4 text-fg-faint transition-transform", advanced && "rotate-180")} aria-hidden="true" />
          </button>
          {advanced && (
            <div id={`${uid}-advanced`} className="space-y-4 border-t border-line px-3 py-3">
              <div className="space-y-2">
                <p className="text-[12.5px] font-medium text-fg">Static headers</p>
                {!replaceHeaders ? (
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <span className="font-mono text-[12.5px] text-fg-muted">{server?.static_header_names.join(", ")}</span>
                    <Button type="button" variant="ghost" size="sm" onClick={() => setReplaceHeaders(true)}>
                      Replace headers
                    </Button>
                  </div>
                ) : (
                  <>
                    <p className="text-[12px] leading-5 text-fg-muted">Sent on every request. Values are stored encrypted and never shown again.</p>
                    {headers.map((h, i) => (
                      <div key={i} className="space-y-1">
                        <div className="flex gap-2">
                          <Input aria-label={`Header ${i + 1} name`} value={h.name} onChange={(e) => setHeaders(headers.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} placeholder="X-Team" className="font-mono" />
                          <Input aria-label={`Header ${i + 1} value`} value={h.value} onChange={(e) => setHeaders(headers.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))} placeholder="value" />
                          <Button type="button" variant="quiet" size="icon" aria-label={`Remove header ${i + 1}`} onClick={() => setHeaders(headers.filter((_, j) => j !== i))}>
                            <X aria-hidden="true" />
                          </Button>
                        </div>
                        {show(`header-${i}`) && <p className="text-[12px] text-bad">{show(`header-${i}`)}</p>}
                      </div>
                    ))}
                    {headers.length < 20 && (
                      <Button type="button" variant="ghost" size="sm" onClick={() => setHeaders([...headers, { name: "", value: "" }])}>
                        <Plus aria-hidden="true" />
                        Add header
                      </Button>
                    )}
                  </>
                )}
              </div>
              <Field label="Forward client headers" optional hint="Comma-separated names copied from the client's request. Authorization is never forwarded.">
                <Input value={forward} onChange={(e) => setForward(e.target.value)} placeholder="X-Request-Source" className="font-mono" spellCheck={false} />
              </Field>
              <Field label="Timeout" error={show("timeout")} hint="Seconds to wait for one tool call.">
                <Input type="number" min={1} max={600} value={timeoutS} onChange={(e) => setTimeoutS(e.target.value)} className="w-32 tabular-nums" />
              </Field>
            </div>
          )}
        </div>

        <div ref={testRef} aria-live="polite" className="scroll-mb-48 sm:scroll-mb-20">
          {test && <TestResult result={test.result} host={hostOf(effectiveURL)} stale={test.signature !== signature} />}
        </div>
        {mode === "create" && keysWithAllAccess !== undefined && keysWithAllAccess > 0 && (
          <p className="text-[12.5px] text-fg-muted">
            {keysWithAllAccess} API key{keysWithAllAccess === 1 ? "" : "s"} with access to every MCP server will be able to use it.{" "}
            <Link to="/keys" className="text-link hover:underline">
              Review keys
            </Link>
          </p>
        )}
        {submitted && !valid && <ErrorBanner message="Fix the highlighted fields to continue." />}
      </div>

      <div className="sticky bottom-0 flex flex-col gap-2 border-t border-line bg-subtle px-5 py-3 sm:flex-row sm:items-center sm:justify-end [&>button]:w-full sm:[&>button]:w-auto">
        {onCancel && (
          <Button type="button" variant="quiet" onClick={onCancel} className="sm:mr-auto">
            Cancel
          </Button>
        )}
        <Button
          type="button"
          variant="secondary"
          disabled={testConn.isPending}
          onClick={() => {
            setSubmitted(true);
            if (valid) testConn.mutate(signature);
          }}
        >
          {testConn.isPending ? "Testing…" : test && test.signature !== signature ? "Test again" : "Test connection"}
        </Button>
        <Button type="submit" disabled={submitting}>
          {submitting ? "Saving…" : submitLabel}
        </Button>
      </div>
    </form>
  );
}
