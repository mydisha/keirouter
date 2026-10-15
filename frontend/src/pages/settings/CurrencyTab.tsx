import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleDollarSign } from "lucide-react";
import { api, type CurrencyStatus } from "../../lib/api";
import { useToast } from "../../components/Toast";
import { MarketPricingSection } from "../../components/MarketPricingSection";
import { Button, ErrorBanner, Skeleton } from "../../components/ui";
import { FormField, SettingsCard, ToggleRow, inputClass } from "./shared";

// ── Currency tab ────────────────────────────────────────────────────
type CurrencyConfig = CurrencyStatus["config"];

const SOURCE_LABEL: Record<string, string> = {
  api: "Live API",
  override: "Manual override",
  none: "No rate yet",
};

export function CurrencyTab() {
  const qc = useQueryClient();
  const toast = useToast();
  const status = useQuery({ queryKey: ["currency-settings"], queryFn: () => api.currencySettings() });
  const [local, setLocal] = useState<CurrencyConfig | null>(null);

  useEffect(() => {
    if (status.data) setLocal(status.data.config);
  }, [status.data]);

  const save = useMutation({
    mutationFn: (patch: Partial<CurrencyConfig>) => api.updateCurrencySettings(patch),
    onSuccess: (data) => {
      setLocal(data.config);
      qc.setQueryData(["currency-settings"], data);
      toast.success("Currency settings saved");
    },
    onError: (e) => toast.error("Save failed", (e as Error).message),
  });

  const refresh = useMutation({
    mutationFn: () => api.refreshCurrency(),
    onSuccess: (data) => {
      setLocal(data.config);
      qc.setQueryData(["currency-settings"], data);
      if (data.last_error) toast.error("Refresh failed", data.last_error);
      else toast.success("Rate updated", `1 USD = ${data.effective_rate} IDR`);
    },
    onError: (e) => toast.error("Refresh failed", (e as Error).message),
  });

  const update = (patch: Partial<CurrencyConfig>) => {
    if (local) setLocal({ ...local, ...patch });
  };

  const handleSave = () => {
    if (!local) return;
    save.mutate({
      auto_refresh_enabled: local.auto_refresh_enabled,
      refresh_interval_h: local.refresh_interval_h,
      override_enabled: local.override_enabled,
      override_rate: local.override_rate,
      source_url: local.source_url,
    });
  };

  if (status.isError) {
    return <ErrorBanner message={`Failed to load currency settings: ${(status.error as Error)?.message ?? "unknown error"}`} />;
  }
  if (status.isLoading || !local) return <Skeleton className="h-72 w-full rounded-2xl" />;

  const st = status.data!;

  return (
    <div className="space-y-4">
      <SettingsCard
        icon={CircleDollarSign}
        title="Currency"
        description="USD→IDR rate used to convert IDR top-ups into USD credit. Auto-refreshes on an interval, with an optional manual override."
        footer={
          <div className="flex flex-col gap-3 border-t border-line bg-subtle px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-5">
            <div className="min-w-0 text-[12.5px]">
              <p className="font-medium text-fg">1 USD = {st.effective_rate > 0 ? `${st.effective_rate} IDR` : "—"}</p>
              <p className="mt-0.5 text-[12px] text-fg-muted">
                Source: {SOURCE_LABEL[st.source] ?? st.source}
                {st.fetched_at ? ` · updated ${new Date(st.fetched_at).toLocaleString()}` : ""}
              </p>
              {st.last_error && <p className="mt-1 text-[12px] text-bad">Last error: {st.last_error}</p>}
            </div>
            <div className="flex gap-2">
              <Button variant="ghost" onClick={() => refresh.mutate()} disabled={refresh.isPending}>
                {refresh.isPending ? "Refreshing…" : "Refresh now"}
              </Button>
              <Button onClick={handleSave} disabled={save.isPending}>
                {save.isPending ? "Saving…" : "Save"}
              </Button>
            </div>
          </div>
        }
      >
        <ToggleRow
          label="Auto refresh"
          description="Fetch the rate automatically in the background."
          checked={local.auto_refresh_enabled}
          onChange={(v) => update({ auto_refresh_enabled: v })}
        />
        <div className="px-4 py-4">
          <FormField
            label="Refresh interval (hours)"
            htmlFor="currency-interval"
            hint="Default 24. The upstream rate updates about once a day."
          >
            <input
              id="currency-interval"
              type="number"
              min={1}
              className={inputClass}
              value={local.refresh_interval_h}
              onChange={(e) => update({ refresh_interval_h: Number(e.target.value) || 1 })}
            />
          </FormField>
        </div>
        <ToggleRow
          label="Override rate"
          description="While on, the API rate is ignored entirely."
          checked={local.override_enabled}
          onChange={(v) => update({ override_enabled: v })}
        />
        <div className="px-4 py-4">
          <FormField label="IDR per 1 USD" htmlFor="currency-override">
            <input
              id="currency-override"
              type="number"
              min={0}
              step="0.01"
              className={inputClass}
              disabled={!local.override_enabled}
              value={local.override_rate}
              onChange={(e) => update({ override_rate: Number(e.target.value) || 0 })}
            />
          </FormField>
        </div>
        <div className="px-4 py-4">
          <FormField label="Source URL" htmlFor="currency-source">
            <input
              id="currency-source"
              className={inputClass}
              value={local.source_url}
              onChange={(e) => update({ source_url: e.target.value })}
            />
          </FormField>
        </div>
      </SettingsCard>
      <MarketPricingSection />
    </div>
  );
}
