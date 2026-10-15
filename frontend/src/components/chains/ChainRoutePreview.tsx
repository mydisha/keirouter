import { ChevronRight, Repeat2, Route, Shield } from "lucide-react";
import type { Chain, Provider } from "../../lib/api";
import { ProviderLogo } from "../ProviderLogo";
import { isRoundRobinStrategy, providerIcon, strategyLabel } from "./chainUtils";

type Step = { provider: string; model: string };

function providerName(provider: Provider | undefined, id: string) {
  return provider?.display_name || id;
}

// routeText is the text alternative for the visual route: every step in
// order, the rotation note and the final fallback.
function routeText(strategy: string, steps: Step[], fallback: Step | undefined, providerMap: Map<string, Provider>, roundRobin: boolean) {
  if (steps.length === 0 && !fallback) return "No models in this route.";
  const parts = steps.map((step, index) => `${index + 1}. ${providerName(providerMap.get(step.provider), step.provider)} ${step.model}`);
  let text = `${strategyLabel(strategy)} route: ${parts.join(", ")}.`;
  if (roundRobin && steps.length > 1) text += " Starting model rotates.";
  if (fallback) text += ` Final fallback: ${providerName(providerMap.get(fallback.provider), fallback.provider)} ${fallback.model}.`;
  return text;
}

// CompactRoute is the one-line list rendering: provider marks joined by
// chevrons, model ids available on hover. It is visual only; the text
// alternative is rendered alongside it.
function CompactRoute({ steps, hiddenCount, fallback, providerMap, roundRobin }: {
  steps: Step[];
  hiddenCount: number;
  fallback?: Step;
  providerMap: Map<string, Provider>;
  roundRobin: boolean;
}) {
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-1" aria-hidden="true">
      {steps.map((step, index) => {
        const provider = providerMap.get(step.provider);
        return (
          <span key={`${step.provider}/${step.model}/${index}`} className="inline-flex items-center gap-1">
            {index > 0 && <ChevronRight className="h-3 w-3 shrink-0 text-fg-faint" strokeWidth={1.75} />}
            <span className="inline-flex" title={`${index + 1}. ${providerName(provider, step.provider)} · ${step.model}`}>
              <ProviderLogo icon={providerIcon(provider, step.provider)} name={providerName(provider, step.provider)} size={20} />
            </span>
          </span>
        );
      })}
      {hiddenCount > 0 && <span className="ml-0.5 text-[12px] tabular-nums text-fg-muted">+{hiddenCount}</span>}
      {roundRobin && (
        <span title="Starting model rotates" className="inline-flex">
          <Repeat2 className="ml-0.5 h-3.5 w-3.5 shrink-0 text-tone" strokeWidth={1.75} />
        </span>
      )}
      {fallback && (
        <span className="inline-flex items-center gap-1">
          <ChevronRight className="h-3 w-3 shrink-0 text-fg-faint" strokeWidth={1.75} />
          <span
            className="inline-flex h-5 items-center gap-1 rounded-md border border-dashed border-warn/50 bg-warn/5 px-1 text-warn"
            title={`Final fallback · ${providerName(providerMap.get(fallback.provider), fallback.provider)} · ${fallback.model}`}
          >
            <Shield className="h-3 w-3 shrink-0" strokeWidth={1.75} />
            <ProviderLogo
              icon={providerIcon(providerMap.get(fallback.provider), fallback.provider)}
              name={providerName(providerMap.get(fallback.provider), fallback.provider)}
              size={14}
              className="border-0"
            />
          </span>
        </span>
      )}
    </div>
  );
}

// StepNumber is the tone-tinted position circle shared by the editor and preview.
export function StepNumber({ n }: { n: number }) {
  return (
    <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-tone-soft text-[12px] font-semibold tabular-nums text-tone ring-1 ring-inset ring-tone-ring">
      <span className="sr-only">Step </span>{n}
    </span>
  );
}

// FullRoute is the vertical, ordered rendering used by the editor preview.
// It is a real ordered list, so it reads in order without extra text.
function FullRoute({ steps, fallback, providerMap, roundRobin }: {
  steps: Step[];
  fallback?: Step;
  providerMap: Map<string, Provider>;
  roundRobin: boolean;
}) {
  if (steps.length === 0 && !fallback) {
    return (
      <div className="rounded-lg border border-dashed border-line-strong px-3 py-5 text-center">
        <span className="mx-auto mb-2 flex h-7 w-7 items-center justify-center rounded-lg bg-tone-soft text-tone ring-1 ring-inset ring-tone-ring" aria-hidden="true">
          <Route className="h-4 w-4" strokeWidth={1.75} />
        </span>
        <p className="text-[12.5px] text-fg-muted">Choose a model to see the route.</p>
      </div>
    );
  }
  return (
    <ol className="space-y-1.5" aria-label="Route order">
      {steps.map((step, index) => {
        const provider = providerMap.get(step.provider);
        return (
          <li key={`${step.provider}/${step.model}/${index}`} className="flex min-w-0 items-center gap-2.5 rounded-lg border border-line bg-surface px-2.5 py-2">
            <StepNumber n={index + 1} />
            <ProviderLogo icon={providerIcon(provider, step.provider)} name={providerName(provider, step.provider)} size={20} />
            <span className="min-w-0 flex-1">
              <span className="block truncate font-mono text-[12px] text-fg" title={step.model}>{step.model}</span>
              <span className="block truncate text-[12px] text-fg-muted">{providerName(provider, step.provider)}</span>
            </span>
          </li>
        );
      })}
      {roundRobin && steps.length > 1 && (
        <li className="flex items-center gap-1.5 px-2.5 text-[12px] text-fg-muted">
          <Repeat2 className="h-3.5 w-3.5 shrink-0 text-tone" strokeWidth={1.75} aria-hidden="true" />
          Starting model rotates
        </li>
      )}
      {fallback && (
        <li className="flex min-w-0 items-center gap-2.5 rounded-lg border border-dashed border-warn/50 bg-warn/5 px-2.5 py-2">
          <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-warn/12 text-warn ring-1 ring-inset ring-warn/25" aria-hidden="true">
            <Shield className="h-3.5 w-3.5" strokeWidth={1.75} />
          </span>
          <ProviderLogo
            icon={providerIcon(providerMap.get(fallback.provider), fallback.provider)}
            name={providerName(providerMap.get(fallback.provider), fallback.provider)}
            size={20}
          />
          <span className="min-w-0 flex-1">
            <span className="block truncate font-mono text-[12px] text-fg" title={fallback.model}>{fallback.model}</span>
            <span className="block truncate text-[12px] text-fg-muted">Final fallback</span>
          </span>
        </li>
      )}
    </ol>
  );
}

export function ChainRoutePreview({ chain, providers, compact = false }: {
  chain: Pick<Chain, "strategy" | "steps" | "fallback_provider" | "fallback_model">;
  providers: Provider[];
  compact?: boolean;
}) {
  const providerMap = new Map(providers.map((provider) => [provider.id, provider]));
  const visibleSteps = compact ? chain.steps.slice(0, 4) : chain.steps;
  const hiddenCount = chain.steps.length - visibleSteps.length;
  const fallback = chain.fallback_provider && chain.fallback_model
    ? { provider: chain.fallback_provider, model: chain.fallback_model }
    : undefined;
  const roundRobin = isRoundRobinStrategy(chain.strategy);

  if (compact) {
    return (
      <div className="min-w-0">
        <CompactRoute steps={visibleSteps} hiddenCount={hiddenCount} fallback={fallback} providerMap={providerMap} roundRobin={roundRobin} />
        <span className="sr-only">{routeText(chain.strategy, chain.steps, fallback, providerMap, roundRobin)}</span>
      </div>
    );
  }
  return (
    <div className="min-w-0">
      <FullRoute steps={visibleSteps} fallback={fallback} providerMap={providerMap} roundRobin={roundRobin} />
    </div>
  );
}
