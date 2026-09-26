// Public landing page — default-exported page, served at `/` by Task 9.
//
// Reads the four read-only public endpoints via TanStack Query. Every section
// degrades to a zeroed/empty state when a query is loading, errored, or returns
// nothing: no error cards, no blank screen. `fetchPublicPerformance` rejects on
// non-ok responses, which is caught by the query and rendered as an empty series.
//
// Styling is scoped through `.landing-root` tokens only; this module never
// imports lib/api.ts or the dashboard Layout.

import { useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  fetchPublicOverview,
  fetchPublicModels,
  fetchPublicPerformance,
  fetchPublicArchived,
} from "../lib/publicApi";
import type { PublicModel, PublicPerformance } from "../lib/publicApi";
import { PublicLayout } from "../components/PublicLayout";
import { ModelGlyph } from "../components/ModelGlyph";
import { RankCrown } from "../components/RankCrown";
import { ModelCapabilityIcons } from "../components/ModelCapabilityIcons";

const fmtInt = new Intl.NumberFormat("id-ID");
const fmtCompact = new Intl.NumberFormat("id-ID", { notation: "compact", maximumFractionDigits: 1 });
const fmt2 = new Intl.NumberFormat("id-ID", { minimumFractionDigits: 2, maximumFractionDigits: 2 });

const fmtCount = (n: number) => (n > 0 ? fmtInt.format(n) : "0");
const fmtShort = (n: number) => (n > 0 ? fmtCompact.format(n) : "0");
const fmtRate = (n: number) => `$${n.toLocaleString("id-ID", { maximumFractionDigits: 6 })}`;
const fmtPct = (r: number) => `${(r * 100).toFixed(1)}%`;

const WA_URL = "https://wa.me/84826240052";

function Panel({
  id,
  eyebrow,
  title,
  children,
  className = "",
}: {
  id?: string;
  eyebrow?: string;
  title?: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section id={id} className={`scroll-mt-28 rounded-[22px] border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6 ${className}`}>
      {eyebrow && <p className="text-[11px] uppercase tracking-[0.7px] text-[var(--muted)]">{eyebrow}</p>}
      {title && <h2 className="mt-1 text-[22px] font-[650] tracking-[-0.5px] text-[var(--ink)]">{title}</h2>}
      {children}
    </section>
  );
}

function Metric({
  label,
  value,
  sub,
  hero = false,
}: {
  label: string;
  value: string;
  sub?: ReactNode;
  hero?: boolean;
}) {
  return (
    <div
      className="rounded-2xl border border-[var(--line)] p-5"
      style={hero ? { background: "linear-gradient(110deg, var(--soft), var(--paper))" } : undefined}
    >
      <p className="text-[11px] uppercase tracking-[0.7px] text-[var(--muted)]">{label}</p>
      <p
        className="mt-2 font-[650] tabular-nums text-[var(--ink)]"
        style={{ fontSize: "clamp(23px,2.5vw,38px)", lineHeight: 1.1 }}
      >
        {value}
      </p>
      {sub && <div className="mt-2 text-xs text-[var(--muted)]">{sub}</div>}
    </div>
  );
}

/* 24 hourly bars, computed from the performance series. Zero bars render at 0px
   so an empty/errored series is a flat row rather than a broken chart. */
function HourlyBars({ series }: { series: PublicPerformance["series"] }) {
  const max = series.reduce((m, p) => Math.max(m, p.requests), 0);
  return (
    <div className="flex h-[80px] items-end gap-[3px]">
      {series.map((p) => (
        <div
          key={p.bucket}
          title={`Jam ${p.bucket} · ${fmtCount(p.requests)} request`}
          className="flex-1 rounded-t-[2px] bg-[var(--green)] opacity-70"
          style={{ height: max > 0 ? `${Math.max(2, (p.requests / max) * 100)}%` : "0px" }}
        />
      ))}
    </div>
  );
}

function ModelsTable({ models }: { models: PublicModel[] }) {
  const sorted = [...models].sort((a, b) => b.usage_24h.requests - a.usage_24h.requests);
  return (
    <div className="mt-4 overflow-x-auto">
      <table className="w-full border-collapse text-sm">
        <thead>
          <tr className="bg-[var(--soft)] text-left text-[11px] text-[var(--muted)]">
            <th className="px-4 py-3 font-normal">Model</th>
            <th className="px-4 py-3 text-right font-normal">Input / 1M</th>
            <th className="px-4 py-3 text-right font-normal">Output / 1M</th>
            <th className="px-4 py-3 text-right font-normal">Pemakaian / 24 jam</th>
          </tr>
        </thead>
        <tbody>
          {sorted.length === 0 ? (
            <tr className="border-t border-[var(--line)]">
              <td colSpan={4} className="px-4 py-8 text-center text-sm text-[var(--muted)]">
                Belum ada model aktif dalam 24 jam terakhir.
              </td>
            </tr>
          ) : (
            sorted.map((m) => (
              <tr key={m.model_id} className="border-t border-[var(--line)] align-middle">
                <td className="px-4 py-[18px]">
                  <div className="flex items-start gap-2">
                    <ModelGlyph modelId={m.model_id} size={34} />
                    <div className="min-w-0">
                      <p className="text-[15px] font-[650] text-[var(--ink)]">{m.name}</p>
                      <ModelCapabilityIcons capabilities={m.capabilities} className="my-1" />
                      <code className="block break-all text-[11px] text-[var(--muted)]">{m.model_id}</code>
                    </div>
                  </div>
                </td>
                <td className="px-4 py-[18px] text-right tabular-nums text-[18px] font-[650] text-[var(--ink)]">
                  {fmtRate(m.input_per_m)}
                </td>
                <td className="px-4 py-[18px] text-right tabular-nums text-[18px] font-[650] text-[var(--ink)]">
                  {fmtRate(m.output_per_m)}
                </td>
                <td className="px-4 py-[18px] text-right text-xs text-[var(--muted)]">
                  <span className="text-[var(--ink)]">{fmtCount(m.usage_24h.users)} pengguna</span>
                  <br />
                  {fmtCount(m.usage_24h.requests)} request · {fmtShort(m.usage_24h.tokens)} token
                </td>
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  );
}

export default function PublicLanding() {
  const overview = useQuery({
    queryKey: ["public-overview"],
    queryFn: fetchPublicOverview,
    staleTime: 30_000,
    retry: false,
  });
  const models = useQuery({
    queryKey: ["public-models"],
    queryFn: fetchPublicModels,
    staleTime: 60_000,
    retry: false,
  });
  const archived = useQuery({
    queryKey: ["public-archived"],
    queryFn: fetchPublicArchived,
    staleTime: 60_000,
    retry: false,
  });

  const modelList = models.data ?? [];
  const [selectedModel, setSelectedModel] = useState<string | null>(null);
  const activeModel = selectedModel ?? modelList[0]?.model_id ?? "";

  const performance = useQuery({
    queryKey: ["public-performance", activeModel],
    queryFn: () => fetchPublicPerformance(activeModel),
    enabled: activeModel.length > 0,
    staleTime: 30_000,
    retry: false,
  });

  const overviewData = overview.data;
  const requestTotal = overviewData?.total_requests ?? 0;
  const tokenTotal = overviewData?.total_tokens ?? 0;
  const success24 = overviewData?.success_24h ?? 0;
  const failed24 = overviewData?.failed_24h ?? 0;
  const rps = overviewData?.rps_10s ?? 0;
  const tokens24 = modelList.reduce((s, m) => s + m.usage_24h.tokens, 0);
  const recent = overviewData?.recent ?? [];

  // A query that errored or has no data yet renders the same zeroed shape; only
  // show the "Terhubung" pill when overview actually succeeded.
  const connected = overview.isSuccess;
  const perfSeries: PublicPerformance["series"] = performance.data?.series ?? [];

  const podium = (archived.data?.podium ?? []).slice(0, 2);
  const history = archived.data?.history ?? [];

  return (
    <PublicLayout>
      <div id="overview" className="scroll-mt-28">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div>
            <p className="text-[11px] uppercase tracking-[0.7px] text-[var(--muted)]">
              Monitor / aktivitas layanan
            </p>
            <h1 className="mt-1 text-[30px] font-semibold tracking-[-1.1px] text-[var(--ink)]">
              Aktivitas gateway
            </h1>
          </div>
          <div className="flex items-center gap-2">
            <span className="inline-flex items-center gap-2 rounded-full border border-[var(--line)] px-3 py-2 text-xs text-[var(--muted)]">
              <span
                className={`h-2 w-2 rounded-full ${connected ? "bg-[var(--green)]" : "bg-[var(--muted)]"}`}
                aria-hidden="true"
              />
              {connected ? "Terhubung" : "Menunggu data"}
            </span>
            <button
              type="button"
              onClick={() => {
                void overview.refetch();
                void models.refetch();
                void archived.refetch();
                void performance.refetch();
              }}
              className="rounded-full border border-[var(--line)] px-3 py-2 text-xs text-[var(--muted)] transition-colors hover:bg-[var(--soft)]"
            >
              Perbarui
            </button>
          </div>
        </div>

        <div className="mt-5 grid gap-4 md:grid-cols-[1fr_2fr_0.8fr]">
          <Metric
            label="Request"
            value={fmtShort(requestTotal)}
            sub={
              <>
                24 jam: {fmtCount(success24)} berhasil · {fmtCount(failed24)} gagal
              </>
            }
          />
          <Metric
            label="Token"
            value={fmtCount(tokenTotal)}
            hero
            sub={<>24 jam: {fmtShort(tokens24)} token</>}
          />
          <Metric label="Request / detik" value={fmt2.format(rps)} sub={<>rata-rata 24 jam</>} />
        </div>

        <Panel eyebrow="Monitor / live" title="Request terbaru" className="mt-4">
          {recent.length === 0 ? (
            <p className="mt-4 text-sm text-[var(--muted)]">Belum ada request terbaru.</p>
          ) : (
            <ul className="mt-4 divide-y divide-[var(--line)]">
              {recent.map((r, i) => (
                <li key={i} className="flex items-center justify-between gap-3 py-2 text-sm">
                  <span className="min-w-0 truncate text-[var(--ink)]">
                    <span className="text-[var(--muted)]">{r.provider}</span> · {r.model}
                  </span>
                  <span className="flex shrink-0 items-center gap-3 tabular-nums text-xs text-[var(--muted)]">
                    <span className={r.status === "success" ? "text-[var(--green)]" : ""}>{r.status}</span>
                    <span>{fmtCount(r.latency_ms)} ms</span>
                  </span>
                </li>
              ))}
            </ul>
          )}
        </Panel>
      </div>

      <Panel
        id="models"
        eyebrow="Katalog / pilihan model"
        title="Model & harga"
        className="mt-10"
      >
        <p className="mt-2 max-w-2xl text-sm text-[var(--muted)]">
          Model yang aktif dalam 24 jam terakhir, diurutkan berdasarkan popularitas. Harga ditampilkan per
          1 juta token.
        </p>
        <ModelsTable models={modelList} />
      </Panel>

      <Panel eyebrow="Model / inspector" title="Performa model" className="mt-4">
        <div className="mt-4 flex flex-wrap items-center gap-3">
          <select
            value={activeModel}
            onChange={(e) => setSelectedModel(e.target.value)}
            disabled={modelList.length === 0}
            className="min-h-12 rounded-[10px] border border-[var(--line)] bg-[var(--paper)] px-3 text-sm text-[var(--ink)] disabled:opacity-60"
            aria-label="Pilih model"
          >
            {modelList.length === 0 ? (
              <option value="">Tidak ada model</option>
            ) : (
              modelList.map((m) => (
                <option key={m.model_id} value={m.model_id}>
                  {m.name}
                </option>
              ))
            )}
          </select>
          {activeModel && <ModelGlyph modelId={activeModel} size={34} />}
        </div>

        <div className="mt-4 grid gap-4 sm:grid-cols-3">
          <div className="rounded-2xl border border-[var(--line)] bg-[var(--soft)] p-4">
            <p className="text-[11px] uppercase tracking-[0.7px] text-[var(--muted)]">Latensi rata-rata</p>
            <p className="mt-1 text-lg font-[650] tabular-nums text-[var(--ink)]">
              {performance.data ? `${fmtCount(performance.data.avg_latency_ms)} ms` : "—"}
            </p>
          </div>
          <div className="rounded-2xl border border-[var(--line)] bg-[var(--soft)] p-4">
            <p className="text-[11px] uppercase tracking-[0.7px] text-[var(--muted)]">TTFT rata-rata</p>
            <p className="mt-1 text-lg font-[650] tabular-nums text-[var(--ink)]">
              {performance.data ? `${fmtCount(performance.data.avg_ttft_ms)} ms` : "—"}
            </p>
          </div>
          <div className="rounded-2xl border border-[var(--line)] bg-[var(--soft)] p-4">
            <p className="text-[11px] uppercase tracking-[0.7px] text-[var(--muted)]">Tingkat keberhasilan</p>
            <p className="mt-1 text-lg font-[650] tabular-nums text-[var(--ink)]">
              {performance.data ? fmtPct(performance.data.success_rate) : "—"}
            </p>
          </div>
        </div>

        <p className="mt-5 text-[11px] uppercase tracking-[0.7px] text-[var(--muted)]">
          Request per jam · 24 jam terakhir
        </p>
        <div className="mt-2">
          <HourlyBars series={perfSeries} />
        </div>
        {!performance.data && (
          <p className="mt-2 text-xs text-[var(--muted)]">Belum ada data performa untuk model ini.</p>
        )}
      </Panel>

      <Panel eyebrow="Arsip / puncak" title="Podium model arsip" className="mt-10">
        {podium.length === 0 ? (
          <p className="mt-4 text-sm text-[var(--muted)]">Belum ada model arsip.</p>
        ) : (
          <div className="mt-4 grid gap-4 sm:grid-cols-2">
            {podium.map((p, i) => (
              <div
                key={p.model}
                className="flex items-center gap-4 rounded-2xl border border-[var(--line)] p-4"
                style={{
                  background:
                    i === 0
                      ? "linear-gradient(110deg, var(--soft), var(--paper))"
                      : "linear-gradient(110deg, var(--paper), var(--paper))",
                }}
              >
                <RankCrown rank={i === 0 ? 1 : 2} size={32} />
                <div className="min-w-0">
                  <p className="truncate text-[15px] font-[650] text-[var(--ink)]">{p.model}</p>
                  <p className="text-xs tabular-nums text-[var(--muted)]">
                    {fmtShort(p.tokens)} token · {fmtCount(p.requests)} request
                  </p>
                </div>
              </div>
            ))}
          </div>
        )}

        <div className="mt-6 overflow-x-auto">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="bg-[var(--soft)] text-left text-[11px] text-[var(--muted)]">
                <th className="px-4 py-3 font-normal">Model</th>
                <th className="px-4 py-3 text-right font-normal">Token</th>
                <th className="px-4 py-3 text-right font-normal">Request</th>
                <th className="px-4 py-3 text-right font-normal">Status</th>
              </tr>
            </thead>
            <tbody>
              {history.length === 0 ? (
                <tr className="border-t border-[var(--line)]">
                  <td colSpan={4} className="px-4 py-8 text-center text-sm text-[var(--muted)]">
                    Belum ada riwayat arsip.
                  </td>
                </tr>
              ) : (
                history.map((h) => (
                  <tr key={h.model} className="border-t border-[var(--line)]">
                    <td className="px-4 py-3 text-[var(--ink)]">{h.model}</td>
                    <td className="px-4 py-3 text-right tabular-nums text-[var(--ink)]">{fmtShort(h.tokens)}</td>
                    <td className="px-4 py-3 text-right tabular-nums text-[var(--ink)]">{fmtCount(h.requests)}</td>
                    <td className="px-4 py-3 text-right">
                      <span className="rounded-full bg-[var(--soft)] px-2.5 py-1 text-[10px] uppercase tracking-[0.7px] text-[var(--muted)]">
                        {h.status}
                      </span>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </Panel>

      <div className="mt-10 grid gap-4 md:grid-cols-2">
        <section id="purchase" className="scroll-mt-28 rounded-[22px] border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
          <p className="text-[11px] uppercase tracking-[0.7px] text-[var(--muted)]">Beli / top up</p>
          <h2 className="mt-1 text-[22px] font-[650] tracking-[-0.5px] text-[var(--ink)]">Tambah saldo</h2>
          <p className="mt-2 text-sm text-[var(--muted)]">
            Pengisian saldo dilakukan secara manual. Hubungi kami melalui WhatsApp untuk nominal dan metode
            pembayaran. Setelah pembayaran dikonfirmasi, saldo langsung aktif.
          </p>
          <a
            href={WA_URL}
            target="_blank"
            rel="noreferrer"
            className="mt-4 inline-flex items-center gap-1.5 rounded-xl bg-[var(--green)] px-4 py-3 text-sm font-[650] text-[var(--on-accent)] no-underline hover:opacity-90"
          >
            Hubungi WhatsApp ↗
          </a>
        </section>

        <section id="balance" className="scroll-mt-28 rounded-[22px] border border-[var(--line)] bg-[var(--paper)] p-5 sm:p-6">
          <p className="text-[11px] uppercase tracking-[0.7px] text-[var(--muted)]">Saldo / akun</p>
          <h2 className="mt-1 text-[22px] font-[650] tracking-[-0.5px] text-[var(--ink)]">Cek saldo</h2>
          <p className="mt-2 text-sm text-[var(--muted)]">
            Saldo dan limit terpakai terlihat dari portal API key masing-masing pengguna. Hubungi kami bila
            saldo tidak sesuai setelah top up.
          </p>
          <a
            href={WA_URL}
            target="_blank"
            rel="noreferrer"
            className="mt-4 inline-flex items-center gap-1.5 rounded-xl border border-[var(--line)] px-4 py-3 text-sm font-[650] text-[var(--green)] no-underline hover:bg-[var(--soft)]"
          >
            Tanya saldo ↗
          </a>
        </section>
      </div>
    </PublicLayout>
  );
}
