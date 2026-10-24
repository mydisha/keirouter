import type { HostingStatus } from "../../lib/api";

export const QK_HOSTING = {
  settings: ["hosting-settings"] as const,
  instances: ["hosting-instances"] as const,
  finance: ["hosting-finance"] as const,
  coupons: ["hosting-coupons"] as const,
  nodes: ["hosting-nodes"] as const,
  monitorSettings: ["hosting-monitor-settings"] as const,
  monitorServers: ["hosting-monitor-servers"] as const,
  monitorContainers: ["hosting-monitor-containers"] as const,
};

export const STATUS_LABEL: Record<HostingStatus, string> = {
  awaiting_payment: "Awaiting payment",
  paid: "Paid, not provisioned",
  provisioning: "Provisioning",
  active: "Active",
  failed: "Failed",
  expired: "Expired",
  stopped: "Stopped",
  deleted: "Deleted",
};

export const STATUS_TONE: Record<HostingStatus, "success" | "warning" | "danger" | "neutral" | "accent"> = {
  awaiting_payment: "neutral",
  paid: "warning",
  provisioning: "accent",
  active: "success",
  failed: "danger",
  expired: "warning",
  stopped: "neutral",
  deleted: "neutral",
};

/** "12/20", or "3/∞" for a node without a capacity limit. */
export function nodeUsage(n: { used: number; capacity: number }): string {
  return `${n.used}/${n.capacity > 0 ? n.capacity : "∞"}`;
}

/** Why a node takes no new instances, or "" when it does. */
export function nodeBlockedLabel(n: { enabled: boolean; available: boolean }): string {
  if (!n.enabled) return "disabled";
  return n.available ? "" : "full";
}

/** Node name by id, falling back to the id itself. */
export function nodeName(nodes: readonly { id: string; name: string }[] | undefined, id: string): string {
  if (!id) return "";
  return nodes?.find((n) => n.id === id)?.name || id;
}

/** A fresh node id matching the server's ^[a-z0-9][a-z0-9-]{0,39}$. */
export function newNodeId(): string {
  const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789";
  const bytes = new Uint8Array(6);
  crypto.getRandomValues(bytes);
  return "n-" + Array.from(bytes, (b) => alphabet[b % alphabet.length]).join("");
}

/** "*.n1.Example.com." → "n1.example.com", the form the server stores. */
export function normalizeDomain(d: string): string {
  return d.trim().toLowerCase().replace(/^\*\./, "").replace(/^\.+|\.+$/g, "");
}

/** Mirrors the server's validDomain. */
export function validDomain(d: string): boolean {
  if (d.length > 253 || !d.includes(".")) return false;
  return d.split(".").every((l) => l.length > 0 && l.length <= 63 && /^[a-z0-9-]+$/.test(l) && !l.startsWith("-") && !l.endsWith("-"));
}

/** "2026-10" → "Okt 2026". */
export function monthLabel(month: string): string {
  const [y, m] = month.split("-").map(Number);
  if (!y || !m) return month;
  return new Date(y, m - 1, 1).toLocaleDateString("en-US", { month: "short", year: "numeric" });
}

/** "2026-10-05" → "5 Okt 2026" without timezone shifts; "" → "". */
export function plainDateID(value: string): string {
  const [y, m, d] = value.split("-").map(Number);
  if (!y || !m || !d) return value;
  return new Date(y, m - 1, d).toLocaleDateString("en-US", { day: "numeric", month: "short", year: "numeric" });
}

/** Short IDR for axis ticks: Rp500K, Rp1.2M, Rp3B. */
export function idrCompact(n: number): string {
  const sign = n < 0 ? "-" : "";
  const a = Math.abs(n);
  const fmt = (v: number) => v.toLocaleString("en-US", { maximumFractionDigits: v >= 10 ? 0 : 1 });
  if (a >= 1e9) return `${sign}Rp${fmt(a / 1e9)}B`;
  if (a >= 1e6) return `${sign}Rp${fmt(a / 1e6)}M`;
  if (a >= 1e3) return `${sign}Rp${fmt(a / 1e3)}K`;
  return `${sign}Rp${Math.round(a)}`;
}

export function pct(n: number, digits = 1): string {
  return `${n.toLocaleString("en-US", { minimumFractionDigits: digits, maximumFractionDigits: digits })}%`;
}

/** Number with Indonesian separators and up to `digits` decimals. */
export function num(n: number, digits = 1): string {
  return n.toLocaleString("en-US", { maximumFractionDigits: digits });
}

/** Seconds → "3 hari 4 jam" / "5 jam 12 menit" / "8 menit". */
export function uptimeID(seconds: number): string {
  if (!(seconds > 0)) return "-";
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return h > 0 ? `${d}d ${h}h` : `${d}d`;
  if (h > 0) return m > 0 ? `${h}h ${m}m` : `${h}h`;
  return `${Math.max(1, m)}m`;
}
