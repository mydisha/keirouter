import { useEffect, useRef } from "react";
import { useSearchParams, useNavigate } from "react-router-dom";
import { AlertTriangle, CheckCircle2, Loader2, XCircle } from "lucide-react";
import { BrandMark } from "../components/BrandMark";
import { Button } from "../components/ui";

/**
 * OAuthCallback is the landing page after a provider redirects back to the
 * dashboard.  It reads the status from the URL (set by the backend), notifies
 * the opener tab via postMessage, then redirects back to the provider detail
 * page so the user sees the newly-connected account.
 */
export function OAuthCallbackPage() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const provider = params.get("provider") ?? "";
  const message = params.get("message") ?? "";
  // Raw provider redirect carries code/state; the gateway's server-side handler
  // (embedded mode) instead redirects here with status/message after exchanging.
  const code = params.get("code") ?? "";
  const errorParam = params.get("error") ?? "";
  let status = params.get("status") ?? "";
  if (!status) status = code ? "success" : errorParam ? "error" : "error";
  const ok = status === "success" || !!code;
  const didNotify = useRef(false);

  useEffect(() => {
    if (didNotify.current) return;
    didNotify.current = true;

    // Notify the opener tab (the connect modal) so it can finish the flow.
    // When a raw code is present we forward it for the opener to exchange;
    // otherwise we forward the server-side result status/message.
    if (window.opener) {
      try {
        if (code) {
          const state = params.get("state") ?? "";
          window.opener.postMessage(
            { type: "oauth-callback", code, state, provider },
            "*",
          );
        } else {
          window.opener.postMessage(
            { type: "oauth-callback", status, provider, message: message || errorParam },
            "*",
          );
        }
      } catch {
        // opener may be gone or cross-origin — ignore
      }
    }

    // After a short delay so the user sees the result, close the popup (opener
    // already got the postMessage and refreshes itself). Only navigate when
    // this page was opened directly (no opener) so we don't leave a dangling
    // tab on the provider detail page.
    const t = setTimeout(() => {
      if (window.opener) {
        try {
          window.close();
        } catch {
          // close blocked — fall back to navigating in place
          navigate(provider ? `/providers/${provider}` : "/providers", { replace: true });
        }
        return;
      }
      navigate(provider ? `/providers/${provider}` : "/providers", { replace: true });
    }, 1200);

    return () => clearTimeout(t);
  }, [status, provider, navigate]);

  const target = provider ? `/providers/${provider}` : "/providers";
  const hasOpener = typeof window !== "undefined" && !!window.opener;

  // A raw code is handed to the opener, which finishes the token exchange, so
  // from this window's point of view sign-in is still in progress.
  const phase: "handoff" | "success" | "error" = code && hasOpener ? "handoff" : ok ? "success" : "error";

  const goNext = () => {
    if (window.opener) {
      try {
        window.close();
        return;
      } catch {
        // close blocked — fall through to in-place navigation
      }
    }
    navigate(target, { replace: true });
  };

  const nextLabel = hasOpener ? "Close window" : provider ? "Back to provider" : "Back to providers";

  return (
    <div className="flex min-h-screen items-center justify-center bg-canvas px-4">
      <main
        className="w-full max-w-sm rounded-2xl border border-line bg-surface p-8 text-center shadow-[var(--shadow-pop)]"
      >
        <div className="flex justify-center">
          <BrandMark size={26} />
        </div>

        <div className="mt-5 flex items-center justify-center gap-2">
          {phase === "handoff" ? (
            <Loader2 className="h-4 w-4 shrink-0 animate-spin text-fg-muted" strokeWidth={1.75} aria-hidden="true" />
          ) : phase === "success" ? (
            <CheckCircle2 className="h-4 w-4 shrink-0 text-ok" strokeWidth={1.75} aria-hidden="true" />
          ) : (
            <XCircle className="h-4 w-4 shrink-0 text-bad" strokeWidth={1.75} aria-hidden="true" />
          )}
          <h1 className="text-[15px] font-semibold tracking-[-0.01em] text-fg">
            {phase === "handoff"
              ? "Finishing sign-in"
              : phase === "success"
                ? `Connected${provider ? ` to ${provider}` : ""}`
                : "Connection failed"}
          </h1>
        </div>

        {/* One live region carries the outcome; errors are assertive. */}
        <p className="mt-1.5 text-[13px] leading-5 text-fg-muted" role={phase === "error" ? undefined : "status"}>
          {phase === "handoff"
            ? "Returning to the dashboard. This window closes on its own."
            : phase === "success"
              ? hasOpener
                ? "The account is ready. This window closes on its own."
                : "The account is ready. Returning to the provider…"
              : hasOpener
                ? "Nothing was connected. Try again from the dashboard."
                : "Nothing was connected. Returning so you can try again…"}
        </p>

        {phase === "error" && (
          <div role="alert" className="mt-4 flex items-start gap-2 rounded-lg border border-bad/30 bg-bad/10 px-3 py-2.5 text-left">
            <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-bad" strokeWidth={1.75} aria-hidden="true" />
            <p className="break-words text-[12.5px] leading-5 text-bad">{message || errorParam || "The provider didn't return a reason."}</p>
          </div>
        )}

        <Button variant="secondary" className="mt-6 w-full" onClick={goNext}>
          {nextLabel}
        </Button>
      </main>
    </div>
  );
}
