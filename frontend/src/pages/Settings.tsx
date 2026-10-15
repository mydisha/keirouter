import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { useLocation, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cn } from "@/lib/utils";
import { api, type EndpointSettings } from "../lib/api";
import { PageHeader } from "../components/Layout";
import { useToast } from "../components/Toast";
import { ErrorBanner, Skeleton } from "../components/ui";
import { SavingTab } from "./settings/SavingTab";
import { NetworkTab, RoutingTab } from "./settings/RoutingNetworkTabs";
import { BrandingTab } from "./settings/BrandingTab";
import { ImportExportTab } from "./settings/DataTab";
import { SystemTab } from "./settings/SystemTab";
import { NotificationTab } from "./settings/NotificationTab";
import { CurrencyTab } from "./settings/CurrencyTab";

// ── Tab definitions ─────────────────────────────────────────────────
type SettingsTab = "saving" | "routing" | "network" | "branding" | "import-export" | "system" | "notifications" | "currency";

const DEFAULT_TAB: SettingsTab = "saving";

const settingsTabs: { value: SettingsTab; label: string }[] = [
  { value: "saving", label: "Token saving" },
  { value: "routing", label: "Routing" },
  { value: "network", label: "Network" },
  { value: "branding", label: "Branding" },
  { value: "import-export", label: "Import & export" },
  { value: "system", label: "System" },
  { value: "notifications", label: "Notification" },
  { value: "currency", label: "Currency" },
];

const validTabs = settingsTabs.map((t) => t.value);
const isTab = (v: string | null): v is SettingsTab => !!v && validTabs.includes(v as SettingsTab);

// Tabs that edit the endpoint settings document (saved on every change).
const ENDPOINT_TABS: SettingsTab[] = ["saving", "routing", "network"];

// useSettingsTab keeps the active tab in `?tab=`. Older links used a hash
// (`/settings#system`); those are migrated to the query param on load.
function useSettingsTab(): [SettingsTab, (t: SettingsTab) => void] {
  const [params, setParams] = useSearchParams();
  const location = useLocation();
  const fromParam = params.get("tab");
  const hash = location.hash.replace("#", "");
  const legacy = !fromParam && isTab(hash) ? hash : null;
  const tab: SettingsTab = isTab(fromParam) ? fromParam : legacy ?? DEFAULT_TAB;

  const setTab = (t: SettingsTab) =>
    setParams(
      (p) => {
        if (t === DEFAULT_TAB) p.delete("tab");
        else p.set("tab", t);
        return p;
      },
      { replace: true },
    );

  // Navigating with the router (rather than history.replaceState) drops the
  // hash and moves the tab into the query string in one step.
  useEffect(() => {
    if (!legacy) return;
    setParams(
      (p) => {
        if (legacy === DEFAULT_TAB) p.delete("tab");
        else p.set("tab", legacy);
        return p;
      },
      { replace: true },
    );
  }, [legacy, setParams]);

  return [tab, setTab];
}

function SettingsTabs({ active, onChange }: { active: SettingsTab; onChange: (t: SettingsTab) => void }) {
  const listRef = useRef<HTMLDivElement>(null);

  // On phones the tab row scrolls sideways; keep the active tab in view when
  // it changes (deep links, arrow keys, taps on a half-hidden tab).
  useEffect(() => {
    const list = listRef.current;
    const tab = list?.querySelector<HTMLElement>('[aria-selected="true"]');
    if (!list || !tab || list.scrollWidth <= list.clientWidth) return;
    // The list is positioned, so offsetLeft is measured from its edge.
    const start = tab.offsetLeft;
    const end = start + tab.offsetWidth;
    const pad = 16;
    const behavior: ScrollBehavior = window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth";
    if (start - pad < list.scrollLeft) list.scrollTo({ left: Math.max(0, start - pad), behavior });
    else if (end + pad > list.scrollLeft + list.clientWidth) list.scrollTo({ left: end + pad - list.clientWidth, behavior });
  }, [active]);

  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (!["ArrowRight", "ArrowLeft", "Home", "End"].includes(event.key)) return;
    const controls = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]'));
    const current = controls.indexOf(document.activeElement as HTMLButtonElement);
    if (current < 0 || controls.length === 0) return;
    event.preventDefault();
    const next =
      event.key === "Home"
        ? 0
        : event.key === "End"
          ? controls.length - 1
          : (current + (event.key === "ArrowRight" ? 1 : -1) + controls.length) % controls.length;
    controls[next]?.focus();
    controls[next]?.click();
  };

  return (
    <div
      ref={listRef}
      // Bleeds to the screen edges on phones so the cut-off tab hints that the row scrolls.
      className="relative -mx-4 mb-5 flex gap-1 overflow-x-auto border-b border-line px-4 [scrollbar-width:none] sm:mx-0 sm:px-0 [&::-webkit-scrollbar]:hidden"
      role="tablist"
      aria-label="Settings sections"
      onKeyDown={onKeyDown}
    >
      {settingsTabs.map(({ value, label }) => {
        const on = active === value;
        return (
          <button
            key={value}
            type="button"
            role="tab"
            id={`settings-tab-${value}`}
            aria-selected={on}
            aria-controls="settings-panel"
            tabIndex={on ? 0 : -1}
            onClick={() => onChange(value)}
            className={cn(
              "relative -mb-px inline-flex shrink-0 items-center whitespace-nowrap px-3 py-2.5 text-[13px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500",
              on ? "text-fg" : "text-fg-muted hover:text-fg",
            )}
          >
            {label}
            {on && <span aria-hidden="true" className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-accent-500" />}
          </button>
        );
      })}
    </div>
  );
}

function TabSkeleton() {
  return (
    <div className="space-y-4" aria-busy="true" aria-label="Loading settings">
      {[3, 4, 2].map((rows, i) => (
        <div key={i} className="overflow-hidden rounded-2xl border border-line bg-surface">
          <div className="space-y-1.5 border-b border-line px-4 py-3">
            <Skeleton className="h-3.5 w-36" />
            <Skeleton className="h-3 w-72 max-w-full" />
          </div>
          <div className="divide-y divide-line">
            {Array.from({ length: rows }).map((_, r) => (
              <div key={r} className="flex items-center justify-between gap-6 px-4 py-3.5">
                <div className="min-w-0 flex-1 space-y-1.5">
                  <Skeleton className="h-3.5 w-40" />
                  <Skeleton className="h-3 w-80 max-w-full" />
                </div>
                <Skeleton className="h-5 w-9 rounded-full" />
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

// ── Page ────────────────────────────────────────────────────────────
export function SettingsPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const settings = useQuery({ queryKey: ["endpoint-settings"], queryFn: () => api.endpointSettings() });
  const [local, setLocal] = useState<EndpointSettings | null>(null);
  const [tab, setTab] = useSettingsTab();

  useEffect(() => {
    if (settings.data) setLocal(settings.data);
  }, [settings.data]);

  // Saves run one at a time. Edits made while a save is in flight are merged
  // into one queued patch, so responses can never arrive out of order and an
  // older response never overwrites newer local edits.
  const inFlight = useRef(false);
  const queued = useRef<Partial<EndpointSettings> | null>(null);

  const save = useMutation({
    mutationFn: (patch: Partial<EndpointSettings>) => api.updateEndpointSettings(patch),
    onSuccess: (data) => {
      qc.setQueryData(["endpoint-settings"], data);
      if (!queued.current) setLocal(data);
    },
    onError: (e) => {
      toast.error("Settings save failed", (e as Error).message);
      // Drop the rejected optimistic edit so the page shows what is actually
      // in effect, unless newer edits are already queued behind it.
      const saved = qc.getQueryData<EndpointSettings>(["endpoint-settings"]);
      if (saved && !queued.current) setLocal(saved);
    },
  });

  const send = (patch: Partial<EndpointSettings>) => {
    inFlight.current = true;
    save
      .mutateAsync(patch)
      .catch(() => {
        /* surfaced by onError */
      })
      .finally(() => {
        inFlight.current = false;
        const next = queued.current;
        queued.current = null;
        if (next) send(next);
      });
  };

  const update = (patch: Partial<EndpointSettings>) => {
    setLocal((prev) => (prev ? { ...prev, ...patch } : prev));
    if (inFlight.current) {
      queued.current = { ...(queued.current ?? {}), ...patch };
      return;
    }
    send(patch);
  };

  const endpointTab = ENDPOINT_TABS.includes(tab);

  return (
    <>
      <PageHeader
        title="Settings"
        description="Gateway behaviour, branding, backups and updates."
        action={
          endpointTab && local ? (
            <span role="status" className="text-[12.5px] text-fg-muted">
              {save.isPending ? "Saving…" : "Changes save automatically"}
            </span>
          ) : undefined
        }
      />

      <SettingsTabs active={tab} onChange={setTab} />

      <div id="settings-panel" role="tabpanel" aria-labelledby={`settings-tab-${tab}`} className="max-w-4xl">
        {endpointTab && (
          <>
            {save.isError && (
              <ErrorBanner
                className="mb-4"
                message={`Failed to save: ${(save.error as Error)?.message ?? "unknown error"}`}
              />
            )}
            {settings.isError ? (
              <ErrorBanner message={`Couldn't load settings: ${(settings.error as Error)?.message ?? "unknown error"}`} />
            ) : settings.isLoading || !local ? (
              <TabSkeleton />
            ) : (
              <>
                {tab === "saving" && <SavingTab local={local} update={update} setLocal={setLocal} />}
                {tab === "routing" && <RoutingTab local={local} update={update} />}
                {tab === "network" && <NetworkTab local={local} update={update} />}
              </>
            )}
          </>
        )}
        {tab === "branding" && <BrandingTab />}
        {tab === "import-export" && <ImportExportTab />}
        {tab === "system" && <SystemTab />}
        {tab === "notifications" && <NotificationTab />}
        {tab === "currency" && <CurrencyTab />}
      </div>
    </>
  );
}
