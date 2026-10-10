import type { ComponentType } from "react";
import type { OAuthProvider, Provider } from "../../lib/api";
import { ApiKeyConnect, BulkKeyImport } from "./ApiKeyConnect";
import { OAuthConnect } from "./OAuthConnect";
import {
  CodebuddyConnect,
  CommandCodeConnect,
  CursorConnect,
  KilocodeConnect,
  KimchiConnect,
  KiroConnect,
  QoderConnect,
  type VendorDialogProps,
} from "./VendorConnect";

// Providers whose sign-in is not the generic OAuth or API-key form get a
// dedicated dialog. Adding a vendor is one entry here plus its dialog.
const VENDOR_DIALOGS: Record<string, ComponentType<VendorDialogProps>> = {
  kiro: KiroConnect,
  qoder: QoderConnect,
  kilocode: KilocodeConnect,
  codebuddy: CodebuddyConnect,
  kimchi: KimchiConnect,
  cursor: CursorConnect,
  commandcode: CommandCodeConnect,
};

export type ConnectMode = "primary" | "api_key" | "bulk";

export interface ConnectOptions {
  /** Label for the main connect action. */
  primaryLabel: string;
  /** The primary action is a sign-in flow rather than a key form. */
  primaryIsSignIn: boolean;
  /** An API-key form is offered in addition to a sign-in flow. */
  alsoApiKey: boolean;
  /** Many keys can be pasted at once. */
  bulk: boolean;
}

// connectOptions decides which connect actions a provider page offers.
export function connectOptions(provider: Provider, oauth?: OAuthProvider): ConnectOptions {
  const vendor = !!VENDOR_DIALOGS[provider.id];
  const supportsKey = provider.auth_modes.includes("api_key") || provider.auth_kind === "api_key";
  const manual = !vendor && (supportsKey || provider.auth_modes.includes("none") || !oauth);
  const signIn = vendor || !!oauth;
  return {
    primaryLabel: signIn ? `Connect ${provider.display_name}` : provider.auth_kind === "none" ? "Enable provider" : "Add API key",
    primaryIsSignIn: signIn,
    alsoApiKey: !vendor && !!oauth && manual,
    // Qoder's PATs bulk-import through the shared endpoint even though it has
    // a vendor dialog; Azure keys each need their own endpoint + deployment.
    bulk: (manual || provider.id === "qoder") && supportsKey && provider.id !== "azure",
  };
}

export function ConnectProviderDialog({
  provider,
  oauth,
  mode,
  onClose,
}: {
  provider: Provider;
  oauth?: OAuthProvider;
  mode: ConnectMode;
  onClose: () => void;
}) {
  const logo = { icon: provider.icon, name: provider.display_name };
  if (mode === "bulk") return <BulkKeyImport provider={provider} onClose={onClose} />;
  if (mode === "api_key") return <ApiKeyConnect provider={provider} onClose={onClose} />;
  const Vendor = VENDOR_DIALOGS[provider.id];
  if (Vendor) return <Vendor logo={logo} onClose={onClose} />;
  if (oauth) return <OAuthConnect provider={oauth} logo={logo} onClose={onClose} />;
  return <ApiKeyConnect provider={provider} onClose={onClose} />;
}
