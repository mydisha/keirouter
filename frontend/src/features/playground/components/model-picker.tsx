import { Command } from "cmdk";
import { Check, ChevronsUpDown, Layers, Search } from "lucide-react";
import { useState } from "react";
import { ProviderLogo } from "@/components/ProviderLogo";
import { Button, Skeleton } from "@/components/ui";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";
import type { PlaygroundModel } from "../types";

/** cmdk filter: every word must appear in the row's text (no fuzzy scoring). */
function matches(value: string, search: string): number {
  const haystack = value.toLowerCase();
  return search
    .toLowerCase()
    .split(/\s+/)
    .every((word) => haystack.includes(word))
    ? 1
    : 0;
}

function Mark({ model, className }: { model: PlaygroundModel; className?: string }) {
  if (model.chain) {
    return <Layers aria-hidden className={cn("size-4 shrink-0 text-fg-muted", className)} />;
  }
  return <ProviderLogo icon={model.icon} name={model.provider_name ?? model.name} className={className} />;
}

/** What a model reads and does, as a short line under its name. */
function abilities(m: PlaygroundModel): string {
  if (m.chain) return "Routing chain";
  return [m.provider_name ?? m.provider, m.reasoning && "Thinks", m.vision && "Reads images", m.pdf && "Reads PDFs"]
    .filter(Boolean)
    .join(" · ");
}

/**
 * Searchable list of the chains and connected chat models, in a popover
 * (WAI-ARIA combobox via cmdk). Rows show the provider mark, name, id and what
 * the model can do.
 */
export default function ModelPicker({
  models,
  value,
  onChange,
  disabled,
}: {
  /** Undefined while the list loads. */
  models: PlaygroundModel[] | undefined;
  value: string;
  onChange: (id: string) => void;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);

  if (models === undefined) {
    return (
      <span aria-busy className="flex items-center">
        <Skeleton className="h-9 w-52 pointer-coarse:h-11" />
        <span className="sr-only" role="status">
          Loading models
        </span>
      </span>
    );
  }

  const selected = models.find((m) => m.id === value);

  return (
    <Popover open={open} onOpenChange={setOpen} modal>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="secondary"
          role="combobox"
          aria-expanded={open}
          aria-haspopup="listbox"
          aria-label={selected ? `Model: ${selected.name}` : "Choose a model"}
          disabled={disabled}
          className="min-w-0 max-w-full justify-start font-normal sm:max-w-80"
        >
          {selected ? (
            <>
              <Mark model={selected} />
              <span className="truncate font-medium">{selected.name}</span>
            </>
          ) : (
            <span className="truncate text-fg-muted">{value || "Choose a model"}</span>
          )}
          <ChevronsUpDown aria-hidden className="ml-auto text-fg-muted" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-[min(calc(100vw-2rem),30rem)] p-0">
        <Command label="Models" loop filter={matches}>
          <div className="flex items-center gap-2 border-b px-3">
            <Search aria-hidden className="size-4 text-fg-muted" />
            <Command.Input
              placeholder="Search by model, provider or chain"
              className="h-11 flex-1 bg-transparent text-base outline-none placeholder:text-fg-faint md:text-sm"
            />
          </div>
          <Command.List className="max-h-[min(60vh,380px)] overflow-y-auto p-1.5">
            <Command.Empty className="px-3 py-6 text-[13px] text-fg-muted">No model matches that search.</Command.Empty>
            {models.map((m) => (
              <Command.Item
                key={m.id}
                value={[m.name, m.id, m.provider_name, m.provider].filter(Boolean).join(" ")}
                onSelect={() => {
                  onChange(m.id);
                  setOpen(false);
                }}
                className="flex cursor-pointer items-start gap-3 rounded-lg px-3 py-2 text-[13px] aria-selected:bg-brand-soft"
              >
                <Mark model={m} className="mt-0.5" />
                <span className="grid min-w-0 flex-1 gap-0.5">
                  <span className="flex min-w-0 flex-col sm:flex-row sm:items-baseline sm:gap-2">
                    <span className="truncate font-medium">{m.name}</span>
                    {m.id !== m.name && <span className="truncate font-mono text-[12px] text-fg-muted">{m.id}</span>}
                  </span>
                  <span className="text-[12px] text-fg-muted">{abilities(m)}</span>
                </span>
                <Check
                  aria-hidden
                  className={cn("mt-0.5 size-4 shrink-0 text-brand-ink", m.id === value ? "opacity-100" : "opacity-0")}
                />
              </Command.Item>
            ))}
          </Command.List>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
