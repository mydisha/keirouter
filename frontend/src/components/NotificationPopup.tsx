// Landing notification popup — a small card that drops below the navbar,
// like a social feed's bell menu. Content is fetched from the public
// notifications API (configured in the dashboard); the list is capped at
// MAX_NOTIFICATIONS.

import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchPublicNotifications, type LandingNotification } from "../lib/publicApi";
import { sanitizeHtml } from "../lib/sanitizeHtml";

export const MAX_NOTIFICATIONS = 5;

function NotificationItem({ item }: { item: LandingNotification }) {
  const inner = (
    <div className="flex items-start gap-2.5">
      <span className="mt-1.5 h-2 w-2 shrink-0 rounded-full bg-[var(--green)]" aria-hidden="true" />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <p
            className="truncate text-[13px] font-[650] text-[var(--ink)] [&_a]:underline"
            dangerouslySetInnerHTML={{ __html: sanitizeHtml(item.title) }}
          />
          {item.tag && (
            <span className="shrink-0 rounded-full bg-[var(--accent-bg)] px-2 py-0.5 text-[9px] font-bold uppercase tracking-[0.5px] text-[var(--green)]">
              {item.tag}
            </span>
          )}
        </div>
        <p
          className="mt-0.5 text-xs leading-relaxed text-[var(--muted)] [&_a]:underline"
          dangerouslySetInnerHTML={{ __html: sanitizeHtml(item.body) }}
        />
      </div>
    </div>
  );

  const className =
    "block rounded-xl px-3 py-2.5 text-left no-underline transition-colors hover:bg-[var(--soft)]";
  return item.href ? (
    <a href={item.href} target="_blank" rel="noopener noreferrer" className={className}>
      {inner}
    </a>
  ) : (
    <div className={className}>{inner}</div>
  );
}

export function NotificationPopup({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { data } = useQuery({ queryKey: ["public-notifications"], queryFn: fetchPublicNotifications });
  const items = data ?? [];

  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    const onPointer = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose();
    };
    document.addEventListener("keydown", onKey);
    // Defer so the click that opened the popup does not immediately close it.
    const t = setTimeout(() => document.addEventListener("mousedown", onPointer), 0);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onPointer);
      clearTimeout(t);
    };
  }, [open, onClose]);

  if (!open) return null;

  const list = items.slice(0, MAX_NOTIFICATIONS);

  return (
    <div
      ref={ref}
      role="dialog"
      aria-label="Notifikasi"
      className="fixed right-4 top-[76px] z-40 w-[min(360px,calc(100vw-32px))] overflow-hidden rounded-2xl border border-[var(--line)] bg-[var(--paper)] text-[var(--ink)] shadow-[0_12px_32px_#1528181f] md:right-[max(1rem,calc(50%-548px))] md:top-[74px]"
    >
      <div className="flex items-center justify-between border-b border-[var(--line)] px-4 py-3">
        <p className="text-[13px] font-[650]">Notifikasi</p>
        <span className="rounded-full bg-[var(--accent-bg)] px-2 py-0.5 text-[10px] font-bold text-[var(--green)]">
          {list.length}
        </span>
      </div>
      <div className="max-h-[min(420px,60dvh)] overflow-y-auto p-1.5">
        {list.length === 0 ? (
          <p className="px-3 py-8 text-center text-xs text-[var(--muted)]">Belum ada notifikasi.</p>
        ) : (
          <ul className="space-y-0.5">
            {list.map((item) => (
              <li key={item.id}>
                <NotificationItem item={item} />
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
