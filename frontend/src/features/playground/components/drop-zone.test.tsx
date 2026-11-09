import { createRef } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import DropZone from "./drop-zone";

describe("DropZone", () => {
  it("renders nothing on the server, where there is no frame to measure", () => {
    const html = renderToStaticMarkup(
      <DropZone
        tone="accept"
        count={2}
        title="Drop to attach 2 images"
        detail={null}
        meta={null}
        column={createRef()}
        toolbar={createRef()}
      />,
    );

    expect(html).toBe("");
  });
});
