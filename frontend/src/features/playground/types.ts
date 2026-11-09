import type { OutputFormat } from "./output-format";

/** A chat model the playground can pick (GET /api/playground/models). */
export type PlaygroundModel = {
  /** What goes in the request's `model` field: provider/model, or a chain's name. */
  id: string;
  name: string;
  provider?: string;
  provider_name?: string;
  /** The gateway's icon path; its basename picks the provider mark. */
  icon?: string;
  /** A routing chain; its steps decide where the request goes. */
  chain?: boolean;
  /** A reasoning effort reaches the model and changes how it thinks. */
  reasoning: boolean;
  /** "none" turns thinking off rather than being ignored. */
  can_disable_reasoning: boolean;
  /** The model reads images. */
  vision: boolean;
  /** The model reads PDF files; otherwise the playground sends a PDF's text. */
  pdf: boolean;
  context_window?: number;
};

export type Effort = "off" | "low" | "medium" | "high";

/** An attached image; its bytes live in the browser's image store (image-store.ts). */
export type ImageRef = {
  id: string;
  name: string;
  mime: string;
  width: number;
  height: number;
  /** Size of the stored (possibly re-encoded) image. */
  bytes: number;
};

/**
 * An attached PDF. The file, its extracted text and any page images live in
 * the browser's attachment store; which of them a request carries depends on
 * the model (api-messages.ts).
 */
export type DocumentRef = {
  id: string;
  name: string;
  bytes: number;
  pages: number;
  /** The text layer as stored text; null for a scanned PDF without one. */
  textId: string | null;
  chars: number;
  /** A scanned PDF's first pages as images, for models that read images. */
  pageImages: ImageRef[];
};

export type UserMessage = {
  id: string;
  role: "user";
  content: string;
  images?: ImageRef[];
  documents?: DocumentRef[];
};

export type AssistantStatus = "streaming" | "done" | "stopped" | "error";

export type TokenUsage = {
  prompt: number;
  completion: number;
  reasoning: number;
};

export type AssistantMessage = {
  id: string;
  role: "assistant";
  /** The model the request asked for; `served` is the one that answered (a fallback can differ). */
  model: string;
  served: string | null;
  effort: Effort | null;
  content: string;
  reasoning: string;
  status: AssistantStatus;
  error: { message: string; status: number | null } | null;
  usage: TokenUsage | null;
  requestId: string | null;
  latencyMs: number | null;
  /** How long the model thought before the answer began. */
  thinkingMs: number | null;
  /** The reply was asked for as JSON (any object, or a schema); absent for text. */
  output?: "json" | "schema";
};

export type ChatMessage = UserMessage | AssistantMessage;

export type Conversation = {
  id: string;
  title: string;
  model: string;
  effort: Effort;
  system: string;
  /** Absent in chats saved before the output control: text. */
  output?: OutputFormat;
  messages: ChatMessage[];
  updatedAt: number;
};
