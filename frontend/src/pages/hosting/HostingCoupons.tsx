import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Loader2, Pencil, Plus, Ticket, Trash2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { api, type HostingCoupon, type HostingCouponDiscountType, type HostingCouponInput, type HostingCouponKind } from "../../lib/api";
import { useToast } from "../../components/Toast";
import { useConfirm } from "../../components/ui/confirm-dialog";
import {
  Badge,
  Button,
  EmptyState,
  ErrorBanner,
  Field,
  Input,
  LoadingRegion,
  Modal,
  ModalFooter,
  Select,
  Skeleton,
  Textarea,
  Toggle,
} from "../../components/ui";
import { Panel, REFETCH_MS, TH, THEAD, dateID, errorText, idr } from "../payments/shared";
import { QK_HOSTING } from "./shared";

const STATE_LABEL: Record<HostingCoupon["state"], string> = {
  active: "Active",
  inactive: "Disabled",
  scheduled: "Scheduled",
  expired: "Expired",
  exhausted: "Used up",
};

const STATE_TONE: Record<HostingCoupon["state"], "success" | "warning" | "danger" | "neutral" | "accent"> = {
  active: "success",
  inactive: "neutral",
  scheduled: "accent",
  expired: "neutral",
  exhausted: "warning",
};

function benefit(c: Pick<HostingCoupon, "kind" | "discount_type" | "discount_value" | "bonus_days">): string {
  if (c.kind === "free") return "Free period, no payment";
  if (c.kind === "bonus_days") return `+${c.bonus_days} days of service`;
  return c.discount_type === "percent" ? `${c.discount_value}% off` : `${idr(c.discount_value)} off`;
}

/** RFC3339 to the value of a datetime-local input, in the browser's zone. */
function toLocalInput(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** A datetime-local value to RFC3339, or "" when empty. */
function fromLocalInput(v: string): string {
  if (!v) return "";
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? "" : d.toISOString();
}

export function CouponsTab() {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const [editing, setEditing] = useState<HostingCoupon | "new" | null>(null);
  const coupons = useQuery({ queryKey: QK_HOSTING.coupons, queryFn: api.listHostingCoupons, refetchInterval: REFETCH_MS });

  const remove = useMutation({
    mutationFn: (id: string) => api.deleteHostingCoupon(id),
    onSuccess: () => {
      toast.success("Coupon deleted");
      qc.invalidateQueries({ queryKey: QK_HOSTING.coupons });
    },
    onError: (e) => toast.error("Couldn't delete coupon", errorText(e)),
  });

  const onDelete = async (c: HostingCoupon) => {
    const ok = await confirm({
      title: `Delete coupon ${c.code}?`,
      description: "Only unused coupons can be deleted. A coupon that has been used can only be disabled, so order history stays intact.",
      confirmLabel: "Delete coupon",
      cancelLabel: "Cancel",
      tone: "danger",
    });
    if (ok) remove.mutate(c.id);
  };

  const list = coupons.data?.coupons ?? [];

  return (
    <Panel
      icon={Ticket}
      title="Hosting coupons"
      subtitle="Price discounts or extra days of service. A use counts against the quota once payment is complete."
      action={
        <Button onClick={() => setEditing("new")}>
          <Plus aria-hidden="true" />
          Add coupon
        </Button>
      }
    >
      {coupons.isLoading ? (
        <LoadingRegion label="Loading coupons" className="p-4">
          <Skeleton className="h-40 w-full rounded-xl" />
        </LoadingRegion>
      ) : coupons.isError ? (
        <div className="p-4">
          <ErrorBanner message={`Couldn't load coupons. ${errorText(coupons.error)}`} />
        </div>
      ) : list.length === 0 ? (
        <EmptyState icon={Ticket} size="sm" title="No coupons yet" hint="Create a discount or extra-days coupon for customers to use at checkout." />
      ) : (
        <div className="overflow-x-auto" role="region" aria-label="Hosting coupons table" tabIndex={0}>
          <table className="w-full min-w-[860px] text-[13px]">
            <thead>
              <tr className={THEAD}>
                <th scope="col" className={TH}>Code</th>
                <th scope="col" className={TH}>Benefit</th>
                <th scope="col" className={TH}>Status</th>
                <th scope="col" className={cn(TH, "text-right")}>Uses</th>
                <th scope="col" className={TH}>Valid</th>
                <th scope="col" className={cn(TH, "text-right")}>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {list.map((c) => (
                <tr key={c.id} className="align-top transition-colors hover:bg-hover">
                  <td className="px-4 py-2.5">
                    <p className="font-mono font-medium text-fg">{c.code}</p>
                    {c.note && <p className="max-w-[220px] break-words text-[12px] text-fg-muted">{c.note}</p>}
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5">{benefit(c)}</td>
                  <td className="px-4 py-2.5">
                    <Badge tone={STATE_TONE[c.state]}>{STATE_LABEL[c.state]}</Badge>
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums">
                    {c.used_count}/{c.max_uses > 0 ? c.max_uses : "∞"} used
                    {c.held_count > 0 && <p className="text-[11.5px] text-fg-muted">{c.held_count} awaiting payment</p>}
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 tabular-nums text-fg-muted">
                    {c.starts_at || c.expires_at ? (
                      <>
                        {c.starts_at ? dateID(c.starts_at, true) : "Now"} – {c.expires_at ? dateID(c.expires_at, true) : "no end date"}
                      </>
                    ) : (
                      "No limit"
                    )}
                  </td>
                  <td className="px-4 py-2.5">
                    <div className="flex justify-end gap-1">
                      <Button variant="ghost" onClick={() => setEditing(c)} title="Edit coupon" aria-label={`Edit ${c.code}`}>
                        <Pencil aria-hidden="true" />
                      </Button>
                      <Button
                        variant="ghost"
                        onClick={() => onDelete(c)}
                        disabled={remove.isPending && remove.variables === c.id}
                        title="Delete coupon"
                        aria-label={`Delete ${c.code}`}
                      >
                        {remove.isPending && remove.variables === c.id ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Trash2 aria-hidden="true" />}
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {editing && <CouponModal coupon={editing === "new" ? null : editing} onClose={() => setEditing(null)} />}
    </Panel>
  );
}

function CouponModal({ coupon, onClose }: { coupon: HostingCoupon | null; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [code, setCode] = useState(coupon?.code ?? "");
  const [kind, setKind] = useState<HostingCouponKind>(coupon?.kind ?? "discount");
  const [discountType, setDiscountType] = useState<HostingCouponDiscountType>(coupon?.discount_type || "percent");
  const [discountValue, setDiscountValue] = useState(coupon && coupon.kind === "discount" ? String(coupon.discount_value) : "");
  const [bonusDays, setBonusDays] = useState(coupon && coupon.kind === "bonus_days" ? String(coupon.bonus_days) : "");
  const [maxUses, setMaxUses] = useState(coupon ? String(coupon.max_uses) : "0");
  const [startsAt, setStartsAt] = useState(toLocalInput(coupon?.starts_at ?? ""));
  const [expiresAt, setExpiresAt] = useState(toLocalInput(coupon?.expires_at ?? ""));
  const [active, setActive] = useState(coupon?.active ?? true);
  const [note, setNote] = useState(coupon?.note ?? "");
  const [touched, setTouched] = useState(false);

  const valueNum = Number(discountValue);
  const daysNum = Number(bonusDays);
  const usesNum = Number(maxUses);
  const codeError = touched && !/^[A-Z0-9][A-Z0-9_-]{2,31}$/.test(code.trim()) ? "3 to 32 characters: letters, numbers, - or _." : null;
  const valueError =
    touched && kind === "discount"
      ? !Number.isInteger(valueNum) || valueNum < 1 || (discountType === "percent" && valueNum > 100)
        ? discountType === "percent"
          ? "Enter a percentage from 1 to 100."
          : "Enter a whole number above 0."
        : null
      : null;
  const daysError = touched && kind === "bonus_days" && (!Number.isInteger(daysNum) || daysNum < 1 || daysNum > 365) ? "Enter 1 to 365 days." : null;
  const usesError = touched && (maxUses.trim() === "" || !Number.isInteger(usesNum) || usesNum < 0) ? "Enter 0 (no limit) or more." : null;
  const rangeError = startsAt && expiresAt && expiresAt <= startsAt ? "End time must be after the start time." : null;

  const save = useMutation({
    mutationFn: (body: HostingCouponInput) => (coupon ? api.updateHostingCoupon(coupon.id, body) : api.createHostingCoupon(body)),
    onSuccess: () => {
      toast.success(coupon ? "Coupon updated" : "Coupon created");
      qc.invalidateQueries({ queryKey: QK_HOSTING.coupons });
      onClose();
    },
  });

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    const c = code.trim();
    const ok =
      /^[A-Z0-9][A-Z0-9_-]{2,31}$/.test(c) &&
      !rangeError &&
      Number.isInteger(usesNum) &&
      usesNum >= 0 &&
      maxUses.trim() !== "" &&
      (kind === "free" ||
        (kind === "discount"
          ? Number.isInteger(valueNum) && valueNum >= 1 && (discountType !== "percent" || valueNum <= 100)
          : Number.isInteger(daysNum) && daysNum >= 1 && daysNum <= 365));
    if (!ok || save.isPending) return;
    save.mutate({
      code: c,
      kind,
      ...(kind === "discount"
        ? { discount_type: discountType, discount_value: valueNum }
        : kind === "bonus_days"
          ? { bonus_days: daysNum }
          : {}),
      max_uses: usesNum,
      starts_at: fromLocalInput(startsAt),
      expires_at: fromLocalInput(expiresAt),
      active,
      note: note.trim(),
    });
  };

  const inUse = !!coupon && coupon.used_count + coupon.held_count > 0;

  return (
    <Modal open onClose={save.isPending ? () => {} : onClose} title={coupon ? `Edit coupon ${coupon.code}` : "Add coupon"} subtitle="9router hosting coupon">
      <form onSubmit={onSubmit} noValidate>
        <div className="space-y-4 px-5 py-4">
          <Field label="Coupon code" error={codeError} hint={codeError ? undefined : "Customers type this code. Not case-sensitive."} required>
            <Input
              value={code}
              onChange={(e) => setCode(e.target.value.toUpperCase().replace(/\s+/g, ""))}
              placeholder="HEMAT20"
              maxLength={32}
              autoComplete="off"
              spellCheck={false}
              className="font-mono uppercase"
              autoFocus
            />
          </Field>
          <Field label="Coupon type" hint={inUse ? "Changing the benefit doesn't affect existing orders." : undefined}>
            <Select value={kind} onChange={(e) => setKind(e.target.value as HostingCouponKind)}>
              <option value="discount">Price discount</option>
              <option value="bonus_days">Extra days</option>
              <option value="free">Free period (no payment)</option>
            </Select>
          </Field>
          {kind === "discount" ? (
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field label="Discount type">
                <Select value={discountType} onChange={(e) => setDiscountType(e.target.value as HostingCouponDiscountType)}>
                  <option value="percent">Percentage (%)</option>
                  <option value="fixed">Fixed amount (IDR)</option>
                </Select>
              </Field>
              <Field
                label={discountType === "percent" ? "Discount (%)" : "Amount off (IDR)"}
                error={valueError}
                hint={!valueError && discountType === "fixed" && valueNum > 0 ? idr(valueNum) : "The final price never goes below the payment minimum."}
                required
              >
                <Input
                  type="number"
                  inputMode="numeric"
                  min="1"
                  max={discountType === "percent" ? "100" : undefined}
                  step="1"
                  value={discountValue}
                  onChange={(e) => setDiscountValue(e.target.value)}
                  placeholder={discountType === "percent" ? "20" : "10000"}
                  className="tabular-nums"
                />
              </Field>
            </div>
          ) : kind === "free" ? (
            <p className="rounded-lg border border-line bg-subtle px-3 py-2 text-[13px] text-fg-muted">
              The customer pays nothing for one 30-day period. The order skips the payment gateway and the instance goes straight to awaiting activation.
            </p>
          ) : (
            <Field label="Extra days" error={daysError} hint={daysError ? undefined : "Added to the 30 days each payment covers."} required>
              <Input type="number" inputMode="numeric" min="1" max="365" step="1" value={bonusDays} onChange={(e) => setBonusDays(e.target.value)} placeholder="7" className="tabular-nums" />
            </Field>
          )}
          <Field
            label="Usage limit"
            error={usesError}
            hint={usesError ? undefined : "0 = no limit. Each customer can use this coupon once."}
            required
          >
            <Input type="number" inputMode="numeric" min="0" step="1" value={maxUses} onChange={(e) => setMaxUses(e.target.value)} className="tabular-nums" />
          </Field>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Starts" optional hint="Leave empty to start now.">
              <Input type="datetime-local" value={startsAt} onChange={(e) => setStartsAt(e.target.value)} />
            </Field>
            <Field label="Ends" optional error={rangeError} hint={rangeError ? undefined : "Leave empty for no end date."}>
              <Input type="datetime-local" value={expiresAt} min={startsAt || undefined} onChange={(e) => setExpiresAt(e.target.value)} />
            </Field>
          </div>
          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              <p id="coupon-active" className="text-[13px] font-medium text-fg">
                Coupon active
              </p>
              <p className="text-[12px] text-fg-muted">When disabled, checkout rejects the coupon. Invoices already issued still apply.</p>
            </div>
            <Toggle checked={active} onChange={setActive} aria-labelledby="coupon-active" />
          </div>
          <Field label="Note" optional>
            <Textarea value={note} onChange={(e) => setNote(e.target.value)} rows={2} maxLength={500} placeholder="e.g. launch promo" />
          </Field>
          {save.isError && <ErrorBanner message={`Couldn't save coupon. ${errorText(save.error)}`} />}
        </div>
        <ModalFooter>
          <Button type="button" variant="ghost" onClick={onClose} disabled={save.isPending}>
            Cancel
          </Button>
          <Button type="submit" disabled={save.isPending}>
            {save.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Check aria-hidden="true" />}
            {coupon ? "Save" : "Add"}
          </Button>
        </ModalFooter>
      </form>
    </Modal>
  );
}
