import { MAX_DOCUMENTS_PER_MESSAGE, MAX_PDF_BYTES, PDF_TYPE } from "./documents";
import { ACCEPTED_IMAGE_TYPES, MAX_EDGE, MAX_IMAGES_PER_MESSAGE } from "./images";

/**
 * What a drag over the page carries, read before the drop. Browsers expose
 * only kind and MIME type until the drop (Safari may expose neither), so
 * `files` is null when the count is unknown.
 */
export type DragFiles = { files: number; images: number; pdfs: number } | { files: null };

export function describeDrag(items: ArrayLike<{ kind: string; type: string }> | null | undefined): DragFiles {
  const files = Array.from(items ?? []).filter((i) => i.kind === "file");
  if (files.length === 0 || files.some((f) => f.type === "")) {
    return { files: null };
  }

  return {
    files: files.length,
    images: files.filter((f) => (ACCEPTED_IMAGE_TYPES as readonly string[]).includes(f.type)).length,
    pdfs: files.filter((f) => f.type === PDF_TYPE).length,
  };
}

export type DropRoom = { images: number; documents: number; vision: boolean };

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

/**
 * The lines the drop zone shows, or 'needs-vision' when the drag is only
 * images and the model cannot read them.
 */
export function dropCopy(
  drag: DragFiles,
  room: DropRoom,
): { title: string; detail: string | null; count: number } | "needs-vision" {
  if (drag.files === null) {
    return { title: "Drop to attach", detail: null, count: 1 };
  }
  if (drag.images > 0 && drag.pdfs === 0 && !room.vision) {
    return "needs-vision";
  }
  if (drag.images === 0 && drag.pdfs === 0) {
    return {
      title: drag.files === 1 ? "This file can't be attached" : "These files can't be attached",
      detail: "Attach images (PNG, JPEG, WebP or GIF) or PDFs.",
      count: 0,
    };
  }

  const images = room.vision ? Math.min(drag.images, room.images) : 0;
  const pdfs = Math.min(drag.pdfs, room.documents);
  if (images + pdfs === 0) {
    return {
      title: "This message is full",
      detail: `A message holds ${MAX_IMAGES_PER_MESSAGE} images and ${MAX_DOCUMENTS_PER_MESSAGE} PDFs. Remove one to add another.`,
      count: 0,
    };
  }

  const notes: string[] = [];
  if (!room.vision && drag.images > 0) {
    notes.push(`${plural(drag.images, "image", "images")} will be skipped: this model can't read images.`);
  } else if (drag.images > images) {
    notes.push(
      `${plural(drag.images - images, "image", "images")} over the ${MAX_IMAGES_PER_MESSAGE}-image limit will be left out.`,
    );
  }
  if (drag.pdfs > pdfs) {
    notes.push(
      `${plural(drag.pdfs - pdfs, "PDF", "PDFs")} over the ${MAX_DOCUMENTS_PER_MESSAGE}-PDF limit will be left out.`,
    );
  }
  const other = drag.files - drag.images - drag.pdfs;
  if (other > 0) {
    notes.push(
      other === 1
        ? "1 file is not an image or PDF and will be skipped."
        : `${other} files are not images or PDFs and will be skipped.`,
    );
  }
  const what = [images > 0 && plural(images, "image", "images"), pdfs > 0 && plural(pdfs, "PDF", "PDFs")]
    .filter(Boolean)
    .join(" and ");

  return {
    title: `Drop to attach ${what}`,
    detail: notes.length > 0 ? notes.join(" ") : null,
    count: images + pdfs,
  };
}

/** Formats and limits in one quiet line. */
export function dropMeta(vision: boolean): string {
  const pdf = `PDF up to ${MAX_PDF_BYTES / 1024 / 1024} MB, read in your browser`;

  return vision ? `Images resized to ${MAX_EDGE} px · ${pdf}` : `${pdf} · images need a model that reads them`;
}

/** What to do when the chosen model cannot read a drop of images. */
export function switchHint(visionModels: string[]): string {
  const [a, b] = visionModels;
  if (a === undefined) {
    return "None of the models here read images yet. PDFs work with every model.";
  }
  const more = visionModels.length > 2 ? ", for example" : "";

  return `Switch to a model that can${more}: ${b === undefined ? a : `${a} or ${b}`}. Then drop again. PDFs work with every model.`;
}
