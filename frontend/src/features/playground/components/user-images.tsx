import { ImageOff } from "lucide-react";
import { useState } from "react";
import { Modal } from "@/components/ui";
import type { ImageRef } from "../types";
import { useImageUrl } from "../use-image-url";

function Thumbnail({ image, label, onOpen }: { image: ImageRef; label: string; onOpen: (url: string) => void }) {
  const { url, missing } = useImageUrl(image.id);
  if (missing) {
    return (
      <div
        role="img"
        aria-label={`${label}, not saved in this browser`}
        className="flex size-24 flex-col items-center justify-center gap-1 rounded-lg border border-dashed p-2 text-center text-xs text-fg-muted sm:size-32"
      >
        <ImageOff aria-hidden className="size-4" />
        Not saved in this browser
      </div>
    );
  }

  return (
    <button
      type="button"
      onClick={() => url && onOpen(url)}
      aria-label={`Open ${label}`}
      className="size-24 overflow-hidden rounded-lg border bg-subtle transition-opacity hover:opacity-90 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-canvas focus-visible:outline-hidden sm:size-32"
    >
      {url && <img src={url} alt="" width={image.width} height={image.height} className="size-full object-cover" />}
    </button>
  );
}

/** Images a member attached to a message; each opens full size. */
export default function UserImages({ images }: { images: ImageRef[] }) {
  const [open, setOpen] = useState<{ url: string; index: number } | null>(null);
  const label = (i: number) =>
    images.length > 1 ? `image ${i + 1} of ${images.length}: ${images[i].name}` : `image: ${images[i].name}`;

  return (
    <>
      <ul aria-label="Attached images" className="flex flex-wrap justify-end gap-2">
        {images.map((image, i) => (
          <li key={image.id}>
            <Thumbnail image={image} label={label(i)} onOpen={(url) => setOpen({ url, index: i })} />
          </li>
        ))}
      </ul>
      <Modal
        open={open !== null}
        onClose={() => setOpen(null)}
        title={open ? images[open.index].name : ""}
        subtitle="Full size. Press Escape to close."
        maxWidth="sm:max-w-4xl"
      >
        {open && (
          <div className="flex justify-center p-2">
            <img src={open.url} alt={images[open.index].name} className="max-h-[75svh] w-auto rounded-lg object-contain" />
          </div>
        )}
      </Modal>
    </>
  );
}
