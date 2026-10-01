import type { ComponentType } from "react";

type PageModule<T extends string> = Record<T, ComponentType<unknown>>;

export const named = <T extends string>(p: Promise<PageModule<T>>, key: T) =>
  p.then((m) => ({ default: m[key] }));

export const routeLoaders = {
  "/": () => named(import("./pages/Overview"), "OverviewPage"),
  "/providers": () => named(import("./pages/Providers"), "ProvidersPage"),
  "/provider-detail": () => named(import("./pages/ProviderDetail"), "ProviderDetailPage"),
  "/chains": () => named(import("./pages/Chains"), "ChainsPage"),
  "/chain-editor": () => named(import("./pages/ChainEditor"), "ChainEditorPage"),
  "/keys": () => named(import("./pages/Keys"), "KeysPage"),
  "/key-detail": () => named(import("./pages/KeyDetail"), "KeyDetailPage"),
  "/plans": () => named(import("./pages/Plans"), "PlansPage"),
  "/settings": () => named(import("./pages/Settings"), "SettingsPage"),
  "/endpoints": () => named(import("./pages/Endpoints"), "EndpointsPage"),
  "/usage": () => named(import("./pages/Usage"), "UsagePage"),
  "/quota": () => named(import("./pages/Quota"), "QuotaPage"),
  "/cli-tools": () => named(import("./pages/CLITools"), "CLIToolsPage"),
  "/cli-tool-detail": () => named(import("./pages/CLIToolDetail"), "CLIToolDetailPage"),
  "/media": () => named(import("./pages/MediaProviders"), "MediaProvidersPage"),
  "/media-detail": () => named(import("./pages/MediaProviderDetail"), "MediaProviderDetailPage"),
  "/proxy-pools": () => named(import("./pages/ProxyPools"), "ProxyPoolsPage"),
  "/skills": () => named(import("./pages/Skills"), "SkillsPage"),
  "/console": () => named(import("./pages/ConsoleLog"), "ConsoleLogPage"),
  "/system": () => named(import("./pages/System"), "SystemPage"),
  "/oauth-callback": () => named(import("./pages/OAuthCallback"), "OAuthCallbackPage"),
  "/portal": () => named(import("./pages/KeyPortal"), "KeyPortalPage"),
  "/guardrails": () => named(import("./pages/Guardrails"), "GuardrailsPage"),
  "/provider-health": () => named(import("./pages/ProviderHealth"), "ProviderHealthPage"),
  "/bansos": () => named(import("./pages/Bansos"), "BansosPage"),
  "/portal-users": () => named(import("./pages/PortalUsers"), "PortalUsersPage"),
} as const;

export type RoutePreloadKey = keyof typeof routeLoaders;

const preloaded = new Set<RoutePreloadKey>();

export function preloadRoute(key: RoutePreloadKey) {
  if (preloaded.has(key)) return;
  preloaded.add(key);
  void routeLoaders[key]().catch(() => preloaded.delete(key));
}
