import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Upload } from "lucide-react";
import { cn } from "@/lib/utils";
import { api, type BrandingSettings } from "../../lib/api";
import { PALETTES, getPaletteScales } from "../../lib/palettes";
import { applyShadeScale, generateShades } from "../../lib/color-utils";
import { useToast } from "../../components/Toast";
import { Skeleton } from "../../components/ui";
import { FormField, SaveBar, SettingsCard, inputClass } from "./shared";

const DEFAULT_LOGO = "/keirouter-logo.png";
const DEFAULT_FAVICON = "/keirouter-favicon.png";

function fileToDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result as string);
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}

// Live preview: apply a palette's shade scales straight to <html>.
function previewPalette(id: string) {
  const root = document.documentElement;
  const scales = getPaletteScales(id);
  applyShadeScale(root, "accent", scales.accent);
  applyShadeScale(root, "secondary", scales.secondary);
}

function sameBranding(a: BrandingSettings, b: BrandingSettings) {
  return (
    a.name === b.name &&
    a.tagline === b.tagline &&
    a.logo_url === b.logo_url &&
    a.favicon_url === b.favicon_url &&
    (a.color_palette || "kei") === (b.color_palette || "kei")
  );
}

export function BrandingTab() {
  const qc = useQueryClient();
  const toast = useToast();
  const branding = useQuery({ queryKey: ["branding"], queryFn: () => api.branding() });
  const [local, setLocal] = useState<BrandingSettings | null>(null);

  useEffect(() => {
    if (branding.data) setLocal(branding.data);
  }, [branding.data]);

  const save = useMutation({
    mutationFn: (patch: Partial<BrandingSettings>) => api.updateBranding(patch),
    onSuccess: (data) => {
      setLocal(data);
      qc.setQueryData(["branding"], data);
      qc.invalidateQueries({ queryKey: ["portal-branding"] });
      toast.success("Branding updated", `Display name set to "${data.name}". Refresh to see changes.`);
    },
    onError: (e) => toast.error("Branding save failed", (e as Error).message),
  });

  const update = (patch: Partial<BrandingSettings>) => {
    if (local) setLocal({ ...local, ...patch });
  };

  const handleSave = () => {
    if (local) save.mutate(local);
  };

  const handleDiscard = () => {
    if (!branding.data) return;
    setLocal(branding.data);
    previewPalette(branding.data.color_palette || "kei");
  };

  if (branding.isLoading || !local) {
    return (
      <div className="space-y-4" aria-busy="true">
        <Skeleton className="h-44 w-full rounded-2xl" />
        <Skeleton className="h-72 w-full rounded-2xl" />
      </div>
    );
  }

  const dirty = branding.data ? !sameBranding(local, branding.data) : false;
  const paletteId = local.color_palette || "kei";

  return (
    <SettingsCard
      title="White-label branding"
      description="Name, logo, favicon and colors. Applies to the admin dashboard and the public Usage Dashboard."
      footer={
        <SaveBar
          dirty={dirty}
          saving={save.isPending}
          onDiscard={handleDiscard}
          onSave={handleSave}
          error={save.isError ? (save.error as Error)?.message : undefined}
          saveLabel="Save branding"
        />
      }
    >
      <div className="grid grid-cols-1 gap-4 px-4 py-4 sm:grid-cols-2">
        <FormField
          label="Display name"
          htmlFor="branding-name"
          hint="Shown in the sidebar, tab title, login screen and Usage Dashboard."
        >
          <input
            id="branding-name"
            value={local.name}
            onChange={(e) => update({ name: e.target.value })}
            placeholder="KeiRouter"
            className={inputClass}
          />
        </FormField>
        <FormField
          label="Portal tagline"
          htmlFor="branding-tagline"
          optional
          hint="Message on the Usage Dashboard login screen."
        >
          <input
            id="branding-tagline"
            value={local.tagline}
            onChange={(e) => update({ tagline: e.target.value })}
            placeholder="Enter your API Key to view usage."
            className={inputClass}
          />
        </FormField>
      </div>

      <div className="grid grid-cols-1 gap-6 px-4 py-4 sm:grid-cols-2">
        <ImageUploadField
          label="Logo"
          hint="SVG or PNG recommended. Leave empty for the default."
          value={local.logo_url}
          fallback={DEFAULT_LOGO}
          onChange={(dataUrl) => update({ logo_url: dataUrl })}
          accept="image/png,image/svg+xml,image/*"
        />
        <ImageUploadField
          label="Favicon"
          hint="PNG or ICO recommended. Leave empty for the default."
          value={local.favicon_url}
          fallback={DEFAULT_FAVICON}
          onChange={(dataUrl) => update({ favicon_url: dataUrl })}
          accept="image/png,image/x-icon,image/*"
          small
        />
      </div>

      <div className="px-4 py-4">
        <p className="text-[12.5px] font-medium text-fg" id="palette-label">
          Color theme
        </p>
        <p className="mt-0.5 text-[12px] leading-5 text-fg-muted">
          Sets the accent and highlight colors across the dashboard. Previews instantly; save to keep it.
        </p>
        <PalettePicker
          value={paletteId}
          onChange={(id) => {
            update({ color_palette: id });
            previewPalette(id);
          }}
        />
      </div>

      <div className="px-4 py-4">
        <p className="text-[12.5px] font-medium text-fg">Preview</p>
        <div className="mt-2 overflow-hidden rounded-lg border border-line">
          <div className="flex items-end gap-1 border-b border-line bg-subtle px-2 pt-2">
            <div className="flex h-8 min-w-0 max-w-64 items-center gap-2 rounded-t-lg border border-b-0 border-line bg-surface px-3">
              <img src={local.favicon_url || DEFAULT_FAVICON} alt="" className="h-4 w-4 shrink-0 object-contain" />
              <span className="truncate text-[12px] text-fg">{local.name || "KeiRouter"}</span>
            </div>
          </div>
          <div className="flex items-center gap-3 bg-surface px-4 py-3">
            <img src={local.logo_url || DEFAULT_LOGO} alt={local.name || "Logo"} className="h-9 w-9 shrink-0 object-contain" />
            <div className="min-w-0">
              <p className="truncate text-[13px] font-semibold text-fg">{local.name || "KeiRouter"}</p>
              {local.tagline && <p className="truncate text-[12px] text-fg-muted">{local.tagline}</p>}
            </div>
            <span className="ml-auto hidden items-center gap-3 text-[12px] sm:flex">
              <span className="font-medium text-accent-500 dark:text-accent-400">Link color</span>
              <span className="inline-flex h-5 w-9 items-center rounded-full bg-accent-500 px-0.5" aria-hidden="true">
                <span className="ml-auto h-4 w-4 rounded-full bg-white" />
              </span>
            </span>
          </div>
        </div>
      </div>
    </SettingsCard>
  );
}

function PalettePicker({ value, onChange }: { value: string; onChange: (id: string) => void }) {
  return (
    <div className="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-4" role="radiogroup" aria-labelledby="palette-label">
      {PALETTES.map((palette) => {
        const selected = palette.id === value;
        const accent = generateShades(palette.accent);
        const secondary = generateShades(palette.secondary);
        return (
          <button
            key={palette.id}
            type="button"
            role="radio"
            aria-checked={selected}
            title={palette.description}
            onClick={() => onChange(palette.id)}
            className={cn(
              "relative flex flex-col gap-2 rounded-lg border p-2 text-left transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40",
              selected ? "border-fg bg-surface" : "border-line bg-surface hover:border-line-strong hover:bg-hover",
            )}
          >
            <span className="flex h-7 overflow-hidden rounded-md" aria-hidden="true">
              {[accent[200], accent[400], accent[500], accent[700]].map((c, i) => (
                <span key={i} className="flex-1" style={{ backgroundColor: c }} />
              ))}
              <span className="w-5 border-l-2 border-surface" style={{ backgroundColor: secondary[500] }} />
            </span>
            <span className="flex items-center justify-between gap-2 px-0.5">
              <span className={cn("truncate text-[12.5px] font-medium", selected ? "text-fg" : "text-fg-muted")}>
                {palette.name}
              </span>
              {selected && <Check className="h-3.5 w-3.5 shrink-0 text-fg" strokeWidth={2} aria-hidden="true" />}
            </span>
          </button>
        );
      })}
    </div>
  );
}

function ImageUploadField({
  label,
  hint,
  value,
  fallback,
  onChange,
  accept,
  small,
}: {
  label: string;
  hint: string;
  value: string;
  fallback: string;
  onChange: (dataUrl: string) => void;
  accept: string;
  /** Render the preview at favicon size. */
  small?: boolean;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [dragOver, setDragOver] = useState(false);

  const handleFile = async (file: File) => {
    if (!file.type.startsWith("image/")) return;
    const dataUrl = await fileToDataUrl(file);
    onChange(dataUrl);
  };

  const handleDrop = async (e: React.DragEvent) => {
    e.preventDefault();
    setDragOver(false);
    const file = e.dataTransfer.files?.[0];
    if (file) await handleFile(file);
  };

  const handleInputChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) await handleFile(file);
    // Reset so the same file can be re-selected
    if (inputRef.current) inputRef.current.value = "";
  };

  return (
    <div>
      <div className="flex items-baseline justify-between">
        <span className="text-[12.5px] font-medium text-fg">{label}</span>
        {value ? (
          <button
            type="button"
            onClick={() => onChange("")}
            className="text-[12px] text-fg-muted hover:text-bad focus:outline-none focus-visible:underline"
          >
            Remove
          </button>
        ) : (
          <span className="text-[12px] text-fg-faint">Default</span>
        )}
      </div>
      <div className="mt-1.5 flex items-stretch gap-3">
        <div className="flex h-16 w-16 shrink-0 items-center justify-center rounded-lg border border-line bg-subtle">
          <img
            src={value || fallback}
            alt={`${label} preview`}
            className={cn("object-contain", small ? "h-8 w-8" : "h-full w-full p-1.5", !value && "opacity-60")}
          />
        </div>
        <button
          type="button"
          onDragOver={(e) => {
            e.preventDefault();
            setDragOver(true);
          }}
          onDragLeave={() => setDragOver(false)}
          onDrop={handleDrop}
          onClick={() => inputRef.current?.click()}
          className={cn(
            "flex min-w-0 flex-1 flex-col items-center justify-center gap-0.5 rounded-lg border border-dashed px-3 py-2 text-center transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40",
            dragOver ? "border-accent-500 bg-accent-500/5" : "border-line-strong hover:bg-hover",
          )}
        >
          <span className="flex items-center gap-1.5 text-[13px] font-medium text-fg">
            <Upload className="h-4 w-4 text-fg-faint" strokeWidth={1.75} aria-hidden="true" />
            {value ? "Replace image" : "Upload image"}
          </span>
          <span className="text-[12px] text-fg-muted">PNG, SVG, ICO — drag & drop or click</span>
        </button>
        <input ref={inputRef} type="file" accept={accept} className="hidden" onChange={handleInputChange} />
      </div>
      <p className="mt-1.5 text-[12px] leading-5 text-fg-muted">{hint}</p>
    </div>
  );
}
