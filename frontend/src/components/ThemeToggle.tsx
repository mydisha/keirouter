import { Moon, Sun } from "lucide-react";
import { cn } from "@/lib/utils";
import { switchTheme } from "@/lib/theme-transition";
import { useTheme } from "./ThemeProvider";

const ICON = "absolute size-4 transition-[opacity,scale,rotate] duration-300 ease-out-soft";

// One click flips light and dark. "System" stays a choice in the account menu;
// picking a side here overrides it.
export function ThemeToggle({ className }: { className?: string }) {
  const { resolved, setTheme } = useTheme();
  const dark = resolved === "dark";
  const next = dark ? "light" : "dark";
  return (
    <button
      type="button"
      aria-label={`Switch to ${next} theme`}
      onClick={() => switchTheme(() => setTheme(next))}
      className={cn(
        "press relative flex size-9 shrink-0 items-center justify-center rounded-lg text-fg-muted hover:bg-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-ring pointer-coarse:size-11",
        className,
      )}
    >
      <Sun aria-hidden="true" data-theme-icon className={cn(ICON, dark && "scale-50 -rotate-90 opacity-0")} />
      <Moon aria-hidden="true" data-theme-icon className={cn(ICON, !dark && "scale-50 rotate-90 opacity-0")} />
    </button>
  );
}
