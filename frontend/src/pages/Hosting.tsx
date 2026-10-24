import { useMemo, useState, type FormEvent, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Navigate, useNavigate, useParams } from "react-router-dom";
import { dashboard } from "../lib/dashboardRoutes";
import {
  ArrowLeft,
  ArrowRight,
  Check,
  ChevronRight,
  CircleDashed,
  ExternalLink,
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
  Trash2,
} from "lucide-react";
import { cn } from "@/lib/utils";
import {
  api,
  type HostingInstanceAdmin,
  type HostingNode,
  type HostingNodeLoad,
  type HostingRegion,
  type HostingRegionStock,
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
  TablePagination,
  Textarea,
  Toggle,
  useClientPagination,
} from "../components/ui";
import { Panel, REFETCH_MS, TH, THEAD, dateID, errorText, idr } from "./payments/shared";
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

const SECTIONS = ["instances", "coupons", "settings"] as const;
type Section = (typeof SECTIONS)[number];

const ORDER_STATUS_LABEL: Record<string, string> = {
  completed: "Paid",
  manual: "Manually approved",
  pending: "Pending",
  failed: "Failed",
  expired: "Expired",
};

const ORDER_STATUS_TONE: Record<string, "success" | "warning" | "danger" | "neutral"> = {
  completed: "success",
  manual: "success",
  pending: "warning",
  failed: "danger",
  expired: "neutral",
};

// Each section has its own URL so the sidebar can link to it directly;
// /hosting itself is the instance list.
export function HostingPage() {
  const params = useParams();
  const section = (params.section ?? "instances") as Section;
  const navigate = useNavigate();
  const settings = useQuery({ queryKey: QK_HOSTING.settings, queryFn: api.getHostingSettings });

  if (params.section === "instances" || !SECTIONS.includes(section)) {
    return <Navigate to={dashboard("/hosting")} replace />;
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Hosting"
        description="9router instances ordered by portal users. Create each one in Dokploy once its order is paid."
      />
      {section === "instances" ? (
        <InstancesTab settings={settings.data} onOpenSettings={() => navigate(dashboard("/hosting/settings"))} />
      ) : section === "coupons" ? (
        <CouponsTab />
      ) : (
        <SettingsTab query={settings} />
      )}
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
      toast.success(kind === "stop" ? "Instance stopped" : kind === "start" ? "Instance started" : "Instance deleted");
      invalidate();
    },
    onError: (e, { kind }) => {
      toast.error(kind === "stop" ? "Couldn't stop instance" : kind === "start" ? "Couldn't start instance" : "Couldn't delete instance", errorText(e));
      invalidate();
    },
  });

  const busy = (id: string) => action.isPending && action.variables?.id === id;

  const onDelete = async (inst: HostingInstanceAdmin) => {
    const ok = await confirm({
      title: "Delete instance?",
      description: (
        <>
          The Dokploy app <span className="font-mono">{inst.dokploy_app_name || inst.subdomain || inst.id}</span> is deleted too,
          including its data. {inst.user_email || inst.key_id} loses access. This can't be undone.
        </>
      ),
      confirmLabel: "Delete instance",
      cancelLabel: "Cancel",
      tone: "danger",
    });
    if (ok) action.mutate({ kind: "delete", id: inst.id });
  };

  return (
    <Panel
      icon={Server}
      title="Instances"
      subtitle="Status, address and paid period of each instance. Refreshes every 15 seconds."
      action={
        <div className="flex flex-wrap gap-2">
          {nodeList.length > 1 && (
            <Select value={nodeFilter} onChange={(e) => setNodeFilter(e.target.value)} aria-label="Filter node" className="w-auto min-w-[150px]">
              <option value="">All nodes</option>
              {nodeList.map((n) => (
                <option key={n.id} value={n.id}>
                  {n.name || n.id}
                </option>
              ))}
              <option value={NO_NODE}>No node yet</option>
            </Select>
          )}
          <Select value={status} onChange={(e) => setStatus(e.target.value)} aria-label="Filter status" className="w-auto min-w-[180px]">
            <option value="">All statuses</option>
            {(Object.keys(STATUS_LABEL) as HostingStatus[]).map((s) => (
              <option key={s} value={s}>
                {STATUS_LABEL[s]}
              </option>
            ))}
          </Select>
        </div>
      }
    >
      {nodesQ.data && <RegionStockStrip regions={nodesQ.data.regions ?? []} />}
      {nodesQ.data && <NodeUsageStrip nodes={nodesQ.data.nodes} onOpenSettings={onOpenSettings} />}
      {list.isLoading ? (
        <LoadingRegion label="Loading instances" className="space-y-2 p-4">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </LoadingRegion>
      ) : list.isError && !list.data ? (
        <div className="p-4">
          <ErrorBanner message={`Couldn't load instances. ${errorText(list.error)}`} />
        </div>
      ) : instances.length === 0 ? (
        <EmptyState
          icon={Server}
          size="sm"
          title={status || nodeFilter ? "No instances match the filter" : "No instances yet"}
          hint="Hosting orders from the portal show up here as soon as a customer starts paying."
        />
      ) : (
        <>
          <div className="overflow-x-auto" role="region" aria-label="Hosting instances table" tabIndex={0}>
            <table className="w-full min-w-[1140px] text-[13px]">
              <thead>
                <tr className={THEAD}>
                  <th scope="col" className={TH}>User</th>
                  <th scope="col" className={TH}>Status</th>
                  <th scope="col" className={TH}>Node / region</th>
                  <th scope="col" className={TH}>Address</th>
                  <th scope="col" className={TH}>Image</th>
                  <th scope="col" className={TH}>Paid until</th>
                  <th scope="col" className={TH}>Created</th>
                  <th scope="col" className={cn(TH, "text-right")}>
                    <span className="sr-only">Actions</span>
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
                    <td className="whitespace-nowrap px-4 py-2.5">
                      {h.node_id ? nodeName(nodeList, h.node_id) : <span className="text-fg-muted">-</span>}
                      {h.region && <p className="text-[11.5px] text-fg-muted">{settings?.regions.find((r) => r.id === h.region)?.name ?? h.region}</p>}
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
                      {h.prepaid_days > 0 && <p className="text-[11.5px] text-fg-muted">+{h.prepaid_days} days prepaid</p>}
                    </td>
                    <td className="whitespace-nowrap px-4 py-2.5 tabular-nums text-fg-muted">{dateID(h.created_at, true)}</td>
                    <td className="px-4 py-2.5">
                      <div className="flex flex-wrap justify-end gap-1">
                        {(h.status === "paid" || h.status === "failed") && (
                          <Button variant={h.status === "paid" ? "primary" : "secondary"} onClick={() => setProvisioning(h)}>
                            {h.status === "paid" ? <Rocket aria-hidden="true" /> : <RotateCcw aria-hidden="true" />}
                            {h.status === "paid" ? "Create" : "Retry"}
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
                        <Button variant="ghost" onClick={() => setOrdersOf(h)} title="View orders">
                          <ReceiptText aria-hidden="true" />
                          Order
                          {(h.orders?.length ?? 0) > 0 && <span className="tabular-nums text-fg-muted">{h.orders!.length}</span>}
                        </Button>
                        {h.status !== "deleted" && (
                          <Button variant="ghost" disabled={busy(h.id)} onClick={() => onDelete(h)} title="Delete instance" aria-label="Delete instance">
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
          <TablePagination page={page} pages={pages} total={total} onPage={setPage} label="Hosting instances pages" />
        </>
      )}

      {provisioning && (
        <ProvisionModal
          inst={provisioning}
          images={settings?.images ?? []}
          nodes={nodesQ.data}
          regions={settings?.regions ?? []}
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

/** Containers still available per region: what the landing page sells. */
function RegionStockStrip({ regions }: { regions: HostingRegionStock[] }) {
  if (regions.length === 0) return null;
  return (
    <div className="flex flex-col divide-y divide-line border-b border-line sm:flex-row sm:divide-x sm:divide-y-0" role="list" aria-label="Stock per region">
      {regions.map((r) => (
        <div key={r.id} role="listitem" className="min-w-0 flex-1 px-4 py-3">
          <div className="flex items-center justify-between gap-2">
            <p className="text-[13px] font-medium text-fg">{r.name}</p>
            <Badge tone={!r.enabled ? "neutral" : r.sold_out ? "danger" : r.stock >= 0 && r.stock <= 2 ? "warning" : "success"}>
              {!r.enabled ? "Disabled" : r.sold_out ? "Sold out" : r.stock < 0 ? "Unlimited" : `${r.stock} available`}
            </Badge>
          </div>
          <p className="mt-1 text-[12px] tabular-nums text-fg-muted">
            {r.nodes} node · {r.used} used{r.reserved > 0 ? ` · ${r.reserved} waiting to be created` : ""}
            {r.capacity >= 0 ? ` · capacity ${r.capacity}` : ""} · {idr(r.price_idr)}
          </p>
        </div>
      ))}
    </div>
  );
}

/** Compact per-node usage so the admin sees when a node is filling up. */
function NodeUsageStrip({ nodes, onOpenSettings }: { nodes: HostingNodeLoad[]; onOpenSettings: () => void }) {
  if (nodes.length === 0) {
    return (
      <div className="border-b border-line px-4 py-3">
        <ErrorBanner message="No nodes yet. Add a node in Hosting setup before creating instances." />
        <Button variant="ghost" className="mt-2" onClick={onOpenSettings}>
          <Settings2 aria-hidden="true" />
          Open settings
        </Button>
      </div>
    );
  }
  const noneAvailable = !nodes.some((n) => n.available);
  return (
    <div className="flex flex-wrap items-center gap-1.5 border-b border-line px-4 py-2.5" role="list" aria-label="Node usage">
      <span className="mr-1 text-[12px] text-fg-muted">Node:</span>
      {nodes.map((n) => {
        const blocked = nodeBlockedLabel(n);
        return (
          <span key={n.id} role="listitem">
            <Badge
              tone={!n.enabled ? "neutral" : !n.available ? "danger" : n.capacity > 0 && n.free <= Math.max(1, Math.floor(n.capacity * 0.1)) ? "warning" : "neutral"}
              title={`${n.public_ip}${blocked ? ` · ${blocked}` : ""}`}
            >
              {n.name || n.id} · <span className="tabular-nums">{nodeUsage(n)}</span>
              {blocked && <span> · {blocked}</span>}
            </Badge>
          </span>
        );
      })}
      {noneAvailable && (
        <button type="button" onClick={onOpenSettings} className="ml-1 text-[12px] text-bad underline-offset-2 hover:underline">
          All nodes are full or disabled. Add a node in Hosting setup.
        </button>
      )}
    </div>
  );
}

/** The node with the most room, mirroring the server's choice. */
function bestNode(nodes: HostingNodeLoad[], overcommit: boolean): HostingNodeLoad | undefined {
  const open = nodes.filter((n) => n.available);
  const rank = (n: HostingNodeLoad) => (n.free === -1 ? Number.MAX_SAFE_INTEGER - n.used : n.free);
  if (open.length > 0) return [...open].sort((a, b) => rank(b) - rank(a))[0];
  if (!overcommit) return undefined;
  // Every node is full: the least overfull enabled one.
  return [...nodes.filter((n) => n.enabled)].sort((a, b) => a.used - a.capacity - (b.used - b.capacity))[0];
}

function ProvisionModal({
  inst,
  images,
  nodes,
  regions,
  nodesLoading,
  nodesError,
  onClose,
  onDone,
  onOpenSettings,
}: {
  inst: HostingInstanceAdmin;
  images: string[];
  nodes?: { nodes: HostingNodeLoad[]; recommended: string };
  regions: HostingRegion[];
  nodesLoading: boolean;
  nodesError: unknown;
  onClose: () => void;
  onDone: () => void;
  onOpenSettings: () => void;
}) {
  const toast = useToast();
  const [image, setImage] = useState(inst.image && images.includes(inst.image) ? inst.image : images[0] ?? "");
  // null follows the automatic choice, which may arrive after the modal opens.
  const [picked, setPicked] = useState<string | null>(null);
  const [overcommit, setOvercommit] = useState(false);
  // A failed instance that already got a subdomain stays on its node.
  const lockedNode = inst.node_id && inst.subdomain ? inst.node_id : "";
  const allNodes = nodes?.nodes ?? [];
  const regionName = regions.find((r) => r.id === inst.region)?.name ?? inst.region;
  // The customer paid for a region: its nodes come first. With no node there
  // at all, every node is offered so the admin is never stuck.
  const inRegion = inst.region ? allNodes.filter((n) => n.region === inst.region) : allNodes;
  const candidates = inRegion.length > 0 ? inRegion : allNodes;
  const auto = bestNode(candidates, overcommit);
  const nodeId = lockedNode || (picked ?? auto?.id ?? "");
  const chosen = candidates.find((n) => n.id === nodeId);
  const anyAvailable = candidates.some((n) => n.available);
  const anyEnabled = candidates.some((n) => n.enabled);
  const chosenFull = !!chosen && chosen.enabled && !chosen.available;
  const nodeReady = lockedNode ? true : !!chosen && chosen.enabled && (chosen.available || overcommit);

  const provision = useMutation({
    mutationFn: () => api.provisionHosting(inst.id, image, lockedNode ? undefined : nodeId, !lockedNode && overcommit && chosenFull),
    onSuccess: (h) => {
      toast.success("Instance created", h.fqdn ? `Live at ${h.fqdn}.` : undefined);
      onDone();
      onClose();
    },
    // A Dokploy failure marks the instance failed; refresh so the row shows it.
    onError: onDone,
  });
  const retry = inst.status === "failed";
  const locked = lockedNode ? allNodes.find((n) => n.id === lockedNode) : undefined;

  return (
    <Modal
      open
      onClose={provision.isPending ? () => {} : onClose}
      title={retry ? "Retry creating instance" : "Create instance"}
      subtitle={`${inst.user_email || inst.key_id}${inst.region ? ` · ${regionName}` : ""}`}
    >
      <div className="space-y-3 px-5 py-4">
        <p className="text-[13px] text-fg-muted">
          The instance is created in Dokploy with a random subdomain on the chosen node's domain. The paid period starts when the instance goes live.
        </p>
        {retry && inst.last_error && <ErrorBanner message={`Last attempt failed: ${inst.last_error}`} />}
        {images.length === 0 ? (
          <ErrorBanner message="No images yet. Add a Docker image in Hosting setup first." />
        ) : (
          <Field label="Docker image">
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
          <Field label="Node" hint="This instance was already set up on this node, so the retry stays there. Delete the instance to move it to another node.">
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
          <ErrorBanner message={`Couldn't load nodes. ${errorText(nodesError)}`} />
        ) : !anyEnabled ? (
          <div className="space-y-2">
            <ErrorBanner
              message={
                candidates.length === 0
                  ? "No nodes yet. Add a node in Hosting setup first."
                  : "All nodes in this region are disabled. Enable a node or add a new one in Hosting setup."
              }
            />
            <Button variant="ghost" onClick={onOpenSettings}>
              <Settings2 aria-hidden="true" />
              Open settings
            </Button>
          </div>
        ) : (
          <>
            {inst.region && inRegion.length === 0 && (
              <ErrorBanner message={`No nodes in ${regionName} yet. The instance will be created on a node in another region.`} />
            )}
            <Field
              label={inst.region && inRegion.length > 0 ? `Node in ${regionName}` : "Node"}
              hint={
                !anyAvailable && !overcommit
                  ? "All nodes are full. Check Overcommit to create the instance anyway."
                  : picked === null
                    ? "Picked automatically: the node with the most free room."
                    : "Node picked manually."
              }
            >
              <Select value={nodeId} onChange={(e) => setPicked(e.target.value)}>
                {!chosen && <option value="">Choose a node</option>}
                {candidates.map((n) => {
                  const blocked = nodeBlockedLabel(n);
                  return (
                    <option key={n.id} value={n.id} disabled={!n.enabled || (!n.available && !overcommit)}>
                      {`${n.name || n.id} · ${nodeUsage(n)}${blocked ? ` (${blocked})` : ""}`}
                    </option>
                  );
                })}
              </Select>
            </Field>
            <label className="flex cursor-pointer items-start gap-2.5 rounded-xl border border-line bg-subtle px-3.5 py-2.5 text-[13px]">
              <input
                type="checkbox"
                checked={overcommit}
                onChange={(e) => {
                  setOvercommit(e.target.checked);
                  if (!e.target.checked && chosenFull) setPicked(null);
                }}
                className="mt-0.5 h-4 w-4 shrink-0 accent-accent-500"
              />
              <span>
                <span className="font-medium text-fg">Overcommit</span>
                <span className="block text-[12px] text-fg-muted">
                  Create the instance even if the node is at capacity. Make sure the server still has enough RAM and CPU.
                </span>
              </span>
            </label>
            {overcommit && chosenFull && (
              <p role="status" className="text-[12.5px] text-warn">
                {chosen?.name || chosen?.id} is full ({nodeUsage(chosen!)}). This instance will go over capacity.
              </p>
            )}
          </>
        )}
        {provision.isError && <ErrorBanner message={`Couldn't create instance. ${errorText(provision.error)}`} />}
      </div>
      <ModalFooter>
        <Button variant="ghost" onClick={onClose} disabled={provision.isPending}>
          Cancel
        </Button>
        <Button onClick={() => provision.mutate()} disabled={!image || !nodeReady || provision.isPending}>
          {provision.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Rocket aria-hidden="true" />}
          {provision.isPending ? "Creating…" : retry ? "Retry" : "Create instance"}
        </Button>
      </ModalFooter>
    </Modal>
  );
}

function OrdersModal({ inst, onClose }: { inst: HostingInstanceAdmin; onClose: () => void }) {
  const orders = inst.orders ?? [];
  return (
    <Modal open onClose={onClose} title="Hosting orders" subtitle={inst.user_email || inst.key_id} maxWidth="max-w-3xl">
      {orders.length === 0 ? (
        <EmptyState icon={ReceiptText} size="sm" title="No orders yet" />
      ) : (
        <div className="overflow-x-auto" role="region" aria-label="Hosting orders table" tabIndex={0}>
          <table className="w-full min-w-[760px] text-[13px]">
            <thead>
              <tr className={THEAD}>
                <th scope="col" className={TH}>Order</th>
                <th scope="col" className={TH}>Status</th>
                <th scope="col" className={cn(TH, "text-right")}>Amount</th>
                <th scope="col" className={TH}>Coupon</th>
                <th scope="col" className={TH}>Paid</th>
                <th scope="col" className={TH}>Terms</th>
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
                        {!!o.bonus_days && <p className="text-[11.5px] text-fg-muted">+{o.bonus_days} days</p>}
                      </>
                    ) : (
                      "-"
                    )}
                    {o.reason?.startsWith("refund_required") && <Badge tone="danger">Refund needed</Badge>}
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 tabular-nums">{o.paid_at ? dateID(o.paid_at, true) : "-"}</td>
                  <td className="px-4 py-2.5">
                    <p className="font-mono text-[12px]">{o.terms_version || "-"}</p>
                    <p className="text-[11.5px] tabular-nums text-fg-muted">
                      {o.terms_accepted_at ? `Accepted ${dateID(o.terms_accepted_at, true)}` : "-"}
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
          Close
        </Button>
      </ModalFooter>
    </Modal>
  );
}

// ─── Settings ───────────────────────────────────────────────────────────────

function SettingsTab({ query }: { query: { data?: HostingSettings; isLoading: boolean; isError: boolean; error: unknown } }) {
  return (
    <div className="space-y-6">
      <Panel icon={Settings2} title="Hosting setup" subtitle="Go through the steps in order: Dokploy, domain, image, region, node, then open ordering.">
        {query.isLoading ? (
          <LoadingRegion label="Loading hosting settings" className="space-y-3 p-4">
            <Skeleton className="h-[320px] w-full rounded-xl" />
          </LoadingRegion>
        ) : !query.data ? (
          <div className="p-4">
            <ErrorBanner message={`Couldn't load hosting settings. ${query.isError ? errorText(query.error) : ""}`.trim()} />
          </div>
        ) : (
          // Re-key on the stored values so a save resets the form to them.
          <SettingsForm key={JSON.stringify(query.data)} settings={query.data} />
        )}
      </Panel>
    </div>
  );
}

function SettingsForm({ settings }: { settings: HostingSettings }) {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const [enabled, setEnabled] = useState(settings.enabled);
  const [dokployUrl, setDokployUrl] = useState(settings.dokploy_url);
  const [apiKey, setApiKey] = useState("");
  const [environmentId, setEnvironmentId] = useState(settings.environment_id);
  const [rootDomain, setRootDomain] = useState(settings.root_domain ?? "");
  const [zoneId, setZoneId] = useState(settings.cloudflare_zone_id ?? "");
  const [cfToken, setCfToken] = useState("");
  const [images, setImages] = useState(settings.images.join("\n"));
  const [port, setPort] = useState(String(settings.container_port || ""));
  const [envTemplate, setEnvTemplate] = useState(settings.env_template);
  const [nodes, setNodes] = useState<NodeDraft[]>(() => (settings.nodes ?? []).map(toNodeDraft));
  const [passwordEnv, setPasswordEnv] = useState(settings.password_env || "INITIAL_PASSWORD");
  const [memoryLimit, setMemoryLimit] = useState(settings.memory_limit ?? "");
  const [cpuLimit, setCpuLimit] = useState(settings.cpu_limit ?? "");
  const [dataPath, setDataPath] = useState(settings.data_path || "/data");
  const [supportUrl, setSupportUrl] = useState(settings.support_url || "https://t.me/damnwahhh");
  const [regions, setRegions] = useState<RegionDraft[]>(() => (settings.regions ?? []).map(toRegionDraft));
  const [step, setStep] = useState(0);

  const portNum = Number(port);
  const portError = port.trim() === "" || !Number.isInteger(portNum) || portNum < 1 || portNum > 65535 ? "Enter a port from 1 to 65535." : null;
  const imageList = images
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);
  const digits = /^[0-9]*$/;
  const memError = !digits.test(memoryLimit.trim()) ? "Enter a whole number of bytes, or leave empty." : null;
  const cpuError = !digits.test(cpuLimit.trim()) ? "Enter a whole number of nano-CPUs, or leave empty." : null;
  const dataPathError = !dataPath.trim().startsWith("/") || dataPath.includes("..") ? "Enter an absolute path inside the container, e.g. /data." : null;
  const envNameError = !/^[A-Za-z_][A-Za-z0-9_]*$/.test(passwordEnv.trim()) ? "Invalid variable name." : null;
  const supportUrlError = !/^https:\/\/[^\s/]+/.test(supportUrl.trim()) ? "Enter an https:// link, e.g. https://t.me/username." : null;
  const urlChanged = dokployUrl.trim().replace(/\/+$/, "") !== settings.dokploy_url;
  const keyNeeded = settings.has_api_key && urlChanged && dokployUrl.trim() !== "" && apiKey.trim() === "";
  const mainDomain = normalizeDomain(rootDomain);
  const rootDomainError = mainDomain !== "" && !validDomain(mainDomain) ? "Invalid domain. Example: tokenizer.ai.id." : null;
  const zoneChanged = zoneId.trim() !== (settings.cloudflare_zone_id ?? "");
  const tokenNeeded = settings.has_cloudflare_token && zoneChanged && cfToken.trim() === "";
  const nodeErrors = nodeErrorsOf(nodes);
  const regionErrors = regionErrorsOf(regions);
  const sellable = regions.some((rg) => rg.enabled && Number(rg.price) > 0 && nodes.some((n) => n.enabled && n.region === rg.id));
  const nodesError =
    enabled && !nodes.some((n) => n.enabled)
      ? "Add at least one active node before opening orders."
      : enabled && !sellable
        ? "Put at least one active node in an active region with a price before opening orders."
        : nodes.some((n) => n.region && !regions.some((rg) => rg.id === n.region))
          ? "A node is in a region that was deleted. Choose its region again."
          : null;
  const valid =
    !rootDomainError && !tokenNeeded && !portError && !memError && !cpuError && !envNameError && !dataPathError && !nodesError && !supportUrlError &&
    regionErrors.every((e) => !e.name && !e.price) &&
    nodeErrors.every((e) => !e.name && !e.ip && !e.capacity);

  const save = useMutation({
    mutationFn: (opts: { clearKey?: boolean; clearToken?: boolean }) =>
      api.putHostingSettings({
        enabled,
        dokploy_url: dokployUrl.trim(),
        ...(apiKey.trim() ? { api_key: apiKey.trim() } : {}),
        ...(opts.clearKey ? { clear_api_key: true } : {}),
        environment_id: environmentId.trim(),
        root_domain: mainDomain,
        cloudflare_zone_id: zoneId.trim(),
        ...(cfToken.trim() ? { cloudflare_token: cfToken.trim() } : {}),
        ...(opts.clearToken ? { clear_cloudflare_token: true } : {}),
        images: imageList,
        container_port: portNum,
        env_template: envTemplate,
        nodes: nodes.map(
          (n): HostingNode => ({
            id: n.id,
            name: n.name.trim(),
            server_id: n.serverId.trim(),
            public_ip: n.publicIp.trim(),
            capacity: Number(n.capacity.trim() || "0"),
            enabled: n.enabled,
            region: n.region,
          }),
        ),
        password_env: passwordEnv.trim(),
        memory_limit: memoryLimit.trim(),
        cpu_limit: cpuLimit.trim(),
        data_path: dataPath.trim(),
        support_url: supportUrl.trim(),
        regions: regions.map((rg): HostingRegion => ({ id: rg.id, name: rg.name.trim(), price_idr: Number(rg.price), enabled: rg.enabled })),
      }),
    onSuccess: (data) => {
      qc.setQueryData(QK_HOSTING.settings, data);
      qc.invalidateQueries({ queryKey: QK_HOSTING.finance });
      qc.invalidateQueries({ queryKey: QK_HOSTING.nodes });
      toast.success("Settings saved", data.enabled ? "Hosting orders are open in the portal." : "Hosting orders are closed in the portal.");
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
      title: "Delete Dokploy API key?",
      description: "Instances can't be created, stopped or deleted from the dashboard until a new API key is saved.",
      confirmLabel: "Delete key",
      cancelLabel: "Cancel",
      tone: "danger",
    });
    if (!ok) return;
    test.reset();
    setApiKey("");
    save.mutate({ clearKey: true });
  };

  const onClearToken = async () => {
    const ok = await confirm({
      title: "Delete Cloudflare API token?",
      description: "Instances can't be created or deleted until a new token is saved, because their DNS records are managed with this token.",
      confirmLabel: "Delete token",
      cancelLabel: "Cancel",
      tone: "danger",
    });
    if (!ok) return;
    setCfToken("");
    save.mutate({ clearToken: true });
  };

  const dokployBase = dokployUrl.trim().replace(/\/+$/, "");
  const hasDokployBase = /^https:\/\/[^\s/]+/.test(dokployBase);
  const anyRegionError = regionErrors.some((e) => e.name || e.price);
  const anyNodeError = nodeErrors.some((e) => e.name || e.ip || e.capacity) || !!nodesError;
  const steps: { title: string; short: string; error: boolean }[] = [
    { title: "Dokploy connection", short: "Dokploy", error: keyNeeded },
    { title: "Domain & Cloudflare", short: "Domain", error: !!rootDomainError || tokenNeeded },
    { title: "Image & container", short: "Image", error: !!(portError || memError || cpuError || envNameError || dataPathError) },
    { title: "Regions & prices", short: "Region", error: anyRegionError },
    { title: "Node", short: "Node", error: anyNodeError },
    { title: "Open orders", short: "Open", error: !!supportUrlError || (enabled && !!nodesError) },
  ];
  const last = steps.length - 1;
  const ready = [
    { ok: !!dokployBase && (settings.has_api_key || !!apiKey.trim()) && !!environmentId.trim(), label: "Dokploy URL, API key and Environment ID filled in" },
    { ok: !!mainDomain && !!zoneId.trim() && (settings.has_cloudflare_token || !!cfToken.trim()), label: "Main domain and Cloudflare filled in" },
    { ok: imageList.length > 0, label: "At least one Docker image" },
    { ok: sellable, label: "An active region with a price and at least one active node" },
  ];

  return (
    <form onSubmit={onSubmit} noValidate>
      <SettingsStepper steps={steps} step={step} onStep={setStep} />
      <div className="space-y-5 px-4 py-4">
        {step === 0 && (
          <StepIntro>Connect the dashboard to the Dokploy panel that runs customer containers. Save first, then click Test connection.</StepIntro>
        )}
        {step === 0 && (
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Dokploy URL" hint="Address of the Dokploy panel, e.g. https://dokploy.example.com. Must be https://." className="sm:col-span-2">
              <Input type="url" value={dokployUrl} onChange={(e) => setDokployUrl(e.target.value)} placeholder="https://dokploy.example.com" autoComplete="off" spellCheck={false} className="font-mono" />
            </Field>
            <Field
              label="Dokploy API key"
              hint={
                keyNeeded ? undefined : (
                  <>
                    Create one in <DokployLink base={dokployBase} ok={hasDokployBase} path="/dashboard/settings/profile">Dokploy → Profile → API keys</DokployLink>.{" "}
                    {settings.has_api_key ? "Leave empty to keep the saved key." : "No key saved yet."}
                  </>
                )
              }
              error={keyNeeded ? "The Dokploy URL changed. Enter the API key again for the new URL." : null}
            >
              <Input
                type="password"
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                placeholder={settings.has_api_key ? settings.api_key_masked : "Paste API key"}
                autoComplete="new-password"
                spellCheck={false}
                className="font-mono"
              />
            </Field>
            <Field
              label="Environment ID"
              hint={
                <>
                  Open <DokployLink base={dokployBase} ok={hasDokployBase} path="/dashboard/projects">Projects in Dokploy</DokployLink>, choose the project and
                  environment set aside for hosting, then copy the ID after <span className="font-mono">/environment/</span> in the browser address bar.
                </>
              }
            >
              <Input value={environmentId} onChange={(e) => setEnvironmentId(e.target.value)} autoComplete="off" spellCheck={false} className="font-mono" />
            </Field>
          </div>
        )}

        {step === 1 && (
          <StepIntro>
            Each instance gets a random subdomain on the main domain. When an instance is created, its DNS record is added to Cloudflare
            automatically (proxied) and points to the node IP.
          </StepIntro>
        )}
        {step === 1 && (
          <>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field
                label="Main domain"
                error={rootDomainError}
                hint={rootDomainError ? undefined : <>Instances look like <span className="font-mono">abc123xy.{mainDomain || "tokenizer.ai.id"}</span>.</>}
                className="sm:col-span-2"
              >
                <Input value={rootDomain} onChange={(e) => setRootDomain(e.target.value)} placeholder="tokenizer.ai.id" autoComplete="off" spellCheck={false} className="font-mono" />
              </Field>
              <Field
                label="Cloudflare Zone ID"
                hint={
                  <>
                    Open the <ExtLink href="https://dash.cloudflare.com/">Cloudflare dashboard</ExtLink>, choose the domain, then copy the Zone ID from the Overview page
                    (right column, API section).
                  </>
                }
              >
                <Input value={zoneId} onChange={(e) => setZoneId(e.target.value)} autoComplete="off" spellCheck={false} className="font-mono" />
              </Field>
              <Field
                label="Cloudflare API token"
                error={tokenNeeded ? "The Zone ID changed. Enter the API token again." : null}
                hint={
                  tokenNeeded ? undefined : (
                    <>
                      Create one in <ExtLink href="https://dash.cloudflare.com/profile/api-tokens">Cloudflare → API Tokens</ExtLink> with the Zone → DNS → Edit permission for
                      this domain. {settings.has_cloudflare_token ? "Leave empty to keep the saved token." : "No token saved yet."}
                    </>
                  )
                }
              >
                <Input
                  type="password"
                  value={cfToken}
                  onChange={(e) => setCfToken(e.target.value)}
                  placeholder={settings.has_cloudflare_token ? settings.cloudflare_token_masked : "Paste API token"}
                  autoComplete="new-password"
                  spellCheck={false}
                  className="font-mono"
                />
              </Field>
            </div>
            <p className="rounded-xl border border-line bg-subtle/60 px-3.5 py-2.5 text-[12.5px] text-fg-muted">
              Set this domain's SSL/TLS mode to <span className="font-medium text-fg">Full</span> in Cloudflare (SSL/TLS → Overview). Flexible causes
              a redirect loop, and Full (strict) fails until the node's certificate is issued.
            </p>
          </>
        )}

        {step === 2 && (
          <StepIntro>
            The image and limits used by each customer container. After saving, create a test instance and check that the default password{" "}
            <span className="font-mono text-fg">123456</span> is rejected.
          </StepIntro>
        )}
        {step === 2 && (
          <>
            <Field
              label="Docker image"
              hint={
                <>
                  One image per line. The first image is the default when creating an instance. See versions on{" "}
                  <ExtLink href="https://hub.docker.com/r/luqmenul/9router-go/tags">Docker Hub luqmenul/9router-go</ExtLink>. Versions below 0.4.60 are rejected
                  (CVE-2026-63732).
                </>
              }
            >
              <Textarea value={images} onChange={(e) => setImages(e.target.value)} rows={3} placeholder="luqmenul/9router-go:latest" spellCheck={false} className="font-mono text-[12.5px]" />
            </Field>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field label="Port container" hint="Passed as the PORT env. The luqmenul/9router-go image uses 20130." error={portError}>
                <Input type="number" inputMode="numeric" min="1" max="65535" value={port} onChange={(e) => setPort(e.target.value)} className="tabular-nums" />
              </Field>
              <Field
                label="Initial password env"
                hint="Each instance gets a random password in this variable. It must match what the 9router image reads."
                error={envNameError}
              >
                <Input value={passwordEnv} onChange={(e) => setPasswordEnv(e.target.value)} autoComplete="off" spellCheck={false} className="font-mono" />
              </Field>
              <Field
                label="Data path"
                hint="A Docker volume per instance is mounted here and passed as DATA_DIR, so customer data survives a redeploy."
                error={dataPathError}
              >
                <Input value={dataPath} onChange={(e) => setDataPath(e.target.value)} placeholder="/data" autoComplete="off" spellCheck={false} className="font-mono" />
              </Field>
              <Field label="Memory limit (bytes)" hint="For example 536870912 for 512 MB. Leave empty for no limit." error={memError}>
                <Input inputMode="numeric" value={memoryLimit} onChange={(e) => setMemoryLimit(e.target.value)} placeholder="536870912" className="font-mono tabular-nums" />
              </Field>
              <Field label="CPU limit (nano-CPUs)" hint="For example 1000000000 for 1 CPU. Leave empty for no limit." error={cpuError}>
                <Input inputMode="numeric" value={cpuLimit} onChange={(e) => setCpuLimit(e.target.value)} placeholder="1000000000" className="font-mono tabular-nums" />
              </Field>
            </div>
            <details className="group rounded-xl border border-line" open={envTemplate.trim() !== ""}>
              <summary className="flex cursor-pointer list-none items-center gap-2 rounded-xl px-3.5 py-2.5 text-[13px] font-medium text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 [&::-webkit-details-marker]:hidden">
                <ChevronRight className="h-4 w-4 shrink-0 text-fg-muted transition-transform group-open:rotate-90" aria-hidden="true" />
                Advanced: environment template
              </summary>
              <div className="border-t border-line px-3.5 py-3">
                <Field
                  label="Environment template"
                  hint={
                    <>
                      KEY=value format, one per line. Placeholders replaced automatically: <code className="font-mono">{"{{SUBDOMAIN}}"}</code>,{" "}
                      <code className="font-mono">{"{{FQDN}}"}</code>, <code className="font-mono">{"{{URL}}"}</code>,{" "}
                      <code className="font-mono">{"{{INSTANCE_ID}}"}</code>, <code className="font-mono">{"{{PASSWORD}}"}</code>. Set automatically and can't be
                      overridden: the initial password variable, <code className="font-mono">PORT</code> and <code className="font-mono">DATA_DIR</code>.{" "}
                      <code className="font-mono">STRICT_SSL=true</code> is added unless the template sets it. <code className="font-mono">JWT_SECRET</code>{" "}
                      is rejected because it must be unique per instance.
                    </>
                  }
                >
                  <Textarea value={envTemplate} onChange={(e) => setEnvTemplate(e.target.value)} rows={6} spellCheck={false} className="font-mono text-[12.5px]" />
                </Field>
              </div>
            </details>
          </>
        )}

        {step === 3 && <RegionsEditor regions={regions} setRegions={setRegions} nodes={nodes} setNodes={setNodes} errors={regionErrors} />}

        {step === 4 && (
          <NodesEditor
            nodes={nodes}
            setNodes={setNodes}
            regions={regions}
            errors={nodeErrors}
            listError={nodesError}
            serversLink={
              <DokployLink base={dokployBase} ok={hasDokployBase} path="/dashboard/settings/servers">
                Servers in Dokploy
              </DokployLink>
            }
          />
        )}

        {step === 5 && <StepIntro>Check the list below, fill in the activation contact, then open orders and save.</StepIntro>}
        {step === 5 && (
          <>
            <ul className="space-y-1.5 text-[13px]" aria-label="Readiness">
              {ready.map((r) => (
                <li key={r.label} className="flex items-start gap-2">
                  {r.ok ? (
                    <Check className="mt-0.5 h-4 w-4 shrink-0 text-ok" aria-hidden="true" />
                  ) : (
                    <CircleDashed className="mt-0.5 h-4 w-4 shrink-0 text-fg-faint" aria-hidden="true" />
                  )}
                  <span className={r.ok ? "text-fg" : "text-fg-muted"}>
                    <span className="sr-only">{r.ok ? "Done: " : "Not done: "}</span>
                    {r.label}
                  </span>
                </li>
              ))}
            </ul>
            <Field
              label="Activation contact link"
              hint="The “Hubungi admin” button in the portal for customers who have paid and are waiting for activation."
              error={supportUrlError}
            >
              <Input type="url" value={supportUrl} onChange={(e) => setSupportUrl(e.target.value)} placeholder="https://t.me/damnwahhh" autoComplete="off" spellCheck={false} className="font-mono" />
            </Field>
            <div className="flex items-start justify-between gap-4 rounded-xl border border-line bg-subtle/60 p-3.5">
              <div className="min-w-0">
                <p id="hosting-enabled" className="text-[13px] font-medium text-fg">
                  Open orders in the portal
                </p>
                <p className="text-[12px] text-fg-muted">When off, customers can't order or renew. Existing instances keep running.</p>
              </div>
              <Toggle checked={enabled} onChange={setEnabled} aria-labelledby="hosting-enabled" />
            </div>
            {enabled && nodesError && <ErrorBanner message={nodesError} />}
            <p className="text-[12px] text-fg-muted">
              Active terms version: <span className="font-mono">{settings.terms_version}</span>
            </p>
          </>
        )}

        {save.isError && <ErrorBanner message={`Couldn't save settings. ${errorText(save.error)}`} />}
        {step === 0 && test.isError && <ErrorBanner message={`Connection test failed. ${errorText(test.error)}`} />}
        {step === 0 &&
          test.data &&
          (test.data.ok ? (
            <p role="status" className="flex items-center gap-2 rounded-xl border border-ok/30 bg-ok/10 px-3.5 py-2.5 text-[13px] text-fg">
              <Check className="h-4 w-4 shrink-0 text-ok" aria-hidden="true" />
              Connected to Dokploy.
            </p>
          ) : (
            <ErrorBanner message={`Couldn't connect to Dokploy. ${test.data.error ?? ""}`.trim()} />
          ))}
      </div>
      <div className="flex flex-col gap-2 border-t border-line bg-subtle px-4 py-2.5 sm:flex-row sm:items-center sm:justify-end">
        {step > 0 && (
          <Button type="button" variant="ghost" className="sm:mr-auto" onClick={() => setStep(step - 1)}>
            <ArrowLeft aria-hidden="true" />
            Back
          </Button>
        )}
        {step === 0 && settings.has_api_key && (
          <Button type="button" variant="ghost" className="sm:mr-auto" onClick={onClearKey} disabled={save.isPending}>
            <Trash2 aria-hidden="true" />
            Delete API key
          </Button>
        )}
        {step === 1 && settings.has_cloudflare_token && (
          <Button type="button" variant="ghost" onClick={onClearToken} disabled={save.isPending}>
            <Trash2 aria-hidden="true" />
            Delete Cloudflare token
          </Button>
        )}
        {step === 0 && (
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              save.reset();
              test.mutate();
            }}
            disabled={test.isPending}
            title="Tests the saved settings"
          >
            {test.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <PlugZap aria-hidden="true" />}
            Test connection
          </Button>
        )}
        <Button type="submit" variant={step < last ? "ghost" : undefined} disabled={!valid || keyNeeded || save.isPending}>
          {save.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Check aria-hidden="true" />}
          Save
        </Button>
        {step < last && (
          <Button type="button" onClick={() => setStep(step + 1)}>
            Next
            <ArrowRight aria-hidden="true" />
          </Button>
        )}
      </div>
    </form>
  );
}

function StepIntro({ children }: { children: ReactNode }) {
  return <p className="text-[12.5px] text-fg-muted">{children}</p>;
}

function ExtLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-0.5 text-link hover:underline">
      {children}
      <ExternalLink className="h-3 w-3 shrink-0" aria-hidden="true" />
      <span className="sr-only"> (new tab)</span>
    </a>
  );
}

/** Link into the Dokploy panel, or plain text until a valid URL is filled in. */
function DokployLink({ base, ok, path, children }: { base: string; ok: boolean; path: string; children: ReactNode }) {
  if (!ok) return <span className="text-fg">{children} (enter the Dokploy URL first)</span>;
  return <ExtLink href={base + path}>{children}</ExtLink>;
}

function SettingsStepper({ steps, step, onStep }: { steps: { title: string; short: string; error: boolean }[]; step: number; onStep: (i: number) => void }) {
  return (
    <nav aria-label="Settings steps" className="border-b border-line px-4 py-3">
      <ol className="grid gap-1.5" style={{ gridTemplateColumns: `repeat(${steps.length}, minmax(0, 1fr))` }}>
        {steps.map((s, i) => {
          const current = i === step;
          return (
            <li key={s.title} className="min-w-0">
              <button
                type="button"
                onClick={() => onStep(i)}
                aria-current={current ? "step" : undefined}
                title={s.title}
                className={cn(
                  "flex w-full min-w-0 flex-col items-center gap-1 rounded-lg px-1 py-1.5 text-center focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 sm:flex-row sm:gap-2 sm:px-2 sm:text-left",
                  current ? "bg-subtle" : "hover:bg-subtle/60",
                )}
              >
                <span
                  className={cn(
                    "relative flex h-6 w-6 shrink-0 items-center justify-center rounded-full border text-[12px] font-semibold tabular-nums",
                    current ? "border-accent-500 bg-accent-500 text-white" : "border-line-strong text-fg-muted",
                  )}
                >
                  {i + 1}
                  {s.error && <span className="absolute -right-0.5 -top-0.5 h-2 w-2 rounded-full bg-bad ring-2 ring-surface" aria-hidden="true" />}
                </span>
                <span className={cn("truncate text-[12px] sm:text-[12.5px]", current ? "font-medium text-fg" : "text-fg-muted")}>
                  {s.short}
                  {s.error && <span className="sr-only"> (has fields to fix)</span>}
                </span>
              </button>
            </li>
          );
        })}
      </ol>
      <p className="mt-2 text-[12px] text-fg-muted" aria-hidden="true">
        Step {step + 1} of {steps.length} · {steps[step].title}
      </p>
    </nav>
  );
}

// ─── Nodes ──────────────────────────────────────────────────────────────────

interface NodeDraft {
  key: string;
  id: string;
  name: string;
  serverId: string;
  publicIp: string;
  capacity: string;
  enabled: boolean;
  region: string;
  isNew: boolean;
}

const MAX_NODE_CAPACITY = 10000;

const toNodeDraft = (n: HostingNode): NodeDraft => ({
  key: n.id,
  id: n.id,
  name: n.name,
  serverId: n.server_id ?? "",
  publicIp: n.public_ip ?? "",
  capacity: String(n.capacity ?? 0),
  enabled: n.enabled,
  region: n.region ?? "",
  isNew: false,
});

const IPV4 = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/;

function nodeErrorsOf(nodes: NodeDraft[]) {
  return nodes.map((n) => {
    const name = n.name.trim();
    const ip = n.publicIp.trim();
    const capText = n.capacity.trim();
    const cap = Number(capText || "0");
    return {
      name: !name ? "Enter a node name." : name.length > 80 ? "80 characters at most." : null,
      ip: ip === "" ? (n.enabled ? "Enter the public IP of an active node." : null) : !IPV4.test(ip) ? "Enter an IPv4 address, e.g. 203.0.113.7." : null,
      capacity:
        !/^[0-9]*$/.test(capText) || !Number.isInteger(cap) || cap > MAX_NODE_CAPACITY ? `Enter a whole number from 0 to ${MAX_NODE_CAPACITY}.` : null,
    };
  });
}

// ─── Regions ────────────────────────────────────────────────────────────────

interface RegionDraft {
  id: string;
  name: string;
  price: string;
  enabled: boolean;
}

const toRegionDraft = (r: HostingRegion): RegionDraft => ({ id: r.id, name: r.name, price: String(r.price_idr), enabled: r.enabled });

function regionErrorsOf(regions: RegionDraft[]) {
  return regions.map((r) => {
    const n = Number(r.price);
    return {
      name: !r.name.trim() ? "Enter a region name." : null,
      price: r.price.trim() === "" || !Number.isInteger(n) || n < 0 ? "Enter a whole number, 0 or more." : null,
    };
  });
}

// regionIdFor turns a country name into a stable id ("Malaysia" -> "malaysia"),
// unique among taken ids and within the 20-character limit the API allows.
function regionIdFor(name: string, taken: string[]): string {
  const base =
    name
      .normalize("NFD")
      .replace(/[̀-ͯ]/g, "")
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 20)
      .replace(/-+$/, "") || "region";
  let id = base;
  for (let n = 2; taken.includes(id); n++) id = `${base.slice(0, 20 - String(n).length - 1)}-${n}`;
  return id;
}

const MAX_REGIONS = 20;

function RegionsEditor({
  regions,
  setRegions,
  nodes,
  setNodes,
  errors,
}: {
  regions: RegionDraft[];
  setRegions: (fn: (rows: RegionDraft[]) => RegionDraft[]) => void;
  nodes: NodeDraft[];
  setNodes: (fn: (rows: NodeDraft[]) => NodeDraft[]) => void;
  errors: ReturnType<typeof regionErrorsOf>;
}) {
  const confirm = useConfirm();
  const [newName, setNewName] = useState("");
  const update = (id: string, patch: Partial<RegionDraft>) => setRegions((rs) => rs.map((r) => (r.id === id ? { ...r, ...patch } : r)));
  const trimmed = newName.trim();
  const duplicate = regions.some((r) => r.name.trim().toLowerCase() === trimmed.toLowerCase());
  const full = regions.length >= MAX_REGIONS;

  // Not a <form>: this editor already sits inside the settings form.
  const add = () => {
    if (!trimmed || duplicate || full) return;
    setRegions((rs) => [...rs, { id: regionIdFor(trimmed, rs.map((r) => r.id)), name: trimmed.slice(0, 60), price: "", enabled: true }]);
    setNewName("");
  };
  const remove = async (r: RegionDraft) => {
    const onNodes = nodes.filter((n) => n.region === r.id).length;
    const ok = await confirm({
      title: `Delete ${r.name.trim() || r.id}?`,
      description:
        onNodes > 0
          ? `${onNodes} node(s) are in this region and will have no region, so they add no stock until you pick a new one. Saving fails if instances still belong to it; turn off For sale instead.`
          : "Saving fails if instances still belong to this region; turn off For sale instead.",
      confirmLabel: "Delete",
      cancelLabel: "Cancel",
      tone: "danger",
    });
    if (!ok) return;
    setRegions((rs) => rs.filter((x) => x.id !== r.id));
    if (onNodes > 0) setNodes((ns) => ns.map((n) => (n.region === r.id ? { ...n, region: "" } : n)));
  };

  return (
    <section aria-labelledby="hosting-regions-title" className="space-y-3">
      <div>
        <p id="hosting-regions-title" className="text-[13px] font-medium text-fg">
          Region
        </p>
        <p className="text-[12px] text-fg-muted">
          The countries your servers are in, each with its own price per 30 days. Customers pick one on the hosting page and in the portal. A
          region with no active node, or with For sale off, shows as sold out.
        </p>
      </div>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        {regions.map((r, i) => {
          const err = errors[i];
          const toggleId = `hosting-region-enabled-${r.id}`;
          const priceNum = Number(r.price);
          return (
            <fieldset key={r.id} className="rounded-xl border border-line bg-subtle/60 p-3.5">
              <legend className="sr-only">{r.name || r.id}</legend>
              <div className="mb-3 flex items-center justify-between gap-2">
                <span className="font-mono text-[11px] text-fg-faint">{r.id}</span>
                <div className="flex items-center gap-2">
                  <span id={toggleId} className="text-[12px] text-fg-muted">
                    For sale
                  </span>
                  <Toggle checked={r.enabled} onChange={(v) => update(r.id, { enabled: v })} aria-labelledby={toggleId} />
                  <Button
                    type="button"
                    variant="ghost"
                    onClick={() => remove(r)}
                    disabled={regions.length <= 1}
                    aria-label={`Delete ${r.name.trim() || r.id}`}
                    title={regions.length <= 1 ? "Keep at least one region" : "Delete region"}
                  >
                    <Trash2 aria-hidden="true" />
                  </Button>
                </div>
              </div>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <Field label="Country" error={err.name}>
                  <Input value={r.name} onChange={(e) => update(r.id, { name: e.target.value })} maxLength={60} autoComplete="off" />
                </Field>
                <Field label="Price (IDR)" error={err.price} hint={!err.price && priceNum > 0 ? idr(priceNum) : undefined}>
                  <Input
                    type="number"
                    inputMode="numeric"
                    min="0"
                    step="1"
                    value={r.price}
                    onChange={(e) => update(r.id, { price: e.target.value })}
                    className="tabular-nums"
                  />
                </Field>
              </div>
            </fieldset>
          );
        })}
      </div>
      <div className="flex flex-col gap-2 sm:flex-row sm:items-start">
        <Field
          label="Add a country"
          className="flex-1"
          error={duplicate ? "That country is already in the list." : undefined}
          hint={full ? `${MAX_REGIONS} regions at most.` : "Any name works, e.g. Malaysia or Australia. Set its price after adding it."}
        >
          <Input
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                add();
              }
            }}
            placeholder="Malaysia"
            maxLength={60}
            autoComplete="off"
            disabled={full}
          />
        </Field>
        <Button type="button" variant="ghost" onClick={add} disabled={!trimmed || duplicate || full} className="sm:mt-[26px]">
          <Plus aria-hidden="true" />
          Add region
        </Button>
      </div>
    </section>
  );
}

function NodesEditor({
  nodes,
  setNodes,
  regions,
  errors,
  listError,
  serversLink,
}: {
  nodes: NodeDraft[];
  regions: RegionDraft[];
  setNodes: (fn: (rows: NodeDraft[]) => NodeDraft[]) => void;
  errors: ReturnType<typeof nodeErrorsOf>;
  listError: string | null;
  serversLink: ReactNode;
}) {
  const confirm = useConfirm();
  const loads = useQuery({ queryKey: QK_HOSTING.nodes, queryFn: api.listHostingNodes });
  const loadOf = (id: string) => loads.data?.nodes.find((n) => n.id === id);
  // Saved nodes start folded so the step reads as a list; new nodes open.
  const [openKeys, setOpenKeys] = useState<Set<string>>(() => new Set());
  const toggleOpen = (key: string) =>
    setOpenKeys((ks) => {
      const next = new Set(ks);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });

  const update = (key: string, patch: Partial<NodeDraft>) => setNodes((rs) => rs.map((r) => (r.key === key ? { ...r, ...patch } : r)));
  const add = () => {
    const id = newNodeId();
    setOpenKeys((ks) => new Set(ks).add(id));
    setNodes((rs) => {
      return [
        ...rs,
        {
          key: id, id, name: `Node ${rs.length + 1}`, serverId: "", publicIp: "", capacity: "0", enabled: true,
          region: regions[0]?.id ?? "", isNew: true,
        },
      ];
    });
  };
  const remove = async (r: NodeDraft) => {
    const used = loadOf(r.id)?.used ?? 0;
    if (used > 0) {
      const ok = await confirm({
        title: `Delete ${r.name.trim() || "node"}?`,
        description: (
          <>
            {used} instance(s) still use this node. Running instances are not deleted, but failed instances on this node can no longer be
            retried. To stop taking new instances only, turn off Active on this node instead.
          </>
        ),
        confirmLabel: "Delete anyway",
        cancelLabel: "Cancel",
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
          Dokploy servers that run customer containers. Each instance's DNS record points to its node's public IP. Add remote servers just for
          customers in {serversLink}, then copy their Server ID and IP. Containers on the main Dokploy server can reach Dokploy's own database and Redis. The region sets the stock sold on the hosting page: the free slots of active nodes in that region.
        </p>
      </div>
      {nodes.length === 0 && (
        <p className="rounded-xl border border-dashed border-line-strong px-4 py-6 text-center text-[13px] text-fg-muted">
          No nodes yet. Add at least one node so instances can be created.
        </p>
      )}
      {nodes.map((r, i) => {
        const err = errors[i];
        const load = loadOf(r.id);
        const cap = Number(r.capacity.trim() || "0");
        const full = load ? cap > 0 && load.used >= cap : false;
        const toggleId = `hosting-node-enabled-${r.key}`;
        const panelId = `hosting-node-panel-${r.key}`;
        const label = r.name.trim() || `Node ${i + 1}`;
        const hasError = !!(err.name || err.ip || err.capacity);
        // A node with an error stays open so the message is never hidden.
        const open = openKeys.has(r.key) || hasError;
        const regionName = regions.find((rg) => rg.id === r.region)?.name.trim() || r.region;
        return (
          <fieldset key={r.key} className="rounded-xl border border-line bg-subtle/60">
            <legend className="sr-only">{label}</legend>
            <div className="flex flex-wrap items-center justify-between gap-2 p-2 pl-1.5">
              <button
                type="button"
                onClick={() => toggleOpen(r.key)}
                aria-expanded={open}
                aria-controls={panelId}
                disabled={hasError}
                className="flex min-w-0 flex-1 items-center gap-2 rounded-lg px-2 py-1.5 text-left hover:bg-surface focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 disabled:cursor-default disabled:hover:bg-transparent"
              >
                <ChevronRight className={cn("h-4 w-4 shrink-0 text-fg-muted transition-transform", open && "rotate-90")} aria-hidden="true" />
                <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                  <span className="text-[12.5px] font-medium text-fg">{label}</span>
                  <span className="font-mono text-[11.5px] tabular-nums text-fg-muted">{r.publicIp.trim() || "no IP"}</span>
                  {regionName ? <span className="text-[12px] text-fg-muted">{regionName}</span> : <Badge tone="warning">No region</Badge>}
                  {r.isNew ? (
                    <Badge tone="accent">New</Badge>
                  ) : load ? (
                    <Badge tone={!r.enabled ? "neutral" : full ? "danger" : "neutral"} title="Instances on this node / capacity">
                      <span className="tabular-nums">{load.used}/{cap > 0 ? cap : "∞"}</span> instance{full && r.enabled ? " · full" : ""}
                    </Badge>
                  ) : null}
                  {!r.enabled && <Badge tone="neutral">Disabled</Badge>}
                  {hasError && <Badge tone="danger">Needs a fix</Badge>}
                </span>
              </button>
              <div className="flex items-center gap-2 pr-1.5">
                <span id={toggleId} className="text-[12px] text-fg-muted">
                  Active
                </span>
                <Toggle checked={r.enabled} onChange={(v) => update(r.key, { enabled: v })} aria-labelledby={toggleId} />
                <Button type="button" variant="ghost" onClick={() => remove(r)} aria-label={`Delete ${label}`} title="Delete node">
                  <Trash2 aria-hidden="true" />
                </Button>
              </div>
            </div>
            {open && (
              <div id={panelId} className="grid grid-cols-1 gap-3 border-t border-line p-3.5 sm:grid-cols-2 lg:grid-cols-3">
                <Field label="Name" error={err.name}>
                  <Input value={r.name} onChange={(e) => update(r.key, { name: e.target.value })} placeholder="Node 1" maxLength={80} autoComplete="off" />
                </Field>
                <Field label="Public IP" error={err.ip} hint={err.ip ? undefined : <>IPv4 of the node server. Find it in {serversLink}.</>}>
                  <Input
                    inputMode="decimal"
                    value={r.publicIp}
                    onChange={(e) => update(r.key, { publicIp: e.target.value })}
                    placeholder="203.0.113.7"
                    autoComplete="off"
                    spellCheck={false}
                    className="font-mono tabular-nums"
                  />
                </Field>
                <Field label="Dokploy Server ID" hint={<>Copy it from {serversLink}. Leave empty for the main Dokploy server (not recommended).</>}>
                  <Input value={r.serverId} onChange={(e) => update(r.key, { serverId: e.target.value })} autoComplete="off" spellCheck={false} className="font-mono" />
                </Field>
                <Field
                  label="Region"
                  hint={r.region ? "Missing a country? Add it in the Region step." : "A node without a region adds no stock for sale. Add countries in the Region step."}
                >
                  <Select value={r.region} onChange={(e) => update(r.key, { region: e.target.value })}>
                    <option value="">No region (not for sale)</option>
                    {regions.map((rg) => (
                      <option key={rg.id} value={rg.id}>
                        {rg.name.trim() || rg.id}
                      </option>
                    ))}
                  </Select>
                </Field>
                <Field
                  label="Container capacity"
                  hint={err.capacity ? undefined : "Slots sold on this node. 0 = no limit. Overcommit can go past it when creating an instance."}
                  error={err.capacity}
                >
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
            )}
          </fieldset>
        );
      })}
      {listError && <ErrorBanner message={listError} />}
      <Button type="button" variant="ghost" onClick={add}>
        <Plus aria-hidden="true" />
        Add node
      </Button>
    </section>
  );
}
