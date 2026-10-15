// A tiny, dependency-free syntax highlighter for the snippets the dashboard
// shows: shell commands, env files, JSON, TOML, YAML, Python and JS/TS. It is
// a sticky-regex tokenizer, not a parser: good enough to make keys, strings
// and flags scannable, cheap enough to run on every render, and it never
// drops a character (unmatched input falls through as plain text).

export type CodeLanguage = "bash" | "env" | "json" | "toml" | "yaml" | "python" | "js" | "text";

export type TokenType = "plain" | "key" | "str" | "num" | "kw" | "com" | "var" | "flag" | "fn" | "punc";

export interface Token {
  type: TokenType;
  value: string;
}

type Rule = [TokenType, RegExp];

// Every pattern is compiled with the sticky flag so it only matches at the
// current scan position.
const sticky = (re: RegExp) => new RegExp(re.source, re.flags.includes("y") ? re.flags : `${re.flags}y`);
const rules = (list: [TokenType, RegExp][]): Rule[] => list.map(([t, re]) => [t, sticky(re)]);

const STRINGS: [TokenType, RegExp][] = [
  ["str", /"(?:[^"\\\n]|\\.)*"?/],
  ["str", /'(?:[^'\\\n]|\\.)*'?/],
];

const NUMBER: [TokenType, RegExp] = ["num", /-?\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b/];

const LANGS: Record<Exclude<CodeLanguage, "text">, Rule[]> = {
  json: rules([
    ["key", /"(?:[^"\\\n]|\\.)*"(?=\s*:)/],
    ...STRINGS,
    NUMBER,
    ["kw", /\b(?:true|false|null)\b/],
    ["punc", /[{}[\],:]/],
  ]),
  bash: rules([
    ["com", /(?:^|(?<=\s))#.*/m],
    ["str", /"(?:[^"\\]|\\.)*"?/],
    ["str", /'[^']*'?/],
    ["var", /\$\{[^}\n]*\}?|\$[A-Za-z_][A-Za-z0-9_]*/],
    ["kw", /\b(?:export|if|then|else|fi|for|do|done|while|in|case|esac|function|sudo)\b/],
    ["key", /[A-Za-z_][A-Za-z0-9_]*(?==)/],
    // A command: the first word on a line or after a pipe / && / ; / (.
    ["fn", /(?:(?<=^\s*)|(?<=[|;&(]\s*))[A-Za-z_./~][\w./~-]*/m],
    ["flag", /(?<=\s)--?[A-Za-z][\w-]*/],
    ["punc", /\\$|[|&;<>()=]/m],
  ]),
  env: rules([
    ["com", /#.*/],
    ["kw", /\bexport\b/],
    ["key", /[A-Za-z_][A-Za-z0-9_.]*(?=\s*=)/],
    ["var", /\$\{[^}\n]*\}?|\$[A-Za-z_][A-Za-z0-9_]*/],
    ["str", /"(?:[^"\\\n]|\\.)*"?|'[^'\n]*'?/],
    ["punc", /=/],
  ]),
  toml: rules([
    ["com", /#.*/],
    ["kw", /^\s*\[\[?[^\]\n]*\]\]?/m],
    ["key", /[A-Za-z0-9_.-]+(?=\s*=)/],
    ["str", /"""[\s\S]*?(?:"""|$)|'''[\s\S]*?(?:'''|$)/],
    ...STRINGS,
    ["kw", /\b(?:true|false)\b/],
    NUMBER,
    ["punc", /[=[\]{},]/],
  ]),
  yaml: rules([
    ["com", /(?:^|(?<=\s))#.*/m],
    ["key", /[A-Za-z0-9_.\-/"']+(?=\s*:(?:\s|$))/],
    ...STRINGS,
    ["kw", /\b(?:true|false|null|yes|no|on|off)\b/],
    NUMBER,
    ["punc", /^\s*-(?=\s)|[:|>[\]{},]/m],
  ]),
  python: rules([
    ["com", /#.*/],
    ["str", /[rbfu]{0,2}(?:"""[\s\S]*?(?:"""|$)|'''[\s\S]*?(?:'''|$))/i],
    ["str", /[rbfu]{0,2}(?:"(?:[^"\\\n]|\\.)*"?|'(?:[^'\\\n]|\\.)*'?)/i],
    ["kw", /\b(?:import|from|as|def|class|return|if|elif|else|for|while|in|not|and|or|with|try|except|finally|raise|lambda|yield|async|await|pass|None|True|False|is|print)\b/],
    ["fn", /@[A-Za-z_][\w.]*|[A-Za-z_]\w*(?=\()/],
    ["key", /[A-Za-z_]\w*(?==(?!=))/],
    NUMBER,
    ["punc", /[()[\]{},.:=]/],
  ]),
  js: rules([
    ["com", /\/\/.*|\/\*[\s\S]*?(?:\*\/|$)/],
    ["str", /`(?:[^`\\]|\\.)*`?/],
    ...STRINGS,
    ["kw", /\b(?:import|from|export|default|const|let|var|function|return|async|await|new|if|else|for|of|in|while|class|extends|try|catch|finally|throw|typeof|null|undefined|true|false|this)\b/],
    ["key", /[A-Za-z_$][\w$]*(?=\s*:(?!:))/],
    ["fn", /[A-Za-z_$][\w$]*(?=\()/],
    NUMBER,
    ["punc", /[()[\]{},.;:=>]/],
  ]),
};

/** Splits `code` into typed tokens. Unknown languages come back as one plain token. */
export function tokenize(code: string, lang: CodeLanguage): Token[] {
  if (lang === "text" || !code) return [{ type: "plain", value: code }];
  const list = LANGS[lang];
  const out: Token[] = [];
  let plain = "";
  let i = 0;
  scan: while (i < code.length) {
    for (const [type, re] of list) {
      re.lastIndex = i;
      const m = re.exec(code);
      if (m && m[0].length > 0) {
        if (plain) {
          out.push({ type: "plain", value: plain });
          plain = "";
        }
        out.push({ type, value: m[0] });
        i += m[0].length;
        continue scan;
      }
    }
    plain += code[i];
    i += 1;
  }
  if (plain) out.push({ type: "plain", value: plain });
  return out;
}

const EXTENSIONS: Record<string, CodeLanguage> = {
  json: "json",
  jsonc: "json",
  toml: "toml",
  yaml: "yaml",
  yml: "yaml",
  env: "env",
  sh: "bash",
  bash: "bash",
  zsh: "bash",
  ps1: "bash",
  py: "python",
  js: "js",
  mjs: "js",
  cjs: "js",
  ts: "js",
  tsx: "js",
};

/** Picks a language from a file name (".env", "config.toml") or by sniffing the content. */
export function detectLanguage(code: string, filename?: string): CodeLanguage {
  if (filename) {
    const base = filename.split(/[\\/]/).pop()?.toLowerCase() ?? "";
    if (base === ".env" || base.startsWith(".env.") || base.endsWith(".env")) return "env";
    if (base.endsWith("rc") && !base.includes(".")) return "bash";
    const ext = base.includes(".") ? base.split(".").pop()! : "";
    if (EXTENSIONS[ext]) return EXTENSIONS[ext];
  }
  const head = code.trimStart();
  if (/^[{[]/.test(head)) {
    try {
      JSON.parse(code);
      return "json";
    } catch {
      /* not JSON; keep sniffing */
    }
  }
  if (/^\s*\[[\w.-]+\]\s*$/m.test(code) && /^\s*[\w.-]+\s*=/m.test(code)) return "toml";
  if (/^(?:curl|export|npx|npm|pip|pipx|claude|codex|brew|cd|set|setx|\$env:)\b/m.test(head)) return "bash";
  if (/^[A-Z_][A-Z0-9_]*=/m.test(head)) return "env";
  if (/^(?:from|import)\s+\w/m.test(head)) return /\bfrom\s+["']/.test(head) || /\bconst\b/.test(head) ? "js" : "python";
  return "text";
}
