// Remembers which portal page a visitor wanted before the Google sign-in,
// which always lands on /portal, so they can be sent back afterwards (for
// example /portal/hosting?region=eu from the hosting landing page). Only
// same-site /portal/ paths are kept, so it cannot become an open redirect.

const KEY = "portal-return-to";

export function isPortalReturnPath(p: string): boolean {
  return /^\/portal\/[A-Za-z0-9/_-]*(\?[A-Za-z0-9=&_%.-]*)?$/.test(p) && !p.startsWith("/portal/auth");
}

export function rememberPortalReturn(): void {
  const p = window.location.pathname + window.location.search;
  if (!isPortalReturnPath(p)) return;
  try {
    sessionStorage.setItem(KEY, p);
  } catch {
    /* storage unavailable: the visitor lands on the portal home */
  }
}

export function takePortalReturn(): string | null {
  try {
    const p = sessionStorage.getItem(KEY);
    sessionStorage.removeItem(KEY);
    return p && isPortalReturnPath(p) ? p : null;
  } catch {
    return null;
  }
}
