const OPEN = "<think>";
const CLOSE = "</think>";

/**
 * Some models write their reasoning inline as `<think>…</think>` before the
 * answer, and the gateway relays OpenAI-dialect streams as they are. While
 * streaming, a tag can be cut anywhere: a content that may still become
 * `<think>` is held back, and an unclosed block is all reasoning.
 */
export function splitInlineThinking(raw: string): {
  reasoning: string;
  content: string;
} {
  const text = raw.trimStart();
  if (text.length < OPEN.length && OPEN.startsWith(text)) {
    return { reasoning: "", content: "" };
  }
  if (!text.startsWith(OPEN)) {
    return { reasoning: "", content: raw };
  }
  const end = text.indexOf(CLOSE);
  if (end < 0) {
    return { reasoning: text.slice(OPEN.length).trim(), content: "" };
  }

  return {
    reasoning: text.slice(OPEN.length, end).trim(),
    content: text.slice(end + CLOSE.length).trimStart(),
  };
}
