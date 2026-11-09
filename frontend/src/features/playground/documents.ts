import { formatTokens } from "@/lib/format";
import type { DocumentRef } from "./types";

export const PDF_TYPE = "application/pdf";
export const MAX_DOCUMENTS_PER_MESSAGE = 5;
export const MAX_PDF_BYTES = 30 * 1024 * 1024;
/** Text past this many pages is left out; a reply rarely needs more. */
export const MAX_PDF_PAGES = 300;
/** A scanned PDF goes to a vision model as images of its first pages. */
export const MAX_SCANNED_PAGES = 10;
/** Below this many characters a page on average, the PDF is a scan. */
const MIN_CHARS_PER_PAGE = 25;

/** Why a picked or dropped file cannot be attached as a PDF, or null when it can. */
export function rejectDocument(file: { type: string; size: number }): string | null {
  if (file.type !== PDF_TYPE) {
    return "Only PDF documents can be attached.";
  }
  if (file.size > MAX_PDF_BYTES) {
    return `PDFs can be up to ${MAX_PDF_BYTES / 1024 / 1024} MB.`;
  }

  return null;
}

export function isPdf(file: { type: string; name?: string }): boolean {
  return file.type === PDF_TYPE || (file.type === "" && /\.pdf$/i.test(file.name ?? ""));
}

/** A scan has a page count but next to no text layer. */
export function isScanned(text: string, pages: number): boolean {
  return pages > 0 && text.replace(/\s+/g, "").length < pages * MIN_CHARS_PER_PAGE;
}

/** Rough count for warnings and budgets: about four characters a token. */
export function estimateTokens(chars: number): number {
  return Math.ceil(chars / 4);
}

/** Page text joined with page markers, so a reply can cite "page 3". */
export function joinPages(pages: string[]): string {
  return pages.map((text, i) => `[Page ${i + 1}]\n${text.trim()}`).join("\n\n");
}

/** How the document goes to a model that cannot read the PDF file itself. */
export function documentBlock(doc: Pick<DocumentRef, "name" | "pages">, text: string): string {
  const name = doc.name.replace(/["<>]/g, "");

  return `<document name="${name}" pages="${doc.pages}">\n${text}\n</document>`;
}

/**
 * What the next request carries for a document: the PDF itself, its text,
 * images of its pages, or nothing the model can read.
 */
export type DocumentMode = "file" | "text" | "pages" | "unreadable";

export function documentMode(
  doc: Pick<DocumentRef, "textId" | "pageImages">,
  model: { documents: boolean; vision: boolean },
): DocumentMode {
  if (model.documents) {
    return "file";
  }
  if (doc.textId !== null) {
    return "text";
  }

  return model.vision && doc.pageImages.length > 0 ? "pages" : "unreadable";
}

/** Bytes a document adds to a request body in the given mode. */
export function documentBytes(doc: DocumentRef, mode: DocumentMode): number {
  switch (mode) {
    case "file":
      return `data:${PDF_TYPE};base64,`.length + Math.ceil(doc.bytes / 3) * 4;
    case "text":
      // JSON escapes newlines and quotes; leave a little room for them.
      return Math.ceil(doc.chars * 1.1) + 64;
    case "pages":
      return doc.pageImages.reduce((sum, i) => sum + `data:${i.mime};base64,`.length + Math.ceil(i.bytes / 3) * 4, 0);
    case "unreadable":
      return 0;
  }
}

/** One line under the attachment: size of the text, or what a scan becomes. */
export function documentSummary(doc: DocumentRef): string {
  const pages = `${doc.pages} ${doc.pages === 1 ? "page" : "pages"}`;
  if (doc.textId !== null) {
    return `${pages} · ~${formatTokens(estimateTokens(doc.chars))} tokens`;
  }

  if (doc.pageImages.length === 0) {
    return `${pages} · scanned, no text`;
  }

  return doc.pageImages.length < doc.pages
    ? `${pages} · scan, first ${doc.pageImages.length} as images`
    : `${pages} · scan, sent as images`;
}
