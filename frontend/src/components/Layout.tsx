import { Suspense, useId, useState, useEffect, type ReactNode } from "react";
import { Link, NavLink, Outlet, useLocation } from "react-router-dom";
import { useQuery, useQueryClient, useIsFetching } from "@tanstack/react-query";
import {
  Activity,
  BarChart3,
  Boxes,
  Cpu,
  Gauge,
  Image,
  Key,
  LayoutGrid,
  Layers,
  LogOut,
  Menu,
  Monitor,
  Moon,
  Plug,
  ReceiptText,
  ScrollText,
  Search,
  Settings,
  Shield,
  Sparkles,
  Sun,
  TerminalSquare,
  Waypoints,
  type LucideIcon,
} from "lucide-react";
import { api } from "../lib/api";
import { cn } from "@/lib/utils";
import { useBranding } from "../contexts/BrandingContext";
import { useTheme, type Theme } from "./ThemeProvider";
import { BrandMark } from "./BrandMark";
import { CommandPalette } from "./CommandPalette";
import { UpdateNotification } from "./UpdateNotification";
import { preloadRoute, type RoutePreloadKey } from "../routePreload";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { Sheet, SheetContent, SheetTitle } from "./ui/sheet";

interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
  end?: boolean;
  preload?: RoutePreloadKey;
  /** Optional status indicator rendered at the end of the row. */
  indicator?: "live" | "health";
}

interface NavGroup {
  heading?: string;
  items: NavItem[];
}

// Grouped by the job the operator is doing, not by feature origin: watch
// traffic, shape routing, manage upstreams, control access, run the instance.
const navGroups: NavGroup[] = [
  {
    items: [
      { to: "/", label: "Overview", icon: LayoutGrid, end: true, preload: "/" },
      { to: "/usage", label: "Usage", icon: BarChart3, preload: "/usage" },
      { to: "/console", label: "Console", icon: ScrollText, preload: "/console", indicator: "live" },
    ],
  },
  {
    heading: "Routing",
    items: [
      { to: "/endpoints", label: "Endpoints", icon: Plug, preload: "/endpoints" },
      { to: "/chains", label: "Chains", icon: Layers, preload: "/chains" },
      { to: "/skills", label: "Skills", icon: Sparkles, preload: "/skills" },
    ],
  },
  {
    heading: "Providers",
    items: [
      { to: "/providers", label: "Providers", icon: Boxes, preload: "/providers" },
      { to: "/media", label: "Media", icon: Image, preload: "/media" },
      { to: "/provider-health", label: "Health", icon: Activity, preload: "/provider-health", indicator: "health" },
      { to: "/quota", label: "Quota", icon: Gauge, preload: "/quota" },
      { to: "/proxy-pools", label: "Proxy pools", icon: Waypoints, preload: "/proxy-pools" },
    ],
  },
  {
    heading: "Access",
    items: [
      { to: "/keys", label: "API keys", icon: Key, preload: "/keys" },
      { to: "/plans", label: "Plans & budgets", icon: ReceiptText, preload: "/plans" },
      { to: "/guardrails", label: "Guardrails", icon: Shield, preload: "/guardrails" },
    ],
  },
  {
    heading: "Workspace",
    items: [
      { to: "/cli-tools", label: "CLI tools", icon: TerminalSquare, preload: "/cli-tools" },
      { to: "/system", label: "System", icon: Cpu, preload: "/system" },
      { to: "/settings", label: "Settings", icon: Settings, preload: "/settings" },
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
  "/proxy-pools": "Proxy pools",
  "/usage": "Usage",
  "/plans": "Plans & budgets",
  "/budgets": "Plans & budgets",
  "/quota": "Quota",
  "/settings": "Settings",
  "/keys": "API keys",
  "/guardrails": "Guardrails",
  "/provider-health": "Provider health",
  "/console": "Console",
  "/cli-tools": "CLI tools",
  "/system": "System",
};

const TITLE_BY_PREFIX: [string, string][] = [
  ["/providers/", "Provider"],
  ["/cli-tools/", "CLI tool"],
  ["/media/", "Media"],
  ["/keys/", "API key"],
  ["/chains/", "Chain"],
  ["/provider-health/", "Provider health"],
];

function titleForPath(pathname: string): string {
  const exact = TITLE_BY_PATH[pathname];
  if (exact) return exact;
  for (const [prefix, label] of TITLE_BY_PREFIX) {
    if (pathname.startsWith(prefix)) return label;
  }
  return "";
}

export function Layout() {
  const location = useLocation();
  const { branding } = useBranding();
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [paletteOpen, setPaletteOpen] = useState(false);

  // Cmd+K / Ctrl+K to open command palette.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen((v) => !v);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  // Close the mobile drawer whenever the route changes.
  useEffect(() => {
    setSidebarOpen(false);
  }, [location.pathname]);

  // Set browser tab title from current route.
  useEffect(() => {
    const label = titleForPath(location.pathname);
    const appName = branding.name || "KeiRouter";
    document.title = label ? `${appName} - ${label}` : appName;
  }, [location.pathname, branding.name]);

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
    <div className="flex h-full bg-canvas">
      <SkipLink />
      {/* Desktop sidebar — hidden below lg. */}
      <div className="hidden lg:flex">
        <Sidebar />
      </div>

      {/* Mobile navigation drawer. */}
      <Sheet open={sidebarOpen} onOpenChange={setSidebarOpen}>
        <SheetContent id={MOBILE_NAV_ID} side="left" className="w-60 lg:hidden" aria-describedby={undefined}>
          <SheetTitle className="sr-only">Navigation</SheetTitle>
          <Sidebar />
        </SheetContent>
      </Sheet>

      <div className="flex min-w-0 flex-1 flex-col">
        <RouteProgress />
        <TopBar menuOpen={sidebarOpen} onMenuToggle={() => setSidebarOpen(true)} onSearchOpen={() => setPaletteOpen(true)} />
        {/* tabIndex -1: the skip link moves focus here; no ring on a landmark. */}
        <main id="main" tabIndex={-1} className="flex-1 overflow-y-auto" style={{ outline: "none" }}>
          <div className="mx-auto w-full max-w-[1400px] px-4 py-6 sm:px-6 lg:px-8">
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

const MOBILE_NAV_ID = "mobile-navigation";

// SkipLink is the first focusable element: hidden until focused, it jumps
// keyboard users past the sidebar and top bar (WCAG 2.4.1). Focus is moved in
// JS so the URL hash (used by some pages) is left untouched.
function SkipLink() {
  return (
    <a
      href="#main"
      onClick={(e) => {
        e.preventDefault();
        const main = document.getElementById("main");
        main?.focus();
        main?.scrollIntoView({ block: "start" });
      }}
      className="fixed left-3 top-3 z-[120] -translate-y-[200%] rounded-lg bg-primary px-3 py-2 text-[13px] font-medium text-primary-fg shadow-[var(--shadow-pop)] focus:translate-y-0 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 focus-visible:ring-offset-2 focus-visible:ring-offset-canvas"
    >
      Skip to main content
    </a>
  );
}

function PageOutletFallback() {
  return (
    <div className="flex min-h-[240px] items-center justify-center py-16" role="status">
      <div className="h-5 w-5 animate-spin rounded-full border-2 border-line-strong border-t-fg-muted" aria-hidden="true" />
      <span className="sr-only">Loading page</span>
    </div>
  );
}

// RouteProgress shows a thin indeterminate bar at the top of the content area
// whenever queries are in flight, so a nav click gets immediate feedback even
// while the next route's chunk and data are still loading.
function RouteProgress() {
  const fetching = useIsFetching();
  if (fetching === 0) return null;
  return <div className="route-progress" role="progressbar" aria-label="Loading" />;
}

function useGatewayInfo() {
  return useQuery({
    queryKey: ["gateway-info"],
    queryFn: () => api.gatewayInfo(),
    staleTime: 60_000,
    refetchInterval: 60_000,
  });
}

function Sidebar() {
  const health = useQuery({
    queryKey: ["health-overview-nav"],
    queryFn: () => api.healthOverview("1h"),
    staleTime: 60_000,
    refetchInterval: 60_000,
  });
  const summary = health.data?.summary;
  const healthTone = summary ? (summary.unhealthy > 0 ? "bad" : summary.degraded > 0 ? "warn" : null) : null;
  // Sidebar renders twice (desktop + mobile drawer), so heading ids are unique per instance.
  const navId = useId();

  return (
    <aside className="flex h-full w-60 shrink-0 flex-col border-r border-line bg-surface">
      <SidebarBrand />

      <nav aria-label="Primary" className="flex-1 overflow-y-auto px-2.5 pb-4 pt-2">
        {navGroups.map((group, gi) => (
          <div key={gi} className={gi > 0 ? "mt-5" : undefined}>
            {group.heading && (
              <h2 id={`${navId}-group-${gi}`} className="px-2.5 pb-1.5 text-[11.5px] font-medium text-fg-faint">
                {group.heading}
              </h2>
            )}
            <ul className="space-y-px" aria-labelledby={group.heading ? `${navId}-group-${gi}` : undefined}>
              {group.items.map((item) => (
                <li key={item.to}>
                  <NavLink
                    to={item.to}
                    end={item.end}
                    aria-current="page"
                    onMouseEnter={() => item.preload && preloadRoute(item.preload)}
                    onFocus={() => item.preload && preloadRoute(item.preload)}
                    onTouchStart={() => item.preload && preloadRoute(item.preload)}
                    className={({ isActive }) =>
                      cn(
                        "group flex h-8 items-center gap-2.5 rounded-lg px-2.5 text-[13px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
                        isActive ? "bg-hover text-fg" : "text-fg-muted hover:bg-hover hover:text-fg",
                      )
                    }
                  >
                    {({ isActive }) => (
                      <>
                        <item.icon
                          className={cn("h-4 w-4 shrink-0", isActive ? "text-fg" : "text-fg-faint group-hover:text-fg-muted")}
                          strokeWidth={1.75}
                          aria-hidden="true"
                        />
                        <span className="truncate">{item.label}</span>
                        {item.indicator === "live" && (
                          <span className="live-dot ml-auto h-1.5 w-1.5 rounded-full bg-ok" role="img" aria-label="Live" />
                        )}
                        {item.indicator === "health" && healthTone && (
                          <span
                            className={cn("ml-auto h-1.5 w-1.5 rounded-full", healthTone === "bad" ? "bg-bad" : "bg-warn")}
                            role="img"
                            aria-label={healthTone === "bad" ? "A provider is unhealthy" : "A provider is degraded"}
                          />
                        )}
                      </>
                    )}
                  </NavLink>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </nav>

      <GatewayStatus />
    </aside>
  );
}

function SidebarBrand() {
  const { branding } = useBranding();
  const info = useGatewayInfo();
  const name = branding.name || "KeiRouter";
  return (
    <Link
      to="/"
      className="flex h-14 shrink-0 items-center gap-2.5 border-b border-line px-4 focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500"
    >
      {branding.logo_url ? (
        <img src={branding.logo_url} alt={name} className="h-7 max-w-[150px] object-contain object-left" />
      ) : (
        <>
          <BrandMark />
          <span className="text-[14px] font-semibold tracking-[-0.01em] text-fg">{name}</span>
        </>
      )}
      {info.data?.version && (
        <span className="ml-auto font-mono text-[11px] text-fg-faint">{shortVersion(info.data.version)}</span>
      )}
    </Link>
  );
}

function GatewayStatus() {
  const info = useGatewayInfo();
  const data = info.data;
  // The state is always written out; the dot only repeats it.
  const label = info.isError ? "Gateway unreachable" : data ? "Gateway running" : "Checking gateway…";
  return (
    <section aria-label="Gateway status" className="m-2.5 mt-0 rounded-2xl border border-line bg-subtle px-3 py-2.5">
      <div className="flex items-center gap-2 text-[12px] font-medium text-fg" role="status">
        <span
          className={cn("h-1.5 w-1.5 rounded-full", info.isError ? "bg-bad" : data ? "live-dot bg-ok" : "bg-fg-faint")}
          aria-hidden="true"
        />
        {label}
      </div>
      {data && (
        <>
          <p className="mt-1 truncate font-mono text-[11.5px] text-fg-muted" title={data.listen_addr}>
            {data.listen_addr}
          </p>
          <div className="mt-1 flex justify-between text-[11.5px] text-fg-faint">
            <span>Up {formatUptime(data.uptime_s)}</span>
            <span>{data.dialect === "postgres" ? "Postgres" : "SQLite"}</span>
          </div>
        </>
      )}
    </section>
  );
}

function TopBar({ menuOpen, onMenuToggle, onSearchOpen }: { menuOpen: boolean; onMenuToggle: () => void; onSearchOpen: () => void }) {
  const { branding } = useBranding();
  return (
    <header className="flex h-14 shrink-0 items-center gap-3 border-b border-line bg-surface px-4 sm:px-6 lg:px-8">
      <button
        type="button"
        onClick={onMenuToggle}
        aria-label="Open navigation"
        aria-expanded={menuOpen}
        aria-controls={MOBILE_NAV_ID}
        aria-haspopup="dialog"
        className="-ml-1 flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 lg:hidden"
      >
        <Menu className="h-[18px] w-[18px]" aria-hidden="true" />
      </button>

      {/* Name = visible text (WCAG 2.5.3); the shortcut is exposed separately. */}
      <button
        type="button"
        onClick={onSearchOpen}
        aria-haspopup="dialog"
        aria-keyshortcuts="Meta+K Control+K"
        className="flex h-9 min-w-0 flex-1 items-center gap-2 rounded-lg border border-input bg-subtle px-3 text-left text-[13px] text-fg-faint transition-colors hover:border-fg-faint sm:max-w-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
      >
        <Search className="h-4 w-4 shrink-0" strokeWidth={1.75} aria-hidden="true" />
        <span className="truncate">Search {branding.name || "KeiRouter"}…</span>
        <kbd className="ml-auto hidden rounded border border-line bg-surface px-1.5 font-mono text-[10.5px] text-fg-faint sm:inline" aria-hidden="true">⌘K</kbd>
      </button>

      <div className="ml-auto flex items-center gap-1.5">
        <UpdateNotification />
        <AccountMenu />
      </div>
    </header>
  );
}

const THEME_OPTIONS: { value: Theme; label: string; icon: LucideIcon }[] = [
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
];

function AccountMenu() {
  const qc = useQueryClient();
  const { theme, setTheme } = useTheme();
  const { branding } = useBranding();
  const initial = (branding.name || "KeiRouter").slice(0, 1).toUpperCase();
  const themeLabelId = useId();

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label="Account and theme"
        className="flex h-8 w-8 items-center justify-center rounded-full border border-line bg-subtle text-[12px] font-semibold text-fg transition-colors hover:border-line-strong focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
      >
        <span aria-hidden="true">{initial}</span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        <DropdownMenuLabel>
          <span className="block text-[13px] font-medium text-fg">Administrator</span>
          <span className="block">Dashboard session</span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuLabel className="pb-1 pt-1" id={themeLabelId}>Theme</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={theme} onValueChange={(v) => setTheme(v as Theme)} aria-labelledby={themeLabelId}>
          {THEME_OPTIONS.map((opt) => (
            <DropdownMenuRadioItem key={opt.value} value={opt.value}>
              <opt.icon aria-hidden="true" />
              {opt.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link to="/settings">
            <Settings aria-hidden="true" />
            Settings
          </Link>
        </DropdownMenuItem>
        <DropdownMenuItem
          tone="danger"
          onSelect={async () => {
            await api.logout();
            qc.invalidateQueries({ queryKey: ["auth-status"] });
          }}
        >
          <LogOut aria-hidden="true" />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// PageHeader is the title row at the top of every page. `icon` is accepted for
// backwards compatibility with existing pages but intentionally not rendered:
// page titles carry no decorative icon chip.
export function PageHeader(props: {
  title: string;
  description?: string;
  icon?: LucideIcon;
  action?: ReactNode;
}) {
  const { title, description, action } = props;
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-x-6 gap-y-3">
      <div className="min-w-[min(100%,280px)] flex-1">
        <h1 className="text-[22px] font-semibold leading-tight tracking-[-0.02em] text-fg">{title}</h1>
        {description && <p className="mt-1 max-w-3xl text-[13.5px] leading-5 text-fg-muted">{description}</p>}
      </div>
      {action && <div className="flex shrink-0 flex-wrap items-center gap-2">{action}</div>}
    </div>
  );
}

function shortVersion(version: string): string {
  if (!version || version === "dev") return "dev";
  return version.startsWith("v") ? version : `v${version}`;
}

export function formatUptime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "—";
  const d = Math.floor(seconds / 86_400);
  const h = Math.floor((seconds % 86_400) / 3_600);
  const m = Math.floor((seconds % 3_600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${Math.max(1, m)}m`;
}
