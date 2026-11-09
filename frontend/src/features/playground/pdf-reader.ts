import { canvasBlob, fitWithin } from "./images";
import { MAX_PDF_PAGES, MAX_SCANNED_PAGES, isScanned, joinPages } from "./documents";

/** Where pdf.js finds its data files: emitted by vite.config.ts in a build. */
const ASSETS = import.meta.env.DEV
  ? new URL("/node_modules/pdfjs-dist/", import.meta.url).href
  : new URL("../pdfjs/", import.meta.url).href;

/** Scanned pages are read like photos of paper: legible well under 2048 px. */
const PAGE_EDGE = 1600;
const PAGE_QUALITY = 0.8;

export type ReadPdf = {
  pages: number;
  /** Null when the PDF has no text layer worth sending. */
  text: string | null;
  /** Only for a scan: its first pages as JPEG. */
  pageImages: { blob: Blob; width: number; height: number }[];
};

/**
 * Reads a PDF in the browser with pdf.js, loaded on first use. Throws when the
 * file is not a readable PDF (damaged, or locked with a password).
 */
export async function readPdf(file: Blob, onProgress: (done: number, total: number) => void): Promise<ReadPdf> {
  const [pdfjs, { default: PdfWorker }] = await Promise.all([
    import("pdfjs-dist"),
    // Bundled as a plain .js worker: some servers send .mjs as a download.
    import("pdfjs-dist/build/pdf.worker.min.mjs?worker"),
  ]);
  pdfjs.GlobalWorkerOptions.workerPort ??= new PdfWorker();

  const task = pdfjs.getDocument({
    data: await file.arrayBuffer(),
    cMapUrl: `${ASSETS}cmaps/`,
    standardFontDataUrl: `${ASSETS}standard_fonts/`,
    iccUrl: `${ASSETS}iccs/`,
    wasmUrl: `${ASSETS}wasm/`,
    // The CSP does not allow WebAssembly compilation; pdf.js takes its
    // plain-JS decoders instead of logging a violation first.
    useWasm: false,
  });
  const pdf = await task.promise;
  try {
    const total = Math.min(pdf.numPages, MAX_PDF_PAGES);
    const texts: string[] = [];
    for (let n = 1; n <= total; n++) {
      const page = await pdf.getPage(n);
      const content = await page.getTextContent();
      texts.push(content.items.map((item) => ("str" in item ? item.str + (item.hasEOL ? "\n" : "") : "")).join(""));
      page.cleanup();
      onProgress(n, total);
    }
    const text = joinPages(texts);
    if (!isScanned(text, total)) {
      return { pages: pdf.numPages, text, pageImages: [] };
    }

    const pageImages: ReadPdf["pageImages"] = [];
    for (let n = 1; n <= Math.min(total, MAX_SCANNED_PAGES); n++) {
      const page = await pdf.getPage(n);
      const base = page.getViewport({ scale: 1 });
      const size = fitWithin(Math.ceil(base.width * 2), Math.ceil(base.height * 2), PAGE_EDGE);
      const viewport = page.getViewport({
        scale: size.width / base.width,
      });
      const canvas = document.createElement("canvas");
      canvas.width = Math.round(viewport.width);
      canvas.height = Math.round(viewport.height);
      await page.render({ canvas, viewport, background: "#ffffff" }).promise;
      pageImages.push({
        blob: await canvasBlob(canvas, "image/jpeg", PAGE_QUALITY),
        width: canvas.width,
        height: canvas.height,
      });
      page.cleanup();
    }

    return { pages: pdf.numPages, text: null, pageImages };
  } finally {
    void task.destroy();
  }
}
