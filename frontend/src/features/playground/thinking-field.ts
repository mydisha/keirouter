/** Hermite ease between edges a and b, clamped to 0..1. */
export const smoothstep = (a: number, b: number, x: number) => {
  const k = Math.max(0, Math.min(1, (x - a) / (b - a)));
  return k * k * (3 - 2 * k);
};

/** Lattice pitch of the dot matrix, in CSS pixels. */
export const CELL = 6;
const STRANDS = [0, (2 * Math.PI) / 3, (4 * Math.PI) / 3];
/** Gaussian half-width of a strand, in pixels: one to two dots thick. */
const THICKNESS = 3.8;

/**
 * Brightness 0..1 of the dot at (`x`, `y`) pixels in a `width`×`height`
 * band at `t` seconds. Three strands leave the left edge as one line, braid
 * apart in the middle and meet again at the right: a prompt going in, a reply
 * coming out. `energy` (0..1, the thinking level) widens and quickens the
 * braid. At 0 it stays a slow, narrow braid: three strands on one line read
 * as a broken illustration, not as "no thinking".
 */
export function intensity(x: number, y: number, t: number, energy: number, width: number, height: number): number {
  const cx = x / width;
  const edge = smoothstep(0, 0.16, cx) * smoothstep(0, 0.16, 1 - cx);
  if (edge === 0) {
    return 0;
  }
  const pinch = Math.sin(Math.PI * cx) ** 1.4;
  const amp = height * (0.2 + 0.12 * energy) * pinch;
  const waves = 1.1 + 1.3 * energy;
  const speed = 0.45 + 1.2 * energy;
  const drift = 0.1 + 0.16 * energy;
  let v = 0;
  STRANDS.forEach((phase, k) => {
    const mid = height / 2 + amp * Math.sin(cx * waves * 2 * Math.PI + t * speed + phase);
    const near = Math.exp(-(((y - mid) / THICKNESS) ** 2));
    const p = cx * 1.5 - t * drift + k / 3;
    const packet = Math.exp(-(((p - Math.floor(p) - 0.5) / 0.07) ** 2));
    v = Math.max(v, near * (0.6 + 0.4 * packet));
  });

  return v * edge;
}

/**
 * How much of the unlit matrix shows at (`x`, `y`): a lens-shaped patch
 * around the braid, so the strands read as lit cells of a dot-matrix display
 * rather than dots floating on the page.
 */
export function matrix(x: number, y: number, width: number, height: number): number {
  const cx = x / width;
  const reach = (height / 2) * (0.25 + 0.75 * Math.sin(Math.PI * cx));
  const dy = Math.abs(y - height / 2);

  return smoothstep(0, 0.22, cx) * smoothstep(0, 0.22, 1 - cx) * smoothstep(reach, reach * 0.55, dy);
}
