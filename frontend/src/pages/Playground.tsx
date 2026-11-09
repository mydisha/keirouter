import { useQuery } from "@tanstack/react-query";
import { History, Plus } from "lucide-react";
import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { Button, EmptyState, ErrorBanner, Skeleton } from "@/components/ui";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "@/components/ui/sheet";
import Composer, { attachableFiles } from "@/features/playground/components/composer";
import ConversationList from "@/features/playground/components/conversation-list";
import DropZone from "@/features/playground/components/drop-zone";
import ModelPicker from "@/features/playground/components/model-picker";
import OutputControl from "@/features/playground/components/output-control";
import SkillsMenu, { playgroundSkills } from "@/features/playground/components/skills-menu";
import SystemPrompt from "@/features/playground/components/system-prompt";
import ThinkingControl, { ThinkingControlSkeleton } from "@/features/playground/components/thinking-control";
import ThinkingField from "@/features/playground/components/thinking-field";
import { MAX_DOCUMENTS_PER_MESSAGE } from "@/features/playground/documents";
import { dropCopy, dropMeta, switchHint } from "@/features/playground/drag-files";
import { EFFORT_BARS, effectiveEffort } from "@/features/playground/effort";
import { MAX_IMAGES_PER_MESSAGE } from "@/features/playground/images";
import { TEXT_OUTPUT } from "@/features/playground/output-format";
import { sendCheck } from "@/features/playground/send-check";
import type { PlaygroundModel } from "@/features/playground/types";
import { useAttachments } from "@/features/playground/use-attachments";
import { useChat } from "@/features/playground/use-chat";
import { useFileDrop } from "@/features/playground/use-file-drop";
import { api } from "@/lib/api";

// Markdown, code and math rendering: only needed once a chat has messages.
const loadMessageList = () => import("@/features/playground/components/message-list");
const MessageList = lazy(loadMessageList);

const NO_MODELS: PlaygroundModel[] = [];
const STORAGE_SCOPE = "dashboard";

/** Pixels from the bottom within which new tokens keep the view pinned. */
const STICK_TO_BOTTOM = 120;

/**
 * Chat with any chain or connected model from the dashboard. Requests go
 * through the gateway's playground endpoint: the same routing, guardrails,
 * skills and metering as /v1, under the managed "Playground" key. Chats and
 * attachments stay in this browser.
 */
export function PlaygroundPage() {
  const modelsQ = useQuery({ queryKey: ["playground-models"], queryFn: () => api.playgroundModels(), staleTime: 60_000 });
  const skillsQ = useQuery({ queryKey: ["skills"], queryFn: () => api.listSkills(), staleTime: 30_000 });
  const models = modelsQ.data?.models;
  const listed = models ?? NO_MODELS;
  const modelsLoading = modelsQ.isLoading;
  const skills = skillsQ.data ? playgroundSkills(skillsQ.data.skills) : undefined;

  const [skipOptional, setSkipOptional] = useState(false);
  const chat = useChat({ storageScope: STORAGE_SCOPE, models: listed, skipOptionalSkills: skipOptional });
  const attachments = useAttachments(STORAGE_SCOPE);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [announcement, setAnnouncement] = useState("");
  const pinned = useRef(true);
  const column = useRef<HTMLDivElement>(null);
  const toolbar = useRef<HTMLDivElement>(null);
  const scroller = useRef<HTMLDivElement>(null);

  const active = chat.active;
  const model = listed.find((m) => m.id === active?.model);
  const lastMessage = active?.messages.at(-1);
  const vision = model?.vision ?? false;
  const thinking = effectiveEffort(model, active?.effort ?? "medium");
  const noImages = `${model?.name ?? "This model"} can't read images.`;
  const visionHint = `${noImages} Pick a model that can.`;
  const taken = (kind: "image" | "document") =>
    attachments.items.filter((a) => a.kind === kind && a.status !== "error").length;
  const room = {
    images: Math.max(0, MAX_IMAGES_PER_MESSAGE - taken("image")),
    documents: Math.max(0, MAX_DOCUMENTS_PER_MESSAGE - taken("document")),
    vision,
  };
  const dragging = useFileDrop((files) => {
    const usable = attachableFiles(files).filter((f) => vision || !f.type.startsWith("image/"));
    if (usable.length > 0) {
      attachments.add(usable, { images: vision });
    } else {
      setAnnouncement(visionHint);
    }
  });
  const drop = dragging ? dropCopy(dragging, room) : null;

  const { blocked, notice } = modelsLoading
    ? { blocked: null, notice: null }
    : sendCheck({
        model: model ?? null,
        gatewayReachable: true,
        sent: active?.messages ?? [],
        images: attachments.images,
        documents: attachments.documents,
        output: active?.output,
      });

  useEffect(() => {
    void loadMessageList();
  }, []);

  // The message area scrolls on its own; stay at the bottom while a reply
  // streams unless the reader scrolled up.
  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const onScroll = () => {
      pinned.current = el.scrollTop + el.clientHeight >= el.scrollHeight - STICK_TO_BOTTOM;
    };
    el.addEventListener("scroll", onScroll, { passive: true });
    return () => el.removeEventListener("scroll", onScroll);
  }, []);

  useEffect(() => {
    const el = scroller.current;
    if (el && pinned.current) el.scrollTo({ top: el.scrollHeight });
  }, [lastMessage]);

  const lastStatus = lastMessage?.role === "assistant" ? lastMessage.status : null;
  useEffect(() => {
    if (lastStatus === "done") setAnnouncement("Reply finished.");
    else if (lastStatus === "error") setAnnouncement("The reply failed.");
    else if (lastStatus === "stopped") setAnnouncement("Reply stopped.");
  }, [lastStatus, lastMessage?.id]);

  const history = (
    <ConversationList
      conversations={chat.conversations}
      activeId={active && chat.conversations.includes(active) ? active.id : null}
      onOpen={(id) => {
        chat.open(id);
        setHistoryOpen(false);
      }}
      onNew={() => {
        chat.newChat();
        setHistoryOpen(false);
      }}
      onDelete={chat.remove}
    />
  );

  return (
    <>
      <div className="flex h-full min-h-0">
        <aside className="hidden w-64 shrink-0 flex-col border-r border-line px-4 py-5 lg:flex">{history}</aside>

        <div ref={column} className="flex min-h-0 min-w-0 flex-1 flex-col">
          <div
            ref={toolbar}
            className="@container flex shrink-0 flex-wrap items-center gap-x-2 gap-y-2 border-b border-line px-3 py-2.5 md:px-5"
          >
            <Button variant="quiet" size="icon" aria-label="Chats" onClick={() => setHistoryOpen(true)} className="lg:hidden">
              <History aria-hidden />
            </Button>
            <ModelPicker
              models={models}
              value={active?.model ?? ""}
              onChange={(m) => chat.configure({ model: m })}
              disabled={!chat.loaded || listed.length === 0}
            />
            {models === undefined ? (
              <ThinkingControlSkeleton />
            ) : (
              <ThinkingControl
                model={model}
                value={active?.effort ?? "medium"}
                onChange={(effort) => chat.configure({ effort })}
              />
            )}
            <OutputControl value={active?.output ?? TEXT_OUTPUT} onChange={(output) => chat.configure({ output })} />
            <div className="ml-auto flex items-center gap-1">
              <SkillsMenu skills={skills} skipOptional={skipOptional} onSkipOptionalChange={setSkipOptional} />
              <SystemPrompt value={active?.system ?? ""} onChange={(system) => chat.configure({ system })} />
              <Button variant="quiet" size="icon" aria-label="New chat" onClick={chat.newChat} className="lg:hidden">
                <Plus aria-hidden />
              </Button>
            </div>
          </div>

          <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
            <div className="mx-auto flex min-h-full w-full max-w-3xl flex-col px-4 py-6 md:px-6 md:py-8">
              <h1 className="sr-only">Playground</h1>
              {modelsQ.isError ? (
                <ErrorBanner
                  message="Couldn't load the models. Check that the gateway is running."
                  action={
                    <Button variant="secondary" size="sm" onClick={() => void modelsQ.refetch()}>
                      Try again
                    </Button>
                  }
                />
              ) : !modelsLoading && listed.length === 0 ? (
                <EmptyState
                  variant="card"
                  className="my-auto"
                  title="No chat models to try yet"
                  hint="Connect a provider or create a chain, and its chat models show up here."
                  action={
                    <Link to="/providers" className="text-[13px] font-medium text-link underline-offset-4 hover:underline">
                      Connect a provider
                    </Link>
                  }
                />
              ) : active && active.messages.length > 0 ? (
                <Suspense fallback={<MessagesFallback />}>
                  <MessageList
                    messages={active.messages}
                    models={listed}
                    streaming={chat.streaming}
                    onRegenerate={chat.regenerate}
                  />
                </Suspense>
              ) : (
                <div className="my-auto py-10 text-center">
                  <ThinkingField
                    energy={thinking === null ? 1 / 3 : EFFORT_BARS[thinking] / 3}
                    className="mx-auto mb-6 h-36 max-w-xl text-brand-ink"
                  />
                  <div className="mx-auto max-w-prose space-y-2">
                    <p className="text-title font-medium text-balance">Try a model before you wire it into a tool</p>
                    <p className="text-[13px] text-pretty text-fg-muted">
                      Pick a model or chain, set how much it thinks and send a message. Requests run through your routing,
                      guardrails and skills, and show up in{" "}
                      <Link to="/usage" className="text-link underline-offset-4 hover:underline">
                        Usage
                      </Link>{" "}
                      and the{" "}
                      <Link to="/console" className="text-link underline-offset-4 hover:underline">
                        console
                      </Link>{" "}
                      under the Playground key.
                    </p>
                  </div>
                </div>
              )}
            </div>
          </div>

          <div className="shrink-0 px-4 pb-4 pt-2 md:px-6">
            <div className="mx-auto w-full max-w-3xl">
              {notice && <p className="mb-2 text-[13px] text-fg-muted">{notice}</p>}
              <Composer
                streaming={chat.streaming}
                disabled={!chat.loaded || (!modelsLoading && listed.length === 0)}
                waiting={modelsLoading}
                attachments={attachments}
                vision={vision}
                visionHint={visionHint}
                blocked={blocked}
                onSend={(text) => {
                  pinned.current = true;
                  chat.send(text, attachments.images, attachments.documents);
                  attachments.clear();
                }}
                onStop={chat.stop}
              />
              <p id="playground-hint" className="mt-2 text-[12px] text-fg-muted">
                Enter to send, Shift+Enter for a new line. Chats and their attachments are saved in this browser only.
              </p>
            </div>
          </div>
        </div>
      </div>

      <p aria-live="polite" className="sr-only">
        {announcement}
      </p>

      {drop &&
        (drop === "needs-vision" ? (
          <DropZone
            tone="reject"
            count={0}
            title={noImages.replace(/\.$/, "")}
            detail={switchHint(listed.filter((m) => m.vision).map((m) => m.name))}
            meta={null}
            column={column}
            toolbar={toolbar}
          />
        ) : (
          <DropZone
            tone={drop.count > 0 ? "accept" : "reject"}
            count={drop.count}
            title={drop.title}
            detail={drop.detail}
            meta={drop.count > 0 ? dropMeta(vision) : null}
            column={column}
            toolbar={toolbar}
          />
        ))}

      <Sheet open={historyOpen} onOpenChange={setHistoryOpen}>
        <SheetContent side="left" className="flex w-80 flex-col gap-4 p-4" aria-describedby={undefined}>
          <SheetTitle className="sr-only">Chats</SheetTitle>
          <SheetDescription className="sr-only">Chats saved in this browser.</SheetDescription>
          {history}
        </SheetContent>
      </Sheet>
    </>
  );
}

function MessagesFallback() {
  return (
    <div aria-busy className="space-y-8">
      <Skeleton className="ml-auto h-10 w-2/3 rounded-xl" />
      <div className="space-y-2">
        <Skeleton className="h-4 w-full" />
        <Skeleton className="h-4 w-5/6" />
        <Skeleton className="h-4 w-2/3" />
      </div>
    </div>
  );
}
