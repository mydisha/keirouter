// Checks every text/surface pair the dashboard uses against WCAG 2.2 AA, in
// light and dark, by resolving the tokens in src/index.css:
//   npm run contrast
// Text needs 4.5:1; input borders, focus rings and icons need 3:1.
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../src/index.css", import.meta.url), "utf8");

// Custom properties per scope. @theme and :root are the light base; .dark overrides.
function block(selector) {
  const start = css.search(new RegExp(`^${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{`, "m"));
  if (start < 0) throw new Error(`no ${selector} block`);
  let depth = 0;
  for (let i = css.indexOf("{", start); i < css.length; i++) {
    if (css[i] === "{") depth++;
    else if (css[i] === "}" && --depth === 0) return css.slice(start, i);
  }
  throw new Error(`unclosed ${selector}`);
}
function vars(text) {
  const out = {};
  for (const [, name, value] of text.matchAll(/(--[\w-]+):\s*([^;]+);/g)) out[name] = value.trim();
  return out;
}
const theme = vars(block("@theme"));
const light = { ...theme, ...vars(block(":root")) };
const dark = { ...light, ...vars(block(".dark")) };

// ── colour maths ───────────────────────────────────────────────────────────
const toLinear = (c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
const toGamma = (c) => (c <= 0.0031308 ? 12.92 * c : 1.055 * c ** (1 / 2.4) - 0.055);
const clamp = (v) => Math.min(1, Math.max(0, v));

function oklabToLinear([L, a, b]) {
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3;
  return [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ];
}
function linearToOklab([r, g, b]) {
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  return [
    0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
  ];
}

// A colour is { rgb: linear sRGB [0..1], alpha }.
function parse(value, scope, seen = new Set()) {
  value = value.trim();
  const ref = value.match(/^var\((--[\w-]+)\)$/);
  if (ref) {
    if (seen.has(ref[1])) throw new Error(`cycle at ${ref[1]}`);
    seen.add(ref[1]);
    const next = scope[ref[1]];
    if (!next) throw new Error(`unknown ${ref[1]}`);
    return parse(next, scope, seen);
  }
  const hex = value.match(/^#([0-9a-f]{6})$/i);
  if (hex) {
    const n = parseInt(hex[1], 16);
    return { rgb: [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((c) => toLinear(c / 255)), alpha: 1 };
  }
  const ok = value.match(/^oklch\(([\d.]+)\s+([\d.]+)\s+([\d.]+)(?:\s*\/\s*([\d.]+))?\)$/);
  if (ok) {
    const [L, C, H] = [Number(ok[1]), Number(ok[2]), (Number(ok[3]) * Math.PI) / 180];
    const rgb = oklabToLinear([L, C * Math.cos(H), C * Math.sin(H)]).map((c) => toLinear(clamp(toGamma(c))));
    return { rgb, alpha: ok[4] ? Number(ok[4]) : 1 };
  }
  const mix = value.match(/^color-mix\(in (oklab|srgb),\s*(.+?)\s+([\d.]+)%,\s*(.+)\)$/);
  if (mix) {
    const a = parse(mix[2], scope);
    const b = mix[4] === "transparent" ? null : parse(mix[4], scope);
    const p = Number(mix[3]) / 100;
    if (!b) return { rgb: a.rgb, alpha: a.alpha * p };
    if (mix[1] === "srgb") return { rgb: a.rgb.map((c, i) => c * p + b.rgb[i] * (1 - p)), alpha: 1 };
    const [la, lb] = [linearToOklab(a.rgb), linearToOklab(b.rgb)];
    return { rgb: oklabToLinear(la.map((c, i) => c * p + lb[i] * (1 - p))), alpha: 1 };
  }
  throw new Error(`can't parse "${value}"`);
}

const luminance = ([r, g, b]) => 0.2126 * r + 0.7152 * g + 0.0722 * b;
function over(fg, bg) {
  if (fg.alpha >= 1) return fg.rgb;
  const [f, b] = [fg.rgb.map(toGamma), bg.rgb.map(toGamma)];
  return f.map((c, i) => toLinear(c * fg.alpha + b[i] * (1 - fg.alpha)));
}
function ratio(fgName, bgName, scope) {
  const bg = parse(`var(${bgName})`, scope);
  const fg = over(parse(`var(${fgName})`, scope), bg);
  const [a, b] = [luminance(fg), luminance(bg.rgb)].sort((x, y) => y - x);
  return (a + 0.05) / (b + 0.05);
}

// ── pairs the UI uses ──────────────────────────────────────────────────────
const surfaces = ["--bg", "--bg-elevated", "--bg-subtle", "--sidebar"];
const pairs = [
  ...surfaces.flatMap((s) => [["--text", s, 4.5], ["--text-muted", s, 4.5], ["--text-faint", s, 4.5]]),
  ["--link", "--bg", 4.5],
  ["--link", "--bg-elevated", 4.5],
  ["--brand-ink", "--bg-elevated", 4.5],
  ["--brand-ink", "--brand-soft", 4.5],
  ["--brand-ink", "--sidebar", 3],
  ["--action-fg", "--action", 4.5],
  ["--action-fg", "--action-hover", 4.5],
  ["--primary-fg", "--primary", 4.5],
  ["--status-ok", "--status-ok-soft", 4.5],
  ["--status-warn", "--status-warn-soft", 4.5],
  ["--status-bad", "--status-bad-soft", 4.5],
  ["--status-ok", "--bg-elevated", 4.5],
  ["--status-warn", "--bg-elevated", 4.5],
  ["--status-bad", "--bg-elevated", 4.5],
  ["--text-muted", "--bg-hover", 4.5],
  ["--code-fg", "--code-bg", 4.5],
  ["--code-com", "--code-bg", 4.5],
  ["--border-input", "--bg-elevated", 3],
  ["--ring", "--bg-elevated", 3],
  ["--ring", "--bg", 3],
];

let failed = 0;
for (const [label, scope] of [["light", light], ["dark", dark]]) {
  for (const [fg, bg, min] of pairs) {
    const r = ratio(fg, bg, scope);
    const ok = r >= min;
    if (!ok) failed++;
    console.log(`${ok ? "ok  " : "FAIL"} ${label.padEnd(5)} ${fg} on ${bg}: ${r.toFixed(2)} (needs ${min})`);
  }
}
if (failed) {
  console.error(`\n${failed} pair(s) below WCAG AA`);
  process.exit(1);
}
