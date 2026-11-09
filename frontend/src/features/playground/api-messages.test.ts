import { describe, expect, it } from "vitest";
import { apiMessages, attachmentIds, chatImages } from "./api-messages";
import type { AssistantMessage, ChatMessage, DocumentRef, ImageRef, UserMessage } from "./types";

const image = (id: string): ImageRef => ({
  id,
  name: `${id}.png`,
  mime: "image/png",
  width: 1,
  height: 1,
  bytes: 3,
});

const reply = (content: string, status: AssistantMessage["status"] = "done"): ChatMessage => ({
  id: "a",
  role: "assistant",
  model: "m",
  served: null,
  effort: null,
  content,
  reasoning: "thought",
  status,
  error: null,
  usage: null,
  requestId: null,
  latencyMs: null,
  thinkingMs: null,
});

const urls: Record<string, string> = {
  one: "data:image/png;base64,AAAA",
  two: "data:image/png;base64,BBBB",
};
const dataUrl = async (id: string) => urls[id] ?? null;
const texts: Record<string, string> = { "inv-text": "[Page 1]\nInvoice 7" };
const reader = {
  vision: true,
  documents: false,
  dataUrl,
  text: async (id: string) => texts[id] ?? null,
};

const doc = (over: Partial<DocumentRef> = {}): DocumentRef => ({
  id: "inv",
  name: "invoice.pdf",
  bytes: 3,
  pages: 1,
  textId: "inv-text",
  chars: 18,
  pageImages: [],
  ...over,
});

describe("apiMessages", () => {
  it("sends text-only turns as plain strings, without failed or empty replies", async () => {
    const out = await apiMessages(
      "  Be brief. ",
      [{ id: "u1", role: "user", content: "Hi" }, reply("", "stopped"), reply("Boom", "error"), reply("Hello")],
      { ...reader, vision: true },
    );

    expect(out).toEqual([
      { role: "system", content: "Be brief." },
      { role: "user", content: "Hi" },
      { role: "assistant", content: "Hello" },
    ]);
  });

  it("sends images as image_url parts after the text, in order", async () => {
    const out = await apiMessages(
      "",
      [
        {
          id: "u1",
          role: "user",
          content: "Compare",
          images: [image("one"), image("two")],
        },
      ],
      { ...reader, vision: true },
    );

    expect(out).toEqual([
      {
        role: "user",
        content: [
          { type: "text", text: "Compare" },
          { type: "image_url", image_url: { url: urls.one } },
          { type: "image_url", image_url: { url: urls.two } },
        ],
      },
    ]);
  });

  it("sends an image-only message without an empty text part", async () => {
    const out = await apiMessages("", [{ id: "u1", role: "user", content: "", images: [image("one")] }], {
      ...reader,
      vision: true,
    });

    expect(out[0].content).toEqual([{ type: "image_url", image_url: { url: urls.one } }]);
  });

  it("leaves images out for a model that cannot read them", async () => {
    const out = await apiMessages(
      "",
      [
        {
          id: "u1",
          role: "user",
          content: "What is this?",
          images: [image("one")],
        },
        { id: "u2", role: "user", content: "", images: [image("two")] },
      ],
      { ...reader, vision: false },
    );

    expect(out).toEqual([
      { role: "user", content: "What is this?" },
      { role: "user", content: "(image)" },
    ]);
  });

  it("skips images no longer in this browser", async () => {
    const out = await apiMessages(
      "",
      [
        {
          id: "u1",
          role: "user",
          content: "See",
          images: [image("gone"), image("one")],
        },
      ],
      { ...reader, vision: true },
    );

    expect(out[0].content).toEqual([
      { type: "text", text: "See" },
      { type: "image_url", image_url: { url: urls.one } },
    ]);
  });
});

describe("chatImages", () => {
  it("lists every user image in order", () => {
    expect(
      chatImages([
        { id: "u1", role: "user", content: "", images: [image("one")] },
        reply("ok"),
        { id: "u2", role: "user", content: "x" },
        { id: "u3", role: "user", content: "", images: [image("two")] },
      ]).map((i) => i.id),
    ).toEqual(["one", "two"]);
  });
});

describe("apiMessages with PDFs", () => {
  const ask = (d: DocumentRef): UserMessage => ({
    id: "u",
    role: "user",
    content: "Total?",
    documents: [d],
  });
  urls.inv = "data:application/pdf;base64,JVBE";

  it("sends the PDF itself to a model that reads PDFs, before the question", async () => {
    const [turn] = await apiMessages("", [ask(doc())], {
      ...reader,
      documents: true,
    });

    expect(turn.content).toEqual([
      {
        type: "file",
        file: {
          filename: "invoice.pdf",
          file_data: "data:application/pdf;base64,JVBE",
        },
      },
      { type: "text", text: "Total?" },
    ]);
  });

  it("sends the extracted text to every other model", async () => {
    const [turn] = await apiMessages("", [ask(doc())], reader);

    expect(turn.content).toEqual([
      {
        type: "text",
        text: '<document name="invoice.pdf" pages="1">\n[Page 1]\nInvoice 7\n</document>',
      },
      { type: "text", text: "Total?" },
    ]);
  });

  it("sends a scan as page images to a model that reads images", async () => {
    const scan = doc({
      textId: null,
      pages: 4,
      pageImages: [image("one")],
    });
    const [turn] = await apiMessages("", [ask(scan)], reader);

    expect(turn.content).toEqual([
      {
        type: "text",
        text: "[invoice.pdf: a scanned PDF of 4 pages; the first 1 follow as images]",
      },
      { type: "image_url", image_url: { url: urls.one } },
      { type: "text", text: "Total?" },
    ]);
  });

  it("says a scan was left out where the model reads neither", async () => {
    const scan = doc({ textId: null, pageImages: [image("one")] });
    const [turn] = await apiMessages("", [ask(scan)], {
      ...reader,
      vision: false,
    });

    expect(turn.content).toEqual([
      {
        type: "text",
        text: "[invoice.pdf is a scanned PDF without text and was not sent]",
      },
      { type: "text", text: "Total?" },
    ]);
  });

  it("says so when the stored text is gone", async () => {
    const [turn] = await apiMessages("", [ask(doc({ textId: "missing" }))], reader);

    expect(turn.content).toEqual([
      {
        type: "text",
        text: "[invoice.pdf is no longer in this browser and was not sent]",
      },
      { type: "text", text: "Total?" },
    ]);
  });

  it("keeps every stored piece of a document referenced", () => {
    expect(
      attachmentIds([
        {
          ...ask(doc({ pageImages: [image("p1")] })),
          images: [image("i1")],
        },
      ]),
    ).toEqual(["i1", "inv", "inv-text", "p1"]);
  });
});
