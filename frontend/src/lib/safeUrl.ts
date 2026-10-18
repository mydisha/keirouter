// URL guards for links that come from the server or an upstream provider.
// React blocks javascript: URLs in JSX href/src, but window.open and
// window.location do not, so every URL handed to them goes through here.

/** Returns the link when it is an absolute http(s) URL, else null. */
export function safeHttpUrl(link: string | undefined | null): string | null {
  if (!link) return null;
  try {
    const proto = new URL(link).protocol;
    return proto === "http:" || proto === "https:" ? link : null;
  } catch {
    return null;
  }
}

/**
 * window.open that refuses anything but an absolute http(s) URL. Returns the
 * opened window, or null when the URL was refused or the popup was blocked.
 */
export function openExternal(link: string | undefined | null, target = "_blank", features?: string): Window | null {
  const url = safeHttpUrl(link);
  if (!url) return null;
  return window.open(url, target, features);
}
