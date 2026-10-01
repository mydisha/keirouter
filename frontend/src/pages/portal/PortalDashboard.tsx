import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound } from "lucide-react";
import {
  fetchPortalStatus, fetchPortalUsage, createPortalKey, claimPortalKey,
  type PortalStatus,
} from "../../lib/api";
import { Button, Card, ErrorCard, Input, Spinner } from "../../components/ui";
import {
  CopyButton, DateFilter, OverviewSection, RecentRequestsSection, TrendSection,
} from "./components";

function PageHeader({ title, subtitle }: { title: string; subtitle?: string }) {
  return (
    <header className="space-y-1">
      <h1 className="text-2xl font-display font-semibold tracking-tight text-[var(--text)]">{title}</h1>
      {subtitle && <p className="text-sm text-[var(--text-muted)]">{subtitle}</p>}
    </header>
  );
}

// RevealKeyCard shows the freshly created plaintext key exactly once. It is only
// rendered while `value` is set; Continue clears it and refetches status/usage.
function RevealKeyCard({ value, onContinue }: { value: string; onContinue: () => void }) {
  return (
    <Card className="mx-auto w-full max-w-md p-8">
      <div className="mb-6 flex items-center gap-3">
        <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-accent-100 text-accent-700 dark:bg-accent-900/40 dark:text-accent-300">
          <KeyRound size={18} />
        </div>
        <h1 className="text-xl font-display font-semibold tracking-tight text-[var(--text)]">Your API key is ready</h1>
      </div>
      <p className="mb-4 text-sm text-[var(--text-muted)]">
        Copy this key now and store it securely. For your protection it is shown
        only once and cannot be retrieved later.
      </p>
      <div className="mb-6 flex items-center gap-2 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)]/60 p-3">
        <code className="min-w-0 flex-1 break-all font-mono text-[13px] text-[var(--text)]">{value}</code>
        <CopyButton value={value} />
      </div>
      <Button onClick={onContinue} className="w-full">Continue</Button>
    </Card>
  );
}

// SetupCard is the inline onboarding surface for a signed-in user without a key.
function SetupCard({
  status,
  onCreate,
  creating,
  createError,
}: {
  status: PortalStatus;
  onCreate: () => void;
  creating: boolean;
  createError: string;
}) {
  const queryClient = useQueryClient();
  const [claimInput, setClaimInput] = useState("");
  const claim = useMutation({
    mutationFn: () => claimPortalKey(claimInput.trim()),
    onSuccess: () => {
      setClaimInput("");
      queryClient.invalidateQueries({ queryKey: ["portal-status"] });
    },
  });

  return (
    <Card className="mx-auto w-full max-w-md p-8">
      <div className="mb-6 text-center">
        <h2 className="text-xl font-display font-semibold tracking-tight text-[var(--text)]">Set up your API key</h2>
        {status.email && (
          <p className="mt-2 text-sm text-[var(--text-muted)]">
            Signed in as <strong className="font-medium text-[var(--text)]">{status.email}</strong>
          </p>
        )}
      </div>

      {status.provisioning_enabled ? (
        <div className="space-y-5">
          <p className="text-center text-sm text-[var(--text-muted)]">
            Generate an API key on your plan and start monitoring your usage.
          </p>
          {createError && <p className="text-sm text-[color:var(--color-danger)]">{createError}</p>}
          <Button onClick={onCreate} disabled={creating} className="w-full">
            {creating ? "Creating…" : "Create API key"}
          </Button>
        </div>
      ) : (
        <p className="mb-5 text-center text-sm text-[var(--text-muted)]">
          Automatic key provisioning is disabled. Contact an administrator to set up your key.
        </p>
      )}

      <form
        onSubmit={(e) => {
          e.preventDefault();
          const val = claimInput.trim();
          if (val) claim.mutate();
        }}
        className="mt-5 space-y-3 border-t border-[var(--border)] pt-5"
      >
        <label htmlFor="portal-claim-key" className="block text-xs font-semibold uppercase tracking-widest text-[var(--text-muted)]">
          I already have a key
        </label>
        <Input
          id="portal-claim-key"
          type="password"
          value={claimInput}
          onChange={(e) => setClaimInput(e.target.value)}
          placeholder="kr_..."
        />
        {claim.error instanceof Error && <p className="text-sm text-[color:var(--color-danger)]">{claim.error.message}</p>}
        <Button
          type="submit"
          variant="ghost"
          className="w-full"
          disabled={!claimInput.trim() || claim.isPending}
        >
          {claim.isPending ? "Claiming…" : "Claim existing key"}
        </Button>
      </form>
    </Card>
  );
}

export function PortalDashboardPage() {
  const queryClient = useQueryClient();
  const [days, setDays] = useState(30);
  const [createdKey, setCreatedKey] = useState("");

  const { data: status, isLoading: statusLoading } = useQuery({
    queryKey: ["portal-status"],
    queryFn: fetchPortalStatus,
    retry: false,
  });

  const create = useMutation({
    mutationFn: createPortalKey,
    onSuccess: (res) => {
      setCreatedKey(res.key);
      queryClient.invalidateQueries({ queryKey: ["portal-status"] });
    },
  });

  const usage = useQuery({
    queryKey: ["portal-usage", days],
    queryFn: () => fetchPortalUsage(days),
    enabled: !!status?.has_key,
    refetchInterval: 30000,
  });

  if (statusLoading) return <Spinner />;
  if (!status) return <ErrorCard message="Failed to load portal status" />;

  const handleContinue = () => {
    setCreatedKey("");
    queryClient.invalidateQueries({ queryKey: ["portal-status"] });
    queryClient.invalidateQueries({ queryKey: ["portal-key"] });
    queryClient.invalidateQueries({ queryKey: ["portal-usage"] });
  };

  if (createdKey) return <RevealKeyCard value={createdKey} onContinue={handleContinue} />;

  const setup = (
    <div className="space-y-8">
      <PageHeader title="Welcome" subtitle={status.email ? `Signed in as ${status.email}` : undefined} />
      <SetupCard
        status={status}
        onCreate={() => create.mutate()}
        creating={create.isPending}
        createError={create.error instanceof Error ? create.error.message : ""}
      />
    </div>
  );

  if (status?.has_key === false) return setup;

  // The status query normally guarantees a claimed key, but a 409 from
  // /portal/usage (e.g. the claim raced away) falls back to the setup card.
  const noKey =
    usage.error instanceof Error && /no api key claimed/i.test(usage.error.message);
  if (noKey) return setup;

  if (usage.isLoading) return <Spinner />;
  if (usage.isError) {
    const msg = usage.error instanceof Error ? usage.error.message : "Failed to load usage";
    return (
      <div className="space-y-6">
        <PageHeader title="Usage" />
        <ErrorCard message={msg} />
      </div>
    );
  }

  const data = usage.data;
  if (!data) return <Spinner />;

  const daily = data.daily ?? [];
  const recent = (data.recent ?? []).slice(0, 10);

  return (
    <div className="space-y-8">
      <PageHeader
        title={data.key_name || "Your API key"}
        subtitle={`Key ID: ${data.key_id}`}
      />

      <DateFilter days={days} onChange={setDays} />

      <OverviewSection d={data} />

      {daily.length > 0 && <TrendSection daily={daily} days={days} />}

      {recent.length > 0 && <RecentRequestsSection recent={recent} days={days} />}
    </div>
  );
}
