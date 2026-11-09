import type { HighlighterCore, LanguageRegistration, ThemedToken } from "shiki/core";

type Grammar = () => Promise<{ default: LanguageRegistration[] }>;

/**
 * Each grammar is its own chunk, fetched the first time a block in that
 * language shows up. The import paths must stay literal for the bundler.
 */
const GRAMMARS = {
  bash: () => import("shiki/langs/bash.mjs"),
  c: () => import("shiki/langs/c.mjs"),
  cpp: () => import("shiki/langs/cpp.mjs"),
  csharp: () => import("shiki/langs/csharp.mjs"),
  css: () => import("shiki/langs/css.mjs"),
  dart: () => import("shiki/langs/dart.mjs"),
  diff: () => import("shiki/langs/diff.mjs"),
  dockerfile: () => import("shiki/langs/dockerfile.mjs"),
  elixir: () => import("shiki/langs/elixir.mjs"),
  go: () => import("shiki/langs/go.mjs"),
  graphql: () => import("shiki/langs/graphql.mjs"),
  haskell: () => import("shiki/langs/haskell.mjs"),
  hcl: () => import("shiki/langs/hcl.mjs"),
  html: () => import("shiki/langs/html.mjs"),
  ini: () => import("shiki/langs/ini.mjs"),
  java: () => import("shiki/langs/java.mjs"),
  javascript: () => import("shiki/langs/javascript.mjs"),
  json: () => import("shiki/langs/json.mjs"),
  jsonc: () => import("shiki/langs/jsonc.mjs"),
  jsx: () => import("shiki/langs/jsx.mjs"),
  kotlin: () => import("shiki/langs/kotlin.mjs"),
  lua: () => import("shiki/langs/lua.mjs"),
  makefile: () => import("shiki/langs/makefile.mjs"),
  markdown: () => import("shiki/langs/markdown.mjs"),
  php: () => import("shiki/langs/php.mjs"),
  powershell: () => import("shiki/langs/powershell.mjs"),
  proto: () => import("shiki/langs/proto.mjs"),
  python: () => import("shiki/langs/python.mjs"),
  r: () => import("shiki/langs/r.mjs"),
  ruby: () => import("shiki/langs/ruby.mjs"),
  rust: () => import("shiki/langs/rust.mjs"),
  scala: () => import("shiki/langs/scala.mjs"),
  scss: () => import("shiki/langs/scss.mjs"),
  sql: () => import("shiki/langs/sql.mjs"),
  svelte: () => import("shiki/langs/svelte.mjs"),
  swift: () => import("shiki/langs/swift.mjs"),
  toml: () => import("shiki/langs/toml.mjs"),
  tsx: () => import("shiki/langs/tsx.mjs"),
  typescript: () => import("shiki/langs/typescript.mjs"),
  vue: () => import("shiki/langs/vue.mjs"),
  xml: () => import("shiki/langs/xml.mjs"),
  yaml: () => import("shiki/langs/yaml.mjs"),
} satisfies Record<string, Grammar>;

export type CodeLanguage = keyof typeof GRAMMARS;

export const CODE_LANGUAGES = Object.keys(GRAMMARS) as CodeLanguage[];

const ALIASES: Record<string, CodeLanguage> = {
  sh: "bash",
  shell: "bash",
  shellscript: "bash",
  zsh: "bash",
  console: "bash",
  terminal: "bash",
  "c++": "cpp",
  cc: "cpp",
  hpp: "cpp",
  cs: "csharp",
  "c#": "csharp",
  docker: "dockerfile",
  patch: "diff",
  ex: "elixir",
  exs: "elixir",
  golang: "go",
  gql: "graphql",
  hs: "haskell",
  tf: "hcl",
  terraform: "hcl",
  htm: "html",
  conf: "ini",
  cfg: "ini",
  properties: "ini",
  js: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  node: "javascript",
  kt: "kotlin",
  kts: "kotlin",
  make: "makefile",
  mk: "makefile",
  md: "markdown",
  ps: "powershell",
  ps1: "powershell",
  pwsh: "powershell",
  protobuf: "proto",
  py: "python",
  python3: "python",
  rb: "ruby",
  rs: "rust",
  postgres: "sql",
  postgresql: "sql",
  mysql: "sql",
  sqlite: "sql",
  ts: "typescript",
  mts: "typescript",
  cts: "typescript",
  svg: "xml",
  plist: "xml",
  yml: "yaml",
};

const LABELS: Partial<Record<CodeLanguage, string>> = {
  bash: "Bash",
  c: "C",
  cpp: "C++",
  csharp: "C#",
  css: "CSS",
  diff: "Diff",
  graphql: "GraphQL",
  hcl: "HCL",
  html: "HTML",
  ini: "INI",
  javascript: "JavaScript",
  json: "JSON",
  jsonc: "JSONC",
  jsx: "JSX",
  php: "PHP",
  powershell: "PowerShell",
  proto: "Protobuf",
  r: "R",
  scss: "SCSS",
  sql: "SQL",
  toml: "TOML",
  tsx: "TSX",
  typescript: "TypeScript",
  xml: "XML",
  yaml: "YAML",
};

const EXTENSIONS: Partial<Record<CodeLanguage, string>> = {
  bash: "sh",
  cpp: "cpp",
  csharp: "cs",
  dockerfile: "Dockerfile",
  elixir: "ex",
  haskell: "hs",
  hcl: "tf",
  javascript: "js",
  kotlin: "kt",
  makefile: "Makefile",
  markdown: "md",
  powershell: "ps1",
  python: "py",
  ruby: "rb",
  rust: "rs",
  typescript: "ts",
  yaml: "yml",
};

/** The grammar for a fence's info string (`ts`, `Python`, `c++`), if any. */
export function resolveLanguage(name: string | null | undefined): CodeLanguage | null {
  const key = name?.trim().toLowerCase();
  if (!key) {
    return null;
  }
  if (key in GRAMMARS) {
    return key as CodeLanguage;
  }

  return ALIASES[key] ?? null;
}

export function languageLabel(language: CodeLanguage): string {
  return LABELS[language] ?? language.charAt(0).toUpperCase() + language.slice(1);
}

/** A file name to save a snippet under: `snippet.ts`, or `Dockerfile` as is. */
export function snippetFileName(language: CodeLanguage | null): string {
  if (!language) {
    return "snippet.txt";
  }
  const ext = EXTENSIONS[language] ?? language;

  return /^[A-Z]/.test(ext) ? ext : `snippet.${ext}`;
}

export type CodeLine = Pick<ThemedToken, "content" | "color" | "fontStyle">[];

let highlighter: Promise<HighlighterCore> | null = null;
const loaded = new Map<CodeLanguage, Promise<void>>();

/**
 * Shiki with the JavaScript regex engine (no WASM), loaded on first use so
 * pages without code never pay for it. Colors are CSS variables
 * (`--code-token-*` in app.css), so one tokenisation serves light and dark.
 */
function load(): Promise<HighlighterCore> {
  highlighter ??= Promise.all([import("shiki/core"), import("shiki/engine/javascript")]).then(([core, engine]) =>
    core.createHighlighterCore({
      themes: [
        core.createCssVariablesTheme({
          name: "kb",
          variablePrefix: "--code-",
          fontStyle: true,
        }),
      ],
      langs: [],
      engine: engine.createJavaScriptRegexEngine(),
    }),
  );

  return highlighter;
}

async function grammar(shiki: HighlighterCore, language: CodeLanguage): Promise<void> {
  let pending = loaded.get(language);
  if (!pending) {
    pending = GRAMMARS[language]().then((m) => shiki.loadLanguage(...m.default));
    // A failed chunk fetch may succeed on the next block.
    pending.catch(() => loaded.delete(language));
    loaded.set(language, pending);
  }
  await pending;
}

export async function tokenize(code: string, language: CodeLanguage): Promise<CodeLine[]> {
  const shiki = await load();
  await grammar(shiki, language);

  return shiki.codeToTokens(code, { lang: language, theme: "kb" }).tokens;
}
