import { useMemo, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  Check,
  ExternalLink,
  ListChecks,
  Loader2,
  Play,
  PlugZap,
  Plus,
  ReceiptText,
  Rocket,
  RotateCcw,
  Server,
  Settings2,
  Square,
  Ticket,
  Trash2,
  Wallet,
} from "lucide-react";
import { cn } from "@/lib/utils";
import {
  api,
  type HostingInstanceAdmin,
  type HostingMonitorTarget,
  type HostingNode,
  type HostingNodeLoad,
  type HostingSettings,
  type HostingStatus,
} from "../lib/api";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { useConfirm } from "../components/ui/confirm-dialog";
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
  TabBar,
  TabPanel,
  TablePagination,
  Textarea,
  Toggle,
  useClientPagination,
} from "../components/ui";
import { Panel, REFETCH_MS, TH, THEAD, dateID, errorText, idr } from "./payments/shared";
import { FinanceTab } from "./hosting/HostingFinance";
import { MonitoringTab } from "./hosting/HostingMonitoring";
import { CouponsTab } from "./hosting/HostingCoupons";
import {
  QK_HOSTING,
  STATUS_LABEL,
  STATUS_TONE,
  newNodeId,
  nodeBlockedLabel,
  nodeName,
  nodeUsage,
  normalizeDomain,
  validDomain,
} from "./hosting/shared";

type Tab = "instances" | "coupons" | "finance" | "monitoring" | "settings";
const TABS_ID = "hosting-tabs";

const ORDER_STATUS_LABEL: Record<string, string> = {
  completed: "Lunas",
  manual: "Disetujui manual",
  pending: "Menunggu",
  failed: "Gagal",
  expired: "Kedaluwarsa",
};

const ORDER_STATUS_TONE: Record<string, "success" | "warning" | "danger" | "neutral"> = {
  completed: "success",
  manual: "success",
  pending: "warning",
  failed: "danger",
  expired: "neutral",
};

export function HostingPage() {
  const [tab, setTab] = useState<Tab>("instances");
  const settings = useQuery({ queryKey: QK_HOSTING.settings, queryFn: api.getHostingSettings });

  return (
    <div className="space-y-6">
      <PageHeader
        title="Hosting"
        description="Instance 9router yang dipesan user portal. Buat instance di Dokploy setelah pembayaran lunas."
      />
      <TabBar
        idPrefix={TABS_ID}
        label="Bagian hosting"
        active={tab}
        onChange={setTab}
        tabs={[
          { value: "instances", label: "Instance", icon: Server },
          { value: "coupons", label: "Kupon", icon: Ticket },
          { value: "finance", label: "Keuangan", icon: Wallet },
          { value: "monitoring", label: "Monitoring", icon: Activity },
          { value: "settings", label: "Pengaturan", icon: Settings2 },
        ]}
      />
      <TabPanel idPrefix={TABS_ID} value={tab}>
        {tab === "instances" ? (
          <InstancesTab settings={settings.data} onOpenSettings={() => setTab("settings")} />
        ) : tab === "coupons" ? (
          <CouponsTab />
        ) : tab === "finance" ? (
          <FinanceTab />
        ) : tab === "monitoring" ? (
          <MonitoringTab />
        ) : (
          <SettingsTab query={settings} />
        )}
      </TabPanel>
    </div>
  );
}

// ─── Instances ──────────────────────────────────────────────────────────────

/** Node filter value for instances that have no node yet. */
const NO_NODE = "__none";

function InstancesTab({ settings, onOpenSettings }: { settings?: HostingSettings; onOpenSettings: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const [status, setStatus] = useState("");
  const [nodeFilter, setNodeFilter] = useState("");
  const [provisioning, setProvisioning] = useState<HostingInstanceAdmin | null>(null);
  const [ordersOf, setOrdersOf] = useState<HostingInstanceAdmin | null>(null);

  const list = useQuery({
    queryKey: [...QK_HOSTING.instances, status],
    queryFn: () => api.listHostingInstances(status || undefined),
    refetchInterval: REFETCH_MS,
  });
  const nodesQ = useQuery({ queryKey: QK_HOSTING.nodes, queryFn: api.listHostingNodes, refetchInterval: REFETCH_MS });
  const nodeList: { id: string; name: string }[] = nodesQ.data?.nodes ?? settings?.nodes ?? [];
  const all = list.data?.instances;
  const instances = useMemo(() => {
    const rows = all ?? [];
    if (!nodeFilter) return rows;
    return rows.filter((h) => (nodeFilter === NO_NODE ? !h.node_id : h.node_id === nodeFilter));
  }, [all, nodeFilter]);
  const { paged, page, pages, setPage, total } = useClientPagination(instances, 10);

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: QK_HOSTING.instances });
    qc.invalidateQueries({ queryKey: QK_HOSTING.nodes });
  };

  const action = useMutation({
    mutationFn: ({ kind, id }: { kind: "stop" | "start" | "delete"; id: string }) =>
      kind === "stop" ? api.stopHosting(id) : kind === "start" ? api.startHosting(id) : api.deleteHosting(id),
    onSuccess: (_d, { kind }) => {
      toast.success(kind === "stop" ? "Instance dihentikan" : kind === "start" ? "Instance dijalankan" : "Instance dihapus");
      invalidate();
    },
    onError: (e, { kind }) => {
      toast.error(kind === "stop" ? "Gagal menghentikan instance" : kind === "start" ? "Gagal menjalankan instance" : "Gagal menghapus instance", errorText(e));
      invalidate();
    },
  });

  const busy = (id: string) => action.isPending && action.variables?.id === id;

  const onDelete = async (inst: HostingInstanceAdmin) => {
    const ok = await confirm({
      title: "Hapus instance?",
      description: (
        <>
          Aplikasi <span className="font-mono">{inst.dokploy_app_name || inst.subdomain || inst.id}</span> di Dokploy ikut dihapus,
          termasuk datanya. User {inst.user_email || inst.key_id} kehilangan akses dan tindakan ini tidak bisa dibatalkan.
        </>
      ),
      confirmLabel: "Hapus instance",
      cancelLabel: "Batal",
      tone: "danger",
    });
    if (ok) action.mutate({ kind: "delete", id: inst.id });
  };

  return (
    <Panel
      icon={Server}
      title="Instance"
      subtitle="Status, alamat, dan masa aktif tiap instance. Diperbarui tiap 15 detik."
      action={
        <div className="flex flex-wrap gap-2">
          {nodeList.length > 1 && (
            <Select value={nodeFilter} onChange={(e) => setNodeFilter(e.target.value)} aria-label="Filter node" className="w-auto min-w-[150px]">
              <option value="">Semua node</option>
              {nodeList.map((n) => (
                <option key={n.id} value={n.id}>
                  {n.name || n.id}
                </option>
              ))}
              <option value={NO_NODE}>Belum ada node</option>
            </Select>
          )}
          <Select value={status} onChange={(e) => setStatus(e.target.value)} aria-label="Filter status" className="w-auto min-w-[180px]">
            <option value="">Semua status</option>
            {(Object.keys(STATUS_LABEL) as HostingStatus[]).map((s) => (
              <option key={s} value={s}>
                {STATUS_LABEL[s]}
              </option>
            ))}
          </Select>
        </div>
      }
    >
      {nodesQ.data && <NodeUsageStrip nodes={nodesQ.data.nodes} onOpenSettings={onOpenSettings} />}
      {list.isLoading ? (
        <LoadingRegion label="Memuat instance" className="space-y-2 p-4">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </LoadingRegion>
      ) : list.isError && !list.data ? (
        <div className="p-4">
          <ErrorBanner message={`Instance gagal dimuat. ${errorText(list.error)}`} />
        </div>
      ) : instances.length === 0 ? (
        <EmptyState
          icon={Server}
          size="sm"
          title={status || nodeFilter ? "Tidak ada instance yang cocok dengan filter" : "Belum ada instance"}
          hint="Pesanan hosting dari portal muncul di sini begitu user memulai pembayaran."
        />
      ) : (
        <>
          <div className="overflow-x-auto" role="region" aria-label="Tabel instance hosting" tabIndex={0}>
            <table className="w-full min-w-[1140px] text-[13px]">
              <thead>
                <tr className={THEAD}>
                  <th scope="col" className={TH}>User</th>
                  <th scope="col" className={TH}>Status</th>
                  <th scope="col" className={TH}>Node</th>
                  <th scope="col" className={TH}>Alamat</th>
                  <th scope="col" className={TH}>Image</th>
                  <th scope="col" className={TH}>Aktif sampai</th>
                  <th scope="col" className={TH}>Dibuat</th>
                  <th scope="col" className={cn(TH, "text-right")}>
                    <span className="sr-only">Aksi</span>
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {paged.map((h) => (
                  <tr key={h.id} className="align-top transition-colors hover:bg-hover">
                    <td className="px-4 py-2.5">
                      <p className="text-fg">{h.user_email || "-"}</p>
                      <p className="font-mono text-[11px] text-fg-muted">{h.key_id}</p>
                    </td>
                    <td className="px-4 py-2.5">
                      <Badge tone={STATUS_TONE[h.status] ?? "neutral"}>{STATUS_LABEL[h.status] ?? h.status}</Badge>
                      {h.status === "failed" && h.last_error && (
                        <p className="mt-1 max-w-[260px] break-words text-[11.5px] text-bad">{h.last_error}</p>
                      )}
                    </td>
                    <td className="whitespace-nowrap px-4 py-2.5">{h.node_id ? nodeName(nodeList, h.node_id) : <span className="text-fg-muted">-</span>}</td>
                    <td className="px-4 py-2.5">
                      {h.url ? (
                        <a
                          href={h.url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="inline-flex items-center gap-1 break-all font-mono text-[12px] text-link hover:underline"
                        >
                          {h.fqdn || h.url}
                          <ExternalLink className="h-3 w-3 shrink-0" aria-hidden="true" />
                        </a>
                      ) : h.fqdn || h.subdomain ? (
                        <span className="font-mono text-[12px] text-fg-muted">{h.fqdn || h.subdomain}</span>
                      ) : (
                        "-"
                      )}
                    </td>
                    <td className="max-w-[220px] break-all px-4 py-2.5 font-mono text-[12px] text-fg-muted">{h.image || "-"}</td>
                    <td className="whitespace-nowrap px-4 py-2.5 tabular-nums">
                      {h.paid_until ? dateID(h.paid_until) : "-"}
                      {h.prepaid_days > 0 && <p className="text-[11.5px] text-fg-muted">+{h.prepaid_days} hari prabayar</p>}
                    </td>
                    <td className="whitespace-nowrap px-4 py-2.5 tabular-nums text-fg-muted">{dateID(h.created_at, true)}</td>
                    <td className="px-4 py-2.5">
                      <div className="flex flex-wrap justify-end gap-1">
                        {(h.status === "paid" || h.status === "failed") && (
                          <Button variant={h.status === "paid" ? "primary" : "secondary"} onClick={() => setProvisioning(h)}>
                            {h.status === "paid" ? <Rocket aria-hidden="true" /> : <RotateCcw aria-hidden="true" />}
                            {h.status === "paid" ? "Buat" : "Coba lagi"}
                          </Button>
                        )}
                        {(h.status === "active" || h.status === "expired") && (
                          <Button variant="ghost" disabled={busy(h.id)} onClick={() => action.mutate({ kind: "stop", id: h.id })}>
                            {busy(h.id) && action.variables?.kind === "stop" ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Square aria-hidden="true" />}
                            Stop
                          </Button>
                        )}
                        {h.status === "stopped" && (
                          <Button variant="ghost" disabled={busy(h.id)} onClick={() => action.mutate({ kind: "start", id: h.id })}>
                            {busy(h.id) && action.variables?.kind === "start" ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Play aria-hidden="true" />}
                            Start
                          </Button>
                        )}
                        <Button variant="ghost" onClick={() => setOrdersOf(h)} title="Lihat order">
                          <ReceiptText aria-hidden="true" />
                          Order
                          {(h.orders?.length ?? 0) > 0 && <span className="tabular-nums text-fg-muted">{h.orders!.length}</span>}
                        </Button>
                        {h.status !== "deleted" && (
                          <Button variant="ghost" disabled={busy(h.id)} onClick={() => onDelete(h)} title="Hapus instance" aria-label="Hapus instance">
                            {busy(h.id) && action.variables?.kind === "delete" ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Trash2 aria-hidden="true" />}
                          </Button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <TablePagination page={page} pages={pages} total={total} onPage={setPage} label="Halaman instance hosting" />
        </>
      )}

      {provisioning && (
        <ProvisionModal
          inst={provisioning}
          images={settings?.images ?? []}
          nodes={nodesQ.data}
          nodesLoading={nodesQ.isLoading}
          nodesError={nodesQ.isError && !nodesQ.data ? nodesQ.error : null}
          onClose={() => setProvisioning(null)}
          onDone={invalidate}
          onOpenSettings={() => {
            setProvisioning(null);
            onOpenSettings();
          }}
        />
      )}
      {ordersOf && <OrdersModal inst={ordersOf} onClose={() => setOrdersOf(null)} />}
    </Panel>
  );
}

/** Compact per-node usage so the admin sees when a node is filling up. */
function NodeUsageStrip({ nodes, onOpenSettings }: { nodes: HostingNodeLoad[]; onOpenSettings: () => void }) {
  if (nodes.length === 0) {
    return (
      <div className="border-b border-line px-4 py-3">
        <ErrorBanner message="Belum ada node. Tambahkan node di tab Pengaturan sebelum membuat instance." />
        <Button variant="ghost" className="mt-2" onClick={onOpenSettings}>
          <Settings2 aria-hidden="true" />
          Buka Pengaturan
        </Button>
      </div>
    );
  }
  const noneAvailable = !nodes.some((n) => n.available);
  return (
    <div className="flex flex-wrap items-center gap-1.5 border-b border-line px-4 py-2.5" role="list" aria-label="Pemakaian node">
      <span className="mr-1 text-[12px] text-fg-muted">Node:</span>
      {nodes.map((n) => {
        const blocked = nodeBlockedLabel(n);
        return (
          <span key={n.id} role="listitem">
            <Badge
              tone={!n.enabled ? "neutral" : !n.available ? "danger" : n.capacity > 0 && n.free <= Math.max(1, Math.floor(n.capacity * 0.1)) ? "warning" : "neutral"}
              title={`${n.root_domain}${blocked ? ` · ${blocked}` : ""}`}
            >
              {n.name || n.id} · <span className="tabular-nums">{nodeUsage(n)}</span>
              {blocked && <span> · {blocked}</span>}
            </Badge>
          </span>
        );
      })}
      {noneAvailable && (
        <button type="button" onClick={onOpenSettings} className="ml-1 text-[12px] text-bad underline-offset-2 hover:underline">
          Semua node penuh atau nonaktif. Tambah node di Pengaturan.
        </button>
      )}
    </div>
  );
}

function ProvisionModal({
  inst,
  images,
  nodes,
  nodesLoading,
  nodesError,
  onClose,
  onDone,
  onOpenSettings,
}: {
  inst: HostingInstanceAdmin;
  images: string[];
  nodes?: { nodes: HostingNodeLoad[]; recommended: string };
  nodesLoading: boolean;
  nodesError: unknown;
  onClose: () => void;
  onDone: () => void;
  onOpenSettings: () => void;
}) {
  const toast = useToast();
  const [image, setImage] = useState(inst.image && images.includes(inst.image) ? inst.image : images[0] ?? "");
  // null follows the server's recommendation, which may arrive after the modal opens.
  const [picked, setPicked] = useState<string | null>(null);
  // A failed instance that already got a subdomain stays on its node.
  const lockedNode = inst.node_id && inst.subdomain ? inst.node_id : "";
  const nodeList = nodes?.nodes ?? [];
  const nodeId = lockedNode || (picked ?? nodes?.recommended ?? "");
  const anyAvailable = nodeList.some((n) => n.available);
  const nodeReady = lockedNode ? true : !!nodeId && anyAvailable;

  const provision = useMutation({
    mutationFn: () => api.provisionHosting(inst.id, image, lockedNode ? undefined : nodeId),
    onSuccess: (h) => {
      toast.success("Instance dibuat", h.fqdn ? `Aktif di ${h.fqdn}.` : undefined);
      onDone();
      onClose();
    },
    // A Dokploy failure marks the instance failed; refresh so the row shows it.
    onError: onDone,
  });
  const retry = inst.status === "failed";
  const locked = lockedNode ? nodeList.find((n) => n.id === lockedNode) : undefined;

  return (
    <Modal
      open
      onClose={provision.isPending ? () => {} : onClose}
      title={retry ? "Coba buat ulang instance" : "Buat instance"}
      subtitle={inst.user_email || inst.key_id}
    >
      <div className="space-y-3 px-5 py-4">
        <p className="text-[13px] text-fg-muted">
          Instance dibuat di Dokploy dengan subdomain acak pada domain node terpilih. Masa aktif mulai dihitung saat instance aktif.
        </p>
        {retry && inst.last_error && <ErrorBanner message={`Percobaan terakhir gagal: ${inst.last_error}`} />}
        {images.length === 0 ? (
          <ErrorBanner message="Belum ada image. Tambahkan image Docker di tab Pengaturan dulu." />
        ) : (
          <Field label="Image Docker">
            <Select value={image} onChange={(e) => setImage(e.target.value)} className="font-mono">
              {images.map((img) => (
                <option key={img} value={img}>
                  {img}
                </option>
              ))}
            </Select>
          </Field>
        )}
        {lockedNode ? (
          <Field label="Node" hint="Instance ini sudah disiapkan di node ini, jadi percobaan ulang tetap di sana. Hapus instance jika perlu pindah node.">
            <p className="rounded-lg border border-line bg-subtle px-3 py-2 text-[13px] text-fg">
              {locked?.name || lockedNode}
              {locked && (
                <span className="text-fg-muted">
                  {" "}
                  · {nodeUsage(locked)}
                  {nodeBlockedLabel(locked) && ` · ${nodeBlockedLabel(locked)}`}
                </span>
              )}
              <span className="ml-1 font-mono text-[11.5px] text-fg-muted">{inst.fqdn || inst.subdomain}</span>
            </p>
          </Field>
        ) : nodesLoading ? (
          <Skeleton className="h-[62px] w-full" />
        ) : nodesError ? (
          <ErrorBanner message={`Daftar node gagal dimuat. ${errorText(nodesError)}`} />
        ) : !anyAvailable ? (
          <div className="space-y-2">
            <ErrorBanner
              message={
                nodeList.length === 0
                  ? "Belum ada node. Tambahkan node di tab Pengaturan dulu."
                  : "Semua node penuh atau nonaktif. Tambah node baru, naikkan kapasitas, atau aktifkan node di tab Pengaturan."
              }
            />
            <Button variant="ghost" onClick={onOpenSettings}>
              <Settings2 aria-hidden="true" />
              Buka Pengaturan
            </Button>
          </div>
        ) : (
          <Field
            label="Node"
            hint={nodeId === nodes?.recommended ? "Dipilih otomatis: node dengan ruang paling lega." : "Node dipilih manual."}
          >
            <Select value={nodeId} onChange={(e) => setPicked(e.target.value)}>
              {nodeList.map((n) => {
                const blocked = nodeBlockedLabel(n);
                return (
                  <option key={n.id} value={n.id} disabled={!n.available}>
                    {`${n.name || n.id} — ${nodeUsage(n)}${blocked ? ` (${blocked})` : ""}`}
                  </option>
                );
              })}
            </Select>
          </Field>
        )}
        {provision.isError && <ErrorBanner message={`Instance gagal dibuat. ${errorText(provision.error)}`} />}
      </div>
      <ModalFooter>
        <Button variant="ghost" onClick={onClose} disabled={provision.isPending}>
          Batal
        </Button>
        <Button onClick={() => provision.mutate()} disabled={!image || !nodeReady || provision.isPending}>
          {provision.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Rocket aria-hidden="true" />}
          {provision.isPending ? "Membuat…" : retry ? "Coba lagi" : "Buat instance"}
        </Button>
      </ModalFooter>
    </Modal>
  );
}

function OrdersModal({ inst, onClose }: { inst: HostingInstanceAdmin; onClose: () => void }) {
  const orders = inst.orders ?? [];
  return (
    <Modal open onClose={onClose} title="Order hosting" subtitle={inst.user_email || inst.key_id} maxWidth="max-w-3xl">
      {orders.length === 0 ? (
        <EmptyState icon={ReceiptText} size="sm" title="Belum ada order" />
      ) : (
        <div className="overflow-x-auto" role="region" aria-label="Tabel order hosting" tabIndex={0}>
          <table className="w-full min-w-[760px] text-[13px]">
            <thead>
              <tr className={THEAD}>
                <th scope="col" className={TH}>Order</th>
                <th scope="col" className={TH}>Status</th>
                <th scope="col" className={cn(TH, "text-right")}>Jumlah</th>
                <th scope="col" className={TH}>Kupon</th>
                <th scope="col" className={TH}>Dibayar</th>
                <th scope="col" className={TH}>Ketentuan</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {orders.map((o) => (
                <tr key={o.order_id}>
                  <td className="px-4 py-2.5">
                    <p className="font-mono text-[11.5px] text-fg-muted">{o.order_id}</p>
                    <p className="text-[11.5px] tabular-nums text-fg-faint">{dateID(o.created_at, true)}</p>
                  </td>
                  <td className="px-4 py-2.5">
                    <Badge tone={ORDER_STATUS_TONE[o.status] ?? "neutral"}>{ORDER_STATUS_LABEL[o.status] ?? o.status}</Badge>
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums">
                    {idr(o.amount_idr)}
                    {!!o.base_amount_idr && o.base_amount_idr !== o.amount_idr && (
                      <p className="text-[11.5px] text-fg-faint line-through">{idr(o.base_amount_idr)}</p>
                    )}
                  </td>
                  <td className="px-4 py-2.5">
                    {o.coupon_code ? (
                      <>
                        <p className="font-mono text-[12px]">{o.coupon_code}</p>
                        {!!o.bonus_days && <p className="text-[11.5px] text-fg-muted">+{o.bonus_days} hari</p>}
                      </>
                    ) : (
                      "-"
                    )}
                    {o.reason?.startsWith("refund_required") && <Badge tone="danger">Perlu refund</Badge>}
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 tabular-nums">{o.paid_at ? dateID(o.paid_at, true) : "-"}</td>
                  <td className="px-4 py-2.5">
                    <p className="font-mono text-[12px]">{o.terms_version || "-"}</p>
                    <p className="text-[11.5px] tabular-nums text-fg-muted">
                      {o.terms_accepted_at ? `Disetujui ${dateID(o.terms_accepted_at, true)}` : "-"}
                    </p>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <ModalFooter>
        <Button variant="ghost" onClick={onClose}>
          Tutup
        </Button>
      </ModalFooter>
    </Modal>
  );
}

// ─── Settings ───────────────────────────────────────────────────────────────

function SettingsTab({ query }: { query: { data?: HostingSettings; isLoading: boolean; isError: boolean; error: unknown } }) {
  return (
    <div className="space-y-6">
      <Panel icon={Settings2} title="Pengaturan hosting" subtitle="Harga, koneksi Dokploy, dan template container 9router.">
        {query.isLoading ? (
          <LoadingRegion label="Memuat pengaturan hosting" className="space-y-3 p-4">
            <Skeleton className="h-[320px] w-full rounded-xl" />
          </LoadingRegion>
        ) : !query.data ? (
          <div className="p-4">
            <ErrorBanner message={`Pengaturan hosting gagal dimuat. ${query.isError ? errorText(query.error) : ""}`.trim()} />
          </div>
        ) : (
          // Re-key on the stored values so a save resets the form to them.
          <SettingsForm key={JSON.stringify(query.data)} settings={query.data} />
        )}
      </Panel>
      <SetupChecklist nodes={query.data?.nodes} />
    </div>
  );
}

function SettingsForm({ settings }: { settings: HostingSettings }) {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const [enabled, setEnabled] = useState(settings.enabled);
  const [price, setPrice] = useState(String(settings.monthly_price_idr || ""));
  const [dokployUrl, setDokployUrl] = useState(settings.dokploy_url);
  const [apiKey, setApiKey] = useState("");
  const [environmentId, setEnvironmentId] = useState(settings.environment_id);
  const [images, setImages] = useState(settings.images.join("\n"));
  const [port, setPort] = useState(String(settings.container_port || ""));
  const [envTemplate, setEnvTemplate] = useState(settings.env_template);
  const [nodes, setNodes] = useState<NodeDraft[]>(() => (settings.nodes ?? []).map(toNodeDraft));
  const [passwordEnv, setPasswordEnv] = useState(settings.password_env || "INITIAL_PASSWORD");
  const [memoryLimit, setMemoryLimit] = useState(settings.memory_limit ?? "");
  const [cpuLimit, setCpuLimit] = useState(settings.cpu_limit ?? "");
  const [dataPath, setDataPath] = useState(settings.data_path || "/data");
  const [supportUrl, setSupportUrl] = useState(settings.support_url || "https://t.me/damnwahhh");

  const priceNum = Number(price);
  const portNum = Number(port);
  const priceError = price.trim() === "" || !Number.isInteger(priceNum) || priceNum < 0 ? "Isi angka bulat 0 atau lebih." : null;
  const portError = port.trim() === "" || !Number.isInteger(portNum) || portNum < 1 || portNum > 65535 ? "Isi port 1 sampai 65535." : null;
  const imageList = images
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);
  const digits = /^[0-9]*$/;
  const memError = !digits.test(memoryLimit.trim()) ? "Isi angka bulat dalam byte, atau kosongkan." : null;
  const cpuError = !digits.test(cpuLimit.trim()) ? "Isi angka bulat dalam nano-CPU, atau kosongkan." : null;
  const dataPathError = !dataPath.trim().startsWith("/") || dataPath.includes("..") ? "Isi path absolut di dalam container, misal /data." : null;
  const envNameError = !/^[A-Za-z_][A-Za-z0-9_]*$/.test(passwordEnv.trim()) ? "Nama variabel tidak valid." : null;
  const supportUrlError = !/^https:\/\/[^\s/]+/.test(supportUrl.trim()) ? "Isi link https://, misal https://t.me/username." : null;
  const urlChanged = dokployUrl.trim().replace(/\/+$/, "") !== settings.dokploy_url;
  const keyNeeded = settings.has_api_key && urlChanged && dokployUrl.trim() !== "" && apiKey.trim() === "";
  const nodeErrors = nodeErrorsOf(nodes);
  const nodesError = enabled && !nodes.some((n) => n.enabled) ? "Tambahkan minimal satu node aktif sebelum membuka pemesanan." : null;
  const valid =
    !priceError && !portError && !memError && !cpuError && !envNameError && !dataPathError && !nodesError && !supportUrlError &&
    nodeErrors.every((e) => !e.name && !e.domain && !e.capacity);

  const save = useMutation({
    mutationFn: (opts: { clearKey?: boolean }) =>
      api.putHostingSettings({
        enabled,
        monthly_price_idr: priceNum,
        dokploy_url: dokployUrl.trim(),
        ...(apiKey.trim() ? { api_key: apiKey.trim() } : {}),
        ...(opts.clearKey ? { clear_api_key: true } : {}),
        environment_id: environmentId.trim(),
        images: imageList,
        container_port: portNum,
        env_template: envTemplate,
        nodes: nodes.map(
          (n): HostingNode => ({
            id: n.id,
            name: n.name.trim(),
            server_id: n.serverId.trim(),
            root_domain: normalizeDomain(n.rootDomain),
            monitor_target_id: n.monitorTargetId,
            capacity: Number(n.capacity.trim() || "0"),
            enabled: n.enabled,
          }),
        ),
        password_env: passwordEnv.trim(),
        memory_limit: memoryLimit.trim(),
        cpu_limit: cpuLimit.trim(),
        data_path: dataPath.trim(),
        support_url: supportUrl.trim(),
      }),
    onSuccess: (data) => {
      qc.setQueryData(QK_HOSTING.settings, data);
      qc.invalidateQueries({ queryKey: QK_HOSTING.finance });
      qc.invalidateQueries({ queryKey: QK_HOSTING.nodes });
      toast.success("Pengaturan disimpan", data.enabled ? "Pemesanan hosting dibuka di portal." : "Pemesanan hosting ditutup di portal.");
    },
  });

  const test = useMutation({ mutationFn: api.testHostingSettings });

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (!valid || save.isPending) return;
    test.reset();
    save.mutate({});
  };

  const onClearKey = async () => {
    const ok = await confirm({
      title: "Hapus API key Dokploy?",
      description: "Instance tidak bisa dibuat, dihentikan, atau dihapus dari dashboard sampai API key baru disimpan.",
      confirmLabel: "Hapus key",
      cancelLabel: "Batal",
      tone: "danger",
    });
    if (!ok) return;
    test.reset();
    setApiKey("");
    save.mutate({ clearKey: true });
  };

  return (
    <form onSubmit={onSubmit} noValidate>
      <div className="space-y-5 px-4 py-4">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <p id="hosting-enabled" className="text-[13px] font-medium text-fg">
              Buka pemesanan di portal
            </p>
            <p className="text-[12px] text-fg-muted">Saat mati, user tidak bisa memesan atau memperpanjang. Instance yang ada tetap berjalan.</p>
          </div>
          <Toggle checked={enabled} onChange={setEnabled} aria-labelledby="hosting-enabled" />
        </div>

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field label={`Harga per ${settings.period_days || 30} hari (IDR)`} error={priceError} hint={!priceError && priceNum > 0 ? idr(priceNum) : undefined}>
            <Input type="number" inputMode="numeric" min="0" step="1" value={price} onChange={(e) => setPrice(e.target.value)} className="tabular-nums" />
          </Field>
          <Field label="Port container" hint="Diisi ke env PORT. Image luqmenul/9router-go memakai 20130." error={portError}>
            <Input type="number" inputMode="numeric" min="1" max="65535" value={port} onChange={(e) => setPort(e.target.value)} className="tabular-nums" />
          </Field>
          <Field label="URL Dokploy" hint="Alamat panel Dokploy, misal https://dokploy.example.com.">
            <Input type="url" value={dokployUrl} onChange={(e) => setDokployUrl(e.target.value)} placeholder="https://dokploy.example.com" autoComplete="off" spellCheck={false} className="font-mono" />
          </Field>
          <Field
            label="API key Dokploy"
            hint={
              keyNeeded
                ? undefined
                : settings.has_api_key
                  ? "Kosongkan untuk tetap memakai key yang tersimpan."
                  : "Belum ada key tersimpan."
            }
            error={keyNeeded ? "URL Dokploy berubah. Masukkan ulang API key untuk URL baru." : null}
          >
            <Input
              type="password"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              placeholder={settings.has_api_key ? settings.api_key_masked : "Tempel API key"}
              autoComplete="new-password"
              spellCheck={false}
              className="font-mono"
            />
          </Field>
          <Field label="Environment ID" hint="environmentId dari project Dokploy tempat instance dibuat.">
            <Input value={environmentId} onChange={(e) => setEnvironmentId(e.target.value)} autoComplete="off" spellCheck={false} className="font-mono" />
          </Field>
          <Field
            label="Link kontak aktivasi"
            hint="Tombol “Hubungi admin” untuk pelanggan yang sudah bayar dan menunggu aktivasi."
            error={supportUrlError}
          >
            <Input type="url" value={supportUrl} onChange={(e) => setSupportUrl(e.target.value)} placeholder="https://t.me/damnwahhh" autoComplete="off" spellCheck={false} className="font-mono" />
          </Field>
        </div>

        <NodesEditor nodes={nodes} setNodes={setNodes} errors={nodeErrors} listError={nodesError} />

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field
            label="Env password awal"
            hint="Setiap instance mendapat password acak di variabel ini. Pastikan sesuai dengan yang dibaca image 9router."
            error={envNameError}
          >
            <Input value={passwordEnv} onChange={(e) => setPasswordEnv(e.target.value)} autoComplete="off" spellCheck={false} className="font-mono" />
          </Field>
          <Field
            label="Path data"
            hint="Volume Docker per instance dipasang di sini dan diisi ke DATA_DIR, agar database, provider, dan API key pelanggan tidak hilang saat redeploy."
            error={dataPathError}
          >
            <Input value={dataPath} onChange={(e) => setDataPath(e.target.value)} placeholder="/data" autoComplete="off" spellCheck={false} className="font-mono" />
          </Field>
          <Field label="Batas memori (byte)" hint="Contoh 536870912 untuk 512 MB. Kosongkan untuk tanpa batas." error={memError}>
            <Input inputMode="numeric" value={memoryLimit} onChange={(e) => setMemoryLimit(e.target.value)} placeholder="536870912" className="font-mono tabular-nums" />
          </Field>
          <Field label="Batas CPU (nano-CPU)" hint="Contoh 1000000000 untuk 1 CPU. Kosongkan untuk tanpa batas." error={cpuError}>
            <Input inputMode="numeric" value={cpuLimit} onChange={(e) => setCpuLimit(e.target.value)} placeholder="1000000000" className="font-mono tabular-nums" />
          </Field>
        </div>

        <Field label="Image Docker" hint="Satu image per baris. Image pertama jadi pilihan default saat membuat instance. Versi 9router di bawah 0.4.60 ditolak (CVE-2026-63732).">
          <Textarea
            value={images}
            onChange={(e) => setImages(e.target.value)}
            rows={4}
            placeholder="luqmenul/9router-go:latest"
            spellCheck={false}
            className="font-mono text-[12.5px]"
          />
        </Field>

        <Field
          label="Template environment"
          hint={
            <>
              Format KEY=value, satu per baris. Placeholder yang diganti otomatis:{" "}
              <code className="font-mono">{"{{SUBDOMAIN}}"}</code>, <code className="font-mono">{"{{FQDN}}"}</code>,{" "}
              <code className="font-mono">{"{{URL}}"}</code>, <code className="font-mono">{"{{INSTANCE_ID}}"}</code>,{" "}
              <code className="font-mono">{"{{PASSWORD}}"}</code>. Diatur otomatis dan tidak bisa ditimpa: variabel password awal (password acak per instance), <code className="font-mono">PORT</code>, dan{" "}
              <code className="font-mono">DATA_DIR</code>. <code className="font-mono">STRICT_SSL=true</code> ditambahkan kecuali template mengaturnya.{" "}
              <code className="font-mono">JWT_SECRET</code> ditolak karena harus unik per instance.
            </>
          }
        >
          <Textarea value={envTemplate} onChange={(e) => setEnvTemplate(e.target.value)} rows={6} spellCheck={false} className="font-mono text-[12.5px]" />
        </Field>

        <p className="text-[12px] text-fg-muted">
          Versi ketentuan aktif: <span className="font-mono">{settings.terms_version}</span>
        </p>

        {save.isError && <ErrorBanner message={`Pengaturan gagal disimpan. ${errorText(save.error)}`} />}
        {test.isError && <ErrorBanner message={`Tes koneksi gagal. ${errorText(test.error)}`} />}
        {test.data &&
          (test.data.ok ? (
            <p role="status" className="flex items-center gap-2 rounded-xl border border-ok/30 bg-ok/10 px-3.5 py-2.5 text-[13px] text-fg">
              <Check className="h-4 w-4 shrink-0 text-ok" aria-hidden="true" />
              Terhubung ke Dokploy.
            </p>
          ) : (
            <ErrorBanner message={`Tidak bisa terhubung ke Dokploy. ${test.data.error ?? ""}`.trim()} />
          ))}
      </div>
      <div className="flex flex-col gap-2 border-t border-line bg-subtle px-4 py-2.5 sm:flex-row sm:items-center sm:justify-end">
        {settings.has_api_key && (
          <Button type="button" variant="ghost" className="sm:mr-auto" onClick={onClearKey} disabled={save.isPending}>
            <Trash2 aria-hidden="true" />
            Hapus API key
          </Button>
        )}
        <Button
          type="button"
          variant="ghost"
          onClick={() => {
            save.reset();
            test.mutate();
          }}
          disabled={test.isPending}
          title="Menguji pengaturan yang sudah tersimpan"
        >
          {test.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <PlugZap aria-hidden="true" />}
          Tes koneksi
        </Button>
        <Button type="submit" disabled={!valid || keyNeeded || save.isPending}>
          {save.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Check aria-hidden="true" />}
          Simpan
        </Button>
      </div>
    </form>
  );
}

// ─── Nodes ──────────────────────────────────────────────────────────────────

interface NodeDraft {
  key: string;
  id: string;
  name: string;
  serverId: string;
  rootDomain: string;
  monitorTargetId: string;
  capacity: string;
  enabled: boolean;
  isNew: boolean;
}

const MAX_NODE_CAPACITY = 10000;

const toNodeDraft = (n: HostingNode): NodeDraft => ({
  key: n.id,
  id: n.id,
  name: n.name,
  serverId: n.server_id ?? "",
  rootDomain: n.root_domain,
  monitorTargetId: n.monitor_target_id ?? "",
  capacity: String(n.capacity ?? 0),
  enabled: n.enabled,
  isNew: false,
});

function nodeErrorsOf(nodes: NodeDraft[]) {
  const domains = nodes.map((n) => normalizeDomain(n.rootDomain));
  return nodes.map((n, i) => {
    const name = n.name.trim();
    const domain = domains[i];
    const capText = n.capacity.trim();
    const cap = Number(capText || "0");
    return {
      name: !name ? "Isi nama node." : name.length > 80 ? "Maksimal 80 karakter." : null,
      domain: !domain
        ? "Isi root domain node."
        : !validDomain(domain)
          ? "Domain tidak valid, misal n1.example.com."
          : domains.indexOf(domain) !== i
            ? "Domain ini sudah dipakai node lain."
            : null,
      capacity:
        !/^[0-9]*$/.test(capText) || !Number.isInteger(cap) || cap > MAX_NODE_CAPACITY ? `Isi angka bulat 0 sampai ${MAX_NODE_CAPACITY}.` : null,
    };
  });
}

function NodesEditor({
  nodes,
  setNodes,
  errors,
  listError,
}: {
  nodes: NodeDraft[];
  setNodes: (fn: (rows: NodeDraft[]) => NodeDraft[]) => void;
  errors: ReturnType<typeof nodeErrorsOf>;
  listError: string | null;
}) {
  const confirm = useConfirm();
  const loads = useQuery({ queryKey: QK_HOSTING.nodes, queryFn: api.listHostingNodes });
  const monitor = useQuery({ queryKey: QK_HOSTING.monitorSettings, queryFn: api.getHostingMonitoring });
  const targets: HostingMonitorTarget[] = monitor.data?.targets ?? [];
  const loadOf = (id: string) => loads.data?.nodes.find((n) => n.id === id);

  const update = (key: string, patch: Partial<NodeDraft>) => setNodes((rs) => rs.map((r) => (r.key === key ? { ...r, ...patch } : r)));
  const add = () =>
    setNodes((rs) => {
      const id = newNodeId();
      return [
        ...rs,
        { key: id, id, name: `Node ${rs.length + 1}`, serverId: "", rootDomain: "", monitorTargetId: "", capacity: "0", enabled: true, isNew: true },
      ];
    });
  const remove = async (r: NodeDraft) => {
    const used = loadOf(r.id)?.used ?? 0;
    if (used > 0) {
      const ok = await confirm({
        title: `Hapus ${r.name.trim() || "node"}?`,
        description: (
          <>
            Node ini masih dipakai {used} instance. Instance yang berjalan tidak ikut dihapus, tetapi instance gagal di node ini tidak bisa dicoba
            ulang lagi. Jika hanya ingin berhenti menerima instance baru, matikan saja tombol Aktif di node ini.
          </>
        ),
        confirmLabel: "Tetap hapus",
        cancelLabel: "Batal",
        tone: "danger",
      });
      if (!ok) return;
    }
    setNodes((rs) => rs.filter((x) => x.key !== r.key));
  };

  return (
    <section aria-labelledby="hosting-nodes-title" className="space-y-3">
      <div>
        <p id="hosting-nodes-title" className="text-[13px] font-medium text-fg">
          Node
        </p>
        <p className="text-[12px] text-fg-muted">
          Server Dokploy yang menjalankan container pelanggan. Tiap node butuh DNS wildcard sendiri{" "}
          <span className="font-mono text-fg">*.&lt;root domain&gt;</span> yang mengarah ke IP node itu. Tambahkan server di Dokploy (Settings → Servers),
          lalu salin Server ID-nya. Instance baru otomatis ditaruh di node aktif dengan ruang paling lega.
        </p>
      </div>
      {nodes.length === 0 && (
        <p className="rounded-xl border border-dashed border-line-strong px-4 py-6 text-center text-[13px] text-fg-muted">
          Belum ada node. Tambahkan minimal satu node agar instance bisa dibuat.
        </p>
      )}
      {nodes.map((r, i) => {
        const err = errors[i];
        const load = loadOf(r.id);
        const cap = Number(r.capacity.trim() || "0");
        const full = load ? cap > 0 && load.used >= cap : false;
        const domain = normalizeDomain(r.rootDomain);
        const unknownTarget = r.monitorTargetId !== "" && monitor.data && !targets.some((t) => t.id === r.monitorTargetId);
        const toggleId = `hosting-node-enabled-${r.key}`;
        return (
          <fieldset key={r.key} className="rounded-xl border border-line bg-subtle/60 p-3.5">
            <legend className="sr-only">{r.name.trim() || `Node ${i + 1}`}</legend>
            <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <span className="text-[12.5px] font-medium text-fg">{r.name.trim() || `Node ${i + 1}`}</span>
                <span className="font-mono text-[11px] text-fg-faint">{r.id}</span>
                {r.isNew ? (
                  <Badge tone="accent">Baru</Badge>
                ) : load ? (
                  <Badge tone={!r.enabled ? "neutral" : full ? "danger" : "neutral"} title="Instance di node ini / kapasitas">
                    <span className="tabular-nums">{load.used}/{cap > 0 ? cap : "∞"}</span> instance{full && r.enabled ? " · penuh" : ""}
                  </Badge>
                ) : null}
                {!r.enabled && <Badge tone="neutral">Nonaktif</Badge>}
              </div>
              <div className="flex items-center gap-2">
                <span id={toggleId} className="text-[12px] text-fg-muted">
                  Aktif
                </span>
                <Toggle checked={r.enabled} onChange={(v) => update(r.key, { enabled: v })} aria-labelledby={toggleId} />
                <Button type="button" variant="ghost" onClick={() => remove(r)} aria-label={`Hapus ${r.name.trim() || `node ${i + 1}`}`} title="Hapus node">
                  <Trash2 aria-hidden="true" />
                </Button>
              </div>
            </div>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
              <Field label="Nama" error={err.name}>
                <Input value={r.name} onChange={(e) => update(r.key, { name: e.target.value })} placeholder="Node 1" maxLength={80} autoComplete="off" />
              </Field>
              <Field
                label="Root domain"
                error={err.domain}
                hint={!err.domain && domain ? <>Arahkan <span className="font-mono">*.{domain}</span> ke IP node ini.</> : undefined}
              >
                <Input
                  value={r.rootDomain}
                  onChange={(e) => update(r.key, { rootDomain: e.target.value })}
                  placeholder="n1.example.com"
                  autoComplete="off"
                  spellCheck={false}
                  className="font-mono"
                />
              </Field>
              <Field label="Server ID Dokploy" hint="Kosongkan untuk server utama Dokploy (tidak disarankan).">
                <Input value={r.serverId} onChange={(e) => update(r.key, { serverId: e.target.value })} autoComplete="off" spellCheck={false} className="font-mono" />
              </Field>
              <Field label="Server monitoring" hint={monitor.isError ? `Daftar server monitoring gagal dimuat. ${errorText(monitor.error)}` : undefined}>
                <Select value={r.monitorTargetId} onChange={(e) => update(r.key, { monitorTargetId: e.target.value })}>
                  <option value="">Otomatis (server pelanggan)</option>
                  {targets.map((t) => (
                    <option key={t.id} value={t.id}>
                      {t.name}
                    </option>
                  ))}
                  {unknownTarget && <option value={r.monitorTargetId}>(server tidak ditemukan)</option>}
                </Select>
              </Field>
              <Field label="Kapasitas" hint={err.capacity ? undefined : "Jumlah instance maksimal. 0 = tanpa batas."} error={err.capacity}>
                <Input
                  type="number"
                  inputMode="numeric"
                  min="0"
                  max={MAX_NODE_CAPACITY}
                  step="1"
                  value={r.capacity}
                  onChange={(e) => update(r.key, { capacity: e.target.value })}
                  className="tabular-nums"
                />
              </Field>
            </div>
          </fieldset>
        );
      })}
      {listError && <ErrorBanner message={listError} />}
      <Button type="button" variant="ghost" onClick={add}>
        <Plus aria-hidden="true" />
        Tambah node
      </Button>
    </section>
  );
}

function SetupChecklist({ nodes }: { nodes?: HostingNode[] }) {
  const domains = (nodes ?? []).map((n) => n.root_domain).filter(Boolean);
  return (
    <Panel icon={ListChecks} title="Persiapan Dokploy" subtitle="Lakukan sekali sebelum membuka pemesanan, dan ulangi langkah node setiap menambah server.">
      <ol className="list-decimal space-y-2 px-4 py-4 pl-9 text-[13px] text-fg-muted">
        <li>
          Buat project dan environment di Dokploy khusus untuk hosting, lalu salin <span className="font-mono text-fg">environmentId</span>-nya ke kolom Environment ID.
        </li>
        <li>
          Buat API key di Dokploy lewat menu <span className="text-fg">Settings → Profile → API</span>, lalu tempel di kolom API key Dokploy.
        </li>
        <li>
          Tambahkan server remote khusus pelanggan di Dokploy (Settings → Servers), lalu daftarkan sebagai node dan isi <span className="font-mono text-fg">Server ID</span>-nya.
          Container pelanggan di server utama bisa menjangkau database dan Redis milik Dokploy.
        </li>
        <li>
          Beri tiap node root domain sendiri dan arahkan DNS wildcard-nya ke IP node itu, karena satu wildcard hanya bisa mengarah ke satu server
          {domains.length > 0 ? (
            <>
              :{" "}
              {domains.map((d, i) => (
                <span key={d}>
                  {i > 0 && ", "}
                  <span className="font-mono text-fg">*.{d}</span>
                </span>
              ))}
              .
            </>
          ) : (
            <>
              , misal <span className="font-mono text-fg">*.n1.example.com</span>.
            </>
          )}
        </li>
        <li>
          Pakai image 9router versi 0.4.60 atau lebih baru, dan pastikan nama env password awal sesuai image tersebut. Buat satu instance uji, lalu cek bahwa
          password default <span className="font-mono text-fg">123456</span> ditolak.
        </li>
        <li>Isi minimal satu image Docker 9router, simpan, lalu klik Tes koneksi sebelum menyalakan pemesanan.</li>
        <li>Saat node mendekati penuh, tambah server baru di Dokploy lalu tambahkan sebagai node baru dengan domain wildcard sendiri.</li>
      </ol>
    </Panel>
  );
}
