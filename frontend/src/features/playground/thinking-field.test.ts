import { describe, expect, it } from "vitest";
import { intensity, matrix } from "./thinking-field";

const W = 600;
const H = 144;

const spread = (energy: number, cx: number) => {
  let lit = 0;
  for (let t = 0; t < 6; t += 0.5) {
    for (let y = 0; y < H; y += 2) {
      if (intensity(cx * W, y, t, energy, W, H) > 0.05) {
        lit++;
      }
    }
  }

  return lit;
};

describe("thinking field intensity", () => {
  it("stays within 0..1 and is empty at both edges", () => {
    for (const energy of [0, 1 / 3, 2 / 3, 1]) {
      for (let t = 0; t < 10; t += 1.3) {
        for (let x = 0; x <= W; x += 25) {
          for (let y = 0; y <= H; y += 12) {
            const v = intensity(x, y, t, energy, W, H);
            expect(v).toBeGreaterThanOrEqual(0);
            expect(v).toBeLessThanOrEqual(1);
          }
        }
        expect(intensity(0, H / 2, t, energy, W, H)).toBe(0);
        expect(intensity(W, H / 2, t, energy, W, H)).toBe(0);
      }
    }
  });

  it("braids even with thinking off, wider as thinking rises", () => {
    // Off once collapsed the strands into one flat line that looked broken.
    let reach = 0;
    for (let t = 0; t < 6; t += 0.25) {
      for (let y = 0; y < H; y++) {
        if (intensity(W / 2, y, t, 0, W, H) > 0.05) {
          reach = Math.max(reach, Math.abs(y - H / 2));
        }
      }
    }
    expect(reach).toBeGreaterThan(24);
    expect(spread(1 / 3, 0.5)).toBeGreaterThan(spread(0, 0.5));
    expect(spread(1, 0.5)).toBeGreaterThan(spread(1 / 3, 0.5));
    expect(spread(1 / 3, 0.5)).toBeGreaterThan(spread(0, 0.5));
  });

  it("keeps the braid inside the band", () => {
    for (let t = 0; t < 10; t += 0.7) {
      for (let x = 0; x <= W; x += 10) {
        expect(intensity(x, 0, t, 1, W, H)).toBeLessThan(0.05);
        expect(intensity(x, H, t, 1, W, H)).toBeLessThan(0.05);
      }
    }
  });
});

describe("thinking field matrix", () => {
  it("shows around the braid and fades out at the edges", () => {
    expect(matrix(W / 2, H / 2, W, H)).toBe(1);
    expect(matrix(0, H / 2, W, H)).toBe(0);
    expect(matrix(W / 2, 0, W, H)).toBe(0);
    expect(matrix(W * 0.1, H * 0.2, W, H)).toBe(0);
  });
});
