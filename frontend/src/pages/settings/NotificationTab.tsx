import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bell } from "lucide-react";
import { api, type LandingNotification } from "../../lib/api";
import { sanitizeHtml } from "../../lib/sanitizeHtml";
import { useToast } from "../../components/Toast";
import { useConfirm } from "../../components/ui/confirm-dialog";
import { Badge, Button, ErrorBanner, Modal, Skeleton } from "../../components/ui";
import { FormField, SettingsCard, inputClass } from "./shared";

// ── Notification tab ────────────────────────────────────────────────
const MAX_NOTIFICATIONS = 5;

export function NotificationTab() {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const list = useQuery({ queryKey: ["landing-notifications"], queryFn: () => api.notifications() });
  const save = useMutation({
    mutationFn: (items: LandingNotification[]) => api.updateNotifications(items),
    onSuccess: (data) => {
      qc.setQueryData(["landing-notifications"], data);
      qc.invalidateQueries({ queryKey: ["public-notifications"] });
      toast.success("Notifications saved");
    },
    onError: (e) => toast.error("Save failed", (e as Error).message),
  });

  const [editing, setEditing] = useState<{ index: number; item: LandingNotification } | null>(null);
  const items = list.data?.notifications ?? [];

  const persist = (next: LandingNotification[]) => save.mutate(next);

  const openAdd = () => {
    setEditing({ index: -1, item: { id: "", title: "", body: "", tag: "", href: "" } });
  };
  const openEdit = (index: number) => setEditing({ index, item: { ...items[index] } });

  const commit = () => {
    if (!editing) return;
    const it = editing.item;
    if (!it.title.trim()) return;
    const next = editing.index < 0 ? [...items, it] : items.map((x, i) => (i === editing.index ? it : x));
    persist(next);
    setEditing(null);
  };

  const remove = async (index: number) => {
    if (!(await confirm({ title: "Delete this notification?", tone: "danger" }))) return;
    persist(items.filter((_, i) => i !== index));
  };

  if (list.isLoading) return <Skeleton className="h-44 w-full rounded-2xl" />;
  if (list.isError) {
    return (
      <ErrorBanner message={`Failed to load notifications: ${(list.error as Error)?.message ?? "unknown error"}`} />
    );
  }

  const setItem = (patch: Partial<LandingNotification>) =>
    editing && setEditing({ ...editing, item: { ...editing.item, ...patch } });

  return (
    <SettingsCard
      icon={Bell}
      title="Landing page notifications"
      description={`Announcements shown in the landing page bell. Max ${MAX_NOTIFICATIONS}. Title and body support limited HTML (bold, italic, links).`}
      action={
        <Button onClick={openAdd} disabled={items.length >= MAX_NOTIFICATIONS || save.isPending}>
          Add notification
        </Button>
      }
    >
      {items.length === 0 && <p className="px-4 py-8 text-center text-[13px] text-fg-muted">No notifications yet.</p>}
      {items.map((n, i) => (
        <div key={n.id || i} className="flex items-start justify-between gap-4 px-4 py-3.5">
          <div className="min-w-0">
            <p className="flex items-center gap-2 text-[13px] font-medium text-fg">
              {n.title}
              {n.tag && <Badge tone="neutral">{n.tag}</Badge>}
            </p>
            <p
              className="mt-0.5 truncate text-[12px] text-fg-muted [&_a]:underline"
              dangerouslySetInnerHTML={{ __html: sanitizeHtml(n.body) }}
            />
          </div>
          <div className="flex shrink-0 gap-2">
            <Button variant="ghost" onClick={() => openEdit(i)}>
              Edit
            </Button>
            <Button variant="ghost" onClick={() => void remove(i)}>
              Delete
            </Button>
          </div>
        </div>
      ))}

      <Modal
        open={editing !== null}
        onClose={() => setEditing(null)}
        title={editing && editing.index < 0 ? "Add notification" : "Edit notification"}
      >
        {editing && (
          <>
            <div className="space-y-4 px-6 py-5">
              <FormField label="Title (HTML allowed)" htmlFor="notif-title">
                <input id="notif-title" className={inputClass} value={editing.item.title} onChange={(e) => setItem({ title: e.target.value })} />
              </FormField>
              <FormField label="Tag" htmlFor="notif-tag" optional>
                <input id="notif-tag" className={inputClass} value={editing.item.tag ?? ""} onChange={(e) => setItem({ tag: e.target.value })} />
              </FormField>
              <FormField label="Body (HTML allowed)" htmlFor="notif-body">
                <input id="notif-body" className={inputClass} value={editing.item.body} onChange={(e) => setItem({ body: e.target.value })} />
              </FormField>
              <FormField label="Link" htmlFor="notif-href" optional>
                <input
                  id="notif-href"
                  className={inputClass}
                  value={editing.item.href ?? ""}
                  onChange={(e) => setItem({ href: e.target.value })}
                  placeholder="https://…"
                />
              </FormField>
              <div>
                <p className="mb-1 text-[12px] font-medium text-fg-muted">Preview</p>
                <div className="rounded-lg border border-line bg-subtle px-4 py-3 text-[13px]">
                  <p className="font-medium text-fg [&_a]:underline" dangerouslySetInnerHTML={{ __html: sanitizeHtml(editing.item.title) }} />
                  <p className="mt-0.5 text-[12px] text-fg-muted [&_a]:underline" dangerouslySetInnerHTML={{ __html: sanitizeHtml(editing.item.body) }} />
                </div>
              </div>
            </div>
            <div className="flex justify-end gap-2 border-t border-line bg-subtle px-6 py-4">
              <Button variant="ghost" onClick={() => setEditing(null)}>
                Cancel
              </Button>
              <Button onClick={commit} disabled={!editing.item.title.trim() || save.isPending}>
                {save.isPending ? "Saving…" : "Save"}
              </Button>
            </div>
          </>
        )}
      </Modal>
    </SettingsCard>
  );
}
