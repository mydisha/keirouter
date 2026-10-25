import { useEffect, useId, useState } from "react";
import { Link, NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  BadgePercent, BarChart3, Bell, BookOpen, Key, Layers, LayoutGrid, LogOut, Menu, Monitor, Moon, Server, Sun, Wallet, X,
  type LucideIcon,
} from "lucide-react";
import { fetchPortalStatus, portalLogout, type LandingNotification } from "../../lib/api";
import { takePortalReturn } from "../../lib/portalReturn";
import { fetchPublicNotifications } from "../../lib/publicApi";
import { useBranding } from "../../contexts/BrandingContext";
import { useTheme, type Theme } from "../../components/ThemeProvider";
import { BrandMark } from "../../components/BrandMark";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel,
  DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuSeparator, DropdownMenuTrigger,
} from "../../components/ui/dropdown-menu";
import { Sheet, SheetClose, SheetContent, SheetTitle } from "../../components/ui/sheet";
import { cn } from "@/lib/utils";
import { portal } from "../../lib/portalRoutes";

const MAX_NOTIFICATIONS = 5;
const MOBILE_NAV_ID = "portal-mobile-navigation";

type Tone = "blue" | "violet" | "teal" | "orange" | "slate";
interface NavItem { to: string; label: string; icon: LucideIcon; end?: boolean }
interface NavGroup { heading?: string; tone: Tone; items: NavItem[] }

const NAV_GROUPS: NavGroup[] = [
  {
    tone: "blue",
    items: [
      { to: portal("/"), label: "Dashboard", icon: LayoutGrid, end: true },
      { to: portal("/usage"), label: "Usage", icon: BarChart3 },
    ],
  },
  {
    heading: "Access",
    tone: "orange",
    items: [
      { to: portal("/key"), label: "API Key", icon: Key },
      { to: portal("/models"), label: "Models", icon: Layers },
      { to: portal("/discounts"), label: "Discount Settings", icon: BadgePercent },
    ],
  },
  {
    heading: "Hosting",
    tone: "violet",
    items: [{ to: portal("/hosting"), label: "9router", icon: Server }],
  },
  {
    heading: "Billing",
    tone: "teal",
    items: [{ to: portal("/topup"), label: "Topup", icon: Wallet }],
  },
  {
    heading: "Help",
    tone: "slate",
    items: [{ to: portal("/docs"), label: "Docs", icon: BookOpen }],
  },
];

const TITLES: Record<string, string> = {
  "/portal": "Dashboard",
  "/portal/key": "API Key",
  "/portal/usage": "Usage",
  "/portal/models": "Models",
  "/portal/discounts": "Discount Settings",
  "/portal/topup": "Topup",
  "/portal/hosting": "Hosting 9router",
  "/portal/docs": "Docs",
};

// The nav group that owns a path lends its tone to the page, like the dashboard.
function toneForPath(pathname: string): Tone {
  const path = pathname.replace(/\/+$/, "") || "/portal";
  for (const g of NAV_GROUPS) {
    for (const item of g.items) {
      if (item.end ? path === item.to.replace(/\/+$/, "") : path.startsWith(item.to)) return g.tone;
    }
  }
  return "blue";
}

function Brand() {
  const { branding } = useBranding();
  const name = branding.name || "KeiRouter";
  return branding.logo_url ? (
    <img src={branding.logo_url} alt={name} className="h-7 max-w-[150px] object-contain object-left" />
  ) : (
    <>
      <BrandMark />
      <span className="truncate text-[14px] font-semibold tracking-[-0.01em] text-fg">{name}</span>
    </>
  );
}

function Sidebar({ onNavigate }: { onNavigate?: () => void }) {
  const navId = useId();
  return (
    <aside className="flex h-full w-60 shrink-0 flex-col border-r border-line bg-surface">
      <Link
        to={portal("/")}
        onClick={onNavigate}
        className="flex h-14 shrink-0 items-center gap-2.5 border-b border-line px-4 focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500"
      >
        <Brand />
        <span className="ml-auto rounded-md border border-line bg-subtle px-1.5 py-px text-[11px] font-medium text-fg-muted">Portal</span>
      </Link>
      <nav aria-label="Portal" className="min-h-0 flex-1 overflow-y-auto px-2.5 pb-4 pt-2">
        {NAV_GROUPS.map((group, gi) => (
          <div key={gi} data-tone={group.tone} className={gi > 0 ? "mt-4" : undefined}>
            {group.heading && (
              <h2 id={`${navId}-g${gi}`} className="px-2.5 pb-1.5 text-[11.5px] font-medium text-fg-faint">
                {group.heading}
              </h2>
            )}
            <ul className="space-y-px" aria-labelledby={group.heading ? `${navId}-g${gi}` : undefined}>
              {group.items.map((item) => (
                <li key={item.to}>
                  <NavLink
                    to={item.to}
                    end={item.end}
                    onClick={onNavigate}
                    className={({ isActive }) =>
                      cn(
                        "group flex h-8 items-center gap-2.5 rounded-lg px-2.5 text-[13px] font-medium transition-colors [@media(pointer:coarse)]:h-10 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
                        isActive ? "bg-tone-soft text-fg" : "text-fg-muted hover:bg-hover hover:text-fg",
                      )
                    }
                  >
                    {({ isActive }) => (
                      <>
                        <item.icon
                          className={cn("h-4 w-4 shrink-0 transition-colors", isActive ? "text-tone" : "text-fg-faint group-hover:text-tone")}
                          strokeWidth={1.75}
                          aria-hidden="true"
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
    </aside>
  );
}

function NotificationsPanel({ items }: { items: LandingNotification[] }) {
  const list = items.slice(0, MAX_NOTIFICATIONS);
  return (
    <div className="absolute right-0 top-full z-40 mt-2 w-[min(340px,calc(100vw-32px))] overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-pop)]">
      <div className="flex items-center justify-between border-b border-line px-4 py-2.5">
        <p className="text-[13px] font-semibold text-fg">Notifications</p>
        <span className="rounded-md bg-subtle px-1.5 py-px text-[11.5px] font-medium tabular-nums text-fg-muted">{list.length}</span>
      </div>
      <div className="max-h-[min(400px,60dvh)] overflow-y-auto p-1">
        {list.length === 0 ? (
          <p className="px-3 py-8 text-center text-[12.5px] text-fg-muted">No notifications yet.</p>
        ) : (
          <ul className="divide-y divide-line">
            {list.map((item) => (
              <li key={item.id} className="flex items-start gap-2.5 px-3 py-2.5">
                <span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-accent-500" aria-hidden="true" />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <p className="truncate text-[13px] font-medium text-fg">{item.title}</p>
                    {item.tag && (
                      <span className="shrink-0 rounded-md border border-accent-500/20 bg-accent-500/10 px-1.5 py-px text-[11px] font-medium text-link">
                        {item.tag}
                      </span>
                    )}
                  </div>
                  <p className="mt-0.5 text-[12px] leading-relaxed text-fg-muted">{item.body}</p>
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

const THEME_OPTIONS: { value: Theme; label: string; icon: LucideIcon }[] = [
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
];

function AccountMenu({ email, onLogout }: { email?: string; onLogout: () => void }) {
  const { theme, setTheme } = useTheme();
  const themeLabelId = useId();
  const initial = (email || "U").slice(0, 1).toUpperCase();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label="Account and theme"
        className="flex h-8 w-8 items-center justify-center rounded-full border border-line bg-subtle text-[12px] font-semibold text-fg transition-colors hover:border-line-strong focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 [@media(pointer:coarse)]:h-9 [@media(pointer:coarse)]:w-9"
      >
        <span aria-hidden="true">{initial}</span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-60">
        <DropdownMenuLabel>
          <span className="block text-[13px] font-medium text-fg">Signed in</span>
          <span className="block truncate">{email || "Portal session"}</span>
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
        <DropdownMenuItem tone="danger" onSelect={onLogout}>
          <LogOut aria-hidden="true" />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function PortalLayoutPage() {
  const location = useLocation();
  const { branding } = useBranding();
  const [open, setOpen] = useState(false);
  const [notifOpen, setNotifOpen] = useState(false);

  // Same cached query PortalRoot already resolved; read here for the email.
  const status = useQuery({ queryKey: ["portal-status"], queryFn: fetchPortalStatus, retry: false });

  const notif = useQuery({
    queryKey: ["public-notifications"],
    queryFn: fetchPublicNotifications,
    staleTime: 5 * 60 * 1000,
  });
  const notifCount = notif.data?.length ?? 0;
  const title = TITLES[location.pathname.replace(/\/+$/, "") || "/portal"] ?? "Portal";
  const tone = toneForPath(location.pathname);

  useEffect(() => {
    if (!notifOpen) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setNotifOpen(false); };
    const onDown = (e: MouseEvent) => {
      if (!(e.target as Element | null)?.closest?.("[data-portal-notif]")) setNotifOpen(false);
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onDown);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onDown);
    };
  }, [notifOpen]);

  useEffect(() => {
    document.title = `${branding.name || "KeiRouter"} - ${title}`;
  }, [title, branding.name]);

  // Back from Google sign-in: go to the page the visitor asked for.
  const navigate = useNavigate();
  useEffect(() => {
    const to = takePortalReturn();
    if (to && to !== location.pathname + location.search) navigate(to, { replace: true });
    // Only once, right after sign-in.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleLogout = async () => {
    await portalLogout();
    window.location.href = "/portal";
  };

  return (
    <div className="flex h-dvh overflow-hidden bg-canvas text-fg">
      <div className="hidden lg:flex">
        <Sidebar />
      </div>

      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent id={MOBILE_NAV_ID} side="left" className="w-60 lg:hidden" aria-describedby={undefined}>
          <SheetTitle className="sr-only">Navigation</SheetTitle>
          <SheetClose
            aria-label="Close navigation"
            className="absolute right-2 top-3 z-10 flex h-8 w-8 items-center justify-center rounded-lg text-fg-muted hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
          >
            <X className="h-4 w-4" aria-hidden="true" />
          </SheetClose>
          <Sidebar onNavigate={() => setOpen(false)} />
        </SheetContent>
      </Sheet>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-[calc(3.5rem+env(safe-area-inset-top))] shrink-0 items-center gap-2 border-b border-line bg-surface px-4 pt-[env(safe-area-inset-top)] sm:gap-3 sm:px-6 lg:px-8">
          <button
            type="button"
            onClick={() => setOpen(true)}
            aria-label="Open navigation"
            aria-expanded={open}
            aria-controls={MOBILE_NAV_ID}
            className="-ml-1 flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 lg:hidden"
          >
            <Menu className="h-[18px] w-[18px]" aria-hidden="true" />
          </button>
          <Link to={portal("/")} className="flex min-w-0 items-center gap-2 rounded-lg sm:hidden">
            <Brand />
          </Link>
          <nav aria-label="Breadcrumb" className="hidden min-w-0 items-center gap-1.5 text-[13px] sm:flex">
            <span className="text-fg-muted">Portal</span>
            <span className="text-fg-faint" aria-hidden="true">/</span>
            <span className="truncate font-medium text-fg" aria-current="page">{title}</span>
          </nav>

          <div className="ml-auto flex shrink-0 items-center gap-1.5">
            {status.data?.email && (
              <span className="mr-1 hidden max-w-[220px] truncate text-[12.5px] text-fg-muted md:inline">{status.data.email}</span>
            )}
            <div className="relative" data-portal-notif>
              <button
                type="button"
                onClick={() => setNotifOpen((v) => !v)}
                aria-haspopup="dialog"
                aria-expanded={notifOpen}
                aria-label="Notifications"
                title="Notifications"
                className="relative flex h-9 w-9 items-center justify-center rounded-lg text-fg-muted transition-colors hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
              >
                <Bell className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
                {notifCount > 0 && (
                  <span className="absolute right-1 top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-bad px-1 text-[9.5px] font-semibold text-white">
                    {notifCount > 9 ? "9+" : notifCount}
                  </span>
                )}
              </button>
              {notifOpen && <NotificationsPanel items={notif.data ?? []} />}
            </div>
            <AccountMenu email={status.data?.email} onLogout={handleLogout} />
          </div>
        </header>

        <main data-tone={tone} className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
          <div className="mx-auto w-full max-w-[1200px] px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}
