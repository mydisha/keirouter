import { useId, useState, type ReactNode } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { WifiOff } from "lucide-react";
import { api, fetchPortalBranding } from "../lib/api";
import { Button, Input, Spinner } from "./ui";
import { BrandMark } from "./BrandMark";

// AuthGate gates the dashboard behind a login, and surfaces a one-time
// onboarding step that nudges the operator off the default password.
export function AuthGate({ children }: { children: ReactNode }) {
  const status = useQuery({ queryKey: ["auth-status"], queryFn: () => api.authStatus() });

  // No data and nothing in flight (e.g. the fetch was paused or settled
  // without a body) is treated as unreachable rather than crashing below.
  const unreachable = status.isError || (!status.data && !status.isFetching && !status.isLoading);
  if (!unreachable && !status.data) {
    return (
      <div className="flex h-full items-center justify-center">
        <Spinner label="Checking sign-in" />
      </div>
    );
  }
  if (unreachable || !status.data) {
    return (
      <AuthShell>
        <div className="px-6 py-7 text-center">
          <AuthGateLogo className="mx-auto h-8 object-contain" />
          <div role="alert">
            <h1 className="mt-5 flex items-center justify-center gap-1.5 text-[13px] font-medium text-bad">
              <WifiOff className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
              Cannot reach <AuthGateName />
            </h1>
            <p className="mt-1.5 text-[13px] text-fg-muted">
              Check that the backend is running on <code className="rounded-md bg-subtle px-1.5 py-0.5 font-mono text-[12px] text-fg">:20180</code>.
            </p>
          </div>
          <Button variant="secondary" className="mt-5" onClick={() => status.refetch()}>
            Try again
          </Button>
        </div>
      </AuthShell>
    );
  }

  const s = status.data;
  if (!s.authenticated) {
    return <LoginScreen />;
  }
  if (s.using_default && !s.onboarding_complete) {
    return <OnboardingScreen />;
  }
  return <>{children}</>;
}

// AuthShell centres one minimal card on the canvas for the pre-dashboard screens.
function AuthShell({ children, wide = false }: { children: ReactNode; wide?: boolean }) {
  return (
    <div className="flex h-full min-h-full items-center justify-center bg-canvas px-4 py-10">
      <div className={`w-full ${wide ? "max-w-md" : "max-w-sm"} rounded-2xl border border-line bg-surface shadow-[var(--shadow-pop)]`}>
        {children}
      </div>
    </div>
  );
}

function AuthGateLogo({ className }: { className?: string }) {
  const { data } = useQuery({
    queryKey: ["portal-branding"],
    queryFn: fetchPortalBranding,
    staleTime: 5 * 60_000,
    retry: false,
  });
  if (data?.logo_url) {
    return <img src={data.logo_url} alt={data.name || "KeiRouter"} className={className} />;
  }
  // Built-in identity: the mark plus the configured name as text, so the
  // login screen matches the sidebar instead of a raster wordmark.
  return (
    <span className="inline-flex items-center gap-2.5">
      <BrandMark size={26} />
      <span className="text-[16px] font-semibold tracking-[-0.01em] text-fg">{data?.name || "KeiRouter"}</span>
    </span>
  );
}

function AuthGateName() {
  const { data } = useQuery({
    queryKey: ["portal-branding"],
    queryFn: fetchPortalBranding,
    staleTime: 5 * 60_000,
    retry: false,
  });
  return <>{data?.name || "KeiRouter"}</>;
}

function LoginScreen() {
  const qc = useQueryClient();
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const uid = useId();
  const inputId = `${uid}-password`;
  const errorId = `${uid}-error`;
  const hintId = `${uid}-hint`;

  const login = useMutation({
    mutationFn: () => api.login(password),
    onSuccess: () => {
      setError("");
      qc.invalidateQueries({ queryKey: ["auth-status"] });
    },
    onError: () => setError("Incorrect password. Try again."),
  });

  return (
    <AuthShell>
      <div className="px-6 pb-6 pt-7">
        <div className="flex flex-col items-center text-center">
          <AuthGateLogo className="h-8 object-contain" />
          <h1 className="mt-5 text-[15px] font-semibold tracking-[-0.01em] text-fg">Sign in to your dashboard</h1>
        </div>
        <form
          className="mt-6 space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (password) login.mutate();
          }}
        >
          <div className="space-y-1.5">
            <label htmlFor={inputId} className="block text-[12.5px] font-medium text-fg">
              Dashboard password
            </label>
            <Input
              id={inputId}
              type="password"
              value={password}
              onChange={(e) => {
                setPassword(e.target.value);
                if (error) setError("");
              }}
              autoComplete="current-password"
              aria-required="true"
              aria-invalid={error ? true : undefined}
              aria-describedby={error ? `${errorId} ${hintId}` : hintId}
              autoFocus
            />
            {error && (
              <p id={errorId} role="alert" className="text-[12px] text-bad">
                {error}
              </p>
            )}
          </div>
          <Button type="submit" className="w-full" disabled={login.isPending || !password}>
            {login.isPending ? "Signing in…" : "Sign in"}
          </Button>
          <span className="sr-only" role="status">
            {login.isPending ? "Signing in" : ""}
          </span>
        </form>
      </div>
      <p id={hintId} className="rounded-b-2xl border-t border-line bg-subtle px-6 py-3 text-center text-[12px] text-fg-muted">
        First run? The default password is <code className="font-mono text-fg">keirouter</code>.
      </p>
    </AuthShell>
  );
}

function OnboardingScreen() {
  const qc = useQueryClient();
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");

  const save = useMutation({
    mutationFn: async () => {
      await api.changePassword(password);
      await api.completeOnboarding();
    },
    onSuccess: () => {
      setError("");
      qc.invalidateQueries({ queryKey: ["auth-status"] });
    },
    onError: (e: Error) => setError(e.message),
  });

  const skip = useMutation({
    mutationFn: () => api.completeOnboarding(),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["auth-status"] }),
  });

  const valid = password.length >= 6 && password === confirm;
  const uid = useId();
  const ids = {
    password: `${uid}-new`,
    passwordHint: `${uid}-new-hint`,
    confirm: `${uid}-confirm`,
    confirmError: `${uid}-confirm-error`,
    error: `${uid}-error`,
  };
  const tooShort = password.length > 0 && password.length < 6;
  const mismatch = confirm.length > 0 && password !== confirm;

  return (
    <AuthShell wide>
      <div className="px-6 pb-6 pt-7">
        <AuthGateLogo className="h-8 object-contain" />
        <h1 className="mt-5 text-[15px] font-semibold tracking-[-0.01em] text-fg">Welcome to <AuthGateName /></h1>
        <p className="mt-1 text-[13px] leading-5 text-fg-muted">
          You're using the default password. Set a new one to secure this dashboard.
        </p>
        <form
          className="mt-5 space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (valid) save.mutate();
          }}
        >
          <div className="space-y-1.5">
            <label htmlFor={ids.password} className="block text-[12.5px] font-medium text-fg">
              New password
            </label>
            <Input
              id={ids.password}
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="new-password"
              aria-required="true"
              aria-describedby={ids.passwordHint}
              autoFocus
            />
            <p id={ids.passwordHint} className={`text-[12px] ${tooShort ? "text-fg" : "text-fg-muted"}`}>
              At least 6 characters.
            </p>
          </div>
          <div className="space-y-1.5">
            <label htmlFor={ids.confirm} className="block text-[12.5px] font-medium text-fg">
              Confirm password
            </label>
            <Input
              id={ids.confirm}
              type="password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              autoComplete="new-password"
              aria-required="true"
              aria-invalid={mismatch ? true : undefined}
              aria-describedby={mismatch ? ids.confirmError : undefined}
            />
            {mismatch && (
              <p id={ids.confirmError} className="text-[12px] text-bad">
                Passwords don't match.
              </p>
            )}
          </div>
          {error && <p id={ids.error} role="alert" className="text-[12px] text-bad">{error}</p>}
          <div className="flex items-center justify-between gap-2 pt-1">
            <Button variant="ghost" type="button" onClick={() => skip.mutate()} disabled={skip.isPending}>
              Keep default for now
            </Button>
            <Button type="submit" disabled={save.isPending || !valid}>
              {save.isPending ? "Saving…" : "Set password"}
            </Button>
          </div>
        </form>
      </div>
    </AuthShell>
  );
}
