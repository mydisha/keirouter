// Announcement modal — native <dialog> per design.md §8 (announcement-dialog):
// 3-row grid [header | scroll content | footer], eyebrow/title/subtitle, model
// sections with `BARU` / `TETAP TERSEDIA` pills and <code> ids, a support CTA to
// WhatsApp, and a scroll-hint footer with a `Tutup` button. Content is the copy
// captured for the 26 Sep 2026 catalogue update; it is static (no endpoint).

import { useEffect, useRef } from "react";

const WA_URL = "https://wa.me/84826240052";

// hex colours are the announcement art-block values from design.md §8 (not
// landing tokens); kept inline because they are specific to the mascot art.
const SECTIONS = [
  {
    pill: "BARU",
    pillTone: "new",
    model: "DeepSeek V4.1 Flash",
    id: "deepseek-v4.1-flash",
    body: "Model flash generasi baru dengan konteks panjang dan harga per-1M sangat rendah. Mendukung vision, reasoning, dan tools.",
    limits: "\u2192 1.000.000 token konteks · 65.536 token keluaran",
  },
  {
    pill: "BARU · STEALTH",
    pillTone: "stealth",
    model: "Pixel Canary",
    id: "pixel-canary",
    body: "Akses sementara selama masa uji coba. Kuota terbatas dan dapat berubah tanpa pemberitahuan.",
    limits: "\u2192 Akses sementara · kuota terbatas",
  },
  {
    pill: "TETAP TERSEDIA",
    pillTone: "stay",
    model: "Space Bunny Alpha",
    id: "space-bunny-alpha",
    body: "Masih tersedia untuk sementara. Harga tetap Rp 1 selama periode stealth berlangsung.",
    limits: "\u2192 Akses sementara · Rp 1 / 1M",
  },
] as const;

function CloseIcon() {
  return (
    <svg
      viewBox="0 0 24 24"
      width={20}
      height={20}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.65}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M6 6l12 12M18 6L6 18" />
    </svg>
  );
}

export function AnnouncementDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (open && !el.open) el.showModal();
    else if (!open && el.open) el.close();
  }, [open]);

  return (
    <dialog
      ref={ref}
      onClose={onClose}
      onCancel={onClose}
      aria-labelledby="announcement-title"
      className="w-[min(640px,100vw-32px)] rounded-[18px] border border-[var(--line)] bg-[var(--paper)] p-0 text-[var(--ink)] backdrop:bg-black/45"
    >
      <div className="grid h-[min(810px,100dvh-48px)] grid-rows-[auto_1fr_auto]">
        {/* Header */}
        <header className="flex items-start justify-between gap-4 border-b border-[var(--line)] px-6 py-5">
          <div>
            <p className="text-[11px] uppercase tracking-[0.7px] text-[var(--muted)]">
              Pembaruan model / 26 September 2026
            </p>
            <h2 id="announcement-title" className="mt-1 text-[22px] font-[650] tracking-[-0.5px]">
              Model baru &amp; status stealth
            </h2>
            <p className="mt-1 text-sm text-[var(--muted)]">
              Rangkuman katalog terbaru dan masa akses sementara.
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Tutup"
            className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl border border-[var(--line)] text-[var(--muted)] transition-colors hover:bg-[var(--soft)]"
          >
            <CloseIcon />
          </button>
        </header>

        {/* Scrollable content */}
        <div className="overflow-y-auto px-6 py-5">
          <div className="flex items-center gap-4 rounded-2xl p-4" style={{ background: "#1c3528" }}>
            <div className="flex h-[72px] w-[72px] shrink-0 items-center justify-center rounded-xl bg-[#253229]">
              <svg viewBox="0 0 40 40" width={44} height={44} aria-hidden="true">
                <circle cx="16" cy="16" r="7" fill="#dbe5cb" opacity="0.85" />
                <circle cx="27" cy="24" r="4" fill="#dbe5cb" opacity="0.5" />
              </svg>
            </div>
            <div className="text-sm" style={{ color: "#f5ebd0" }}>
              <p className="font-[650]">Pixel Canary &amp; Space Bunny</p>
              <p className="opacity-80">Dua model stealth yang sedang diuji coba.</p>
            </div>
          </div>

          <p className="mt-5 rounded-xl border border-[var(--line)] bg-[var(--accent-bg)] px-4 py-3 text-sm font-[650] text-[var(--green)]">
            Harga khusus: Rp 1 / 1M token selama periode stealth.
          </p>

          {SECTIONS.map((s) => (
            <section key={s.id} className="mt-5 border-t border-[var(--line)] pt-4">
              <span
                className={`inline-block rounded-full px-2.5 py-1 text-[10px] font-bold uppercase tracking-[0.7px] ${
                  s.pillTone === "new"
                    ? "bg-[var(--accent-bg)] text-[var(--green)]"
                    : s.pillTone === "stealth"
                      ? "bg-[var(--soft)] text-[var(--muted)]"
                      : "bg-[var(--tag-bg)] text-[var(--tag-ink)]"
                }`}
              >
                {s.pill}
              </span>
              <h3 className="mt-2 text-[15px] font-[650]">{s.model}</h3>
              <code className="mt-1 block text-xs text-[var(--muted)]">{s.id}</code>
              <p className="mt-2 text-sm text-[var(--muted)]">{s.body}</p>
              <p className="mt-1 text-xs text-[var(--muted)]">{s.limits}</p>
            </section>
          ))}

          <a
            href={WA_URL}
            target="_blank"
            rel="noreferrer"
            className="mt-6 inline-flex items-center gap-1.5 rounded-xl bg-[var(--green)] px-4 py-3 text-sm font-[650] text-[var(--on-accent)] no-underline hover:opacity-90"
          >
            Hubungi WhatsApp ↗
          </a>
        </div>

        {/* Footer */}
        <footer className="flex items-center justify-between gap-4 border-t border-[var(--line)] px-6 py-4 text-xs text-[var(--muted)]">
          <span>Gulir untuk melihat detail &darr;</span>
          <button
            type="button"
            onClick={onClose}
            className="rounded-xl bg-[var(--green)] px-4 py-2.5 text-sm font-[650] text-[var(--on-accent)] transition-opacity hover:opacity-90"
          >
            Tutup
          </button>
        </footer>
      </div>
    </dialog>
  );
}
