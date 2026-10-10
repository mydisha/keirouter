import { createContext, useContext, useEffect, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, fetchPortalBranding, type BrandingSettings } from "../lib/api";
import { getPaletteScales } from "../lib/palettes";
import { applyShadeScale } from "../lib/color-utils";

const DEFAULT_BRANDING: BrandingSettings = {
  name: "KeiRouter",
  logo_url: "",
  favicon_url: "",
  tagline: "",
  color_palette: "kei",
};

interface BrandingContextValue {
  branding: BrandingSettings;
  logoSrc: string;
  faviconSrc: string;
  isLoading: boolean;
}

const BrandingContext = createContext<BrandingContextValue>({
  branding: DEFAULT_BRANDING,
  logoSrc: "/keirouter-logo.png",
  faviconSrc: "/keirouter-favicon.png",
  isLoading: false,
});

/**
 * Admin dashboard branding — fetches from /api/settings/branding (session auth).
 */
export function AdminBrandingProvider({ children }: { children: ReactNode }) {
  const { data, isLoading } = useQuery({
    queryKey: ["branding"],
    queryFn: () => api.branding(),
    staleTime: 5 * 60_000, // branding changes rarely; cache for 5 min
    retry: false,
  });

  const branding = data ?? DEFAULT_BRANDING;
  const logoSrc = branding.logo_url || "/keirouter-logo.png";
  const faviconSrc = branding.favicon_url || "/keirouter-favicon.png";

  // Update document title, favicon, and color palette dynamically
  useEffect(() => {
    if (branding.name) {
      document.title = branding.name;
    }
    if (branding.favicon_url) {
      let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
      if (!link) {
        link = document.createElement("link");
        link.rel = "icon";
        document.head.appendChild(link);
      }
      link.href = branding.favicon_url;
    }
    // Apply color palette as CSS custom properties on <html>
    const root = document.documentElement;
    const paletteId = branding.color_palette || "kei";
    const scales = getPaletteScales(paletteId);
    applyShadeScale(root, "accent", scales.accent);
    applyShadeScale(root, "secondary", scales.secondary);
  }, [branding.name, branding.favicon_url, branding.color_palette]);

  return (
    <BrandingContext.Provider value={{ branding, logoSrc, faviconSrc, isLoading }}>
      {children}
    </BrandingContext.Provider>
  );
}

/**
 * Portal branding — fetches from /v1/portal/branding (no auth).
 */
export function PortalBrandingProvider({ children }: { children: ReactNode }) {
  const { data, isLoading } = useQuery({
    queryKey: ["portal-branding"],
    queryFn: fetchPortalBranding,
    staleTime: 5 * 60_000,
    retry: false,
  });

  const branding = data ?? DEFAULT_BRANDING;
  const logoSrc = branding.logo_url || "/keirouter-logo.png";
  const faviconSrc = branding.favicon_url || "/keirouter-favicon.png";

  useEffect(() => {
    const title = branding.name || "KeiRouter";
    document.title = `${title} — Usage Dashboard`;
    if (branding.favicon_url) {
      let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
      if (!link) {
        link = document.createElement("link");
        link.rel = "icon";
        document.head.appendChild(link);
      }
      link.href = branding.favicon_url;
    }
    // Apply color palette as CSS custom properties on <html>
    const root = document.documentElement;
    const paletteId = branding.color_palette || "kei";
    const scales = getPaletteScales(paletteId);
    applyShadeScale(root, "accent", scales.accent);
    applyShadeScale(root, "secondary", scales.secondary);
  }, [branding.name, branding.favicon_url, branding.color_palette]);

  return (
    <BrandingContext.Provider value={{ branding, logoSrc, faviconSrc, isLoading }}>
      {children}
    </BrandingContext.Provider>
  );
}

export function useBranding() {
  return useContext(BrandingContext);
}