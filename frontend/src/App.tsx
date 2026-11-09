import { lazy, Suspense } from "react";
import { Routes, Route } from "react-router-dom";
import { AuthGate } from "./components/AuthGate";
import { Layout } from "./components/Layout";
import { AdminBrandingProvider, PortalBrandingProvider } from "./contexts/BrandingContext";
import { routeLoaders } from "./routePreload";

// Routes are code-split; loaders live in routePreload so navigation can warm
// chunks before click without pulling page modules into the shell bundle.
const OverviewPage = lazy(routeLoaders["/"]);
const ProvidersPage = lazy(routeLoaders["/providers"]);
const ProviderDetailPage = lazy(routeLoaders["/provider-detail"]);
const ChainsPage = lazy(routeLoaders["/chains"]);
const ChainEditorPage = lazy(routeLoaders["/chain-editor"]);
const KeysPage = lazy(routeLoaders["/keys"]);
const PlansPage = lazy(routeLoaders["/plans"]);
const SettingsPage = lazy(routeLoaders["/settings"]);
const EndpointsPage = lazy(routeLoaders["/endpoints"]);
const UsagePage = lazy(routeLoaders["/usage"]);
const QuotaPage = lazy(routeLoaders["/quota"]);
const CLIToolsPage = lazy(routeLoaders["/cli-tools"]);
const CLIToolDetailPage = lazy(routeLoaders["/cli-tool-detail"]);
const MediaProvidersPage = lazy(routeLoaders["/media"]);
const MediaProviderDetailPage = lazy(routeLoaders["/media-detail"]);
const ProxyPoolsPage = lazy(routeLoaders["/proxy-pools"]);
const SkillsPage = lazy(routeLoaders["/skills"]);
const ConsoleLogPage = lazy(routeLoaders["/console"]);
const PlaygroundPage = lazy(routeLoaders["/playground"]);
const SystemPage = lazy(routeLoaders["/system"]);
const OAuthCallbackPage = lazy(routeLoaders["/oauth-callback"]);
const KeyPortalPage = lazy(routeLoaders["/portal"]);
const KeyDetailPage = lazy(routeLoaders["/key-detail"]);
const GuardrailsPage = lazy(routeLoaders["/guardrails"]);
const AuditPage = lazy(routeLoaders["/audit"]);
const ProviderHealthPage = lazy(routeLoaders["/provider-health"]);

function PageFallback() {
  return (
    <div className="flex items-center justify-center h-full w-full py-24">
      <div className="h-6 w-6 animate-spin rounded-full border-2 border-current border-t-transparent opacity-40" />
    </div>
  );
}

export function App() {
  return (
    <Suspense fallback={<PageFallback />}>
      <Routes>
        <Route path="portal" element={
          <PortalBrandingProvider>
            <KeyPortalPage />
          </PortalBrandingProvider>
        } />
        <Route path="*" element={
          <AuthGate>
            <AdminBrandingProvider>
            <Routes>
              {/* OAuth callback — standalone page, no sidebar layout */}
              <Route path="oauth/callback" element={<OAuthCallbackPage />} />
              <Route element={<Layout />}>
                <Route index element={<OverviewPage />} />
                <Route path="providers" element={<ProvidersPage />} />
                <Route path="providers/:id" element={<ProviderDetailPage />} />
                <Route path="endpoints" element={<EndpointsPage />} />
                <Route path="chains" element={<ChainsPage />} />
                <Route path="chains/new" element={<ChainEditorPage />} />
                <Route path="chains/:id/edit" element={<ChainEditorPage />} />
                <Route path="usage" element={<UsagePage />} />
                <Route path="quota" element={<QuotaPage />} />
                <Route path="cli-tools" element={<CLIToolsPage />} />
                <Route path="cli-tools/:toolId" element={<CLIToolDetailPage />} />
                <Route path="media" element={<MediaProvidersPage />} />
                <Route path="media/:kind" element={<MediaProvidersPage />} />
                <Route path="media/:kind/:id" element={<MediaProviderDetailPage />} />
                <Route path="proxy-pools" element={<ProxyPoolsPage />} />
                <Route path="skills" element={<SkillsPage />} />
                <Route path="console" element={<ConsoleLogPage />} />
                <Route path="playground" element={<PlaygroundPage />} />
                <Route path="keys" element={<KeysPage />} />
                <Route path="keys/:id" element={<KeyDetailPage />} />
                <Route path="guardrails" element={<GuardrailsPage />} />
                <Route path="audit" element={<AuditPage />} />
                <Route path="provider-health" element={<ProviderHealthPage />} />
                <Route path="provider-health/:provider" element={<ProviderHealthPage />} />
                <Route path="plans" element={<PlansPage />} />
                <Route path="budgets" element={<PlansPage />} />
                <Route path="system" element={<SystemPage />} />
                <Route path="settings" element={<SettingsPage />} />
              </Route>
            </Routes>
            </AdminBrandingProvider>
          </AuthGate>
        } />
      </Routes>
    </Suspense>
  );
}
