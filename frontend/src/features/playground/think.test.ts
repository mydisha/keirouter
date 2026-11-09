import { describe, expect, it } from "vitest";
import { splitInlineThinking } from "./think";

describe("splitInlineThinking", () => {
  it("leaves content without a think block alone", () => {
    expect(splitInlineThinking("Hello <b>there</b>")).toEqual({
      reasoning: "",
      content: "Hello <b>there</b>",
    });
  });

  it("holds back a tag that is still arriving", () => {
    expect(splitInlineThinking("<thi")).toEqual({
      reasoning: "",
      content: "",
    });
    expect(splitInlineThinking("  <")).toEqual({
      reasoning: "",
      content: "",
    });
  });

  it("treats an unclosed block as reasoning", () => {
    expect(splitInlineThinking("<think>\nLet me see")).toEqual({
      reasoning: "Let me see",
      content: "",
    });
  });

  it("splits a closed block from the answer", () => {
    expect(splitInlineThinking("<think>2+2</think>\n\nIt is 4.")).toEqual({
      reasoning: "2+2",
      content: "It is 4.",
    });
  });

  it("only takes a block at the start", () => {
    const text = "Use <think> tags like this";
    expect(splitInlineThinking(text)).toEqual({
      reasoning: "",
      content: text,
    });
  });
});
