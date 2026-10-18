// Public landing shell — sticky glass nav, mobile top bar + bottom dock,
// announcement popover and footer. Standalone: it does not import the
// dashboard Layout or ThemeProvider, and scopes every token through
// `.landing-root` (index.css), which mirrors the dashboard design system. The
// theme toggle flips `data-theme` on this element only, so it never touches
// the dashboard's `.dark` class.

import { useEffect, useState, type ReactNode } from "react";
import { Bell, Boxes, Gift, Home, Moon, Send, Server, Sun, UserRound, type LucideIcon } from "lucide-react";
import { NotificationPopup } from "./NotificationPopup";
import { storedDashboardTheme } from "./ThemeProvider";

type Theme = "light" | "dark";

// Separate from the dashboard theme key: the dashboard's light default must
// not switch public pages away from their dark default. Read by index.html.
const THEME_STORAGE_KEY = "keirouter-public-theme";
export const TELEGRAM_URL = "https://t.me/+F2C4PaeMgv1hYjE1";

function storedTheme(): Theme {
  try {
    if (localStorage.getItem(THEME_STORAGE_KEY) === "light") return "light";
    // Public pages open in dark mode until the visitor picks a theme.
    return "dark";
  } catch {
    return "dark";
  }
}

const TABS: { id: string; label: string; href: string; icon: LucideIcon }[] = [
  { id: "overview", label: "Home", href: "/", icon: Home },
  { id: "models", label: "Model", href: "/model", icon: Boxes },
  { id: "hosting", label: "Hosting", href: "/hosting-9router", icon: Server },
  { id: "bansos", label: "Bansos", href: "/bansos", icon: Gift },
  { id: "telegram", label: "Telegram", href: TELEGRAM_URL, icon: Send },
];

const FOCUS = "focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--green)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg)]";

export function BrandMark({ className = "h-8 w-8" }: { className?: string }) {
  return (
    <span
      className={`inline-flex shrink-0 items-center justify-center rounded-lg bg-[var(--action)] text-[var(--action-fg)] ${className}`}
      aria-hidden="true"
    >
      <svg viewBox="0 0 24 24" className="h-[58%] w-[58%]">
        <path fill="none" stroke="currentColor" strokeWidth="2.6" strokeLinecap="round" d="M5 6.5h14M12 6.5V19" />
        <circle cx="18.5" cy="18" r="2" fill="var(--color-secondary-500)" />
      </svg>
    </span>
  );
}

function Brand() {
  return (
    <a href="/" className={`flex items-center gap-2.5 rounded-lg no-underline ${FOCUS}`}>
      <BrandMark />
      <span className="leading-none">
        <strong className="block text-[16px] font-semibold tracking-[-0.3px] text-[var(--ink)]">Tokenizer</strong>
        <small className="mt-1 hidden font-mono text-[10px] uppercase tracking-[1.2px] text-[var(--muted)] sm:block">
          AI pay-as-you-go
        </small>
      </span>
    </a>
  );
}

function IconButton({
  label,
  onClick,
  href,
  children,
  pressed,
}: {
  label: string;
  onClick?: () => void;
  href?: string;
  children: ReactNode;
  pressed?: boolean;
}) {
  const cls = `relative inline-flex h-9 w-9 items-center justify-center rounded-lg border border-[var(--line)] bg-[var(--paper)] text-[var(--muted)] no-underline transition-colors hover:border-[var(--line-strong)] hover:text-[var(--ink)] ${FOCUS}`;
  if (href) {
    return (
      <a href={href} title={label} aria-label={label} className={cls}>
        {children}
      </a>
    );
  }
  return (
    <button type="button" onClick={onClick} title={label} aria-label={label} aria-pressed={pressed} className={cls}>
      {children}
    </button>
  );
}

function TabLink({ tab, active, stacked = false }: { tab: (typeof TABS)[number]; active: string; stacked?: boolean }) {
  const isActive = active === tab.id;
  const external = tab.href.startsWith("http");
  const Icon = tab.icon;
  return (
    <a
      href={tab.href}
      aria-current={isActive ? "page" : undefined}
      target={external ? "_blank" : undefined}
      rel={external ? "noopener noreferrer" : undefined}
      className={`flex items-center justify-center no-underline transition-colors ${FOCUS} ${
        stacked
          ? `flex-col gap-1 rounded-lg px-1 py-2 text-[10.5px] ${isActive ? "font-semibold text-[var(--ink)]" : "text-[var(--muted)]"}`
          : `gap-1.5 rounded-lg px-3 py-1.5 text-[13px] ${
              isActive ? "bg-[var(--soft)] font-medium text-[var(--ink)]" : "text-[var(--muted)] hover:text-[var(--ink)]"
            }`
      }`}
    >
      <Icon className={stacked ? "h-5 w-5" : "h-4 w-4"} strokeWidth={1.75} aria-hidden="true" />
      <span>{tab.label}</span>
    </a>
  );
}

export function PublicLayout({ children }: { children: ReactNode }) {
  const [theme, setTheme] = useState<Theme>(storedTheme);
  const [active] = useState(() => {
    const p = window.location.pathname;
    if (p.startsWith("/bansos")) return "bansos";
    if (p.startsWith("/hosting-9router")) return "hosting";
    if (p.startsWith("/model")) return "models";
    return "overview";
  });
  const [announcementOpen, setAnnouncementOpen] = useState(false);

  // Keep the page background (html.dark) on the public theme; hand the root
  // back to the dashboard theme when leaving for the portal or dashboard.
  useEffect(() => {
    document.documentElement.classList.toggle("dark", theme === "dark");
  }, [theme]);
  useEffect(() => () => void document.documentElement.classList.toggle("dark", storedDashboardTheme() === "dark"), []);

  const toggleTheme = () => {
    const next: Theme = theme === "dark" ? "light" : "dark";
    try {
      localStorage.setItem(THEME_STORAGE_KEY, next);
    } catch {
      /* storage unavailable: the choice lasts for this page only */
    }
    setTheme(next);
  };

  const actions = (
    <div className="flex items-center gap-1.5">
      <IconButton label="Pengumuman" onClick={() => setAnnouncementOpen((v) => !v)} pressed={announcementOpen}>
        <Bell className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
        <span className="landing-live-dot absolute right-1.5 top-1.5 h-1.5 w-1.5 rounded-full bg-[var(--color-danger)]" aria-hidden="true" />
      </IconButton>
      <IconButton label={theme === "dark" ? "Mode terang" : "Mode gelap"} onClick={toggleTheme} pressed={theme === "dark"}>
        {theme === "dark" ? (
          <Sun className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
        ) : (
          <Moon className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
        )}
      </IconButton>
      <a
        href="/portal"
        className={`ml-1 hidden h-9 items-center gap-1.5 rounded-lg bg-[var(--action)] px-3.5 text-[13px] font-medium text-[var(--action-fg)] no-underline transition-opacity hover:opacity-90 sm:inline-flex ${FOCUS}`}
      >
        <UserRound className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
        Masuk portal
      </a>
      <span className="sm:hidden">
        <IconButton label="Masuk portal" href="/portal">
          <UserRound className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
        </IconButton>
      </span>
    </div>
  );

  return (
    <div className="landing-root relative min-h-dvh overflow-x-clip" data-theme={theme}>
      <header className="sticky top-0 z-30 border-b border-[var(--line)] bg-[var(--nav-surface)] backdrop-blur-xl">
        <div className="mx-auto flex h-14 w-full max-w-[1152px] items-center gap-3 px-4 sm:px-6">
          <Brand />
          <nav className="mx-auto hidden items-center gap-0.5 md:flex" aria-label="Navigasi utama">
            {TABS.map((tab) => (
              <TabLink key={tab.id} tab={tab} active={active} />
            ))}
          </nav>
          <div className="ml-auto md:ml-0">{actions}</div>
        </div>
      </header>

      {/* Mobile: fixed bottom dock. */}
      <nav
        className="fixed inset-x-0 bottom-0 z-30 grid grid-cols-5 border-t border-[var(--line)] bg-[var(--nav-surface)] px-1 pb-[env(safe-area-inset-bottom)] backdrop-blur-xl md:hidden"
        aria-label="Navigasi utama"
      >
        {TABS.map((tab) => (
          <TabLink key={tab.id} tab={tab} active={active} stacked />
        ))}
      </nav>

      <main className="relative z-10 mx-auto w-full max-w-[1152px] px-4 pb-[calc(88px+env(safe-area-inset-bottom))] pt-8 sm:px-6 md:pb-24 md:pt-12">
        {children}
      </main>

      <footer className="relative z-10 border-t border-[var(--line)] bg-[var(--paper)]">
        <div className="mx-auto grid w-full max-w-[1152px] gap-8 px-4 pb-[calc(104px+env(safe-area-inset-bottom))] pt-10 text-[13px] sm:grid-cols-[1.4fr_1fr_1fr] sm:px-6 md:pb-12">
          <div>
            <div className="flex items-center gap-2.5">
              <BrandMark className="h-7 w-7" />
              <strong className="text-[15px] font-semibold text-[var(--ink)]">Tokenizer</strong>
            </div>
            <p className="mt-3 max-w-xs text-[var(--muted)]">
              Gateway AI pay-as-you-go. Satu API key untuk model frontier, bayar sesuai token yang dipakai.
            </p>
          </div>
          <div>
            <p className="font-mono text-[11px] uppercase tracking-[1px] text-[var(--muted)]">Layanan</p>
            <ul className="mt-3 space-y-2">
              <li><a href="/model" className="text-[var(--ink)] no-underline hover:underline">Model &amp; harga</a></li>
              <li><a href="/hosting-9router" className="text-[var(--ink)] no-underline hover:underline">Hosting 9router</a></li>
              <li><a href="/bansos" className="text-[var(--ink)] no-underline hover:underline">Bansos</a></li>
              <li><a href="/portal" className="text-[var(--ink)] no-underline hover:underline">Portal pengguna</a></li>
            </ul>
          </div>
          <div>
            <p className="font-mono text-[11px] uppercase tracking-[1px] text-[var(--muted)]">Bantuan</p>
            <ul className="mt-3 space-y-2">
              <li>
                <a href={TELEGRAM_URL} target="_blank" rel="noopener noreferrer" className="text-[var(--ink)] no-underline hover:underline">
                  Komunitas Telegram
                </a>
              </li>
              <li><a href="/ketentuan" className="text-[var(--ink)] no-underline hover:underline">Ketentuan Layanan</a></li>
              <li><a href="/ketentuan/hosting-9router" className="text-[var(--ink)] no-underline hover:underline">Ketentuan Hosting 9router</a></li>
            </ul>
          </div>
        </div>
        <div className="border-t border-[var(--line)]">
          <p className="mx-auto w-full max-w-[1152px] px-4 py-4 font-mono text-[11px] text-[var(--muted)] sm:px-6">
            © {new Date().getFullYear()} Tokenizer · PAYG AI Frontier
          </p>
        </div>
      </footer>

      <NotificationPopup open={announcementOpen} onClose={() => setAnnouncementOpen(false)} />
    </div>
  );
}
