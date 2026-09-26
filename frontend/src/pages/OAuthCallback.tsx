import { useEffect, useRef } from "react";
import { useSearchParams, useNavigate } from "react-router-dom";
import { CheckCircle, XCircle } from "lucide-react";
import { dashboard } from "../lib/dashboardRoutes";

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
          navigate(provider ? dashboard(`/providers/${provider}`) : dashboard("/providers"), { replace: true });
        }
        return;
      }
      navigate(provider ? dashboard(`/providers/${provider}`) : dashboard("/providers"), { replace: true });
    }, 1200);

    return () => clearTimeout(t);
  }, [status, provider, navigate]);

  return (
    <div className="flex min-h-screen items-center justify-center bg-[var(--bg)] p-4">
      <div className="w-full max-w-sm space-y-4 rounded-2xl border border-[var(--border)] bg-[var(--bg-elevated)] p-8 text-center shadow-[var(--shadow-float)]">
        {ok ? (
          <>
            <CheckCircle className="mx-auto h-10 w-10 text-emerald-500" />
            <h1 className="text-sm font-semibold text-[var(--text)]">
              Connected{provider ? ` to ${provider}` : ""}
            </h1>
            <p className="text-xs text-[var(--text-muted)]">
              Redirecting back…
            </p>
          </>
        ) : (
          <>
            <XCircle className="mx-auto h-10 w-10 text-red-500" />
            <h1 className="text-sm font-semibold text-[var(--text)]">
              Connection failed
            </h1>
            <p className="text-xs text-[var(--text-muted)]">
              {message || "An unknown error occurred."}
            </p>
            <p className="text-xs text-[var(--text-muted)]">
              Redirecting back…
            </p>
          </>
        )}
      </div>
    </div>
  );
}
