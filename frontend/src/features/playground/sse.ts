import type { TokenUsage } from "./types";

/** One chat.completion.chunk, reduced to what the playground shows. */
export type StreamDelta = {
  content: string;
  reasoning: string;
  model: string | null;
  usage: TokenUsage | null;
};

/** An error the gateway or the provider sent inside the stream. */
export class StreamError extends Error {}

/**
 * Splits server-sent events into the payloads of their `data:` lines. Text can
 * arrive cut anywhere, so an unfinished event stays buffered; comment lines
 * (`: keep-alive` heartbeats) are dropped.
 */
export function createSSEDecoder(): {
  push: (text: string) => string[];
  flush: () => string[];
} {
  let buffer = "";
  const events = (blocks: string[]) =>
    blocks.flatMap((block) => {
      const data = block
        .split(/\r?\n/)
        .filter((line) => line.startsWith("data:"))
        .map((line) => line.slice(line.startsWith("data: ") ? 6 : 5));

      return data.length > 0 ? [data.join("\n")] : [];
    });

  return {
    push(text) {
      buffer += text;
      const blocks = buffer.split(/\r?\n\r?\n/);
      buffer = blocks.pop() ?? "";

      return events(blocks);
    },
    flush() {
      const rest = buffer;
      buffer = "";

      return events([rest]);
    },
  };
}

type Chunk = {
  model?: string;
  error?: { message?: string };
  choices?: {
    delta?: {
      content?: string | null;
      reasoning_content?: string | null;
      reasoning?: string | null;
    };
  }[];
  usage?: {
    prompt_tokens?: number;
    completion_tokens?: number;
    completion_tokens_details?: { reasoning_tokens?: number } | null;
  } | null;
};

/** null for `[DONE]` and payloads without anything to show. */
export function parseChunk(data: string): StreamDelta | null {
  if (data.trim() === "[DONE]") {
    return null;
  }
  let chunk: Chunk;
  try {
    chunk = JSON.parse(data) as Chunk;
  } catch {
    return null;
  }
  if (chunk.error) {
    throw new StreamError(chunk.error.message || "The provider stopped the response.");
  }
  const delta = chunk.choices?.[0]?.delta;
  const usage = chunk.usage
    ? {
        prompt: chunk.usage.prompt_tokens ?? 0,
        completion: chunk.usage.completion_tokens ?? 0,
        reasoning: chunk.usage.completion_tokens_details?.reasoning_tokens ?? 0,
      }
    : null;

  return {
    content: delta?.content ?? "",
    reasoning: delta?.reasoning_content ?? delta?.reasoning ?? "",
    model: chunk.model ?? null,
    usage,
  };
}

/** Reads a streamed chat completion, calling onDelta per chunk. */
export async function readChatStream(
  body: ReadableStream<Uint8Array>,
  onDelta: (delta: StreamDelta) => void,
): Promise<void> {
  const reader = body.getReader();
  const text = new TextDecoder();
  const sse = createSSEDecoder();
  const emit = (payloads: string[]) => {
    for (const payload of payloads) {
      const delta = parseChunk(payload);
      if (delta) {
        onDelta(delta);
      }
    }
  };

  for (;;) {
    const { done, value } = await reader.read();
    if (done) {
      emit(sse.push(text.decode()));
      emit(sse.flush());

      return;
    }
    emit(sse.push(text.decode(value, { stream: true })));
  }
}
