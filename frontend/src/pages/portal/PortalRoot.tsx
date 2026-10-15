import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Outlet } from "react-router-dom";
import { fetchPortalStatus } from "../../lib/api";
import { useBranding } from "../../contexts/BrandingContext";
import { Button, Spinner } from "../../components/ui";
import { BrandMark } from "../../components/BrandMark";
import { TurnstileWidget, useTurnstileSiteKey } from "../../components/TurnstileWidget";

function SignInCard() {
  const { branding, logoSrc } = useBranding();
  const siteKey = useTurnstileSiteKey();
  const [token, setToken] = useState("");
  const failed = new URLSearchParams(window.location.search).get("turnstile") === "failed";
  const ready = !siteKey || !!token;

  const start = () => {
    if (!ready) return;
    window.location.href = siteKey
      ? `/portal/auth/google/start?token=${encodeURIComponent(token)}`
      : "/portal/auth/google/start";
  };

  return (
    <div className="h-dvh overflow-y-auto overscroll-contain bg-canvas">
      <div
        className="flex min-h-full items-center justify-center px-4"
        style={{
          paddingTop: "max(1.5rem, env(safe-area-inset-top))",
          paddingBottom: "max(1.5rem, env(safe-area-inset-bottom))",
        }}
      >
        <div className="w-full max-w-sm rounded-2xl border border-line bg-surface shadow-[var(--shadow-pop)]">
          <div className="px-5 pb-6 pt-7 sm:px-6">
            <div className="flex flex-col items-center text-center">
              {branding.logo_url ? (
                <img src={logoSrc} alt={branding.name || "KeiRouter"} className="h-8 object-contain" />
              ) : (
                <span className="inline-flex items-center gap-2.5">
                  <BrandMark size={26} />
                  <span className="text-[16px] font-semibold tracking-[-0.01em] text-fg">{branding.name || "KeiRouter"}</span>
                </span>
              )}
              <h1 className="mt-5 text-[15px] font-semibold tracking-[-0.01em] text-fg">Portal Access</h1>
              <p className="mt-1.5 text-[13px] text-fg-muted">
                {branding.tagline || "Sign in with Google to manage your API key and monitor usage."}
              </p>
            </div>
            <div className="mt-6 space-y-4">
              <TurnstileWidget siteKey={siteKey} onToken={setToken} className="flex justify-center" />
              {failed && (
                <p role="alert" className="text-center text-[12px] text-bad">
                  Verification failed. Please complete the challenge and try again.
                </p>
              )}
              <Button className="w-full" onClick={start} disabled={!ready}>
                <GoogleGlyph />
                Sign in with Google
              </Button>
            </div>
          </div>
          <p className="rounded-b-2xl border-t border-line bg-subtle px-6 py-3 text-center text-[12px] text-fg-muted">
            Your API key, usage and top-ups in one place.
          </p>
        </div>
      </div>
    </div>
  );
}

function GoogleGlyph() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path fill="#4285F4" d="M22.6 12.3c0-.8-.1-1.6-.2-2.3H12v4.3h5.9a5 5 0 0 1-2.2 3.3v2.8h3.6c2.1-1.9 3.3-4.8 3.3-8.1Z" />
      <path fill="#34A853" d="M12 23c3 0 5.5-1 7.3-2.7l-3.6-2.8c-1 .7-2.2 1.1-3.7 1.1-2.9 0-5.3-1.9-6.2-4.5H2.1v2.9A11 11 0 0 0 12 23Z" />
      <path fill="#FBBC05" d="M5.8 14.1a6.6 6.6 0 0 1 0-4.2V7H2.1a11 11 0 0 0 0 10l3.7-2.9Z" />
      <path fill="#EA4335" d="M12 5.4c1.6 0 3.1.6 4.2 1.7l3.2-3.2A11 11 0 0 0 2.1 7l3.7 2.9C6.7 7.3 9.1 5.4 12 5.4Z" />
    </svg>
  );
}

export function PortalRootPage() {
  const { data: status, isLoading } = useQuery({
    queryKey: ["portal-status"],
    queryFn: fetchPortalStatus,
    retry: false,
  });
  if (isLoading) {
    return (
      <div className="flex h-dvh items-center justify-center bg-canvas">
        <Spinner />
      </div>
    );
  }
  if (!status?.authenticated) return <SignInCard />;
  return <Outlet />;
}
