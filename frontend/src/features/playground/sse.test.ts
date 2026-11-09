import { describe, expect, it } from "vitest";
import { StreamError, createSSEDecoder, parseChunk, readChatStream } from "./sse";
import type { StreamDelta } from "./sse";

const chunk = (delta: object, extra: object = {}) =>
  `data: ${JSON.stringify({ model: "openai/o3", choices: [{ index: 0, delta }], ...extra })}\n\n`;

function streamOf(parts: string[]): ReadableStream<Uint8Array> {
  const bytes = new TextEncoder();

  return new ReadableStream({
    start(controller) {
      for (const part of parts) {
        controller.enqueue(bytes.encode(part));
      }
      controller.close();
    },
  });
}

describe("createSSEDecoder", () => {
  it("keeps an event cut across reads until it is complete", () => {
    const sse = createSSEDecoder();
    expect(sse.push('data: {"a"')).toEqual([]);
    expect(sse.push(":1}\n")).toEqual([]);
    expect(sse.push("\ndata: [DONE]\n\n")).toEqual(['{"a":1}', "[DONE]"]);
  });

  it("drops heartbeat comments and reads CRLF events", () => {
    const sse = createSSEDecoder();
    expect(sse.push(": keep-alive\n\ndata: x\r\n\r\ndata:y\n\n")).toEqual(["x", "y"]);
  });

  it("returns a last event without its blank line on flush", () => {
    const sse = createSSEDecoder();
    expect(sse.push("data: tail")).toEqual([]);
    expect(sse.flush()).toEqual(["tail"]);
  });
});

describe("parseChunk", () => {
  it("reads content, reasoning and the served model", () => {
    expect(
      parseChunk(
        JSON.stringify({
          model: "deepseek/r1",
          choices: [{ delta: { reasoning_content: "hm", content: "Hi" } }],
        }),
      ),
    ).toEqual({
      content: "Hi",
      reasoning: "hm",
      model: "deepseek/r1",
      usage: null,
    });
  });

  it("reads usage with reasoning tokens from the final chunk", () => {
    expect(
      parseChunk(
        JSON.stringify({
          choices: [],
          usage: {
            prompt_tokens: 12,
            completion_tokens: 40,
            completion_tokens_details: { reasoning_tokens: 30 },
          },
        }),
      )?.usage,
    ).toEqual({ prompt: 12, completion: 40, reasoning: 30 });
  });

  it("ignores [DONE] and junk", () => {
    expect(parseChunk("[DONE]")).toBeNull();
    expect(parseChunk("not json")).toBeNull();
  });

  it("throws the message of an error sent mid-stream", () => {
    expect(() => parseChunk(JSON.stringify({ error: { message: "upstream timed out" } }))).toThrow(
      new StreamError("upstream timed out"),
    );
  });
});

describe("readChatStream", () => {
  it("delivers every delta even when bytes split events and characters", async () => {
    const body = chunk({ content: "Héllo" }) + chunk({ content: " wörld" }) + "data: [DONE]\n\n";
    const bytes = new TextEncoder().encode(body);
    // Split inside the multi-byte é.
    const cut = body.indexOf("é") + 1;
    const parts = [bytes.slice(0, cut), bytes.slice(cut)];
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        parts.forEach((p) => controller.enqueue(p));
        controller.close();
      },
    });
    const seen: StreamDelta[] = [];

    await readChatStream(stream, (d) => seen.push(d));

    expect(seen.map((d) => d.content).join("")).toBe("Héllo wörld");
  });

  it("rejects with the stream error", async () => {
    const stream = streamOf([
      chunk({ content: "partial" }),
      `data: ${JSON.stringify({ error: { message: "provider failed" } })}\n\n`,
    ]);

    await expect(readChatStream(stream, () => {})).rejects.toThrow("provider failed");
  });
});
