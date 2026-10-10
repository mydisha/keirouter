import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { CheckCircle2, AlertCircle, Info, X } from "lucide-react";

// Toast system: a lightweight, dependency-free notifier styled with the
// KeiRouter design system. Wrap the app in <ToastProvider> and call useToast()
// to push success / error / info messages from anywhere.

type ToastTone = "success" | "error" | "info";

interface Toast {
  id: number;
  tone: ToastTone;
  title: string;
  description?: string;
}

interface ToastAPI {
  toast: (t: { tone?: ToastTone; title: string; description?: string }) => void;
  success: (title: string, description?: string) => void;
  error: (title: string, description?: string) => void;
  info: (title: string, description?: string) => void;
}

const ToastContext = createContext<ToastAPI | null>(null);

// useToast returns the imperative toast API. Must be used within ToastProvider.
export function useToast(): ToastAPI {
  const ctx = useContext(ToastContext);
  if (!ctx) {
    throw new Error("useToast must be used within a ToastProvider");
  }
  return ctx;
}

const AUTO_DISMISS_MS = 5000;
// Errors carry what-to-do text, so they stay up longer.
const ERROR_DISMISS_MS = 8000;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const idRef = useRef(0);
  // Screen-reader announcements go through two always-mounted live regions
  // (a live region inserted together with its text is often not read).
  const [politeText, setPoliteText] = useState("");
  const [assertiveText, setAssertiveText] = useState("");

  const dismiss = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const push = useCallback((t: { tone?: ToastTone; title: string; description?: string }) => {
    const id = ++idRef.current;
    const toast: Toast = { id, tone: t.tone ?? "info", title: t.title, description: t.description };
    setToasts((prev) => [...prev, toast]);
    const text = t.description ? `${t.title}. ${t.description}` : t.title;
    // Re-announce identical consecutive messages by toggling a trailing space.
    if (toast.tone === "error") setAssertiveText((prev) => (prev === text ? `${text} ` : text));
    else setPoliteText((prev) => (prev === text ? `${text} ` : text));
  }, []);

  const api: ToastAPI = {
    toast: push,
    success: (title, description) => push({ tone: "success", title, description }),
    error: (title, description) => push({ tone: "error", title, description }),
    info: (title, description) => push({ tone: "info", title, description }),
  };

  return (
    <ToastContext.Provider value={api}>
      {children}
      <div className="sr-only" role="status" aria-live="polite" aria-atomic="true">
        {politeText}
      </div>
      <div className="sr-only" role="alert" aria-live="assertive" aria-atomic="true">
        {assertiveText}
      </div>
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </ToastContext.Provider>
  );
}

function ToastViewport({ toasts, onDismiss }: { toasts: Toast[]; onDismiss: (id: number) => void }) {
  return (
    <div
      data-toast-region
      className="pointer-events-none fixed right-4 top-4 z-60 flex w-[calc(100%-2rem)] max-w-sm flex-col gap-2"
    >
      {toasts.length > 0 && (
        <section aria-label="Notifications">
          <ol className="flex flex-col gap-2">
            {toasts.map((t) => (
              <ToastCard key={t.id} toast={t} onDismiss={() => onDismiss(t.id)} />
            ))}
          </ol>
        </section>
      )}
    </div>
  );
}

// Status is carried by a small icon plus sr-only tone text; the card stays neutral.
const toneMeta: Record<ToastTone, { icon: typeof Info; iconClass: string; srLabel: string }> = {
  success: { icon: CheckCircle2, iconClass: "text-ok", srLabel: "Success" },
  error: { icon: AlertCircle, iconClass: "text-bad", srLabel: "Error" },
  info: { icon: Info, iconClass: "text-fg-faint", srLabel: "Info" },
};

// ToastCard owns its timer: it pauses while hovered or focused (WCAG 2.2.1)
// and Escape dismisses it when focus is inside.
function ToastCard({ toast, onDismiss }: { toast: Toast; onDismiss: () => void }) {
  const meta = toneMeta[toast.tone];
  const Icon = meta.icon;
  const duration = toast.tone === "error" ? ERROR_DISMISS_MS : AUTO_DISMISS_MS;
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const paused = hovered || focused;
  const remaining = useRef(duration);
  const onDismissRef = useRef(onDismiss);

  useEffect(() => {
    onDismissRef.current = onDismiss;
  }, [onDismiss]);

  useEffect(() => {
    if (paused) return;
    const startedAt = Date.now();
    const timer = setTimeout(() => onDismissRef.current(), remaining.current);
    return () => {
      clearTimeout(timer);
      remaining.current = Math.max(0, remaining.current - (Date.now() - startedAt));
    };
  }, [paused]);

  return (
    <li
      className="pointer-events-auto relative overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-pop)] motion-safe:animate-[toast-in_0.2s_ease-out]"
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      onFocus={() => setFocused(true)}
      onBlur={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setFocused(false);
      }}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          onDismiss();
        }
      }}
    >
      <div className="flex items-start gap-2.5 py-2.5 pl-3.5 pr-2">
        <Icon className={`mt-px h-4 w-4 shrink-0 ${meta.iconClass}`} strokeWidth={1.75} aria-hidden="true" />
        <div className="min-w-0 flex-1">
          <p className="text-[13px] font-medium leading-snug text-fg">
            <span className="sr-only">{meta.srLabel}: </span>
            {toast.title}
          </p>
          {toast.description && (
            <p className="mt-0.5 break-words text-[12px] leading-5 text-fg-muted">{toast.description}</p>
          )}
        </div>
        <button
          type="button"
          onClick={onDismiss}
          className="-my-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-fg-faint transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
          aria-label={`Dismiss notification: ${toast.title}`}
        >
          <X className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
        </button>
      </div>
      {/* Auto-dismiss progress: a neutral hairline that freezes while paused.
          Hidden under reduced motion. */}
      <div className="absolute inset-x-0 bottom-0 h-px motion-reduce:hidden" aria-hidden="true">
        <div
          className="h-full bg-line-strong"
          style={{
            animation: `toast-progress ${duration}ms linear forwards`,
            animationPlayState: paused ? "paused" : "running",
          }}
        />
      </div>
    </li>
  );
}
