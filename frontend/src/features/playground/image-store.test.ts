import { describe, expect, it } from "vitest";
import { PRUNE_GRACE_MS, imageBlob, imageDataUrl, pruneImages, putImage } from "./image-store";

// Node has no IndexedDB: these run on the in-memory fallback that private
// windows also get.
describe("image store without IndexedDB", () => {
  it("keeps images for this tab and reads them back as data URLs", async () => {
    await putImage("1.acme", "kept", new Blob(["abc"], { type: "image/png" }));

    expect(await (await imageBlob("kept"))?.text()).toBe("abc");
    expect(await imageDataUrl("kept")).toBe("data:image/png;base64,YWJj");
    expect(await imageDataUrl("never-stored")).toBeNull();
  });

  it("prunes only stale, unreferenced images of the same scope", async () => {
    const blob = new Blob(["x"], { type: "image/png" });
    await putImage("1.acme", "orphan", blob);
    await putImage("1.acme", "used", blob);
    await putImage("2.acme", "other-scope", blob);
    await putImage("1.acme", "fresh", blob);

    await pruneImages("1.acme", new Set(["used"]), Date.now() + PRUNE_GRACE_MS + 1);
    expect(await imageBlob("orphan")).toBeNull();
    expect(await imageBlob("used")).not.toBeNull();
    expect(await imageBlob("other-scope")).not.toBeNull();

    await putImage("1.acme", "just-attached", blob);
    await pruneImages("1.acme", new Set());
    expect(await imageBlob("just-attached")).not.toBeNull();
  });
});
