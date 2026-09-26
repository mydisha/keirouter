import { Suspense, useState, useRef, useEffect, useCallback, type ReactNode } from "react";
import { NavLink, Outlet, useLocation } from "react-router-dom";
import { useQueryClient, useIsFetching } from "@tanstack/react-query";
import {
  LayoutGrid,
  Boxes,
  Layers,
  Wallet,
  Sparkles,
  Settings,
  Search,
  ChevronDown,
  LogOut,
  Network,
  BarChart3,
  Clock,
  TerminalSquare,
  Image,
  Waypoints,
  ScrollText,
  Menu,
  X,
  Key,
  Activity,
  Shield,
  HeartPulse,
  type LucideIcon,
} from "lucide-react";
import { api } from "../lib/api";
import { useBranding } from "../contexts/BrandingContext";
import { ThemeToggle } from "./ThemeToggle";
import { CommandPalette } from "./CommandPalette";
import { UpdateNotification } from "./UpdateNotification";
import { preloadRoute, type RoutePreloadKey } from "../routePreload";
import { dashboard, DASHBOARD_PREFIX } from "../lib/dashboardRoutes";

interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
  end?: boolean;
  preload?: RoutePreloadKey;
}

interface NavGroup {
  heading?: string;
  items: NavItem[];
}

const navGroups: NavGroup[] = [
  {
    items: [
      { to: "/", label: "Overview", icon: LayoutGrid, end: true, preload: "/" },
    ],
  },
  {
    heading: "Traffic & Logic",
    items: [
      { to: "/endpoints", label: "Endpoints", icon: Network, preload: "/endpoints" },
      { to: "/chains", label: "Chains", icon: Layers, preload: "/chains" },
      { to: "/skills", label: "Skills", icon: Sparkles, preload: "/skills" },
    ],
  },
  {
    heading: "Connections",
    items: [
      { to: "/keys", label: "API Keys", icon: Key, preload: "/keys" },
      { to: "/providers", label: "Providers", icon: Boxes, preload: "/providers" },
      { to: "/media", label: "Media", icon: Image, preload: "/media" },
      { to: "/proxy-pools", label: "Proxy Pools", icon: Waypoints, preload: "/proxy-pools" },
    ],
  },
  {
    heading: "Safety",
    items: [
      { to: "/guardrails", label: "Guardrails", icon: Shield, preload: "/guardrails" },
      { to: "/provider-health", label: "Provider Health", icon: HeartPulse, preload: "/provider-health" },
    ],
  },
  {
    heading: "Cost & Analytics",
    items: [
      { to: "/usage", label: "Usage", icon: BarChart3, preload: "/usage" },
      { to: "/plans", label: "Plans", icon: Wallet, preload: "/plans" },
      { to: "/quota", label: "Quota Tracker", icon: Clock, preload: "/quota" },
      { to: "/system", label: "System", icon: Activity, preload: "/system" },
      { to: "/settings", label: "Settings", icon: Settings, preload: "/settings" },
    ],
  },
  {
    heading: "Developer",
    items: [
      { to: "/console", label: "Console Log", icon: ScrollText, preload: "/console" },
      { to: "/cli-tools", label: "CLI Tools", icon: TerminalSquare, preload: "/cli-tools" },
    ],
  },
];

const TITLE_BY_PATH: Record<string, string> = {
  "/": "Overview",
  "/endpoints": "Endpoints",
  "/chains": "Chains",
  "/skills": "Skills",
  "/providers": "Providers",
  "/media": "Media",
  "/proxy-pools": "Proxy Pools",
  "/usage": "Usage",
  "/plans": "Plans",
  "/budgets": "Plans",
  "/quota": "Quota Tracker",
  "/settings": "Settings",
  "/keys": "API Keys",
  "/guardrails": "Guardrails",
  "/provider-health": "Provider Health",
  "/console": "Console Log",
  "/cli-tools": "CLI Tools",
  "/system": "System",
};

const TITLE_BY_PREFIX: [string, string][] = [
  ["/providers/", "Provider"],
  ["/cli-tools/", "CLI Tool"],
  ["/media/", "Media"],
  ["/keys/", "API Key"],
];

function titleForPath(pathname: string): string {
  const path = pathname.startsWith(DASHBOARD_PREFIX)
    ? pathname.slice(DASHBOARD_PREFIX.length) || "/"
    : pathname;
  const exact = TITLE_BY_PATH[path];
  if (exact) return exact;
  for (const [prefix, label] of TITLE_BY_PREFIX) {
    if (path.startsWith(prefix)) return label;
  }
  return "";
}

export function Layout() {
  const location = useLocation();
  const { branding } = useBranding();
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [paletteOpen, setPaletteOpen] = useState(false);

  // Close sidebar on navigation (mobile).
  const closeSidebar = useCallback(() => setSidebarOpen(false), []);

  // Close sidebar on Escape.
  useEffect(() => {
    if (!sidebarOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeSidebar();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [sidebarOpen, closeSidebar]);

  // Cmd+K / Ctrl+K to open command palette.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        setPaletteOpen((v) => !v);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  // Set browser tab title from current route.
  useEffect(() => {
    const label = titleForPath(location.pathname);
    const appName = branding.name || "KeiRouter";
    document.title = label ? `${appName} - ${label}` : appName;
  }, [location.pathname, branding.name]);

  // Lock body scroll when mobile sidebar is open.
  useEffect(() => {
    if (sidebarOpen) {
      document.body.style.overflow = "hidden";
    } else {
      document.body.style.overflow = "";
    }
    return () => { document.body.style.overflow = ""; };
  }, [sidebarOpen]);

  useEffect(() => {
    const warmCommonRoutes = () => {
      preloadRoute("/usage");
      preloadRoute("/providers");
      preloadRoute("/keys");
    };
    type IdleWindow = Window & {
      requestIdleCallback?: (cb: () => void, opts?: { timeout: number }) => number;
      cancelIdleCallback?: (id: number) => void;
    };
    const idleWindow = window as IdleWindow;
    if (idleWindow.requestIdleCallback && idleWindow.cancelIdleCallback) {
      const id = idleWindow.requestIdleCallback(warmCommonRoutes, { timeout: 3000 });
      return () => idleWindow.cancelIdleCallback?.(id);
    }
    const id = window.setTimeout(warmCommonRoutes, 1500);
    return () => window.clearTimeout(id);
  }, []);

  return (
    <div className="flex h-full bg-[var(--bg)]">
      {/* Desktop sidebar — hidden below lg. */}
      <div className="hidden lg:flex">
        <SidebarContent onNavigate={closeSidebar} />
      </div>

      {/* Mobile sidebar overlay + drawer. */}
      {sidebarOpen && (
        <div className="fixed inset-0 z-50 lg:hidden" role="dialog" aria-modal="true" aria-label="Navigation">
          <div
            className="fixed inset-0 bg-black/30"
            style={{ animation: "overlay-in 0.15s ease-out" }}
            onClick={closeSidebar}
          />
          <div
            className="fixed inset-y-0 left-0 z-50 w-60 shadow-[var(--shadow-float)]"
            style={{ animation: "drawer-in 0.2s ease-out" }}
          >
            <SidebarContent onNavigate={closeSidebar} />
          </div>
        </div>
      )}

      <div className="flex min-w-0 flex-1 flex-col">
        <RouteProgress />
        <TopBar onMenuToggle={() => setSidebarOpen((v) => !v)} onSearchOpen={() => setPaletteOpen(true)} />
        <main className="flex-1 overflow-y-auto">
          <div className="mx-auto w-full max-w-7xl px-4 py-6 sm:px-6 sm:py-8 lg:px-8">
            <Suspense fallback={<PageOutletFallback />}>
              <Outlet />
            </Suspense>
          </div>
        </main>
      </div>

      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
    </div>
  );
}

function PageOutletFallback() {
  return (
    <div className="flex min-h-[240px] items-center justify-center py-16">
      <div className="h-6 w-6 animate-spin rounded-full border-2 border-current border-t-transparent opacity-40" />
    </div>
  );
}

// RouteProgress shows a thin indeterminate bar at the top of the content area
// whenever queries are in flight (page navigation kicks off the next page's
// data fetches). This gives an immediate visual response to a nav click even
// while the route's chunk and data are still loading, instead of the page
// appearing frozen for seconds. Pure CSS animation (.route-progress).
function RouteProgress() {
  const fetching = useIsFetching();
  if (fetching === 0) return null;
  return <div className="route-progress" role="progressbar" aria-label="Loading" aria-busy="true" />;
}

function SidebarContent({ onNavigate }: { onNavigate: () => void }) {
  const { branding, logoSrc } = useBranding();
  return (
    <aside className="flex h-full w-60 shrink-0 flex-col border-r border-[var(--border)] bg-[var(--bg-elevated)]">
      <div className="flex items-center justify-between px-5 py-5">
        <img src={logoSrc} alt={branding.name || "KeiRouter"} className="h-14 w-full object-contain object-left" />
        {/* Close button — only visible on mobile when rendered inside the drawer. */}
        <button
          onClick={onNavigate}
          aria-label="Close navigation"
          className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-[var(--text-muted)] transition-colors hover:bg-ink-100 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/60 dark:hover:bg-ink-800 lg:hidden"
        >
          <X className="h-5 w-5" />
        </button>
      </div>

      <nav aria-label="Main navigation" className="flex-1 space-y-6 overflow-y-auto px-3 py-2">
        {navGroups.map((group, gi) => (
          <div key={gi} role="group" aria-label={group.heading}>
            {group.heading && (
              <p className="px-3 pb-1.5 text-[11px] font-semibold uppercase tracking-widest text-ink-400 dark:text-ink-500">
                {group.heading}
              </p>
            )}
            <ul className="space-y-0.5">
              {group.items.map((item) => (
                <li key={item.to}>
                  <NavLink
                    to={dashboard(item.to)}
                    end={item.end}
                    onMouseEnter={() => item.preload && preloadRoute(item.preload)}
                    onFocus={() => item.preload && preloadRoute(item.preload)}
                    onTouchStart={() => item.preload && preloadRoute(item.preload)}
                    onClick={onNavigate}
                    className={({ isActive }) =>
                      `group flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm transition-all duration-200 ease-out focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/60 ${
                        isActive
                          ? "bg-ink-100 font-medium text-ink-950 ring-1 ring-ink-200 dark:bg-ink-800/80 dark:text-white dark:ring-ink-700/50"
                          : "text-ink-500 hover:bg-ink-100/50 hover:text-ink-900 dark:text-ink-400 dark:hover:bg-ink-800/40 dark:hover:text-ink-100"
                      }`
                    }
                  >
                    {({ isActive }) => (
                      <>
                        <item.icon
                          className={`h-[18px] w-[18px] shrink-0 transition-colors duration-200 ${
                            isActive
                              ? "text-accent-600 dark:text-accent-400"
                              : "text-ink-400 group-hover:text-ink-600 dark:text-ink-500 dark:group-hover:text-ink-300"
                          }`}
                          strokeWidth={isActive ? 2.5 : 2}
                        />
                        <span className="truncate">{item.label}</span>
                      </>
                    )}
                  </NavLink>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </nav>

      <div className="border-t border-[var(--border)] p-3 space-y-2">
        <div className="flex items-center justify-between px-1">
          <span className="text-[10px] font-medium uppercase tracking-wider text-[var(--text-muted)]">Theme</span>
          <ThemeToggle />
        </div>
      </div>
    </aside>
  );
}

function TopBar({ onMenuToggle, onSearchOpen }: { onMenuToggle: () => void; onSearchOpen: () => void }) {
  const { branding } = useBranding();
  return (
    <header className="flex h-16 shrink-0 items-center justify-center border-b border-[var(--border)] bg-[var(--bg-elevated)]">
      <div className="mx-auto flex w-full max-w-7xl items-center gap-3 px-4 sm:px-6 lg:px-8">
      {/* Hamburger — visible on mobile only. */}
      <button
        onClick={onMenuToggle}
        aria-label="Open navigation"
        className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl text-[var(--text-muted)] transition-colors hover:bg-ink-100 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/60 dark:hover:bg-ink-800 lg:hidden"
      >
        <Menu className="h-5 w-5" />
      </button>

      <button
        type="button"
        onClick={onSearchOpen}
        className="relative max-w-md flex-1 text-left"
        aria-label="Open search (⌘K)"
      >
        <div className="hidden sm:block">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--text-muted)]" />
          <span className="block w-full rounded-xl border border-[var(--border)] bg-[var(--bg)] py-2 pl-9 pr-12 text-sm text-[var(--text-muted)]">
            Search {branding.name || "KeiRouter"}…
          </span>
          <kbd className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 rounded border border-[var(--border)] bg-[var(--bg-elevated)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
            ⌘K
          </kbd>
        </div>
        <div className="flex sm:hidden h-11 w-11 items-center justify-center rounded-xl text-[var(--text-muted)] transition-colors hover:bg-ink-100 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/60 dark:hover:bg-ink-800">
          <Search className="h-5 w-5" />
        </div>
      </button>

        <div className="ml-auto flex items-center gap-1">
          <UpdateNotification />
          <ProfileMenu />
        </div>
      </div>
    </header>
  );
}

function ProfileMenu() {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const qc = useQueryClient();

  useEffect(() => {
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, []);

  // Close on Escape.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open]);

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="true"
        aria-expanded={open}
        className="flex h-11 items-center gap-2.5 rounded-xl px-2 transition-colors hover:bg-ink-100 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-400/60 dark:hover:bg-ink-800"
      >
        <div className="flex h-8 w-8 items-center justify-center rounded-full bg-accent-600 text-xs font-semibold text-white">
          K
        </div>
        <div className="hidden text-left sm:block">
          <p className="text-sm font-medium leading-tight">Kei</p>
          <p className="text-xs leading-tight text-[var(--text-muted)]">AI Bender</p>
        </div>
        <ChevronDown className="h-4 w-4 text-[var(--text-muted)]" />
      </button>

      {open && (
        <div
          role="menu"
          className="absolute right-0 top-full z-50 mt-2 w-48 overflow-hidden rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] py-1 shadow-[var(--shadow-float)]"
        >
          <div className="px-4 py-3">
            <p className="text-sm font-medium">Kei</p>
            <p className="text-xs text-[var(--text-muted)]">AI Bender</p>
          </div>
          <div className="my-1 h-px bg-[var(--border)]" />
          
          <div className="py-1">
            <button
              role="menuitem"
              onClick={async () => {
                await api.logout();
                qc.invalidateQueries({ queryKey: ["auth-status"] });
              }}
              className="flex w-full items-center gap-2.5 px-4 py-2 text-left text-sm text-danger transition-colors hover:bg-danger/10 focus:outline-none focus-visible:bg-danger/10"
            >
              <LogOut className="h-4 w-4" strokeWidth={2} />
              Sign out
            </button>
          </div>
        </div>
      )}
    </div>
  );
}



export function PageHeader({
  title,
  description,
  icon: Icon,
  action,
}: {
  title: string;
  description?: string;
  icon?: LucideIcon;
  action?: ReactNode;
}) {
  return (
    <div className="mb-7 flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
      <div className="flex min-w-0 items-start gap-3.5">
        {Icon && (
          <div className="mt-0.5 flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-accent-200 bg-accent-100 text-accent-700 shadow-sm dark:border-accent-800 dark:bg-accent-900/40 dark:text-accent-300">
            <Icon className="h-5 w-5" strokeWidth={2} />
          </div>
        )}
        <div className="min-w-0">
          <h1 className="font-display text-2xl font-semibold tracking-tight sm:text-3xl">{title}</h1>
          {description && <p className="mt-1.5 max-w-3xl text-sm leading-6 text-[var(--text-muted)]">{description}</p>}
        </div>
      </div>
      {action && <div className="flex shrink-0 flex-wrap items-center gap-2">{action}</div>}
    </div>
  );
}
