import { useState, useMemo, useEffect } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, Pencil, Trash2, Sparkles, AlertTriangle, Loader2, CheckCircle, Search, Copy, X } from "lucide-react";

import { api, type CustomModel, type CustomModelInput, type Provider } from "../lib/api";
import { Card, CardHeader, Button, Input, Field, Select, Badge, Modal, EmptyState } from "./ui";
import { useToast } from "./Toast";

const MODEL_KINDS = ["llm", "embedding", "image", "stt", "tts", "search", "fetch"] as const;

// CustomModelsSection renders a provider's user-registered models with full
// add / edit / remove controls. It is intentionally separate from the
// catalog/discovered model list so the two never blur together.
export function CustomModelsSection({ provider }: { provider: Provider }) {
  const qc = useQueryClient();
  const toast = useToast();
  const providerId = provider.id;

  const customModels = useQuery({
    queryKey: ["custom-models", providerId],
    queryFn: () => api.listCustomModels(providerId),
    enabled: !!providerId,
  });

  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<CustomModel | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<CustomModel | null>(null);

  const models = customModels.data?.models ?? [];

  // Search + pagination
  const [searchQuery, setSearchQuery] = useState("");
  const [page, setPage] = useState(1);
  const PER_PAGE = 12;

  const filteredModels = useMemo(() => {
    if (!searchQuery.trim()) return models;
    const q = searchQuery.toLowerCase();
    return models.filter(m =>
      m.id.toLowerCase().includes(q) ||
      (m.name && m.name.toLowerCase().includes(q)) ||
      (m.kind && m.kind.toLowerCase().includes(q))
    );
  }, [models, searchQuery]);

  useEffect(() => { setPage(1); }, [searchQuery]);

  const totalPages = Math.ceil(filteredModels.length / PER_PAGE);
  const paginatedModels = filteredModels.slice(
    (page - 1) * PER_PAGE,
    page * PER_PAGE,
  );

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["custom-models", providerId] });
    // The provider model list merges custom models, so refresh it too.
    qc.invalidateQueries({ queryKey: ["provider-models", providerId] });
  };

  const createMut = useMutation({
    mutationFn: (input: CustomModelInput) => api.createCustomModel(providerId, input),
    onSuccess: () => {
      invalidate();
      setModalOpen(false);
      toast.success("Model added", "The custom model is now available for routing.");
    },
    onError: (e: Error) => toast.error("Couldn't add model", e.message),
  });

  const updateMut = useMutation({
    mutationFn: ({ dbId, patch }: { dbId: string; patch: Partial<CustomModelInput> }) =>
      api.updateCustomModel(providerId, dbId, patch),
    onSuccess: () => {
      invalidate();
      setModalOpen(false);
      setEditing(null);
      toast.success("Model updated", "Your changes were saved.");
    },
    onError: (e: Error) => toast.error("Couldn't update model", e.message),
  });

  const deleteMut = useMutation({
    mutationFn: (dbId: string) => api.deleteCustomModel(providerId, dbId),
    onSuccess: () => {
      invalidate();
      setDeleteTarget(null);
      toast.success("Model removed", "The custom model was deleted.");
    },
    onError: (e: Error) => toast.error("Couldn't remove model", e.message),
  });

  const openAdd = () => {
    setEditing(null);
    setModalOpen(true);
  };
  const openEdit = (m: CustomModel) => {
    setEditing(m);
    setModalOpen(true);
  };

  return (
    <Card>
      <CardHeader
        title="Custom model registry"
        description="Add fine-tunes and private upstream models that are not included in the provider catalog."
        action={
          <Button variant="secondary" onClick={openAdd}>
            <Plus className="h-4 w-4" />
            Add custom model
          </Button>
        }
      />

      {models.length === 0 ? (
        <div className="border-t border-[var(--border)] px-6 py-10">
          <EmptyState
            title="No custom models registered"
            hint={`Add a model ID to route it as ${provider.alias || provider.id}/<model>.`}
          />
        </div>
      ) : (
        <>
          {models.length > 0 && (
            <div className="flex flex-col gap-3 border-t border-[var(--border)] bg-[var(--bg-subtle)] px-4 py-4 sm:flex-row sm:items-center sm:justify-between sm:px-6">
              <div className="relative w-full sm:max-w-md">
                <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--text-muted)]" />
                <Input
                  aria-label="Search custom models"
                  placeholder="Search custom models…"
                  value={searchQuery}
                  onChange={(event) => setSearchQuery(event.target.value)}
                  className="pl-10 pr-10"
                />
                {searchQuery && (
                  <button
                    type="button"
                    onClick={() => setSearchQuery("")}
                    aria-label="Clear custom model search"
                    className="absolute right-2 top-1/2 flex h-8 w-8 -translate-y-1/2 items-center justify-center rounded-lg text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-elevated)] hover:text-[var(--text)] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50"
                  >
                    <X className="h-4 w-4" />
                  </button>
                )}
              </div>
              <span className="text-sm text-[var(--text-muted)]">
                {filteredModels.length} of {models.length} {models.length === 1 ? "model" : "models"}
              </span>
            </div>
          )}
          {filteredModels.length === 0 ? (
            <div className="px-6 py-12 text-center text-sm text-[var(--text-muted)] border-t border-[var(--border)]">
              No custom models found matching "{searchQuery}"
            </div>
          ) : (
            <div className="grid grid-cols-1 gap-px border-t border-[var(--border)] bg-[var(--border)] sm:grid-cols-2 xl:grid-cols-3">
              {paginatedModels.map((m) => (
                <CustomModelCell
                  key={m.db_id}
                  model={m}
                  provider={provider}
                  onEdit={() => openEdit(m)}
                  onDelete={() => setDeleteTarget(m)}
                />
              ))}
            </div>
          )}
          {totalPages > 1 && (
            <div className="flex items-center justify-between rounded-b-2xl border-t border-[var(--border)] bg-[var(--bg-subtle)] px-6 py-3">
              <span className="text-xs text-[var(--text-muted)]">
                Showing {(page - 1) * PER_PAGE + 1} to {Math.min(page * PER_PAGE, filteredModels.length)} of {filteredModels.length} models
              </span>
              <div className="flex items-center gap-1">
                <Button
                  variant="ghost"
                  className="h-8 px-2 text-xs"
                  disabled={page === 1}
                  onClick={() => setPage((p) => p - 1)}
                >
                  Previous
                </Button>
                <Button
                  variant="ghost"
                  className="h-8 px-2 text-xs"
                  disabled={page === totalPages}
                  onClick={() => setPage((p) => p + 1)}
                >
                  Next
                </Button>
              </div>
            </div>
          )}
        </>
      )}

      <CustomModelModal
        open={modalOpen}
        editing={editing}
        pending={createMut.isPending || updateMut.isPending}
        onClose={() => {
          setModalOpen(false);
          setEditing(null);
        }}
        onSubmit={(input) => {
          if (editing) {
            updateMut.mutate({ dbId: editing.db_id, patch: input });
          } else {
            createMut.mutate(input);
          }
        }}
      />

      {/* Delete confirmation dialog */}
      <Modal
        open={!!deleteTarget}
        onClose={() => { if (!deleteMut.isPending) setDeleteTarget(null); }}
        title={`Remove custom model "${deleteTarget?.id}"?`}
        subtitle="This model will no longer be routable."
        maxWidth="max-w-md"
      >
        <div className="space-y-4 px-6 py-5">
          <div className="flex items-start gap-3 rounded-xl border border-[color:var(--color-danger)]/30 bg-[color:var(--color-danger)]/10 px-3.5 py-3">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-[color:var(--color-danger)]" strokeWidth={2} />
            <div className="text-sm leading-snug text-[color:var(--color-danger)]">
              The model will be unregistered from this provider.
              <span className="font-semibold"> This action cannot be undone.</span>
            </div>
          </div>
          <div className="flex justify-end gap-2">
            <Button
              variant="ghost"
              onClick={() => setDeleteTarget(null)}
              disabled={deleteMut.isPending}
            >
              Cancel
            </Button>
            <Button
              variant="danger"
              onClick={() => deleteTarget && deleteMut.mutate(deleteTarget.db_id)}
              disabled={deleteMut.isPending}
            >
              {deleteMut.isPending ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <Trash2 className="h-3.5 w-3.5" />
              )}
              Remove model
            </Button>
          </div>
        </div>
      </Modal>
    </Card>
  );
}

// CustomModelCell renders a single custom model in a hairline grid cell,
// matching the style of the catalog model list.
function CustomModelCell({
  model: m,
  provider,
  onEdit,
  onDelete,
}: {
  model: CustomModel;
  provider: Provider;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const fullModel = `${provider.alias || provider.id}/${m.id}`;

  const handleCopy = () => {
    navigator.clipboard.writeText(fullModel);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <article className="group flex min-h-36 flex-col bg-[var(--bg-elevated)] p-4 transition-colors duration-150 hover:bg-[var(--bg-subtle)]">
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border border-line bg-subtle text-fg-muted">
            <Sparkles className="h-4 w-4" />
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            <Badge tone="accent">Custom</Badge>
            <Badge tone="neutral">{m.kind || "Model"}</Badge>
          </div>
        </div>
        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={onEdit}
            className="flex h-9 w-9 items-center justify-center rounded-lg text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50"
            title="Edit model"
            aria-label={`Edit ${m.name || m.id}`}
          >
            <Pencil className="h-4 w-4" />
          </button>
          <button
            type="button"
            onClick={onDelete}
            className="flex h-9 w-9 items-center justify-center rounded-lg text-[var(--text-muted)] transition-colors hover:bg-[color:var(--color-danger)]/10 hover:text-[color:var(--color-danger)] focus:outline-none focus-visible:ring-2 focus-visible:ring-[color:var(--color-danger)]/40"
            title="Remove model"
            aria-label={`Remove ${m.name || m.id}`}
          >
            <Trash2 className="h-4 w-4" />
          </button>
        </div>
      </div>

      <div className="mt-4 min-w-0 flex-1">
        <h3 className="truncate text-sm font-semibold" title={m.name || m.id}>{m.name || m.id}</h3>
        <code className="mt-1.5 block truncate font-mono text-xs text-[var(--text-muted)]" title={fullModel}>
          {fullModel}
        </code>
      </div>

      <div className="mt-3 flex items-center gap-2 border-t border-[var(--border)] pt-2.5">
        {m.context_window > 0 && (
          <span className="text-xs text-[var(--text-muted)]">{m.context_window.toLocaleString()} context</span>
        )}
        {(m.input_per_m > 0 || m.output_per_m > 0) && (
          <span className="text-xs text-[var(--text-muted)]">${m.input_per_m}/${m.output_per_m} per M</span>
        )}
        <button
          type="button"
          onClick={handleCopy}
          className="ml-auto flex h-9 w-9 items-center justify-center rounded-lg text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-subtle)] hover:text-[var(--text)] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/50"
          title="Copy model path"
          aria-label={`Copy model path ${fullModel}`}
        >
          {copied ? <CheckCircle className="h-4 w-4 text-ok" /> : <Copy className="h-4 w-4" />}
        </button>
      </div>
    </article>
  );
}

function CustomModelModal({
  open,
  editing,
  pending,
  onClose,
  onSubmit,
}: {
  open: boolean;
  editing: CustomModel | null;
  pending: boolean;
  onClose: () => void;
  onSubmit: (input: CustomModelInput) => void;
}) {
  const [id, setId] = useState("");
  const [name, setName] = useState("");
  const [kind, setKind] = useState("llm");
  const [contextWindow, setContextWindow] = useState("");
  const [inputPerM, setInputPerM] = useState("");
  const [outputPerM, setOutputPerM] = useState("");

  // Sync form state whenever the modal opens or the editing target changes.
  const [syncedFor, setSyncedFor] = useState<string | null>(null);
  const syncKey = open ? editing?.db_id ?? "new" : "closed";
  if (open && syncedFor !== syncKey) {
    setSyncedFor(syncKey);
    setId(editing?.id ?? "");
    setName(editing?.name ?? "");
    setKind(editing?.kind || "llm");
    setContextWindow(editing?.context_window ? String(editing.context_window) : "");
    setInputPerM(editing?.input_per_m ? String(editing.input_per_m) : "");
    setOutputPerM(editing?.output_per_m ? String(editing.output_per_m) : "");
  } else if (!open && syncedFor !== "closed") {
    setSyncedFor("closed");
  }

  const canSubmit = id.trim().length > 0 && !pending;

  const submit = () => {
    if (!canSubmit) return;
    onSubmit({
      id: id.trim(),
      name: name.trim() || undefined,
      kind,
      context_window: contextWindow ? Number(contextWindow) : undefined,
      input_per_m: inputPerM ? Number(inputPerM) : undefined,
      output_per_m: outputPerM ? Number(outputPerM) : undefined,
    });
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={editing ? "Edit custom model" : "Add custom model"}
      subtitle="Register a model id the upstream accepts. Pricing and context are optional metadata."
    >
      <div className="space-y-4 px-6 py-5">
        <Field label="Model ID (required)">
          <Input
            value={id}
            onChange={(e) => setId(e.target.value)}
            placeholder="e.g. gpt-4o-mini or my-finetune-v1"
            autoFocus
          />
        </Field>
        <Field label="Display name">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Friendly label (optional)" />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Kind">
            <Select value={kind} onChange={(e) => setKind(e.target.value)}>
              {MODEL_KINDS.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </Select>
          </Field>
          <Field label="Context window">
            <Input
              type="number"
              min={0}
              value={contextWindow}
              onChange={(e) => setContextWindow(e.target.value)}
              placeholder="e.g. 128000"
            />
          </Field>
        </div>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Input $/M tokens">
            <Input
              type="number"
              min={0}
              step="0.01"
              value={inputPerM}
              onChange={(e) => setInputPerM(e.target.value)}
              placeholder="0"
            />
          </Field>
          <Field label="Output $/M tokens">
            <Input
              type="number"
              min={0}
              step="0.01"
              value={outputPerM}
              onChange={(e) => setOutputPerM(e.target.value)}
              placeholder="0"
            />
          </Field>
        </div>
      </div>
      <div className="flex items-center justify-end gap-2 border-t border-[var(--border)] px-6 py-4">
        <Button variant="ghost" onClick={onClose}>
          Cancel
        </Button>
        <Button onClick={submit} disabled={!canSubmit}>
          {editing ? "Save changes" : "Add model"}
        </Button>
      </div>
    </Modal>
  );
}
