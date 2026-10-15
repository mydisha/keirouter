import type { CredentialField, FieldCondition, Provider } from "../../lib/api";
import { SelectField, TextAreaField, TextField } from "./ConnectKit";

// Helpers for the provider-declared connect-form fields (AWS keys, regions,
// project ids, service principals, ...). The backend applies the same
// visibility and required rules, so a hidden field is never submitted.

export type FieldValues = Record<string, string>;

function valueOf(provider: Provider, values: FieldValues, key: string): string {
  const v = values[key]?.trim();
  if (v) return v;
  return provider.fields?.find((f) => f.key === key)?.default ?? "";
}

function matches(provider: Provider, values: FieldValues, cond?: FieldCondition): boolean {
  if (!cond) return true;
  return cond.values.includes(valueOf(provider, values, cond.field));
}

export function initialFieldValues(provider: Provider): FieldValues {
  const out: FieldValues = {};
  for (const f of provider.fields ?? []) out[f.key] = f.default ?? "";
  return out;
}

export function visibleFields(provider: Provider, values: FieldValues): CredentialField[] {
  return (provider.fields ?? []).filter((f) => matches(provider, values, f.when));
}

export function apiKeyVisible(provider: Provider, values: FieldValues): boolean {
  if (provider.api_key_mode === "hidden") return false;
  return matches(provider, values, provider.api_key_when);
}

export function apiKeyRequired(provider: Provider, values: FieldValues): boolean {
  if (provider.auth_kind === "none" || provider.api_key_mode === "optional") return false;
  return apiKeyVisible(provider, values);
}

// missingFields lists the visible required fields that are still empty.
export function missingFields(provider: Provider, values: FieldValues): Set<string> {
  const out = new Set<string>();
  for (const f of visibleFields(provider, values)) {
    if (f.required && !valueOf(provider, values, f.key)) out.add(f.key);
  }
  return out;
}

// submittedFields returns the visible, non-empty values to send.
export function submittedFields(provider: Provider, values: FieldValues): Record<string, string> | undefined {
  const out: Record<string, string> = {};
  for (const f of visibleFields(provider, values)) {
    const v = (values[f.key] ?? "").trim();
    if (v) out[f.key] = v;
  }
  return Object.keys(out).length ? out : undefined;
}

// hasComplexAuth marks providers whose credentials are more than one key per
// account, which rules out bulk key import.
export function hasComplexAuth(provider: Provider): boolean {
  return provider.api_key_mode === "hidden" || !!provider.api_key_when || !!provider.fields?.some((f) => f.secret);
}

export function CredentialFieldInputs({
  provider,
  values,
  onChange,
  advanced,
  missing,
  showErrors,
}: {
  provider: Provider;
  values: FieldValues;
  onChange: (key: string, value: string) => void;
  /** Render the advanced fields instead of the main ones. */
  advanced: boolean;
  missing?: Set<string>;
  showErrors?: boolean;
}) {
  const fields = visibleFields(provider, values).filter((f) => !!f.advanced === advanced);
  if (!fields.length) return null;
  return (
    <>
      {fields.map((f) => {
        const error = showErrors && missing?.has(f.key) ? `${f.label} is required.` : undefined;
        if (f.options?.length) {
          return (
            <SelectField key={f.key} label={f.label} hint={f.hint} value={valueOf(provider, values, f.key)} onChange={(e) => onChange(f.key, e.target.value)}>
              {f.options.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </SelectField>
          );
        }
        if (f.multiline) {
          return (
            <TextAreaField
              key={f.key}
              label={f.label}
              required={f.required}
              rows={5}
              value={values[f.key] ?? ""}
              onChange={(e) => onChange(f.key, e.target.value)}
              placeholder={f.placeholder}
              hint={f.hint}
              error={error}
            />
          );
        }
        return (
          <TextField
            key={f.key}
            label={f.label}
            required={f.required}
            optional={!f.required}
            type={f.secret ? "password" : f.url ? "url" : "text"}
            autoComplete="off"
            value={values[f.key] ?? ""}
            onChange={(e) => onChange(f.key, e.target.value)}
            placeholder={f.placeholder ?? f.default}
            className={f.url || f.secret || f.key.endsWith("_id") ? "font-mono" : undefined}
            hint={f.hint}
            error={error}
          />
        );
      })}
    </>
  );
}
