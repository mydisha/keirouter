import { documentBlock, documentMode } from "./documents";
import type { ChatMessage, DocumentRef, ImageRef } from "./types";

type ContentPart =
  | { type: "text"; text: string }
  | { type: "image_url"; image_url: { url: string } }
  | { type: "file"; file: { filename: string; file_data: string } };

export type ApiMessage = {
  role: "system" | "user" | "assistant";
  content: string | ContentPart[];
};

type Reader = {
  vision: boolean;
  documents: boolean;
  /** A stored attachment as a `data:` URL, or null once it is gone. */
  dataUrl: (id: string) => Promise<string | null>;
  /** A stored document's extracted text, or null once it is gone. */
  text: (id: string) => Promise<string | null>;
};

async function documentParts(doc: DocumentRef, reader: Reader): Promise<ContentPart[]> {
  const gone: ContentPart[] = [
    {
      type: "text",
      text: `[${doc.name} is no longer in this browser and was not sent]`,
    },
  ];
  switch (documentMode(doc, reader)) {
    case "file": {
      const url = await reader.dataUrl(doc.id);

      return url
        ? [
            {
              type: "file",
              file: { filename: doc.name, file_data: url },
            },
          ]
        : gone;
    }
    case "text": {
      const text = doc.textId ? await reader.text(doc.textId) : null;

      return text === null ? gone : [{ type: "text", text: documentBlock(doc, text) }];
    }
    case "pages": {
      const urls = await imageUrls(doc.pageImages, reader);

      return [
        {
          type: "text",
          text: `[${doc.name}: a scanned PDF of ${doc.pages} pages; the first ${urls.length} follow as images]`,
        },
        ...urls.map((url) => ({
          type: "image_url" as const,
          image_url: { url },
        })),
      ];
    }
    case "unreadable":
      return [
        {
          type: "text",
          text: `[${doc.name} is a scanned PDF without text and was not sent]`,
        },
      ];
  }
}

async function imageUrls(images: ImageRef[], reader: Reader) {
  return (await Promise.all(images.map((i) => reader.dataUrl(i.id)))).filter((url): url is string => url !== null);
}

/**
 * History sent upstream: finished turns only, without reasoning. Documents
 * come first in a turn, then its text, then its images: models answer better
 * with the material ahead of the question. Images go as OpenAI `image_url`
 * parts with `data:` URLs, and only when the model reads them; an attachment
 * no longer in this browser is left out.
 */
export async function apiMessages(system: string, messages: ChatMessage[], reader: Reader): Promise<ApiMessage[]> {
  const out: ApiMessage[] = system.trim() ? [{ role: "system", content: system.trim() }] : [];
  for (const m of messages) {
    if (m.role === "assistant") {
      if (m.status !== "error" && m.content.trim() !== "") {
        out.push({ role: "assistant", content: m.content });
      }
      continue;
    }
    const docs = (await Promise.all((m.documents ?? []).map((d) => documentParts(d, reader)))).flat();
    const urls = reader.vision ? await imageUrls(m.images ?? [], reader) : [];
    if (docs.length === 0 && urls.length === 0) {
      // A message that was only images still needs a turn here.
      out.push({ role: "user", content: m.content || "(image)" });
      continue;
    }
    out.push({
      role: "user",
      content: [
        ...docs,
        ...(m.content ? [{ type: "text" as const, text: m.content }] : []),
        ...urls.map((url) => ({
          type: "image_url" as const,
          image_url: { url },
        })),
      ],
    });
  }

  return out;
}

/** Every image a chat would send with its next turn. */
export function chatImages(messages: ChatMessage[]): ImageRef[] {
  return messages.flatMap((m) => (m.role === "user" ? (m.images ?? []) : []));
}

/** Every document in a chat; each turn resends them all. */
export function chatDocuments(messages: ChatMessage[]): DocumentRef[] {
  return messages.flatMap((m) => (m.role === "user" ? (m.documents ?? []) : []));
}

/** Store ids a chat references: images, and each document's file, text and pages. */
export function attachmentIds(messages: ChatMessage[]): string[] {
  return [
    ...chatImages(messages).map((i) => i.id),
    ...chatDocuments(messages).flatMap((d) => [
      d.id,
      ...(d.textId ? [d.textId] : []),
      ...d.pageImages.map((i) => i.id),
    ]),
  ];
}
