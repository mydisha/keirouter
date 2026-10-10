import { useState, type ReactNode } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2, Copy, Check, ChevronRight, MoreHorizontal, ExternalLink, AlertCircle } from "lucide-react";
import { api, type Skill } from "../lib/api";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { Button, Input, Modal, Skeleton, Toggle, ErrorBanner } from "../components/ui";
import { useConfirm } from "../components/ui/confirm-dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../components/ui/dropdown-menu";

// Built-in reference skills — documentation pages that teach AI agents how to
// call KeiRouter endpoints. These are static docs with copyable URLs, not
// runtime request modifiers. The entry skill indexes the rest, so it stays
// visible; the endpoint skills are grouped behind a disclosure.
const REFERENCE_SKILLS = [
  { id: "keirouter", name: "KeiRouter (entry)", endpoint: null as string | null, description: "Setup guide and index of all capabilities.", group: "entry" },
  { id: "keirouter-chat", name: "Chat", endpoint: "/v1/chat/completions", description: "Chat and code generation via OpenAI or Anthropic format with streaming.", group: "Text" },
  { id: "keirouter-embeddings", name: "Embeddings", endpoint: "/v1/embeddings", description: "Vectors for RAG and semantic search.", group: "Text" },
  { id: "keirouter-image", name: "Image generation", endpoint: "/v1/images/generations", description: "Text-to-image via DALL-E, Imagen, FLUX, and more.", group: "Media" },
  { id: "keirouter-tts", name: "Text-to-speech", endpoint: "/v1/audio/speech", description: "OpenAI, ElevenLabs, Edge, Google, Deepgram voices.", group: "Media" },
  { id: "keirouter-stt", name: "Speech-to-text", endpoint: "/v1/audio/transcriptions", description: "Transcribe via Whisper, Groq, Gemini, Deepgram, AssemblyAI.", group: "Media" },
  { id: "keirouter-web-search", name: "Web search", endpoint: "/v1/search", description: "Tavily, Exa, Brave, Serper, SearXNG, Google PSE, You.com.", group: "Web" },
  { id: "keirouter-web-fetch", name: "Web fetch", endpoint: "/v1/web/fetch", description: "URL to markdown/text/HTML via Firecrawl, Jina, Tavily, Exa.", group: "Web" },
];

type ReferenceSkill = (typeof REFERENCE_SKILLS)[number];

const ENTRY_SKILL = REFERENCE_SKILLS.find((skill) => skill.group === "entry")!;
const ENDPOINT_SKILLS = REFERENCE_SKILLS.filter((skill) => skill.group !== "entry");
const ENDPOINT_GROUPS = [...new Set(ENDPOINT_SKILLS.map((skill) => skill.group))].map((group) => ({
  group,
  skills: ENDPOINT_SKILLS.filter((skill) => skill.group === group),
}));

const focusRing = "focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500";

const skillUrl = (id: string) => `https://raw.githubusercontent.com/mydisha/keirouter/main/skills/${id}/SKILL.md`;

export function SkillsPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const askConfirm = useConfirm();
  const skills = useQuery({ queryKey: ["skills"], queryFn: () => api.listSkills() });

  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [prompt, setPrompt] = useState("");
  const [error, setError] = useState("");

  const create = useMutation({
    mutationFn: () => api.createSkill({ name, description, prompt }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["skills"] });
      setName("");
      setDescription("");
      setPrompt("");
      setError("");
      setCreateOpen(false);
      toast.success("Skill created", `"${name}" is ready to apply to requests.`);
    },
    onError: (e) => {
      setError((e as Error).message);
      toast.error("Skill creation failed", (e as Error).message);
    },
  });

  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      api.updateSkill(id, { enabled }),
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
      description: "Its prompt stops being injected into matching requests immediately. This cannot be undone.",
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

  const custom = skills.data?.skills ?? [];
  const isEmpty = !skills.isLoading && !skills.isError && custom.length === 0;
  const openCreate = () => setCreateOpen(true);

  return (
    <>
      <PageHeader
        title="Skills"
        description="System prompts the gateway adds to matching requests."
        action={
          isEmpty ? undefined : (
            <Button onClick={openCreate}>
              <Plus aria-hidden="true" />
              New skill
            </Button>
          )
        }
      />

      <div className="space-y-8">
        {/* Custom skills */}
        <section aria-labelledby="custom-skills-heading">
          <h2 id="custom-skills-heading" className="mb-2.5 flex items-baseline gap-2 text-[14px] font-semibold text-fg">
            Custom skills
            {custom.length > 0 && <span className="text-[12px] font-normal tabular-nums text-fg-muted">{custom.length}</span>}
          </h2>

          {skills.isLoading ? (
            <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]" aria-busy="true" aria-label="Loading skills">
              <div className="h-9 border-b border-line bg-subtle" />
              <div className="divide-y divide-line">
                {Array.from({ length: 3 }).map((_, i) => (
                  <div key={i} className="flex items-center gap-4 px-4 py-3">
                    <div className="flex-1 space-y-1.5">
                      <Skeleton className="h-3.5 w-40" />
                      <Skeleton className="h-3 w-64" />
                    </div>
                    <Skeleton className="h-5 w-9 rounded-full" />
                  </div>
                ))}
              </div>
            </div>
          ) : skills.isError ? (
            <div role="alert" className="flex items-start gap-2.5 rounded-2xl border border-bad/30 bg-bad/5 px-4 py-3">
              <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-bad" strokeWidth={1.75} aria-hidden="true" />
              <p className="text-[13px] text-bad">
                Couldn't load skills. {skills.error instanceof Error ? `${skills.error.message}. ` : ""}Refresh the page to try again.
              </p>
            </div>
          ) : custom.length === 0 ? (
            <div className="rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-10 text-center">
              <h3 className="text-[14px] font-medium text-fg">No custom skills yet</h3>
              <p className="mx-auto mt-1 max-w-md text-[13px] text-fg-muted">
                A skill gives every tool the same instructions without changing its config.
              </p>
              <Button className="mt-4" onClick={openCreate}>
                <Plus aria-hidden="true" />
                Create skill
              </Button>
            </div>
          ) : (
            <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
              <div className="overflow-x-auto">
                <table className="w-full min-w-[640px] text-[13px]">
                  <thead>
                    <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-muted">
                      <th scope="col" className="px-4 py-2 font-medium">Skill</th>
                      <th scope="col" className="px-4 py-2 font-medium">Prompt</th>
                      <th scope="col" className="px-4 py-2 text-right font-medium">Created</th>
                      <th scope="col" className="px-4 py-2 font-medium">Enabled</th>
                      <th scope="col" className="w-10 px-2 py-2"><span className="sr-only">Actions</span></th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-line">
                    {custom.map((sk) => (
                      <SkillRow
                        key={sk.id}
                        skill={sk}
                        onToggle={(enabled) => toggle.mutate({ id: sk.id, enabled })}
                        onDelete={() => deleteSkill(sk)}
                        onCopyPrompt={() => copyText(sk.prompt, `Prompt for ${sk.name}`)}
                      />
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </section>

        {/* Reference skills */}
        <ReferenceSkills />
      </div>

      <Modal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title="New skill"
      >
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (name.trim() && !create.isPending) create.mutate();
          }}
          aria-busy={create.isPending || undefined}
        >
          <div className="space-y-4 px-5 py-4">
            <FormField id="skill-name" label="Name" required>
              <Input id="skill-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Concise reviewer" autoFocus aria-required="true" />
            </FormField>
            <FormField id="skill-description" label="Description" optional>
              <Input
                id="skill-description"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="Short summary of what it does"
              />
            </FormField>
            <FormField id="skill-prompt" label="Prompt" hint="Added as a system prompt to every matching request.">
              <textarea
                id="skill-prompt"
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                rows={6}
                spellCheck={false}
                aria-describedby="skill-prompt-hint"
                placeholder="You are a meticulous code reviewer. Prefer small, safe diffs…"
                className="w-full rounded-lg border border-input bg-surface px-3 py-2 font-mono text-[12px] leading-5 text-fg placeholder:text-fg-faint transition-colors hover:border-fg-faint focus:border-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
              />
            </FormField>
            {error && <ErrorBanner message={error} />}
          </div>
          <div className="flex justify-end gap-2 border-t border-line bg-subtle px-5 py-3">
            <Button type="button" variant="ghost" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={!name.trim() || create.isPending}>
              {create.isPending ? "Creating…" : "Create skill"}
            </Button>
          </div>
        </form>
      </Modal>
    </>
  );
}

function FormField({ id, label, hint, optional, required, children }: { id: string; label: string; hint?: string; optional?: boolean; required?: boolean; children: ReactNode }) {
  return (
    <div className="space-y-1.5">
      <label htmlFor={id} className="flex items-baseline justify-between text-[12.5px] font-medium text-fg">
        {label}
        {optional && <span className="text-[12px] font-normal text-fg-muted">Optional</span>}
        {required && <span className="text-[12px] font-normal text-fg-muted">Required</span>}
      </label>
      {children}
      {hint && <p id={`${id}-hint`} className="text-[12px] leading-5 text-fg-muted">{hint}</p>}
    </div>
  );
}

// ReferenceSkills shows the entry skill up front and groups the per-endpoint
// skills behind one disclosure, so the reference list doesn't crowd the page.
function ReferenceSkills() {
  const [expanded, setExpanded] = useState(false);
  return (
    <section aria-labelledby="reference-skills-heading">
      <div className="mb-2.5 flex flex-wrap items-baseline justify-between gap-x-4 gap-y-0.5">
        <h2 id="reference-skills-heading" className="flex items-baseline gap-2 text-[14px] font-semibold text-fg">
          Reference skills
          <span className="text-[12px] font-normal tabular-nums text-fg-muted">{REFERENCE_SKILLS.length}</span>
        </h2>
        <p className="text-[12.5px] text-fg-muted">Give an agent a skill URL to teach it an endpoint</p>
      </div>
      <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]">
        <ul aria-label="Entry skill">
          <ReferenceSkillRow skill={ENTRY_SKILL} />
        </ul>
        <div className="border-t border-line">
          <button
            type="button"
            aria-expanded={expanded}
            aria-controls="reference-endpoint-skills"
            onClick={() => setExpanded((current) => !current)}
            className="flex w-full items-center gap-2 px-4 py-2.5 text-left text-[13px] transition-colors hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500"
          >
            <ChevronRight className={`h-4 w-4 shrink-0 text-fg-faint transition-transform ${expanded ? "rotate-90" : ""}`} strokeWidth={1.75} aria-hidden="true" />
            <span className="font-medium text-fg">Endpoint skills</span>
            <span className="text-[12px] tabular-nums text-fg-muted">{ENDPOINT_SKILLS.length}</span>
          </button>
          {expanded && (
            <div id="reference-endpoint-skills" className="border-t border-line">
              {ENDPOINT_GROUPS.map((g) => (
                <div key={g.group} className="border-b border-line last:border-b-0">
                  <h3 id={`reference-group-${g.group}`} className="bg-subtle px-4 py-1.5 text-[12px] font-medium text-fg-muted">{g.group}</h3>
                  <ul aria-labelledby={`reference-group-${g.group}`} className="divide-y divide-line">
                    {g.skills.map((sk) => (
                      <ReferenceSkillRow key={sk.id} skill={sk} />
                    ))}
                  </ul>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </section>
  );
}

function ReferenceSkillRow({ skill }: { skill: ReferenceSkill }) {
  const toast = useToast();
  const [copied, setCopied] = useState(false);
  const url = skillUrl(skill.id);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
      toast.success("Skill URL copied", `Paste it to your agent to teach it ${skill.name}.`);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      toast.error("Copy failed", "The browser blocked clipboard access.");
    }
  };

  return (
    <li className="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-2.5 transition-colors hover:bg-hover">
      <span className="min-w-0 flex-1 basis-56">
        <span className="block text-[13px] font-medium text-fg">{skill.name}</span>
        <span className="block text-[12px] text-fg-muted">{skill.description}</span>
      </span>
      <span className="shrink-0 text-[12px]">
        {skill.endpoint ? (
          <span className="font-mono text-fg-muted">{skill.endpoint}</span>
        ) : (
          <span className="text-fg-muted">All endpoints</span>
        )}
      </span>
      <span className="inline-flex shrink-0 items-center gap-0.5">
        <button
          type="button"
          onClick={copy}
          title="Copy skill URL"
          aria-label={`Copy ${skill.name} skill URL`}
          className={`inline-flex h-8 items-center gap-1.5 rounded-lg px-2 text-[12px] font-medium text-fg-muted transition-colors hover:bg-subtle hover:text-fg ${focusRing}`}
        >
          {copied ? <Check className="h-3.5 w-3.5 text-ok" aria-hidden="true" /> : <Copy className="h-3.5 w-3.5 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />}
          <span aria-hidden="true">{copied ? "Copied" : "Copy URL"}</span>
        </button>
        <span className="sr-only" role="status">{copied ? `${skill.name} skill URL copied` : ""}</span>
        <a
          href={url}
          target="_blank"
          rel="noopener noreferrer"
          title="Open SKILL.md"
          aria-label={`Open ${skill.name} SKILL.md (opens in a new tab)`}
          className={`flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-subtle hover:text-fg ${focusRing}`}
        >
          <ExternalLink className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
        </a>
      </span>
    </li>
  );
}

function SkillRow({
  skill,
  onToggle,
  onDelete,
  onCopyPrompt,
}: {
  skill: Skill;
  onToggle: (enabled: boolean) => void;
  onDelete: () => void;
  onCopyPrompt: () => void;
}) {
  const created = skill.created_at ? new Date(skill.created_at) : null;
  const createdLabel =
    created && !Number.isNaN(created.getTime())
      ? created.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" })
      : "—";

  return (
    <tr className="transition-colors hover:bg-hover">
      <td className="max-w-[280px] px-4 py-2.5 align-top">
        <span className="block truncate font-medium text-fg">{skill.name}</span>
        {skill.description ? (
          <span className="block truncate text-[12px] text-fg-muted">{skill.description}</span>
        ) : (
          <span className="block truncate font-mono text-[12px] text-fg-muted">{skill.id}</span>
        )}
      </td>
      <td className="max-w-[360px] px-4 py-2.5 align-top">
        {skill.prompt ? (
          <span className="line-clamp-2 font-mono text-[12px] leading-5 text-fg-muted" title={skill.prompt}>
            {skill.prompt}
          </span>
        ) : (
          <span className="text-[12px] text-fg-muted">No prompt</span>
        )}
      </td>
      <td className="whitespace-nowrap px-4 py-2.5 text-right align-top text-[12px] text-fg-muted">{createdLabel}</td>
      <td className="px-4 py-2.5 align-top">
        <Toggle checked={skill.enabled} onChange={onToggle} label={`Enable ${skill.name}`} />
      </td>
      <td className="px-2 py-2 text-right align-top">
        <DropdownMenu>
          <DropdownMenuTrigger
            aria-label={`Actions for ${skill.name}`}
            className={`flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-subtle hover:text-fg ${focusRing}`}
          >
            <MoreHorizontal className="h-4 w-4" aria-hidden="true" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
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
      </td>
    </tr>
  );
}
