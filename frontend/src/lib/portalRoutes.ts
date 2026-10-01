// The portal dashboard lives under this prefix so the marketing sign-in card
// and the authenticated shell share /portal. portal() builds absolute URLs.
export const PORTAL_PREFIX = "/portal";

export const portal = (path: string) => `${PORTAL_PREFIX}${path}`;
