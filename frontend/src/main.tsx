import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router-dom";
import "@fontsource-variable/geist";
import "@fontsource-variable/geist-mono";
import "./index.css";
import { App } from "./App";
import { APIError } from "./lib/api";
import { ToastProvider } from "./components/Toast";
import { ConfirmProvider } from "./components/ui/confirm-dialog";
import { ThemeProvider } from "./components/ThemeProvider";

// shouldRetry retries transient failures (timeouts, 5xx, network errors) but
// fails fast on client errors. Retrying a 401/403/404 just delays the error the
// user needs to see; retrying a 408 timeout or a 502 may recover. Capped at two
// attempts so a genuinely down backend surfaces an error quickly instead of
// leaving the page spinning.
function shouldRetry(failureCount: number, error: unknown): boolean {
  if (failureCount >= 2) return false;
  if (error instanceof APIError) {
    // 408 (our timeout) is worth retrying; other 4xx are not.
    if (error.status === 408) return true;
    if (error.status >= 400 && error.status < 500) return false;
  }
  return true;
}

const queryClient = new QueryClient({
  defaultOptions: {
    // staleTime keeps data fresh across page navigations so revisiting a page
    // serves instantly from cache instead of refetching. gcTime holds the
    // cached data long enough to survive a round-trip away and back.
    queries: {
      retry: shouldRetry,
      // Exponential backoff capped at 4s so retries don't compound the delay
      // before an error or success is shown.
      retryDelay: (attempt) => Math.min(1000 * 2 ** attempt, 4000),
      refetchOnWindowFocus: false,
      staleTime: 30_000,
      gcTime: 300_000,
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <ToastProvider>
            <ConfirmProvider>
              <App />
            </ConfirmProvider>
          </ToastProvider>
        </BrowserRouter>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
);