import { NotebookPen } from "lucide-react";
import { useId } from "react";
import { Link } from "react-router-dom";
import { Button, Skeleton } from "@/components/ui";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import type { Skill } from "@/lib/api";

export type PlaygroundSkill = {
  name: string;
  optional: boolean;
  /** Limited to some providers or models: only some replies carry it. */
  scoped: boolean;
};

/**
 * The skills a playground request carries: enabled ones not limited to
 * particular keys or plans (the playground's key is in neither).
 */
export function playgroundSkills(skills: Skill[]): PlaygroundSkill[] {
  return skills
    .filter((s) => s.enabled && !s.scope?.keys?.length && !s.scope?.plans?.length)
    .map((s) => ({ name: s.name, optional: s.optional, scoped: (s.scope?.models?.length ?? 0) > 0 }));
}

/** The skills this chat's requests carry, and a switch to leave the optional ones out. */
export default function SkillsMenu({
  skills,
  skipOptional,
  onSkipOptionalChange,
}: {
  /** Undefined while they load. */
  skills: PlaygroundSkill[] | undefined;
  skipOptional: boolean;
  onSkipOptionalChange: (skip: boolean) => void;
}) {
  const skipId = useId();

  if (skills === undefined) {
    return (
      <Button type="button" variant="quiet" size="sm" aria-busy aria-disabled aria-label="Skills: loading">
        <NotebookPen aria-hidden />
        <span className="hidden @5xl:inline">Skills</span>
        <Skeleton className="h-3 w-2.5" />
      </Button>
    );
  }

  const applied = skills.filter((s) => !(skipOptional && s.optional));
  const hasOptional = skills.some((s) => s.optional);

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="quiet" size="sm" aria-label={`Skills: ${applied.length}`}>
          <NotebookPen aria-hidden />
          <span className="hidden @5xl:inline">Skills</span>
          <span className="text-xs text-fg-muted tabular-nums">{applied.length}</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="grid w-[min(calc(100vw-2rem),22rem)] gap-3">
        <div className="space-y-1">
          <p className="text-sm font-medium">Skills</p>
          <p className="text-xs text-pretty text-fg-muted">
            {skills.length > 0
              ? "We add these to the system prompt of every message here, before your own."
              : "No enabled skill applies to every key, so messages here carry none."}
          </p>
        </div>
        {skills.length > 0 && (
          <ul className="grid gap-1 text-sm">
            {skills.map((s) => (
              <li
                key={s.name}
                className={skipOptional && s.optional ? "text-fg-muted line-through" : undefined}
              >
                {s.name}
                {(s.optional || s.scoped) && (
                  <span className="text-xs text-fg-muted no-underline">
                    {" · "}
                    {[s.optional && "optional", s.scoped && "some models only"].filter(Boolean).join(", ")}
                  </span>
                )}
              </li>
            ))}
          </ul>
        )}
        {hasOptional && (
          <div className="flex items-start gap-2 border-t pt-3">
            <input
              id={skipId}
              type="checkbox"
              checked={skipOptional}
              onChange={(e) => onSkipOptionalChange(e.target.checked)}
              className="mt-0.5 size-4 shrink-0 accent-[var(--action)]"
            />
            <label htmlFor={skipId} className="text-[13px]">
              Leave out optional skills (sends X-KeiRouter-Skip-Skills)
            </label>
          </div>
        )}
        <Link
          to="/skills"
          className="text-sm text-brand-ink underline decoration-1 underline-offset-4 hover:decoration-2"
        >
          {skills.length > 0 ? "Change skills" : "Set up skills"}
        </Link>
      </PopoverContent>
    </Popover>
  );
}
