import { useState, useMemo, useEffect } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, Pencil, Trash2, Check, Search, Copy, X, MoreHorizontal } from "lucide-react";

import { api, type CustomModel, type CustomModelInput, type Provider } from "../lib/api";
import { Card, CardHeader, Button, Field, Input, Select, Modal, TablePagination } from "./ui";
import { useConfirm } from "./ui/confirm-dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { useToast } from "./Toast";

const MODEL_KINDS = ["llm", "embedding", "image", "stt", "tts", "search", "fetch"] as const;

// CustomModelsSection renders a provider's user-registered models with full
// add / edit / remove controls. It is intentionally separate from the
// catalog/discovered model list so the two never blur together.
export function CustomModelsSection({ provider }: { provider: Provider }) {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const providerId = provider.id;

  const customModels = useQuery({
    queryKey: ["custom-models", providerId],
    queryFn: () => api.listCustomModels(providerId),
    enabled: !!providerId,
  });

  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<CustomModel | null>(null);

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
      toast.success("Model added", "It's available for routing now.");
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
      toast.success("Model updated");
    },
    onError: (e: Error) => toast.error("Couldn't update model", e.message),
  });

  const deleteMut = useMutation({
    mutationFn: (dbId: string) => api.deleteCustomModel(providerId, dbId),
    onSuccess: () => {
      invalidate();
      toast.success("Model removed");
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
  const removeModel = async (m: CustomModel) => {
    const ok = await confirm({
      title: `Remove ${m.id}?`,
      description: `Requests for ${provider.alias || provider.id}/${m.id} fail immediately. This can't be undone.`,
      confirmLabel: "Remove model",
      tone: "danger",
    });
    if (ok) deleteMut.mutate(m.db_id);
  };

  return (
    <Card>
      <CardHeader
        title="Custom models"
        description="Models not in the provider catalog"
        action={
          <Button variant="secondary" onClick={openAdd}>
            <Plus strokeWidth={1.75} />
            Add custom model
          </Button>
        }
      />

      {models.length === 0 ? (
        <div className="px-6 py-10 text-center">
          <p className="text-[13px] font-medium text-fg">No custom models</p>
          <p className="mx-auto mt-1 max-w-md text-[13px] text-fg-muted">
            Route any upstream model ID as{" "}
            <span className="font-mono text-[12.5px] text-fg">{provider.alias || provider.id}/&lt;model&gt;</span>.
          </p>
          <Button className="mt-4" onClick={openAdd}>
            <Plus strokeWidth={1.75} />
            Add custom model
          </Button>
        </div>
      ) : (
        <>
          <div className="flex flex-col gap-2 border-b border-line px-4 py-2.5 sm:flex-row sm:items-center sm:justify-between">
            <div className="relative w-full sm:max-w-xs">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
              <Input
                aria-label="Search custom models"
                placeholder="Search custom models…"
                value={searchQuery}
                onChange={(event) => setSearchQuery(event.target.value)}
                className="h-8 min-h-8 pl-8 pr-8"
              />
              {searchQuery && (
                <button
                  type="button"
                  onClick={() => setSearchQuery("")}
                  aria-label="Clear search"
                  className="absolute right-1 top-1/2 flex h-6 w-6 -translate-y-1/2 items-center justify-center rounded-md text-fg-faint transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
                >
                  <X className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
                </button>
              )}
            </div>
            <span className="text-[12px] tabular-nums text-fg-muted" role="status">
              {filteredModels.length} of {models.length} {models.length === 1 ? "model" : "models"}
            </span>
          </div>
          {filteredModels.length === 0 ? (
            <div className="px-6 py-10 text-center text-[13px] text-fg-muted">
              No custom models match “{searchQuery}”.
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[640px] text-[13px]">
                <thead>
                  <tr className="border-b border-line bg-subtle text-left text-[12px] text-fg-faint">
                    <th scope="col" className="px-4 py-2 font-medium">Model</th>
                    <th scope="col" className="px-4 py-2 font-medium">Kind</th>
                    <th scope="col" className="px-4 py-2 text-right font-medium">Context</th>
                    <th scope="col" className="px-4 py-2 text-right font-medium">Input / output per 1M</th>
                    <th scope="col" className="w-24 px-4 py-2"><span className="sr-only">Actions</span></th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {paginatedModels.map((m) => (
                    <CustomModelRow
                      key={m.db_id}
                      model={m}
                      provider={provider}
                      onEdit={() => openEdit(m)}
                      onDelete={() => removeModel(m)}
                      deleting={deleteMut.isPending && deleteMut.variables === m.db_id}
                    />
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <TablePagination page={page} pages={totalPages} total={filteredModels.length} onPage={setPage} label="Custom models pages" />
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
    </Card>
  );
}

// CustomModelRow renders one custom model as a dense table row.
function CustomModelRow({
  model: m,
  provider,
  onEdit,
  onDelete,
  deleting,
}: {
  model: CustomModel;
  provider: Provider;
  onEdit: () => void;
  onDelete: () => void;
  deleting: boolean;
}) {
  const toast = useToast();
  const [copied, setCopied] = useState(false);
  const fullModel = `${provider.alias || provider.id}/${m.id}`;

  const handleCopy = () => {
    navigator.clipboard
      .writeText(fullModel)
      .then(() => {
        setCopied(true);
        toast.success("Copied", fullModel);
        setTimeout(() => setCopied(false), 1500);
      })
      .catch(() => toast.error("Couldn't copy", "Your browser blocked clipboard access."));
  };

  const hasPrice = m.input_per_m > 0 || m.output_per_m > 0;

  return (
    <tr className={`transition-colors hover:bg-hover ${deleting ? "opacity-50" : ""}`} aria-busy={deleting || undefined}>
      <td className="max-w-0 px-4 py-2.5">
        <p className="truncate font-medium text-fg" title={m.name || m.id}>{m.name || m.id}</p>
        <p className="truncate font-mono text-[12px] text-fg-muted" title={fullModel}>{fullModel}</p>
      </td>
      <td className="px-4 py-2.5">
        <span className="inline-flex h-5 items-center rounded-md border border-line bg-subtle px-1.5 font-mono text-[11.5px] text-fg-muted">
          {m.kind || "model"}
        </span>
      </td>
      <td className="px-4 py-2.5 text-right tabular-nums text-fg-muted">
        {m.context_window > 0 ? m.context_window.toLocaleString() : <span className="text-fg-faint">—</span>}
      </td>
      <td className="px-4 py-2.5 text-right tabular-nums text-fg-muted">
        {hasPrice ? `$${m.input_per_m} / $${m.output_per_m}` : <span className="text-fg-faint">—</span>}
      </td>
      <td className="px-3 py-2.5">
        <div className="flex items-center justify-end gap-0.5">
          <button
            type="button"
            onClick={handleCopy}
            className="flex h-8 w-8 items-center justify-center rounded-lg text-fg-faint transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
            title="Copy model path"
            aria-label={`Copy model path ${fullModel}`}
          >
            {copied ? <Check className="h-4 w-4 text-ok" strokeWidth={1.75} aria-hidden="true" /> : <Copy className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />}
          </button>
          <DropdownMenu>
            <DropdownMenuTrigger
              aria-label={`Actions for ${m.name || m.id}`}
              className="flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
            >
              <MoreHorizontal className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onSelect={onEdit}>
                <Pencil />
                Edit model
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem tone="danger" onSelect={onDelete} disabled={deleting}>
                <Trash2 />
                Remove model
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </td>
    </tr>
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
      subtitle="Only the model ID is required"
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        <div className="space-y-4 px-5 py-4">
          <Field label="Model ID" required hint="Sent to the upstream exactly as typed.">
            <Input
              value={id}
              onChange={(e) => setId(e.target.value)}
              placeholder="e.g. my-finetune-v1"
              className="font-mono"
              autoFocus
            />
          </Field>
          <Field label="Display name" optional>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Friendly label" />
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
            <Field label="Context window" optional>
              <Input
                type="number"
                min={0}
                value={contextWindow}
                onChange={(e) => setContextWindow(e.target.value)}
                placeholder="e.g. 128000"
                className="tabular-nums"
              />
            </Field>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Input $ per 1M tokens" optional>
              <Input
                type="number"
                min={0}
                step="0.01"
                value={inputPerM}
                onChange={(e) => setInputPerM(e.target.value)}
                placeholder="0"
                className="tabular-nums"
              />
            </Field>
            <Field label="Output $ per 1M tokens" optional>
              <Input
                type="number"
                min={0}
                step="0.01"
                value={outputPerM}
                onChange={(e) => setOutputPerM(e.target.value)}
                placeholder="0"
                className="tabular-nums"
              />
            </Field>
          </div>
        </div>
        <div className="flex items-center justify-end gap-2 border-t border-line bg-subtle px-5 py-3">
          <Button type="button" variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" disabled={!canSubmit}>
            {editing ? "Save changes" : "Add model"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
