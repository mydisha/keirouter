import { useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound } from "lucide-react";
import {
  fetchPortalKey, fetchPortalStatus, createPortalKey, claimPortalKey,
  type PortalKeyInfo,
} from "../../lib/api";
import { Badge, Button, Card, ErrorCard, Input, Spinner } from "../../components/ui";
import { CopyButton, SectionTitle, formatDateTime } from "./components";

function PageHeader({ title, subtitle }: { title: string; subtitle?: string }) {
  return (
    <header className="space-y-1">
      <h1 className="text-2xl font-display font-semibold tracking-tight text-[var(--text)]">{title}</h1>
      {subtitle && <p className="text-sm text-[var(--text-muted)]">{subtitle}</p>}
    </header>
  );
}

// RevealPanel shows the freshly created plaintext key exactly once. Continue
// clears it and refetches status/metadata.
function RevealPanel({ value, onDone }: { value: string; onDone: () => void }) {
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
      <Button onClick={onDone} className="w-full">Done</Button>
    </Card>
  );
}

// ClaimForm binds an existing key to the signed-in account.
function ClaimForm({ onClaimed }: { onClaimed?: () => void }) {
  const queryClient = useQueryClient();
  const [value, setValue] = useState("");
  const claim = useMutation({
    mutationFn: () => claimPortalKey(value.trim()),
    onSuccess: () => {
      setValue("");
      queryClient.invalidateQueries({ queryKey: ["portal-status"] });
      queryClient.invalidateQueries({ queryKey: ["portal-key"] });
      onClaimed?.();
    },
  });

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (value.trim()) claim.mutate();
      }}
      className="space-y-3"
    >
      <label htmlFor="portal-claim-key" className="block text-xs font-semibold uppercase tracking-widest text-[var(--text-muted)]">
        Claim an existing key
      </label>
      <Input
        id="portal-claim-key"
        type="password"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        placeholder="kr_..."
      />
      {claim.error instanceof Error && (
        <p className="text-sm text-[color:var(--color-danger)]">{claim.error.message}</p>
      )}
      <Button type="submit" variant="ghost" className="w-full" disabled={!value.trim() || claim.isPending}>
        {claim.isPending ? "Claiming…" : "Claim key"}
      </Button>
    </form>
  );
}

// SetupCard is the onboarding surface for a signed-in user without a binding.
function SetupCard({
  provisioning, email, onCreate, creating, createError,
}: {
  provisioning: boolean;
  email?: string;
  onCreate: () => void;
  creating: boolean;
  createError: string;
}) {
  return (
    <Card className="mx-auto w-full max-w-md p-8">
      <div className="mb-6 text-center">
        <h2 className="text-xl font-display font-semibold tracking-tight text-[var(--text)]">Set up your API key</h2>
        {email && (
          <p className="mt-2 text-sm text-[var(--text-muted)]">
            Signed in as <strong className="font-medium text-[var(--text)]">{email}</strong>
          </p>
        )}
      </div>

      {provisioning ? (
        <div className="space-y-5">
          <p className="text-center text-sm text-[var(--text-muted)]">
            Generate an API key on your plan and start using the endpoint.
          </p>
          {createError && <p className="text-sm text-[color:var(--color-danger)]">{createError}</p>}
          <Button onClick={onCreate} disabled={creating} className="w-full">
            {creating ? "Creating…" : "Create API key"}
          </Button>
        </div>
      ) : (
        <p className="text-center text-sm text-[var(--text-muted)]">
          Automatic key provisioning is disabled. Contact an administrator to set up your key.
        </p>
      )}

      <div className="mt-5 border-t border-[var(--border)] pt-5">
        <ClaimForm />
      </div>
    </Card>
  );
}

function MetaRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1 border-b border-[var(--border)] py-3 last:border-0 sm:flex-row sm:items-center sm:justify-between sm:gap-6">
      <dt className="text-xs font-semibold uppercase tracking-widest text-[var(--text-muted)]">{label}</dt>
      <dd className="text-sm text-[var(--text)]">{children}</dd>
    </div>
  );
}

// KeyDetails is the primary metadata view for a bound key.
function KeyDetails({
  info, provisioning, onCreate, creating, createError,
}: {
  info: PortalKeyInfo;
  provisioning: boolean;
  onCreate: () => void;
  creating: boolean;
  createError: string;
}) {
  return (
    <Card className="p-7 md:p-8">
      <div className="mb-5 flex items-start justify-between gap-4">
        <SectionTitle title="API Key" icon={<KeyRound size={17} />} />
        {info.disabled && <Badge tone="danger">Disabled</Badge>}
      </div>

      <div className="mb-6 flex items-center gap-2 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)]/60 p-3">
        <code className="min-w-0 flex-1 break-all font-mono text-[13px] text-[var(--text)]">{info.display}</code>
        <CopyButton value={info.display} />
      </div>

      <dl>
        <MetaRow label="Name">{info.name}</MetaRow>
        <MetaRow label="Key ID"><span className="font-mono text-[13px]">{info.key_id}</span></MetaRow>
        {info.plan_name && <MetaRow label="Plan">{info.plan_name}</MetaRow>}
        <MetaRow label="Created">{formatDateTime(info.created_at)}</MetaRow>
        {info.last_used_at && <MetaRow label="Last used">{formatDateTime(info.last_used_at)}</MetaRow>}
      </dl>

      <details className="mt-6 border-t border-[var(--border)] pt-5">
        <summary className="cursor-pointer list-none text-sm font-medium text-[var(--text-muted)] transition-colors hover:text-[var(--text)]">
          I already have / want a different key
        </summary>
        <div className="mt-4 space-y-4">
          {provisioning && (
            <div className="space-y-2">
              <Button variant="ghost" className="w-full" onClick={onCreate} disabled={creating}>
                {creating ? "Creating…" : "Generate a new key"}
              </Button>
              {createError && <p className="text-sm text-[color:var(--color-danger)]">{createError}</p>}
            </div>
          )}
          <ClaimForm />
        </div>
      </details>
    </Card>
  );
}

export function PortalKeyPage() {
  const queryClient = useQueryClient();
  const [createdKey, setCreatedKey] = useState("");

  const statusQuery = useQuery({
    queryKey: ["portal-status"],
    queryFn: fetchPortalStatus,
    retry: false,
  });
  const keyQuery = useQuery({
    queryKey: ["portal-key"],
    queryFn: fetchPortalKey,
    retry: false,
  });

  const create = useMutation({
    mutationFn: createPortalKey,
    onSuccess: (res) => {
      setCreatedKey(res.key);
      queryClient.invalidateQueries({ queryKey: ["portal-status"] });
    },
  });

  const handleDone = () => {
    setCreatedKey("");
    queryClient.invalidateQueries({ queryKey: ["portal-status"] });
    queryClient.invalidateQueries({ queryKey: ["portal-key"] });
  };

  if (createdKey) return <RevealPanel value={createdKey} onDone={handleDone} />;

  if (statusQuery.isLoading) return <Spinner />;

  const keyError = keyQuery.error instanceof Error ? keyQuery.error.message : "";
  const noBinding =
    statusQuery.data?.has_key === false ||
    (keyQuery.isError && /no api key claimed/i.test(keyError));

  if (noBinding) {
    return (
      <div className="space-y-8">
        <PageHeader title="API Key" subtitle={statusQuery.data?.email ? `Signed in as ${statusQuery.data.email}` : undefined} />
        <SetupCard
          provisioning={!!statusQuery.data?.provisioning_enabled}
          email={statusQuery.data?.email}
          onCreate={() => create.mutate()}
          creating={create.isPending}
          createError={create.error instanceof Error ? create.error.message : ""}
        />
      </div>
    );
  }

  if (keyQuery.isLoading) return <Spinner />;
  if (keyQuery.isError || !keyQuery.data) {
    return (
      <div className="space-y-8">
        <PageHeader title="API Key" />
        <ErrorCard message={keyError || "Failed to load key"} />
      </div>
    );
  }

  return (
    <div className="space-y-8">
      <PageHeader title="Your API key" subtitle="Manage the credential used to authenticate requests." />
      <KeyDetails
        info={keyQuery.data}
        provisioning={!!statusQuery.data?.provisioning_enabled}
        onCreate={() => create.mutate()}
        creating={create.isPending}
        createError={create.error instanceof Error ? create.error.message : ""}
      />
    </div>
  );
}
