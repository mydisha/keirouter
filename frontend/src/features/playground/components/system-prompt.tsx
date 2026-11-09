import { SlidersHorizontal } from "lucide-react";
import { useId } from "react";
import { Button } from "@/components/ui";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Textarea } from "@/components/ui";
import { SYSTEM_PRESETS } from "../system-presets";

export default function SystemPrompt({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const id = useId();

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="quiet" size="sm" aria-label="System prompt">
          <SlidersHorizontal aria-hidden />
          <span className="hidden @5xl:inline">System prompt</span>
          {value.trim() && <span aria-hidden className="size-1.5 rounded-full bg-brand" />}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="grid w-[min(calc(100vw-2rem),28rem)] gap-3">
        <div className="space-y-1">
          <div className="flex items-baseline justify-between gap-2">
            <label htmlFor={id} className="text-sm font-medium">
              System prompt
            </label>
            {value && (
              <button
                type="button"
                onClick={() => onChange("")}
                className="text-xs text-fg-muted underline-offset-4 hover:text-fg hover:underline focus-visible:rounded-sm focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-hidden"
              >
                Clear
              </button>
            )}
          </div>
          <p id={`${id}-hint`} className="text-xs text-pretty text-fg-muted">
            Instructions the model reads before every message in this chat: who it is, the tone, the format of its
            replies and what to avoid. Test the prompt your app will send before you put it in code.
          </p>
        </div>
        <Textarea
          id={id}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder="You are a support agent for Acme. Reply in two short paragraphs at most."
          aria-describedby={`${id}-hint`}
          className="max-h-64 min-h-28"
        />
        <div className="grid gap-1.5">
          <p id={`${id}-presets`} className="text-xs text-fg-muted">
            Start from an example
          </p>
          <div role="group" aria-labelledby={`${id}-presets`} className="flex flex-wrap gap-1.5">
            {SYSTEM_PRESETS.map((preset) => (
              <Button
                key={preset.label}
                type="button"
                variant="secondary"
                size="sm"
                aria-pressed={value === preset.text}
                onClick={() => onChange(preset.text)}
                className="h-7 rounded-sm px-2 text-xs aria-pressed:border-brand aria-pressed:bg-brand-soft aria-pressed:text-brand-ink"
              >
                {preset.label}
              </Button>
            ))}
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
}
