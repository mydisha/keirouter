import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { CheckCircle2, Key } from "lucide-react";
import { fetchPortalStatus, fetchPortalUsage } from "../../lib/api";
import { Card, EmptyState, ErrorCard, Spinner } from "../../components/ui";
import { portal } from "../../lib/portalRoutes";
import { ModelSection, SectionTitle } from "./components";

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
        <EmptyState
          title="No API key yet"
          hint="Create an API key to see your models and authorized routes."
        />
        <div className="flex justify-center">
          <Link
            to={portal("/key")}
            className="text-sm font-semibold text-accent-600 hover:underline dark:text-accent-400"
          >
            Create an API key
          </Link>
        </div>
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
    <div className="space-y-8">
      {allowed.length > 0 && (
        <section className="space-y-4">
          <SectionTitle title="Authorized Routes" icon={<Key size={17} />} count={allowed.length} />
          <Card className="p-6 md:p-7">
            <div className="flex flex-wrap gap-2.5">
              {allowed.map((m) => (
                <div key={m} className="flex items-center gap-2 rounded-full border border-[var(--border)] bg-[var(--bg-subtle)]/60 px-3.5 py-1.5 text-sm text-[var(--text)] transition-colors hover:bg-[var(--bg-subtle)]">
                  <CheckCircle2 size={15} className="text-accent-500" />
                  <span className="font-mono text-[13px] tracking-tight">{m}</span>
                </div>
              ))}
            </div>
          </Card>
        </section>
      )}

      {models.length > 0 && <ModelSection models={models} />}
    </div>
  );
}
