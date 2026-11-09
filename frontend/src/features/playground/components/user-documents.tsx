import { documentSummary } from "../documents";
import type { DocumentRef } from "../types";
import { useImageUrl } from "../use-image-url";
import DocumentChip from "./document-chip";

function SentDocument({ doc }: { doc: DocumentRef }) {
  const { url, missing } = useImageUrl(doc.id);
  const chip = (
    <DocumentChip
      name={doc.name}
      detail={missing ? "Not saved in this browser" : documentSummary(doc)}
      className="pr-2.5"
    />
  );

  return url ? (
    <a
      href={url}
      target="_blank"
      rel="noopener"
      aria-label={`Open ${doc.name}`}
      className="rounded-md transition-opacity hover:opacity-90 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-canvas focus-visible:outline-hidden"
    >
      {chip}
    </a>
  ) : (
    chip
  );
}

/** PDFs sent with a message; each opens in a new tab from this browser's copy. */
export default function UserDocuments({ documents }: { documents: DocumentRef[] }) {
  return (
    <ul aria-label="Attached PDFs" className="flex max-w-[85%] flex-wrap justify-end gap-2">
      {documents.map((d) => (
        <li key={d.id}>
          <SentDocument doc={d} />
        </li>
      ))}
    </ul>
  );
}
