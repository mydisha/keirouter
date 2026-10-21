import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Globe } from "lucide-react";
import { api, type PublicPricingModel, type PublicPricingSettings } from "../../lib/api";
import { useToast } from "../../components/Toast";
import { Badge, Button, ErrorBanner, Skeleton } from "../../components/ui";
import { CodeBlock } from "../../components/ui/code-block";
import { SettingsCard, ToggleRow } from "./shared";

const QUERY_KEY = ["public-pricing"];

function money(v: number | null): string {
  if (v === null) return "—";
  return `$${v < 0.01 ? v.toPrecision(3) : v.toFixed(4)}`;
}

function PriceCell({ m }: { m: PublicPricingModel }) {
  if (!m.available) return <Badge tone="warning">Sold out</Badge>;
  return (
    <span className="tabular-nums text-[12.5px] text-fg-muted">
      <span className="text-fg">{money(m.input_per_m)}</span> in · <span className="text-fg">{money(m.output_per_m)}</span> out
      <span className="hidden sm:inline"> per 1M</span>
    </span>
  );
}

// PublicPricingTab picks which Surplus-priced chains GET /v1/public/pricing
// shows without an API key. Off with nothing picked by default.
export function PublicPricingTab() {
  const qc = useQueryClient();
  const toast = useToast();
  const query = useQuery({ queryKey: QUERY_KEY, queryFn: () => api.publicPricing() });
  const [enabled, setEnabled] = useState(false);
  const [picked, setPicked] = useState<Set<string>>(new Set());

  const sync = (d: PublicPricingSettings) => {
    setEnabled(d.enabled);
    setPicked(new Set(d.chain_ids));
  };
  useEffect(() => {
    if (query.data) sync(query.data);
  }, [query.data]);

  const save = useMutation({
    mutationFn: () => api.updatePublicPricing({ enabled, chain_ids: [...picked] }),
    onSuccess: (d) => {
      qc.setQueryData(QUERY_KEY, d);
      sync(d);
      toast.success("Public pricing saved");
    },
    onError: (e) => toast.error("Save failed", (e as Error).message),
  });

  if (query.isError) {
    return <ErrorBanner message={`Failed to load public pricing: ${(query.error as Error)?.message ?? "unknown error"}`} />;
  }
  if (query.isLoading || !query.data) return <Skeleton className="h-72 w-full rounded-2xl" />;

  const data = query.data;
  const saved = new Set(data.chain_ids);
  const dirty = enabled !== data.enabled || picked.size !== saved.size || [...picked].some((id) => !saved.has(id));
  const url = `${window.location.origin}${data.path}`;
  const names = data.models.filter((m) => picked.has(m.chain_id)).map((m) => m.model);
  const example = `curl "${url}${names.length > 0 ? `?model=${names.slice(0, 2).join(",")}` : ""}"`;

  const toggle = (id: string, on: boolean) => {
    const next = new Set(picked);
    if (on) next.add(id);
    else next.delete(id);
    setPicked(next);
  };

  return (
    <div className="space-y-4">
      <SettingsCard
        icon={Globe}
        title="Public pricing"
        description="Show live Surplus prices for the models you pick, without an API key. Prices include your markup; upstream cost and margin stay private."
        action={data.enabled ? <Badge tone="success">On · {data.chain_ids.length} published</Badge> : <Badge>Off</Badge>}
        footer={
          <div className="flex flex-col gap-3 border-t border-line bg-subtle px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-5">
            <p className="text-[12px] text-fg-muted">
              Limited to 30 requests a minute per visitor IP. Prices follow the market refresh interval.
            </p>
            <div className="flex gap-2">
              <Button variant="ghost" onClick={() => sync(data)} disabled={!dirty || save.isPending}>
                Discard
              </Button>
              <Button onClick={() => save.mutate()} disabled={!dirty || save.isPending}>
                {save.isPending ? "Saving…" : "Save"}
              </Button>
            </div>
          </div>
        }
      >
        <ToggleRow
          label="Enable public pricing"
          description="While off, the public URL answers 404 for every model."
          checked={enabled}
          onChange={setEnabled}
        />
        <div className="px-4 py-4">
          <p className="mb-2 text-[12.5px] font-medium text-fg">Published models</p>
          {data.models.length === 0 ? (
            <p className="text-[12.5px] text-fg-muted">
              No chain is priced by Surplus yet. Add a surplus-trusted market slug to a chain to publish it here.
            </p>
          ) : (
            <ul className="divide-y divide-line rounded-lg border border-line">
              {data.models.map((m) => (
                <li key={m.chain_id}>
                  <label className="flex cursor-pointer flex-col gap-1 px-3 py-2.5 transition-colors hover:bg-hover sm:flex-row sm:items-center sm:justify-between sm:gap-4">
                    <span className="flex min-w-0 items-center gap-2.5">
                      <input
                        type="checkbox"
                        checked={picked.has(m.chain_id)}
                        onChange={(e) => toggle(m.chain_id, e.target.checked)}
                        className="h-4 w-4 shrink-0 accent-accent-500"
                      />
                      <span className="break-all font-mono text-[13px] text-fg">{m.model}</span>
                    </span>
                    <span className="pl-[26px] sm:pl-0">
                      <PriceCell m={m} />
                    </span>
                  </label>
                </li>
              ))}
            </ul>
          )}
          <p className="mt-2 text-[12px] text-fg-muted">Only chains priced by Surplus are listed. Unchecked models are hidden from the public URL.</p>
        </div>
        <div className="space-y-2 px-4 py-4">
          <p className="text-[12.5px] font-medium text-fg">Public URL</p>
          <CodeBlock code={example} language="bash" copyToast="Command copied" wrap />
          <p className="text-[12px] text-fg-muted">
            Add <code className="rounded-md bg-subtle px-1 py-px font-mono text-[11.5px] text-fg">?model=a,b</code> to pick models, or leave it out for all published ones.
          </p>
        </div>
      </SettingsCard>
    </div>
  );
}
