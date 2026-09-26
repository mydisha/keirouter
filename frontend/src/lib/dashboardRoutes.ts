// The authenticated dashboard lives under this prefix so `/` can serve the
// public landing without an auth gate. dashboard() builds absolute dashboard
// URLs; use it for any Link/navigate target that must stay inside the prefix.
export const DASHBOARD_PREFIX = "/ahoirilaila";

export const dashboard = (path: string) => `${DASHBOARD_PREFIX}${path}`;
