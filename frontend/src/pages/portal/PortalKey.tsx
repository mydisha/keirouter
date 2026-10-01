import { useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, EyeOff, Globe, KeyRound } from "lucide-react";
import {
  fetchPortalKey, fetchPortalStatus, createPortalKey, revealPortalKey,
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

// BaseUrlCard shows the active endpoint the key authenticates against. The
// portal is served from the same origin as the API, so the current origin is
// the correct base URL for the user.
function BaseUrlCard() {
  const baseURL = `${window.location.origin}/v1`;
  return (
    <Card className="p-7 md:p-8">
      <SectionTitle title="Base URL" icon={<Globe size={17} />} />
      <div className="mt-4 flex items-center gap-2 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)]/60 p-3">
        <code className="min-w-0 flex-1 break-all font-mono text-[13px] text-[var(--text)]">{baseURL}</code>
        <CopyButton value={baseURL} />
      </div>
      <p className="mt-3 text-sm text-[var(--text-muted)]">
        Point your OpenAI-compatible client at this URL and authenticate with your API key.
      </p>
    </Card>
  );
}

// KeyDetails is the primary metadata view for a bound key. Provisioned keys are
// revealable (show/hide) because the portal sealed their plaintext; claimed keys
// are masked-only.
function KeyDetails({
  info, provisioning, onCreate, creating, createError,
}: {
  info: PortalKeyInfo;
  provisioning: boolean;
  onCreate: () => void;
  creating: boolean;
  createError: string;
}) {
  const [shown, setShown] = useState(false);
  const reveal = useMutation({ mutationFn: revealPortalKey });

  const fullKey = reveal.data ?? "";
  const value = shown && fullKey ? fullKey : info.display;

  const toggle = () => {
    if (shown) {
      setShown(false);
      return;
    }
    setShown(true);
    if (!reveal.data) reveal.mutate();
  };

  return (
    <Card className="p-7 md:p-8">
      <div className="mb-5 flex items-start justify-between gap-4">
        <SectionTitle title="API Key" icon={<KeyRound size={17} />} />
        {info.disabled && <Badge tone="danger">Disabled</Badge>}
      </div>

      <div className="mb-2 flex items-center gap-2 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)]/60 p-3">
        <code className="min-w-0 flex-1 break-all font-mono text-[13px] text-[var(--text)]">{value}</code>
        {info.revealable && (
          <button
            type="button"
            onClick={toggle}
            disabled={reveal.isPending}
            className="shrink-0 rounded-lg p-2 text-[var(--text-muted)] transition-colors hover:bg-[var(--bg-elevated)] hover:text-[var(--text)] disabled:opacity-50"
            title={shown ? "Hide" : "Show"}
            aria-label={shown ? "Hide API key" : "Show API key"}
          >
            {shown ? <EyeOff size={16} /> : <Eye size={16} />}
          </button>
        )}
        <CopyButton value={value} />
      </div>
      {info.revealable ? (
        reveal.isError && <p className="mb-4 text-sm text-[color:var(--color-danger)]">{(reveal.error as Error).message}</p>
      ) : (
        <p className="mb-4 text-xs text-[var(--text-muted)]">
          This key was claimed, so its full value cannot be shown again. Store claimed keys somewhere safe.
        </p>
      )}

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
      <BaseUrlCard />
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
