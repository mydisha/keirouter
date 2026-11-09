import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { apiMessages, attachmentIds } from "./api-messages";
import { effectiveEffort, reasoningEffortParam } from "./effort";
import { newId } from "./id";
import { TEXT_OUTPUT, responseFormat, withJsonInstruction } from "./output-format";
import type { OutputFormat } from "./output-format";
import { imageBlob, imageDataUrl, pruneImages } from "./image-store";
import { REQUEST_IMAGE_BUDGET, formatBytes } from "./images";
import { StreamError, readChatStream } from "./sse";
import { loadPlayground, mergeConversations, savePlayground, storageKey } from "./storage";
import type { PlaygroundState } from "./storage";
import { splitInlineThinking } from "./think";
import type {
  AssistantMessage,
  ChatMessage,
  Conversation,
  DocumentRef,
  Effort,
  ImageRef,
  PlaygroundModel,
} from "./types";

/** Same-origin endpoint: the dashboard session cookie is the credential. */
export const PLAYGROUND_CHAT_URL = "/api/playground/chat/completions";

/** A failed request, with the HTTP status when there was one. */
export class PlaygroundError extends Error {
  constructor(
    message: string,
    readonly status: number | null = null,
  ) {
    super(message);
  }
}

const TITLE_LENGTH = 60;
const SAVE_DELAY_MS = 300;

function blankConversation(model: string, effort: Effort, output: OutputFormat = TEXT_OUTPUT): Conversation {
  return {
    id: newId(),
    title: "New chat",
    model,
    effort,
    system: "",
    output,
    messages: [],
    updatedAt: Date.now(),
  };
}

/** Gateway messages are lower-case fragments: "model is not available". */
function sentence(text: string): string {
  const t = text.trim();
  const capital = t.charAt(0).toUpperCase() + t.slice(1);

  return /[.!?]$/.test(capital) ? capital : `${capital}.`;
}

async function failureMessage(response: Response): Promise<PlaygroundError> {
  const body = (await response.json().catch(() => null)) as {
    error?: { message?: string };
  } | null;
  const said = body?.error?.message;
  switch (response.status) {
    case 401:
      return new PlaygroundError("Your dashboard session ended. Sign in again, then resend.", 401);
    case 403:
      if (said) {
        return new PlaygroundError(
          `A guardrail blocked this prompt: ${said}. Edit the prompt, or change the rule on the Guardrails page.`,
          403,
        );
      }
      break;
    case 413:
      return new PlaygroundError(
        `This chat is too large to send. Start a new chat or keep its images under ${formatBytes(REQUEST_IMAGE_BUDGET)} in total.`,
        413,
      );
    case 429: {
      const wait = Number(response.headers.get("Retry-After"));

      return new PlaygroundError(
        wait > 0
          ? `You've hit the rate limit. Try again in ${wait} s.`
          : "You've hit the rate limit. Wait a moment and send again.",
        429,
      );
    }
  }

  return new PlaygroundError(
    said ? sentence(said) : "The request didn't go through. Try again or pick another model.",
    response.status,
  );
}

/**
 * The unsent chat with a model from the list: one made before the list
 * arrived (or whose model left it) gets `fallback`; otherwise it is returned
 * as it is, so state does not change.
 */
export function withListedModel(
  draft: Conversation | null,
  models: PlaygroundModel[],
  fallback: string,
): Conversation | null {
  if (!draft || draft.model === fallback || models.some((m) => m.id === draft.model)) {
    return draft;
  }

  return { ...draft, model: fallback };
}

/**
 * Chats saved in this browser. A reply streams from the gateway's playground
 * endpoint, which runs the same pipeline as /v1 under the dashboard session.
 */
export function useChat({
  storageScope = "dashboard",
  models,
  skipOptionalSkills = false,
}: {
  /** localStorage scope for the saved chats. */
  storageScope?: string;
  models: PlaygroundModel[];
  /** Sends X-KeiRouter-Skip-Skills, so optional skills are left out. */
  skipOptionalSkills?: boolean;
}) {
  const fallbackModel = models[0]?.id ?? "";
  const [state, setState] = useState<PlaygroundState>(() => ({
    conversations: [],
    lastModel: null,
    lastEffort: "medium",
  }));
  const [activeId, setActiveId] = useState<string | null>(null);
  const [draft, setDraft] = useState<Conversation | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [streamingId, setStreamingId] = useState<string | null>(null);
  const abort = useRef<AbortController | null>(null);
  const scope = storageScope;

  const modelOrFallback = useCallback(
    (slug: string | null) => (models.some((m) => m.id === slug) ? (slug as string) : fallbackModel),
    [models, fallbackModel],
  );

  // Streaming changes state every frame; write at most a few times a second.
  const latest = useRef(state);
  latest.current = state;
  const pickModel = useRef(modelOrFallback);
  pickModel.current = modelOrFallback;

  // localStorage is read after mount: the server render has no window.
  useEffect(() => {
    const saved = loadPlayground(scope);
    setState(saved);
    setActiveId(null);
    setDraft(blankConversation(pickModel.current(saved.lastModel), saved.lastEffort));
    setLoaded(true);
  }, [scope]);

  // The model list can arrive after the saved chats (a deferred prop): the
  // new chat then gets the last used model, or the first one listed.
  useEffect(() => {
    setDraft((d) => withListedModel(d, models, modelOrFallback(latest.current.lastModel)));
  }, [models, modelOrFallback]);

  useEffect(() => {
    if (!loaded) {
      return;
    }
    const timer = window.setTimeout(() => {
      savePlayground(scope, state);
      const referenced = new Set(state.conversations.flatMap((c) => attachmentIds(c.messages)));
      void pruneImages(scope, referenced);
    }, SAVE_DELAY_MS);

    return () => window.clearTimeout(timer);
  }, [scope, state, loaded]);
  useEffect(() => {
    if (!loaded) {
      return;
    }
    const flush = () => savePlayground(scope, latest.current);
    const merge = (event: StorageEvent) => {
      if (event.key === storageKey(scope)) {
        const theirs = loadPlayground(scope).conversations;
        setState((s) => ({
          ...s,
          conversations: mergeConversations(s.conversations, theirs),
        }));
      }
    };
    window.addEventListener("pagehide", flush);
    window.addEventListener("storage", merge);

    return () => {
      flush();
      window.removeEventListener("pagehide", flush);
      window.removeEventListener("storage", merge);
    };
  }, [scope, loaded]);

  useEffect(() => () => abort.current?.abort(), []);

  const active = useMemo(
    () => state.conversations.find((c) => c.id === activeId) ?? draft,
    [state.conversations, activeId, draft],
  );

  const updateConversation = useCallback((id: string, change: (c: Conversation) => Conversation) => {
    setState((s) => ({
      ...s,
      conversations: s.conversations.map((c) => (c.id === id ? change(c) : c)),
    }));
  }, []);

  const updateAssistant = useCallback(
    (conversationId: string, messageId: string, change: Partial<AssistantMessage>) =>
      updateConversation(conversationId, (c) => ({
        ...c,
        messages: c.messages.map((m) => (m.id === messageId && m.role === "assistant" ? { ...m, ...change } : m)),
      })),
    [updateConversation],
  );

  /** Changes the open chat's settings; a draft is not saved until its first message. */
  const configure = useCallback(
    (change: Partial<Pick<Conversation, "model" | "effort" | "system" | "output">>) => {
      if (!active) {
        return;
      }
      if (active === draft) {
        setDraft({ ...draft, ...change });
      } else {
        updateConversation(active.id, (c) => ({ ...c, ...change }));
      }
      setState((s) => ({
        ...s,
        lastModel: change.model ?? s.lastModel,
        lastEffort: change.effort ?? s.lastEffort,
      }));
    },
    [active, draft, updateConversation],
  );

  const stream = useCallback(
    async (conversation: Conversation, history: ChatMessage[]) => {
      const model = models.find((m) => m.id === conversation.model);
      const effort = effectiveEffort(model, conversation.effort);
      const output = conversation.output ?? TEXT_OUTPUT;
      const format = responseFormat(output);
      const reply: AssistantMessage = {
        id: newId(),
        role: "assistant",
        model: conversation.model,
        served: null,
        effort,
        content: "",
        reasoning: "",
        status: "streaming",
        error: null,
        usage: null,
        requestId: null,
        latencyMs: null,
        thinkingMs: null,
        ...(output.mode !== "text" ? { output: output.mode } : {}),
      };
      updateConversation(conversation.id, (c) => ({
        ...c,
        messages: [...history, reply],
        updatedAt: Date.now(),
      }));
      const controller = new AbortController();
      abort.current = controller;
      setStreamingId(reply.id);

      const effortParam = reasoningEffortParam(effort);
      const started = performance.now();
      let raw = "";
      let nativeReasoning = "";
      let thinkingEndedAt: number | null = null;
      let pendingFrame = 0;
      const flush = () => {
        pendingFrame = 0;
        const inline = splitInlineThinking(raw);
        const content = nativeReasoning ? raw : inline.content;
        const reasoning = nativeReasoning || inline.reasoning;
        if (content && thinkingEndedAt === null && reasoning) {
          thinkingEndedAt = performance.now();
        }
        updateAssistant(conversation.id, reply.id, {
          content,
          reasoning,
          thinkingMs: thinkingEndedAt === null ? null : Math.round(thinkingEndedAt - started),
        });
      };

      try {
        const body = JSON.stringify({
          model: conversation.model,
          stream: true,
          messages: await apiMessages(withJsonInstruction(conversation.system, output), history, {
            vision: model?.vision ?? false,
            documents: model?.pdf ?? false,
            dataUrl: imageDataUrl,
            text: async (id) => (await (await imageBlob(id))?.text()) ?? null,
          }),
          // Ask for the final usage chunk so the receipt has real counts.
          stream_options: { include_usage: true },
          ...(effortParam ? { reasoning_effort: effortParam } : {}),
          ...(format ? { response_format: format } : {}),
        });
        const response = await fetch(PLAYGROUND_CHAT_URL, {
          method: "POST",
          credentials: "same-origin",
          headers: {
            "Content-Type": "application/json",
            Accept: "text/event-stream",
            ...(skipOptionalSkills ? { "X-KeiRouter-Skip-Skills": "1" } : {}),
          },
          body,
          signal: controller.signal,
        });
        updateAssistant(conversation.id, reply.id, { requestId: response.headers.get("X-Request-Id") });
        if (!response.ok || !response.body) {
          throw await failureMessage(response);
        }
        await readChatStream(response.body, (delta) => {
          raw += delta.content;
          nativeReasoning += delta.reasoning;
          if (delta.model) {
            updateAssistant(conversation.id, reply.id, {
              served: delta.model,
            });
          }
          if (delta.usage) {
            updateAssistant(conversation.id, reply.id, {
              usage: delta.usage,
            });
          }
          pendingFrame ||= requestAnimationFrame(flush);
        });
        cancelAnimationFrame(pendingFrame);
        flush();
        updateAssistant(conversation.id, reply.id, {
          status: "done",
          latencyMs: Math.round(performance.now() - started),
        });
      } catch (error) {
        cancelAnimationFrame(pendingFrame);
        flush();
        if (controller.signal.aborted) {
          updateAssistant(conversation.id, reply.id, {
            status: "stopped",
            latencyMs: Math.round(performance.now() - started),
          });
        } else {
          const message =
            error instanceof PlaygroundError || error instanceof StreamError
              ? error.message
              : "Can't reach KeiRouter. Check that the gateway is running, then try again.";
          updateAssistant(conversation.id, reply.id, {
            status: "error",
            error: {
              message,
              status: error instanceof PlaygroundError ? error.status : null,
            },
          });
        }
      } finally {
        if (abort.current === controller) {
          abort.current = null;
        }
        setStreamingId((id) => (id === reply.id ? null : id));
      }
    },
    [models, skipOptionalSkills, updateConversation, updateAssistant],
  );

  const send = useCallback(
    (text: string, images: ImageRef[] = [], documents: DocumentRef[] = []) => {
      const content = text.trim();
      if (!active || (!content && images.length === 0 && documents.length === 0) || streamingId) {
        return;
      }
      const message: ChatMessage = {
        id: newId(),
        role: "user",
        content,
        ...(images.length > 0 ? { images } : {}),
        ...(documents.length > 0 ? { documents } : {}),
      };
      const history = [...active.messages, message];
      let conversation = active;
      if (active === draft) {
        conversation = {
          ...draft,
          title: (content || documents[0]?.name || images[0].name).replace(/\s+/g, " ").slice(0, TITLE_LENGTH),
        };
        setState((s) => ({
          ...s,
          conversations: [conversation, ...s.conversations],
        }));
        setActiveId(conversation.id);
        setDraft(null);
      }
      void stream(conversation, history);
    },
    [active, draft, streamingId, stream],
  );

  /** Asks again for the last reply, with the chat's current model and thinking. */
  const regenerate = useCallback(() => {
    if (!active || streamingId) {
      return;
    }
    const lastUser = active.messages.findLastIndex((m) => m.role === "user");
    if (lastUser < 0) {
      return;
    }
    void stream(active, active.messages.slice(0, lastUser + 1));
  }, [active, streamingId, stream]);

  const stop = useCallback(() => abort.current?.abort(), []);

  const newChat = useCallback(() => {
    stop();
    setActiveId(null);
    setDraft(
      blankConversation(
        modelOrFallback(active?.model ?? state.lastModel),
        active?.effort ?? state.lastEffort,
        active?.output,
      ),
    );
  }, [stop, modelOrFallback, active, state.lastModel, state.lastEffort]);

  const open = useCallback(
    (id: string) => {
      if (id === activeId) {
        return;
      }
      stop();
      setActiveId(id);
      setDraft(null);
    },
    [activeId, stop],
  );

  const remove = useCallback(
    (id: string) => {
      if (id === activeId) {
        stop();
        setActiveId(null);
        setDraft(blankConversation(modelOrFallback(state.lastModel), state.lastEffort));
      }
      setState((s) => ({
        ...s,
        conversations: s.conversations.filter((c) => c.id !== id),
      }));
    },
    [activeId, stop, modelOrFallback, state.lastModel, state.lastEffort],
  );

  return {
    loaded,
    conversations: state.conversations,
    active,
    streaming: streamingId !== null,
    configure,
    send,
    stop,
    regenerate,
    newChat,
    open,
    remove,
  };
}
