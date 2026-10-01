import { useState, useEffect, useCallback } from "react";
import { NavLink, Outlet, useLocation } from "react-router-dom";
import { LayoutGrid, Key, BarChart3, Layers, Wallet, LogOut, Menu, X } from "lucide-react";
import { portalLogout } from "../../lib/api";
import { useBranding } from "../../contexts/BrandingContext";
import { ThemeToggle } from "../../components/ThemeToggle";
import { portal } from "../../lib/portalRoutes";

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
  const close = useCallback(() => setOpen(false), []);

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
          <div className="md:hidden"><ThemeToggle /></div>
        </header>
        <main className="mx-auto max-w-[1040px] p-4 md:p-8">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
