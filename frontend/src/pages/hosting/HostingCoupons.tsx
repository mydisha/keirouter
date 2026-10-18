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
  active: "Aktif",
  inactive: "Nonaktif",
  scheduled: "Terjadwal",
  expired: "Kedaluwarsa",
  exhausted: "Kuota habis",
};

const STATE_TONE: Record<HostingCoupon["state"], "success" | "warning" | "danger" | "neutral" | "accent"> = {
  active: "success",
  inactive: "neutral",
  scheduled: "accent",
  expired: "neutral",
  exhausted: "warning",
};

function benefit(c: Pick<HostingCoupon, "kind" | "discount_type" | "discount_value" | "bonus_days">): string {
  if (c.kind === "bonus_days") return `+${c.bonus_days} hari masa aktif`;
  return c.discount_type === "percent" ? `Diskon ${c.discount_value}%` : `Potongan ${idr(c.discount_value)}`;
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
      toast.success("Kupon dihapus");
      qc.invalidateQueries({ queryKey: QK_HOSTING.coupons });
    },
    onError: (e) => toast.error("Gagal menghapus kupon", errorText(e)),
  });

  const onDelete = async (c: HostingCoupon) => {
    const ok = await confirm({
      title: `Hapus kupon ${c.code}?`,
      description: "Kupon yang belum pernah dipakai bisa dihapus. Kupon yang sudah dipakai hanya bisa dinonaktifkan agar riwayat order tetap utuh.",
      confirmLabel: "Hapus kupon",
      cancelLabel: "Batal",
      tone: "danger",
    });
    if (ok) remove.mutate(c.id);
  };

  const list = coupons.data?.coupons ?? [];

  return (
    <Panel
      icon={Ticket}
      title="Kupon hosting"
      subtitle="Kupon diskon harga atau bonus masa aktif. Kuota dihitung terpakai saat pembayaran lunas."
      action={
        <Button onClick={() => setEditing("new")}>
          <Plus aria-hidden="true" />
          Tambah kupon
        </Button>
      }
    >
      {coupons.isLoading ? (
        <LoadingRegion label="Memuat kupon" className="p-4">
          <Skeleton className="h-40 w-full rounded-xl" />
        </LoadingRegion>
      ) : coupons.isError ? (
        <div className="p-4">
          <ErrorBanner message={`Kupon gagal dimuat. ${errorText(coupons.error)}`} />
        </div>
      ) : list.length === 0 ? (
        <EmptyState icon={Ticket} size="sm" title="Belum ada kupon" hint="Buat kupon diskon atau bonus masa aktif untuk dipakai pelanggan saat checkout." />
      ) : (
        <div className="overflow-x-auto" role="region" aria-label="Tabel kupon hosting" tabIndex={0}>
          <table className="w-full min-w-[860px] text-[13px]">
            <thead>
              <tr className={THEAD}>
                <th scope="col" className={TH}>Kode</th>
                <th scope="col" className={TH}>Manfaat</th>
                <th scope="col" className={TH}>Status</th>
                <th scope="col" className={cn(TH, "text-right")}>Pemakaian</th>
                <th scope="col" className={TH}>Berlaku</th>
                <th scope="col" className={cn(TH, "text-right")}>
                  <span className="sr-only">Aksi</span>
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
                    {c.used_count}/{c.max_uses > 0 ? c.max_uses : "∞"} terpakai
                    {c.held_count > 0 && <p className="text-[11.5px] text-fg-muted">{c.held_count} menunggu bayar</p>}
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 tabular-nums text-fg-muted">
                    {c.starts_at || c.expires_at ? (
                      <>
                        {c.starts_at ? dateID(c.starts_at, true) : "Sekarang"} – {c.expires_at ? dateID(c.expires_at, true) : "tanpa batas"}
                      </>
                    ) : (
                      "Tanpa batas"
                    )}
                  </td>
                  <td className="px-4 py-2.5">
                    <div className="flex justify-end gap-1">
                      <Button variant="ghost" onClick={() => setEditing(c)} title="Ubah kupon" aria-label={`Ubah ${c.code}`}>
                        <Pencil aria-hidden="true" />
                      </Button>
                      <Button
                        variant="ghost"
                        onClick={() => onDelete(c)}
                        disabled={remove.isPending && remove.variables === c.id}
                        title="Hapus kupon"
                        aria-label={`Hapus ${c.code}`}
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
  const codeError = touched && !/^[A-Z0-9][A-Z0-9_-]{2,31}$/.test(code.trim()) ? "3-32 karakter: huruf, angka, - atau _." : null;
  const valueError =
    touched && kind === "discount"
      ? !Number.isInteger(valueNum) || valueNum < 1 || (discountType === "percent" && valueNum > 100)
        ? discountType === "percent"
          ? "Isi persen 1 sampai 100."
          : "Isi angka bulat lebih dari 0."
        : null
      : null;
  const daysError = touched && kind === "bonus_days" && (!Number.isInteger(daysNum) || daysNum < 1 || daysNum > 365) ? "Isi 1 sampai 365 hari." : null;
  const usesError = touched && (maxUses.trim() === "" || !Number.isInteger(usesNum) || usesNum < 0) ? "Isi 0 (tanpa batas) atau lebih." : null;
  const rangeError = startsAt && expiresAt && expiresAt <= startsAt ? "Waktu berakhir harus setelah waktu mulai." : null;

  const save = useMutation({
    mutationFn: (body: HostingCouponInput) => (coupon ? api.updateHostingCoupon(coupon.id, body) : api.createHostingCoupon(body)),
    onSuccess: () => {
      toast.success(coupon ? "Kupon diperbarui" : "Kupon dibuat");
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
      (kind === "discount"
        ? Number.isInteger(valueNum) && valueNum >= 1 && (discountType !== "percent" || valueNum <= 100)
        : Number.isInteger(daysNum) && daysNum >= 1 && daysNum <= 365);
    if (!ok || save.isPending) return;
    save.mutate({
      code: c,
      kind,
      ...(kind === "discount" ? { discount_type: discountType, discount_value: valueNum } : { bonus_days: daysNum }),
      max_uses: usesNum,
      starts_at: fromLocalInput(startsAt),
      expires_at: fromLocalInput(expiresAt),
      active,
      note: note.trim(),
    });
  };

  const inUse = !!coupon && coupon.used_count + coupon.held_count > 0;

  return (
    <Modal open onClose={save.isPending ? () => {} : onClose} title={coupon ? `Ubah kupon ${coupon.code}` : "Tambah kupon"} subtitle="Kupon hosting 9router">
      <form onSubmit={onSubmit} noValidate>
        <div className="space-y-4 px-5 py-4">
          <Field label="Kode kupon" error={codeError} hint={codeError ? undefined : "Pelanggan mengetik kode ini. Huruf besar dan kecil dianggap sama."} required>
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
          <Field label="Jenis kupon" hint={inUse ? "Mengubah manfaat tidak memengaruhi order yang sudah dibuat." : undefined}>
            <Select value={kind} onChange={(e) => setKind(e.target.value as HostingCouponKind)}>
              <option value="discount">Diskon harga</option>
              <option value="bonus_days">Bonus masa aktif</option>
            </Select>
          </Field>
          {kind === "discount" ? (
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field label="Tipe diskon">
                <Select value={discountType} onChange={(e) => setDiscountType(e.target.value as HostingCouponDiscountType)}>
                  <option value="percent">Persen (%)</option>
                  <option value="fixed">Nominal (IDR)</option>
                </Select>
              </Field>
              <Field
                label={discountType === "percent" ? "Diskon (%)" : "Potongan (IDR)"}
                error={valueError}
                hint={!valueError && discountType === "fixed" && valueNum > 0 ? idr(valueNum) : "Harga akhir tidak pernah di bawah minimum pembayaran."}
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
          ) : (
            <Field label="Bonus masa aktif (hari)" error={daysError} hint={daysError ? undefined : "Ditambahkan ke masa aktif 30 hari dari pembayaran."} required>
              <Input type="number" inputMode="numeric" min="1" max="365" step="1" value={bonusDays} onChange={(e) => setBonusDays(e.target.value)} placeholder="7" className="tabular-nums" />
            </Field>
          )}
          <Field
            label="Kuota pemakaian"
            error={usesError}
            hint={usesError ? undefined : "0 = tanpa batas. Satu pelanggan hanya bisa memakai kupon ini sekali."}
            required
          >
            <Input type="number" inputMode="numeric" min="0" step="1" value={maxUses} onChange={(e) => setMaxUses(e.target.value)} className="tabular-nums" />
          </Field>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Mulai berlaku" optional hint="Kosongkan untuk langsung berlaku.">
              <Input type="datetime-local" value={startsAt} onChange={(e) => setStartsAt(e.target.value)} />
            </Field>
            <Field label="Berakhir" optional error={rangeError} hint={rangeError ? undefined : "Kosongkan untuk tanpa batas waktu."}>
              <Input type="datetime-local" value={expiresAt} min={startsAt || undefined} onChange={(e) => setExpiresAt(e.target.value)} />
            </Field>
          </div>
          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              <p id="coupon-active" className="text-[13px] font-medium text-fg">
                Kupon aktif
              </p>
              <p className="text-[12px] text-fg-muted">Saat nonaktif, kupon ditolak di checkout. Tagihan yang sudah dibuat tetap berlaku.</p>
            </div>
            <Toggle checked={active} onChange={setActive} aria-labelledby="coupon-active" />
          </div>
          <Field label="Catatan" optional>
            <Textarea value={note} onChange={(e) => setNote(e.target.value)} rows={2} maxLength={500} placeholder="Misal: promo peluncuran" />
          </Field>
          {save.isError && <ErrorBanner message={`Kupon gagal disimpan. ${errorText(save.error)}`} />}
        </div>
        <ModalFooter>
          <Button type="button" variant="ghost" onClick={onClose} disabled={save.isPending}>
            Batal
          </Button>
          <Button type="submit" disabled={save.isPending}>
            {save.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Check aria-hidden="true" />}
            {coupon ? "Simpan" : "Tambah"}
          </Button>
        </ModalFooter>
      </form>
    </Modal>
  );
}
