import { ExternalLink } from "lucide-react";
import { Children, isValidElement, useEffect, useMemo, useState } from "react";
import type { ReactElement, ReactNode } from "react";
import Markdown from "react-markdown";
import type { Components, Options } from "react-markdown";
import remarkGfm from "remark-gfm";
import { mayHaveMath, normalizeMath } from "../math";
import CodeFence from "./code-fence";
import MermaidDiagram from "./mermaid-diagram";

type Plugins = Pick<Options, "remarkPlugins" | "rehypePlugins">;

const PLAIN: Plugins = { remarkPlugins: [remarkGfm] };

let mathPlugins: Promise<Plugins> | null = null;

/**
 * KaTeX and its fonts load only once a reply looks like it holds math.
 * `trust: false` keeps \href and \url from making links; errors render as
 * the source instead of throwing.
 */
function loadMath(): Promise<Plugins> {
  mathPlugins ??= Promise.all([import("remark-math"), import("rehype-katex"), import("katex/dist/katex.min.css")]).then(
    ([math, katex]) => ({
      remarkPlugins: [remarkGfm, math.default],
      rehypePlugins: [
        [
          katex.default,
          {
            throwOnError: false,
            strict: "ignore",
            trust: false,
            output: "htmlAndMathml",
          },
        ],
      ],
    }),
  );

  return mathPlugins;
}

function useMarkdownPlugins(content: string): Plugins {
  const wantsMath = mayHaveMath(content);
  const [plugins, setPlugins] = useState<Plugins | null>(null);
  useEffect(() => {
    if (!wantsMath || plugins) {
      return;
    }
    let active = true;
    loadMath()
      .then((p) => active && setPlugins(p))
      .catch(() => {});

    return () => {
      active = false;
    };
  }, [wantsMath, plugins]);

  return wantsMath && plugins ? plugins : PLAIN;
}

/** The text of a code element's children (react-markdown passes one string). */
function textOf(node: ReactNode): string {
  if (typeof node === "string" || typeof node === "number") {
    return String(node);
  }

  return Array.isArray(node) ? node.map(textOf).join("") : "";
}

function components(streaming: boolean): Components {
  return {
    pre({ children }) {
      const child = Children.toArray(children)[0];
      if (!isValidElement(child)) {
        return <pre>{children}</pre>;
      }
      const props = (
        child as ReactElement<{
          className?: string;
          children?: ReactNode;
        }>
      ).props;
      const info = /language-([^\s]+)/.exec(props.className ?? "")?.[1] ?? null;
      const code = textOf(props.children).replace(/\n$/, "");
      if (info?.toLowerCase() === "mermaid") {
        return <MermaidDiagram code={code} streaming={streaming} />;
      }

      return <CodeFence code={code} info={info} streaming={streaming} />;
    },
    code({ children }) {
      return <code className="rounded-sm bg-subtle px-1 py-0.5 font-mono text-[0.9em]">{children}</code>;
    },
    a({ href, children }) {
      return (
        <a
          href={href}
          target="_blank"
          rel="noopener noreferrer nofollow"
          className="text-brand-ink underline decoration-1 underline-offset-4 hover:decoration-2"
        >
          {children}
        </a>
      );
    },
    // The CSP blocks remote images, and loading one would tell its host
    // the chat was read. Offer the link instead.
    img({ src, alt }) {
      if (typeof src !== "string" || !/^https?:\/\//i.test(src)) {
        return <span>{alt}</span>;
      }

      return (
        <a
          href={src}
          target="_blank"
          rel="noopener noreferrer nofollow"
          className="inline-flex items-center gap-1 text-brand-ink underline decoration-1 underline-offset-4 hover:decoration-2"
        >
          Image{alt ? `: ${alt}` : ""}
          <ExternalLink aria-hidden className="size-3.5" />
        </a>
      );
    },
    table({ children }) {
      return (
        <div className="my-4 overflow-x-auto rounded-lg border">
          <table className="w-full text-sm">{children}</table>
        </div>
      );
    },
    th({ children, style }) {
      return (
        <th style={style} className="border-b bg-subtle px-3 py-2 text-left font-medium">
          {children}
        </th>
      );
    },
    td({ children, style }) {
      return (
        <td style={style} className="border-b px-3 py-2 align-top">
          {children}
        </td>
      );
    },
    input({ type, checked }) {
      if (type !== "checkbox") {
        return null;
      }

      return (
        <input
          type="checkbox"
          checked={checked ?? false}
          disabled
          aria-label={checked ? "Done" : "Not done"}
          className="mr-1.5 size-3.5 translate-y-0.5 accent-(--brand)"
        />
      );
    },
  };
}

/**
 * A model's answer. Raw HTML in it is shown as text, never rendered
 * (react-markdown's default), and links open in a new tab without a referrer.
 */
export default function MarkdownMessage({
  content,
  streaming = false,
}: {
  content: string;
  /** The reply is still arriving: code highlights on an interval and diagrams wait. */
  streaming?: boolean;
}) {
  const plugins = useMarkdownPlugins(content);
  const source = useMemo(() => (plugins === PLAIN ? content : normalizeMath(content)), [content, plugins]);
  const parts = useMemo(() => components(streaming), [streaming]);

  return (
    <div className="text-body-lg [&_.contains-task-list]:list-none [&_.contains-task-list]:pl-1 [&_.footnotes]:mt-8 [&_.footnotes]:border-t [&_.footnotes]:pt-4 [&_.footnotes]:text-sm [&_.footnotes]:text-fg-muted [&_.katex-display]:my-4 [&_.katex-display]:overflow-x-auto [&_.katex-display]:overflow-y-hidden [&_.katex-display]:py-1 [&_blockquote]:my-3 [&_blockquote]:border-l-2 [&_blockquote]:pl-4 [&_blockquote]:text-fg-muted [&_del]:text-fg-muted [&_h1]:mt-6 [&_h1]:mb-3 [&_h1]:text-title [&_h1]:font-medium [&_h2]:mt-6 [&_h2]:mb-2 [&_h2]:text-heading [&_h2]:font-medium [&_h3]:mt-4 [&_h3]:mb-2 [&_h3]:font-medium [&_h4]:mt-4 [&_h4]:mb-1 [&_h4]:font-medium [&_h5]:mt-3 [&_h5]:font-medium [&_h6]:mt-3 [&_h6]:font-medium [&_h6]:text-fg-muted [&_hr]:my-6 [&_li]:my-1 [&_li>ol]:my-1 [&_li>ul]:my-1 [&_ol]:my-3 [&_ol]:list-decimal [&_ol]:pl-6 [&_p]:my-3 [&_sup>a]:no-underline [&_ul]:my-3 [&_ul]:list-disc [&_ul]:pl-6 [&>*:first-child]:mt-0 [&>*:last-child]:mb-0">
      <Markdown {...plugins} components={parts}>
        {source}
      </Markdown>
    </div>
  );
}
