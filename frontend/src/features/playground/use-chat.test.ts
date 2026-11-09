import { describe, expect, it } from "vitest";
import { TEXT_OUTPUT } from "./output-format";
import type { Conversation, PlaygroundModel } from "./types";
import { withListedModel } from "./use-chat";

const draft = (model: string): Conversation => ({
  id: "c1",
  title: "New chat",
  model,
  effort: "medium",
  system: "",
  output: TEXT_OUTPUT,
  messages: [],
  updatedAt: 0,
});

const model = (id: string) => ({ id }) as PlaygroundModel;

describe("withListedModel", () => {
  it("gives a chat made before the list arrived the fallback model", () => {
    const before = draft("");

    expect(withListedModel(before, [model("openai/gpt-x"), model("anthropic/claude-x")], "anthropic/claude-x")).toEqual(
      { ...before, model: "anthropic/claude-x" },
    );
  });

  it("keeps the same object while the list is still empty, so state does not change", () => {
    const before = draft("");

    expect(withListedModel(before, [], "")).toBe(before);
  });

  it("keeps a model that is listed", () => {
    const chosen = draft("openai/gpt-x");

    expect(withListedModel(chosen, [model("openai/gpt-x"), model("meta/llama-x")], "meta/llama-x")).toBe(chosen);
  });

  it("replaces a model that left the list", () => {
    expect(withListedModel(draft("old/model"), [model("meta/llama-x")], "meta/llama-x")?.model).toBe("meta/llama-x");
  });

  it("leaves an open saved chat alone (no draft)", () => {
    expect(withListedModel(null, [model("meta/llama-x")], "meta/llama-x")).toBeNull();
  });
});
