"use client";

import type { Schemas } from "@airship/client";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@airship/ui/components/alert-dialog";
import { Button } from "@airship/ui/components/button";
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@airship/ui/components/field";
import { Input } from "@airship/ui/components/input";
import { Skeleton } from "@airship/ui/components/skeleton";
import { Textarea } from "@airship/ui/components/textarea";
import { cn } from "@airship/ui/lib/utils";
import { useQueryClient } from "@tanstack/react-query";
import { ChevronRight } from "lucide-react";
import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import { toast } from "sonner";
import { $api, invalidatePath } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";
import {
  awsRegions,
  azureRegions,
  daytonaRegions,
  gcpLocations,
} from "@/lib/labels";
import { CloudLogo } from "./cloud-logo";
import { CodeBlock } from "./copy-button";
import { ErrorRow } from "./data-table";
import { DocsLink } from "./docs-link";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";
import {
  Fact,
  Facts,
  SettingsCard,
  SettingsCardBody,
  SettingsCardFooter,
  SettingsCardHeader,
} from "./panel";
import { CloudStatus } from "./status";
import { RelativeTime } from "./time";

const CLOUDS = "/v1/orgs/{org}/clouds";
const DEFAULT_AWS_REGION = "eu-west-1";
const DEFAULT_GCP_LOCATION = "us-central1-b";
const DEFAULT_DAYTONA_REGION = "us";
const DEFAULT_AZURE_REGION = "eastus";

type CloudName = Schemas["Cloud"]["cloud"];

/** What the dashboard says about each cloud. */
const cloudCopy: Record<
  CloudName,
  {
    title: string;
    identity: string;
    account: string;
    location: string;
    intro: ReactNode;
    /** Set where the cache proxy has no object storage to speak to. */
    noCache?: boolean;
  }
> = {
  aws: {
    account: "Account",
    identity: "Access key",
    intro: (
      <>
        An access key for an IAM user with AmazonEC2FullAccess. Each runner is
        an EC2 instance. <DocsLink page="Amazon Web Services" />
      </>
    ),
    location: "Default region",
    title: "Amazon Web Services",
  },
  azure: {
    account: "Subscription",
    identity: "Application",
    intro: (
      <>
        An Entra application with the Contributor role on the subscription. Each
        runner is a virtual machine. <DocsLink page="Microsoft Azure" />
      </>
    ),
    location: "Default region",
    noCache: true,
    title: "Microsoft Azure",
  },
  daytona: {
    account: "Organization",
    identity: "API key",
    intro:
      "An organization API key with the sandbox permissions. Each runner is a sandbox.",
    location: "Default region",
    noCache: true,
    title: "Daytona",
  },
  gcp: {
    account: "Project",
    identity: "Service account",
    intro: (
      <>
        A JSON key for a service account with the Compute Admin role. Each
        runner is a Compute Engine instance. <DocsLink page="Google Cloud" />
      </>
    ),
    location: "Default zone",
    title: "Google Cloud",
  },
};

const ORDER: readonly CloudName[] = ["aws", "gcp", "azure", "daytona"];

function CloudTile({
  name,
  size = "md",
}: {
  name: CloudName;
  size?: "sm" | "md";
}) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "grid shrink-0 place-items-center rounded-lg border bg-background text-foreground shadow-xs",
        size === "md" ? "size-11" : "size-9"
      )}
    >
      <CloudLogo className={size === "md" ? "size-6" : "size-5"} name={name} />
    </span>
  );
}

function StatusPill({ cloud }: { cloud: Schemas["Cloud"] | undefined }) {
  return (
    <span className="inline-flex h-6 items-center rounded-full border bg-background px-2.5 text-xs">
      {cloud ? (
        <CloudStatus cloud={cloud} />
      ) : (
        <span className="text-muted-foreground">Not connected</span>
      )}
    </span>
  );
}

export function CloudPage() {
  const { org } = useOrg();
  const clouds = $api.useQuery("get", CLOUDS, {
    params: { path: { org: org.login } },
  });
  const find = (name: CloudName) =>
    clouds.data?.items?.find((item) => item.cloud === name);
  const forms: Record<
    CloudName,
    (existing: Schemas["Cloud"] | undefined) => ReactNode
  > = {
    aws: (existing) => <ConnectAWSForm existing={existing} />,
    azure: (existing) => <ConnectAzureForm existing={existing} />,
    daytona: (existing) => <ConnectDaytonaForm existing={existing} />,
    gcp: (existing) => <ConnectGCPForm existing={existing} />,
  };

  return (
    <>
      <PageHeader
        description="Where Airship launches runners. Credentials are encrypted and never shown again."
        title="Cloud"
      />
      <nav
        aria-label="Providers"
        className="grid grid-cols-2 gap-px overflow-hidden rounded-lg border bg-border lg:grid-cols-4"
      >
        {ORDER.map((name) => (
          <a
            className="flex items-center gap-3 bg-card p-4 transition-colors hover:bg-muted"
            href={`#${name}`}
            key={name}
          >
            <CloudTile name={name} size="sm" />
            <span className="grid min-w-0 gap-0.5">
              <span className="truncate font-medium text-sm">
                {cloudCopy[name].title}
              </span>
              <span className="text-xs">
                {clouds.isPending ? (
                  <Skeleton className="h-3 w-16" />
                ) : (
                  <ProviderState cloud={find(name)} />
                )}
              </span>
            </span>
          </a>
        ))}
      </nav>
      <div className="grid gap-6">
        {ORDER.map((name) => (
          <CloudSection
            clouds={clouds}
            existing={find(name)}
            form={forms[name]}
            key={name}
            name={name}
          />
        ))}
      </div>
    </>
  );
}

function ProviderState({ cloud }: { cloud: Schemas["Cloud"] | undefined }) {
  if (!cloud) {
    return <span className="text-muted-foreground">Not connected</span>;
  }
  return <CloudStatus cloud={cloud} />;
}

function CloudSection({
  name,
  clouds,
  existing,
  form,
}: {
  name: CloudName;
  clouds: {
    isPending: boolean;
    isError: boolean;
    isSuccess: boolean;
    error: unknown;
  };
  existing: Schemas["Cloud"] | undefined;
  form: (existing: Schemas["Cloud"] | undefined) => ReactNode;
}) {
  const copy = cloudCopy[name];
  return (
    <SettingsCard
      aria-labelledby={`${name}-title`}
      className="scroll-mt-6"
      id={name}
    >
      <SettingsCardHeader
        action={clouds.isSuccess ? <StatusPill cloud={existing} /> : null}
        description={
          <>
            {copy.intro}
            {copy.noCache ? (
              <>
                {" "}
                <code className="whitespace-nowrap font-mono text-[13px] text-foreground">
                  cache: true
                </code>{" "}
                launches without the cache proxy here.
              </>
            ) : null}
          </>
        }
        icon={<CloudTile name={name} />}
        id={`${name}-title`}
        title={copy.title}
      />
      {clouds.isPending ? (
        <SettingsCardBody>
          <Skeleton className="h-24" />
        </SettingsCardBody>
      ) : null}
      {clouds.isError ? (
        <SettingsCardBody>
          <ErrorRow
            error={clouds.error}
            fallback="Could not load the cloud connection"
          />
        </SettingsCardBody>
      ) : null}
      {clouds.isSuccess && existing ? <Connected cloud={existing} /> : null}
      {clouds.isSuccess ? form(existing) : null}
    </SettingsCard>
  );
}

function Connected({ cloud }: { cloud: Schemas["Cloud"] }) {
  const copy = cloudCopy[cloud.cloud];
  return (
    <SettingsCardBody className="grid gap-4">
      <Facts className="rounded-md border bg-background p-4 lg:grid-cols-4">
        <Fact label={copy.identity} mono>
          <span title={cloud.identity}>{cloud.identity}</span>
        </Fact>
        <Fact label={copy.account} mono>
          {cloud.accountId ?? "—"}
        </Fact>
        <Fact label={copy.location} mono>
          {cloud.defaultRegion}
        </Fact>
        <Fact label="Connected">
          <RelativeTime value={cloud.connectedAt} />
          {cloud.verifiedAt ? (
            <span className="text-muted-foreground">
              , verified <RelativeTime value={cloud.verifiedAt} />
            </span>
          ) : null}
        </Fact>
      </Facts>
      {cloud.error ? (
        <p
          className="rounded-md border border-danger/25 bg-danger-surface px-3 py-2 text-danger-foreground text-sm"
          role="alert"
        >
          {cloud.error}
        </p>
      ) : null}
    </SettingsCardBody>
  );
}

/** Clears a connect mutation's variables (the pasted key) once it succeeded. */
function useForgetKey(connect: { isSuccess: boolean; reset: () => void }) {
  useEffect(() => {
    if (connect.isSuccess) {
      connect.reset();
    }
  }, [connect.isSuccess, connect.reset]);
}

function NotConnected({ title }: { title: string }) {
  return (
    <SettingsCardFooter
      note={`No ${title} account is connected. Only organization owners can connect one.`}
    />
  );
}

/**
 * A connect form inside a cloud's card. Connected, it folds away behind
 * "Replace credentials" so the card shows what is there; not connected, the
 * fields are open. The footer holds the note and the actions.
 */
function CredentialsForm({
  children,
  cloud,
  existing,
  label,
  onSubmit,
  pending,
}: {
  children: ReactNode;
  cloud: CloudName;
  existing: Schemas["Cloud"] | undefined;
  label: string;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  pending: boolean;
}) {
  const [editing, setEditing] = useState(false);
  const open = !existing || editing;
  const updatedAt = existing?.updatedAt;

  // A successful replace changes the connection; fold the form again.
  // biome-ignore lint/correctness/useExhaustiveDependencies: runs per update.
  useEffect(() => {
    setEditing(false);
  }, [updatedAt]);

  let submit = existing ? "Replace credentials" : "Connect";
  if (pending) {
    submit = "Verifying…";
  }
  let note: ReactNode = "Encrypted at rest and never shown again.";
  if (open) {
    note = existing
      ? "A new credential replaces the current one. Live runners keep working."
      : "Airship verifies the credential before saving it.";
  }

  return (
    <form aria-label={label} onSubmit={onSubmit}>
      {open ? (
        <SettingsCardBody className="grid gap-5 border-t pt-5">
          {children}
          <details className="group rounded-md border bg-background">
            <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2.5 text-sm transition-colors hover:bg-muted [&::-webkit-details-marker]:hidden">
              <ChevronRight
                aria-hidden="true"
                className="size-4 text-muted-foreground transition-transform group-open:rotate-90"
              />
              {guides[cloud].title}
            </summary>
            <div className="grid gap-3 border-t p-3">{guides[cloud].body}</div>
          </details>
        </SettingsCardBody>
      ) : null}
      <SettingsCardFooter note={note}>
        {existing && !editing ? (
          <>
            <Disconnect cloud={existing} />
            <Button
              onClick={() => setEditing(true)}
              type="button"
              variant="outline"
            >
              Replace credentials
            </Button>
          </>
        ) : null}
        {existing && editing ? (
          <Button
            onClick={() => setEditing(false)}
            type="button"
            variant="ghost"
          >
            Cancel
          </Button>
        ) : null}
        {open ? (
          <Button disabled={pending} type="submit">
            {submit}
          </Button>
        ) : null}
      </SettingsCardFooter>
    </form>
  );
}

function LocationSelect({
  id,
  label,
  options,
  value,
  onChange,
}: {
  id: string;
  label: string;
  options: readonly string[];
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <Field>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <select
        className="h-9 rounded-md border border-input bg-background px-2.5 font-mono text-[13px] outline-none transition-colors focus-visible:border-foreground/40 focus-visible:ring-[3px] focus-visible:ring-foreground/10 dark:bg-input/30"
        id={id}
        onChange={(event) => onChange(event.target.value)}
        value={value}
      >
        {options.map((candidate) => (
          <option key={candidate} value={candidate}>
            {candidate}
          </option>
        ))}
      </select>
      <FieldDescription>
        Used when a definition names no region
      </FieldDescription>
    </Field>
  );
}

function ConnectAWSForm({
  existing,
}: {
  existing: Schemas["Cloud"] | undefined;
}) {
  const { isAdmin, org } = useOrg();
  const queryClient = useQueryClient();
  const [accessKeyId, setAccessKeyId] = useState("");
  const [secretAccessKey, setSecretAccessKey] = useState("");
  const [region, setRegion] = useState(
    existing?.defaultRegion ?? DEFAULT_AWS_REGION
  );
  const connect = $api.useMutation("put", "/v1/orgs/{org}/clouds/aws", {
    onSuccess: async (cloud) => {
      setAccessKeyId("");
      setSecretAccessKey("");
      toast.success(`Connected AWS account ${cloud.accountId ?? ""}`.trim());
      await invalidatePath(queryClient, CLOUDS);
    },
  });
  useForgetKey(connect);

  if (!isAdmin) {
    return existing ? null : <NotConnected title="AWS" />;
  }

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    connect.mutate({
      body: {
        accessKeyId: accessKeyId.trim(),
        defaultRegion: region,
        secretAccessKey: secretAccessKey.trim(),
      },
      params: { path: { org: org.login } },
    });
  };

  return (
    <CredentialsForm
      cloud="aws"
      existing={existing}
      label={existing ? "Replace AWS credentials" : "Connect AWS"}
      onSubmit={submit}
      pending={connect.isPending}
    >
      <div className="grid gap-4 md:grid-cols-3">
        <Field data-invalid={connect.isError || undefined}>
          <FieldLabel htmlFor="aws-access-key">Access key</FieldLabel>
          <Input
            aria-invalid={connect.isError || undefined}
            autoComplete="off"
            className="font-mono"
            id="aws-access-key"
            onChange={(event) => setAccessKeyId(event.target.value)}
            pattern="[A-Z0-9]{16,128}"
            placeholder="AKIA…"
            required
            spellCheck={false}
            value={accessKeyId}
          />
        </Field>
        <Field data-invalid={connect.isError || undefined}>
          <FieldLabel htmlFor="aws-secret-key">Secret key</FieldLabel>
          <Input
            aria-invalid={connect.isError || undefined}
            autoComplete="off"
            className="font-mono"
            id="aws-secret-key"
            minLength={16}
            onChange={(event) => setSecretAccessKey(event.target.value)}
            required
            type="password"
            value={secretAccessKey}
          />
        </Field>
        <LocationSelect
          id="aws-region"
          label="Default region"
          onChange={setRegion}
          options={awsRegions}
          value={region}
        />
      </div>
      {connect.isError ? (
        <FieldError>
          {errorMessage(connect.error, "Could not connect AWS")}
        </FieldError>
      ) : null}
    </CredentialsForm>
  );
}

function ConnectGCPForm({
  existing,
}: {
  existing: Schemas["Cloud"] | undefined;
}) {
  const { isAdmin, org } = useOrg();
  const queryClient = useQueryClient();
  const [key, setKey] = useState("");
  const [location, setLocation] = useState(
    existing?.defaultRegion ?? DEFAULT_GCP_LOCATION
  );
  const connect = $api.useMutation("put", "/v1/orgs/{org}/clouds/gcp", {
    onSuccess: async (cloud) => {
      setKey("");
      toast.success(
        `Connected Google Cloud project ${cloud.accountId ?? ""}`.trim()
      );
      await invalidatePath(queryClient, CLOUDS);
    },
  });
  useForgetKey(connect);

  if (!isAdmin) {
    return existing ? null : <NotConnected title="Google Cloud" />;
  }

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    connect.mutate({
      body: { defaultRegion: location, serviceAccountKey: key.trim() },
      params: { path: { org: org.login } },
    });
  };

  return (
    <CredentialsForm
      cloud="gcp"
      existing={existing}
      label={
        existing ? "Replace Google Cloud credentials" : "Connect Google Cloud"
      }
      onSubmit={submit}
      pending={connect.isPending}
    >
      <div className="grid gap-4 md:grid-cols-[2fr_1fr]">
        <Field data-invalid={connect.isError || undefined}>
          <FieldLabel htmlFor="gcp-key">Service account key</FieldLabel>
          <Textarea
            aria-invalid={connect.isError || undefined}
            autoComplete="off"
            className="min-h-32 font-mono"
            id="gcp-key"
            onChange={(event) => setKey(event.target.value)}
            placeholder={
              '{\n  "type": "service_account",\n  "project_id": "…"\n}'
            }
            required
            spellCheck={false}
            value={key}
          />
          <FieldDescription>
            The whole JSON file Google Cloud downloads. Only the project and
            email stay readable.
          </FieldDescription>
        </Field>
        <LocationSelect
          id="gcp-location"
          label="Default zone"
          onChange={setLocation}
          options={gcpLocations}
          value={location}
        />
      </div>
      {connect.isError ? (
        <FieldError>
          {errorMessage(connect.error, "Could not connect Google Cloud")}
        </FieldError>
      ) : null}
    </CredentialsForm>
  );
}

function ConnectAzureForm({
  existing,
}: {
  existing: Schemas["Cloud"] | undefined;
}) {
  const { isAdmin, org } = useOrg();
  const queryClient = useQueryClient();
  const [subscriptionId, setSubscriptionId] = useState("");
  const [tenantId, setTenantId] = useState("");
  const [clientId, setClientId] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [region, setRegion] = useState(
    existing?.defaultRegion ?? DEFAULT_AZURE_REGION
  );
  const connect = $api.useMutation("put", "/v1/orgs/{org}/clouds/azure", {
    onSuccess: async (cloud) => {
      setClientSecret("");
      toast.success(
        `Connected Azure subscription ${cloud.accountId ?? ""}`.trim()
      );
      await invalidatePath(queryClient, CLOUDS);
    },
  });
  useForgetKey(connect);

  if (!isAdmin) {
    return existing ? null : <NotConnected title="Microsoft Azure" />;
  }

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    connect.mutate({
      body: {
        clientId: clientId.trim(),
        clientSecret: clientSecret.trim(),
        defaultRegion: region,
        subscriptionId: subscriptionId.trim(),
        tenantId: tenantId.trim(),
      },
      params: { path: { org: org.login } },
    });
  };
  const guid = {
    autoComplete: "off",
    className: "font-mono",
    pattern:
      "[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}",
    placeholder: "00000000-0000-0000-0000-000000000000",
    required: true,
    spellCheck: false,
  } as const;

  return (
    <CredentialsForm
      cloud="azure"
      existing={existing}
      label={existing ? "Replace Azure credentials" : "Connect Microsoft Azure"}
      onSubmit={submit}
      pending={connect.isPending}
    >
      <div className="grid gap-4 md:grid-cols-3">
        <Field>
          <FieldLabel htmlFor="azure-subscription-id">
            Subscription ID
          </FieldLabel>
          <Input
            {...guid}
            id="azure-subscription-id"
            onChange={(event) => setSubscriptionId(event.target.value)}
            value={subscriptionId}
          />
          <FieldDescription>
            Where the application has the Contributor role
          </FieldDescription>
        </Field>
        <Field>
          <FieldLabel htmlFor="azure-tenant-id">
            Directory (tenant) ID
          </FieldLabel>
          <Input
            {...guid}
            id="azure-tenant-id"
            onChange={(event) => setTenantId(event.target.value)}
            value={tenantId}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="azure-client-id">
            Application (client) ID
          </FieldLabel>
          <Input
            {...guid}
            id="azure-client-id"
            onChange={(event) => setClientId(event.target.value)}
            value={clientId}
          />
        </Field>
        <Field data-invalid={connect.isError || undefined}>
          <FieldLabel htmlFor="azure-client-secret">Client secret</FieldLabel>
          <Input
            aria-invalid={connect.isError || undefined}
            autoComplete="off"
            className="font-mono"
            id="azure-client-secret"
            onChange={(event) => setClientSecret(event.target.value)}
            required
            spellCheck={false}
            type="password"
            value={clientSecret}
          />
          <FieldDescription>The value, not the secret ID</FieldDescription>
        </Field>
        <LocationSelect
          id="azure-region"
          label="Default region"
          onChange={setRegion}
          options={azureRegions}
          value={region}
        />
      </div>
      {connect.isError ? (
        <FieldError>
          {errorMessage(connect.error, "Could not connect Microsoft Azure")}
        </FieldError>
      ) : null}
    </CredentialsForm>
  );
}

function ConnectDaytonaForm({
  existing,
}: {
  existing: Schemas["Cloud"] | undefined;
}) {
  const { isAdmin, org } = useOrg();
  const queryClient = useQueryClient();
  const [apiKey, setApiKey] = useState("");
  const [apiUrl, setApiUrl] = useState("");
  const [region, setRegion] = useState(
    existing?.defaultRegion ?? DEFAULT_DAYTONA_REGION
  );
  const connect = $api.useMutation("put", "/v1/orgs/{org}/clouds/daytona", {
    onSuccess: async (cloud) => {
      setApiKey("");
      toast.success(
        `Connected Daytona organization ${cloud.accountId ?? ""}`.trim()
      );
      await invalidatePath(queryClient, CLOUDS);
    },
  });
  useForgetKey(connect);

  if (!isAdmin) {
    return existing ? null : <NotConnected title="Daytona" />;
  }

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const trimmedUrl = apiUrl.trim();
    connect.mutate({
      body: {
        apiKey: apiKey.trim(),
        defaultRegion: region,
        ...(trimmedUrl ? { apiUrl: trimmedUrl } : {}),
      },
      params: { path: { org: org.login } },
    });
  };

  return (
    <CredentialsForm
      cloud="daytona"
      existing={existing}
      label={existing ? "Replace Daytona credentials" : "Connect Daytona"}
      onSubmit={submit}
      pending={connect.isPending}
    >
      <div className="grid gap-4 md:grid-cols-3">
        <Field data-invalid={connect.isError || undefined}>
          <FieldLabel htmlFor="daytona-api-key">API key</FieldLabel>
          <Input
            aria-invalid={connect.isError || undefined}
            autoComplete="off"
            className="font-mono"
            id="daytona-api-key"
            minLength={16}
            onChange={(event) => setApiKey(event.target.value)}
            placeholder="dtn_…"
            required
            spellCheck={false}
            type="password"
            value={apiKey}
          />
          <FieldDescription>
            Needs write:sandboxes and delete:sandboxes
          </FieldDescription>
        </Field>
        <Field>
          <FieldLabel htmlFor="daytona-api-url">API URL</FieldLabel>
          <Input
            autoComplete="off"
            className="font-mono"
            id="daytona-api-url"
            onChange={(event) => setApiUrl(event.target.value)}
            placeholder="https://app.daytona.io/api"
            spellCheck={false}
            type="url"
            value={apiUrl}
          />
          <FieldDescription>
            Leave empty for the hosted service
          </FieldDescription>
        </Field>
        <LocationSelect
          id="daytona-region"
          label="Default region"
          onChange={setRegion}
          options={daytonaRegions}
          value={region}
        />
      </div>
      {connect.isError ? (
        <FieldError>
          {errorMessage(connect.error, "Could not connect Daytona")}
        </FieldError>
      ) : null}
    </CredentialsForm>
  );
}

function Disconnect({ cloud }: { cloud: Schemas["Cloud"] }) {
  const { org } = useOrg();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const { title } = cloudCopy[cloud.cloud];
  const disconnect = $api.useMutation(
    "delete",
    "/v1/orgs/{org}/clouds/{cloud}",
    {
      onError: (error) => {
        setOpen(false);
        toast.error(errorMessage(error, `Could not disconnect ${title}`));
      },
      onSuccess: async () => {
        setOpen(false);
        toast.success(`Disconnected ${title}`);
        await invalidatePath(queryClient, CLOUDS);
      },
    }
  );

  return (
    <>
      <Button
        className="text-danger-foreground hover:text-danger-foreground"
        onClick={() => setOpen(true)}
        type="button"
        variant="ghost"
      >
        Disconnect
      </Button>
      <AlertDialog onOpenChange={setOpen} open={open}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Disconnect {title}?</AlertDialogTitle>
            <AlertDialogDescription>
              Jobs on this cloud stop getting runners until you connect an
              account again. Live runners finish first.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={disconnect.isPending}
              onClick={() =>
                disconnect.mutate({
                  params: { path: { cloud: cloud.cloud, org: org.login } },
                })
              }
              variant="destructive"
            >
              Disconnect
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

const AWS_COMMANDS = `aws iam create-user --user-name airship
aws iam attach-user-policy --user-name airship \\
  --policy-arn arn:aws:iam::aws:policy/AmazonEC2FullAccess
aws iam create-access-key --user-name airship`;

const GCP_COMMANDS = `gcloud services enable compute.googleapis.com
gcloud iam service-accounts create airship --display-name "Airship runners"
gcloud projects add-iam-policy-binding PROJECT_ID \\
  --member serviceAccount:airship@PROJECT_ID.iam.gserviceaccount.com \\
  --role roles/compute.admin
gcloud iam service-accounts keys create service-account.json \\
  --iam-account airship@PROJECT_ID.iam.gserviceaccount.com`;

const AZURE_COMMANDS = [
  "az login",
  'SUBSCRIPTION_ID="$(az account show --query id -o tsv)"',
  "az ad sp create-for-rbac --name airship-runners --role Contributor \\",
  '  --scopes "/subscriptions/$SUBSCRIPTION_ID"',
  "# appId is the application (client) ID, password the client secret,",
  "# tenant the directory (tenant) ID.",
].join("\n");

/** How to make the credential each form asks for. */
const guides: Record<CloudName, { body: ReactNode; title: string }> = {
  aws: {
    body: (
      <>
        <p className="text-muted-foreground text-sm">
          AmazonEC2FullAccess covers everything Airship needs to manage runners.{" "}
          <DocsLink page="Amazon Web Services" />
        </p>
        <CodeBlock
          file="terminal"
          label="Copy IAM commands"
          language="shell"
          value={AWS_COMMANDS}
        />
      </>
    ),
    title: "Creating the IAM user",
  },
  azure: {
    body: (
      <>
        <p className="text-muted-foreground text-sm">
          The Contributor role covers everything Airship needs to manage
          runners. Client secrets expire; launches fail until you paste a new
          one. <DocsLink page="Microsoft Azure" />
        </p>
        <CodeBlock
          file="terminal"
          label="Copy az commands"
          language="shell"
          value={AZURE_COMMANDS}
        />
      </>
    ),
    title: "Registering the Azure application",
  },
  daytona: {
    body: (
      <p className="text-muted-foreground text-sm">
        An organization key needs the Sandboxes write and delete permissions,
        and Volumes read if definitions mount volumes. Keys with an expiry stop
        working on that date.
      </p>
    ),
    title: "Creating the Daytona API key",
  },
  gcp: {
    body: (
      <>
        <p className="text-muted-foreground text-sm">
          The Compute Admin role covers everything Airship needs to manage
          runners. GPU runners also need GPU quota in the zone.{" "}
          <DocsLink page="Google Cloud" />
        </p>
        <CodeBlock
          file="terminal"
          label="Copy gcloud commands"
          language="shell"
          value={GCP_COMMANDS}
        />
      </>
    ),
    title: "Creating the service account",
  },
};
