import { isOutputFormat } from "./output-format";
import type { Conversation, DocumentRef, Effort, ImageRef } from "./types";

/** Chats stay in this browser only; the gateway never stores prompts. */
const KEY_PREFIX = "kr.playground.v1.";
export const MAX_CONVERSATIONS = 30;

export type PlaygroundState = {
  conversations: Conversation[];
  lastModel: string | null;
  lastEffort: Effort;
};

const EMPTY: PlaygroundState = {
  conversations: [],
  lastModel: null,
  lastEffort: "medium",
};

const EFFORTS: Effort[] = ["off", "low", "medium", "high"];

function isImageRef(value: unknown): value is ImageRef {
  const i = value as ImageRef;

  return (
    typeof i === "object" &&
    i !== null &&
    typeof i.id === "string" &&
    typeof i.name === "string" &&
    typeof i.mime === "string" &&
    typeof i.width === "number" &&
    typeof i.height === "number" &&
    typeof i.bytes === "number"
  );
}

function isDocumentRef(value: unknown): value is DocumentRef {
  const d = value as DocumentRef;

  return (
    typeof d === "object" &&
    d !== null &&
    typeof d.id === "string" &&
    typeof d.name === "string" &&
    typeof d.bytes === "number" &&
    typeof d.pages === "number" &&
    (d.textId === null || typeof d.textId === "string") &&
    typeof d.chars === "number" &&
    Array.isArray(d.pageImages) &&
    d.pageImages.every(isImageRef)
  );
}

function isConversation(value: unknown): value is Conversation {
  const c = value as Conversation;

  return (
    typeof c === "object" &&
    c !== null &&
    typeof c.id === "string" &&
    typeof c.title === "string" &&
    typeof c.model === "string" &&
    typeof c.system === "string" &&
    typeof c.updatedAt === "number" &&
    EFFORTS.includes(c.effort) &&
    (c.output === undefined || isOutputFormat(c.output)) &&
    Array.isArray(c.messages) &&
    c.messages.every(
      (m) =>
        typeof m === "object" &&
        m !== null &&
        typeof m.id === "string" &&
        typeof m.content === "string" &&
        (m.role === "user" || m.role === "assistant") &&
        (m.role !== "user" ||
          ((m.images === undefined || (Array.isArray(m.images) && m.images.every(isImageRef))) &&
            (m.documents === undefined || (Array.isArray(m.documents) && m.documents.every(isDocumentRef))))),
    )
  );
}

export function storageKey(scope: string): string {
  return KEY_PREFIX + scope;
}

/**
 * Chats from another tab joined with this tab's, the newer copy of each
 * winning, so two open tabs do not overwrite each other's chats.
 */
export function mergeConversations(mine: Conversation[], theirs: Conversation[]): Conversation[] {
  const byId = new Map(mine.map((c) => [c.id, c]));
  for (const c of theirs) {
    const own = byId.get(c.id);
    if (!own || c.updatedAt > own.updatedAt) {
      byId.set(c.id, c);
    }
  }

  return [...byId.values()];
}

export function loadPlayground(scope: string): PlaygroundState {
  try {
    const raw = window.localStorage.getItem(storageKey(scope));
    if (!raw) {
      return EMPTY;
    }
    const parsed = JSON.parse(raw) as Partial<PlaygroundState>;

    return {
      conversations: Array.isArray(parsed.conversations) ? parsed.conversations.filter(isConversation) : [],
      lastModel: typeof parsed.lastModel === "string" ? parsed.lastModel : null,
      lastEffort: EFFORTS.includes(parsed.lastEffort as Effort) ? (parsed.lastEffort as Effort) : "medium",
    };
  } catch {
    return EMPTY;
  }
}

/**
 * Keeps the most recent chats. A reply still streaming when the page goes
 * away is saved as stopped. When storage is full the oldest chats go first.
 */
export function savePlayground(scope: string, state: PlaygroundState): void {
  let conversations = [...state.conversations]
    .sort((a, b) => b.updatedAt - a.updatedAt)
    .slice(0, MAX_CONVERSATIONS)
    .map((c) => ({
      ...c,
      messages: c.messages.map((m) =>
        m.role === "assistant" && m.status === "streaming" ? { ...m, status: "stopped" as const } : m,
      ),
    }));

  for (;;) {
    try {
      window.localStorage.setItem(storageKey(scope), JSON.stringify({ ...state, conversations }));

      return;
    } catch {
      if (conversations.length === 0) {
        return;
      }
      conversations = conversations.slice(0, -1);
    }
  }
}
