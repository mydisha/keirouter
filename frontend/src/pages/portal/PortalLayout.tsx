import { useState, useEffect, useCallback } from "react";
import { NavLink, Outlet, useLocation } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { BarChart3, Bell, Key, Layers, LayoutGrid, LogOut, Menu, Wallet, X } from "lucide-react";
import { portalLogout, type LandingNotification } from "../../lib/api";
import { fetchPublicNotifications } from "../../lib/publicApi";
import { useBranding } from "../../contexts/BrandingContext";
import { ThemeToggle } from "../../components/ThemeToggle";
import { portal } from "../../lib/portalRoutes";

const MAX_NOTIFICATIONS = 5;

function NotificationsPanel({ items }: { items: LandingNotification[] }) {
  const list = items.slice(0, MAX_NOTIFICATIONS);
  return (
    <div className="absolute right-0 top-full z-40 mt-2 w-[min(340px,calc(100vw-32px))] overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--bg-elevated)] shadow-[var(--shadow-pop)]">
      <div className="flex items-center justify-between border-b border-[var(--border)] px-4 py-3">
        <p className="text-[13px] font-semibold text-[var(--text)]">Notifications</p>
        <span className="rounded-full bg-accent-500/10 px-2 py-0.5 text-[10px] font-bold text-accent-600 dark:text-accent-400">
          {list.length}
        </span>
      </div>
      <div className="max-h-[min(400px,60dvh)] overflow-y-auto p-1.5">
        {list.length === 0 ? (
          <p className="px-3 py-8 text-center text-xs text-[var(--text-muted)]">No notifications yet.</p>
        ) : (
          <ul className="space-y-0.5">
            {list.map((item) => (
              <li key={item.id}>
                <div className="flex items-start gap-2.5 rounded-xl px-3 py-2.5">
                  <span className="mt-1.5 h-2 w-2 shrink-0 rounded-full bg-accent-500" aria-hidden="true" />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <p className="truncate text-[13px] font-semibold text-[var(--text)]">{item.title}</p>
                      {item.tag && (
                        <span className="shrink-0 rounded-full bg-accent-500/10 px-2 py-0.5 text-[9px] font-bold uppercase tracking-wide text-accent-600 dark:text-accent-400">
                          {item.tag}
                        </span>
                      )}
                    </div>
                    <p className="mt-0.5 text-xs leading-relaxed text-[var(--text-muted)]">{item.body}</p>
                  </div>
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

const NAV = [
  { to: portal("/"), label: "Dashboard", icon: LayoutGrid, end: true },
  { to: portal("/key"), label: "API Key", icon: Key },
  { to: portal("/usage"), label: "Usage", icon: BarChart3 },
  { to: portal("/models"), label: "Models", icon: Layers },
  { to: portal("/topup"), label: "Topup", icon: Wallet },
];

const TITLES: Record<string, string> = {
  "/portal": "Dashboard",
  "/portal/key": "API Key",
  "/portal/usage": "Usage",
  "/portal/models": "Models",
  "/portal/topup": "Topup",
};

export function PortalLayoutPage() {
  const location = useLocation();
  const { branding, logoSrc } = useBranding();
  const [open, setOpen] = useState(false);
  const [notifOpen, setNotifOpen] = useState(false);
  const close = useCallback(() => setOpen(false), []);

  const notif = useQuery({
    queryKey: ["public-notifications"],
    queryFn: fetchPublicNotifications,
    staleTime: 5 * 60 * 1000,
  });
  const notifCount = notif.data?.length ?? 0;

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
    document.title = `${branding.name || "KeiRouter"} - ${TITLES[location.pathname] ?? "Portal"}`;
  }, [location.pathname, branding.name]);

  useEffect(() => {
    document.body.style.overflow = open ? "hidden" : "";
    return () => { document.body.style.overflow = ""; };
  }, [open]);

  const handleLogout = async () => {
    await portalLogout();
    window.location.href = "/portal";
  };

  const linkClass = ({ isActive }: { isActive: boolean }) =>
    `flex items-center gap-3 rounded-xl px-3 py-2 text-sm font-medium transition-colors ${
      isActive
        ? "bg-accent-500/10 text-accent-600 dark:text-accent-400"
        : "text-[var(--text-muted)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text)]"
    }`;

  return (
    <div className="min-h-screen bg-[var(--bg)] text-[var(--text)]">
      <aside className="fixed inset-y-0 left-0 z-40 hidden w-64 flex-col border-r border-[var(--border)] bg-[var(--bg-elevated)] p-4 md:flex">
        <div className="mb-6 flex items-center gap-3 px-2">
          <img src={logoSrc} alt={branding.name || "KeiRouter"} className="h-7 object-contain" />
        </div>
        <nav className="flex flex-1 flex-col gap-1">
          {NAV.map((n) => (
            <NavLink key={n.to} to={n.to} end={n.end} className={linkClass}>
              <n.icon size={17} /> {n.label}
            </NavLink>
          ))}
        </nav>
        <div className="mt-4 flex items-center justify-between border-t border-[var(--border)] pt-4">
          <ThemeToggle />
          <button onClick={handleLogout} className="flex items-center gap-2 text-sm text-[var(--text-muted)] hover:text-[var(--text)]">
            <LogOut size={16} /> Sign out
          </button>
        </div>
      </aside>

      {open && (
        <div className="fixed inset-0 z-40 md:hidden" role="dialog">
          <div className="absolute inset-0 bg-black/40" onClick={close} />
          <aside className="absolute inset-y-0 left-0 w-64 bg-[var(--bg-elevated)] p-4">
            <button className="mb-4 text-[var(--text-muted)]" onClick={close} aria-label="Close menu"><X size={18} /></button>
            <nav className="flex flex-col gap-1">
              {NAV.map((n) => (
                <NavLink key={n.to} to={n.to} end={n.end} className={linkClass} onClick={close}>
                  <n.icon size={17} /> {n.label}
                </NavLink>
              ))}
            </nav>
          </aside>
        </div>
      )}

      <div className="md:pl-64">
        <header className="sticky top-0 z-30 flex items-center justify-between border-b border-[var(--border)] bg-[var(--bg)]/90 px-4 py-3 backdrop-blur md:px-8">
          <button className="md:hidden text-[var(--text-muted)]" onClick={() => setOpen(true)} aria-label="Open menu"><Menu size={20} /></button>
          <h1 className="text-lg font-display font-semibold">{TITLES[location.pathname] ?? "Portal"}</h1>
          <div className="flex items-center gap-2">
            <div className="relative" data-portal-notif>
              <button
                type="button"
                onClick={() => setNotifOpen((v) => !v)}
                aria-haspopup="dialog"
                aria-expanded={notifOpen}
                aria-label="Notifications"
                title="Notifications"
                className="relative flex h-9 w-9 items-center justify-center rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] text-[var(--text-muted)] transition-colors hover:text-[var(--text)]"
              >
                <Bell size={17} />
                {notifCount > 0 && (
                  <span className="absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-[#c43d2e] px-1 text-[9px] font-bold text-white">
                    {notifCount > 9 ? "9+" : notifCount}
                  </span>
                )}
              </button>
              {notifOpen && <NotificationsPanel items={notif.data ?? []} />}
            </div>
            <div className="md:hidden"><ThemeToggle /></div>
          </div>
        </header>
        <main className="mx-auto max-w-[1040px] p-4 md:p-8">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
