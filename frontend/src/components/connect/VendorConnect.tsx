import { useEffect, useId, useRef, useState } from "react";
import { Building2, ExternalLink, FileJson, FileUp, KeyRound, Shield } from "lucide-react";
import { api } from "../../lib/api";
import { useToast } from "../Toast";
import {
  ConnectDialog,
  ConnectError,
  Connected,
  DeviceWaiting,
  ExternalTextLink,
  InlineCode,
  MethodPicker,
  PrimaryAction,
  SecondaryAction,
  Starting,
  Steps,
  TextAreaField,
  TextButton,
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
    <ConnectDialog title="Connect KiloCode" logo={logo} onClose={onClose}>
      <DeviceFlowBody
        flow={flow}
        name="KiloCode"
        startingText="Requesting a device code…"
        idle={
          <Steps steps={["Get a one-time code", "Enter it on KiloCode's verification page", "Approve access — this dialog finishes on its own"]} />
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
    <ConnectDialog title="Connect CodeBuddy" logo={logo} onClose={onClose}>
      <DeviceFlowBody
        flow={flow}
        name="CodeBuddy"
        startingText="Creating a sign-in session…"
        openLabel="Open CodeBuddy sign-in"
        idle={
          <Steps steps={["Sign in with your Tencent account in the new tab", "Approve access — this dialog finishes on its own"]} />
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
  const [manualError, setManualError] = useState("");
  const [manualOpen, setManualOpen] = useState(false);
  const manualId = useId();
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
        setManualError("That isn't the callback URL. It must contain both state and token.");
        return;
      }
      setManualError("");
      // The gateway already processed the redirect if it reached it; polling
      // now picks the token up either way.
      pollNow();
      toast.info("Checking callback", "Looking for your token…");
    } catch {
      setManualError("That isn't a valid URL. Paste the full address from the browser bar.");
    }
  };

  return (
    <ConnectDialog title="Connect Kimchi" logo={logo} onClose={onClose}>
      <DeviceFlowBody
        flow={flow}
        name="Kimchi"
        startingText="Creating a sign-in session…"
        openLabel="Open Kimchi sign-in"
        hint="Approve access in the other tab. Kimchi redirects back on its own."
        idle={<Steps steps={["Authorize KeiRouter in the new tab", "Kimchi redirects back — this dialog finishes on its own"]} />}
        startLabel="Open Kimchi sign-in"
        onClose={onClose}
        waitingExtra={
          <div className="border-t border-line pt-3">
            <TextButton onClick={() => setManualOpen((v) => !v)} aria-expanded={manualOpen} aria-controls={manualId}>
              {manualOpen ? "Hide manual callback" : "No redirect? Paste the callback URL"}
            </TextButton>
            {manualOpen && (
              <div id={manualId} className="mt-2.5 space-y-2.5">
                <TextField
                  label="Callback URL"
                  value={manualUrl}
                  onChange={(e) => {
                    setManualUrl(e.target.value);
                    setManualError("");
                  }}
                  placeholder="http://127.0.0.1:20180/kimchi/callback?token=…&state=…"
                  className="font-mono text-[12px]"
                  error={manualError || undefined}
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
  { id: "oauth", title: "Sign in with Qoder", description: "Approve in the browser", icon: ExternalLink, recommended: true },
  { id: "pat", title: "Personal Access Token", description: "Paste a pt-… token", icon: KeyRound },
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
      description={method === "pat" ? "Personal Access Token" : method === "oauth" ? "Sign in with Qoder" : undefined}
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
          idle={<Steps steps={["Pick your account on the Qoder page that opens", "Approve access — this dialog finishes on its own"]} />}
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
            noValidate
            onSubmit={(e) => {
              e.preventDefault();
              pat.run(token);
            }}
          >
            <TextField
              label="Personal Access Token"
              required
              value={token}
              onChange={(e) => {
                setToken(e.target.value);
                pat.setError("");
              }}
              placeholder="pt-…"
              className="font-mono"
              autoComplete="off"
              hint={
                <>
                  Create one on your <ExternalTextLink href="https://qoder.com/account/integrations">Qoder integrations page</ExternalTextLink>
                </>
              }
              error={pat.error || undefined}
            />
            <TextField label="Label" optional value={label} onChange={(e) => setLabel(e.target.value)} placeholder="Qoder" />
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
  { id: "builder-id", title: "AWS Builder ID", description: "Free AWS sign-in, best for most people", icon: Shield, recommended: true },
  { id: "idc", title: "AWS IAM Identity Center", description: "Your organization's start URL", icon: Building2 },
  { id: "import", title: "Import a refresh token", description: "Exported from the Kiro IDE", icon: FileUp },
  { id: "cli-proxy", title: "Import CLIProxy auth JSON", description: "external_idp credential from a Microsoft login", icon: FileJson },
  { id: "api-key", title: "API key", description: "Headless CodeWhisperer key, no refresh", icon: KeyRound },
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
      description={meta ? meta.title : undefined}
      logo={logo}
      width="lg"
      onClose={onClose}
      onBack={method && device.status !== "waiting" && !importer.done ? back : undefined}
    >
      {method === null && <MethodPicker methods={KIRO_METHODS} onSelect={select} />}

      {method === "idc" && !idc && (
        <form
          className="space-y-3.5"
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            if (!startUrl.trim()) {
              setIdcError("Enter your IAM Identity Center start URL.");
              const el = e.currentTarget.querySelector<HTMLInputElement>("input");
              window.requestAnimationFrame(() => el?.focus());
              return;
            }
            setIdcError("");
            setIdc({ startUrl: startUrl.trim(), region: region.trim() });
          }}
        >
          <TextField
            label="Start URL"
            required
            value={startUrl}
            onChange={(e) => {
              setStartUrl(e.target.value);
              setIdcError("");
            }}
            placeholder="https://your-org.awsapps.com/start"
            className="font-mono"
            error={idcError || undefined}
          />
          <TextField label="AWS region" value={region} onChange={(e) => setRegion(e.target.value)} placeholder="us-east-1" className="font-mono" />
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
            noValidate
            onSubmit={(e) => {
              e.preventDefault();
              importer.run(value);
            }}
          >
            {method === "import" && (
              <TextField
                label="Refresh token"
                required
                value={value}
                onChange={(e) => {
                  setValue(e.target.value);
                  importer.setError("");
                }}
                placeholder="aorAAAAAG…"
                className="font-mono"
                autoComplete="off"
                hint="Copy it from the Kiro IDE"
                error={importer.error || undefined}
              />
            )}
            {method === "cli-proxy" && (
              <TextAreaField
                label="Auth JSON"
                required
                value={value}
                onChange={(e) => {
                  setValue(e.target.value);
                  importer.setError("");
                }}
                rows={7}
                placeholder={'{"auth_method":"external_idp","access_token":"…","refresh_token":"…","client_id":"…","token_endpoint":"https://login.microsoftonline.com/…/oauth2/v2.0/token","profile_arn":"…"}'}
                hint={
                  <>
                    Must use <InlineCode>auth_method=external_idp</InlineCode> with a Microsoft login token endpoint
                  </>
                }
                error={importer.error || undefined}
              />
            )}
            {method === "api-key" && (
              <>
                <TextField
                  label="API key"
                  required
                  value={value}
                  onChange={(e) => {
                    setValue(e.target.value);
                    importer.setError("");
                  }}
                  placeholder="CodeWhisperer API key"
                  className="font-mono"
                  autoComplete="off"
                  hint="Validated against your AWS profile, used without refresh"
                  error={importer.error || undefined}
                />
                <TextField label="AWS region" value={region} onChange={(e) => setRegion(e.target.value)} placeholder="us-east-1" className="font-mono" />
              </>
            )}
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
      description="Uses the access token from the Cursor IDE"
      fieldLabel="Access token"
      placeholder="Paste your Cursor access token"
      submit={(v) => api.cursorImport(v)}
      instructions={
        <Steps steps={["Open Cursor's settings", "Go to your account section", "Copy the access token"]} />
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
      description="CLI subscription token or studio API key"
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
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            importer.run(value);
          }}
        >
          {instructions}
          <TextField
            label={fieldLabel}
            required
            value={value}
            onChange={(e) => {
              setValue(e.target.value);
              importer.setError("");
            }}
            placeholder={placeholder}
            className="font-mono"
            autoComplete="off"
            error={importer.error || undefined}
          />
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
            <ExternalLink aria-hidden="true" />
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
