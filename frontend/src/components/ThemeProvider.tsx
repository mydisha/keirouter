import { createContext, useContext, useEffect, useState, type ReactNode } from "react";

export type Theme = "light" | "dark" | "system";

interface ThemeAPI {
  theme: Theme;
  setTheme: (t: Theme) => void;
  resolved: "light" | "dark"; // actual theme after system resolution
}

const ThemeContext = createContext<ThemeAPI | null>(null);
const STORAGE_KEY = "keirouter-theme";

// Public pages (landing, /model, /bansos, /ketentuan) run their own theme in
// PublicLayout; keep in sync with the pre-paint script in index.html.
export const PUBLIC_PATH = /^\/(bansos|model|ketentuan)?\/?$/;

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

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(() => {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "light" || stored === "dark" || stored === "system") return stored;
    return "light";
  });

  const [resolved, setResolved] = useState<"light" | "dark">(() => resolve(theme));

  // Apply theme on mount and when theme changes. Public pages own the root
  // class themselves (PublicLayout), so the dashboard theme stays out of them.
  useEffect(() => {
    const r = resolve(theme);
    setResolved(r);
    if (!PUBLIC_PATH.test(window.location.pathname)) apply(r);
  }, [theme]);

  // Listen for system theme changes when in "system" mode.
  useEffect(() => {
    if (theme !== "system") return;
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const handler = () => {
      const r = getSystemTheme();
      setResolved(r);
      if (!PUBLIC_PATH.test(window.location.pathname)) apply(r);
    };
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, [theme]);

  // Persist only an explicit choice; writing the default on mount would make
  // every visitor look like they had picked it.
  const setTheme = (t: Theme) => {
    try {
      localStorage.setItem(STORAGE_KEY, t);
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
