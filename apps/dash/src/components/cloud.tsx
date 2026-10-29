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
import { CodeBlock } from "./copy-button";
import { ErrorRow } from "./data-table";
import { DocsLink } from "./docs-link";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";
import { CloudStatus } from "./status";
import { RelativeTime } from "./time";

const CLOUDS = "/v1/orgs/{org}/clouds";
const DEFAULT_AWS_REGION = "eu-west-1";
const DEFAULT_GCP_LOCATION = "us-central1-b";
const DEFAULT_DAYTONA_REGION = "us";
const DEFAULT_AZURE_REGION = "eastus";

type CloudName = Schemas["Cloud"]["cloud"];

/** Each cloud's own colour, as the landing page names clouds. */
const cloudDot: Record<CloudName, string> = {
  aws: "bg-cloud-aws",
  azure: "bg-cloud-azure",
  daytona: "bg-cloud-daytona",
  gcp: "bg-cloud-gcp",
};

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

export function CloudPage() {
  const { org } = useOrg();
  const clouds = $api.useQuery("get", CLOUDS, {
    params: { path: { org: org.login } },
  });
  const find = (name: CloudName) =>
    clouds.data?.items?.find((item) => item.cloud === name);

  return (
    <>
      <PageHeader
        description="Where Airship launches runners. Credentials are encrypted and never shown again."
        title="Cloud"
      />
      <CloudSection
        clouds={clouds}
        existing={find("aws")}
        form={(existing) => <ConnectAWSForm existing={existing} />}
        name="aws"
      />
      <CloudSection
        clouds={clouds}
        existing={find("gcp")}
        form={(existing) => <ConnectGCPForm existing={existing} />}
        name="gcp"
      />
      <CloudSection
        clouds={clouds}
        existing={find("azure")}
        form={(existing) => <ConnectAzureForm existing={existing} />}
        name="azure"
      />
      <CloudSection
        clouds={clouds}
        existing={find("daytona")}
        form={(existing) => <ConnectDaytonaForm existing={existing} />}
        name="daytona"
      />
      <AWSPolicy />
      <GCPRole />
      <AzureApp />
      <DaytonaKey />
    </>
  );
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
    <section
      aria-labelledby={name}
      className="grid gap-5 rounded-lg border bg-card p-5"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="grid gap-1.5">
          <h2
            className="flex items-center gap-2 font-medium text-[15px] tracking-tight"
            id={name}
          >
            <span
              aria-hidden="true"
              className={cn("h-2 w-3.5 rounded-full", cloudDot[name])}
            />
            {copy.title}
          </h2>
          <p className="max-w-prose text-muted-foreground text-xs/relaxed">
            {copy.intro}
          </p>
          {copy.noCache ? (
            <p className="text-muted-foreground text-xs">
              <code className="font-mono">cache: true</code> launches without
              the cache proxy here
            </p>
          ) : null}
        </div>
        {existing ? (
          <span className="rounded-full border px-2.5 py-1 font-mono text-[11px]">
            <CloudStatus cloud={existing} />
          </span>
        ) : null}
      </div>
      {clouds.isPending ? <Skeleton className="h-32" /> : null}
      {clouds.isError ? (
        <ErrorRow
          error={clouds.error}
          fallback="Could not load the cloud connection"
        />
      ) : null}
      {clouds.isSuccess && existing ? <Connected cloud={existing} /> : null}
      {clouds.isSuccess ? form(existing) : null}
    </section>
  );
}

function Connected({ cloud }: { cloud: Schemas["Cloud"] }) {
  const { isAdmin } = useOrg();
  const copy = cloudCopy[cloud.cloud];
  return (
    <div className="grid gap-3">
      <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1.5 rounded-md border bg-muted/40 p-3.5 text-xs">
        <dt className="text-muted-foreground">{copy.identity}</dt>
        <dd className="truncate font-mono">{cloud.identity}</dd>
        <dt className="text-muted-foreground">{copy.account}</dt>
        <dd className="font-mono">{cloud.accountId ?? "—"}</dd>
        <dt className="text-muted-foreground">Identity</dt>
        <dd className="truncate font-mono" title={cloud.principal ?? undefined}>
          {cloud.principal ?? "—"}
        </dd>
        <dt className="text-muted-foreground">{copy.location}</dt>
        <dd className="font-mono">{cloud.defaultRegion}</dd>
        <dt className="text-muted-foreground">Connected</dt>
        <dd>
          <RelativeTime value={cloud.connectedAt} />
          {cloud.verifiedAt ? (
            <span className="text-muted-foreground">
              , verified <RelativeTime value={cloud.verifiedAt} />
            </span>
          ) : null}
        </dd>
      </dl>
      {cloud.error ? (
        <p className="text-destructive" role="alert">
          {cloud.error}
        </p>
      ) : null}
      {isAdmin ? <Disconnect cloud={cloud} /> : null}
    </div>
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
    <p className="text-muted-foreground text-xs">
      No {title} account is connected. Only organization owners can connect one.
    </p>
  );
}

function ReplaceNote({ existing }: { existing: Schemas["Cloud"] | undefined }) {
  if (!existing) {
    return null;
  }
  return (
    <p className="text-muted-foreground text-xs">
      A new credential replaces the current one. Live runners keep working.
    </p>
  );
}

/** A connect form's submit button, named for what it does rather than "Save". */
function ConnectButton({
  existing,
  pending,
}: {
  existing: Schemas["Cloud"] | undefined;
  pending: boolean;
}) {
  let label = existing ? "Replace credentials" : "Connect";
  if (pending) {
    label = "Verifying…";
  }
  return (
    <div>
      <Button disabled={pending} type="submit">
        {label}
      </Button>
    </div>
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
        className="h-8 rounded-md border border-input bg-transparent px-2.5 font-mono text-xs outline-none transition-colors focus-visible:border-ring focus-visible:ring-1 focus-visible:ring-ring/50 dark:bg-input/30"
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
    <form
      aria-label={existing ? "Replace AWS credentials" : "Connect AWS"}
      className="grid gap-4 border-t pt-4"
      onSubmit={submit}
    >
      <ReplaceNote existing={existing} />
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
      <ConnectButton existing={existing} pending={connect.isPending} />
    </form>
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
    <form
      aria-label={
        existing ? "Replace Google Cloud credentials" : "Connect Google Cloud"
      }
      className="grid gap-4 border-t pt-4"
      onSubmit={submit}
    >
      <ReplaceNote existing={existing} />
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
      <ConnectButton existing={existing} pending={connect.isPending} />
    </form>
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
    <form
      aria-label={
        existing ? "Replace Azure credentials" : "Connect Microsoft Azure"
      }
      className="grid gap-4 border-t pt-4"
      onSubmit={submit}
    >
      <ReplaceNote existing={existing} />
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
      <ConnectButton existing={existing} pending={connect.isPending} />
    </form>
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
    <form
      aria-label={existing ? "Replace Daytona credentials" : "Connect Daytona"}
      className="grid gap-4 border-t pt-4"
      onSubmit={submit}
    >
      <ReplaceNote existing={existing} />
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
      <ConnectButton existing={existing} pending={connect.isPending} />
    </form>
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
    <div>
      <Button onClick={() => setOpen(true)} size="sm" variant="outline">
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
    </div>
  );
}

function AWSPolicy() {
  const commands = `aws iam create-user --user-name airship
aws iam attach-user-policy --user-name airship \\
  --policy-arn arn:aws:iam::aws:policy/AmazonEC2FullAccess
aws iam create-access-key --user-name airship`;
  return (
    <section aria-labelledby="iam" className="grid gap-3">
      <h2 className="font-medium text-[15px] tracking-tight" id="iam">
        Creating the IAM user
      </h2>
      <p className="max-w-prose text-muted-foreground text-xs/relaxed">
        AmazonEC2FullAccess covers everything a runner needs.{" "}
        <DocsLink page="Amazon Web Services" />
      </p>
      <CodeBlock
        file="terminal"
        label="Copy IAM commands"
        language="shell"
        value={commands}
      />
    </section>
  );
}

function GCPRole() {
  const commands = `gcloud services enable compute.googleapis.com
gcloud iam service-accounts create airship --display-name "Airship runners"
gcloud projects add-iam-policy-binding PROJECT_ID \\
  --member serviceAccount:airship@PROJECT_ID.iam.gserviceaccount.com \\
  --role roles/compute.admin
gcloud iam service-accounts keys create service-account.json \\
  --iam-account airship@PROJECT_ID.iam.gserviceaccount.com`;
  return (
    <section aria-labelledby="gcp-role" className="grid gap-3">
      <h2 className="font-medium text-[15px] tracking-tight" id="gcp-role">
        Creating the service account
      </h2>
      <p className="max-w-prose text-muted-foreground text-xs/relaxed">
        The Compute Admin role covers everything a runner needs. GPU runners
        also need GPU quota in the zone. <DocsLink page="Google Cloud" />
      </p>
      <CodeBlock
        file="terminal"
        label="Copy gcloud commands"
        language="shell"
        value={commands}
      />
    </section>
  );
}

function AzureApp() {
  const commands = [
    "az login",
    'SUBSCRIPTION_ID="$(az account show --query id -o tsv)"',
    "az ad sp create-for-rbac --name airship-runners --role Contributor \\",
    '  --scopes "/subscriptions/$SUBSCRIPTION_ID"',
    "# appId is the application (client) ID, password the client secret,",
    "# tenant the directory (tenant) ID.",
  ].join("\n");

  return (
    <section aria-labelledby="azure-app" className="grid gap-3">
      <h2 className="font-medium text-[15px] tracking-tight" id="azure-app">
        Registering the Azure application
      </h2>
      <p className="max-w-prose text-muted-foreground text-xs/relaxed">
        The Contributor role covers everything a runner needs. Client secrets
        expire; launches fail until you paste a new one.{" "}
        <DocsLink page="Microsoft Azure" />
      </p>
      <CodeBlock
        file="terminal"
        label="Copy az commands"
        language="shell"
        value={commands}
      />
    </section>
  );
}

function DaytonaKey() {
  return (
    <section aria-labelledby="daytona-key" className="grid gap-3">
      <h2 className="font-medium text-[15px] tracking-tight" id="daytona-key">
        Creating the Daytona API key
      </h2>
      <p className="max-w-prose text-muted-foreground text-xs/relaxed">
        An organization key needs the Sandboxes write and delete permissions,
        and Volumes read if definitions mount volumes. Keys with an expiry stop
        working on that date.
      </p>
    </section>
  );
}
