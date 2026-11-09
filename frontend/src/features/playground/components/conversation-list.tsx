import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui";
import { useConfirm } from "@/components/ui/confirm-dialog";
import { formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { Conversation } from "../types";

export default function ConversationList({
  conversations,
  activeId,
  onOpen,
  onNew,
  onDelete,
}: {
  conversations: Conversation[];
  activeId: string | null;
  onOpen: (id: string) => void;
  onNew: () => void;
  onDelete: (id: string) => void;
}) {
  const confirm = useConfirm();
  const remove = async (c: Conversation) => {
    const ok = await confirm({
      title: "Delete this chat?",
      description: "It's removed from this browser. Its requests stay in Usage and the console.",
      confirmLabel: "Delete chat",
      tone: "danger",
    });
    if (ok) onDelete(c.id);
  };
  const sorted = [...conversations].sort((a, b) => b.updatedAt - a.updatedAt);

  return (
    <nav aria-labelledby="chats-heading" className="flex min-h-0 flex-1 flex-col gap-3">
      <div className="flex items-center justify-between gap-2">
        <h2 id="chats-heading" className="text-heading font-medium">
          Chats
        </h2>
        <Button variant="secondary" size="sm" onClick={onNew}>
          <Plus aria-hidden />
          New chat
        </Button>
      </div>
      {sorted.length === 0 ? (
        <p className="text-meta text-fg-muted">
          Your chats show up here once you send a message. We save them in this browser only.
        </p>
      ) : (
        <ul className="-mx-1 flex min-h-0 flex-col gap-0.5 overflow-y-auto">
          {sorted.map((c) => (
            <li key={c.id} className="group/chat relative">
              <button
                type="button"
                onClick={() => onOpen(c.id)}
                aria-current={c.id === activeId ? "page" : undefined}
                className={cn(
                  "press flex w-full flex-col items-start rounded-md px-2 py-1.5 pr-10 text-left hover:bg-hover focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
                  c.id === activeId && "bg-hover",
                )}
              >
                <span className="w-full truncate text-sm">{c.title}</span>
                <span className="text-xs text-fg-muted">
                  {formatRelative(c.updatedAt)}
                </span>
              </button>
              <Button
                variant="quiet"
                size="icon"
                aria-label={`Delete chat: ${c.title}`}
                onClick={() => void remove(c)}
                className="absolute top-1/2 right-1 size-8 -translate-y-1/2 text-fg-muted opacity-0 group-hover/chat:opacity-100 focus-visible:opacity-100 pointer-coarse:opacity-100"
              >
                <Trash2 aria-hidden />
              </Button>
            </li>
          ))}
        </ul>
      )}
    </nav>
  );
}
