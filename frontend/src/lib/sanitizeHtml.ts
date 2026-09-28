// Allowlist HTML sanitizer for landing notifications. Dependency-free and
// environment-independent (no DOMParser), so it runs identically in the
// browser and under `node --test`. Default-deny: only allowlisted tags are
// emitted; everything else is unwrapped (markup dropped, text kept). This is
// the single source of truth for notification HTML rendering (public popup +
// admin preview).

const ALLOWED_TAGS = new Set([
  "b", "strong", "i", "em", "u", "a", "br", "span", "code", "small",
]);
const VOID_TAGS = new Set(["br"]);
const DROP_CONTENT = new Set(["script", "style", "iframe", "object", "embed"]);
const SAFE_SCHEMES = new Set(["http:", "https:", "mailto:"]);

const escapeText = (s: string): string =>
  s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

const escapeAttr = (s: string): string => s.replace(/&/g, "&amp;").replace(/"/g, "&quot;");

// safeHref returns the href only when it is an absolute http/https/mailto URL.
// Relative and anchor hrefs are dropped; so are javascript:/data:/etc.
export function safeHref(raw: string): string | null {
  const href = raw.trim();
  if (!href) return null;
  try {
    if (SAFE_SCHEMES.has(new URL(href).protocol)) return href;
  } catch {
    /* not an absolute URL */
  }
  return null;
}

export function sanitizeHtml(html: string): string {
  let out = "";
  let last = 0;
  const tagRe = /<(\/?)([a-zA-Z][a-zA-Z0-9-]*)((?:"[^"]*"|'[^']*'|[^>])*?)(\/?)>/g;
  let m: RegExpExecArray | null;

  while ((m = tagRe.exec(html)) !== null) {
    out += escapeText(html.slice(last, m.index));
    last = tagRe.lastIndex;
    const closing = m[1] === "/";
    const tag = m[2].toLowerCase();
    const attrs = m[3] ?? "";

    if (DROP_CONTENT.has(tag)) {
      if (!closing) {
        const closeRe = new RegExp(`</${tag}\\s*>`, "i");
        const cm = closeRe.exec(html.slice(last));
        last = cm ? last + cm.index + cm[0].length : html.length;
        tagRe.lastIndex = last;
      }
      continue;
    }

    if (!ALLOWED_TAGS.has(tag)) {
      continue; // unwrap: drop markup, keep surrounding text
    }
    if (closing) {
      out += `</${tag}>`;
      continue;
    }
    if (VOID_TAGS.has(tag)) {
      out += `<${tag}>`;
      continue;
    }
    if (tag === "a") {
      const hm = /\bhref\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/i.exec(attrs);
      const href = hm ? safeHref(hm[1] ?? hm[2] ?? hm[3] ?? "") : null;
      out += href
        ? `<a href="${escapeAttr(href)}" rel="noopener noreferrer" target="_blank">`
        : `<a>`;
      continue;
    }
    out += `<${tag}>`;
  }

  out += escapeText(html.slice(last));
  return out;
}
