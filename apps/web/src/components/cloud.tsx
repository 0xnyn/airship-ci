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
import { useQueryClient } from "@tanstack/react-query";
import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import { toast } from "sonner";
import { $api, invalidatePath } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";
import { awsRegions, gcpLocations } from "@/lib/labels";
import { CodeBlock } from "./copy-button";
import { ErrorRow } from "./data-table";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";
import { CloudStatus } from "./status";
import { RelativeTime } from "./time";

const CLOUDS = "/v1/orgs/{org}/clouds";
const DEFAULT_AWS_REGION = "eu-west-1";
const DEFAULT_GCP_LOCATION = "us-central1-b";

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
  }
> = {
  aws: {
    account: "Account",
    identity: "Access key",
    intro: (
      <>
        Create an IAM user with the AmazonEC2FullAccess policy, generate an
        access key for it, and paste the key here. Runners get an EC2 instance
        each, in the default VPC of the region a{" "}
        <code className="font-mono">.cirun.yml</code> names, or the default
        region below.
      </>
    ),
    location: "Default region",
    title: "Amazon Web Services",
  },
  gcp: {
    account: "Project",
    identity: "Service account",
    intro: (
      <>
        Create a service account with the Compute Admin role, enable the Compute
        Engine API, download a JSON key for it and paste the file here. Runners
        get a Compute Engine instance each, in the zone a{" "}
        <code className="font-mono">.cirun.yml</code> names (or any zone of the
        region it names), or the default zone below.
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
        description="Airship launches every runner in your own cloud account with the credentials you connect here. They are stored encrypted and never shown again. Connect one cloud or both; each runner definition says which it launches in."
        title="Cloud credentials"
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
      <AWSPolicy />
      <GCPRole />
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
    <section aria-labelledby={name} className="grid gap-4 border p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="grid gap-1">
          <h2 className="font-medium text-sm" id={name}>
            {copy.title}
          </h2>
          <p className="max-w-prose text-muted-foreground text-xs/relaxed">
            {copy.intro}
          </p>
        </div>
        {existing ? <CloudStatus cloud={existing} /> : null}
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
      <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 text-xs">
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
      No {title} account is connected. An organization owner can connect one.
    </p>
  );
}

function ReplaceNote({ existing }: { existing: Schemas["Cloud"] | undefined }) {
  if (!existing) {
    return null;
  }
  return (
    <p className="text-muted-foreground text-xs">
      Pasting a new key replaces the current one. Runners already launched keep
      working.
    </p>
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
        className="h-8 border bg-transparent px-2 font-mono text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring"
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
        Used when a runner definition names no region.
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
      <div>
        <Button disabled={connect.isPending} type="submit">
          {connect.isPending ? "Verifying…" : "Save"}
        </Button>
      </div>
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
            The whole JSON file Google Cloud downloads when you create a key.
            Only its project and email are kept readable.
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
      <div>
        <Button disabled={connect.isPending} type="submit">
          {connect.isPending ? "Verifying…" : "Save"}
        </Button>
      </div>
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
              Jobs on this cloud stop getting runners until an account is
              connected again. Runners that are still alive must finish first;
              the key itself is not changed on {title}.
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
      <h2 className="font-medium text-sm" id="iam">
        Creating the IAM user
      </h2>
      <p className="max-w-prose text-muted-foreground text-xs/relaxed">
        AmazonEC2FullAccess covers everything a runner needs: launching and
        terminating instances, reading images and subnets, and creating the
        egress-only security group Airship uses in the default VPC. Runner
        machines themselves get no AWS credentials unless a definition sets an
        instance profile.
      </p>
      <CodeBlock label="Copy IAM commands" value={commands} />
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
      <h2 className="font-medium text-sm" id="gcp-role">
        Creating the service account
      </h2>
      <p className="max-w-prose text-muted-foreground text-xs/relaxed">
        The Compute Admin role covers launching and deleting instances, reading
        images and zones, and creating the Cloud NAT that runners without a
        public address use. Runner machines get no service account unless a
        definition sets one with{" "}
        <code className="font-mono">serviceAccounts</code>. GPU runners also
        need a GPU quota in the zone, which new projects have at zero.
      </p>
      <CodeBlock label="Copy gcloud commands" value={commands} />
    </section>
  );
}
