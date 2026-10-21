import { useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BadgePercent, Eye, EyeOff, Globe, KeyRound, RefreshCw } from "lucide-react";
import {
  fetchPortalKey, fetchPortalStatus, createPortalKey, revealPortalKey, setPortalDiscountFloor,
  type PortalKeyInfo,
} from "../../lib/api";
import { Badge, Button, ErrorCard, Input, Spinner } from "../../components/ui";
import {
  ClaimForm, IdField, PageHeader, PanelCard, RevealPanel, SetupCard, formatDateTime,
} from "./components";

// BaseUrlCard shows the active endpoint the key authenticates against. The
// portal is served from the same origin as the API, so the current origin is
// the correct base URL for the user.
function BaseUrlCard() {
  const baseURL = `${window.location.origin}/v1`;
  return (
    <PanelCard icon={Globe} title="Base URL" subtitle="Point your OpenAI-compatible client at this URL and authenticate with your API key.">
      <div className="p-4 sm:p-5">
        <IdField value={baseURL} />
      </div>
    </PanelCard>
  );
}

const MAX_DISCOUNT_FLOOR = 95;

// DiscountFloorCard sets the minimum discount every request must meet. The
// server refuses requests no route can serve at that price, before any charge.
function DiscountFloorCard({ current }: { current: number }) {
  const queryClient = useQueryClient();
  const [value, setValue] = useState(String(current));
  const save = useMutation({
    mutationFn: setPortalDiscountFloor,
    onSuccess: (res) => {
      setValue(String(res.min_discount_pct));
      queryClient.invalidateQueries({ queryKey: ["portal-key"] });
    },
  });

  const parsed = Number(value);
  const valid = value.trim() !== "" && Number.isInteger(parsed) && parsed >= 0 && parsed <= MAX_DISCOUNT_FLOOR;
  const dirty = valid && parsed !== current;

  return (
    <PanelCard
      icon={BadgePercent}
      title="Minimum discount"
      subtitle="Only run requests that cost at least this much less than the model's official price. Requests that can't meet it are refused and not charged."
      action={current > 0 ? <Badge tone="success">{current}% floor</Badge> : <Badge>Off</Badge>}
    >
      <form
        className="space-y-2 p-4 sm:p-5"
        onSubmit={(e) => {
          e.preventDefault();
          if (dirty) save.mutate(parsed);
        }}
      >
        <label htmlFor="discount-floor" className="block text-[12.5px] font-medium text-fg">
          Minimum discount (%)
        </label>
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <Input
            id="discount-floor"
            type="number"
            inputMode="numeric"
            min={0}
            max={MAX_DISCOUNT_FLOOR}
            step={1}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            aria-invalid={!valid}
            aria-describedby="discount-floor-help"
            className="tabular-nums sm:w-32"
          />
          <Button type="submit" disabled={!dirty || save.isPending}>
            {save.isPending ? "Saving…" : "Save"}
          </Button>
        </div>
        <p id="discount-floor-help" className="text-[12px] text-fg-muted">
          {valid ? `0 turns it off. Whole numbers from 0 to ${MAX_DISCOUNT_FLOOR}.` : `Enter a whole number from 0 to ${MAX_DISCOUNT_FLOOR}.`}
        </p>
        {save.isError && <p role="alert" className="text-[12.5px] text-bad">{(save.error as Error).message}</p>}
        {save.isSuccess && !dirty && <p role="status" className="text-[12.5px] text-fg-muted">Saved.</p>}
      </form>
    </PanelCard>
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
    <>
      <PanelCard
        icon={KeyRound}
        title="API key"
        action={info.disabled ? <Badge tone="danger">Disabled</Badge> : <Badge tone="success">Active</Badge>}
      >
        <div className="space-y-2 p-4 sm:p-5">
          <IdField value={value}>
            {info.revealable && (
              <button
                type="button"
                onClick={toggle}
                disabled={reveal.isPending}
                className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-fg-muted transition-colors hover:bg-hover hover:text-fg disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
                title={shown ? "Hide" : "Show"}
                aria-label={shown ? "Hide API key" : "Show API key"}
              >
                {shown ? <EyeOff className="h-4 w-4" aria-hidden="true" /> : <Eye className="h-4 w-4" aria-hidden="true" />}
              </button>
            )}
          </IdField>
          {info.revealable ? (
            reveal.isError && <p role="alert" className="text-[12.5px] text-bad">{(reveal.error as Error).message}</p>
          ) : (
            <p className="text-[12px] text-fg-muted">
              This key was claimed, so its full value cannot be shown again. Store claimed keys somewhere safe.
            </p>
          )}
        </div>
        <dl className="divide-y divide-line border-t border-line">
          <MetaRow label="Name"><span className="font-mono text-[12.5px]">{info.name}</span></MetaRow>
          <MetaRow label="Key ID"><span className="font-mono text-[12.5px]">{info.key_id}</span></MetaRow>
          {info.plan_name && <MetaRow label="Plan">{info.plan_name}</MetaRow>}
          <MetaRow label="Created"><span className="tabular-nums">{formatDateTime(info.created_at)}</span></MetaRow>
          {info.last_used_at && <MetaRow label="Last used"><span className="tabular-nums">{formatDateTime(info.last_used_at)}</span></MetaRow>}
        </dl>
      </PanelCard>

      <PanelCard icon={RefreshCw} title="Use a different key" subtitle="I already have / want a different key">
        <div className="space-y-4 p-4 sm:p-5">
          {provisioning && (
            <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
              <div>
                <p className="text-[13px] font-medium text-fg">Generate a new key</p>
                <p className="mt-0.5 text-[12px] text-fg-muted">Issue a fresh key on your plan.</p>
              </div>
              <Button variant="ghost" onClick={onCreate} disabled={creating}>
                {creating ? "Creating…" : "Generate a new key"}
              </Button>
            </div>
          )}
          {createError && <p role="alert" className="text-[12.5px] text-bad">{createError}</p>}
          <div className={provisioning ? "border-t border-line pt-4" : undefined}>
            <ClaimForm label="Claim an existing key" submitLabel="Claim key" />
          </div>
        </div>
      </PanelCard>
    </>
  );
}

function MetaRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1 px-4 py-2.5 sm:flex-row sm:items-center sm:justify-between sm:gap-6 sm:px-5">
      <dt className="text-[13px] text-fg-muted">{label}</dt>
      <dd className="min-w-0 break-all text-[13px] text-fg sm:text-right">{children}</dd>
    </div>
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
      <div className="space-y-6">
        <PageHeader icon={KeyRound} title="API Key" subtitle={statusQuery.data?.email ? `Signed in as ${statusQuery.data.email}` : undefined} />
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
      <div className="space-y-6">
        <PageHeader icon={KeyRound} title="API Key" />
        <ErrorCard message={keyError || "Failed to load key"} />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader icon={KeyRound} title="Your API key" subtitle="Manage the credential used to authenticate requests." />
      <BaseUrlCard />
      <KeyDetails
        info={keyQuery.data}
        provisioning={!!statusQuery.data?.provisioning_enabled}
        onCreate={() => create.mutate()}
        creating={create.isPending}
        createError={create.error instanceof Error ? create.error.message : ""}
      />
      <DiscountFloorCard key={keyQuery.data.key_id} current={keyQuery.data.min_discount_pct ?? 0} />
    </div>
  );
}
