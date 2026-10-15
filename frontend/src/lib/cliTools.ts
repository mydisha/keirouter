import type { CLITool } from "./api";

// Tool metadata shared by the CLI tools list and detail pages — descriptions,
// logos (bundled /providers/*.png assets) and install commands.
export const cliToolMeta: Record<string, { description: string; image: string; installCmd?: string }> = {
  claude:   { description: "Anthropic's CLI coding agent", image: "/providers/claude.png", installCmd: "npm install -g @anthropic-ai/claude-code" },
  codex:    { description: "OpenAI Codex CLI", image: "/providers/codex.png", installCmd: "npm install -g @openai/codex" },
  cline:    { description: "VS Code AI coding assistant", image: "/providers/cline.png" },
  copilot:  { description: "GitHub Copilot Chat", image: "/providers/copilot.png" },
  droid:    { description: "Factory Droid CLI", image: "/providers/droid.png", installCmd: "curl -fsSL https://factory.ai/install.sh | sh" },
  openclaw: { description: "OpenClaw agent framework", image: "/providers/openclaw.png", installCmd: "npm install -g openclaw" },
  opencode: { description: "OpenCode multi-model agent", image: "/providers/opencode.png", installCmd: "npm install -g opencode-ai" },
  kilo:     { description: "Kilo Code AI assistant", image: "/providers/kilocode.png", installCmd: "npm install -g @kilocode/cli" },
  hermes:   { description: "Hermes Agent CLI", image: "/providers/hermes.png", installCmd: "npm install -g hermes-agent" },
  deepseek: { description: "DeepSeek TUI", image: "/providers/deepseek-tui.png", installCmd: "npm install -g deepseek-tui" },
  jcode:    { description: "jcode coding agent", image: "/providers/jcode.png", installCmd: "npm install -g jcode" },
};

export type CLIConnectionKind = "connected" | "attention" | "ready" | "missing";

export interface CLIConnection {
  kind: CLIConnectionKind;
  tone: "success" | "warning" | "danger" | "neutral" | "secondary";
  label: string;
  /** One sentence on what is wrong / what to do; empty when connected. */
  detail: string;
}

// cliConnection condenses a tool's detection result into one state. "Connected"
// means the config points at this gateway with a key the gateway accepts;
// anything configured but off (other endpoint, rejected key, unreadable file)
// needs attention.
export function cliConnection(t: CLITool): CLIConnection {
  const s = t.settings;
  if (t.error) {
    return { kind: "attention", tone: "danger", label: "Config unreadable", detail: t.error };
  }
  if (t.configured && s) {
    // A foreign endpoint comes first: its key belongs to that service, so
    // judging it against this gateway's keys would be misleading.
    if (!s.endpoint_match) {
      return { kind: "attention", tone: "warning", label: "Other endpoint", detail: `It points at ${s.base_url}, not this gateway. Fine if that's a tunnel or LAN address for this gateway; otherwise write the config again.` };
    }
    if (s.key_status === "missing") {
      return { kind: "attention", tone: "warning", label: "No key", detail: "The config has no API key, so every request will be rejected. Write the config again with a key." };
    }
    if (s.key_status === "invalid") {
      return { kind: "attention", tone: "warning", label: "Key rejected", detail: "The stored API key isn't valid for this gateway — it may have been deleted. Write the config again with a current key." };
    }
    if (s.key_status === "disabled") {
      return { kind: "attention", tone: "warning", label: "Key disabled", detail: `The stored key${s.key_name ? ` "${s.key_name}"` : ""} is disabled. Enable it on the API keys page or pick another key.` };
    }
    return { kind: "connected", tone: "success", label: "Connected", detail: "" };
  }
  if (t.installed) {
    return { kind: "ready", tone: "secondary", label: "Not configured", detail: "" };
  }
  return { kind: "missing", tone: "neutral", label: "Not installed", detail: "" };
}

/** Sort rank: problems first (they need action), then connected, ready, missing. */
export const cliConnectionRank: Record<CLIConnectionKind, number> = { attention: 0, connected: 1, ready: 2, missing: 3 };

/** Human wording for how a tool was detected. */
export function detectedViaLabel(t: CLITool): string {
  const via = t.detected_via ?? [];
  if (via.includes("binary")) return "CLI on PATH";
  if (via.includes("extension")) return "Editor extension";
  if (via.includes("config")) return "Config file only";
  return "";
}
