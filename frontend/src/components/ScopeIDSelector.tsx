import { useCallback, useId, useMemo, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { useQuery, useQueries } from "@tanstack/react-query";
import { Globe, Info, type LucideIcon } from "lucide-react";
import { api, type GuardrailScope } from "../lib/api";
import { ICONS } from "../lib/icons";
import { Select, Skeleton } from "./ui";

// SCOPE_ICONS gives each guardrail scope one glyph, matching the sidebar icon
// of the thing it targets, so scope chips, badges and pickers read the same.
export const SCOPE_ICONS: Record<GuardrailScope, LucideIcon> = {
  global: Globe,
  provider: ICONS.providers,
  model: ICONS.model,
  chain: ICONS.chains,
  apikey: ICONS.keys,
};

interface Props {
  scope: GuardrailScope;
  value: string;
  onChange: (id: string) => void;
}

// ScopeIDSelector renders a dropdown appropriate to the policy scope so users
// pick from known providers / models / chains / API keys instead of typing
// raw identifiers. For models we fan out one query per provider and flatten
// the results into a single grouped dropdown.
export function ScopeIDSelector({ scope, value, onChange }: Props) {
  if (scope === "global") return null;

  switch (scope) {
    case "provider":
      return <ProviderSelector value={value} onChange={onChange} />;
    case "model":
      return <ModelSelector value={value} onChange={onChange} />;
    case "chain":
      return <ChainSelector value={value} onChange={onChange} />;
    case "apikey":
      return <APIKeySelector value={value} onChange={onChange} />;
  }
}

// useScopeTargetLabels resolves a policy's scope_id into a readable name using
// the same cached queries the selectors use. Queries only run for scopes that
// are actually present, so a page with only global policies fetches nothing.
export function useScopeTargetLabels(scopes: Iterable<GuardrailScope>) {
  const present = new Set(scopes);
  const providers = useQuery({
    queryKey: ["providers"],
    queryFn: () => api.providers(),
    staleTime: 60_000,
    enabled: present.has("provider"),
  });
  const chains = useQuery({
    queryKey: ["chains"],
    queryFn: () => api.listChains(),
    staleTime: 30_000,
    enabled: present.has("chain"),
  });
  const keys = useQuery({
    queryKey: ["keys"],
    queryFn: () => api.listKeys(),
    staleTime: 30_000,
    enabled: present.has("apikey"),
  });

  return useCallback(
    (scope: GuardrailScope, id: string): string | undefined => {
      if (!id) return undefined;
      switch (scope) {
        case "provider":
          return providers.data?.providers.find((p) => p.id === id)?.display_name;
        case "chain":
          return chains.data?.chains.find((c) => c.id === id)?.name;
        case "apikey":
          return keys.data?.keys.find((k) => k.id === id)?.name;
        default:
          return undefined;
      }
    },
    [providers.data, chains.data, keys.data],
  );
}

// ── Field chrome (matches ConnectKit / Keys form fields) ─────────────────────

type FieldIds = { id: string; "aria-describedby"?: string };

// ScopeField wires the visible label and one-line hint to the control.
function ScopeField({
  label,
  hint,
  loading,
  children,
}: {
  label: string;
  hint?: ReactNode;
  loading?: boolean;
  children: (ids: FieldIds) => ReactNode;
}) {
  const id = useId();
  const hintId = `${id}-hint`;
  return (
    <div className="space-y-1.5" aria-busy={loading || undefined}>
      <label htmlFor={id} className="block text-[12.5px] font-medium text-fg">
        {label}
      </label>
      {loading ? <Skeleton className="h-9 w-full rounded-lg" /> : children({ id, "aria-describedby": hint ? hintId : undefined })}
      {hint && (
        <p id={hintId} className="text-[12px] text-fg-muted">
          {hint}
        </p>
      )}
    </div>
  );
}

function EmptyNotice({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-9 items-center gap-2 rounded-lg border border-dashed border-line-strong bg-subtle px-3 py-2 text-[12.5px] text-fg-muted">
      <Info className="h-4 w-4 shrink-0 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
      <span>{children}</span>
    </div>
  );
}

const inlineLink = "font-medium text-link hover:underline";

function ProviderSelector({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const q = useQuery({
    queryKey: ["providers"],
    queryFn: () => api.providers(),
    staleTime: 60_000,
  });
  const options = q.data?.providers ?? [];
  return (
    <ScopeField label="Provider" loading={q.isLoading}>
      {(ids) => (
        <Select {...ids} required value={value} onChange={(e) => onChange(e.target.value)}>
          <option value="">Select a provider</option>
          {options.map((p) => (
            <option key={p.id} value={p.id}>
              {p.display_name} ({p.id})
            </option>
          ))}
        </Select>
      )}
    </ScopeField>
  );
}

function ChainSelector({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const q = useQuery({
    queryKey: ["chains"],
    queryFn: () => api.listChains(),
    staleTime: 30_000,
  });
  const options = q.data?.chains ?? [];
  return (
    <ScopeField label="Chain" loading={q.isLoading}>
      {(ids) => options.length === 0 ? (
        <EmptyNotice>
          No chains yet. <Link to="/chains" className={inlineLink}>Create a chain</Link> first.
        </EmptyNotice>
      ) : (
        <Select {...ids} required value={value} onChange={(e) => onChange(e.target.value)}>
          <option value="">Select a chain</option>
          {options.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </Select>
      )}
    </ScopeField>
  );
}

function APIKeySelector({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const q = useQuery({
    queryKey: ["keys"],
    queryFn: () => api.listKeys(),
    staleTime: 30_000,
  });
  const options = q.data?.keys ?? [];
  return (
    <ScopeField label="API key" loading={q.isLoading}>
      {(ids) => options.length === 0 ? (
        <EmptyNotice>
          No API keys yet. <Link to="/keys" className={inlineLink}>Create a key</Link> first.
        </EmptyNotice>
      ) : (
        <Select {...ids} required value={value} onChange={(e) => onChange(e.target.value)}>
          <option value="">Select an API key</option>
          {options.map((k) => (
            <option key={k.id} value={k.id}>
              {k.name} — {k.display}
            </option>
          ))}
        </Select>
      )}
    </ScopeField>
  );
}

// ModelSelector fans out per-provider queries (cheap, cached, 1 min staleTime)
// and flattens results into a single grouped <optgroup>-style dropdown. The
// scope_id we save is just the model id — that's what the engine compares
// against req.Model at request time.
function ModelSelector({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const providers = useQuery({
    queryKey: ["providers"],
    queryFn: () => api.providers(),
    staleTime: 60_000,
  });
  const provs = providers.data?.providers ?? [];
  const queries = useQueries({
    queries: provs.map((p) => ({
      queryKey: ["provider-models", p.id],
      queryFn: () => api.providerModels(p.id),
      enabled: !!p.id,
      staleTime: 60_000,
    })),
  });

  const grouped = useMemo(() => {
    return provs.map((p, i) => ({
      provider: p,
      models: queries[i]?.data?.models ?? [],
    }));
  }, [provs, queries]);

  const loading = providers.isLoading || queries.some((q) => q.isLoading);

  return (
    <ScopeField label="Model" hint="Matches this model id on every provider" loading={loading}>
      {(ids) => (
        <Select {...ids} required value={value} onChange={(e) => onChange(e.target.value)} className="font-mono">
          <option value="">Select a model</option>
          {grouped.map((g) =>
            g.models.length === 0 ? null : (
              <optgroup key={g.provider.id} label={g.provider.display_name}>
                {g.models.map((m) => (
                  <option key={`${g.provider.id}:${m.id}`} value={m.id}>
                    {m.id}
                  </option>
                ))}
              </optgroup>
            ),
          )}
        </Select>
      )}
    </ScopeField>
  );
}
