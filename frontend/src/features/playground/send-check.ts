import { formatTokens } from "@/lib/format";
import { chatDocuments, chatImages } from "./api-messages";
import { documentBytes, documentMode, estimateTokens } from "./documents";
import { REQUEST_IMAGE_BUDGET, formatBytes, totalEncodedBytes } from "./images";
import { schemaError } from "./output-format";
import type { OutputFormat } from "./output-format";
import type { ChatMessage, DocumentRef, ImageRef, PlaygroundModel } from "./types";

/** Share of the context window the documents may take, leaving room for the reply. */
const CONTEXT_SHARE = 0.85;

/**
 * Why the composer refuses to send (`blocked`) or what it should point out
 * (`notice`), from the chosen model and everything the next request carries:
 * every earlier turn's attachments plus the ones waiting in the composer.
 */
export function sendCheck({
  model,
  gatewayReachable,
  sent,
  images,
  documents,
  output = { mode: "text" },
}: {
  model: Pick<PlaygroundModel, "name" | "vision" | "pdf" | "context_window"> | null;
  gatewayReachable: boolean;
  sent: ChatMessage[];
  images: ImageRef[];
  documents: DocumentRef[];
  output?: OutputFormat;
}): { blocked: string | null; notice: string | null } {
  const invalid = schemaError(output);
  if (invalid) {
    return {
      blocked: `Fix the output schema first: ${invalid}`,
      notice: null,
    };
  }
  const name = model?.name ?? "This model";
  const reads = {
    vision: model?.vision ?? false,
    documents: model?.pdf ?? false,
  };
  const noImages = gatewayReachable ? `${name} can't read images.` : "Images are off until the gateway answers again.";

  if (!reads.vision && images.length > 0) {
    return {
      blocked: gatewayReachable
        ? `${noImages} Remove them or pick a model that can.`
        : `${noImages} Remove them or try again in a minute.`,
      notice: null,
    };
  }
  const scan = documents.find((d) => documentMode(d, reads) === "unreadable");
  if (scan) {
    return {
      blocked: `${scan.name} is a scanned PDF without text. Pick a model that reads images or PDFs, or remove it.`,
      notice: null,
    };
  }

  const sentImages = chatImages(sent);
  const allDocuments = [...chatDocuments(sent), ...documents];
  const bytes =
    (reads.vision ? totalEncodedBytes([...sentImages, ...images]) : 0) +
    allDocuments.reduce((sum, d) => sum + documentBytes(d, documentMode(d, reads)), 0);
  if (bytes > REQUEST_IMAGE_BUDGET) {
    return {
      blocked: `This chat carries ${formatBytes(bytes)} of attachments and a request can carry ${formatBytes(REQUEST_IMAGE_BUDGET)}. Remove an attachment or start a new chat.`,
      notice: null,
    };
  }

  const tokens = allDocuments
    .filter((d) => documentMode(d, reads) === "text")
    .reduce((sum, d) => sum + estimateTokens(d.chars), 0);
  const context = model?.context_window || null;
  if (context !== null && tokens > context * CONTEXT_SHARE) {
    return {
      blocked: `This chat's PDFs come to about ${formatTokens(tokens)} tokens and ${name} reads up to ${formatTokens(context)}. Remove a PDF, start a new chat, or pick a model with a longer context.`,
      notice: null,
    };
  }

  if (gatewayReachable && !reads.vision && sentImages.length > 0) {
    return {
      blocked: null,
      notice: `${name} can't read images, so this chat's images are not sent to it.`,
    };
  }

  return { blocked: null, notice: null };
}
