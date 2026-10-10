import { createContext, useCallback, useContext, useRef, useState, type ReactNode } from "react";
import { AlertDialog } from "radix-ui";
import { cn } from "@/lib/utils";

// useConfirm replaces window.confirm() with an accessible, themed dialog that
// still reads like the native call at the call site:
//
//   const confirm = useConfirm();
//   if (!(await confirm({ title: "Revoke key?", tone: "danger" }))) return;

export interface ConfirmOptions {
  title: string;
  description?: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  /** "danger" styles the confirm action as destructive. */
  tone?: "default" | "danger";
}

type ConfirmFn = (options: ConfirmOptions) => Promise<boolean>;

const ConfirmContext = createContext<ConfirmFn | null>(null);

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [options, setOptions] = useState<ConfirmOptions | null>(null);
  const resolver = useRef<((ok: boolean) => void) | null>(null);

  const confirm = useCallback<ConfirmFn>((next) => {
    // A second request while one is open resolves the first as cancelled.
    resolver.current?.(false);
    setOptions(next);
    return new Promise<boolean>((resolve) => {
      resolver.current = resolve;
    });
  }, []);

  const settle = (ok: boolean) => {
    resolver.current?.(ok);
    resolver.current = null;
    setOptions(null);
  };

  const danger = options?.tone === "danger";
  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      <AlertDialog.Root open={options !== null} onOpenChange={(open) => !open && settle(false)}>
        <AlertDialog.Portal>
          <AlertDialog.Overlay className="fixed inset-0 z-50 bg-black/45 data-[state=open]:animate-in data-[state=open]:fade-in-0" />
          <AlertDialog.Content className="fixed left-1/2 top-1/2 z-50 w-[calc(100%-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 rounded-2xl border border-line bg-surface p-5 shadow-[var(--shadow-float)] data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-[0.98]">
            <AlertDialog.Title className="text-[15px] font-semibold tracking-[-0.01em] text-fg">{options?.title}</AlertDialog.Title>
            {options?.description ? (
              <AlertDialog.Description className="mt-1.5 text-[13px] leading-5 text-fg-muted">{options.description}</AlertDialog.Description>
            ) : (
              <AlertDialog.Description className="sr-only">Confirm this action</AlertDialog.Description>
            )}
            <div className="mt-5 flex justify-end gap-2">
              <AlertDialog.Cancel className="inline-flex h-9 items-center rounded-lg border border-line-strong bg-surface px-3 text-[13px] font-medium text-fg transition-colors hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40">
                {options?.cancelLabel ?? "Cancel"}
              </AlertDialog.Cancel>
              <AlertDialog.Action
                onClick={() => settle(true)}
                className={cn(
                  "inline-flex h-9 items-center rounded-lg px-3 text-[13px] font-medium transition-opacity hover:opacity-85 focus:outline-none focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-elevated)]",
                  danger ? "bg-bad text-white focus-visible:ring-bad/50" : "bg-primary text-primary-fg focus-visible:ring-accent-500/40",
                )}
              >
                {options?.confirmLabel ?? (danger ? "Delete" : "Confirm")}
              </AlertDialog.Action>
            </div>
          </AlertDialog.Content>
        </AlertDialog.Portal>
      </AlertDialog.Root>
    </ConfirmContext.Provider>
  );
}

export function useConfirm(): ConfirmFn {
  const ctx = useContext(ConfirmContext);
  if (!ctx) throw new Error("useConfirm must be used within a ConfirmProvider");
  return ctx;
}
