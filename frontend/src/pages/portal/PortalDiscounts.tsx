import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { BadgePercent, Layers } from "lucide-react";
import {
  fetchPortalDiscounts, fetchPortalStatus, resetPortalModelDiscount, setPortalDiscountFloor, setPortalModelDiscount,
  type PortalDiscountModel,
} from "../../lib/api";
import { Badge, Button, EmptyState, ErrorCard, Input, Spinner } from "../../components/ui";
import { portal } from "../../lib/portalRoutes";
import { PageHeader, PanelCard } from "./components";

const EXAMPLE_LIST_PRICE = 10;
const QUERY_KEY = ["portal-discounts"];

function parsePct(value: string, max: number): number | null {
  const n = Number(value);
  return value.trim() !== "" && Number.isInteger(n) && n >= 0 && n <= max ? n : null;
}

// DefaultDiscountCard sets the minimum discount for every model without its
// own setting. The worked example turns the percentage into money.
function DefaultDiscountCard({ current, max }: { current: number; max: number }) {
  const queryClient = useQueryClient();
  const [value, setValue] = useState(String(current));
  const save = useMutation({
    mutationFn: setPortalDiscountFloor,
    onSuccess: (res) => {
      setValue(String(res.min_discount_pct));
      queryClient.invalidateQueries({ queryKey: QUERY_KEY });
      queryClient.invalidateQueries({ queryKey: ["portal-key"] });
    },
  });

  const parsed = parsePct(value, max);
  const dirty = parsed !== null && parsed !== current;
  const examplePct = parsed !== null && parsed > 0 ? parsed : 30;
  const examplePays = (EXAMPLE_LIST_PRICE * (100 - examplePct)) / 100;

  return (
    <PanelCard
      icon={BadgePercent}
      title="Default minimum discount"
      subtitle="Used for every model below that doesn't have its own number. Models marked No discount guarantee are skipped."
      action={current > 0 ? <Badge tone="success">At least {current}% off</Badge> : <Badge>Off</Badge>}
    >
      <form
        className="space-y-2 p-4 sm:p-5"
        onSubmit={(e) => {
          e.preventDefault();
          if (dirty && parsed !== null) save.mutate(parsed);
        }}
      >
        <label htmlFor="discount-default" className="block text-[12.5px] font-medium text-fg">
          Minimum discount (%)
        </label>
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <Input
            id="discount-default"
            type="number"
            inputMode="numeric"
            min={0}
            max={max}
            step={1}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            aria-invalid={parsed === null}
            aria-describedby="discount-default-help"
            className="tabular-nums sm:w-32"
          />
          <Button type="submit" disabled={!dirty || save.isPending}>
            {save.isPending ? "Saving…" : "Save"}
          </Button>
        </div>
        <p id="discount-default-help" className="text-[12px] text-fg-muted">
          {parsed !== null ? `0 turns it off. Whole numbers from 0 to ${max}.` : `Enter a whole number from 0 to ${max}.`}
        </p>
        {save.isError && <p role="alert" className="text-[12.5px] text-bad">{(save.error as Error).message}</p>}
        {save.isSuccess && !dirty && <p role="status" className="text-[12.5px] text-fg-muted">Saved.</p>}
        <div className="mt-3 space-y-1.5 rounded-md border border-line bg-subtle p-3 text-[12.5px] text-fg-muted">
          <p className="font-medium text-fg">How it works</p>
          <p>
            Pick how much cheaper than the official price a request must be. Example: the official price is{" "}
            ${EXAMPLE_LIST_PRICE.toFixed(2)} per 1M tokens. At {examplePct}%, you never pay more than{" "}
            <span className="font-medium tabular-nums text-fg">${examplePays.toFixed(2)}</span> per 1M tokens.
          </p>
          <p>
            If no one sells the model that cheaply right now, we stop the request (error 422) and you pay nothing. Try
            again in 15 seconds, or lower the number.
          </p>
          <p>A higher number means you pay less, but more requests get stopped. Set 0 to turn it off.</p>
        </div>
      </form>
    </PanelCard>
  );
}

// ModelDiscountRow edits one model's own minimum, or returns it to the default.
function ModelDiscountRow({ row, defaultPct, max }: { row: PortalDiscountModel; defaultPct: number; max: number }) {
  const queryClient = useQueryClient();
  const [value, setValue] = useState(String(row.override_pct ?? row.effective_pct));
  const done = () => queryClient.invalidateQueries({ queryKey: QUERY_KEY });
  const save = useMutation({ mutationFn: (pct: number) => setPortalModelDiscount(row.model, pct), onSuccess: done });
  const reset = useMutation({
    mutationFn: () => resetPortalModelDiscount(row.model),
    onSuccess: () => {
      setValue(String(defaultPct));
      done();
    },
  });

  const parsed = parsePct(value, max);
  const own = row.override_pct !== null;
  const dirty = parsed !== null && (own ? parsed !== row.override_pct : parsed !== defaultPct);
  const inputId = `discount-${row.model}`;
  const helpId = `${inputId}-help`;
  const error = save.error ?? reset.error;

  if (!row.supported) {
    // No discount guarantee: the model always runs at its set price, so it
    // takes no minimum and the default never reaches it.
    return (
      <li className="flex flex-col gap-3 px-4 py-3.5 sm:flex-row sm:items-start sm:justify-between sm:px-5">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <label htmlFor={inputId} className="break-all font-mono text-[13px] font-medium text-fg">{row.model}</label>
            <Badge tone="warning">No discount guarantee</Badge>
          </div>
          <p id={helpId} className="text-[12px] text-fg-muted">
            Runs at its set price. A minimum discount doesn't apply to this model.
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <div className="w-24 shrink-0">
            <Input id={inputId} type="number" value="0" disabled readOnly aria-describedby={helpId} className="tabular-nums" />
          </div>
          <span className="text-[12.5px] text-fg-muted" aria-hidden="true">%</span>
        </div>
      </li>
    );
  }

  return (
    <li className="flex flex-col gap-3 px-4 py-3.5 sm:flex-row sm:items-start sm:justify-between sm:px-5">
      <div className="min-w-0 space-y-1">
        <div className="flex flex-wrap items-center gap-2">
          <label htmlFor={inputId} className="break-all font-mono text-[13px] font-medium text-fg">{row.model}</label>
          {own ? <Badge tone="accent">Own setting</Badge> : <Badge>Default</Badge>}
        </div>
        <p id={helpId} className="text-[12px] text-fg-muted">
          {row.effective_pct > 0 ? `At least ${row.effective_pct}% off` : "No minimum"}
          {own ? "" : ` (follows the default)`}.
        </p>
        {error && <p role="alert" className="text-[12.5px] text-bad">{(error as Error).message}</p>}
      </div>
      <form
        className="flex shrink-0 flex-wrap items-center gap-2 sm:flex-nowrap"
        onSubmit={(e) => {
          e.preventDefault();
          if (dirty && parsed !== null) save.mutate(parsed);
        }}
      >
        <div className="w-24 shrink-0">
          <Input
            id={inputId}
            type="number"
            inputMode="numeric"
            min={0}
            max={max}
            step={1}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            aria-invalid={parsed === null}
            aria-describedby={helpId}
            className="tabular-nums"
          />
        </div>
        <span className="text-[12.5px] text-fg-muted" aria-hidden="true">%</span>
        <Button type="submit" disabled={!dirty || save.isPending}>
          {save.isPending ? "Saving…" : "Save"}
        </Button>
        {own && (
          <Button type="button" variant="ghost" disabled={reset.isPending} onClick={() => reset.mutate()}>
            {reset.isPending ? "Resetting…" : "Use default"}
          </Button>
        )}
      </form>
    </li>
  );
}

export function PortalDiscountsPage() {
  const status = useQuery({ queryKey: ["portal-status"], queryFn: fetchPortalStatus, retry: false });
  const discounts = useQuery({ queryKey: QUERY_KEY, queryFn: fetchPortalDiscounts, enabled: !!status.data?.has_key });

  if (status.isLoading) return <Spinner />;
  if (!status.data) return <ErrorCard message="Failed to load portal status" />;

  if (status.data.has_key === false) {
    return (
      <div className="space-y-6">
        <PageHeader icon={BadgePercent} title="Discount Settings" />
        <EmptyState
          variant="card"
          icon={BadgePercent}
          title="No API key yet"
          hint="Create an API key to set the minimum discount for your requests."
          action={<Link to={portal("/key")} className="text-[13px] font-medium text-link hover:underline">Create an API key</Link>}
        />
      </div>
    );
  }

  if (discounts.isLoading) return <Spinner />;
  if (discounts.isError || !discounts.data) {
    const msg = discounts.error instanceof Error ? discounts.error.message : "Failed to load discount settings";
    return (
      <div className="space-y-6">
        <PageHeader icon={BadgePercent} title="Discount Settings" />
        <ErrorCard message={msg} />
      </div>
    );
  }

  const { default_pct: defaultPct, max_pct: max, models } = discounts.data;

  return (
    <div className="space-y-6">
      <PageHeader
        icon={BadgePercent}
        title="Discount Settings"
        subtitle="Choose the smallest discount your requests must get, for all models or one model at a time."
      />
      <DefaultDiscountCard key={`default-${defaultPct}`} current={defaultPct} max={max} />
      <PanelCard
        icon={Layers}
        title="Per-model minimum discount"
        subtitle="A model's own setting replaces the default for that model. Use 0 to turn the minimum off for one model."
        count={models.length}
      >
        {models.length === 0 ? (
          <EmptyState title="No models available to this key" />
        ) : (
          <ul className="divide-y divide-line">
            {models.map((m) => (
              <ModelDiscountRow key={`${m.model}-${m.override_pct ?? "d"}-${defaultPct}`} row={m} defaultPct={defaultPct} max={max} />
            ))}
          </ul>
        )}
      </PanelCard>
    </div>
  );
}
