import { useState, type ReactNode } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { WifiOff } from "lucide-react";
import { api, fetchPortalBranding } from "../lib/api";
import { Card, Button, Input, Field, Spinner } from "./ui";
import { BrandMark } from "./BrandMark";

// AuthGate gates the dashboard behind a login, and surfaces a one-time
// onboarding step that nudges the operator off the default password.
export function AuthGate({ children }: { children: ReactNode }) {
  const status = useQuery({ queryKey: ["auth-status"], queryFn: () => api.authStatus() });

  if (status.isLoading) {
    return (
      <div className="flex h-full items-center justify-center">
        <Spinner />
      </div>
    );
  }
  if (status.isError) {
    return (
      <div className="flex h-full items-center justify-center px-4">
        <Card className="w-full max-w-sm p-8 text-center shadow-[var(--shadow-pop)]">
          <div className="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-2xl bg-[color:var(--color-danger)]/10">
            <WifiOff className="h-7 w-7 text-[color:var(--color-danger)]" strokeWidth={1.75} />
          </div>
          <AuthGateLogo className="mx-auto h-10 object-contain opacity-60" />
          <h1 className="mt-4 text-base font-semibold tracking-tight">Cannot reach <AuthGateName /></h1>
          <p className="mt-1.5 text-sm text-[var(--text-muted)]">
            Is the backend running on <code className="rounded-md bg-[var(--bg-subtle)] px-1.5 py-0.5 font-mono text-xs">:20180</code>?
          </p>
          <button
            onClick={() => status.refetch()}
            className="mt-5 inline-flex items-center gap-1.5 rounded-xl border border-[var(--border)] bg-[var(--bg-elevated)] px-4 py-2 text-sm font-medium text-[var(--text)] transition-colors hover:bg-ink-100 dark:hover:bg-ink-800"
          >
            Try again
          </button>
        </Card>
      </div>
    );
  }

  const s = status.data!;
  if (!s.authenticated) {
    return <LoginScreen />;
  }
  if (s.using_default && !s.onboarding_complete) {
    return <OnboardingScreen />;
  }
  return <>{children}</>;
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

  const login = useMutation({
    mutationFn: () => api.login(password),
    onSuccess: () => {
      setError("");
      qc.invalidateQueries({ queryKey: ["auth-status"] });
    },
    onError: () => setError("Incorrect password"),
  });

  return (
    <div className="flex h-full items-center justify-center px-4">
      <Card className="w-full max-w-sm p-8 shadow-[var(--shadow-pop)]">
        <div className="mb-6 flex flex-col items-center text-center">
          <AuthGateLogo className="h-16 object-contain" />
          <h1 className="mt-4 text-lg font-semibold tracking-tight">Sign in to your dashboard</h1>
          <p className="mt-1 text-sm text-[var(--text-muted)]">Enter your dashboard password to continue.</p>
        </div>
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (password) login.mutate();
          }}
        >
          <Field label="Dashboard password">
            <Input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="••••••••"
              autoFocus
            />
          </Field>
          {error && <p className="text-xs text-[color:var(--color-danger)]">{error}</p>}
          <Button type="submit" className="w-full" disabled={login.isPending || !password}>
            {login.isPending ? "Signing in…" : "Sign in"}
          </Button>
        </form>
        <p className="mt-4 text-center text-xs text-[var(--text-muted)]">
          First run? The default password is{" "}
          <code className="font-mono">keirouter</code>.
        </p>
      </Card>
    </div>
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

  return (
    <div className="flex h-full items-center justify-center px-4">
      <Card className="w-full max-w-md p-8 shadow-[var(--shadow-pop)]">
        <div className="mb-5"><AuthGateLogo className="h-16 object-contain" /></div>
        <h1 className="text-lg font-semibold tracking-tight">Welcome to <AuthGateName /></h1>
        <p className="mt-2 text-sm text-[var(--text-muted)]">
          You're signed in with the default password. Set a new one to secure your dashboard.
        </p>
        <form
          className="mt-5 space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (valid) save.mutate();
          }}
        >
          <Field label="New password">
            <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoFocus />
          </Field>
          <Field label="Confirm password">
            <Input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
          </Field>
          {password && password.length < 6 && (
            <p className="text-xs text-[var(--text-muted)]">Use at least 6 characters.</p>
          )}
          {confirm && password !== confirm && (
            <p className="text-xs text-[color:var(--color-danger)]">Passwords don't match.</p>
          )}
          {error && <p className="text-xs text-[color:var(--color-danger)]">{error}</p>}
          <div className="flex items-center justify-between">
            <Button variant="ghost" type="button" onClick={() => skip.mutate()} disabled={skip.isPending}>
              Keep default for now
            </Button>
            <Button type="submit" disabled={save.isPending || !valid}>
              {save.isPending ? "Saving…" : "Set password"}
            </Button>
          </div>
        </form>
      </Card>
    </div>
  );
}