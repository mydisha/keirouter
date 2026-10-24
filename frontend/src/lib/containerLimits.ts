// Dokploy wants the container memory limit in bytes and the CPU limit in
// nano-CPUs. Admins type MB and CPUs instead; these convert both ways.
// "MB" means MiB here, the unit Docker uses for "512m".

const MIB = 1024 * 1024;
const NANO = 1_000_000_000;

/** "512", "512MB" or "1.5 GB" to a byte string; "" stays "" (no limit); null when invalid. */
export function memoryToBytes(input: string): string | null {
  const m = /^(\d+(?:\.\d+)?)\s*(mb|mib|m|gb|gib|g)?$/i.exec(input.trim());
  if (input.trim() === "") return "";
  if (!m) return null;
  const gb = m[2] && m[2].toLowerCase().startsWith("g");
  const bytes = Math.round(Number(m[1]) * (gb ? 1024 : 1) * MIB);
  return bytes > 0 ? String(bytes) : null;
}

/** A stored byte string to MB text for the input, e.g. "536870912" to "512". */
export function bytesToMemory(bytes: string | undefined): string {
  const n = Number(bytes);
  if (!bytes || !Number.isFinite(n) || n <= 0) return "";
  return String(Math.round((n / MIB) * 100) / 100);
}

/** "0.5", "1 CPU" or "2cpu" to a nano-CPU string; "" stays ""; null when invalid. */
export function cpuToNano(input: string): string | null {
  const m = /^(\d+(?:\.\d+)?|\.\d+)\s*(cpus?|cores?)?$/i.exec(input.trim());
  if (input.trim() === "") return "";
  if (!m) return null;
  const nano = Math.round(Number(m[1]) * NANO);
  return nano > 0 ? String(nano) : null;
}

/** A stored nano-CPU string to CPU text for the input, e.g. "500000000" to "0.5". */
export function nanoToCpu(nano: string | undefined): string {
  const n = Number(nano);
  if (!nano || !Number.isFinite(n) || n <= 0) return "";
  return String(Math.round((n / NANO) * 1000) / 1000);
}
