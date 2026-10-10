import { useEffect, useRef, useState } from "react";
import { Building2, ExternalLink, FileJson, FileUp, KeyRound, Shield } from "lucide-react";
import { api } from "../../lib/api";
import { useToast } from "../Toast";
import {
  ConnectDialog,
  ConnectError,
  Connected,
  DeviceWaiting,
  ExternalTextLink,
  FormError,
  InlineCode,
  MethodPicker,
  PrimaryAction,
  SecondaryAction,
  Starting,
  Steps,
  TextAreaField,
  TextField,
  useDevicePoll,
  useTokenImport,
  type ConnectMethod,
} from "./ConnectKit";

// Provider-specific connect dialogs. Each one describes only what is unique
// about that vendor — its methods, instructions and endpoints — and leaves
// layout, polling, focus and feedback to the connect kit.

export interface VendorDialogProps {
  logo: { icon?: string; name: string };
  onClose: () => void;
}

// ── KiloCode: device authorization ───────────────────────────────────────────

export function KilocodeConnect({ logo, onClose }: VendorDialogProps) {
  const flow = useDevicePoll({
    providerName: "KiloCode",
    start: () => api.kilocodeDeviceStart(),
    poll: (code) => api.kilocodeDevicePoll(code),
    onConnected: onClose,
  });
  return (
    <ConnectDialog title="Connect KiloCode" description="Sign in with your KiloCode account using a one-time device code." logo={logo} onClose={onClose}>
      <DeviceFlowBody
        flow={flow}
        name="KiloCode"
        startingText="Requesting a device code…"
        idle={
          <Steps
            steps={[
              "KeiRouter requests a one-time code from KiloCode.",
              "You open the verification page and enter the code.",
              "Once you approve, the token is encrypted and stored.",
            ]}
          />
        }
        startLabel="Get a device code"
        onClose={onClose}
      />
    </ConnectDialog>
  );
}

// ── CodeBuddy: browser approval with polling ─────────────────────────────────

export function CodebuddyConnect({ logo, onClose }: VendorDialogProps) {
  const flow = useDevicePoll({
    providerName: "CodeBuddy",
    start: () => api.codebuddyAuthStart(),
    poll: (code) => api.codebuddyAuthPoll(code),
    autoOpen: true,
    onConnected: onClose,
  });
  return (
    <ConnectDialog title="Connect CodeBuddy" description="Authorize with your Tencent CodeBuddy account in the browser." logo={logo} onClose={onClose}>
      <DeviceFlowBody
        flow={flow}
        name="CodeBuddy"
        startingText="Creating a sign-in session…"
        openLabel="Open CodeBuddy sign-in"
        idle={
          <Steps
            steps={[
              "A CodeBuddy sign-in page opens in a new tab.",
              "Sign in with your Tencent account and approve access.",
              "KeiRouter picks up the token automatically.",
            ]}
          />
        }
        startLabel="Open CodeBuddy sign-in"
        onClose={onClose}
      />
    </ConnectDialog>
  );
}

// ── Kimchi: browser callback, with a manual fallback ────────────────────────

export function KimchiConnect({ logo, onClose }: VendorDialogProps) {
  const toast = useToast();
  const [manualUrl, setManualUrl] = useState("");
  const [manualOpen, setManualOpen] = useState(false);
  const flow = useDevicePoll({
    providerName: "Kimchi",
    start: () => api.kimchiAuthStart(),
    poll: (code) => api.kimchiAuthPoll(code),
    autoOpen: true,
    minInterval: 0,
    onConnected: onClose,
  });

  // The callback page posts a message to its opener; check immediately.
  const { pollNow } = flow;
  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      if (e.data?.type === "kimchi-callback" && e.data?.status === "success") pollNow();
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [pollNow]);

  const checkManual = () => {
    try {
      const u = new URL(manualUrl.trim());
      if (!u.searchParams.get("state") || !u.searchParams.get("token")) {
        toast.error("That isn't the callback URL", "It must contain both state and token parameters.");
        return;
      }
      // The gateway already processed the redirect if it reached it; polling
      // now picks the token up either way.
      pollNow();
      toast.info("Checking callback", "Looking for your token…");
    } catch {
      toast.error("Invalid URL", "Paste the full URL from the browser's address bar.");
    }
  };

  return (
    <ConnectDialog title="Connect Kimchi" description="Authorize KeiRouter from your Kimchi account in the browser." logo={logo} onClose={onClose}>
      <DeviceFlowBody
        flow={flow}
        name="Kimchi"
        startingText="Creating a sign-in session…"
        openLabel="Open Kimchi sign-in"
        hint="Approve access in the other tab — Kimchi redirects back here automatically."
        idle={
          <Steps
            steps={[
              "A Kimchi sign-in page opens in a new tab.",
              "Authorize KeiRouter in the browser.",
              "Kimchi redirects back with your token, which is encrypted and stored.",
            ]}
          />
        }
        startLabel="Open Kimchi sign-in"
        onClose={onClose}
        waitingExtra={
          <div className="border-t border-line pt-3">
            <button
              type="button"
              onClick={() => setManualOpen((v) => !v)}
              aria-expanded={manualOpen}
              className="text-[12.5px] font-medium text-fg-muted underline-offset-2 hover:text-fg hover:underline"
            >
              {manualOpen ? "Hide manual callback" : "Redirect didn't come back? Paste the callback URL"}
            </button>
            {manualOpen && (
              <div className="mt-2.5 space-y-2.5">
                <TextField
                  label="Callback URL"
                  value={manualUrl}
                  onChange={(e) => setManualUrl(e.target.value)}
                  placeholder="http://127.0.0.1:20180/kimchi/callback?token=…&state=…"
                  className="font-mono text-[12px]"
                />
                <div className="flex justify-end">
                  <SecondaryAction onClick={checkManual} disabled={!manualUrl.trim()}>
                    Check callback
                  </SecondaryAction>
                </div>
              </div>
            )}
          </div>
        }
      />
    </ConnectDialog>
  );
}

// ── Qoder: OAuth device flow or Personal Access Token ───────────────────────

type QoderMethod = "oauth" | "pat";
const QODER_METHODS: ConnectMethod<QoderMethod>[] = [
  { id: "oauth", title: "Sign in with Qoder", description: "Authorize in the browser — nothing to copy.", icon: ExternalLink, recommended: true },
  { id: "pat", title: "Personal Access Token", description: "Paste a pt-… token from your Qoder integrations page.", icon: KeyRound },
];

export function QoderConnect({ logo, onClose }: VendorDialogProps) {
  const [method, setMethod] = useState<QoderMethod | null>(null);
  const [token, setToken] = useState("");
  const [label, setLabel] = useState("");
  const flow = useDevicePoll({
    providerName: "Qoder",
    start: () => api.qoderDeviceStart(),
    poll: (code) => api.qoderDevicePoll(code),
    onConnected: onClose,
  });
  const pat = useTokenImport((value) => api.createAccount({ provider: "qoder", label: label.trim(), api_key: value }), "Qoder", onClose);

  const back = method ? () => setMethod(null) : undefined;
  return (
    <ConnectDialog
      title="Connect Qoder"
      description={method === "pat" ? "Personal Access Tokens are validated and encrypted before storage." : "Choose how to connect your Qoder account."}
      logo={logo}
      onClose={onClose}
      onBack={flow.status === "waiting" || pat.done ? undefined : back}
    >
      {method === null && <MethodPicker methods={QODER_METHODS} onSelect={setMethod} />}
      {method === "oauth" && (
        <DeviceFlowBody
          flow={flow}
          name="Qoder"
          startingText="Generating a secure sign-in challenge…"
          openLabel="Open Qoder account picker"
          hint="Pick your account in the Qoder tab. The link expires in 5 minutes."
          idle={
            <Steps
              steps={[
                "KeiRouter generates a PKCE challenge locally.",
                "A Qoder page opens for you to pick your account.",
                "Once authorized, the token is encrypted and stored.",
              ]}
            />
          }
          startLabel="Open Qoder sign-in"
          onClose={onClose}
        />
      )}
      {method === "pat" &&
        (pat.done ? (
          <Connected name="Qoder" />
        ) : (
          <form
            className="space-y-3.5"
            onSubmit={(e) => {
              e.preventDefault();
              pat.run(token);
            }}
          >
            <p className="text-[13px] leading-5 text-fg-muted">
              Create a token on your <ExternalTextLink href="https://qoder.com/account/integrations">Qoder integrations page</ExternalTextLink>, then paste it below.
            </p>
            <TextField label="Personal Access Token" value={token} onChange={(e) => setToken(e.target.value)} placeholder="pt-…" className="font-mono" autoComplete="off" />
            <TextField label="Label" optional value={label} onChange={(e) => setLabel(e.target.value)} placeholder="Qoder" />
            <FormError message={pat.error} />
            <div className="flex justify-end">
              <PrimaryAction type="submit" busy={pat.busy} disabled={!token.trim()}>
                {pat.busy ? "Validating…" : "Connect Qoder"}
              </PrimaryAction>
            </div>
          </form>
        ))}
    </ConnectDialog>
  );
}

// ── Kiro: five methods, including AWS SSO device authorization ──────────────

type KiroMethod = "builder-id" | "idc" | "import" | "cli-proxy" | "api-key";
const KIRO_METHODS: ConnectMethod<KiroMethod>[] = [
  { id: "builder-id", title: "AWS Builder ID", description: "Sign in with a free AWS Builder ID. Best for most people.", icon: Shield, recommended: true },
  { id: "idc", title: "AWS IAM Identity Center", description: "For organizations using their own IAM Identity Center start URL.", icon: Building2 },
  { id: "import", title: "Import a refresh token", description: "Paste the refresh token exported from the Kiro IDE.", icon: FileUp },
  { id: "cli-proxy", title: "Import CLIProxy auth JSON", description: "An external_idp credential from a Microsoft login.", icon: FileJson },
  { id: "api-key", title: "API key", description: "A headless CodeWhisperer API key — used as-is, no refresh.", icon: KeyRound },
];

export function KiroConnect({ logo, onClose }: VendorDialogProps) {
  const [method, setMethod] = useState<KiroMethod | null>(null);
  const [idc, setIdc] = useState<{ startUrl: string; region: string } | null>(null);
  const [startUrl, setStartUrl] = useState("");
  const [region, setRegion] = useState("us-east-1");
  const [value, setValue] = useState("");
  const [idcError, setIdcError] = useState("");

  const device = useDevicePoll({
    providerName: "Kiro",
    start: () =>
      api.kiroDeviceStart(
        method === "idc" && idc ? { method: "idc", start_url: idc.startUrl, region: idc.region } : { method: "builder-id" },
      ),
    poll: (code) => api.kiroDevicePoll(code),
    onConnected: onClose,
  });
  const importer = useTokenImport(
    (v) => (method === "cli-proxy" ? api.kiroImportCLIProxy(v) : method === "api-key" ? api.kiroAPIKey(v, region.trim() || undefined) : api.kiroImport(v)),
    "Kiro",
    onClose,
  );

  // Builder ID needs no input: start as soon as it is picked, as before. The
  // ref keys each start so a re-render never restarts a running flow.
  const { start } = device;
  const startedFor = useRef("");
  useEffect(() => {
    const key = method === "builder-id" ? "builder-id" : method === "idc" && idc ? `idc|${idc.startUrl}|${idc.region}` : "";
    if (!key || startedFor.current === key) return;
    startedFor.current = key;
    start();
  }, [method, idc, start]);

  const select = (m: KiroMethod) => {
    setValue("");
    importer.setError("");
    setMethod(m);
  };
  const back = () => {
    startedFor.current = "";
    setMethod(null);
    setIdc(null);
  };
  const isDevice = method === "builder-id" || (method === "idc" && idc);
  const meta = KIRO_METHODS.find((m) => m.id === method);

  return (
    <ConnectDialog
      title="Connect Kiro"
      description={meta ? meta.title : "Choose how to authenticate with Kiro."}
      logo={logo}
      width="lg"
      onClose={onClose}
      onBack={method && device.status !== "waiting" && !importer.done ? back : undefined}
    >
      {method === null && <MethodPicker methods={KIRO_METHODS} onSelect={select} />}

      {method === "idc" && !idc && (
        <form
          className="space-y-3.5"
          onSubmit={(e) => {
            e.preventDefault();
            if (!startUrl.trim()) {
              setIdcError("Enter your IAM Identity Center start URL.");
              return;
            }
            setIdcError("");
            setIdc({ startUrl: startUrl.trim(), region: region.trim() });
          }}
        >
          <TextField label="Start URL" value={startUrl} onChange={(e) => setStartUrl(e.target.value)} placeholder="https://your-org.awsapps.com/start" className="font-mono" />
          <TextField label="AWS region" value={region} onChange={(e) => setRegion(e.target.value)} placeholder="us-east-1" className="font-mono" />
          <FormError message={idcError} />
          <div className="flex justify-end">
            <PrimaryAction type="submit">Continue</PrimaryAction>
          </div>
        </form>
      )}

      {isDevice && (
        <DeviceFlowBody
          flow={device}
          name="Kiro"
          startingText="Registering with AWS SSO…"
          hint="Enter the code on the AWS page, then approve access."
          idle={null}
          startLabel="Start"
          onClose={onClose}
        />
      )}

      {(method === "import" || method === "cli-proxy" || method === "api-key") &&
        (importer.done ? (
          <Connected name="Kiro" />
        ) : (
          <form
            className="space-y-3.5"
            onSubmit={(e) => {
              e.preventDefault();
              importer.run(value);
            }}
          >
            {method === "import" && (
              <>
                <p className="text-[13px] leading-5 text-fg-muted">
                  Copy the refresh token from the Kiro IDE. It usually starts with <InlineCode>aorAAAAAG…</InlineCode>
                </p>
                <TextField label="Refresh token" value={value} onChange={(e) => setValue(e.target.value)} placeholder="aorAAAAAG…" className="font-mono" autoComplete="off" />
              </>
            )}
            {method === "cli-proxy" && (
              <>
                <p className="text-[13px] leading-5 text-fg-muted">
                  Paste the auth JSON with <InlineCode>auth_method=external_idp</InlineCode>. Only Microsoft login token endpoints are accepted.
                </p>
                <TextAreaField
                  label="Auth JSON"
                  value={value}
                  onChange={(e) => setValue(e.target.value)}
                  rows={7}
                  placeholder={'{"auth_method":"external_idp","access_token":"…","refresh_token":"…","client_id":"…","token_endpoint":"https://login.microsoftonline.com/…/oauth2/v2.0/token","profile_arn":"…"}'}
                />
              </>
            )}
            {method === "api-key" && (
              <>
                <p className="text-[13px] leading-5 text-fg-muted">The key is validated against your AWS profile and used directly, with no refresh.</p>
                <TextField label="API key" value={value} onChange={(e) => setValue(e.target.value)} placeholder="CodeWhisperer API key" className="font-mono" autoComplete="off" />
                <TextField label="AWS region" value={region} onChange={(e) => setRegion(e.target.value)} placeholder="us-east-1" className="font-mono" />
              </>
            )}
            <FormError message={importer.error} />
            <div className="flex justify-end">
              <PrimaryAction type="submit" busy={importer.busy} disabled={!value.trim()}>
                {importer.busy ? "Validating…" : method === "api-key" ? "Connect with API key" : "Import"}
              </PrimaryAction>
            </div>
          </form>
        ))}
    </ConnectDialog>
  );
}

// ── Cursor and Command Code: paste a token from the vendor's app ────────────

export function CursorConnect({ logo, onClose }: VendorDialogProps) {
  return (
    <TokenDialog
      logo={logo}
      onClose={onClose}
      name="Cursor"
      description="Cursor has no public OAuth, so connect it with the access token from the IDE."
      fieldLabel="Access token"
      placeholder="Paste your Cursor access token"
      submit={(v) => api.cursorImport(v)}
      instructions={
        <Steps
          title="Where to find it"
          steps={["Open Cursor's settings.", "Go to your account section.", "Copy the access token."]}
        />
      }
    />
  );
}

export function CommandCodeConnect({ logo, onClose }: VendorDialogProps) {
  return (
    <TokenDialog
      logo={logo}
      onClose={onClose}
      name="Command Code"
      description="CLI subscriptions (Go, Pro, Max, Ultra) and studio API keys both work."
      fieldLabel="Token or API key"
      placeholder="Paste your Command Code token or API key"
      submit={(v) => api.commandcodeImport(v)}
      instructions={
        <div className="grid gap-2 sm:grid-cols-2">
          <Steps
            title="From the CLI"
            steps={[
              <>
                Run <InlineCode>cmd login</InlineCode>
              </>,
              <>
                Copy the token from <InlineCode>~/.commandcode/auth.json</InlineCode>
              </>,
            ]}
          />
          <Steps
            title="From the studio"
            steps={[
              <>
                Open <ExternalTextLink href="https://commandcode.ai/studio">commandcode.ai/studio</ExternalTextLink>
              </>,
              "Generate and copy an API key",
            ]}
          />
        </div>
      }
    />
  );
}

function TokenDialog({
  logo,
  onClose,
  name,
  description,
  fieldLabel,
  placeholder,
  instructions,
  submit,
}: VendorDialogProps & {
  name: string;
  description: string;
  fieldLabel: string;
  placeholder: string;
  instructions: React.ReactNode;
  submit: (value: string) => Promise<unknown>;
}) {
  const [value, setValue] = useState("");
  const importer = useTokenImport(submit, name, onClose);
  return (
    <ConnectDialog title={`Connect ${name}`} description={description} logo={logo} onClose={onClose} width="lg">
      {importer.done ? (
        <Connected name={name} />
      ) : (
        <form
          className="space-y-3.5"
          onSubmit={(e) => {
            e.preventDefault();
            importer.run(value);
          }}
        >
          {instructions}
          <TextField label={fieldLabel} value={value} onChange={(e) => setValue(e.target.value)} placeholder={placeholder} className="font-mono" autoComplete="off" />
          <FormError message={importer.error} />
          <div className="flex justify-end">
            <PrimaryAction type="submit" busy={importer.busy} disabled={!value.trim()}>
              {importer.busy ? "Importing…" : "Import token"}
            </PrimaryAction>
          </div>
        </form>
      )}
    </ConnectDialog>
  );
}

// ── Shared body for device / browser-approval flows ─────────────────────────

function DeviceFlowBody({
  flow,
  name,
  idle,
  startLabel,
  startingText,
  openLabel,
  hint,
  waitingExtra,
  onClose,
}: {
  flow: ReturnType<typeof useDevicePoll>;
  name: string;
  idle: React.ReactNode;
  startLabel: string;
  startingText: string;
  openLabel?: string;
  hint?: string;
  waitingExtra?: React.ReactNode;
  onClose: () => void;
}) {
  if (flow.status === "idle") {
    return (
      <div className="space-y-4">
        {idle}
        <div className="flex justify-end">
          <PrimaryAction onClick={flow.start}>
            <ExternalLink />
            {startLabel}
          </PrimaryAction>
        </div>
      </div>
    );
  }
  if (flow.status === "starting" || (flow.status === "waiting" && !flow.code)) return <Starting text={startingText} />;
  if (flow.status === "waiting" && flow.code) {
    return (
      <DeviceWaiting code={flow.code} elapsed={flow.elapsed} openLabel={openLabel} hint={hint}>
        {waitingExtra}
      </DeviceWaiting>
    );
  }
  if (flow.status === "done") return <Connected name={name} />;
  return <ConnectError message={flow.error} onRetry={flow.start} onClose={onClose} />;
}
