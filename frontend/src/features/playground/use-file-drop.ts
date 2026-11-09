import { useEffect, useRef, useState } from "react";
import { describeDrag } from "./drag-files";
import type { DragFiles } from "./drag-files";

const carriesFiles = (e: DragEvent) => e.dataTransfer?.types.includes("Files") ?? false;

/**
 * Files dropped anywhere on the page. Without this a dropped image would make
 * the browser leave the page to open it. Returns what the drag carries while
 * files hover, null otherwise.
 */
export function useFileDrop(onFiles: (files: FileList) => void): DragFiles | null {
  const [dragging, setDragging] = useState<DragFiles | null>(null);
  const handler = useRef(onFiles);
  handler.current = onFiles;

  useEffect(() => {
    // dragenter/dragleave fire for every child crossed; count them.
    let depth = 0;
    const enter = (e: DragEvent) => {
      if (!carriesFiles(e)) {
        return;
      }
      e.preventDefault();
      if (depth++ === 0) {
        setDragging(describeDrag(e.dataTransfer?.items));
      }
    };
    const over = (e: DragEvent) => {
      if (carriesFiles(e)) {
        e.preventDefault();
      }
    };
    const leave = (e: DragEvent) => {
      if (!carriesFiles(e)) {
        return;
      }
      depth = Math.max(0, depth - 1);
      if (depth === 0) {
        setDragging(null);
      }
    };
    const drop = (e: DragEvent) => {
      if (!carriesFiles(e)) {
        return;
      }
      e.preventDefault();
      depth = 0;
      setDragging(null);
      if (e.dataTransfer?.files.length) {
        handler.current(e.dataTransfer.files);
      }
    };
    window.addEventListener("dragenter", enter);
    window.addEventListener("dragover", over);
    window.addEventListener("dragleave", leave);
    window.addEventListener("drop", drop);

    return () => {
      window.removeEventListener("dragenter", enter);
      window.removeEventListener("dragover", over);
      window.removeEventListener("dragleave", leave);
      window.removeEventListener("drop", drop);
    };
  }, []);

  return dragging;
}
