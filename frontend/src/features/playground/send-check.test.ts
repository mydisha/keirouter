import { describe, expect, it } from "vitest";
import { sendCheck } from "./send-check";
import type { DocumentRef, ImageRef } from "./types";

const model = {
  name: "DeepSeek V4.1 Flash",
  vision: true,
  pdf: false,
  context_window: 100_000,
};
const image: ImageRef = {
  id: "i",
  name: "i.png",
  mime: "image/png",
  width: 1,
  height: 1,
  bytes: 300,
};
const doc = (over: Partial<DocumentRef> = {}): DocumentRef => ({
  id: "d",
  name: "report.pdf",
  bytes: 3000,
  pages: 3,
  textId: "t",
  chars: 4000,
  pageImages: [],
  ...over,
});
const check = (over: Partial<Parameters<typeof sendCheck>[0]> = {}) =>
  sendCheck({
    model,
    gatewayReachable: true,
    sent: [],
    images: [],
    documents: [],
    ...over,
  });

describe("sendCheck", () => {
  it("lets a PDF through to a model without vision or PDF support", () => {
    expect(
      check({
        model: { ...model, vision: false },
        documents: [doc()],
      }),
    ).toEqual({ blocked: null, notice: null });
  });

  it("refuses images for a model that cannot read them", () => {
    expect(check({ model: { ...model, vision: false }, images: [image] }).blocked).toBe(
      "DeepSeek V4.1 Flash can't read images. Remove them or pick a model that can.",
    );
    expect(
      check({
        model: { ...model, vision: false },
        gatewayReachable: false,
        images: [image],
      }).blocked,
    ).toBe("Images are off until the gateway answers again. Remove them or try again in a minute.");
  });

  it("refuses a scan the model cannot read in any form", () => {
    expect(
      check({
        model: { ...model, vision: false },
        documents: [doc({ textId: null })],
      }).blocked,
    ).toBe("report.pdf is a scanned PDF without text. Pick a model that reads images or PDFs, or remove it.");
  });

  it("counts earlier turns against the context window", () => {
    const big = doc({ chars: 300_000 });

    expect(
      check({
        sent: [{ id: "u", role: "user", content: "x", documents: [big] }],
        documents: [doc({ chars: 50_000 })],
      }).blocked,
    ).toBe(
      "This chat's PDFs come to about 87.5K tokens and DeepSeek V4.1 Flash reads up to 100K. Remove a PDF, start a new chat, or pick a model with a longer context.",
    );
  });

  it("does not count text for a model that gets the PDF itself", () => {
    expect(
      check({
        model: { ...model, pdf: true },
        documents: [doc({ chars: 1_000_000, bytes: 1000 })],
      }).blocked,
    ).toBeNull();
  });

  it("refuses a chat whose attachments outgrow a request", () => {
    expect(
      check({
        model: { ...model, pdf: true },
        documents: [doc({ bytes: 10 * 1024 * 1024 })],
      }).blocked,
    ).toMatch(/^This chat carries 13 MB of attachments and a request can carry 12 MB\./);
  });

  it("refuses to send while the output schema has an error", () => {
    expect(check({ output: { mode: "schema", name: "x", schema: "{" } }).blocked).toBe(
      "Fix the output schema first: The schema is not valid JSON.",
    );
    expect(check({ output: { mode: "json" } }).blocked).toBeNull();
  });

  it("notes that earlier images are left out for a text model", () => {
    expect(
      check({
        model: { ...model, vision: false },
        sent: [{ id: "u", role: "user", content: "x", images: [image] }],
      }).notice,
    ).toBe("DeepSeek V4.1 Flash can't read images, so this chat's images are not sent to it.");
  });
});
