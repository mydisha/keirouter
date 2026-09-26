// Public landing shell — floating pill nav + mobile top bar / bottom dock,
// announcement dialog and footer (design.md §5, §8). Standalone: it does not
// import the dashboard Layout or ThemeProvider, and scopes every token through
// `.landing-root` (index.css). The theme toggle flips `data-theme` on this
// element only, so it never touches the dashboard's `.dark` class.

import { useState, type ReactNode } from "react";
import { AnnouncementDialog } from "./AnnouncementDialog";

type Theme = "light" | "dark";

const TABS = [
  { id: "overview", label: "Monitor", href: "#overview", d: "M3 12h4l3-8 4 16 3-8h4" },
  { id: "models", label: "Model", href: "#models", d: "M3 3h7v7H3V3Zm11 0h7v7h-7V3ZM3 14h7v7H3v-7Zm11 0h7v7h-7v-7" },
  { id: "purchase", label: "Beli", href: "#purchase", d: "M3 7V5h16v3M3 8h18v12H3V8Zm18 4h-6v4h6" },
  { id: "balance", label: "Saldo", href: "#balance", d: "M14 10a6 6 0 1 1-2-4M13 11l9-9m-4 4 3 3m-6 0 3 3" },
  { id: "docs", label: "Docs", href: "/docs", d: "M3 4h7l2 2 2-2h7v16h-7l-2 1-2-1H3V4Zm9 2v15" },
] as const;

const BELL_D = "M6 17h12l-1.5-3V9a4.5 4.5 0 0 0-9 0v5L6 17Zm4 3h4";
const THEME_D = "M20 13A8 8 0 0 1 11 4a7 7 0 1 0 9 9Z";

function UiIcon({ d, size = 20 }: { d: string; size?: number }) {
  return (
    <svg
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.65}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d={d} />
    </svg>
  );
}

function BrandMark({ size = 38 }: { size?: number }) {
  return (
    <span className="inline-flex shrink-0" style={{ width: size, height: size }} aria-hidden="true">
      <svg viewBox="0 0 40 40" width={size} height={size}>
        <rect width="40" height="40" rx="11" fill="#252820" />
        <path
          fill="none"
          stroke="#dbe5cb"
          strokeWidth="1.3"
          strokeLinecap="round"
          strokeLinejoin="round"
          d="M17.77 8.14L6.00 12.81M22.13 16.38L32.37 10.22M8.46 29.12L18.69 22.23M31.80 26.06L22.82 35.00M6.00 12.81L8.46 29.12M22.13 16.38L6.00 12.81M8.46 29.12L22.82 35.00M22.82 35.00L22.13 16.38M32.37 10.22L31.80 26.06M17.77 8.14L32.37 10.22M31.80 26.06L18.69 22.23M18.69 22.23L17.77 8.14"
        />
      </svg>
    </span>
  );
}

function Brand() {
  return (
    <a href="#overview" className="flex items-center gap-2.5 no-underline">
      <BrandMark />
      <span className="leading-none">
        <strong className="block text-[17px] font-[650] tracking-[-0.7px] text-[var(--ink)]">KeiRouter</strong>
        <small className="mt-0.5 hidden text-[10px] uppercase tracking-[1.5px] text-[var(--muted)] sm:block">
          gateway
        </small>
      </span>
    </a>
  );
}

function AnnouncementButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      title="Pengumuman"
      aria-label="Pengumuman"
      className="relative flex h-11 w-11 items-center justify-center rounded-xl border border-[var(--line)] text-[var(--green)] transition-colors hover:bg-[var(--soft)]"
    >
      <UiIcon d={BELL_D} />
      <span className="animate-pulse absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-[#c43d2e] px-1 text-[9px] font-bold text-white">
        1
      </span>
    </button>
  );
}

function ThemeButton({ theme, onToggle }: { theme: Theme; onToggle: () => void }) {
  return (
    <button
      type="button"
      onClick={onToggle}
      aria-pressed={theme === "dark"}
      title="Mode"
      aria-label="Mode"
      className="flex h-11 w-11 items-center justify-center rounded-xl border border-[var(--line)] text-[var(--green)] transition-colors hover:bg-[var(--soft)]"
    >
      <UiIcon d={THEME_D} />
    </button>
  );
}

function TabLink({
  tab,
  active,
  onSelect,
  stacked = false,
}: {
  tab: (typeof TABS)[number];
  active: string;
  onSelect: (id: string) => void;
  stacked?: boolean;
}) {
  const isActive = active === tab.id;
  return (
    <a
      href={tab.href}
      role="tab"
      aria-selected={isActive}
      onClick={() => onSelect(tab.id)}
      className={`flex items-center justify-center gap-2 rounded-xl no-underline transition-colors ${
        stacked ? "flex-col gap-1 px-1 py-1.5 text-[10px]" : "px-3 py-2 text-[13px]"
      } ${isActive ? "bg-[var(--accent-bg)] font-[650] text-[var(--green)]" : "text-[var(--muted)] hover:bg-[var(--soft)]"}`}
    >
      <UiIcon d={tab.d} size={stacked ? 20 : 17} />
      <span>{tab.label}</span>
    </a>
  );
}

export function PublicLayout({ children }: { children: ReactNode }) {
  const [theme, setTheme] = useState<Theme>("light");
  const [active, setActive] = useState("overview");
  const [announcementOpen, setAnnouncementOpen] = useState(false);

  return (
    <div className="landing-root min-h-dvh" data-theme={theme}>
      {/* Desktop: floating centered pill nav. */}
      <nav
        className="fixed left-1/2 top-[18px] z-30 hidden w-[min(1096px,calc(100%-40px))] -translate-x-1/2 items-center gap-2 rounded-[18px] border border-[var(--line)] bg-[var(--nav-surface)] px-4 py-2 shadow-[0_5px_22px_#1528180c] md:flex"
        role="tablist"
        aria-label="Navigasi utama"
      >
        <Brand />
        <div className="mx-auto flex items-center gap-1">
          {TABS.map((tab) => (
            <TabLink key={tab.id} tab={tab} active={active} onSelect={setActive} />
          ))}
        </div>
        <AnnouncementButton onClick={() => setAnnouncementOpen(true)} />
        <ThemeButton theme={theme} onToggle={() => setTheme(theme === "dark" ? "light" : "dark")} />
      </nav>

      {/* Mobile: top bar + fixed bottom dock. */}
      <header className="fixed inset-x-3 top-[10px] z-30 flex items-center justify-between rounded-2xl border border-[var(--line)] bg-[var(--nav-surface)] px-3 py-2 shadow-[0_5px_22px_#1528180c] md:hidden">
        <Brand />
        <div className="flex items-center gap-2">
          <AnnouncementButton onClick={() => setAnnouncementOpen(true)} />
          <ThemeButton theme={theme} onToggle={() => setTheme(theme === "dark" ? "light" : "dark")} />
        </div>
      </header>
      <nav
        className="fixed inset-x-0 bottom-0 z-30 grid grid-cols-5 border-t border-[var(--line)] bg-[var(--nav-surface)] px-1 pb-[env(safe-area-inset-bottom)] md:hidden"
        role="tablist"
        aria-label="Navigasi utama"
      >
        {TABS.map((tab) => (
          <TabLink key={tab.id} tab={tab} active={active} onSelect={setActive} stacked />
        ))}
      </nav>

      <main className="relative z-10 mx-auto w-full max-w-[1152px] px-4 pb-[calc(80px+env(safe-area-inset-bottom))] pt-[84px] sm:px-7 md:pb-24 md:pt-[112px]">
        {children}
      </main>

      <footer className="relative z-10 mx-auto w-full max-w-[1152px] border-t border-[var(--line)] px-4 pb-[calc(96px+env(safe-area-inset-bottom))] pt-8 text-xs text-[var(--muted)] sm:px-7 md:pb-12">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-2">
            <strong className="text-[var(--ink)]">KeiRouter</strong>
            <span className="uppercase tracking-[1.5px]">gateway</span>
          </div>
          <p>Developed by 0xverssace</p>
          <div className="flex items-center gap-4">
            <a href="/docs" className="text-[var(--green)] hover:underline">
              Dokumentasi API
            </a>
            <a href="/tos" className="text-[var(--green)] hover:underline">
              Syarat Layanan
            </a>
          </div>
        </div>
      </footer>

      <AnnouncementDialog open={announcementOpen} onClose={() => setAnnouncementOpen(false)} />
    </div>
  );
}
