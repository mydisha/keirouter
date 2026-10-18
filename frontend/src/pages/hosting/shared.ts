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
  awaiting_payment: "Menunggu pembayaran",
  paid: "Lunas, belum dibuat",
  provisioning: "Sedang dibuat",
  active: "Aktif",
  failed: "Gagal",
  expired: "Kedaluwarsa",
  stopped: "Dihentikan",
  deleted: "Dihapus",
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
  if (!n.enabled) return "nonaktif";
  return n.available ? "" : "penuh";
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
  return new Date(y, m - 1, 1).toLocaleDateString("id-ID", { month: "short", year: "numeric" });
}

/** "2026-10-05" → "5 Okt 2026" without timezone shifts; "" → "". */
export function plainDateID(value: string): string {
  const [y, m, d] = value.split("-").map(Number);
  if (!y || !m || !d) return value;
  return new Date(y, m - 1, d).toLocaleDateString("id-ID", { day: "numeric", month: "short", year: "numeric" });
}

/** Short IDR for axis ticks: Rp500 rb, Rp1,2 jt, Rp3 M. */
export function idrCompact(n: number): string {
  const sign = n < 0 ? "-" : "";
  const a = Math.abs(n);
  const fmt = (v: number) => v.toLocaleString("id-ID", { maximumFractionDigits: v >= 10 ? 0 : 1 });
  if (a >= 1e9) return `${sign}Rp${fmt(a / 1e9)} M`;
  if (a >= 1e6) return `${sign}Rp${fmt(a / 1e6)} jt`;
  if (a >= 1e3) return `${sign}Rp${fmt(a / 1e3)} rb`;
  return `${sign}Rp${Math.round(a)}`;
}

export function pct(n: number, digits = 1): string {
  return `${n.toLocaleString("id-ID", { minimumFractionDigits: digits, maximumFractionDigits: digits })}%`;
}

/** Number with Indonesian separators and up to `digits` decimals. */
export function num(n: number, digits = 1): string {
  return n.toLocaleString("id-ID", { maximumFractionDigits: digits });
}

/** Seconds → "3 hari 4 jam" / "5 jam 12 menit" / "8 menit". */
export function uptimeID(seconds: number): string {
  if (!(seconds > 0)) return "-";
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return h > 0 ? `${d} hari ${h} jam` : `${d} hari`;
  if (h > 0) return m > 0 ? `${h} jam ${m} menit` : `${h} jam`;
  return `${Math.max(1, m)} menit`;
}
