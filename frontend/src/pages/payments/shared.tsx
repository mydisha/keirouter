import { useId, type ReactNode } from "react";
import { type LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import { SectionTitle } from "../../components/ui";

// Every Payments query polls at the same pace so the numbers on the page move together.
export const REFETCH_MS = 15_000;

// Query keys shared by the sections. Mutations invalidate these prefixes.
export const QK = {
  finance: ["finance"] as const,
  accounts: ["upstream-accounts"] as const,
  topups: ["topups"] as const,
  providers: ["finance-providers"] as const,
  alerts: ["finance-alerts"] as const,
};

export const SETTINGS_ANCHOR = "pengaturan-keuangan";

export function usd(n: number): string {
  const sign = n < 0 ? "-" : "";
  return `${sign}$${Math.abs(n).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

export function idr(n: number): string {
  return new Intl.NumberFormat("id-ID", { style: "currency", currency: "IDR", maximumFractionDigits: 0 }).format(n);
}

export function count(n: number): string {
  return n.toLocaleString("id-ID");
}

/** "15 Okt 2026", or "-" when the value is empty or invalid. */
export function dateID(value: string, withTime = false): string {
  if (!value) return "-";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "-";
  return d.toLocaleString("id-ID", {
    day: "numeric",
    month: "short",
    year: "numeric",
    ...(withTime ? { hour: "2-digit", minute: "2-digit", hour12: false } : {}),
  });
}

/** Today's date as YYYY-MM-DD in local time, for <input type="date">. */
export function todayISO(): string {
  return toDateInput(new Date().toISOString());
}

/** RFC3339 to YYYY-MM-DD in local time; "" when empty. */
export function toDateInput(value: string): string {
  if (!value) return "";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/**
 * YYYY-MM-DD from a date input to an RFC3339 instant in the browser's timezone.
 * A bare date would be read as midnight in the server's zone, which can land in
 * the future for operators ahead of it. Today sends the current time so it is
 * never in the future; earlier dates send local midnight. When `floor` (an
 * RFC3339 instant) falls on the chosen local day, the result is never earlier
 * than it, so a top-up on an account's opening day is not dated before
 * opening_at. "" stays "".
 */
export function dateInputToRFC3339(value: string, floor?: string): string {
  if (!value) return "";
  const [y, m, d] = value.split("-").map(Number);
  const at = value === todayISO() ? new Date() : new Date(y, m - 1, d);
  if (floor && value === toDateInput(floor)) {
    const f = new Date(floor);
    if (f.getTime() > at.getTime()) return f.toISOString();
  }
  return at.toISOString();
}

export function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

export const TH = "px-4 py-2 font-medium";
export const THEAD = "border-b border-line bg-subtle text-left text-[12px] text-fg-faint";

/** A bordered section with a SectionTitle header row, matching Usage.tsx panels. */
export function Panel({
  icon,
  title,
  subtitle,
  action,
  children,
  className,
  id,
}: {
  icon: LucideIcon;
  title: string;
  subtitle?: ReactNode;
  action?: ReactNode;
  children: ReactNode;
  className?: string;
  id?: string;
}) {
  const headingId = useId();
  return (
    <section
      id={id}
      aria-labelledby={headingId}
      className={cn("min-w-0 scroll-mt-6 overflow-hidden rounded-2xl border border-line bg-surface shadow-[var(--shadow-card)]", className)}
    >
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line px-4 py-3">
        <SectionTitle id={headingId} icon={icon} title={title} subtitle={subtitle} />
        {action}
      </div>
      {children}
    </section>
  );
}
