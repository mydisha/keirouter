import { describe, expect, it } from "vitest";
import { describeDrag, dropCopy, dropMeta, switchHint } from "./drag-files";

const file = (type: string) => ({ kind: "file", type });
const room = { images: 10, documents: 5, vision: true };

describe("describeDrag", () => {
  it("counts files, images and PDFs among them", () => {
    expect(
      describeDrag([
        file("image/png"),
        file("image/webp"),
        file("application/pdf"),
        file("application/zip"),
        { kind: "string", type: "text/uri-list" },
      ]),
    ).toEqual({ files: 4, images: 2, pdfs: 1 });
  });

  it("does not count image types the playground cannot attach", () => {
    expect(describeDrag([file("image/svg+xml")])).toEqual({
      files: 1,
      images: 0,
      pdfs: 0,
    });
  });

  it("is unknown when the browser hides the items or their types", () => {
    expect(describeDrag(undefined)).toEqual({ files: null });
    expect(describeDrag([])).toEqual({ files: null });
    expect(describeDrag([file("image/png"), file("")])).toEqual({
      files: null,
    });
  });
});

describe("dropCopy", () => {
  it("names what a drop attaches", () => {
    expect(dropCopy({ files: 1, images: 1, pdfs: 0 }, room)).toEqual({
      title: "Drop to attach 1 image",
      detail: null,
      count: 1,
    });
    expect(dropCopy({ files: 3, images: 2, pdfs: 1 }, room)).toEqual({
      title: "Drop to attach 2 images and 1 PDF",
      detail: null,
      count: 3,
    });
  });

  it("stays generic when the count is unknown", () => {
    expect(dropCopy({ files: null }, room)).toEqual({
      title: "Drop to attach",
      detail: null,
      count: 1,
    });
  });

  it("takes PDFs for a model that cannot read images, and says images are skipped", () => {
    expect(dropCopy({ files: 2, images: 1, pdfs: 1 }, { ...room, vision: false })).toEqual({
      title: "Drop to attach 1 PDF",
      detail: "1 image will be skipped: this model can't read images.",
      count: 1,
    });
  });

  it("hands an images-only drop on a text model back to the page", () => {
    expect(dropCopy({ files: 2, images: 2, pdfs: 0 }, { ...room, vision: false })).toBe("needs-vision");
  });

  it("says what is left out over the limits and what is not attachable", () => {
    const copy = dropCopy({ files: 7, images: 4, pdfs: 2 }, { images: 2, documents: 1, vision: true });

    expect(copy).toEqual({
      title: "Drop to attach 2 images and 1 PDF",
      detail:
        "2 images over the 10-image limit will be left out. 1 PDF over the 5-PDF limit will be left out. 1 file is not an image or PDF and will be skipped.",
      count: 3,
    });
  });

  it("refuses a drop with nothing attachable or no room", () => {
    expect(dropCopy({ files: 2, images: 0, pdfs: 0 }, room)).toMatchObject({
      title: "These files can't be attached",
      count: 0,
    });
    expect(dropCopy({ files: 1, images: 0, pdfs: 1 }, { images: 10, documents: 0, vision: true })).toEqual({
      title: "This message is full",
      detail: "A message holds 10 images and 5 PDFs. Remove one to add another.",
      count: 0,
    });
  });
});

describe("dropMeta", () => {
  it("describes images only where the model reads them", () => {
    expect(dropMeta(true)).toBe("Images resized to 2048 px · PDF up to 30 MB, read in your browser");
    expect(dropMeta(false)).toBe("PDF up to 30 MB, read in your browser · images need a model that reads them");
  });
});

describe("switchHint", () => {
  it("names up to two models that read images", () => {
    expect(switchHint(["Claude Sonnet 5.5"])).toBe(
      "Switch to a model that can: Claude Sonnet 5.5. Then drop again. PDFs work with every model.",
    );
    expect(switchHint(["A", "B", "C"])).toBe(
      "Switch to a model that can, for example: A or B. Then drop again. PDFs work with every model.",
    );
  });

  it("says so when no listed model reads images", () => {
    expect(switchHint([])).toBe("None of the models here read images yet. PDFs work with every model.");
  });
});
