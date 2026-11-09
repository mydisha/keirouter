import { Download } from "lucide-react";
import { useEffect, useState } from "react";
import { CopyButton } from "@/components/ui/code-block";
import { Button, SegmentedControl } from "@/components/ui";
import { newId } from "../id";
import CodeFence from "./code-fence";

type Mermaid = (typeof import("mermaid"))["default"];

let mermaid: Promise<Mermaid> | null = null;
const loadMermaid = () => (mermaid ??= import("mermaid").then((m) => m.default));

/** Theme tokens as hex: Mermaid's colour maths cannot read oklch(). */
function tokenHex(name: string): string {
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = 1;
  const ctx = canvas.getContext("2d", { willReadFrequently: true });
  if (!ctx) {
    return "#888888";
  }
  ctx.fillStyle = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  ctx.fillRect(0, 0, 1, 1);
  const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data;

  return `#${[r, g, b].map((v) => v.toString(16).padStart(2, "0")).join("")}`;
}

function useDarkTheme(): boolean {
  const read = () => typeof document !== "undefined" && document.documentElement.classList.contains("dark");
  const [dark, setDark] = useState(read);
  useEffect(() => {
    const observer = new MutationObserver(() => setDark(read()));
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
    });

    return () => observer.disconnect();
  }, []);

  return dark;
}

async function renderSvg(code: string, dark: boolean): Promise<string> {
  const m = await loadMermaid();
  m.initialize({
    startOnLoad: false,
    securityLevel: "strict",
    suppressErrorRendering: true,
    htmlLabels: false,
    flowchart: { htmlLabels: false },
    theme: "base",
    // The SVG is shown in an <img>, which cannot load the page's fonts;
    // measure and draw with the same system font.
    fontFamily: "ui-sans-serif, system-ui, sans-serif",
    themeVariables: {
      darkMode: dark,
      background: tokenHex("--bg"),
      primaryColor: tokenHex("--bg-elevated"),
      primaryTextColor: tokenHex("--text"),
      primaryBorderColor: tokenHex("--brand"),
      secondaryColor: tokenHex("--bg-subtle"),
      tertiaryColor: tokenHex("--bg-hover"),
      lineColor: tokenHex("--text-muted"),
      textColor: tokenHex("--text"),
      fontFamily: "ui-sans-serif, system-ui, sans-serif",
    },
  });
  const { svg } = await m.render(`mermaid-${newId()}`, code);

  return svg;
}

function saveSvg(svg: string) {
  const url = URL.createObjectURL(new Blob([svg], { type: "image/svg+xml" }));
  const link = document.createElement("a");
  link.href = url;
  link.download = "diagram.svg";
  link.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}

/**
 * A ```mermaid block drawn as a diagram once the reply has finished. The SVG
 * is shown through an <img>, so nothing in it can run script or reach the
 * page, whatever the model wrote.
 */
export default function MermaidDiagram({ code, streaming }: { code: string; streaming: boolean }) {
  const dark = useDarkTheme();
  const [view, setView] = useState<"diagram" | "code">("diagram");
  const [result, setResult] = useState<
    { key: string; svg: string; url: string } | { key: string; error: string } | null
  >(null);
  const key = `${dark}:${code}`;

  useEffect(() => {
    if (streaming) {
      return;
    }
    let active = true;
    let url: string | null = null;
    renderSvg(code, dark)
      .then((svg) => {
        if (!active) {
          return;
        }
        url = URL.createObjectURL(new Blob([svg], { type: "image/svg+xml" }));
        setResult({ key, svg, url });
      })
      .catch((e: unknown) => {
        if (active) {
          const message = e instanceof Error ? e.message : String(e);
          setResult({ key, error: message.split("\n")[0] });
        }
      });

    return () => {
      active = false;
      if (url) {
        URL.revokeObjectURL(url);
      }
    };
  }, [code, dark, streaming, key]);

  const current = result?.key === key ? result : null;
  if (streaming || view === "code" || (current && "error" in current)) {
    return (
      <div>
        {current && "error" in current && (
          <p className="mt-4 -mb-2 text-sm text-fg-muted">Couldn&rsquo;t draw this diagram: {current.error}</p>
        )}
        {!streaming && current && "svg" in current && <Toggle view={view} onChange={setView} />}
        <CodeFence code={code} info="mermaid" streaming={streaming} />
      </div>
    );
  }

  return (
    <figure className="my-4 overflow-hidden rounded-lg border">
      <figcaption className="flex h-10 items-center justify-between gap-2 border-b pr-1 pl-1.5">
        <Toggle view={view} onChange={setView} />
        <div className="flex items-center">
          {current && "svg" in current && (
            <Button
              type="button"
              size="icon"
              variant="quiet"
              aria-label="Download diagram.svg"
              onClick={() => saveSvg(current.svg)}
              className="size-8"
            >
              <Download aria-hidden />
            </Button>
          )}
          <CopyButton value={code} label="Copy diagram code" />
        </div>
      </figcaption>
      <div className="flex min-h-24 items-center justify-center overflow-x-auto bg-canvas p-4">
        {current && "url" in current ? (
          <img
            src={current.url}
            alt="Diagram drawn from the Mermaid code in this reply"
            className="h-auto max-w-full"
          />
        ) : (
          <span className="text-sm text-fg-muted">Drawing diagram…</span>
        )}
      </div>
    </figure>
  );
}

function Toggle({ view, onChange }: { view: "diagram" | "code"; onChange: (view: "diagram" | "code") => void }) {
  return (
    <SegmentedControl
      label="Show"
      value={view}
      onChange={onChange}
      options={[
        { value: "diagram", label: "Diagram" },
        { value: "code", label: "Code" },
      ]}
    />
  );
}
