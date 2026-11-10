import { lazy, Suspense, useId, useState, useEffect, type ReactNode } from "react";
import { Link, Outlet, useLocation } from "react-router-dom";
import { useQuery, useQueryClient, useIsFetching } from "@tanstack/react-query";
import {
  BarChart3,
  Boxes,
  History,
  Image,
  KeyRound,
  LayoutGrid,
  ListChecks,
  Layers,
  Loader2,
  LogOut,
  Menu,
  MessagesSquare,
  Monitor,
  NotebookPen,
  PanelLeftClose,
  PanelLeftOpen,
  Moon,
  Plug,
  ReceiptText,
  ScrollText,
  Search,
  Settings,
  Shield,
  Sun,
  Waypoints,
  Wrench,
  X,
  type LucideIcon,
} from "lucide-react";
import { api } from "../lib/api";
import { cn } from "@/lib/utils";
import { useBranding } from "../contexts/BrandingContext";
import { useTheme, type Theme } from "./ThemeProvider";
import { BrandMark } from "./BrandMark";
import { UpdateNotification } from "./UpdateNotification";
import { NavIndicator } from "./NavIndicator";
import { ThemeToggle } from "./ThemeToggle";
import { preloadRoute, type RoutePreloadKey } from "../routePreload";

// The command palette (and cmdk) stays out of the shell bundle: it is fetched
// on idle or on the first Ctrl/Cmd+K, then mounted for the rest of the session.
const loadCommandPalette = () => import("./CommandPalette");
const CommandPalette = lazy(() => loadCommandPalette().then((m) => ({ default: m.CommandPalette })));
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
import { Sheet, SheetClose, SheetContent, SheetTitle } from "./ui/sheet";

interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
  preload?: RoutePreloadKey;
  /** Optional status indicator rendered at the end of the row. */
  indicator?: "live" | "health";
  /** Other pages this item owns, shown as a switch in their headers. */
  also?: string[];
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
      { to: "/", label: "Overview", icon: LayoutGrid, preload: "/" },
      { to: "/usage", label: "Usage", icon: BarChart3, preload: "/usage" },
      { to: "/console", label: "Console", icon: ScrollText, preload: "/console", indicator: "live" },
      { to: "/playground", label: "Playground", icon: MessagesSquare, preload: "/playground" },
    ],
  },
  {
    heading: "Routing",
    items: [
      { to: "/endpoints", label: "Endpoints", icon: Plug, preload: "/endpoints", also: ["/cli-tools"] },
      { to: "/chains", label: "Chains", icon: Layers, preload: "/chains" },
      { to: "/skills", label: "Skills", icon: NotebookPen, preload: "/skills" },
    ],
  },
  {
    heading: "Providers",
    items: [
      { to: "/providers", label: "Providers", icon: Boxes, preload: "/providers", indicator: "health", also: ["/provider-health", "/quota"] },
      { to: "/media", label: "Media", icon: Image, preload: "/media" },
      { to: "/proxy-pools", label: "Proxy pools", icon: Waypoints, preload: "/proxy-pools" },
    ],
  },
  {
    heading: "MCP",
    items: [
      { to: "/mcp-servers", label: "Servers", icon: Wrench, preload: "/mcp-servers" },
      { to: "/mcp-servers/calls", label: "Tool calls", icon: ListChecks, preload: "/mcp-calls" },
    ],
  },
  {
    heading: "Access",
    items: [
      { to: "/keys", label: "API keys", icon: KeyRound, preload: "/keys" },
      { to: "/plans", label: "Plans & budgets", icon: ReceiptText, preload: "/plans" },
      { to: "/guardrails", label: "Guardrails", icon: Shield, preload: "/guardrails" },
    ],
  },
  {
    heading: "Workspace",
    items: [
      { to: "/audit", label: "Audit log", icon: History, preload: "/audit" },
      { to: "/settings", label: "Settings", icon: Settings, preload: "/settings", also: ["/system"] },
    ],
  },
];

// Pages that share one sidebar item. Each list page of a section shows a
// switch between its siblings in its header instead of its own nav row.
interface SectionLink {
  to: string;
  label: string;
  preload: RoutePreloadKey;
}

const sections: { label: string; links: SectionLink[] }[] = [
  {
    label: "Provider views",
    links: [
      { to: "/providers", label: "Connections", preload: "/providers" },
      { to: "/provider-health", label: "Health", preload: "/provider-health" },
      { to: "/quota", label: "Quota", preload: "/quota" },
    ],
  },
  {
    label: "Endpoint views",
    links: [
      { to: "/endpoints", label: "Endpoints", preload: "/endpoints" },
      { to: "/cli-tools", label: "CLI tools", preload: "/cli-tools" },
    ],
  },
  {
    label: "Settings views",
    links: [
      { to: "/settings", label: "Settings", preload: "/settings" },
      { to: "/system", label: "System", preload: "/system" },
    ],
  },
];

function sectionFor(pathname: string) {
  return sections.find((s) => s.links.some((l) => l.to === pathname)) ?? null;
}

// activeSection finds the nav item (and its group) that owns a path, so detail
// pages such as /providers/:id inherit their list page's place in the nav.
function activeSection(pathname: string): { item: NavItem; heading?: string } | null {
  let best: { item: NavItem; heading?: string; len: number } | null = null;
  for (const group of navGroups) {
    for (const item of group.items) {
      for (const to of [item.to, ...(item.also ?? [])]) {
        const matches = to === "/" ? pathname === "/" : pathname === to || pathname.startsWith(to + "/");
        if (matches && (!best || to.length > best.len)) best = { item, heading: group.heading, len: to.length };
      }
    }
  }
  return best;
}

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
  "/playground": "Playground",
  "/cli-tools": "CLI tools",
  "/system": "System",
  "/audit": "Audit log",
  "/mcp-servers": "MCP servers",
  "/mcp-servers/calls": "Tool calls",
};

const TITLE_BY_PREFIX: [string, string][] = [
  ["/providers/", "Provider"],
  ["/cli-tools/", "CLI tool"],
  ["/media/", "Media"],
  ["/keys/", "API key"],
  ["/chains/", "Chain"],
  ["/provider-health/", "Provider health"],
  ["/mcp-servers/", "MCP server"],
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
  const [collapsed, setCollapsed] = useCollapsedSidebar();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [paletteMounted, setPaletteMounted] = useState(false);
  useEffect(() => {
    if (paletteOpen) setPaletteMounted(true);
  }, [paletteOpen]);

  // Cmd+K / Ctrl+K opens the command palette; Cmd+B / Ctrl+B folds the sidebar.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey)) return;
      const key = e.key.toLowerCase();
      if (key === "k") {
        e.preventDefault();
        setPaletteOpen((v) => !v);
      } else if (key === "b") {
        e.preventDefault();
        setCollapsed((v) => !v);
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
      void loadCommandPalette().catch(() => {});
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
    // The shell is locked to the viewport: only <main> (and the sidebar nav)
    // scroll. The sidebar sits on the desk; from lg the content is a sheet of
    // paper floating on it with a hairline and a 1px lip.
    <div className="relative flex h-full overflow-hidden bg-sidebar">
      <SkipLink />
      <div className="hidden lg:flex">
        <Sidebar collapsed={collapsed} />
      </div>

      {/* Mobile navigation drawer. */}
      <Sheet open={sidebarOpen} onOpenChange={setSidebarOpen}>
        <SheetContent id={MOBILE_NAV_ID} side="left" className="w-64 bg-sidebar lg:hidden" aria-describedby={undefined}>
          <SheetTitle className="sr-only">Navigation</SheetTitle>
          <SheetClose
            className="press absolute right-2 top-[calc(env(safe-area-inset-top)+0.5rem)] z-10 flex size-10 items-center justify-center rounded-lg text-fg-muted hover:bg-sidebar-accent hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            aria-label="Close navigation"
          >
            <X className="size-4" aria-hidden="true" />
          </SheetClose>
          <Sidebar collapsed={false} drawer />
        </SheetContent>
      </Sheet>

      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-canvas lg:my-2 lg:mr-2 lg:rounded-2xl lg:border lg:border-line lg:shadow-paper">
        <RouteProgress />
        <TopBar
          menuOpen={sidebarOpen}
          onMenuToggle={() => setSidebarOpen(true)}
          collapsed={collapsed}
          onCollapseToggle={() => setCollapsed((v) => !v)}
          onSearchOpen={() => setPaletteOpen(true)}
        />
        {/* tabIndex -1: the skip link moves focus here; no ring on a landmark. */}
        <main
          id="main"
          tabIndex={-1}
          className="relative min-h-0 flex-1 overflow-y-auto overscroll-contain pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)] pr-[env(safe-area-inset-right)] lg:pl-0 lg:pr-0"
          style={{ outline: "none" }}
        >
          {/* Full-bleed pages (the playground) lay out and scroll their own panes. */}
          <div className={FULL_BLEED.has(location.pathname) ? "h-full" : "mx-auto w-full max-w-6xl px-4 py-6 md:px-6 md:py-8"}>
            <Suspense fallback={<PageOutletFallback />}>
              <Outlet />
            </Suspense>
          </div>
        </main>
      </div>

      {paletteMounted && (
        <Suspense fallback={null}>
          <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
        </Suspense>
      )}
    </div>
  );
}

const FULL_BLEED = new Set(["/playground"]);

const COLLAPSED_KEY = "keirouter-sidebar-collapsed";

// useCollapsedSidebar keeps the desktop sidebar folded to icons across visits.
function useCollapsedSidebar() {
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return localStorage.getItem(COLLAPSED_KEY) === "1";
    } catch {
      return false;
    }
  });
  useEffect(() => {
    try {
      localStorage.setItem(COLLAPSED_KEY, collapsed ? "1" : "0");
    } catch {
      // Storage blocked: the choice lasts for this visit only.
    }
  }, [collapsed]);
  return [collapsed, setCollapsed] as const;
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
        main?.scrollTo({ top: 0 });
      }}
      className="fixed left-3 top-3 z-[120] -translate-y-[200%] rounded-lg bg-primary px-3 py-2 text-[13px] font-medium text-primary-fg shadow-lift focus:translate-y-0 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-canvas"
    >
      Skip to main content
    </a>
  );
}

function PageOutletFallback() {
  return (
    <div className="flex min-h-[240px] items-center justify-center py-16" role="status">
      <Loader2 className="size-5 animate-spin text-fg-muted" aria-hidden="true" />
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

function Sidebar({ collapsed, drawer = false }: { collapsed: boolean; /** Inside the mobile sheet, beside its close button. */ drawer?: boolean }) {
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
  const { pathname } = useLocation();
  const current = activeSection(pathname)?.item;

  return (
    <aside
      className={cn(
        "relative flex h-full shrink-0 flex-col bg-sidebar transition-[width] duration-200 ease-out-soft",
        collapsed ? "w-[3.75rem]" : "w-60",
      )}
    >
      <SidebarBrand collapsed={collapsed} drawer={drawer} />

      <nav aria-label="Primary" className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden overscroll-contain px-2.5 pb-4 pt-1">
        <div className="relative">
          <NavIndicator className="border border-line bg-surface shadow-paper" />
          {navGroups.map((group, gi) => (
            <div key={gi} className={gi > 0 ? "mt-5" : undefined}>
              {group.heading &&
                (collapsed ? (
                  <div className="mx-2 mb-2 border-t border-line" aria-hidden="true" />
                ) : (
                  <h2 id={`${navId}-group-${gi}`} className="px-2.5 pb-1.5 text-[12px] font-medium text-fg-faint">
                    {group.heading}
                  </h2>
                ))}
              <ul className="space-y-px" aria-labelledby={group.heading && !collapsed ? `${navId}-group-${gi}` : undefined}>
                {group.items.map((item) => (
                  <li key={item.to}>
                    <Link
                      to={item.to}
                      aria-current={item === current ? "page" : undefined}
                      aria-label={collapsed ? item.label : undefined}
                      title={collapsed ? item.label : undefined}
                      onMouseEnter={() => item.preload && preloadRoute(item.preload)}
                      onFocus={() => item.preload && preloadRoute(item.preload)}
                      onTouchStart={() => item.preload && preloadRoute(item.preload)}
                      className={cn(
                        "group relative flex h-8 items-center gap-2.5 rounded-lg px-2.5 text-[13px] font-medium transition-colors pointer-coarse:h-11 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                        item === current ? "text-fg" : "text-fg-muted hover:bg-sidebar-accent hover:text-fg",
                      )}
                    >
                      <item.icon
                        className={cn("size-4 shrink-0 transition-colors", item === current ? "text-brand-ink" : "text-fg-faint group-hover:text-fg-muted")}
                        aria-hidden="true"
                      />
                      {!collapsed && <span className="truncate">{item.label}</span>}
                      {item.indicator === "live" && (
                        <span
                          className={cn("live-dot size-1.5 rounded-full bg-ok text-ok", collapsed ? "absolute right-1.5 top-1.5" : "ml-auto")}
                          role="img"
                          aria-label="Live"
                        />
                      )}
                      {item.indicator === "health" && healthTone && (
                        <span
                          className={cn("size-1.5 rounded-full", healthTone === "bad" ? "bg-bad" : "bg-warn", collapsed ? "absolute right-1.5 top-1.5" : "ml-auto")}
                          role="img"
                          aria-label={healthTone === "bad" ? "A provider is unhealthy" : "A provider is degraded"}
                        />
                      )}
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </nav>

      {!collapsed && <GatewayStatus />}
    </aside>
  );
}

function SidebarBrand({ collapsed, drawer }: { collapsed: boolean; drawer: boolean }) {
  const { branding } = useBranding();
  const info = useGatewayInfo();
  const name = branding.name || "KeiRouter";
  return (
    <Link
      to="/"
      aria-label={collapsed ? name : undefined}
      className={cn("mx-2.5 mt-2 flex h-12 shrink-0 items-center gap-2.5 rounded-lg px-2 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring", drawer && "mr-12")}
    >
      {branding.logo_url && !collapsed ? (
        <img src={branding.logo_url} alt={name} className="h-7 max-w-[150px] object-contain object-left" />
      ) : (
        <>
          <BrandMark />
          {!collapsed && <span className="text-[15px] font-medium tracking-[-0.02em] text-fg">{name}</span>}
        </>
      )}
      {info.data?.version && !collapsed && (
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
    <section aria-label="Gateway status" className="mx-2.5 mb-2.5 border-t border-line px-2.5 pb-1 pt-3">
      <div className="flex items-center gap-2 text-[12px] font-medium text-fg" role="status">
        <span
          className={cn("h-1.5 w-1.5 shrink-0 rounded-full", info.isError ? "bg-bad" : data ? "live-dot bg-ok" : "bg-fg-faint")}
          aria-hidden="true"
        />
        <span className="truncate">{label}</span>
        {data && <span className="ml-auto shrink-0 text-[11.5px] font-normal tabular-nums text-fg-faint">Up {formatUptime(data.uptime_s)}</span>}
      </div>
      {data && (
        <div className="mt-0.5 flex items-center justify-between gap-2 text-[11.5px]">
          <span className="truncate font-mono text-fg-muted" title={data.listen_addr}>
            {data.listen_addr}
          </span>
          <span className="shrink-0 text-fg-faint">{data.dialect === "postgres" ? "Postgres" : "SQLite"}</span>
        </div>
      )}
    </section>
  );
}

function TopBar({
  menuOpen,
  onMenuToggle,
  collapsed,
  onCollapseToggle,
  onSearchOpen,
}: {
  menuOpen: boolean;
  onMenuToggle: () => void;
  collapsed: boolean;
  onCollapseToggle: () => void;
  onSearchOpen: () => void;
}) {
  const { branding } = useBranding();
  const { pathname } = useLocation();
  const name = branding.name || "KeiRouter";
  const section = activeSection(pathname);
  // A page that shares its sidebar item (Health under Providers) names itself
  // after the item in the breadcrumb.
  const sub = sections
    .flatMap((s) => s.links)
    .find((l) => l.to !== section?.item.to && (pathname === l.to || pathname.startsWith(l.to + "/")));
  const iconButton =
    "press flex size-9 shrink-0 items-center justify-center rounded-lg text-fg-muted hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-ring pointer-coarse:size-11";
  return (
    // Solid, no blur: the header is part of the paper. Padded for notches.
    <header className="flex h-[calc(3.5rem+env(safe-area-inset-top))] shrink-0 items-center gap-2 border-b border-line bg-canvas pl-[max(0.75rem,env(safe-area-inset-left))] pr-[max(0.75rem,env(safe-area-inset-right))] pt-[env(safe-area-inset-top)] md:gap-3 md:px-4">
      <button
        type="button"
        onClick={onMenuToggle}
        aria-label="Open navigation"
        aria-expanded={menuOpen}
        aria-controls={MOBILE_NAV_ID}
        aria-haspopup="dialog"
        className={cn(iconButton, "lg:hidden")}
      >
        <Menu className="size-[18px]" aria-hidden="true" />
      </button>
      <button
        type="button"
        onClick={onCollapseToggle}
        aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
        aria-keyshortcuts="Meta+B Control+B"
        className={cn(iconButton, "hidden lg:flex")}
      >
        {collapsed ? <PanelLeftOpen aria-hidden="true" /> : <PanelLeftClose aria-hidden="true" />}
      </button>
      <span className="hidden h-5 w-px bg-line lg:block" aria-hidden="true" />

      {/* Phones: the product identity, since the sidebar is hidden. From sm:
          where you are, in sentence case. */}
      <Link to="/" className="flex min-w-0 items-center gap-2 rounded-lg focus:outline-none focus-visible:ring-2 focus-visible:ring-ring sm:hidden">
        {branding.logo_url ? (
          <img src={branding.logo_url} alt={name} className="h-6 max-w-[120px] object-contain object-left" />
        ) : (
          <>
            <BrandMark />
            <span className="truncate text-[14px] font-medium text-fg">{name}</span>
          </>
        )}
      </Link>
      {section && (
        <p className="hidden min-w-0 items-center gap-1.5 truncate text-[13px] sm:flex">
          {section.heading && section.heading !== section.item.label && (
            <>
              <span className="text-fg-faint">{section.heading}</span>
              <span className="text-fg-faint" aria-hidden="true">/</span>
            </>
          )}
          {sub ? (
            <>
              <span className="text-fg-faint">{section.item.label}</span>
              <span className="text-fg-faint" aria-hidden="true">/</span>
              <span className="truncate font-medium text-fg">{sub.label}</span>
            </>
          ) : (
            <span className="truncate font-medium text-fg">{section.item.label}</span>
          )}
        </p>
      )}

      <div className="ml-auto flex shrink-0 items-center gap-1">
        {/* Name contains the visible text (WCAG 2.5.3); below md it is an icon
            button named by the same label. */}
        <button
          type="button"
          onClick={onSearchOpen}
          aria-label={`Search ${name}`}
          aria-haspopup="dialog"
          aria-keyshortcuts="Meta+K Control+K"
          className="press flex size-9 shrink-0 items-center justify-center gap-2 rounded-lg text-fg-muted hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-ring pointer-coarse:size-11 md:mr-1 md:h-8 md:w-56 md:justify-start md:border md:border-line md:bg-surface md:px-2.5 md:text-[13px] md:text-fg-faint md:shadow-paper md:hover:border-input md:hover:bg-surface"
        >
          <Search className="size-4 shrink-0" aria-hidden="true" />
          <span className="hidden truncate md:inline">Search…</span>
          <kbd className="ml-auto hidden rounded-md border border-line px-1.5 font-mono text-[10.5px] text-fg-faint md:inline" aria-hidden="true">⌘K</kbd>
        </button>
        <UpdateNotification />
        <ThemeToggle />
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
        className="ml-1 flex size-8 items-center justify-center rounded-full bg-brand-soft font-display text-[13px] text-brand-ink transition-colors hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-ring pointer-coarse:size-10"
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

// PageHeader is the title row at the top of every page: an eyebrow naming
// the nav group (with the brand rule), the title, one line of description and
// the page's actions. No icon chip.
export function PageHeader({ title, description, action }: { title: string; description?: string; action?: ReactNode }) {
  const { pathname } = useLocation();
  const heading = activeSection(pathname)?.heading;
  const eyebrow = heading && heading !== title ? heading : undefined;
  const section = sectionFor(pathname);
  return (
    <div className="mb-8 flex flex-wrap items-end justify-between gap-x-6 gap-y-4 md:mb-10">
      <div className="min-w-[min(100%,280px)] flex-1">
        {section ? (
          <SectionSwitch label={section.label} links={section.links} pathname={pathname} />
        ) : eyebrow && (
          <p className="mb-2 flex items-center gap-2 text-meta text-fg-muted">
            <span className="h-px w-5 bg-brand-ink" aria-hidden="true" />
            {eyebrow}
          </p>
        )}
        <h1 className="font-display text-display-2 text-balance text-fg">{title}</h1>
        {description && <p className="mt-2 max-w-2xl text-body-lg text-fg-muted">{description}</p>}
      </div>
      {action && <div className="flex min-w-0 max-w-full flex-wrap items-center gap-2">{action}</div>}
    </div>
  );
}

// SectionSwitch links the pages that share one sidebar item. It looks like
// SegmentedControl but each option is a link, so the pages keep their URLs.
function SectionSwitch({ label, links, pathname }: { label: string; links: SectionLink[]; pathname: string }) {
  return (
    <nav aria-label={label} className="mb-3">
      <ul className="inline-flex max-w-full overflow-x-auto overscroll-x-contain rounded-xl bg-subtle p-0.5 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
        {links.map((l) => {
          const active = l.to === pathname;
          return (
            <li key={l.to} className="shrink-0">
              <Link
                to={l.to}
                aria-current={active ? "page" : undefined}
                onMouseEnter={() => preloadRoute(l.preload)}
                onFocus={() => preloadRoute(l.preload)}
                className={cn(
                  "press flex h-7 items-center whitespace-nowrap rounded-lg px-2.5 text-[12px] font-medium pointer-coarse:h-11 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                  active ? "bg-surface text-fg shadow-paper ring-1 ring-line" : "text-fg-muted hover:text-fg",
                )}
              >
                {l.label}
              </Link>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}

function shortVersion(version: string): string {
  if (!version || version === "dev") return "dev";
  return version.startsWith("v") ? version : `v${version}`;
}

export function formatUptime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "n/a";
  const d = Math.floor(seconds / 86_400);
  const h = Math.floor((seconds % 86_400) / 3_600);
  const m = Math.floor((seconds % 3_600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${Math.max(1, m)}m`;
}
