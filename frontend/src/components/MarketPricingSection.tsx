import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { LineChart } from "lucide-react";
import { api, type MarketPricingSettings } from "../lib/api";
import { Button, Card, Field, Input, SectionHeader, Spinner, Toggle } from "./ui";
import { useToast } from "./Toast";

export function MarketPricingSection() {
  const qc = useQueryClient();
  const toast = useToast();
  const settings = useQuery({ queryKey: ["market-pricing-settings"], queryFn: () => api.marketPricingSettings() });
  const [markup, setMarkup] = useState("");
  const [intervalSec, setIntervalSec] = useState("");
  const [safetyPct, setSafetyPct] = useState("");
  const [depth, setDepth] = useState("");
  useEffect(() => {
    if (settings.data) {
      setMarkup(String(settings.data.markup_percent));
      setIntervalSec(String(settings.data.refresh_interval_seconds));
      setSafetyPct(String(settings.data.safety_margin_percent));
      setDepth(String(settings.data.surplus_fallback_depth ?? 3));
    }
  }, [settings.data]);

  const save = useMutation({
    mutationFn: (patch: Partial<Pick<MarketPricingSettings, "auto_refresh" | "refresh_interval_seconds" | "markup_percent" | "safety_margin_enabled" | "safety_margin_percent" | "surplus_fallback_depth">>) =>
      api.updateMarketPricingSettings(patch),
    onSuccess: (data) => {
      qc.setQueryData(["market-pricing-settings"], data);
      toast.success("Saved", "Market pricing settings updated.");
    },
    onError: (e) => toast.error("Save failed", (e as Error).message),
  });
  const refresh = useMutation({
    mutationFn: () => api.refreshMarketPrices(),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["market-pricing-settings"] });
      qc.invalidateQueries({ queryKey: ["chains"] });
      toast.success("Synced", `${r.synced} chain price(s) updated.`);
    },
    onError: (e) => {
      qc.invalidateQueries({ queryKey: ["market-pricing-settings"] });
      toast.error("Sync failed", (e as Error).message);
    },
  });

  if (settings.isLoading || !settings.data) return <Spinner />;
  const data = settings.data;
  const markupTrim = markup.trim();
  const markupInvalid =
    markupTrim === "" || !Number.isFinite(Number(markup)) || Number(markup) < 0 || Number(markup) > 1000;
  const intervalTrim = intervalSec.trim();
  const intervalInvalid =
    intervalTrim === "" || !Number.isInteger(Number(intervalSec)) || Number(intervalSec) < 1;
  const safetyTrim = safetyPct.trim();
  const safetyInvalid =
    safetyTrim === "" || !Number.isFinite(Number(safetyPct)) || Number(safetyPct) < 0 || Number(safetyPct) > 1000;
  const depthInvalid =
    depth.trim() === "" || !Number.isInteger(Number(depth)) || Number(depth) < 0 || Number(depth) > 20;

  return (
    <div className="space-y-4">
      <Card>
        <SectionHeader
          title="Market Pricing"
          description="Auto-update chain prices from inferhub.dev and surplusintelligence.ai. Markup is applied over the cheapest slug bound on each chain."
          icon={LineChart}
          iconTone="neutral"
        />
        <div className="divide-y divide-[var(--border)] border-t border-[var(--border)]">
          <div className="flex items-center justify-between gap-4 px-6 py-4">
            <div>
              <p className="text-sm font-medium">Auto-refresh</p>
              <p className="mt-0.5 text-xs text-[var(--text-muted)]">
                Periodically sync bound chain prices from the market.
              </p>
            </div>
            <Toggle checked={data.auto_refresh} onChange={(v) => save.mutate({ auto_refresh: v })} />
          </div>

          <div className="flex items-center justify-between gap-4 px-6 py-4">
            <div>
              <p className="text-sm font-medium">Safety margin</p>
              <p className="mt-0.5 text-xs text-[var(--text-muted)]">
                Floor the price at the next-best (failover) market offer plus this margin, so a
                cheaper offer going down cannot sell below its fallback cost. Off = use markup only.
              </p>
            </div>
            <Toggle checked={data.safety_margin_enabled} onChange={(v) => save.mutate({ safety_margin_enabled: v })} />
          </div>

          <div className="px-6 py-5">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field label="Markup %">
                <Input
                  type="number"
                  min={0}
                  max={1000}
                  value={markup}
                  onChange={(e) => setMarkup(e.target.value)}
                  className="w-28"
                />
                <p className="mt-1 text-xs text-[var(--text-muted)]">
                  Added on top of the cheapest bound slug price.
                </p>
              </Field>
              <Field label="Refresh interval (seconds)">
                <Input
                  type="number"
                  min={1}
                  value={intervalSec}
                  onChange={(e) => setIntervalSec(e.target.value)}
                  className="w-24"
                />
              </Field>
              <Field label="Safety margin %">
                <Input
                  type="number"
                  min={0}
                  max={1000}
                  value={safetyPct}
                  onChange={(e) => setSafetyPct(e.target.value)}
                  className="w-28"
                  disabled={!data.safety_margin_enabled}
                />
                <p className="mt-1 text-xs text-[var(--text-muted)]">
                  Applied over the next-best offer when safety margin is on.
                </p>
              </Field>
              <Field label="Surplus fallback depth">
                <Input
                  type="number"
                  min={0}
                  max={20}
                  value={depth}
                  onChange={(e) => setDepth(e.target.value)}
                  className="w-24"
                />
                <p className="mt-1 text-xs text-[var(--text-muted)]">
                  For <code>surplus-trusted:</code> slugs: Surplus may fail over across this many trusted
                  price levels (sent as X-Min-Discount). Users are billed Surplus's reported cost plus markup.
                  0 = no ceiling.
                </p>
              </Field>
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-3 px-6 py-4">
            <Button
              onClick={() =>
                save.mutate({
                  markup_percent: Number(markup),
                  refresh_interval_seconds: Number(intervalSec),
                  safety_margin_percent: Number(safetyPct),
                  surplus_fallback_depth: Number(depth),
                })
              }
              disabled={save.isPending || markupInvalid || intervalInvalid || safetyInvalid || depthInvalid}
            >
              {save.isPending ? "Saving…" : "Save"}
            </Button>
            <Button variant="ghost" onClick={() => refresh.mutate()} disabled={refresh.isPending}>
              {refresh.isPending ? "Refreshing…" : "Refresh now"}
            </Button>
            {data.last_fetched_at && (
              <span className="text-xs text-[var(--text-muted)]">
                Last sync {new Date(data.last_fetched_at).toLocaleString()} · {data.last_synced_count} updated
              </span>
            )}
            {data.last_fetch_error && (
              <span className="text-xs text-[color:var(--color-danger)]">{data.last_fetch_error}</span>
            )}
          </div>
        </div>
      </Card>
    </div>
  );
}
