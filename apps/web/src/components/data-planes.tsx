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
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@airship/ui/components/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@airship/ui/components/dropdown-menu";
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@airship/ui/components/field";
import { Input } from "@airship/ui/components/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@airship/ui/components/table";
import { useQueryClient } from "@tanstack/react-query";
import { MoreHorizontal, Plus } from "lucide-react";
import { type FormEvent, useState } from "react";
import { toast } from "sonner";

import { $api, invalidatePath } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";

import { CodeBlock } from "./copy-button";
import { EmptyRow, FilterTabs, TableSkeleton } from "./data-table";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";
import { DataPlaneStatus } from "./status";
import { RelativeTime } from "./time";

const POLL_MS = 10_000;
const DATA_PLANES = "/v1/orgs/{org}/data-planes";
const NAME_PATTERN = "[a-z0-9][a-z0-9\\-]*";
const AGENT_IMAGE = "ghcr.io/wizenheimer/airship-agent:latest";
const CLOUDFORMATION_URL =
  "https://github.com/wizenheimer/airship/blob/main/deploy/aws/data-plane.yaml";
const CLOUDFORMATION_RAW_URL =
  "https://raw.githubusercontent.com/wizenheimer/airship/main/deploy/aws/data-plane.yaml";

const providerLabel: Record<string, string> = {
  aws: "AWS EC2",
  docker: "Docker",
  fake: "Fake",
};

export function DataPlanes() {
  const { isAdmin, org } = useOrg();
  const [creating, setCreating] = useState(false);
  const [credentials, setCredentials] = useState<
    Schemas["DataPlaneCredentials"] | null
  >(null);
  const planes = $api.useQuery(
    "get",
    DATA_PLANES,
    { params: { path: { org: org.login } } },
    { refetchInterval: POLL_MS }
  );
  const items = planes.data?.items ?? [];

  return (
    <>
      <PageHeader
        action={
          isAdmin ? (
            <Button onClick={() => setCreating(true)}>
              <Plus aria-hidden="true" />
              New data plane
            </Button>
          ) : null
        }
        description="A data plane is the Airship agent running in your own AWS account or on your own Docker host. It connects out to Airship, so it needs no inbound access, and your runners and their data never leave your infrastructure."
        title="Data planes"
      />
      {planes.isPending ? <TableSkeleton /> : null}
      {planes.isSuccess && items.length === 0 ? (
        <EmptyRow>
          No data planes yet.
          {isAdmin
            ? " Create one to get a token for the agent."
            : " Ask an admin to create one."}
        </EmptyRow>
      ) : null}
      {items.length > 0 ? (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Provider</TableHead>
              <TableHead className="text-right">Max runners</TableHead>
              <TableHead>Agent</TableHead>
              <TableHead>Token</TableHead>
              <TableHead className="text-right">Last seen</TableHead>
              {isAdmin ? (
                <TableHead className="w-10">
                  <span className="sr-only">Actions</span>
                </TableHead>
              ) : null}
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((plane) => (
              <TableRow key={plane.id}>
                <TableCell className="font-medium">{plane.name}</TableCell>
                <TableCell>
                  <DataPlaneStatus status={plane.status} />
                </TableCell>
                <TableCell>
                  <div className="grid gap-0.5">
                    <span>
                      {plane.provider
                        ? (providerLabel[plane.provider] ?? plane.provider)
                        : "—"}
                    </span>
                    {plane.region ? (
                      <span className="font-mono text-[11px] text-muted-foreground">
                        {plane.region}
                      </span>
                    ) : null}
                  </div>
                </TableCell>
                <TableCell className="text-right font-mono tabular-nums">
                  {plane.maxRunners}
                </TableCell>
                <TableCell className="font-mono text-[11px]">
                  {plane.agentVersion ?? "—"}
                </TableCell>
                <TableCell className="font-mono text-[11px] text-muted-foreground">
                  {plane.tokenHint}…
                </TableCell>
                <TableCell className="text-right text-muted-foreground">
                  <RelativeTime value={plane.lastSeenAt} />
                </TableCell>
                {isAdmin ? (
                  <TableCell>
                    <DataPlaneActions
                      onRotated={setCredentials}
                      plane={plane}
                    />
                  </TableCell>
                ) : null}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      ) : null}
      <CreateDataPlaneDialog
        onCreated={(created) => {
          setCreating(false);
          setCredentials(created);
        }}
        onOpenChange={setCreating}
        open={creating}
      />
      <CredentialsDialog
        credentials={credentials}
        onClose={() => setCredentials(null)}
      />
    </>
  );
}

function CreateDataPlaneDialog({
  onCreated,
  onOpenChange,
  open,
}: {
  onCreated: (credentials: Schemas["DataPlaneCredentials"]) => void;
  onOpenChange: (open: boolean) => void;
  open: boolean;
}) {
  const { org } = useOrg();
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const create = $api.useMutation("post", DATA_PLANES, {
    onSuccess: async (created) => {
      setName("");
      onCreated(created);
      await invalidatePath(queryClient, DATA_PLANES);
    },
  });

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    create.mutate({
      body: { name: name.trim() },
      params: { path: { org: org.login } },
    });
  };

  return (
    <Dialog
      onOpenChange={(next) => {
        create.reset();
        onOpenChange(next);
      }}
      open={open}
    >
      <DialogContent>
        <form className="grid gap-4" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>New data plane</DialogTitle>
            <DialogDescription>
              Name it after where it runs. You will get a token for the agent
              next.
            </DialogDescription>
          </DialogHeader>
          <Field data-invalid={create.isError || undefined}>
            <FieldLabel htmlFor="data-plane-name">Name</FieldLabel>
            <Input
              aria-invalid={create.isError || undefined}
              autoComplete="off"
              id="data-plane-name"
              maxLength={63}
              onChange={(event) => setName(event.target.value.toLowerCase())}
              pattern={NAME_PATTERN}
              placeholder="aws-us-east-1"
              required
              value={name}
            />
            <FieldDescription>
              Lowercase letters, numbers and dashes.
            </FieldDescription>
            {create.isError ? (
              <FieldError>
                {errorMessage(create.error, "Could not create the data plane")}
              </FieldError>
            ) : null}
          </Field>
          <DialogFooter>
            <Button
              disabled={create.isPending || name.trim() === ""}
              type="submit"
            >
              {create.isPending ? "Creating…" : "Create"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function DataPlaneActions({
  onRotated,
  plane,
}: {
  onRotated: (credentials: Schemas["DataPlaneCredentials"]) => void;
  plane: Schemas["DataPlane"];
}) {
  const { org } = useOrg();
  const queryClient = useQueryClient();
  const [confirm, setConfirm] = useState<"rotate" | "delete" | null>(null);
  const [limiting, setLimiting] = useState(false);
  const path = { params: { path: { id: plane.id, org: org.login } } };

  const rotate = $api.useMutation(
    "post",
    "/v1/orgs/{org}/data-planes/{id}/rotate-token",
    {
      onError: (error) => {
        toast.error(errorMessage(error, "Could not rotate the token"));
      },
      onSuccess: async (credentials) => {
        setConfirm(null);
        onRotated(credentials);
        await invalidatePath(queryClient, DATA_PLANES);
      },
    }
  );
  const remove = $api.useMutation("delete", "/v1/orgs/{org}/data-planes/{id}", {
    onError: (error) => {
      setConfirm(null);
      toast.error(errorMessage(error, "Could not delete the data plane"));
    },
    onSuccess: async () => {
      setConfirm(null);
      toast.success(`Deleted ${plane.name}`);
      await invalidatePath(queryClient, DATA_PLANES);
    },
  });

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              aria-label={`Actions for ${plane.name}`}
              size="icon-sm"
              variant="ghost"
            />
          }
        >
          <MoreHorizontal />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onClick={() => setLimiting(true)}>
            Change runner limit
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => setConfirm("rotate")}>
            Rotate token
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => setConfirm("delete")}
            variant="destructive"
          >
            Delete
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <AlertDialog
        onOpenChange={(open) => setConfirm(open ? confirm : null)}
        open={confirm !== null}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {confirm === "delete"
                ? `Delete ${plane.name}?`
                : `Rotate the token for ${plane.name}?`}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {confirm === "delete"
                ? "Its agent stops being able to connect. Delete or move the profiles that use it first."
                : "The current token stops working immediately. Update the agent with the new one to bring it back online."}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={rotate.isPending || remove.isPending}
              onClick={() =>
                confirm === "delete" ? remove.mutate(path) : rotate.mutate(path)
              }
              variant={confirm === "delete" ? "destructive" : "default"}
            >
              {confirm === "delete" ? "Delete" : "Rotate"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <RunnerLimitDialog
        onOpenChange={setLimiting}
        open={limiting}
        plane={plane}
      />
    </>
  );
}

function RunnerLimitDialog({
  onOpenChange,
  open,
  plane,
}: {
  onOpenChange: (open: boolean) => void;
  open: boolean;
  plane: Schemas["DataPlane"];
}) {
  const { org } = useOrg();
  const queryClient = useQueryClient();
  const [value, setValue] = useState(String(plane.maxRunners));
  const update = $api.useMutation("patch", "/v1/orgs/{org}/data-planes/{id}", {
    onSuccess: async () => {
      onOpenChange(false);
      toast.success(`${plane.name} now runs at most ${value} runners`);
      await invalidatePath(queryClient, DATA_PLANES);
    },
  });

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    update.mutate({
      body: { maxRunners: Number(value) },
      params: { path: { id: plane.id, org: org.login } },
    });
  };

  return (
    <Dialog
      onOpenChange={(next) => {
        update.reset();
        setValue(String(plane.maxRunners));
        onOpenChange(next);
      }}
      open={open}
    >
      <DialogContent>
        <form className="grid gap-4" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>Runner limit for {plane.name}</DialogTitle>
            <DialogDescription>
              The most runners this data plane runs at once, across profiles,
              config files and warm pools. Jobs past it wait in the queue.
            </DialogDescription>
          </DialogHeader>
          <Field data-invalid={update.isError || undefined}>
            <FieldLabel htmlFor={`limit-${plane.id}`}>Max runners</FieldLabel>
            <Input
              aria-invalid={update.isError || undefined}
              id={`limit-${plane.id}`}
              inputMode="numeric"
              max={10_000}
              min={1}
              onChange={(event) => setValue(event.target.value)}
              required
              type="number"
              value={value}
            />
            {update.isError ? (
              <FieldError>
                {errorMessage(update.error, "Could not change the limit")}
              </FieldError>
            ) : null}
          </Field>
          <DialogFooter>
            <Button disabled={update.isPending} type="submit">
              {update.isPending ? "Saving…" : "Save"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

type Install = "docker" | "aws";

const installOptions = [
  { label: "AWS", value: "aws" },
  { label: "Docker host", value: "docker" },
] as const satisfies readonly { label: string; value: Install }[];

function CredentialsDialog({
  credentials,
  onClose,
}: {
  credentials: Schemas["DataPlaneCredentials"] | null;
  onClose: () => void;
}) {
  const [install, setInstall] = useState<Install>("aws");
  if (!credentials) {
    return null;
  }
  const { controlPlaneUrl, dataPlane, token } = credentials;
  const docker = [
    "docker run -d --name airship-agent --restart unless-stopped \\",
    "  --user root -v /var/run/docker.sock:/var/run/docker.sock \\",
    `  -e AIRSHIP_URL=${controlPlaneUrl} \\`,
    `  -e AIRSHIP_TOKEN=${token} \\`,
    "  -e AIRSHIP_PROVIDER=docker \\",
    `  ${AGENT_IMAGE}`,
  ].join("\n");
  const aws = [
    `curl -fsSLO ${CLOUDFORMATION_RAW_URL}`,
    `aws cloudformation deploy --stack-name airship-${dataPlane.name} \\`,
    "  --template-file data-plane.yaml --capabilities CAPABILITY_IAM \\",
    "  --parameter-overrides \\",
    `    ControlPlaneUrl=${controlPlaneUrl} \\`,
    `    DataPlaneId=${dataPlane.id} \\`,
    `    AgentToken=${token} \\`,
    "    VpcId=vpc-REPLACE SubnetIds=subnet-REPLACE,subnet-REPLACE",
  ].join("\n");

  return (
    <Dialog onOpenChange={(open) => (open ? null : onClose())} open>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Connect {dataPlane.name}</DialogTitle>
          <DialogDescription>
            This token is shown once. Airship only stores a hash of it; if you
            lose it, rotate it.
          </DialogDescription>
        </DialogHeader>
        <CodeBlock label="Copy token" value={token} />
        <div className="grid gap-3">
          <FilterTabs
            label="Where the agent runs"
            onChange={setInstall}
            options={installOptions}
            value={install}
          />
          {install === "aws" ? (
            <>
              <p className="text-muted-foreground">
                Deploy the{" "}
                <a
                  className="underline"
                  href={CLOUDFORMATION_URL}
                  rel="noopener noreferrer"
                  target="_blank"
                >
                  CloudFormation template
                </a>{" "}
                into the account and VPC where runners should start. It runs the
                agent on Fargate with an IAM role that can only launch and
                terminate instances tagged for this data plane.
              </p>
              <CodeBlock label="Copy AWS command" value={aws} />
            </>
          ) : (
            <>
              <p className="text-muted-foreground">
                Runs each job in a fresh container on this host. Mounting the
                Docker socket gives the agent root on the host; use a machine
                dedicated to CI.
              </p>
              <CodeBlock label="Copy Docker command" value={docker} />
            </>
          )}
        </div>
        <DialogFooter>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
