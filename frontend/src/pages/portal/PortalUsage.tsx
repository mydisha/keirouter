import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { BarChart3 } from "lucide-react";
import { fetchPortalStatus, fetchPortalUsage } from "../../lib/api";
import { EmptyState, ErrorCard, Spinner } from "../../components/ui";
import { portal } from "../../lib/portalRoutes";
import {
  DateFilter, InsightsSection, PageHeader, RecentRequestsSection, TrendSection,
} from "./components";

export function PortalUsagePage() {
  const [days, setDays] = useState(30);

  const { data: status, isLoading: statusLoading } = useQuery({
    queryKey: ["portal-status"],
    queryFn: fetchPortalStatus,
    retry: false,
  });

  const usage = useQuery({
    queryKey: ["portal-usage", days],
    queryFn: () => fetchPortalUsage(days),
    enabled: !!status?.has_key,
  });

  if (statusLoading) return <Spinner />;
  if (!status) return <ErrorCard message="Failed to load portal status" />;

  if (status.has_key === false) {
    return (
      <div className="space-y-6">
        <PageHeader icon={BarChart3} title="Usage" />
        <EmptyState
          variant="card"
          icon={BarChart3}
          title="No API key yet"
          hint="Create an API key to start tracking usage."
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

  const daily = data.daily ?? [];
  const recent = data.recent ?? [];

  return (
    <div className="space-y-6">
      <PageHeader
        icon={BarChart3}
        title="Usage"
        subtitle="Daily trend, token mix and every recent request."
        action={<DateFilter days={days} onChange={setDays} />}
      />

      {daily.length > 0 && <TrendSection daily={daily} days={days} />}

      <InsightsSection d={data} />

      {recent.length > 0 && <RecentRequestsSection recent={recent} days={days} />}
    </div>
  );
}
