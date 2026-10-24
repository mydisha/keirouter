import { useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { dashboard } from "../../lib/dashboardRoutes";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ExternalLink, Loader2, Plus, Server } from "lucide-react";
import { cn } from "@/lib/utils";
import { api, type FinanceReport, type UpstreamAccountBalance } from "../../lib/api";
import { useToast } from "../../components/Toast";
import { Badge, Button, EmptyState, ErrorBanner, Field, Input, LoadingRegion, Modal, ModalFooter, Skeleton } from "../../components/ui";
import { QK, Panel, count, dateID, dateInputToRFC3339, errorText, toDateInput, todayISO, usd } from "./shared";

export function UpstreamAccounts({
  report,
  loading,
  error,
  rangeLabel,
}: {
  report?: FinanceReport;
  loading: boolean;
  error: string | null;
  rangeLabel: string;
}) {
  const [topupFor, setTopupFor] = useState<UpstreamAccountBalance | null>(null);
  const accounts = (report?.accounts ?? []).filter((a) => a.status !== "archived");
  // Active accounts first, planned ones after; the API order is kept inside each group.
  const sorted = [...accounts.filter((a) => a.status !== "planned"), ...accounts.filter((a) => a.status === "planned")];
  const unmapped = report?.unmapped;

  return (
    <Panel icon={Server} title="Upstream provider balances" subtitle={`Balance is the opening balance plus top-ups minus usage. Spent: ${rangeLabel.toLowerCase()}.`}>
      {loading && !report ? (
        <LoadingRegion label="Loading provider balances" className="grid grid-cols-1 gap-3 p-4 md:grid-cols-2 xl:grid-cols-3">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-[220px] w-full rounded-xl" />
          ))}
        </LoadingRegion>
      ) : !report ? (
        <div className="p-4">
          <ErrorBanner message={`Couldn't load provider balances. ${error ?? ""}`.trim()} />
        </div>
      ) : (
        <div className="space-y-3 p-4">
          {unmapped && (unmapped.spent_range_usd > 0 || unmapped.requests > 0) && (
            <div role="status" className="flex flex-col gap-2 rounded-lg border border-warn/30 bg-warn/10 px-3.5 py-2.5 sm:flex-row sm:items-center sm:justify-between">
              <div className="flex min-w-0 items-start gap-2.5">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warn" strokeWidth={1.75} aria-hidden="true" />
                <p className="min-w-0 break-words text-[13px] leading-snug text-fg">
                  {usd(unmapped.spent_range_usd)} from {count(unmapped.requests)} requests isn't assigned to any account
                  {unmapped.providers.length > 0 && <> ({unmapped.providers.join(", ")})</>}. Account balances don't include this usage.
                </p>
              </div>
              <Link to={dashboard("/finance/settings")} className="shrink-0 text-[12.5px] font-medium text-link hover:underline">
                Set up provider mapping
              </Link>
            </div>
          )}
          {sorted.length === 0 ? (
            <EmptyState icon={Server} size="sm" title="No upstream accounts yet" hint="Upstream accounts show up here once they are created in settings." />
          ) : (
            <ul className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
              {sorted.map((a) => (
                <AccountCard key={a.id} account={a} onTopup={() => setTopupFor(a)} />
              ))}
            </ul>
          )}
        </div>
      )}
      {topupFor && <TopupModal account={topupFor} onClose={() => setTopupFor(null)} />}
    </Panel>
  );
}

function daysLeftText(a: UpstreamAccountBalance): string {
  if (a.days_left == null) return "No usage in the last 7 days";
  if (a.days_left <= 0) return "Empty";
  if (a.days_left < 1) return "Less than 1 day";
  return `About ${Math.floor(a.days_left).toLocaleString("en-US")} days`;
}

function AccountCard({ account: a, onTopup }: { account: UpstreamAccountBalance; onTopup: () => void }) {
  const planned = a.status === "planned";
  return (
    <li
      className={cn(
        "flex min-w-0 flex-col rounded-xl border bg-surface p-4",
        a.low && !planned ? "border-bad/40" : "border-line",
        planned && "bg-subtle",
      )}
    >
      <div className={cn("flex min-w-0 items-start justify-between gap-2", planned && "opacity-70")}>
        <div className="min-w-0">
          <h3 className="truncate text-[14px] font-semibold text-fg">{a.name}</h3>
          {a.url && (
            <a href={a.url} target="_blank" rel="noreferrer" className="inline-flex max-w-full items-center gap-1 truncate text-[12px] text-fg-muted hover:text-fg hover:underline">
              <span className="truncate">{a.url.replace(/^https?:\/\//, "")}</span>
              <ExternalLink className="h-3 w-3 shrink-0" aria-hidden="true" />
            </a>
          )}
        </div>
        <div className="flex shrink-0 flex-wrap justify-end gap-1">
          {planned && <Badge>Planned</Badge>}
          {a.low && !planned && <Badge tone="danger">Low balance</Badge>}
        </div>
      </div>

      <div className={cn("mt-3", planned && "opacity-70")}>
        <p className="text-[12px] text-fg-muted">Balance</p>
        <p className={cn("text-[22px] font-semibold leading-tight tabular-nums", a.low && !planned ? "text-bad" : "text-fg")}>{usd(a.balance_usd)}</p>
        <p className="mt-0.5 text-[12px] text-fg-muted">
          Alert threshold {a.low_balance_usd > 0 ? usd(a.low_balance_usd) : "not set"}
          {a.spent_estimated_usd > 0 && <> · includes {usd(a.spent_estimated_usd)} estimated</>}
        </p>
      </div>

      <dl className={cn("mt-3 grid grid-cols-2 gap-x-3 gap-y-2 text-[12.5px]", planned && "opacity-70")}>
        <div className="min-w-0">
          <dt className="text-fg-muted">Spent in range</dt>
          <dd className="tabular-nums text-fg">{usd(a.spent_range_usd)}</dd>
        </div>
        <div className="min-w-0">
          <dt className="text-fg-muted">Runs out in</dt>
          <dd className="text-fg">{daysLeftText(a)}</dd>
        </div>
        <div className="min-w-0">
          <dt className="text-fg-muted">Active requests</dt>
          <dd className="tabular-nums text-fg">{count(a.in_flight)}</dd>
        </div>
        <div className="min-w-0">
          <dt className="text-fg-muted">Daily average</dt>
          <dd className="tabular-nums text-fg">{usd(a.avg_daily_usd)}</dd>
        </div>
      </dl>

      <div className={cn("mt-3 flex min-w-0 flex-wrap gap-1", planned && "opacity-70")}>
        {a.providers.length === 0 ? (
          <span className="text-[12px] text-fg-faint">No providers mapped yet</span>
        ) : (
          a.providers.map((p) => (
            <Badge key={p} className="max-w-full truncate">
              {p}
            </Badge>
          ))
        )}
      </div>

      <div className="mt-4 flex flex-1 items-end">
        <Button variant="ghost" onClick={onTopup} className="w-full sm:w-auto">
          <Plus aria-hidden="true" /> Record top-up
        </Button>
      </div>
    </li>
  );
}

function TopupModal({ account, onClose }: { account: UpstreamAccountBalance; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [amount, setAmount] = useState("");
  const [date, setDate] = useState(todayISO());
  const [note, setNote] = useState("");
  const [submitted, setSubmitted] = useState(false);

  const amountNum = Number(amount);
  const amountError =
    amount.trim() === "" || !Number.isFinite(amountNum) || amountNum <= 0
      ? "Enter an amount above 0."
      : amountNum > 1_000_000
        ? "Maximum amount is $1,000,000."
        : null;
  const openingDate = toDateInput(account.opening_at);
  const dateError =
    date && date > todayISO()
      ? "Date can't be in the future."
      : date && openingDate && date < openingDate
        ? `Date can't be before the opening balance date (${dateID(account.opening_at)}).`
        : null;
  const noteError = note.length > 500 ? "Note can be at most 500 characters." : null;
  const valid = !amountError && !dateError && !noteError;

  const create = useMutation({
    mutationFn: () => api.createUpstreamTopup(account.id, { amount_usd: amountNum, topup_at: dateInputToRFC3339(date, account.opening_at) || undefined, note: note.trim() || undefined }),
    onSuccess: () => {
      toast.success("Top-up recorded", `${usd(amountNum)} added to the ${account.name} balance.`);
      qc.invalidateQueries({ queryKey: QK.finance });
      qc.invalidateQueries({ queryKey: QK.accounts });
      qc.invalidateQueries({ queryKey: QK.topups });
      onClose();
    },
  });

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    setSubmitted(true);
    if (!valid || create.isPending) return;
    create.mutate();
  };

  return (
    <Modal open onClose={onClose} title="Record top-up" subtitle={account.name}>
      <form onSubmit={onSubmit} noValidate>
        <div className="space-y-4 px-5 py-4">
          <Field label="Amount (USD)" required error={submitted ? amountError : undefined}>
            <Input
              type="number"
              inputMode="decimal"
              min="0.01"
              step="0.01"
              autoFocus
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              placeholder="50.00"
            />
          </Field>
          <Field label="Date" hint="When the balance arrived at the provider." error={dateError ?? undefined}>
            <Input type="date" min={openingDate || undefined} max={todayISO()} value={date} onChange={(e) => setDate(e.target.value)} />
          </Field>
          <Field label="Note" optional error={noteError ?? undefined}>
            <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder="For example: paid by credit card" maxLength={500} />
          </Field>
          {create.isError && <ErrorBanner message={`Couldn't record the top-up. ${errorText(create.error)}`} />}
        </div>
        <ModalFooter>
          <Button type="button" variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" disabled={create.isPending}>
            {create.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Plus aria-hidden="true" />}
            Save top-up
          </Button>
        </ModalFooter>
      </form>
    </Modal>
  );
}
