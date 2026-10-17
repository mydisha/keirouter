import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  Check,
  ExternalLink,
  ListChecks,
  Loader2,
  Play,
  PlugZap,
  ReceiptText,
  Rocket,
  RotateCcw,
  Server,
  Settings2,
  Square,
  Trash2,
  Wallet,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { api, type HostingInstanceAdmin, type HostingSettings, type HostingStatus } from "../lib/api";
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
import { QK_HOSTING, STATUS_LABEL, STATUS_TONE } from "./hosting/shared";

type Tab = "instances" | "finance" | "monitoring" | "settings";
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
          { value: "finance", label: "Keuangan", icon: Wallet },
          { value: "monitoring", label: "Monitoring", icon: Activity },
          { value: "settings", label: "Pengaturan", icon: Settings2 },
        ]}
      />
      <TabPanel idPrefix={TABS_ID} value={tab}>
        {tab === "instances" ? (
          <InstancesTab settings={settings.data} />
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

function InstancesTab({ settings }: { settings?: HostingSettings }) {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const [status, setStatus] = useState("");
  const [provisioning, setProvisioning] = useState<HostingInstanceAdmin | null>(null);
  const [ordersOf, setOrdersOf] = useState<HostingInstanceAdmin | null>(null);

  const list = useQuery({
    queryKey: [...QK_HOSTING.instances, status],
    queryFn: () => api.listHostingInstances(status || undefined),
    refetchInterval: REFETCH_MS,
  });
  const instances = list.data?.instances ?? [];
  const { paged, page, pages, setPage, total } = useClientPagination(instances, 10);

  const invalidate = () => qc.invalidateQueries({ queryKey: QK_HOSTING.instances });

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
        <Select value={status} onChange={(e) => setStatus(e.target.value)} aria-label="Filter status" className="w-auto min-w-[180px]">
          <option value="">Semua status</option>
          {(Object.keys(STATUS_LABEL) as HostingStatus[]).map((s) => (
            <option key={s} value={s}>
              {STATUS_LABEL[s]}
            </option>
          ))}
        </Select>
      }
    >
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
          title={status ? "Tidak ada instance dengan status ini" : "Belum ada instance"}
          hint="Pesanan hosting dari portal muncul di sini begitu user memulai pembayaran."
        />
      ) : (
        <>
          <div className="overflow-x-auto" role="region" aria-label="Tabel instance hosting" tabIndex={0}>
            <table className="w-full min-w-[1040px] text-[13px]">
              <thead>
                <tr className={THEAD}>
                  <th scope="col" className={TH}>User</th>
                  <th scope="col" className={TH}>Status</th>
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
          onClose={() => setProvisioning(null)}
          onDone={invalidate}
        />
      )}
      {ordersOf && <OrdersModal inst={ordersOf} onClose={() => setOrdersOf(null)} />}
    </Panel>
  );
}

function ProvisionModal({
  inst,
  images,
  onClose,
  onDone,
}: {
  inst: HostingInstanceAdmin;
  images: string[];
  onClose: () => void;
  onDone: () => void;
}) {
  const toast = useToast();
  const [image, setImage] = useState(inst.image && images.includes(inst.image) ? inst.image : images[0] ?? "");
  const provision = useMutation({
    mutationFn: () => api.provisionHosting(inst.id, image),
    onSuccess: (h) => {
      toast.success("Instance dibuat", h.fqdn ? `Aktif di ${h.fqdn}.` : undefined);
      onDone();
      onClose();
    },
    // A Dokploy failure marks the instance failed; refresh so the row shows it.
    onError: onDone,
  });
  const retry = inst.status === "failed";

  return (
    <Modal
      open
      onClose={provision.isPending ? () => {} : onClose}
      title={retry ? "Coba buat ulang instance" : "Buat instance"}
      subtitle={inst.user_email || inst.key_id}
    >
      <div className="space-y-3 px-5 py-4">
        <p className="text-[13px] text-fg-muted">
          Instance dibuat di Dokploy dengan subdomain acak. Masa aktif mulai dihitung saat instance aktif.
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
        {provision.isError && <ErrorBanner message={`Instance gagal dibuat. ${errorText(provision.error)}`} />}
      </div>
      <ModalFooter>
        <Button variant="ghost" onClick={onClose} disabled={provision.isPending}>
          Batal
        </Button>
        <Button onClick={() => provision.mutate()} disabled={!image || provision.isPending}>
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
          <table className="w-full min-w-[640px] text-[13px]">
            <thead>
              <tr className={THEAD}>
                <th scope="col" className={TH}>Order</th>
                <th scope="col" className={TH}>Status</th>
                <th scope="col" className={cn(TH, "text-right")}>Jumlah</th>
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
                  <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums">{idr(o.amount_idr)}</td>
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
      <SetupChecklist rootDomain={query.data?.root_domain} />
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
  const [rootDomain, setRootDomain] = useState(settings.root_domain);
  const [images, setImages] = useState(settings.images.join("\n"));
  const [port, setPort] = useState(String(settings.container_port || ""));
  const [envTemplate, setEnvTemplate] = useState(settings.env_template);
  const [serverId, setServerId] = useState(settings.server_id ?? "");
  const [passwordEnv, setPasswordEnv] = useState(settings.password_env || "INITIAL_PASSWORD");
  const [memoryLimit, setMemoryLimit] = useState(settings.memory_limit ?? "");
  const [cpuLimit, setCpuLimit] = useState(settings.cpu_limit ?? "");
  const [dataPath, setDataPath] = useState(settings.data_path || "/data");

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
  const urlChanged = dokployUrl.trim().replace(/\/+$/, "") !== settings.dokploy_url;
  const keyNeeded = settings.has_api_key && urlChanged && dokployUrl.trim() !== "" && apiKey.trim() === "";
  const valid = !priceError && !portError && !memError && !cpuError && !envNameError && !dataPathError;

  const save = useMutation({
    mutationFn: (opts: { clearKey?: boolean }) =>
      api.putHostingSettings({
        enabled,
        monthly_price_idr: priceNum,
        dokploy_url: dokployUrl.trim(),
        ...(apiKey.trim() ? { api_key: apiKey.trim() } : {}),
        ...(opts.clearKey ? { clear_api_key: true } : {}),
        environment_id: environmentId.trim(),
        root_domain: rootDomain.trim(),
        images: imageList,
        container_port: portNum,
        env_template: envTemplate,
        server_id: serverId.trim(),
        password_env: passwordEnv.trim(),
        memory_limit: memoryLimit.trim(),
        cpu_limit: cpuLimit.trim(),
        data_path: dataPath.trim(),
      }),
    onSuccess: (data) => {
      qc.setQueryData(QK_HOSTING.settings, data);
      qc.invalidateQueries({ queryKey: QK_HOSTING.finance });
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
          <Field label="Root domain" hint="Subdomain acak dibuat di bawah domain ini.">
            <Input value={rootDomain} onChange={(e) => setRootDomain(e.target.value)} placeholder="hosting.example.com" autoComplete="off" spellCheck={false} className="font-mono" />
          </Field>
        </div>

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field
            label="Server ID Dokploy"
            hint="Disarankan: serverId dari server remote khusus pelanggan, agar container tidak bisa menjangkau database dan Redis milik Dokploy. Kosongkan untuk server utama."
          >
            <Input value={serverId} onChange={(e) => setServerId(e.target.value)} autoComplete="off" spellCheck={false} className="font-mono" />
          </Field>
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

function SetupChecklist({ rootDomain }: { rootDomain?: string }) {
  const domain = rootDomain?.trim() || "<root_domain>";
  return (
    <Panel icon={ListChecks} title="Persiapan Dokploy" subtitle="Lakukan sekali sebelum membuka pemesanan.">
      <ol className="list-decimal space-y-2 px-4 py-4 pl-9 text-[13px] text-fg-muted">
        <li>
          Buat project dan environment di Dokploy khusus untuk hosting, lalu salin <span className="font-mono text-fg">environmentId</span>-nya ke kolom Environment ID.
        </li>
        <li>
          Arahkan DNS wildcard <span className="font-mono text-fg">*.{domain}</span> ke IP server Dokploy agar setiap subdomain langsung bisa diakses dengan HTTPS.
        </li>
        <li>
          Buat API key di Dokploy lewat menu <span className="text-fg">Settings → Profile → API</span>, lalu tempel di kolom API key Dokploy.
        </li>
        <li>
          Tambahkan server remote khusus pelanggan di Dokploy (Settings → Servers) dan isi <span className="font-mono text-fg">Server ID</span>. Container pelanggan
          di server utama bisa menjangkau database dan Redis milik Dokploy.
        </li>
        <li>
          Pakai image 9router versi 0.4.60 atau lebih baru, dan pastikan nama env password awal sesuai image tersebut. Buat satu instance uji, lalu cek bahwa
          password default <span className="font-mono text-fg">123456</span> ditolak.
        </li>
        <li>Isi minimal satu image Docker 9router, simpan, lalu klik Tes koneksi sebelum menyalakan pemesanan.</li>
      </ol>
    </Panel>
  );
}
