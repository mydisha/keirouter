import type { ChatMessage, PlaygroundModel } from "../types";
import AssistantMessage from "./assistant-message";
import UserDocuments from "./user-documents";
import UserImages from "./user-images";

export default function MessageList({
  messages,
  models,
  streaming,
  onRegenerate,
}: {
  messages: ChatMessage[];
  models: PlaygroundModel[];
  streaming: boolean;
  onRegenerate: () => void;
}) {
  return (
    <ol aria-label="Messages" className="flex flex-col gap-8">
      {messages.map((m, i) => (
        <li key={m.id}>
          {m.role === "user" ? (
            <div className="flex flex-col items-end gap-2">
              <span className="sr-only">You:</span>
              {m.documents && m.documents.length > 0 && <UserDocuments documents={m.documents} />}
              {m.images && m.images.length > 0 && <UserImages images={m.images} />}
              {m.content && (
                <p className="max-w-[85%] rounded-xl bg-subtle px-4 py-2.5 text-body-lg break-words whitespace-pre-wrap">
                  {m.content}
                </p>
              )}
            </div>
          ) : (
            <AssistantMessage
              message={m}
              models={models}
              isLast={i === messages.length - 1}
              canRegenerate={!streaming}
              onRegenerate={onRegenerate}
            />
          )}
        </li>
      ))}
    </ol>
  );
}
