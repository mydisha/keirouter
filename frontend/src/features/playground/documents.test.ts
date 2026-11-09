import { describe, expect, it } from "vitest";
import {
  documentBlock,
  documentBytes,
  documentMode,
  documentSummary,
  estimateTokens,
  isPdf,
  isScanned,
  joinPages,
  rejectDocument,
} from "./documents";
import type { DocumentRef } from "./types";

const doc = (over: Partial<DocumentRef> = {}): DocumentRef => ({
  id: "d",
  name: "report.pdf",
  bytes: 3000,
  pages: 12,
  textId: "t",
  chars: 33_600,
  pageImages: [],
  ...over,
});
const page = {
  id: "p",
  name: "p",
  mime: "image/jpeg",
  width: 1,
  height: 1,
  bytes: 300,
};

describe("documents", () => {
  it("accepts PDFs up to the size limit", () => {
    expect(rejectDocument({ type: "application/pdf", size: 1024 })).toBeNull();
    expect(rejectDocument({ type: "text/plain", size: 1 })).toBe("Only PDF documents can be attached.");
    expect(
      rejectDocument({
        type: "application/pdf",
        size: 30 * 1024 * 1024 + 1,
      }),
    ).toBe("PDFs can be up to 30 MB.");
  });

  it("treats a typeless .pdf file as a PDF", () => {
    expect(isPdf({ type: "", name: "Scan.PDF" })).toBe(true);
    expect(isPdf({ type: "", name: "notes.txt" })).toBe(false);
    expect(isPdf({ type: "application/pdf" })).toBe(true);
  });

  it("calls a PDF with next to no text a scan", () => {
    expect(isScanned(joinPages(["", " "]), 2)).toBe(true);
    expect(isScanned(joinPages(["Invoice 7, total 54.00 USD due today"]), 1)).toBe(false);
    expect(isScanned("", 0)).toBe(false);
  });

  it("marks each page so a reply can cite it", () => {
    expect(joinPages([" one ", "two"])).toBe("[Page 1]\none\n\n[Page 2]\ntwo");
  });

  it("wraps the text with a name that cannot break the tag", () => {
    expect(documentBlock({ name: 'a "b" <c>.pdf', pages: 2 }, "x")).toBe(
      '<document name="a b c.pdf" pages="2">\nx\n</document>',
    );
  });

  it("picks what a model gets: the file, the text, page images or nothing", () => {
    const text = { documents: false, vision: false };
    expect(documentMode(doc(), { documents: true, vision: false })).toBe("file");
    expect(documentMode(doc(), text)).toBe("text");
    const scan = doc({ textId: null, pageImages: [page] });
    expect(documentMode(scan, { documents: false, vision: true })).toBe("pages");
    expect(documentMode(scan, text)).toBe("unreadable");
    expect(
      documentMode(doc({ textId: null }), {
        documents: false,
        vision: true,
      }),
    ).toBe("unreadable");
  });

  it("sizes each mode as it goes into the request body", () => {
    expect(documentBytes(doc(), "file")).toBe("data:application/pdf;base64,".length + 4000);
    expect(documentBytes(doc({ chars: 1000 }), "text")).toBe(1164);
    expect(documentBytes(doc({ pageImages: [page, page] }), "pages")).toBe(
      2 * ("data:image/jpeg;base64,".length + 400),
    );
    expect(documentBytes(doc(), "unreadable")).toBe(0);
  });

  it("summarises pages and tokens, or what became of a scan", () => {
    expect(estimateTokens(33_600)).toBe(8400);
    expect(documentSummary(doc())).toBe("12 pages · ~8,400 tokens");
    expect(documentSummary(doc({ pages: 1, chars: 40 }))).toBe("1 page · ~10 tokens");
    expect(documentSummary(doc({ textId: null, pageImages: [page, page] }))).toBe("12 pages · scan, first 2 as images");
    expect(documentSummary(doc({ textId: null, pages: 2, pageImages: [page, page] }))).toBe(
      "2 pages · scan, sent as images",
    );
    expect(documentSummary(doc({ textId: null }))).toBe("12 pages · scanned, no text");
  });
});
