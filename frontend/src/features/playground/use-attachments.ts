import { useCallback, useEffect, useRef, useState } from "react";
import { MAX_DOCUMENTS_PER_MESSAGE, isPdf, rejectDocument } from "./documents";
import { newId } from "./id";
import { putImage } from "./image-store";
import { MAX_IMAGES_PER_MESSAGE, prepareImage, rejectReason } from "./images";
import { readPdf } from "./pdf-reader";
import type { DocumentRef, ImageRef } from "./types";

export type Attachment =
  | {
      key: string;
      kind: "image" | "document";
      name: string;
      status: "processing";
      /** Pages read so far, for a PDF. */
      progress?: { done: number; total: number };
    }
  | {
      key: string;
      kind: "image";
      name: string;
      status: "ready";
      image: ImageRef;
      preview: string;
    }
  | {
      key: string;
      kind: "document";
      name: string;
      status: "ready";
      document: DocumentRef;
    }
  | {
      key: string;
      kind: "image" | "document";
      name: string;
      status: "error";
      error: string;
    };

const counts = (list: Attachment[], kind: Attachment["kind"]) =>
  list.filter((a) => a.kind === kind && a.status !== "error").length;

/**
 * Images and PDFs waiting in the composer. Each is prepared and stored in the
 * browser as soon as it is added (an image resized, a PDF read page by page),
 * so sending only moves references into the chat.
 */
export function useAttachments(scope: string) {
  const [items, setItems] = useState<Attachment[]>([]);
  const latest = useRef(items);
  latest.current = items;

  const update = (key: string, next: Attachment) => setItems((list) => list.map((a) => (a.key === key ? next : a)));

  const addImage = (file: File, overLimit: boolean): Attachment => {
    const key = newId();
    const name = file.name || "Pasted image";
    const reason = overLimit
      ? `You can attach up to ${MAX_IMAGES_PER_MESSAGE} images per message.`
      : rejectReason(file);
    if (reason) {
      return { key, kind: "image", name, status: "error", error: reason };
    }
    void prepareImage(file)
      .then(async ({ blob, width, height }) => {
        const id = newId();
        await putImage(scope, id, blob);
        update(key, {
          key,
          kind: "image",
          name,
          status: "ready",
          image: {
            id,
            name,
            mime: blob.type || file.type,
            width,
            height,
            bytes: blob.size,
          },
          preview: URL.createObjectURL(blob),
        });
      })
      .catch(() =>
        update(key, {
          key,
          kind: "image",
          name,
          status: "error",
          error: "This image can't be read. Try another file.",
        }),
      );

    return { key, kind: "image", name, status: "processing" };
  };

  const addDocument = (file: File, overLimit: boolean): Attachment => {
    const key = newId();
    const name = file.name || "Document.pdf";
    const reason = overLimit
      ? `You can attach up to ${MAX_DOCUMENTS_PER_MESSAGE} PDFs per message.`
      : rejectDocument({ type: "application/pdf", size: file.size });
    if (reason) {
      return {
        key,
        kind: "document",
        name,
        status: "error",
        error: reason,
      };
    }
    void readPdf(file, (done, total) =>
      update(key, {
        key,
        kind: "document",
        name,
        status: "processing",
        progress: { done, total },
      }),
    )
      .then(async (read) => {
        const id = newId();
        const textId = read.text === null ? null : newId();
        const pageImages: ImageRef[] = read.pageImages.map((p, i) => ({
          id: newId(),
          name: `${name}, page ${i + 1}`,
          mime: p.blob.type,
          width: p.width,
          height: p.height,
          bytes: p.blob.size,
        }));
        await Promise.all([
          putImage(scope, id, file.slice(0, file.size, "application/pdf")),
          textId !== null && putImage(scope, textId, new Blob([read.text ?? ""], { type: "text/plain" })),
          ...read.pageImages.map((p, i) => putImage(scope, pageImages[i].id, p.blob)),
        ]);
        update(key, {
          key,
          kind: "document",
          name,
          status: "ready",
          document: {
            id,
            name,
            bytes: file.size,
            pages: read.pages,
            textId,
            chars: read.text?.length ?? 0,
            pageImages,
          },
        });
      })
      .catch(() =>
        update(key, {
          key,
          kind: "document",
          name,
          status: "error",
          error: "This PDF can't be read. It may be damaged or locked with a password.",
        }),
      );

    return { key, kind: "document", name, status: "processing" };
  };

  /** Adds what the files hold: images where the model reads them, and PDFs. */
  const add = useCallback(
    (files: File[], { images = true }: { images?: boolean } = {}) => {
      let imageRoom = Math.max(0, MAX_IMAGES_PER_MESSAGE - counts(latest.current, "image"));
      let documentRoom = Math.max(0, MAX_DOCUMENTS_PER_MESSAGE - counts(latest.current, "document"));
      const added: Attachment[] = [];
      for (const file of files) {
        if (isPdf(file)) {
          added.push(addDocument(file, documentRoom-- <= 0));
        } else if (images && file.type.startsWith("image/")) {
          added.push(addImage(file, imageRoom-- <= 0));
        }
      }
      setItems((list) => [...list, ...added]);
    },
    [scope],
  );

  const remove = useCallback((key: string) => {
    setItems((list) => {
      const gone = list.find((a) => a.key === key);
      if (gone?.status === "ready" && gone.kind === "image") {
        URL.revokeObjectURL(gone.preview);
      }

      return list.filter((a) => a.key !== key);
    });
  }, []);

  const clear = useCallback(() => {
    setItems((list) => {
      for (const a of list) {
        if (a.status === "ready" && a.kind === "image") {
          URL.revokeObjectURL(a.preview);
        }
      }

      return [];
    });
  }, []);

  useEffect(
    () => () => {
      for (const a of latest.current) {
        if (a.status === "ready" && a.kind === "image") {
          URL.revokeObjectURL(a.preview);
        }
      }
    },
    [],
  );

  return {
    items,
    images: items.flatMap((a) => (a.status === "ready" && a.kind === "image" ? [a.image] : [])),
    documents: items.flatMap((a) => (a.status === "ready" && a.kind === "document" ? [a.document] : [])),
    processing: items.some((a) => a.status === "processing"),
    add,
    remove,
    clear,
  };
}
