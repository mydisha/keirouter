import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  fetchPortalStatus, fetchPortalUsage, createPortalKey,
} from "../../lib/api";
import { ErrorCard, Spinner } from "../../components/ui";
import {
  DateFilter, OverviewSection, PageHeader, RevealPanel, SetupCard,
} from "./components";

export function PortalDashboardPage() {
  const queryClient = useQueryClient();
  const [days, setDays] = useState(30);
  const [createdKey, setCreatedKey] = useState("");

  const { data: status, isLoading: statusLoading } = useQuery({
    queryKey: ["portal-status"],
    queryFn: fetchPortalStatus,
    retry: false,
  });

  const create = useMutation({
    mutationFn: createPortalKey,
    onSuccess: (res) => {
      setCreatedKey(res.key);
      queryClient.invalidateQueries({ queryKey: ["portal-status"] });
    },
  });

  const usage = useQuery({
    queryKey: ["portal-usage", days],
    queryFn: () => fetchPortalUsage(days),
    enabled: !!status?.has_key,
    refetchInterval: 30000,
  });

  if (statusLoading) return <Spinner />;
  if (!status) return <ErrorCard message="Failed to load portal status" />;

  const handleContinue = () => {
    setCreatedKey("");
    queryClient.invalidateQueries({ queryKey: ["portal-status"] });
    queryClient.invalidateQueries({ queryKey: ["portal-key"] });
    queryClient.invalidateQueries({ queryKey: ["portal-usage"] });
  };

  if (createdKey) return <RevealPanel value={createdKey} onDone={handleContinue} doneLabel="Continue" />;

  const setup = (
    <div className="space-y-8">
      <PageHeader title="Welcome" subtitle={status.email ? `Signed in as ${status.email}` : undefined} />
      <SetupCard
        email={status.email}
        provisioning={!!status.provisioning_enabled}
        description="Generate an API key on your plan and start monitoring your usage."
        onCreate={() => create.mutate()}
        creating={create.isPending}
        createError={create.error instanceof Error ? create.error.message : ""}
      />
    </div>
  );

  if (status?.has_key === false) return setup;

  // The status query normally guarantees a claimed key, but a 409 from
  // /portal/api/usage (e.g. the claim raced away) falls back to the setup card.
  const noKey =
    usage.error instanceof Error && /no api key claimed/i.test(usage.error.message);
  if (noKey) return setup;

  if (usage.isLoading) return <Spinner />;
  if (usage.isError) {
    const msg = usage.error instanceof Error ? usage.error.message : "Failed to load usage";
    return (
      <div className="space-y-6">
        <PageHeader title="Usage" />
        <ErrorCard message={msg} />
      </div>
    );
  }

  const data = usage.data;
  if (!data) return <Spinner />;

  return (
    <div className="space-y-8">
      <DateFilter days={days} onChange={setDays} />

      <OverviewSection d={data} />
    </div>
  );
}
