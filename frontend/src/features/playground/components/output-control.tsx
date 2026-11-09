import { Braces } from "lucide-react";
import { useId } from "react";
import { Button, Input, SegmentedControl, Textarea } from "@/components/ui";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { OUTPUT_PRESETS, outputLabel, presetOutput, schemaError } from "../output-format";
import type { OutputFormat } from "../output-format";

const MODES = [
  { value: "text", label: "Text" },
  { value: "json", label: "JSON" },
  { value: "schema", label: "Schema" },
] as const;

/**
 * How the reply comes back: text, any JSON object, or JSON that follows a
 * schema (with presets for pulling fields out of documents).
 */
export default function OutputControl({
  value,
  onChange,
}: {
  value: OutputFormat;
  onChange: (value: OutputFormat) => void;
}) {
  const id = useId();
  const error = schemaError(value);

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="quiet" size="sm" aria-label={`Output: ${outputLabel(value)}`}>
          <Braces aria-hidden />
          <span className="hidden sm:inline">Output</span>
          <span className="max-w-24 truncate text-fg-muted">{outputLabel(value)}</span>
          {error && <span aria-hidden className="size-1.5 rounded-full bg-bad" />}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="grid w-[min(calc(100vw-2rem),30rem)] gap-3">
        <div className="space-y-1">
          <p id={`${id}-title`} className="text-sm font-medium">
            Output format
          </p>
          <p className="text-xs text-pretty text-fg-muted">
            Sent as <code className="font-mono">response_format</code>, and the system prompt asks for JSON too, so
            models without a JSON mode still answer in JSON. Use a schema to pull the same fields out of every PDF.
          </p>
        </div>
        <SegmentedControl
          aria-labelledby={`${id}-title`}
          value={value.mode}
          onChange={(mode) => {
            if (mode === "text" || mode === "json") {
              onChange({ mode });
            } else if (mode === "schema" && value.mode !== "schema") {
              onChange(presetOutput(OUTPUT_PRESETS[0]));
            }
          }}
          options={MODES.map((m) => ({ value: m.value, label: m.label }))}
          className="w-full [&>button]:flex-1"
        />

        {value.mode === "json" && (
          <p className="text-xs text-pretty text-fg-muted">
            The reply is one JSON object, and the model picks its fields. Say in your message which fields you want.
          </p>
        )}

        {value.mode === "schema" && (
          <>
            <div className="grid gap-1.5">
              <p id={`${id}-presets`} className="text-xs text-fg-muted">
                Start from
              </p>
              <div role="group" aria-labelledby={`${id}-presets`} className="flex flex-wrap gap-1.5">
                {OUTPUT_PRESETS.map((preset) => {
                  const next = presetOutput(preset);

                  return (
                    <Button
                      key={preset.name}
                      type="button"
                      variant="secondary"
                      size="sm"
                      aria-pressed={value.schema === next.schema}
                      onClick={() => onChange(next)}
                      className="h-7 rounded-sm px-2 text-xs aria-pressed:border-brand aria-pressed:bg-brand-soft aria-pressed:text-brand-ink"
                    >
                      {preset.label}
                    </Button>
                  );
                })}
              </div>
            </div>
            <div className="grid gap-1.5">
              <label htmlFor={`${id}-name`} className="text-xs font-medium">
                Name
              </label>
              <Input
                id={`${id}-name`}
                value={value.name}
                onChange={(e) => onChange({ ...value, name: e.target.value })}
                className="h-8 font-mono text-xs"
                spellCheck={false}
              />
            </div>
            <div className="grid gap-1.5">
              <label htmlFor={`${id}-schema`} className="text-xs font-medium">
                JSON schema
              </label>
              <Textarea
                id={`${id}-schema`}
                value={value.schema}
                onChange={(e) =>
                  onChange({
                    ...value,
                    schema: e.target.value,
                  })
                }
                aria-invalid={error !== null || undefined}
                aria-describedby={`${id}-schema-hint`}
                spellCheck={false}
                className="max-h-72 min-h-48 font-mono text-xs"
              />
              <p
                id={`${id}-schema-hint`}
                role={error ? "alert" : undefined}
                className={error ? "text-xs text-bad" : "text-xs text-fg-muted"}
              >
                {error ??
                  'Strict mode: every property is required. Give a field that may be missing the type ["string", "null"].'}
              </p>
            </div>
          </>
        )}
      </PopoverContent>
    </Popover>
  );
}
