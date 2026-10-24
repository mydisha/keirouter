import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Gift, Plus, RotateCw, Eye, EyeOff } from "lucide-react";
import { api, type Bansos } from "../lib/api";
import { PageHeader } from "../components/Layout";
import { ModelMultiSelect } from "../components/ModelSelect";
import { useToast } from "../components/Toast";
import { Card, CardHeader, Button, Input, Field, Badge, Spinner, ErrorBanner, Toggle, Select, Modal } from "../components/ui";

function uuid() {
  return crypto.randomUUID();
}

export function BansosPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const state = useQuery({ queryKey: ["bansos"], queryFn: () => api.getBansos(), retry: false });
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [showCreated, setShowCreated] = useState(false);
  const [rotateKey, setRotateKey] = useState<string | null>(null);

  const [mode, setMode] = useState<"credit" | "unlimited">("unlimited");
  const [models, setModels] = useState<string[]>([]);
  const [rpm, setRpm] = useState("");
  const [tpm, setTpm] = useState("");
  const [creditUSD, setCreditUSD] = useState("");
  const [topupUSD, setTopupUSD] = useState("");
  const [creditModalOpen, setCreditModalOpen] = useState(false);
  const [creditModalUSD, setCreditModalUSD] = useState("");

  const invalidate = () => qc.invalidateQueries({ queryKey: ["bansos"] });

  const create = useMutation({
    mutationFn: () =>
      api.createBansos({
        mode,
        allowed_models: models,
        rpm: rpm ? Number(rpm) : 0,
        tpm: tpm ? Number(tpm) : 0,
        credit_limit_usd: mode === "credit" && creditUSD ? Number(creditUSD) : undefined,
      }),
    onSuccess: (res) => {
      setCreatedKey(res.key ?? null);
      toast.success("Bansos created");
      invalidate();
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const update = useMutation({
    mutationFn: (patch: Parameters<typeof api.updateBansos>[0]) => api.updateBansos(patch),
    onSuccess: () => invalidate(),
    onError: (e: Error) => toast.error(e.message),
  });

  const topup = useMutation({
    mutationFn: () => api.topupBansos({ amount_usd: Number(topupUSD), idempotency_key: uuid() }),
    onSuccess: () => {
      toast.success("Credit added");
      setTopupUSD("");
      invalidate();
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const rotate = useMutation({
    mutationFn: () => api.rotateBansos(),
    onSuccess: (res) => {
      setRotateKey(res.key);
      toast.success("Key rotated");
      invalidate();
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const data: Bansos | undefined = state.data;

  if (state.isLoading) return <div className="p-6"><Spinner /></div>;
  if (state.error) return <div className="p-6"><ErrorBanner message={(state.error as Error).message} /></div>;

  const notConfigured = !data?.exists;

  return (
    <div>
      <PageHeader title="Bansos" description="An API key meant to be shared publicly" icon={Gift} />

      {createdKey && (
        <Card className="mb-4 border-[var(--green)] p-5 sm:p-6">
          <p className="text-sm font-semibold text-[var(--ink)]">Copy this key now. It won't be shown again.</p>
          <div className="mt-2 flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate rounded-lg border border-[var(--line)] bg-[var(--soft)] px-3 py-2 font-mono text-sm">
              {showCreated ? createdKey : "•".repeat(24)}
            </code>
            <Button variant="secondary" onClick={() => setShowCreated((v) => !v)}>
              {showCreated ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
            </Button>
            <Button variant="secondary" onClick={() => navigator.clipboard.writeText(createdKey)}>Copy</Button>
          </div>
        </Card>
      )}

      {rotateKey && (
        <Card className="mb-4 border-[var(--green)] p-5 sm:p-6">
          <p className="text-sm font-semibold text-[var(--ink)]">New key (the old key stops working right away):</p>
          <code className="mt-3 block break-all rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] px-3 py-2 font-mono text-sm">{rotateKey}</code>
        </Card>
      )}

      {notConfigured ? (
        <Card>
          <CardHeader title="Create Bansos" description="Create one public API key that anyone can share." />
          <div className="p-5 sm:p-6">
            <div className="grid gap-4 md:grid-cols-2">
              <Field label="Mode limit">
                <Select
                  value={mode}
                  onChange={(e) => setMode(e.target.value as "credit" | "unlimited")}
                >
                  <option value="unlimited">Unlimited</option>
                  <option value="credit">Credit</option>
                </Select>
              </Field>
              {mode === "credit" && (
                <Field label="Starting credit (USD)">
                  <Input value={creditUSD} onChange={(e) => setCreditUSD(e.target.value)} inputMode="decimal" placeholder="10" />
                </Field>
              )}
              <Field label="RPM (optional)">
                <Input value={rpm} onChange={(e) => setRpm(e.target.value)} inputMode="numeric" placeholder="60" />
              </Field>
              <Field label="TPM (optional)">
                <Input value={tpm} onChange={(e) => setTpm(e.target.value)} inputMode="numeric" placeholder="200000" />
              </Field>
            </div>
            <div className="mt-4">
              <Field label="Allowed models (at least 1)">
                <ModelMultiSelect value={models} onChange={setModels} />
              </Field>
            </div>
            <div className="mt-5 flex justify-end">
              <Button onClick={() => create.mutate()} disabled={models.length === 0 || create.isPending}>
                <Plus className="h-4 w-4" /> Create Bansos
              </Button>
            </div>
          </div>
        </Card>
      ) : (
        <div className="grid gap-4">
          <Card className="p-5 sm:p-6">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-[var(--muted)]">Status</p>
                <Badge tone={data.active ? "success" : "secondary"}>{data.active ? "Active" : "Disabled"}</Badge>
              </div>
              <Toggle checked={data.active} onChange={(v) => update.mutate({ active: v })} />
            </div>
            <div className="mt-4 flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] px-3 py-2 font-mono text-sm">
                {data.masked_display}
              </code>
              <Button variant="secondary" onClick={() => rotate.mutate()} disabled={rotate.isPending}>
                <RotateCw className="h-4 w-4" /> Rotate
              </Button>
            </div>
          </Card>

          <Card>
            <CardHeader title="Limit" />
            <div className="p-5 sm:p-6">
              <div className="grid gap-4 md:grid-cols-2">
                <Field label="Mode">
                  <Select
                    value={data.mode}
                    onChange={(e) => {
                      const next = e.target.value as "credit" | "unlimited";
                      if (next === "credit" && !(data.credit && data.credit.limit_usd > 0)) {
                        // Credit with no positive limit fails closed on the server,
                        // so collect the starting amount in a themed dialog.
                        setCreditModalUSD("");
                        setCreditModalOpen(true);
                        return;
                      }
                      update.mutate({ mode: next });
                    }}
                  >
                    <option value="unlimited">Unlimited</option>
                    <option value="credit">Credit</option>
                  </Select>
                </Field>
                <Field label="RPM">
                  <Input
                    defaultValue={String(data.rpm || "")}
                    onBlur={(e) => update.mutate({ rpm: Number(e.target.value) || 0 })}
                    inputMode="numeric"
                  />
                </Field>
                <Field label="TPM">
                  <Input
                    defaultValue={String(data.tpm || "")}
                    onBlur={(e) => update.mutate({ tpm: Number(e.target.value) || 0 })}
                    inputMode="numeric"
                  />
                </Field>
              </div>
              {data.mode === "credit" && (
                <div className="mt-4 rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4">
                  <p className="text-sm text-[var(--muted)]">
                    Limit: ${data.credit?.limit_usd ?? 0} · Spent: ${data.credit?.spent_usd.toFixed(4) ?? 0} · Left: ${data.credit?.remaining_usd.toFixed(4) ?? 0}
                  </p>
                  <div className="mt-3 flex items-end gap-2">
                    <Field label="Add credit (USD)">
                      <Input value={topupUSD} onChange={(e) => setTopupUSD(e.target.value)} inputMode="decimal" placeholder="5" />
                    </Field>
                    <Button onClick={() => topup.mutate()} disabled={!topupUSD || topup.isPending}>Top up</Button>
                  </div>
                </div>
              )}
            </div>
          </Card>

          <Card>
            <CardHeader title="Allowed models" />
            <div className="p-5 sm:p-6">
              <ModelMultiSelect value={data.allowed_models} onChange={(v) => update.mutate({ allowed_models: v })} />
              <div className="mt-3 flex flex-wrap gap-2">
                {data.allowed_models.map((m) => (
                  <span key={m} className="rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] px-2.5 py-1 font-mono text-xs">{m}</span>
                ))}
              </div>
            </div>
          </Card>

          <Card>
            <CardHeader
              title="Resale warning"
              description="Randomly adds a warning line to output, only for this Bansos key. It doesn't change generated text or code, and never appears for regular user keys."
            />
            <div className="p-5 sm:p-6">
              <div className="flex items-center justify-between">
                <div>
                  <p className="text-sm text-[var(--muted)]">Warning status</p>
                  <Badge tone={data.notice_enabled ? "success" : "secondary"}>
                    {data.notice_enabled ? "Active" : "Disabled"}
                  </Badge>
                </div>
                <Toggle
                  checked={!!data.notice_enabled}
                  onChange={(v) => update.mutate({ notice_enabled: v })}
                />
              </div>
              <div className="mt-4 grid gap-4 md:grid-cols-[1fr_140px]">
                <Field label="Warning text (leave empty to use the default)">
                  <Input
                    defaultValue={data.notice_text ?? ""}
                    placeholder="ini bansos dari tokenizer.id — kalau kamu beli token ini kamu ditipu"
                    onBlur={(e) => update.mutate({ notice_text: e.target.value })}
                  />
                </Field>
                <Field label="Chance to appear (%, 0 turns it off)">
                  <Input
                    defaultValue={String(data.notice_rate ?? 30)}
                    inputMode="numeric"
                    onBlur={(e) => {
                      const n = Number(e.target.value);
                      if (!Number.isFinite(n) || n < 0 || n > 100) {
                        toast.error("Chance must be between 0 and 100");
                        return;
                      }
                      update.mutate({ notice_rate: n });
                    }}
                  />
                </Field>
              </div>
            </div>
          </Card>
        </div>
      )}

      <Modal
        open={creditModalOpen}
        onClose={() => setCreditModalOpen(false)}
        title="Turn on credit mode"
        subtitle="Enter starting credit (USD) to cap usage."
      >
        <div className="space-y-4 px-6 py-5">
          <Field label="Starting credit (USD)">
            <Input
              value={creditModalUSD}
              onChange={(e) => setCreditModalUSD(e.target.value)}
              inputMode="decimal"
              placeholder="10"
              data-modal-autofocus
            />
          </Field>
        </div>
        <div className="flex justify-end gap-2 border-t border-[var(--border)] px-6 py-4">
          <Button variant="secondary" onClick={() => setCreditModalOpen(false)}>Cancel</Button>
          <Button
            onClick={() => {
              const amount = Number(creditModalUSD);
              if (!Number.isFinite(amount) || amount <= 0) {
                toast.error("Credit must be more than 0");
                return;
              }
              update.mutate(
                { mode: "credit", credit_limit_usd: amount },
                { onSuccess: () => setCreditModalOpen(false) },
              );
            }}
            disabled={update.isPending}
          >
            Save
          </Button>
        </div>
      </Modal>
    </div>
  );
}
