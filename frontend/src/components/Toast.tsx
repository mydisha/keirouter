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

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const idRef = useRef(0);
  const timers = useRef<Map<number, ReturnType<typeof setTimeout>>>(new Map());

  const dismiss = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
    const timer = timers.current.get(id);
    if (timer) {
      clearTimeout(timer);
      timers.current.delete(id);
    }
  }, []);

  const push = useCallback(
    (t: { tone?: ToastTone; title: string; description?: string }) => {
      const id = ++idRef.current;
      const toast: Toast = { id, tone: t.tone ?? "info", title: t.title, description: t.description };
      setToasts((prev) => [...prev, toast]);
      const timer = setTimeout(() => dismiss(id), AUTO_DISMISS_MS);
      timers.current.set(id, timer);
    },
    [dismiss],
  );

  useEffect(() => {
    const map = timers.current;
    return () => {
      map.forEach((t) => clearTimeout(t));
      map.clear();
    };
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
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </ToastContext.Provider>
  );
}

function ToastViewport({ toasts, onDismiss }: { toasts: Toast[]; onDismiss: (id: number) => void }) {
  return (
    <div className="pointer-events-none fixed top-4 right-4 z-60 flex w-full max-w-sm flex-col gap-2.5">
      {toasts.map((t) => (
        <ToastCard key={t.id} toast={t} onDismiss={() => onDismiss(t.id)} />
      ))}
    </div>
  );
}

const toneMeta: Record<
  ToastTone,
  { icon: typeof Info; iconClass: string; bg: string; border: string; progressClass: string }
> = {
  success: {
    icon: CheckCircle2,
    iconClass: "text-ok",
    bg: "bg-emerald-50 dark:bg-emerald-950",
    border: "border-emerald-200 dark:border-emerald-800/60",
    progressClass: "bg-ok",
  },
  error: {
    icon: AlertCircle,
    iconClass: "text-bad",
    bg: "bg-red-50 dark:bg-red-950",
    border: "border-red-200 dark:border-red-800/60",
    progressClass: "bg-bad",
  },
  info: {
    icon: Info,
    iconClass: "text-blue-600 dark:text-blue-400",
    bg: "bg-blue-50 dark:bg-blue-950",
    border: "border-blue-200 dark:border-blue-800/60",
    progressClass: "bg-blue-500",
  },
};

function ToastCard({ toast, onDismiss }: { toast: Toast; onDismiss: () => void }) {
  const meta = toneMeta[toast.tone];
  const Icon = meta.icon;
  return (
    <div
      role="status"
      className={`pointer-events-auto relative overflow-hidden rounded-xl border ${meta.border} ${meta.bg} shadow-lg shadow-black/5 animate-[toast-in_0.2s_ease-out] dark:shadow-black/20`}
    >
      <div className="flex items-start gap-3 px-4 py-3">
        <Icon className={`mt-0.5 h-5 w-5 shrink-0 ${meta.iconClass}`} strokeWidth={2} />
        <div className="min-w-0 flex-1">
          <p className="text-sm font-semibold leading-snug text-[var(--text)]">{toast.title}</p>
          {toast.description && (
            <p className="mt-1 break-words text-[13px] leading-relaxed text-[var(--text-muted)]">{toast.description}</p>
          )}
        </div>
        <button
          onClick={onDismiss}
          className="-mr-1 -mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-[var(--text-muted)] transition-colors hover:bg-black/5 hover:text-[var(--text)] focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/60 dark:hover:bg-white/10"
          aria-label="Dismiss"
        >
          <X className="h-4 w-4" />
        </button>
      </div>
      {/* Auto-dismiss progress bar */}
      <div className="h-[3px] w-full bg-black/[0.04] dark:bg-white/[0.06]">
        <div
          className={`h-full ${meta.progressClass} animate-[toast-progress_5s_linear_forwards] rounded-full opacity-40`}
        />
      </div>
    </div>
  );
}
