import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { CheckCircle2, Key, Layers } from "lucide-react";
import { fetchPortalStatus, fetchPortalUsage } from "../../lib/api";
import { EmptyState, ErrorCard, Spinner } from "../../components/ui";
import { portal } from "../../lib/portalRoutes";
import { ModelSection, PageHeader, PanelCard } from "./components";

export function PortalModelsPage() {
  const { data: status, isLoading: statusLoading } = useQuery({
    queryKey: ["portal-status"],
    queryFn: fetchPortalStatus,
    retry: false,
  });

  const usage = useQuery({
    queryKey: ["portal-usage", 30],
    queryFn: () => fetchPortalUsage(30),
    enabled: !!status?.has_key,
  });

  if (statusLoading) return <Spinner />;
  if (!status) return <ErrorCard message="Failed to load portal status" />;

  if (status.has_key === false) {
    return (
      <div className="space-y-6">
        <PageHeader icon={Layers} title="Models" />
        <EmptyState
          variant="card"
          icon={Layers}
          title="No API key yet"
          hint="Create an API key to see your models and authorized routes."
          action={<Link to={portal("/key")} className="text-[13px] font-medium text-link hover:underline">Create an API key</Link>}
        />
      </div>
    );
  }

  if (usage.isLoading) return <Spinner />;
  if (usage.isError) {
    const msg = usage.error instanceof Error ? usage.error.message : "Failed to load usage";
    return <ErrorCard message={msg} />;
  }

  const data = usage.data;
  if (!data) return <Spinner />;

  const models = data.models ?? [];
  const allowed = data.allowed_models ?? [];

  return (
    <div className="space-y-6">
      <PageHeader icon={Layers} title="Models" subtitle="Models this key may call, and what it used in the last 30 days." />
      {allowed.length > 0 && (
        <PanelCard icon={Key} title="Authorized routes" count={allowed.length}>
          <ul className="flex flex-wrap gap-2 p-4 sm:p-5">
            {allowed.map((m) => (
              <li key={m} className="inline-flex items-center gap-1.5 rounded-lg border border-line bg-subtle px-2.5 py-1.5">
                <CheckCircle2 className="h-3.5 w-3.5 text-ok" strokeWidth={2} aria-hidden="true" />
                <span className="font-mono text-[12.5px] text-fg">{m}</span>
              </li>
            ))}
          </ul>
        </PanelCard>
      )}

      {models.length > 0 && <ModelSection models={models} />}
      {allowed.length === 0 && models.length === 0 && (
        <EmptyState variant="card" icon={Layers} title="No model usage yet" hint="Requests made with your key will show up here." />
      )}
    </div>
  );
}
