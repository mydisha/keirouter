import { useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound } from "lucide-react";
import {
  fetchPortalKey, fetchPortalStatus, createPortalKey,
  type PortalKeyInfo,
} from "../../lib/api";
import { Badge, Button, Card, ErrorCard, Spinner } from "../../components/ui";
import {
  ClaimForm, CopyButton, PageHeader, RevealPanel, SectionTitle, SetupCard, formatDateTime,
} from "./components";

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
          <ClaimForm label="Claim an existing key" submitLabel="Claim key" />
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
    enabled: statusQuery.data?.has_key !== false,
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

  if (createdKey) return <RevealPanel value={createdKey} onDone={handleDone} doneLabel="Done" />;

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
          email={statusQuery.data?.email}
          provisioning={!!statusQuery.data?.provisioning_enabled}
          description="Generate an API key on your plan and start using the endpoint."
          claimLabel="Claim an existing key"
          claimSubmitLabel="Claim key"
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
