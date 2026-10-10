import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ExternalLink, RefreshCw } from "lucide-react";
import { api } from "../../lib/api";
import { ChangelogMarkdown } from "../../components/ChangelogMarkdown";
import { useUpdateInfo } from "../../components/UpdateNotification";
import { useToast } from "../../components/Toast";
import { Badge, Button, Skeleton } from "../../components/ui";
import { SettingsCard } from "./shared";

export function SystemTab() {
  return (
    <div className="space-y-4">
      <UpdatesSettings />
    </div>
  );
}

function UpdatesSettings() {
  const { data, isLoading, isError } = useUpdateInfo();
  const qc = useQueryClient();
  const toast = useToast();
  const [checking, setChecking] = useState(false);

  const checkNow = async () => {
    setChecking(true);
    try {
      const fresh = await api.updateCheck(true);
      qc.setQueryData(["update-check"], fresh);
      if (!fresh.checked) {
        toast.error("Update check failed", "Could not reach GitHub. Try again later.");
      } else if (fresh.update_available) {
        toast.success("Update available", `${fresh.latest} is ready to install.`);
      } else {
        toast.success("Up to date", `You're running the latest version (${fresh.current}).`);
      }
    } catch (e) {
      toast.error("Update check failed", (e as Error).message);
    } finally {
      setChecking(false);
    }
  };

  const publishedLabel = data?.published_at
    ? new Date(data.published_at).toLocaleDateString(undefined, {
        year: "numeric",
        month: "short",
        day: "numeric",
      })
    : "";

  const reachable = !isError && !!data && data.checked;

  return (
    <SettingsCard
      title="Updates"
      busy={isLoading}
      action={
        <Button variant="ghost" onClick={checkNow} disabled={checking}>
          <RefreshCw className={`text-fg-faint ${checking ? "animate-spin" : ""}`} strokeWidth={1.75} aria-hidden="true" />
          {checking ? "Checking…" : "Check now"}
        </Button>
      }
    >
      {isLoading ? (
        <div className="grid gap-px bg-line sm:grid-cols-3">
          {[0, 1, 2].map((i) => (
            <div key={i} className="space-y-2 bg-surface px-4 py-3">
              <Skeleton className="h-3 w-16" />
              <Skeleton className="h-5 w-24" />
            </div>
          ))}
        </div>
      ) : !reachable ? (
        <p className="px-4 py-3.5 text-[13px] text-fg-muted">
          Couldn&apos;t reach GitHub. Try again later. Current version:{" "}
          <span className="font-mono text-fg">{data?.current ?? "dev"}</span>
        </p>
      ) : (
        <>
          <div className="grid grid-cols-2 gap-px bg-line sm:grid-cols-3">
            <div className="bg-surface px-4 py-3">
              <p className="text-[12px] text-fg-muted">Current</p>
              <p className="mt-1 font-mono text-[15px] font-semibold text-fg">{data.current}</p>
            </div>
            <div className="bg-surface px-4 py-3">
              <p className="text-[12px] text-fg-muted">Latest</p>
              <p className="mt-1 font-mono text-[15px] font-semibold text-fg">{data.latest || "—"}</p>
              {publishedLabel && <p className="mt-0.5 text-[12px] text-fg-muted">Released {publishedLabel}</p>}
            </div>
            <div className="col-span-2 bg-surface px-4 py-3 sm:col-span-1">
              <p className="text-[12px] text-fg-muted">Status</p>
              <div className="mt-1.5">
                {data.update_available ? (
                  <Badge tone="accent">Update available</Badge>
                ) : (
                  <Badge tone="success">Up to date</Badge>
                )}
              </div>
            </div>
          </div>

          {data.update_available && data.changelog && (
            <div className="px-4 py-4">
              <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                <h3 className="text-[12.5px] font-medium text-fg">Changelog</h3>
                {data.html_url && (
                  <a
                    href={data.html_url}
                    target="_blank"
                    rel="noreferrer noopener"
                    className="inline-flex items-center gap-1 text-[12px] font-medium text-link hover:underline"
                  >
                    View release on GitHub
                    <ExternalLink className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
                    <span className="sr-only">(opens in a new tab)</span>
                  </a>
                )}
              </div>
              <div
                tabIndex={0}
                role="region"
                aria-label={`Changelog for ${data.latest}`}
                className="max-h-80 overflow-y-auto rounded-lg border border-line bg-subtle p-4 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500"
              >
                <ChangelogMarkdown changelog={data.changelog} />
              </div>
            </div>
          )}
        </>
      )}
    </SettingsCard>
  );
}
