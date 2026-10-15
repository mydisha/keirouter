import { useId, useMemo, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2, Copy, Check, MoreHorizontal, ExternalLink, AlertCircle, BookOpen, Binary, AudioLines, Mic, Search, Pencil, NotebookPen, Info, LayoutTemplate, ArrowRight, type LucideIcon } from "lucide-react";
import { ICONS } from "../lib/icons";
import { api, type APIKey, type Plan, type Skill, type SkillScope } from "../lib/api";
import {
  SKILL_TEMPLATE_CATEGORIES,
  SKILL_TEMPLATES,
  placeholderNames,
  renderPrompt,
  templateById,
  variableExample,
  variableLabel,
  type SkillTemplate,
  type SkillTemplateCategory,
} from "../lib/skillTemplates";
import { ModelMultiSelect } from "../components/ModelSelect";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { Badge, Button, EmptyState, Field, IconTile, Input, Modal, SectionTitle, SegmentedControl, Skeleton, Textarea, Toggle, ErrorBanner } from "../components/ui";
import { useConfirm } from "../components/ui/confirm-dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../components/ui/dropdown-menu";
import { dashboard } from "../lib/dashboardRoutes";

// Built-in reference skills — SKILL.md pages that teach AI agents how to call
// KeiRouter endpoints. These are static docs with copyable URLs, not runtime
// request modifiers.
const REFERENCE_SKILLS = [
  { id: "keirouter", name: "KeiRouter (entry)", endpoint: null as string | null, description: "Setup guide and index of all capabilities.", group: "entry" },
  { id: "keirouter-chat", name: "Chat", endpoint: "/v1/chat/completions", description: "Chat and code generation via OpenAI or Anthropic format with streaming.", group: "Text" },
  { id: "keirouter-embeddings", name: "Embeddings", endpoint: "/v1/embeddings", description: "Vectors for RAG and semantic search.", group: "Text" },
  { id: "keirouter-image", name: "Image generation", endpoint: "/v1/images/generations", description: "Text-to-image via OpenAI, xAI, Venice and Recraft.", group: "Media" },
  { id: "keirouter-tts", name: "Text-to-speech", endpoint: "/v1/audio/speech", description: "OpenAI voices, or any OpenAI-compatible speech server.", group: "Media" },
  { id: "keirouter-stt", name: "Speech-to-text", endpoint: "/v1/audio/transcriptions", description: "Transcribe via OpenAI Whisper or Groq.", group: "Media" },
  { id: "keirouter-web-search", name: "Web search", endpoint: "/v1/search", description: "Tavily, Exa, Brave Search and Serper.", group: "Web" },
  { id: "keirouter-web-fetch", name: "Web fetch", endpoint: "/v1/web/fetch", description: "URL to markdown/text/HTML via Firecrawl, Tavily and Exa.", group: "Web" },
];

type ReferenceSkill = (typeof REFERENCE_SKILLS)[number];

// One glyph per reference skill, matching what the endpoint does.
const REFERENCE_ICONS: Record<string, LucideIcon> = {
  keirouter: ICONS.skills,
  "keirouter-chat": ICONS.prompt,
  "keirouter-embeddings": Binary,
  "keirouter-image": ICONS.media,
  "keirouter-tts": AudioLines,
  "keirouter-stt": Mic,
  "keirouter-web-search": Search,
  "keirouter-web-fetch": ICONS.tunnel,
};

const ENTRY_SKILL = REFERENCE_SKILLS.find((skill) => skill.group === "entry")!;
const ENDPOINT_SKILLS = REFERENCE_SKILLS.filter((skill) => skill.group !== "entry");

// Mirrors the gateway's limit in backend/internal/gateway/skills.go.
const MAX_PROMPT_CHARS = 20000;
const MAX_VARIABLE_CHARS = 1000;
// Above this many injected tokens every request pays a noticeable tax.
const HEAVY_TOKENS = 2000;

const focusRing = "focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

const skillUrl = (id: string) => `https://raw.githubusercontent.com/mydisha/keirouter/main/skills/${id}/SKILL.md`;
const agentInstruction = (id: string) => `Read this skill and use it: ${skillUrl(id)}`;

// Rough token estimate (~4 characters per token) — enough to flag heavy skills.
const estimateTokens = (text: string) => Math.ceil(text.trim().length / 4);
const formatTokens = (n: number) => `~${n.toLocaleString()} token${n === 1 ? "" : "s"}`;

type Mode = "all" | "specific";
type Draft = {
  id?: string;
  name: string;
  description: string;
  prompt: string;
  optional: boolean;
  audience: Mode;
  plans: string[];
  keys: string[];
  modelMode: Mode;
  models: string[];
  /** Values for the prompt's {{placeholders}}. */
  variables: Record<string, string>;
  /** Template the draft started from; drives the scope tip in the editor. */
  template?: string;
};
const EMPTY_DRAFT: Draft = { name: "", description: "", prompt: "", optional: false, audience: "all", plans: [], keys: [], modelMode: "all", models: [], variables: {} };

const draftFromSkill = (sk: Skill): Draft => {
  const plans = sk.scope?.plans ?? [];
  const keys = sk.scope?.keys ?? [];
  const models = sk.scope?.models ?? [];
  return {
    id: sk.id,
    name: sk.name,
    description: sk.description,
    prompt: sk.prompt,
    optional: !!sk.optional,
    audience: plans.length || keys.length ? "specific" : "all",
    plans,
    keys,
    modelMode: models.length ? "specific" : "all",
    models,
    variables: { ...sk.variables },
  };
};

// draftFromTemplate pre-fills the template's variables with values already
// used by other skills, so e.g. the company name is typed only once.
const draftFromTemplate = (t: SkillTemplate, known: Record<string, string>): Draft => ({
  ...EMPTY_DRAFT,
  name: t.name,
  description: t.description,
  prompt: t.prompt,
  optional: t.optional,
  variables: Object.fromEntries(placeholderNames(t.prompt).filter((n) => known[n]).map((n) => [n, known[n]])),
  template: t.id,
});

// Only variables the prompt still uses, with a value, are saved.
const draftVariables = (d: Draft) =>
  Object.fromEntries(placeholderNames(d.prompt).map((n) => [n, d.variables[n]?.trim() ?? ""]).filter(([, v]) => v));

// What the model receives: the prompt with its variables filled in.
const renderedPrompt = (sk: Pick<Skill, "prompt" | "variables">) => renderPrompt(sk.prompt, sk.variables ?? {});

const draftScope = (d: Draft): SkillScope => ({
  plans: d.audience === "specific" ? d.plans : [],
  keys: d.audience === "specific" ? d.keys : [],
  models: d.modelMode === "specific" ? d.models : [],
});

const isGlobal = (sk: Skill) => !sk.scope?.plans?.length && !sk.scope?.keys?.length && !sk.scope?.models?.length;

// Lookup tables that turn scope ids into names for badges and pickers.
type Directory = { plans: Map<string, string>; keys: Map<string, string> };

export function SkillsPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const askConfirm = useConfirm();
  const skills = useQuery({ queryKey: ["skills"], queryFn: () => api.listSkills() });
  const plans = useQuery({ queryKey: ["plans"], queryFn: () => api.listPlans(), staleTime: 60_000 });
  const keys = useQuery({ queryKey: ["keys"], queryFn: () => api.listKeys(), staleTime: 60_000 });

  const directory = useMemo<Directory>(() => ({
    plans: new Map((plans.data?.plans ?? []).map((p) => [p.id, p.name])),
    keys: new Map((keys.data?.keys ?? []).map((k) => [k.id, k.name])),
  }), [plans.data, keys.data]);

  const [draft, setDraft] = useState<Draft | null>(null);
  const [error, setError] = useState("");

  const save = useMutation({
    mutationFn: (d: Draft) => {
      const body = { name: d.name.trim(), description: d.description.trim(), prompt: d.prompt.trim(), optional: d.optional, scope: draftScope(d), variables: draftVariables(d) };
      return d.id ? api.updateSkill(d.id, body) : api.createSkill(body);
    },
    onSuccess: (_, d) => {
      qc.invalidateQueries({ queryKey: ["skills"] });
      setDraft(null);
      setError("");
      toast.success(d.id ? "Skill updated" : "Skill created", `"${d.name.trim()}" applies to new matching requests.`);
    },
    onError: (e) => setError((e as Error).message),
  });

  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => api.updateSkill(id, { enabled }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["skills"] }),
    onError: (e) => toast.error("Skill toggle failed", (e as Error).message),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.deleteSkill(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["skills"] });
      toast.success("Skill deleted", "Its prompt is no longer added to requests.");
    },
    onError: (e) => toast.error("Skill removal failed", (e as Error).message),
  });

  const deleteSkill = async (sk: Skill) => {
    const ok = await askConfirm({
      title: `Delete "${sk.name}"?`,
      description: "Its prompt stops being added to requests immediately. This cannot be undone.",
      confirmLabel: "Delete skill",
      tone: "danger",
    });
    if (ok) remove.mutate(sk.id);
  };

  const copyText = async (text: string, label: string) => {
    try {
      await navigator.clipboard.writeText(text);
      toast.success("Copied", label);
    } catch {
      toast.error("Copy failed", "The browser blocked clipboard access.");
    }
  };

  const openEditor = (d: Draft) => {
    setError("");
    setDraft(d);
  };

  const custom = skills.data?.skills ?? [];
  const active = custom.filter((sk) => sk.enabled && sk.prompt.trim());
  const everyTokens = active.filter(isGlobal).reduce((sum, sk) => sum + estimateTokens(renderedPrompt(sk)), 0);
  const maxTokens = active.reduce((sum, sk) => sum + estimateTokens(renderedPrompt(sk)), 0);
  // Variable values already in use, offered to new skills from templates.
  const knownVariables = useMemo(() => Object.assign({}, ...custom.map((sk) => sk.variables ?? {})) as Record<string, string>, [custom]);
  const isEmpty = !skills.isLoading && !skills.isError && custom.length === 0;

  return (
    <>
      <PageHeader
        title="Skills"
        description="Standing instructions the gateway adds to matching chat requests, plus reference docs that teach agents to use KeiRouter."
        action={
          isEmpty ? undefined : (
            <Button onClick={() => openEditor(EMPTY_DRAFT)}>
              <Plus aria-hidden="true" />
              New skill
            </Button>
          )
        }
      />

      <div className="space-y-6">
        {/* Custom skills */}
        <section aria-labelledby="custom-skills-heading" className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
          <div className="border-b border-line px-4 py-3">
            <SectionTitle
              id="custom-skills-heading"
              icon={NotebookPen}
              title={(
                <>
                  Custom skills
                  {custom.length > 0 && <span className="ml-2 text-[12px] font-normal tabular-nums text-fg-muted">{custom.length}</span>}
                </>
              )}
              subtitle="Prepended to the system prompt of matching chat requests — no client changes needed"
            />
          </div>

          {custom.length > 0 && <InjectionSummary activeCount={active.length} everyTokens={everyTokens} maxTokens={maxTokens} />}

          {skills.isLoading ? (
            <div aria-busy="true" aria-label="Loading skills" className="divide-y divide-line">
              {Array.from({ length: 3 }).map((_, i) => (
                <div key={i} className="flex items-center gap-4 px-4 py-3">
                  <Skeleton className="h-7 w-7 rounded-lg" />
                  <div className="flex-1 space-y-1.5">
                    <Skeleton className="h-3.5 w-40" />
                    <Skeleton className="h-3 w-64" />
                  </div>
                  <Skeleton className="h-5 w-9 rounded-full" />
                </div>
              ))}
            </div>
          ) : skills.isError ? (
            <div className="p-4">
              <div role="alert" className="flex items-start gap-2.5 rounded-lg border border-bad/30 bg-bad/5 px-3.5 py-2.5">
                <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-bad" strokeWidth={1.75} aria-hidden="true" />
                <p className="text-[13px] text-bad">
                  Couldn't load skills. {skills.error instanceof Error ? `${skills.error.message}. ` : ""}Refresh the page to try again.
                </p>
              </div>
            </div>
          ) : custom.length === 0 ? (
            <EmptyState
              icon={NotebookPen}
              title="No custom skills yet"
              hint="Write an instruction once — house style, review rules, a persona — and every request routed through KeiRouter receives it. Scope it to plans, API keys or models when it shouldn't apply everywhere."
              action={(
                <Button onClick={() => openEditor(EMPTY_DRAFT)}>
                  <Plus aria-hidden="true" />
                  Create skill
                </Button>
              )}
              secondaryAction={(
                <Button variant="ghost" onClick={() => document.getElementById("skill-templates")?.scrollIntoView({ behavior: "smooth", block: "start" })}>
                  Or start from a template
                  <ArrowRight aria-hidden="true" />
                </Button>
              )}
            />
          ) : (
            <ul className="divide-y divide-line">
              {custom.map((sk) => (
                <SkillRow
                  key={sk.id}
                  skill={sk}
                  directory={directory}
                  onToggle={(enabled) => toggle.mutate({ id: sk.id, enabled })}
                  onEdit={() => openEditor(draftFromSkill(sk))}
                  onDelete={() => deleteSkill(sk)}
                  onCopyPrompt={() => copyText(renderedPrompt(sk), `Prompt for ${sk.name}`)}
                />
              ))}
            </ul>
          )}
        </section>

        {/* Templates */}
        <SkillTemplates onUse={(t) => openEditor(draftFromTemplate(t, knownVariables))} />

        {/* Reference skills */}
        <ReferenceSkills />
      </div>

      <SkillEditor
        draft={draft}
        error={error}
        pending={save.isPending}
        plans={plans.data?.plans ?? []}
        keys={keys.data?.keys ?? []}
        directoryLoading={plans.isLoading || keys.isLoading}
        onChange={setDraft}
        onClose={() => setDraft(null)}
        onSubmit={() => draft && save.mutate(draft)}
      />
    </>
  );
}

// InjectionSummary states what the enabled skills cost: global skills ride
// along on every request, scoped ones only when they match.
function InjectionSummary({ activeCount, everyTokens, maxTokens }: { activeCount: number; everyTokens: number; maxTokens: number }) {
  const heavy = maxTokens > HEAVY_TOKENS;
  return (
    <div className={`flex items-start gap-2 border-b border-line px-4 py-2.5 text-[12.5px] ${heavy ? "bg-warn/10 text-warn" : "bg-subtle text-fg-muted"}`}>
      <Info className="mt-0.5 h-3.5 w-3.5 shrink-0" strokeWidth={1.75} aria-hidden="true" />
      <p>
        {activeCount === 0 ? (
          "No skill is active — requests pass through unchanged."
        ) : (
          <>
            <span className="font-medium text-fg">{activeCount} active</span>
            {" · "}
            <span className="tabular-nums">{formatTokens(everyTokens)}</span> on every chat request
            {maxTokens > everyTokens && (
              <>
                , up to <span className="tabular-nums">{formatTokens(maxTokens)}</span> when scoped skills match
              </>
            )}
            {heavy && ". Consider trimming — this is billed as input on each call."}
          </>
        )}
      </p>
    </div>
  );
}

function SkillEditor({
  draft,
  error,
  pending,
  plans,
  keys,
  directoryLoading,
  onChange,
  onClose,
  onSubmit,
}: {
  draft: Draft | null;
  error: string;
  pending: boolean;
  plans: Plan[];
  keys: APIKey[];
  directoryLoading: boolean;
  onChange: (d: Draft) => void;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const d = draft ?? EMPTY_DRAFT;
  const editing = !!d.id;
  const audienceMissing = d.audience === "specific" && d.plans.length + d.keys.length === 0;
  const modelsMissing = d.modelMode === "specific" && d.models.length === 0;
  const template = editing ? undefined : templateById(d.template);
  const variableNames = placeholderNames(d.prompt);
  const emptyVariables = variableNames.filter((n) => !d.variables[n]?.trim());
  const rendered = renderPrompt(d.prompt, d.variables);
  // The gateway limits both the stored and the filled-in prompt.
  const chars = Math.max(d.prompt.trim().length, rendered.trim().length);
  const over = chars > MAX_PROMPT_CHARS;
  const canSave = !!d.name.trim() && !over && !audienceMissing && !modelsMissing && !pending;
  const toggleIn = (list: string[], id: string) => (list.includes(id) ? list.filter((v) => v !== id) : [...list, id]);

  return (
    <Modal
      open={draft !== null}
      onClose={onClose}
      title={editing ? "Edit skill" : "New skill"}
      subtitle="Prepended to the system prompt of matching chat requests"
      maxWidth="max-w-2xl"
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (canSave) onSubmit();
        }}
        aria-busy={pending || undefined}
      >
        <div className="max-h-[calc(100dvh-15rem)] space-y-4 overflow-y-auto px-4 py-4 sm:max-h-[70vh] sm:px-5">
          {template && (
            <div className="flex items-start gap-2 rounded-lg border border-line bg-subtle px-3.5 py-2.5 text-[12.5px] text-fg-muted">
              <LayoutTemplate className="mt-0.5 h-3.5 w-3.5 shrink-0" strokeWidth={1.75} aria-hidden="true" />
              <p>
                Started from the <span className="font-medium text-fg">{template.name}</span> template — adjust it to your use case before saving.
                {template.scopeTip && <> {template.scopeTip}</>}
              </p>
            </div>
          )}
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Name" required>
              <Input value={d.name} onChange={(e) => onChange({ ...d, name: e.target.value })} placeholder="Concise reviewer" maxLength={80} autoFocus />
            </Field>
            <Field label="Description" optional>
              <Input value={d.description} onChange={(e) => onChange({ ...d, description: e.target.value })} placeholder="Short summary of what it does" />
            </Field>
          </div>
          <Field
            label="Prompt"
            hint={(
              <span className="flex justify-between gap-3">
                <span>
                  Write it as a direct instruction. Use <code className="font-mono text-[11.5px]">{"{{name}}"}</code> for values you fill in below.
                </span>
                <span className={`shrink-0 tabular-nums ${over ? "text-bad" : ""}`}>
                  {chars.toLocaleString()} / {MAX_PROMPT_CHARS.toLocaleString()} · {formatTokens(estimateTokens(rendered))}
                </span>
              </span>
            )}
            error={over ? "Prompt is too long." : undefined}
          >
            <Textarea
              value={d.prompt}
              onChange={(e) => onChange({ ...d, prompt: e.target.value })}
              rows={8}
              spellCheck={false}
              placeholder="You are a meticulous code reviewer. Prefer small, safe diffs…"
              className="font-mono text-[12px]"
            />
          </Field>
          {variableNames.length > 0 && (
            <fieldset className="space-y-3 rounded-xl border border-line p-4 pt-1 [&>legend]:mb-0">
              <legend className="px-1 text-[12.5px] font-semibold text-fg">Variables</legend>
              <p className="text-[12px] leading-5 text-fg-muted">
                Filled into the prompt's placeholders on every request — change a value here instead of editing the text.
              </p>
              <div className="grid gap-3 sm:grid-cols-2">
                {variableNames.map((name) => {
                  const example = variableExample(name);
                  return (
                    <Field key={name} label={variableLabel(name)} hint={<code className="font-mono text-[11.5px]">{`{{${name}}}`}</code>}>
                      <Input
                        value={d.variables[name] ?? ""}
                        onChange={(e) => onChange({ ...d, variables: { ...d.variables, [name]: e.target.value } })}
                        placeholder={example ? `e.g. ${example}` : undefined}
                        maxLength={MAX_VARIABLE_CHARS}
                        aria-label={variableLabel(name)}
                      />
                    </Field>
                  );
                })}
              </div>
              {emptyVariables.length > 0 && (
                <p className="flex items-start gap-1.5 text-[12px] leading-5 text-warn">
                  <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" strokeWidth={1.75} aria-hidden="true" />
                  {emptyVariables.length === 1 ? "1 variable is empty" : `${emptyVariables.length} variables are empty`} — the model receives the raw placeholder until you fill {emptyVariables.length === 1 ? "it" : "them"} in.
                </p>
              )}
              <details className="group rounded-lg border border-line">
                <summary className={`cursor-pointer select-none rounded-lg px-3 py-2 text-[12.5px] font-medium text-fg-muted hover:text-fg ${focusRing}`}>
                  Preview what the model receives
                </summary>
                <pre className="max-h-60 overflow-auto whitespace-pre-wrap break-words border-t border-line px-3 py-2.5 font-mono text-[12px] leading-5 text-fg">{rendered || "—"}</pre>
              </details>
            </fieldset>
          )}

          <fieldset className="space-y-4 rounded-xl border border-line p-4 pt-1 [&>legend]:mb-0">
            <legend className="px-1 text-[12.5px] font-semibold text-fg">Applies to</legend>

            <div className="space-y-2">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span id="skill-audience-label" className="text-[12.5px] font-medium text-fg">Callers</span>
                <SegmentedControl
                  aria-labelledby="skill-audience-label"
                  value={d.audience}
                  onChange={(audience) => onChange({ ...d, audience })}
                  options={[{ value: "all", label: "Every API key" }, { value: "specific", label: "Specific plans or keys" }]}
                />
              </div>
              {d.audience === "specific" && (
                <div className="space-y-3">
                  <ChipPicker
                    label="Plans"
                    empty={<>No plans yet — create one in <Link to={dashboard("/plans")} className="text-link underline-offset-2 hover:underline">Plans &amp; budgets</Link>.</>}
                    loading={directoryLoading}
                    options={plans.map((p) => ({ id: p.id, label: p.name }))}
                    selected={d.plans}
                    onToggle={(id) => onChange({ ...d, plans: toggleIn(d.plans, id) })}
                  />
                  <ChipPicker
                    label="API keys"
                    empty={<>No API keys yet — create one in <Link to={dashboard("/keys")} className="text-link underline-offset-2 hover:underline">API keys</Link>.</>}
                    loading={directoryLoading}
                    options={keys.map((k) => ({ id: k.id, label: k.name || k.display, hint: k.plan_name, title: k.display }))}
                    selected={d.keys}
                    onToggle={(id) => onChange({ ...d, keys: toggleIn(d.keys, id) })}
                  />
                  <p className={`text-[12px] ${audienceMissing ? "text-bad" : "text-fg-muted"}`}>
                    {audienceMissing ? "Pick at least one plan or key." : "Matches when the request's key is selected or belongs to a selected plan."}
                  </p>
                </div>
              )}
            </div>

            <div className="space-y-2 border-t border-line pt-4">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span id="skill-models-label" className="text-[12.5px] font-medium text-fg">Models</span>
                <SegmentedControl
                  aria-labelledby="skill-models-label"
                  value={d.modelMode}
                  onChange={(modelMode) => onChange({ ...d, modelMode })}
                  options={[{ value: "all", label: "All models" }, { value: "specific", label: "Specific models" }]}
                />
              </div>
              {d.modelMode === "specific" && (
                <>
                  <ModelMultiSelect value={d.models} onChange={(models) => onChange({ ...d, models })} label="Skill models" />
                  <p className={`text-[12px] ${modelsMissing ? "text-bad" : "text-fg-muted"}`}>
                    {modelsMissing
                      ? "Pick at least one model or pattern."
                      : "Matched against the requested model and the primary routed model. Patterns like claude-* match a whole family."}
                  </p>
                </>
              )}
            </div>
          </fieldset>

          <div className="flex items-start justify-between gap-4 rounded-xl border border-line px-4 py-3">
            <div className="min-w-0">
              <p id="skill-optional-label" className="text-[12.5px] font-medium text-fg">Optional</p>
              <p id="skill-optional-hint" className="mt-0.5 text-[12px] leading-5 text-fg-muted">
                Clients can skip it by sending <code className="rounded bg-subtle px-1 font-mono text-[11.5px]">X-KeiRouter-Skip-Skills: true</code> (e.g. for evals). Leave off for rules every request must follow.
              </p>
            </div>
            <Toggle
              checked={d.optional}
              onChange={(optional) => onChange({ ...d, optional })}
              aria-labelledby="skill-optional-label"
              aria-describedby="skill-optional-hint"
            />
          </div>

          {error && <ErrorBanner message={error} />}
        </div>
        <div className="flex flex-col gap-2 border-t border-line bg-subtle px-4 py-3 sm:flex-row sm:justify-end sm:px-5">
          <Button type="button" variant="ghost" onClick={onClose} className="w-full sm:w-auto">
            Cancel
          </Button>
          <Button type="submit" disabled={!canSave} className="w-full sm:w-auto">
            {pending ? "Saving…" : editing ? "Save changes" : "Create skill"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}

// ChipPicker is a multi-select of toggle buttons (aria-pressed), named by the
// visible label above it.
function ChipPicker({
  label,
  options,
  selected,
  onToggle,
  empty,
  loading,
}: {
  label: string;
  options: { id: string; label: string; hint?: string; title?: string }[];
  selected: string[];
  onToggle: (id: string) => void;
  empty: ReactNode;
  loading: boolean;
}) {
  const id = useId();
  // Keep ids that no longer exist visible so they can be removed.
  const known = new Set(options.map((o) => o.id));
  const all = [...options, ...selected.filter((s) => !known.has(s)).map((s) => ({ id: s, label: "Deleted", hint: undefined as string | undefined, title: undefined as string | undefined }))];
  return (
    <div className="space-y-1.5">
      <span id={`${id}-label`} className="text-[12px] font-medium text-fg-muted">
        {label}
        {selected.length > 0 && <span className="ml-1.5 tabular-nums">{selected.length}</span>}
      </span>
      {loading ? (
        <div className="flex flex-wrap gap-1.5" aria-busy="true" aria-label={`Loading ${label.toLowerCase()}`}>
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-7 w-24 rounded-lg" />
          ))}
        </div>
      ) : all.length === 0 ? (
        <p className="text-[12px] text-fg-muted">{empty}</p>
      ) : (
        <div role="group" aria-labelledby={`${id}-label`} className="flex flex-wrap gap-1.5">
          {all.map((o) => {
            const on = selected.includes(o.id);
            return (
              <button
                key={o.id}
                type="button"
                aria-pressed={on}
                title={o.title}
                onClick={() => onToggle(o.id)}
                className={`inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-[12px] font-medium transition-colors sm:h-7 ${focusRing} ${
                  on ? "border-accent-500/40 bg-accent-500/10 text-link" : "border-line bg-surface text-fg-muted hover:border-fg-faint hover:text-fg"
                }`}
              >
                {on && <Check className="h-3.5 w-3.5" aria-hidden="true" />}
                {o.label}
                {o.hint && <span className="font-normal text-fg-faint">· {o.hint}</span>}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ScopeBadges summarises where a skill applies; long lists collapse to "+N".
function ScopeBadges({ skill, directory }: { skill: Skill; directory: Directory }) {
  const plans = skill.scope?.plans ?? [];
  const keys = skill.scope?.keys ?? [];
  const models = skill.scope?.models ?? [];
  const items: { key: string; icon: LucideIcon; label: string }[] = [
    ...plans.map((id) => ({ key: `p-${id}`, icon: ICONS.plans, label: directory.plans.get(id) ?? "Deleted plan" })),
    ...keys.map((id) => ({ key: `k-${id}`, icon: ICONS.keys, label: directory.keys.get(id) ?? "Deleted key" })),
    ...models.map((m) => ({ key: `m-${m}`, icon: ICONS.model, label: m })),
  ];
  const shown = items.slice(0, 4);
  const rest = items.length - shown.length;
  return (
    <span className="mt-1.5 flex flex-wrap items-center gap-1">
      {items.length === 0 ? (
        <Badge>All requests</Badge>
      ) : (
        <>
          {shown.map((it) => (
            <Badge key={it.key} tone="secondary">
              <it.icon className="h-3 w-3" strokeWidth={1.75} aria-hidden="true" />
              {it.label}
            </Badge>
          ))}
          {rest > 0 && <Badge title={items.slice(4).map((it) => it.label).join(", ")}>+{rest}</Badge>}
        </>
      )}
      {skill.optional && <Badge title="Clients can skip it with X-KeiRouter-Skip-Skills">Optional</Badge>}
    </span>
  );
}

function SkillRow({
  skill,
  directory,
  onToggle,
  onEdit,
  onDelete,
  onCopyPrompt,
}: {
  skill: Skill;
  directory: Directory;
  onToggle: (enabled: boolean) => void;
  onEdit: () => void;
  onDelete: () => void;
  onCopyPrompt: () => void;
}) {
  const tokens = estimateTokens(renderedPrompt(skill));
  const updated = new Date(skill.updated_at || skill.created_at);
  const updatedLabel = Number.isNaN(updated.getTime())
    ? undefined
    : `${skill.updated_at ? "Updated" : "Created"} ${updated.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" })}`;

  return (
    <li className="flex flex-wrap items-start gap-x-3 gap-y-2 px-4 py-3 transition-colors hover:bg-hover sm:flex-nowrap">
      <IconTile icon={ICONS.prompt} size="sm" tone={skill.enabled ? "section" : "slate"} className="mt-0.5" />
      <div className="min-w-0 flex-1">
        <button type="button" onClick={onEdit} className={`block w-full rounded-md text-left ${focusRing}`} aria-label={`Edit ${skill.name}`}>
          <span className="flex flex-wrap items-baseline gap-x-2">
            <span className={`truncate text-[13px] font-medium ${skill.enabled ? "text-fg" : "text-fg-muted"}`}>{skill.name}</span>
            {skill.description && <span className="truncate text-[12px] text-fg-muted">{skill.description}</span>}
          </span>
          {skill.prompt ? (
            <span className="mt-1 line-clamp-2 font-mono text-[12px] leading-5 text-fg-muted">{renderedPrompt(skill)}</span>
          ) : (
            <span className="mt-1 block text-[12px] italic text-fg-faint">No prompt — adds nothing to requests</span>
          )}
        </button>
        <ScopeBadges skill={skill} directory={directory} />
        <span className="mt-1 block text-[11.5px] tabular-nums text-fg-faint">
          {formatTokens(tokens)}
          {updatedLabel && ` · ${updatedLabel}`}
        </span>
      </div>
      <span className="flex w-full shrink-0 items-center justify-between gap-2 pl-10 sm:w-auto sm:justify-start sm:gap-1 sm:pl-0 sm:pt-0.5">
        <span className="flex items-center gap-2">
          <Toggle checked={skill.enabled} onChange={onToggle} label={`Enable ${skill.name}`} />
          <span className="text-[12px] text-fg-muted sm:hidden" aria-hidden="true">{skill.enabled ? "Enabled" : "Disabled"}</span>
        </span>
        <DropdownMenu>
          <DropdownMenuTrigger
            aria-label={`Actions for ${skill.name}`}
            className={`flex h-9 w-9 items-center justify-center rounded-lg sm:h-8 sm:w-8 text-fg-muted transition-colors hover:bg-subtle hover:text-fg ${focusRing}`}
          >
            <MoreHorizontal className="h-4 w-4" aria-hidden="true" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={onEdit}>
              <Pencil aria-hidden="true" />
              Edit
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onCopyPrompt} disabled={!skill.prompt}>
              <Copy aria-hidden="true" />
              Copy prompt
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem tone="danger" onSelect={onDelete}>
              <Trash2 aria-hidden="true" />
              Delete skill
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </span>
    </li>
  );
}

// TEMPLATES_PREVIEW is how many templates "All" shows before "Show all".
const TEMPLATES_PREVIEW = 6;
const CATEGORY = new Map(SKILL_TEMPLATE_CATEGORIES.map((c) => [c.value, c]));

// SkillTemplates is a gallery of starter skills and personas. Picking one only
// pre-fills the editor; nothing is saved until the operator creates it.
function SkillTemplates({ onUse }: { onUse: (t: SkillTemplate) => void }) {
  const [category, setCategory] = useState<SkillTemplateCategory | "all">("all");
  const [expanded, setExpanded] = useState(false);
  const filtered = category === "all" ? SKILL_TEMPLATES : SKILL_TEMPLATES.filter((t) => t.category === category);
  const collapsed = category === "all" && !expanded && filtered.length > TEMPLATES_PREVIEW;
  const shown = collapsed ? filtered.slice(0, TEMPLATES_PREVIEW) : filtered;

  return (
    <section id="skill-templates" aria-labelledby="skill-templates-heading" className="scroll-mt-4 overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="space-y-3 border-b border-line px-4 py-3">
        <SectionTitle
          id="skill-templates-heading"
          icon={LayoutTemplate}
          title={(
            <>
              Templates
              <span className="ml-2 text-[12px] font-normal tabular-nums text-fg-muted">{SKILL_TEMPLATES.length}</span>
            </>
          )}
          subtitle="Ready-made skills and personas for common use cases — pick one, then tailor it in the editor"
        />
        <SegmentedControl
          label="Template category"
          value={category}
          onChange={setCategory}
          options={[{ value: "all", label: "All" }, ...SKILL_TEMPLATE_CATEGORIES.map((c) => ({ value: c.value, label: c.label }))]}
          className="max-w-full"
        />
      </div>

      <ul aria-label="Skill templates" className="grid grid-cols-1 gap-px bg-line sm:grid-cols-2 xl:grid-cols-3">
        {shown.map((t) => (
          <TemplateCard key={t.id} template={t} onUse={() => onUse(t)} />
        ))}
      </ul>

      {category === "all" && filtered.length > TEMPLATES_PREVIEW && (
        <div className="border-t border-line px-4 py-2">
          <Button variant="ghost" onClick={() => setExpanded((v) => !v)} aria-expanded={!collapsed} className="w-full sm:w-auto">
            {collapsed ? `Show all ${filtered.length} templates` : "Show fewer"}
          </Button>
        </div>
      )}
    </section>
  );
}

function TemplateCard({ template: t, onUse }: { template: SkillTemplate; onUse: () => void }) {
  const cat = CATEGORY.get(t.category);
  return (
    <li className="flex bg-surface">
      <button
        type="button"
        onClick={onUse}
        aria-label={`Use the ${t.name} template`}
        className={`group flex w-full flex-col gap-2 px-4 py-3 text-left transition-colors hover:bg-hover focus-visible:ring-inset ${focusRing}`}
      >
        <span className="flex items-start gap-2.5">
          <IconTile icon={t.icon} size="sm" tone={cat?.tone} />
          <span className="min-w-0 flex-1">
            <span className="flex items-baseline justify-between gap-2">
              <span className="text-[13px] font-medium text-fg">{t.name}</span>
              <span className="shrink-0 text-[11.5px] text-fg-faint">{cat?.label}</span>
            </span>
            <span className="block text-[12px] leading-5 text-fg-muted">{t.description}</span>
          </span>
        </span>
        <span className="mt-auto flex items-center justify-between gap-2 pl-[38px] text-[11.5px]">
          <span className="tabular-nums text-fg-faint">
            {formatTokens(estimateTokens(t.prompt))}
            {t.optional ? " · optional" : ""}
          </span>
          <span className="inline-flex items-center gap-1 font-medium text-link opacity-80 transition-opacity group-hover:opacity-100">
            Use template
            <ArrowRight className="h-3.5 w-3.5 transition-transform group-hover:translate-x-0.5" aria-hidden="true" />
          </span>
        </span>
      </button>
    </li>
  );
}

// useCopy copies text and flips a short-lived "copied" flag for the button.
function useCopy() {
  const toast = useToast();
  const [copied, setCopied] = useState<string | null>(null);
  const copy = async (key: string, text: string, message: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(key);
      toast.success("Copied", message);
      setTimeout(() => setCopied((current) => (current === key ? null : current)), 2000);
    } catch {
      toast.error("Copy failed", "The browser blocked clipboard access.");
    }
  };
  return { copied, copy };
}

// ReferenceSkills leads with the entry skill (the one most agents need) and
// lists the per-endpoint skills as a compact grid.
function ReferenceSkills() {
  const { copied, copy } = useCopy();
  return (
    <section aria-labelledby="reference-skills-heading" className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
      <div className="border-b border-line px-4 py-3">
        <SectionTitle
          id="reference-skills-heading"
          icon={BookOpen}
          title={(
            <>
              Reference skills
              <span className="ml-2 text-[12px] font-normal tabular-nums text-fg-muted">{REFERENCE_SKILLS.length}</span>
            </>
          )}
          subtitle="Paste a link into Claude, Cursor, ChatGPT or any agent to teach it a KeiRouter endpoint"
        />
      </div>

      <div className="flex flex-wrap items-center gap-3 border-b border-line px-4 py-3.5">
        <IconTile icon={ICONS.skills} size="md" />
        <span className="min-w-0 flex-1 basis-60">
          <span className="block text-[13px] font-medium text-fg">Start here: {ENTRY_SKILL.name}</span>
          <span className="block text-[12px] text-fg-muted">{ENTRY_SKILL.description} Covers every endpoint below.</span>
        </span>
        <span className="flex w-full shrink-0 flex-col gap-2 sm:w-auto sm:flex-row sm:flex-wrap sm:items-center">
          <Button
            variant="secondary"
            onClick={() => copy("entry-url", skillUrl(ENTRY_SKILL.id), "Entry skill URL")}
            aria-label="Copy entry skill URL"
          >
            {copied === "entry-url" ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
            Copy URL
          </Button>
          <Button
            onClick={() => copy("entry-prompt", agentInstruction(ENTRY_SKILL.id), "Paste it into your agent's chat.")}
            aria-label="Copy agent instruction for the entry skill"
          >
            {copied === "entry-prompt" ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
            Copy agent prompt
          </Button>
        </span>
        <span className="sr-only" role="status">{copied ? "Copied to clipboard" : ""}</span>
      </div>

      <ul aria-label="Endpoint skills" className="grid grid-cols-1 gap-px bg-line sm:grid-cols-2 xl:grid-cols-3">
        {ENDPOINT_SKILLS.map((sk) => (
          <ReferenceSkillCard key={sk.id} skill={sk} copied={copied === sk.id} onCopy={() => copy(sk.id, skillUrl(sk.id), `Paste it to your agent to teach it ${sk.name}.`)} />
        ))}
      </ul>
    </section>
  );
}

function ReferenceSkillCard({ skill, copied, onCopy }: { skill: ReferenceSkill; copied: boolean; onCopy: () => void }) {
  const url = skillUrl(skill.id);
  return (
    <li className="flex flex-col gap-2 bg-surface px-4 py-3 transition-colors hover:bg-hover">
      <span className="flex items-start gap-2.5">
        <IconTile icon={REFERENCE_ICONS[skill.id] ?? ICONS.skills} size="sm" />
        <span className="min-w-0 flex-1">
          <span className="flex items-baseline justify-between gap-2">
            <span className="text-[13px] font-medium text-fg">{skill.name}</span>
            <span className="shrink-0 text-[11.5px] text-fg-faint">{skill.group}</span>
          </span>
          <span className="block break-all font-mono text-[11.5px] text-fg-muted">{skill.endpoint}</span>
        </span>
      </span>
      <span className="flex-1 text-[12px] leading-5 text-fg-muted">{skill.description}</span>
      <span className="-ml-2 inline-flex items-center gap-2 sm:gap-0.5">
        <button
          type="button"
          onClick={onCopy}
          aria-label={`Copy ${skill.name} skill URL`}
          className={`inline-flex h-9 items-center gap-1.5 rounded-lg px-2 text-[12px] font-medium text-fg-muted transition-colors hover:bg-subtle hover:text-fg sm:h-7 ${focusRing}`}
        >
          {copied ? <Check className="h-3.5 w-3.5 text-ok" aria-hidden="true" /> : <Copy className="h-3.5 w-3.5 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />}
          <span aria-hidden="true">{copied ? "Copied" : "Copy URL"}</span>
        </button>
        <a
          href={url}
          target="_blank"
          rel="noopener noreferrer"
          aria-label={`Open ${skill.name} SKILL.md (opens in a new tab)`}
          className={`inline-flex h-9 items-center gap-1.5 rounded-lg px-2 text-[12px] font-medium text-fg-muted transition-colors hover:bg-subtle hover:text-fg sm:h-7 ${focusRing}`}
        >
          <ExternalLink className="h-3.5 w-3.5 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
          <span aria-hidden="true">View</span>
        </a>
      </span>
    </li>
  );
}
