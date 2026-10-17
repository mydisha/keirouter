import { createContext, useContext, useEffect, useState, type ReactNode } from "react";

export type Theme = "light" | "dark" | "system";

interface ThemeAPI {
  theme: Theme;
  setTheme: (t: Theme) => void;
  resolved: "light" | "dark"; // actual theme after system resolution
}

const ThemeContext = createContext<ThemeAPI | null>(null);
const STORAGE_KEY = "keirouter-theme";

// Public pages (landing, /model, /bansos, /ketentuan[/hosting-9router]) run their own theme in
// PublicLayout; keep in sync with the pre-paint script in index.html.
export const PUBLIC_PATH = /^\/(bansos|model|ketentuan(\/hosting-9router)?)?\/?$/;
// Paths whose root class is owned by something other than the dashboard
// theme: the public pages and the customer portal (its own ThemeProvider).
const SEPARATE_THEME_PATH = /^\/((bansos|model|ketentuan(\/hosting-9router)?)?\/?$|portal(\/|$))/;

/** Storage key for the customer portal theme; read by index.html too. */
export const PORTAL_THEME_KEY = "keirouter-portal-theme";

/** The dashboard theme the visitor saved, resolved against the OS setting. */
export function storedDashboardTheme(): "light" | "dark" {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    return resolve(stored === "dark" || stored === "system" ? stored : "light");
  } catch {
    return "light";
  }
}

function getSystemTheme(): "light" | "dark" {
  if (typeof window === "undefined") return "light";
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

function resolve(theme: Theme): "light" | "dark" {
  return theme === "system" ? getSystemTheme() : theme;
}

function apply(resolved: "light" | "dark") {
  const root = document.documentElement;
  if (resolved === "dark") {
    root.classList.add("dark");
  } else {
    root.classList.remove("dark");
  }
}

/**
 * The dashboard theme by default. The portal mounts a scoped instance with its
 * own storage key and a dark default; it owns the root class while mounted and
 * hands it back to the dashboard theme when it unmounts.
 */
export function ThemeProvider({
  children,
  storageKey = STORAGE_KEY,
  defaultTheme = "light",
  scoped = false,
}: {
  children: ReactNode;
  storageKey?: string;
  defaultTheme?: Theme;
  scoped?: boolean;
}) {
  const [theme, setThemeState] = useState<Theme>(() => {
    try {
      const stored = localStorage.getItem(storageKey);
      if (stored === "light" || stored === "dark" || stored === "system") return stored;
    } catch {
      /* storage unavailable */
    }
    return defaultTheme;
  });
  const ownsRoot = () => scoped || !SEPARATE_THEME_PATH.test(window.location.pathname);

  const [resolved, setResolved] = useState<"light" | "dark">(() => resolve(theme));

  // Apply theme on mount and when theme changes. Public pages own the root
  // class themselves (PublicLayout), so the dashboard theme stays out of them.
  useEffect(() => {
    const r = resolve(theme);
    setResolved(r);
    if (ownsRoot()) apply(r);
  }, [theme]);

  useEffect(() => {
    if (!scoped) return;
    return () => apply(storedDashboardTheme());
  }, [scoped]);

  // Listen for system theme changes when in "system" mode.
  useEffect(() => {
    if (theme !== "system") return;
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const handler = () => {
      const r = getSystemTheme();
      setResolved(r);
      if (ownsRoot()) apply(r);
    };
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, [theme]);

  // Persist only an explicit choice; writing the default on mount would make
  // every visitor look like they had picked it.
  const setTheme = (t: Theme) => {
    try {
      localStorage.setItem(storageKey, t);
    } catch {
      /* storage unavailable: the choice lasts for this page only */
    }
    setThemeState(t);
  };

  return (
    <ThemeContext.Provider value={{ theme, setTheme, resolved }}>
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme(): ThemeAPI {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error("useTheme must be used within ThemeProvider");
  return ctx;
}
