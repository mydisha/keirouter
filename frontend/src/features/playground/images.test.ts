import { describe, expect, it } from "vitest";
import {
  KEEP_BYTES,
  MAX_SOURCE_BYTES,
  REQUEST_IMAGE_BUDGET,
  encodePlan,
  encodedBytes,
  fitWithin,
  formatBytes,
  rejectReason,
  totalEncodedBytes,
} from "./images";

describe("rejectReason", () => {
  it("accepts the four formats every vision provider reads", () => {
    for (const type of ["image/png", "image/jpeg", "image/webp", "image/gif"]) {
      expect(rejectReason({ type, size: 1 })).toBeNull();
    }
  });

  it("refuses SVG, HEIC, non-images and empty types", () => {
    for (const type of ["image/svg+xml", "image/heic", "application/pdf", ""]) {
      expect(rejectReason({ type, size: 1 })).toMatch(/PNG, JPEG, WebP and GIF/);
    }
  });

  it("refuses a file over the source limit, not one at it", () => {
    expect(rejectReason({ type: "image/png", size: MAX_SOURCE_BYTES })).toBeNull();
    expect(rejectReason({ type: "image/png", size: MAX_SOURCE_BYTES + 1 })).toBe(
      "This image is over 20 MB. Pick a smaller one.",
    );
  });
});

describe("fitWithin", () => {
  it("leaves an image within the edge alone", () => {
    expect(fitWithin(2048, 1000)).toEqual({ width: 2048, height: 1000 });
  });

  it("scales the longest edge down and keeps the ratio", () => {
    expect(fitWithin(4096, 3072)).toEqual({ width: 2048, height: 1536 });
    expect(fitWithin(1000, 8000)).toEqual({ width: 256, height: 2048 });
  });

  it("never collapses a side to zero", () => {
    expect(fitWithin(100_000, 10)).toEqual({ width: 2048, height: 1 });
  });
});

describe("encodePlan", () => {
  it("sends small PNG, JPEG and GIF files as they are", () => {
    for (const type of ["image/png", "image/jpeg", "image/gif"]) {
      expect(encodePlan({ type, size: KEEP_BYTES }, 800, 600)).toEqual({
        keep: true,
      });
    }
  });

  it("re-encodes WebP even when small, as JPEG", () => {
    expect(encodePlan({ type: "image/webp", size: 10 }, 800, 600)).toEqual({
      keep: false,
      width: 800,
      height: 600,
      type: "image/jpeg",
    });
  });

  it("keeps PNG as PNG and resizes when too large", () => {
    expect(encodePlan({ type: "image/png", size: 10 }, 4000, 2000)).toEqual({
      keep: false,
      width: 2048,
      height: 1024,
      type: "image/png",
    });
  });

  it("re-encodes a heavy JPEG or GIF within the edge as JPEG", () => {
    for (const type of ["image/jpeg", "image/gif"]) {
      expect(encodePlan({ type, size: KEEP_BYTES + 1 }, 1200, 900)).toEqual({
        keep: false,
        width: 1200,
        height: 900,
        type: "image/jpeg",
      });
    }
  });
});

describe("request size", () => {
  it("counts the data URL prefix and base64 growth", () => {
    expect(encodedBytes({ mime: "image/png", bytes: 3 })).toBe("data:image/png;base64,".length + 4);
    expect(encodedBytes({ mime: "image/png", bytes: 4 })).toBe("data:image/png;base64,".length + 8);
    expect(totalEncodedBytes([])).toBe(0);
  });

  it("fits about eight megabytes of images in the budget", () => {
    const image = { mime: "image/jpeg", bytes: 1024 * 1024 };
    expect(totalEncodedBytes(Array(8).fill(image))).toBeLessThanOrEqual(REQUEST_IMAGE_BUDGET);
    expect(totalEncodedBytes(Array(10).fill(image))).toBeGreaterThan(REQUEST_IMAGE_BUDGET);
  });

  it("formats sizes for people", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1536)).toBe("2 KB");
    expect(formatBytes(1.5 * 1024 * 1024)).toBe("1.5 MB");
    expect(formatBytes(12 * 1024 * 1024)).toBe("12 MB");
  });
});
