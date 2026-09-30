import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Gift, Plus, RotateCw, Eye, EyeOff } from "lucide-react";
import { api, type Bansos } from "../lib/api";
import { PageHeader } from "../components/Layout";
import { ModelMultiSelect } from "../components/ModelSelect";
import { useToast } from "../components/Toast";
import { Card, Button, Input, Field, Badge, Spinner, ErrorBanner, Toggle } from "../components/ui";

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
      toast.success("Bansos dibuat");
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
      toast.success("Kredit ditambahkan");
      setTopupUSD("");
      invalidate();
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const rotate = useMutation({
    mutationFn: () => api.rotateBansos(),
    onSuccess: (res) => {
      setRotateKey(res.key);
      toast.success("Key dirotasi");
      invalidate();
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const data: Bansos | undefined = state.data;

  if (state.isLoading) return <div className="p-6"><Spinner /></div>;
  if (state.error) return <div className="p-6"><ErrorBanner message={(state.error as Error).message} /></div>;

  const notConfigured = !data?.exists;

  return (
    <div className="p-6">
      <PageHeader title="Bansos" description="Legal API key untuk dibagikan secara publik" icon={Gift} />

      {createdKey && (
        <Card className="mb-4 border-[var(--green)]">
          <p className="text-sm font-[650] text-[var(--ink)]">Simpan key ini sekarang — hanya ditampilkan sekali.</p>
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
        <Card className="mb-4 border-[var(--green)]">
          <p className="text-sm font-[650] text-[var(--ink)]">Key baru (key lama langsung tidak berlaku):</p>
          <code className="mt-2 block break-all rounded-lg border border-[var(--line)] bg-[var(--soft)] px-3 py-2 font-mono text-sm">{rotateKey}</code>
        </Card>
      )}

      {notConfigured ? (
        <Card>
          <h2 className="text-lg font-[650] text-[var(--ink)]">Buat bansos</h2>
          <div className="mt-4 grid gap-4 md:grid-cols-2">
            <Field label="Mode limit">
              <select
                className="w-full rounded-lg border border-[var(--line)] bg-[var(--paper)] px-3 py-2 text-sm"
                value={mode}
                onChange={(e) => setMode(e.target.value as "credit" | "unlimited")}
              >
                <option value="unlimited">Unlimited</option>
                <option value="credit">Credit</option>
              </select>
            </Field>
            {mode === "credit" && (
              <Field label="Kredit awal (USD)">
                <Input value={creditUSD} onChange={(e) => setCreditUSD(e.target.value)} inputMode="decimal" placeholder="10" />
              </Field>
            )}
            <Field label="RPM (opsional)">
              <Input value={rpm} onChange={(e) => setRpm(e.target.value)} inputMode="numeric" placeholder="60" />
            </Field>
            <Field label="TPM (opsional)">
              <Input value={tpm} onChange={(e) => setTpm(e.target.value)} inputMode="numeric" placeholder="200000" />
            </Field>
          </div>
          <div className="mt-4">
            <Field label="Model yang diizinkan (minimal 1)">
              <ModelMultiSelect value={models} onChange={setModels} />
            </Field>
          </div>
          <div className="mt-4">
            <Button onClick={() => create.mutate()} disabled={models.length === 0 || create.isPending}>
              <Plus className="h-4 w-4" /> Buat bansos
            </Button>
          </div>
        </Card>
      ) : (
        <div className="grid gap-4">
          <Card>
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-[var(--muted)]">Status</p>
                <Badge tone={data.active ? "success" : "secondary"}>{data.active ? "Aktif" : "Nonaktif"}</Badge>
              </div>
              <Toggle checked={data.active} onChange={(v) => update.mutate({ active: v })} />
            </div>
            <div className="mt-4 flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate rounded-lg border border-[var(--line)] bg-[var(--soft)] px-3 py-2 font-mono text-sm">
                {data.masked_display}
              </code>
              <Button variant="secondary" onClick={() => rotate.mutate()} disabled={rotate.isPending}>
                <RotateCw className="h-4 w-4" /> Rotate
              </Button>
            </div>
          </Card>

          <Card>
            <h2 className="text-lg font-[650] text-[var(--ink)]">Limit</h2>
            <div className="mt-3 grid gap-4 md:grid-cols-2">
              <Field label="Mode">
                <select
                  className="w-full rounded-lg border border-[var(--line)] bg-[var(--paper)] px-3 py-2 text-sm"
                  value={data.mode}
                  onChange={(e) => update.mutate({ mode: e.target.value as "credit" | "unlimited" })}
                >
                  <option value="unlimited">Unlimited</option>
                  <option value="credit">Credit</option>
                </select>
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
              <div className="mt-4 rounded-lg border border-[var(--line)] p-4">
                <p className="text-sm text-[var(--muted)]">
                  Limit: ${data.credit?.limit_usd ?? 0} · Terpakai: ${data.credit?.spent_usd.toFixed(4) ?? 0} · Sisa: ${data.credit?.remaining_usd.toFixed(4) ?? 0}
                </p>
                <div className="mt-2 flex items-end gap-2">
                  <Field label="Tambah kredit (USD)">
                    <Input value={topupUSD} onChange={(e) => setTopupUSD(e.target.value)} inputMode="decimal" placeholder="5" />
                  </Field>
                  <Button onClick={() => topup.mutate()} disabled={!topupUSD || topup.isPending}>Top up</Button>
                </div>
              </div>
            )}
          </Card>

          <Card>
            <h2 className="text-lg font-[650] text-[var(--ink)]">Model yang diizinkan</h2>
            <div className="mt-3">
              <ModelMultiSelect value={data.allowed_models} onChange={(v) => update.mutate({ allowed_models: v })} />
            </div>
            <div className="mt-3 flex flex-wrap gap-2">
              {data.allowed_models.map((m) => (
                <span key={m} className="rounded-lg border border-[var(--line)] bg-[var(--soft)] px-2.5 py-1 font-mono text-xs">{m}</span>
              ))}
            </div>
          </Card>
        </div>
      )}
    </div>
  );
}
