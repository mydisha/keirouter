import { useEffect, useState } from "react";
import { imageBlob } from "./image-store";

/**
 * An object URL for a stored image, revoked when the component goes away.
 * `missing` once the store has no such image (another browser, a private
 * window after reload, or pruned).
 */
export function useImageUrl(id: string): {
  url: string | null;
  missing: boolean;
} {
  const [state, setState] = useState<{
    id: string;
    url: string | null;
    missing: boolean;
  }>({
    id,
    url: null,
    missing: false,
  });

  useEffect(() => {
    let url: string | null = null;
    let live = true;
    void imageBlob(id).then((blob) => {
      if (!live) {
        return;
      }
      url = blob ? URL.createObjectURL(blob) : null;
      setState({ id, url, missing: !blob });
    });

    return () => {
      live = false;
      if (url) {
        URL.revokeObjectURL(url);
      }
    };
  }, [id]);

  return state.id === id ? state : { url: null, missing: false };
}
