import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Users, KeyRound, RefreshCw, Power, Trash2, Copy, Check, ShieldCheck } from "lucide-react";
import { api, type Plan, type PortalUserRecord } from "../lib/api";
import { microsToUSD, formatTokens } from "../lib/format";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { Card, Button, Select, Badge, Spinner, ErrorBanner, EmptyState } from "../components/ui";

export function PortalUsersPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const [revealed, setRevealed] = useState<Record<string, string>>({});

  const usersQuery = useQuery({
    queryKey: ["portal-users"],
    queryFn: api.listPortalUsers,
  });
  const plansQuery = useQuery({
    queryKey: ["plans"],
    queryFn: api.listPlans,
  });
  const settingsQuery = useQuery({
    queryKey: ["portal-settings"],
    queryFn: api.getPortalSettings,
  });

  const plans: Plan[] = plansQuery.data?.plans ?? [];
  const defaultPlanId = settingsQuery.data?.default_plan_id ?? usersQuery.data?.default_plan_id ?? "";

  const invalidate = () => qc.invalidateQueries({ queryKey: ["portal-users"] });

  const settingsMutation = useMutation({
    mutationFn: (planId: string) => api.updatePortalSettings(planId),
    onSuccess: (_d, planId) => {
      toast.success("Portal provisioning updated", planId ? `New users get "${planName(plans, planId)}".` : "Self-provisioning disabled.");
      qc.invalidateQueries({ queryKey: ["portal-settings"] });
      invalidate();
    },
    onError: (e: Error) => toast.error("Update failed", e.message),
  });

  const planMutation = useMutation({
    mutationFn: ({ sub, planId }: { sub: string; planId: string }) => api.setPortalUserPlan(sub, planId),
    onSuccess: () => { toast.success("Plan updated", "The key's budget and models were re-synced."); invalidate(); },
    onError: (e: Error) => toast.error("Plan change failed", e.message),
  });

  const toggleMutation = useMutation({
    mutationFn: ({ sub, disabled }: { sub: string; disabled: boolean }) => api.togglePortalUserKey(sub, disabled),
    onSuccess: (_d, v) => { toast.success(v.disabled ? "Key disabled" : "Key enabled"); invalidate(); },
    onError: (e: Error) => toast.error("Update failed", e.message),
  });

  const rotateMutation = useMutation({
    mutationFn: (sub: string) => api.rotatePortalUserKey(sub),
    onSuccess: (d, sub) => { setRevealed((r) => ({ ...r, [sub]: d.key })); toast.success("Key rotated", "Copy the new key now — it is shown once."); invalidate(); },
    onError: (e: Error) => toast.error("Rotate failed", e.message),
  });

  const deleteMutation = useMutation({
    mutationFn: (sub: string) => api.deletePortalUser(sub),
    onSuccess: () => { toast.success("Portal user revoked", "The binding and its key were deleted."); invalidate(); },
    onError: (e: Error) => toast.error("Delete failed", e.message),
  });

  const users = usersQuery.data?.users ?? [];

  return (
    <div className="space-y-6">
      <PageHeader
        title="Portal Users"
        description="Google SSO accounts that self-provisioned an API key from the default plan."
      />

      {/* Default plan for self-provisioning */}
      <Card className="p-5">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-start gap-3">
            <div className="mt-0.5 flex h-9 w-9 items-center justify-center rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] text-[var(--text-muted)]">
              <ShieldCheck size={17} />
            </div>
            <div>
              <p className="text-sm font-semibold text-[var(--text)]">Default plan for new portal users</p>
              <p className="mt-0.5 text-sm text-[var(--text-muted)]">
                Applied when a user clicks “Create API key”. Set to “Disabled” to turn off self-provisioning.
              </p>
            </div>
          </div>
          <Select
            value={defaultPlanId}
            className="sm:w-64"
            disabled={settingsMutation.isPending}
            onChange={(e) => settingsMutation.mutate(e.target.value)}
          >
            <option value="">Disabled</option>
            {plans.map((p) => (
              <option key={p.id} value={p.id}>{p.name}</option>
            ))}
          </Select>
        </div>
      </Card>

      {usersQuery.isError && <ErrorBanner message={(usersQuery.error as Error).message} />}

      <Card className="overflow-hidden">
        {usersQuery.isLoading ? (
          <div className="flex items-center justify-center py-16"><Spinner /></div>
        ) : users.length === 0 ? (
          <EmptyState title="No portal users yet" hint="Users appear here after they sign in with Google and create an API key." />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="border-b border-[var(--border)] bg-[var(--bg-subtle)]/50">
                <tr className="text-[11px] uppercase tracking-wide text-[var(--text-muted)]">
                  <th className="px-5 py-3 text-left font-semibold">User</th>
                  <th className="px-5 py-3 text-left font-semibold">Key</th>
                  <th className="px-5 py-3 text-left font-semibold">Plan</th>
                  <th className="px-5 py-3 text-left font-semibold">Budget</th>
                  <th className="px-5 py-3 text-right font-semibold">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[var(--border)]">
                {users.map((u) => (
                  <UserRow
                    key={u.google_sub}
                    u={u}
                    plans={plans}
                    revealedKey={revealed[u.google_sub]}
                    busy={planMutation.isPending || toggleMutation.isPending || rotateMutation.isPending || deleteMutation.isPending}
                    onChangePlan={(planId) => planMutation.mutate({ sub: u.google_sub, planId })}
                    onToggle={(disabled) => toggleMutation.mutate({ sub: u.google_sub, disabled })}
                    onRotate={() => rotateMutation.mutate(u.google_sub)}
                    onDelete={() => {
                      if (confirm(`Revoke portal access for ${u.email}? Their API key will be deleted.`)) {
                        deleteMutation.mutate(u.google_sub);
                      }
                    }}
                  />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  );
}

function UserRow({
  u, plans, revealedKey, busy, onChangePlan, onToggle, onRotate, onDelete,
}: {
  u: PortalUserRecord;
  plans: Plan[];
  revealedKey?: string;
  busy: boolean;
  onChangePlan: (planId: string) => void;
  onToggle: (disabled: boolean) => void;
  onRotate: () => void;
  onDelete: () => void;
}) {
  return (
    <tr className="align-top transition-colors hover:bg-[var(--bg-subtle)]/30">
      <td className="px-5 py-4">
        <div className="flex items-center gap-3">
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full border border-[var(--border)] bg-[var(--bg-subtle)] text-[var(--text-muted)]">
            <Users size={16} />
          </div>
          <div className="min-w-0">
            <p className="truncate font-medium text-[var(--text)]">{u.email || "—"}</p>
            <p className="truncate font-mono text-[11px] text-[var(--text-muted)]">{u.google_sub}</p>
          </div>
        </div>
      </td>
      <td className="px-5 py-4">
        <div className="flex flex-col gap-1.5">
          <div className="flex items-center gap-2">
            <span className="font-mono text-[13px] text-[var(--text)]">{u.display || u.key_id}</span>
            {u.disabled ? <Badge tone="danger">Disabled</Badge> : <Badge tone="success">Active</Badge>}
          </div>
          {revealedKey && <RevealedKey value={revealedKey} />}
        </div>
      </td>
      <td className="px-5 py-4">
        <Select
          value={u.plan_id ?? ""}
          disabled={busy}
          className="w-40"
          onChange={(e) => onChangePlan(e.target.value)}
        >
          <option value="">No plan</option>
          {plans.map((p) => (
            <option key={p.id} value={p.id}>{p.name}</option>
          ))}
        </Select>
      </td>
      <td className="px-5 py-4 text-[var(--text-muted)]">
        {u.budget ? (
          <div className="space-y-0.5 text-[13px]">
            {u.budget.limit_micros > 0 && <div>{microsToUSD(u.budget.limit_micros)} / {u.budget.period}</div>}
            {u.budget.limit_tokens > 0 && <div>{formatTokens(u.budget.limit_tokens)} tokens / {u.budget.period}</div>}
          </div>
        ) : (
          <span className="text-[var(--text-muted)]">Unlimited</span>
        )}
      </td>
      <td className="px-5 py-4">
        <div className="flex items-center justify-end gap-1.5">
          <Button variant="ghost" disabled={busy} onClick={onRotate} title="Rotate key">
            <RefreshCw size={15} />
          </Button>
          <Button variant="ghost" disabled={busy} onClick={() => onToggle(!u.disabled)} title={u.disabled ? "Enable key" : "Disable key"}>
            <Power size={15} className={u.disabled ? "text-[var(--text-muted)]" : "text-[color:var(--color-danger)]"} />
          </Button>
          <Button variant="ghost" disabled={busy} onClick={onDelete} title="Revoke user">
            <Trash2 size={15} className="text-[color:var(--color-danger)]" />
          </Button>
        </div>
      </td>
    </tr>
  );
}

function RevealedKey({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable
    }
  };
  return (
    <div className="flex items-center gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)]/60 px-2.5 py-1.5">
      <KeyRound size={13} className="text-[var(--text-muted)]" />
      <code className="max-w-[220px] truncate font-mono text-[12px] text-[var(--text)]">{value}</code>
      <button onClick={copy} className="text-[var(--text-muted)] hover:text-[var(--text)]" title="Copy">
        {copied ? <Check size={14} /> : <Copy size={14} />}
      </button>
    </div>
  );
}

function planName(plans: Plan[], id: string): string {
  return plans.find((p) => p.id === id)?.name ?? id;
}
