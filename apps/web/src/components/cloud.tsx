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
import { useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";
import { toast } from "sonner";

import { $api, invalidatePath } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";
import { awsRegions } from "@/lib/labels";

import { CodeBlock } from "./copy-button";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";
import { CloudStatus } from "./status";
import { RelativeTime } from "./time";

const CLOUDS = "/v1/orgs/{org}/clouds";
const DEFAULT_REGION = "eu-west-1";

export function CloudPage() {
  const { org } = useOrg();
  const clouds = $api.useQuery("get", CLOUDS, {
    params: { path: { org: org.login } },
  });
  const aws = clouds.data?.items?.find((item) => item.cloud === "aws");

  return (
    <>
      <PageHeader
        description="Airship launches every runner in your own cloud account with the credentials you connect here. They are stored encrypted and never shown again."
        title="Cloud credentials"
      />
      <section aria-labelledby="aws" className="grid gap-4 border p-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="grid gap-1">
            <h2 className="font-medium text-sm" id="aws">
              Amazon Web Services
            </h2>
            <p className="max-w-prose text-muted-foreground text-xs/relaxed">
              Create an IAM user with the AmazonEC2FullAccess policy, generate
              an access key for it, and paste the key here. Runners get an EC2
              instance each, in the default VPC of the region a{" "}
              <code className="font-mono">.cirun.yml</code> names, or the
              default region below.
            </p>
          </div>
          {aws ? <CloudStatus cloud={aws} /> : null}
        </div>
        {clouds.isPending ? <Skeleton className="h-32" /> : null}
        {clouds.isSuccess && aws ? <Connected cloud={aws} /> : null}
        {clouds.isSuccess ? <ConnectForm existing={aws} /> : null}
      </section>
      <Policy />
    </>
  );
}

function Connected({ cloud }: { cloud: Schemas["Cloud"] }) {
  const { isAdmin } = useOrg();
  return (
    <div className="grid gap-3">
      <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 text-xs">
        <dt className="text-muted-foreground">Access key</dt>
        <dd className="font-mono">{cloud.identity}</dd>
        <dt className="text-muted-foreground">Account</dt>
        <dd className="font-mono">{cloud.accountId ?? "—"}</dd>
        <dt className="text-muted-foreground">Identity</dt>
        <dd className="truncate font-mono" title={cloud.principal ?? undefined}>
          {cloud.principal ?? "—"}
        </dd>
        <dt className="text-muted-foreground">Default region</dt>
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

function ConnectForm({ existing }: { existing: Schemas["Cloud"] | undefined }) {
  const { isAdmin, org } = useOrg();
  const queryClient = useQueryClient();
  const [accessKeyId, setAccessKeyId] = useState("");
  const [secretAccessKey, setSecretAccessKey] = useState("");
  const [region, setRegion] = useState(
    existing?.defaultRegion ?? DEFAULT_REGION
  );
  const connect = $api.useMutation("put", "/v1/orgs/{org}/clouds/aws", {
    onSuccess: async (cloud) => {
      setAccessKeyId("");
      setSecretAccessKey("");
      toast.success(`Connected AWS account ${cloud.accountId ?? ""}`.trim());
      await invalidatePath(queryClient, CLOUDS);
    },
  });

  if (!isAdmin) {
    return existing ? null : (
      <p className="text-muted-foreground text-xs">
        No AWS account is connected. An organization owner can connect one.
      </p>
    );
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
      {existing ? (
        <p className="text-muted-foreground text-xs">
          Pasting a new key replaces the current one. Runners already launched
          keep working.
        </p>
      ) : null}
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
        <Field>
          <FieldLabel htmlFor="aws-region">Default region</FieldLabel>
          <select
            className="h-8 border bg-transparent px-2 font-mono text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring"
            id="aws-region"
            onChange={(event) => setRegion(event.target.value)}
            value={region}
          >
            {awsRegions.map((candidate) => (
              <option key={candidate} value={candidate}>
                {candidate}
              </option>
            ))}
          </select>
          <FieldDescription>
            Used when a runner definition names no region.
          </FieldDescription>
        </Field>
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

function Disconnect({ cloud }: { cloud: Schemas["Cloud"] }) {
  const { org } = useOrg();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const disconnect = $api.useMutation(
    "delete",
    "/v1/orgs/{org}/clouds/{cloud}",
    {
      onError: (error) => {
        setOpen(false);
        toast.error(errorMessage(error, "Could not disconnect AWS"));
      },
      onSuccess: async () => {
        setOpen(false);
        toast.success("Disconnected AWS");
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
            <AlertDialogTitle>Disconnect AWS?</AlertDialogTitle>
            <AlertDialogDescription>
              Jobs stop getting runners until an account is connected again.
              Runners that are still alive must finish first; the key itself is
              not changed on AWS.
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

function Policy() {
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
