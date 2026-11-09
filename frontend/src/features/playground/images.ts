import type { ImageRef } from "./types";

export const ACCEPTED_IMAGE_TYPES = ["image/png", "image/jpeg", "image/webp", "image/gif"] as const;
export const MAX_IMAGES_PER_MESSAGE = 10;
export const MAX_SOURCE_BYTES = 20 * 1024 * 1024;
/** Providers downscale past this anyway (OpenAI high detail: 2048 px). */
export const MAX_EDGE = 2048;
/** Smaller files go up untouched when they need no resize. */
export const KEEP_BYTES = 1024 * 1024;
const JPEG_QUALITY = 0.85;
/**
 * Every turn resends the whole chat, images included. Providers reject large
 * bodies well before the gateway's own cap, so images stay under 12 MiB.
 */
export const REQUEST_IMAGE_BUDGET = 12 * 1024 * 1024;

export type SourceMeta = { type: string; size: number };

/** Why a picked or dropped file cannot be attached, or null when it can. */
export function rejectReason(file: SourceMeta): string | null {
  if (!(ACCEPTED_IMAGE_TYPES as readonly string[]).includes(file.type)) {
    return "Only PNG, JPEG, WebP and GIF images can be attached.";
  }
  if (file.size > MAX_SOURCE_BYTES) {
    return `This image is over ${formatBytes(MAX_SOURCE_BYTES)}. Pick a smaller one.`;
  }

  return null;
}

export function fitWithin(width: number, height: number, maxEdge = MAX_EDGE): { width: number; height: number } {
  const longest = Math.max(width, height);
  if (longest <= maxEdge) {
    return { width, height };
  }
  const scale = maxEdge / longest;

  return {
    width: Math.max(1, Math.round(width * scale)),
    height: Math.max(1, Math.round(height * scale)),
  };
}

export type EncodePlan =
  | { keep: true }
  | {
      keep: false;
      width: number;
      height: number;
      type: "image/png" | "image/jpeg";
    };

/**
 * Small PNG, JPEG and GIF files go up as they are. Everything else is drawn
 * again within MAX_EDGE: PNG stays PNG (it may carry transparency or text),
 * the rest becomes JPEG. WebP is always re-encoded, since not every provider
 * accepts it.
 */
export function encodePlan(file: SourceMeta, width: number, height: number): EncodePlan {
  const fits = Math.max(width, height) <= MAX_EDGE;
  if (fits && file.size <= KEEP_BYTES && file.type !== "image/webp") {
    return { keep: true };
  }

  return {
    keep: false,
    ...fitWithin(width, height),
    type: file.type === "image/png" ? "image/png" : "image/jpeg",
  };
}

/** Bytes an image adds to a request body as a base64 data URL. */
export function encodedBytes(image: Pick<ImageRef, "bytes" | "mime">): number {
  return `data:${image.mime};base64,`.length + Math.ceil(image.bytes / 3) * 4;
}

export function totalEncodedBytes(images: Pick<ImageRef, "bytes" | "mime">[]): number {
  return images.reduce((sum, image) => sum + encodedBytes(image), 0);
}

export function formatBytes(bytes: number): string {
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${Math.round(bytes / 1024)} KB`;
  }
  const mb = bytes / (1024 * 1024);

  return `${mb < 10 ? mb.toFixed(1) : Math.round(mb)} MB`;
}

export function canvasBlob(canvas: HTMLCanvasElement, type: string, quality?: number): Promise<Blob> {
  return new Promise((resolve, reject) =>
    canvas.toBlob((blob) => (blob ? resolve(blob) : reject(new Error("encode failed"))), type, quality),
  );
}

/**
 * The file as it will be sent: within MAX_EDGE and, once re-encoded, never
 * larger than needed. Throws when the browser cannot decode it.
 */
export async function prepareImage(file: File): Promise<{ blob: Blob; width: number; height: number }> {
  const bitmap = await createImageBitmap(file);
  try {
    const plan = encodePlan(file, bitmap.width, bitmap.height);
    if (plan.keep) {
      return { blob: file, width: bitmap.width, height: bitmap.height };
    }
    const canvas = document.createElement("canvas");
    canvas.width = plan.width;
    canvas.height = plan.height;
    const ctx = canvas.getContext("2d");
    if (!ctx) {
      throw new Error("no 2d context");
    }
    const draw = (background: string | null) => {
      ctx.clearRect(0, 0, plan.width, plan.height);
      if (background) {
        ctx.fillStyle = background;
        ctx.fillRect(0, 0, plan.width, plan.height);
      }
      ctx.drawImage(bitmap, 0, 0, plan.width, plan.height);
    };

    if (plan.type === "image/png") {
      draw(null);
      const png = await canvasBlob(canvas, "image/png");
      if (png.size <= KEEP_BYTES) {
        return { blob: png, width: plan.width, height: plan.height };
      }
    }
    // JPEG has no alpha: transparent pixels would turn black.
    draw("#ffffff");
    const jpeg = await canvasBlob(canvas, "image/jpeg", JPEG_QUALITY);

    return { blob: jpeg, width: plan.width, height: plan.height };
  } finally {
    bitmap.close();
  }
}
