import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { Wallet } from "lucide-react";
import { fetchPortalStatus, fetchPortalTopups } from "../../lib/api";
import { Card, EmptyState, ErrorCard, Spinner } from "../../components/ui";
import { formatUSD } from "../../lib/format";
import { portal } from "../../lib/portalRoutes";
import { SectionTitle, formatDateTime } from "./components";

export function PortalTopupPage() {
  const { data: status, isLoading: statusLoading } = useQuery({
    queryKey: ["portal-status"],
    queryFn: fetchPortalStatus,
    retry: false,
  });

  const topup = useQuery({
    queryKey: ["portal-topups"],
    queryFn: fetchPortalTopups,
    retry: false,
    enabled: !!status?.has_key,
  });

  if (statusLoading) return <Spinner />;
  if (!status) return <ErrorCard message="Failed to load portal status" />;

  if (status.has_key === false) {
    return (
      <div className="space-y-6">
        <EmptyState
          title="No API key yet"
          hint="Create an API key to see your top-up history."
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

  if (topup.isLoading) return <Spinner />;
  if (topup.isError) {
    const msg = topup.error instanceof Error ? topup.error.message : "Failed to load top-ups";
    return <ErrorCard message={msg} />;
  }

  const data = topup.data;
  if (!data) return <Spinner />;

  const topups = data.topups ?? [];
  const balance = data.balance;

  return (
    <div className="space-y-8">
      <section className="space-y-4">
        <SectionTitle title="Balance" icon={<Wallet size={17} />} />
        <Card className="p-6 md:p-7">
          {balance ? (
            <div className="grid grid-cols-1 gap-5 sm:grid-cols-3">
              <BalanceStat label="Budget Limit" value={formatUSD(balance.limit_usd)} />
              <BalanceStat label="Spent" value={formatUSD(balance.spent_usd)} />
              <BalanceStat label="Remaining" value={formatUSD(balance.usd_remaining)} accent />
            </div>
          ) : (
            <p className="py-2 text-sm text-[var(--text-muted)]">No budget limit configured.</p>
          )}
        </Card>
      </section>

      <section className="space-y-4">
        <SectionTitle title="Top-up History" icon={<Wallet size={17} />} count={topups.length} />
        <Card>
          {topups.length === 0 ? (
            <EmptyState title="No top-ups yet" />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="border-b border-[var(--border)] bg-[var(--bg-subtle)]/50">
                  <tr className="text-[11px] uppercase tracking-wide text-[var(--text-muted)]">
                    <th className="px-6 py-4 text-left font-semibold">Date</th>
                    <th className="px-6 py-4 text-left font-semibold">Reason</th>
                    <th className="px-6 py-4 text-right font-semibold">Amount</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[var(--border)]">
                  {topups.map((t) => (
                    <tr key={t.id} className="transition-colors hover:bg-[var(--bg-subtle)]/30">
                      <td className="px-6 py-4 whitespace-nowrap tabular-nums text-[var(--text-muted)]">
                        {formatDateTime(t.created_at)}
                      </td>
                      <td className="px-6 py-4 text-[var(--text)]">{t.reason || "—"}</td>
                      <td className="px-6 py-4 text-right font-semibold tabular-nums text-[var(--text)]">
                        {formatUSD(t.amount_usd)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Card>
      </section>

      <p className="text-xs text-[var(--text-muted)]">Top-ups are applied by an administrator.</p>
    </div>
  );
}

function BalanceStat({ label, value, accent }: { label: string; value: string; accent?: boolean }) {
  return (
    <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)]/40 px-4 py-3">
      <p className="text-[11px] font-semibold uppercase tracking-widest text-[var(--text-muted)]">{label}</p>
      <p className={`mt-1 text-2xl font-display font-semibold tabular-nums tracking-tight ${accent ? "text-accent-600 dark:text-accent-400" : "text-[var(--text)]"}`}>
        {value}
      </p>
    </div>
  );
}
