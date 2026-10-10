import { useRef, useState, useCallback } from "react";
import { toPng } from "html-to-image";
import { Download, Check } from "lucide-react";
import type { UsageInsights } from "../lib/api";

// ─── Helpers ─────────────────────────────────────────────────────────────────

function fmtNum(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return n.toLocaleString();
}

const periodLabels: Record<string, string> = {
  today: "Today",
  "24h": "Last 24 Hours",
  week: "Last 7 Days",
  month: "Last 30 Days",
};

// Earth Tone Palette (App matched)
const C = {
  bg: "#f7f6f3",
  bgGradientTo: "#ece9e2",
  surface: "#ffffff",
  border: "rgba(17,24,39,0.08)",
  borderLight: "rgba(17,24,39,0.05)",
  text: "#14130f",
  textSecondary: "#4b4840",
  textMuted: "#9a978c",
  accent: "#C45F3A", // Terracotta / Orange
  accentLight: "#d98a6a",
  positive: "#059669", // Emerald for good metrics
  positiveDark: "#047857",
  positiveDeep: "#065f46",
};

// Per-card accent colors for the bento grid.
const cardAccents = ["#059669", "#C45F3A", "#2563eb", "#9a978c"];

// ─── Hidden Card (rendered off-screen for capture) ───────────────────────────

interface SavingsCardData {
  costSaved: number;
  costSavedEstimated: boolean;
  tokensSaved: number;
  savingsPct: number;
  totalRequests: number;
  period: string;
  rtkActive: boolean;
  cavemanActive: boolean;
  terseActive: boolean;
  actualCost: number;
}

function formatCost(cost: number): string {
  if (cost === 0) return "0.00";
  if (cost < 0.01) return cost.toFixed(4);
  return cost.toFixed(2);
}

function SavingsCardContent({ data }: { data: SavingsCardData }) {
  const {
    costSaved,
    costSavedEstimated,
    tokensSaved,
    savingsPct,
    totalRequests,
    period,
    rtkActive,
    cavemanActive,
    terseActive,
    actualCost,
  } = data;

  const optimizers = [
    rtkActive && { name: "RTK", desc: "Tokenizer" },
    cavemanActive && { name: "Caveman", desc: "Compression" },
    terseActive && { name: "Terse", desc: "Compression" },
  ].filter(Boolean) as { name: string; desc: string }[];

  return (
		<div
			aria-hidden="true"
			style={{
        width: 1200,
        height: 630,
        position: "relative",
        overflow: "hidden",
        boxSizing: "border-box",
        fontFamily:
          "'-apple-system', 'BlinkMacSystemFont', 'SF Pro Display', 'Inter', sans-serif",
        background: `linear-gradient(150deg, ${C.bg} 0%, ${C.bgGradientTo} 100%)`,
        color: C.text,
        padding: "44px 52px",
        display: "flex",
        flexDirection: "column",
      }}
    >
      {/* Decorative glow blobs */}
      <div
        style={{
          position: "absolute",
          top: -160,
          right: -120,
          width: 460,
          height: 460,
          borderRadius: "50%",
          background:
            "radial-gradient(circle, rgba(5,150,105,0.16) 0%, rgba(5,150,105,0) 70%)",
          zIndex: 0,
        }}
      />
      <div
        style={{
          position: "absolute",
          bottom: -180,
          left: -100,
          width: 420,
          height: 420,
          borderRadius: "50%",
          background:
            "radial-gradient(circle, rgba(196,95,58,0.12) 0%, rgba(196,95,58,0) 70%)",
          zIndex: 0,
        }}
      />
      {/* Subtle grid texture */}
      <div
        style={{
          position: "absolute",
          inset: 0,
          backgroundImage:
            "linear-gradient(rgba(0,0,0,0.015) 1px, transparent 1px), linear-gradient(90deg, rgba(0,0,0,0.015) 1px, transparent 1px)",
          backgroundSize: "44px 44px",
          zIndex: 0,
        }}
      />

      <div style={{ position: "relative", zIndex: 1, flex: 1, display: "flex", flexDirection: "column" }}>
        
        {/* Header */}
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            paddingBottom: "28px",
            borderBottom: `1px solid ${C.border}`,
          }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: 14 }}>
            <img
              src="/keirouter-logo.png"
              alt="KeiRouter"
              style={{ width: 34, height: 34, objectFit: "contain" }}
              crossOrigin="anonymous"
            />
            <span
              style={{
                fontSize: 22,
                fontWeight: 700,
                color: C.text,
                letterSpacing: "-0.01em",
              }}
            >
              KeiRouter
            </span>
            <div style={{ width: 1, height: 16, background: C.border, margin: "0 8px" }} />
            <span
              style={{
                fontSize: 13,
                fontWeight: 600,
                color: C.textSecondary,
                letterSpacing: "0.1em",
                textTransform: "uppercase",
              }}
            >
              Savings Report
            </span>
          </div>
          <div style={{
            background: C.surface,
            padding: "8px 16px",
            borderRadius: 20,
            border: `1px solid ${C.borderLight}`,
            boxShadow: "0 2px 8px rgba(0,0,0,0.03)"
          }}>
            <span
              style={{
                fontSize: 14,
                fontWeight: 600,
                color: C.textSecondary,
              }}
            >
              {periodLabels[period] || period}
            </span>
          </div>
        </div>

        {/* Main Content Layout */}
        <div style={{ flex: 1, display: "flex", alignItems: "stretch", gap: "36px", paddingTop: "32px" }}>
          
          {/* Left: Big Hero Metric — gradient panel */}
          <div
            style={{
              flex: "0 0 46%",
              position: "relative",
              overflow: "hidden",
              display: "flex",
              flexDirection: "column",
              justifyContent: "center",
              gap: "20px",
              padding: "40px",
              borderRadius: 28,
              background: `linear-gradient(140deg, ${C.positive} 0%, ${C.positiveDark} 55%, ${C.positiveDeep} 100%)`,
              boxShadow: "0 24px 60px rgba(5,150,105,0.28)",
            }}
          >
            {/* Sheen */}
            <div
              style={{
                position: "absolute",
                top: -120,
                right: -80,
                width: 320,
                height: 320,
                borderRadius: "50%",
                background:
                  "radial-gradient(circle, rgba(255,255,255,0.18) 0%, rgba(255,255,255,0) 70%)",
              }}
            />
            <span
              style={{
                position: "relative",
                fontSize: 16,
                fontWeight: 600,
                color: "rgba(255,255,255,0.82)",
                textTransform: "uppercase",
                letterSpacing: "0.08em",
              }}
            >
              {costSavedEstimated ? "Estimated Cost Saved" : "Total Cost Saved"}
            </span>
            <span
              style={{
                position: "relative",
                fontSize: 132,
                fontWeight: 800,
                color: "#ffffff",
                lineHeight: 1,
                letterSpacing: "-0.04em",
                textShadow: "0 4px 24px rgba(0,0,0,0.15)",
              }}
            >
              ${formatCost(costSaved)}
            </span>
            
            {costSaved > 0 && (
              <div style={{ position: "relative", display: "flex", alignItems: "center", gap: 12 }}>
                <div style={{
                  background: "rgba(255,255,255,0.18)",
                  color: "#ffffff",
                  padding: "10px 18px",
                  borderRadius: 22,
                  fontSize: 18,
                  fontWeight: 700,
                  display: "flex",
                  alignItems: "center",
                  gap: 8,
                  border: "1px solid rgba(255,255,255,0.25)",
                }}>
                  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                    <line x1="12" y1="5" x2="12" y2="19"></line>
                    <polyline points="19 12 12 19 5 12"></polyline>
                  </svg>
                  {savingsPct.toFixed(1)}% {costSavedEstimated ? "Estimated Reduction" : "Reduction"}
                </div>
              </div>
            )}
          </div>

          {/* Right: Data Cards */}
          <div style={{ flex: 1, display: "flex", flexDirection: "column", gap: "20px" }}>
            
            {/* Bento Grid */}
            <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "20px" }}>
              <DataCard label="Tokens Saved" value={fmtNum(tokensSaved)} accent={cardAccents[0]} />
              <DataCard label="Total Requests" value={fmtNum(totalRequests)} accent={cardAccents[2]} />
              <DataCard label="KeiRouter Cost" value={`$${formatCost(actualCost)}`} accent={cardAccents[1]} />
              {costSaved > 0 ? (
                <DataCard label="Original Cost (Est)" value={`$${formatCost(actualCost + costSaved)}`} accent={cardAccents[3]} muted />
              ) : (
                <DataCard label="Original Cost (Est)" value={`$${formatCost(actualCost)}`} accent={cardAccents[3]} muted />
              )}
            </div>

            {/* Optimizers Strip */}
            <div style={{
              background: C.surface,
              border: `1px solid ${C.borderLight}`,
              borderRadius: 18,
              padding: "22px 24px",
              boxShadow: "0 8px 28px rgba(0,0,0,0.04)"
            }}>
              <span
                style={{
                  display: "block",
                  fontSize: 12,
                  fontWeight: 600,
                  color: C.textMuted,
                  textTransform: "uppercase",
                  letterSpacing: "0.1em",
                  marginBottom: 14,
                }}
              >
                Active Optimizers
              </span>
              <div style={{ display: "flex", gap: 12 }}>
                {optimizers.length > 0 ? optimizers.map((opt) => (
                  <div
                    key={opt.name}
                    style={{
                      padding: "9px 16px",
                      border: `1px solid rgba(5,150,105,0.2)`,
                      borderRadius: 10,
                      background: "rgba(5,150,105,0.06)",
                      display: "flex",
                      alignItems: "center",
                      gap: 8,
                    }}
                  >
                    <div style={{ width: 8, height: 8, borderRadius: "50%", background: C.positive }} />
                    <span style={{ fontSize: 14, fontWeight: 600, color: C.text }}>{opt.name}</span>
                  </div>
                )) : (
                  <span style={{ fontSize: 14, fontWeight: 500, color: C.textMuted }}>None active in this period</span>
                )}
              </div>
            </div>

          </div>
        </div>

        {/* Footer Bar */}
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            paddingTop: "26px",
            marginTop: "auto",
            borderTop: `1px solid ${C.border}`,
          }}
        >
          <span style={{ fontSize: 14, fontWeight: 600, color: C.textSecondary }}>
            keirouter.dev
          </span>
          <span style={{ fontSize: 14, fontWeight: 500, color: C.textMuted }}>
            AI Routing, Optimized.
          </span>
        </div>

      </div>
    </div>
  );
}

function DataCard({ label, value, muted, accent }: { label: string; value: string; muted?: boolean; accent?: string }) {
  return (
    <div style={{ 
      position: "relative",
      overflow: "hidden",
      background: C.surface,
      border: `1px solid ${C.borderLight}`,
      borderRadius: 18,
      padding: "22px 24px",
      display: "flex", 
      flexDirection: "column", 
      gap: "10px",
      boxShadow: "0 8px 28px rgba(0,0,0,0.04)"
    }}>
      {/* Accent bar */}
      <div style={{
        position: "absolute",
        top: 0,
        left: 0,
        width: 4,
        height: "100%",
        background: accent ?? C.positive,
        opacity: muted ? 0.35 : 1,
      }} />
      <span
        style={{
          fontSize: 13,
          fontWeight: 600,
          color: C.textMuted,
        }}
      >
        {label}
      </span>
      <span
        style={{
          fontSize: 30,
          fontWeight: 700,
          color: muted ? C.textSecondary : C.text,
          letterSpacing: "-0.02em",
        }}
      >
        {value}
      </span>
    </div>
  );
}

// ─── Share Button ────────────────────────────────────────────────────────────

export function SavingsCardShareButton({
  insights,
  period,
}: {
  insights: UsageInsights;
  period: string;
}) {
  const cardRef = useRef<HTMLDivElement>(null);
  const [generating, setGenerating] = useState(false);
  const [done, setDone] = useState(false);

  const { summary, savings } = insights;

  const tokensSaved = savings.total_tokens_saved;
  const costSaved = savings.usd_saved;
  const actualCost = summary.cost_usd;
  const originalCost = actualCost + costSaved;
  const savingsPct = originalCost > 0 ? (costSaved / originalCost) * 100 : 0;

  const cardData: SavingsCardData = {
    costSaved,
    costSavedEstimated: savings.usd_saved_estimate,
    tokensSaved,
    savingsPct,
    totalRequests: summary.total_requests,
    period,
    rtkActive: (savings?.slim_tokens_saved ?? 0) > 0,
    cavemanActive: (savings?.caveman_requests ?? 0) > 0,
    terseActive: (savings?.terse_requests ?? 0) > 0,
    actualCost,
  };

  const [status, setStatus] = useState("");

  const handleShare = useCallback(async () => {
    if (!cardRef.current || generating) return;
    setGenerating(true);
    setDone(false);
    setStatus("Creating savings card…");

    try {
      const dataUrl = await toPng(cardRef.current, {
        width: 1200,
        height: 630,
        pixelRatio: 2,
        cacheBust: true,
      });

      const link = document.createElement("a");
      link.download = `keirouter-savings-${period}.png`;
      link.href = dataUrl;
      link.click();
      setDone(true);
      setStatus("Savings card downloaded");
      setTimeout(() => setDone(false), 2000);
    } catch (err) {
      console.error("Failed to generate savings card:", err);
      setStatus("Couldn't create the savings card. Try again.");
    } finally {
      setGenerating(false);
    }
  }, [generating, period]);

  return (
    <>
      {/* Hidden card for image capture */}
      <div
        aria-hidden="true"
        style={{
          position: "fixed",
          left: "-9999px",
          top: 0,
          zIndex: -1,
          pointerEvents: "none",
        }}
      >
        <div ref={cardRef}>
          <SavingsCardContent data={cardData} />
        </div>
      </div>

      {/* Download button — a stable name; progress is announced separately. */}
      <button
        type="button"
        aria-label="Download savings card"
        title="Download savings card as PNG"
        onClick={handleShare}
        disabled={generating || summary.total_requests === 0}
        aria-busy={generating || undefined}
        className="inline-flex h-8 items-center gap-1.5 whitespace-nowrap rounded-lg border border-line-strong bg-surface px-3 text-[12.5px] font-medium text-fg transition-colors hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 disabled:cursor-not-allowed disabled:opacity-50"
      >
        {done ? (
          <>
            <Check className="h-3.5 w-3.5 text-ok" aria-hidden="true" />
            <span className="hidden sm:inline">Downloaded</span>
          </>
        ) : generating ? (
          <>
            <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-line-strong border-t-fg-muted" aria-hidden="true" />
            <span className="hidden sm:inline">Creating…</span>
          </>
        ) : (
          <>
            <Download className="h-3.5 w-3.5 text-fg-faint" aria-hidden="true" />
            <span className="hidden sm:inline">Savings card</span>
          </>
        )}
      </button>
      <span className="sr-only" role="status" aria-live="polite">
        {status}
      </span>
    </>
  );
}
