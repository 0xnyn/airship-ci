"use client";

import type { Schemas } from "@airship/client";
import { Badge } from "@airship/ui/components/badge";
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
  Field,
  FieldContent,
  FieldDescription,
  FieldLabel,
} from "@airship/ui/components/field";
import { Switch } from "@airship/ui/components/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@airship/ui/components/table";
import { Textarea } from "@airship/ui/components/textarea";
import { useQueryClient } from "@tanstack/react-query";
import { type FormEvent, type ReactNode, useState } from "react";
import { toast } from "sonner";

import { $api, invalidatePath } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";

import { APITokens } from "./api-tokens";
import { EmptyRow, FilterTabs, TableSkeleton } from "./data-table";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";
import { StatusDot } from "./status";
import { RelativeTime } from "./time";

const POLL_MS = 15_000;
const REPOSITORIES = "/v1/orgs/{org}/repositories";
const ACTIVATION = "/v1/orgs/{org}/repositories/{repo}/activation";
const SHA_PREFIX = 7;

const EXAMPLE = `runners:
  - name: build
    cloud: aws
    instance_type: [c7i.xlarge, m7i.xlarge]
    machine_image: ami-0123456789abcdef0
    preemptible: true
    labels: [airship-build]
    idle:
      replicas: 1
`;

export function RunnerConfig() {
  const { isAdmin } = useOrg();

  return (
    <>
      <PageHeader
        description={
          <>
            Commit a <code className="font-mono">.airship.yml</code> to a
            repository to define its runners in code, with fallbacks and warm
            pools. Organization-wide runners live in{" "}
            <code className="font-mono">.airship.global.yml</code> in a
            repository named <code className="font-mono">.airship</code>, with
            who may use them in <code className="font-mono">.access.yml</code>.
            A config file only launches runners in repositories an admin
            activated here.
          </>
        }
        title="Runner config"
      />
      <Section title="Repositories">
        <Repositories />
      </Section>
      <Section title="Warm pools">
        <Pools />
      </Section>
      <Section title="Organization runners">
        <OrganizationConfig />
      </Section>
      <Section title="Check a config file">
        <Validator />
      </Section>
      {isAdmin ? (
        <Section title="API tokens">
          <APITokens />
        </Section>
      ) : null}
    </>
  );
}

function Section({ children, title }: { children: ReactNode; title: string }) {
  return (
    <section className="grid gap-3">
      <h2 className="font-medium text-sm">{title}</h2>
      {children}
    </section>
  );
}

function Repositories() {
  const { isAdmin, org } = useOrg();
  const [activating, setActivating] = useState<Schemas["Repository"] | null>(
    null
  );
  const repos = $api.useQuery(
    "get",
    REPOSITORIES,
    { params: { path: { org: org.login } } },
    { refetchInterval: POLL_MS }
  );
  const items = repos.data?.items ?? [];

  if (repos.isPending) {
    return <TableSkeleton />;
  }
  if (repos.isError) {
    return (
      <EmptyRow>
        {errorMessage(repos.error, "Could not list repositories")}
      </EmptyRow>
    );
  }
  if (items.length === 0) {
    return (
      <EmptyRow>
        The Airship app cannot see any repositories. Grant it access to the
        repositories you want to run.
      </EmptyRow>
    );
  }

  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Repository</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>.airship.yml on the default branch</TableHead>
            <TableHead className="text-right">Checked</TableHead>
            {isAdmin ? (
              <TableHead className="w-28">
                <span className="sr-only">Actions</span>
              </TableHead>
            ) : null}
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((repo) => (
            <TableRow key={repo.id}>
              <TableCell className="max-w-72">
                <div className="flex items-center gap-2">
                  <span className="truncate font-medium">{repo.fullName}</span>
                  {repo.private ? null : (
                    <Badge variant="outline">Public</Badge>
                  )}
                </div>
              </TableCell>
              <TableCell>
                <RepositoryStatus repo={repo} />
              </TableCell>
              <TableCell className="max-w-96">
                <ConfigStatus repo={repo} />
              </TableCell>
              <TableCell className="text-right text-muted-foreground">
                {repo.config?.checkedAt ? (
                  <RelativeTime value={repo.config.checkedAt} />
                ) : (
                  "—"
                )}
              </TableCell>
              {isAdmin ? (
                <TableCell className="text-right">
                  <RepositoryAction
                    onActivate={() => setActivating(repo)}
                    repo={repo}
                  />
                </TableCell>
              ) : null}
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <ActivateDialog onClose={() => setActivating(null)} repo={activating} />
    </>
  );
}

function RepositoryStatus({ repo }: { repo: Schemas["Repository"] }) {
  if (!repo.activated) {
    return <StatusDot label="Not activated" tone="neutral" />;
  }
  if (!repo.accessible) {
    return <StatusDot label="App lost access" tone="danger" />;
  }
  if (!(repo.private || repo.allowPublic)) {
    return <StatusDot label="Paused while public" tone="warning" />;
  }
  return <StatusDot label="Active" tone="success" />;
}

function ConfigStatus({ repo }: { repo: Schemas["Repository"] }) {
  if (!repo.activated) {
    return <span className="text-muted-foreground">—</span>;
  }
  const { config } = repo;
  if (!config?.checkedAt) {
    return <span className="text-muted-foreground">Not read yet</span>;
  }
  if (config.error) {
    return (
      <span className="block truncate text-destructive" title={config.error}>
        {config.error}
      </span>
    );
  }
  if (!config.sha) {
    return (
      <span className="text-muted-foreground">
        None; jobs read the file at their own commit
      </span>
    );
  }
  return (
    <span className="font-mono text-[11px]">
      {config.sha.slice(0, SHA_PREFIX)}
    </span>
  );
}

function RepositoryAction({
  onActivate,
  repo,
}: {
  onActivate: () => void;
  repo: Schemas["Repository"];
}) {
  const { org } = useOrg();
  const queryClient = useQueryClient();
  const deactivate = $api.useMutation("delete", ACTIVATION, {
    onError: (error) => {
      toast.error(errorMessage(error, "Could not deactivate the repository"));
    },
    onSuccess: async () => {
      toast.success(`Deactivated ${repo.fullName}`);
      await invalidatePath(queryClient, REPOSITORIES);
    },
  });

  if (!repo.activated) {
    return (
      <Button onClick={onActivate} size="sm" variant="outline">
        Activate
      </Button>
    );
  }
  return (
    <Button
      disabled={deactivate.isPending}
      onClick={() =>
        deactivate.mutate({
          params: { path: { org: org.login, repo: repo.id } },
        })
      }
      size="sm"
      variant="ghost"
    >
      Deactivate
    </Button>
  );
}

function ActivateDialog({
  onClose,
  repo,
}: {
  onClose: () => void;
  repo: Schemas["Repository"] | null;
}) {
  const { org } = useOrg();
  const queryClient = useQueryClient();
  const [allowPublic, setAllowPublic] = useState(false);
  const activate = $api.useMutation("put", ACTIVATION, {
    onError: (error) => {
      toast.error(errorMessage(error, "Could not activate the repository"));
    },
    onSuccess: async (activated) => {
      onClose();
      setAllowPublic(false);
      toast.success(
        activated.config?.error
          ? `Activated ${activated.fullName}, but its .airship.yml has a problem`
          : `Activated ${activated.fullName}`
      );
      await invalidatePath(queryClient, REPOSITORIES);
      await invalidatePath(queryClient, "/v1/orgs/{org}/pools");
    },
  });
  if (!repo) {
    return null;
  }

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    activate.mutate({
      body: { allowPublic },
      params: { path: { org: org.login, repo: repo.id } },
    });
  };

  return (
    <Dialog onOpenChange={(open) => (open ? null : onClose())} open>
      <DialogContent>
        <form className="grid gap-4" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>Activate {repo.fullName}?</DialogTitle>
            <DialogDescription>
              Jobs asking for an <code className="font-mono">airship-</code>{" "}
              label will launch the runners its{" "}
              <code className="font-mono">.airship.yml</code> defines, and its
              warm pools start right away. Pull requests from forks always use
              the file on the default branch, never their own.
            </DialogDescription>
          </DialogHeader>
          <Field orientation="horizontal">
            <FieldContent>
              <FieldLabel htmlFor="allow-public">
                Keep serving it while it is public
              </FieldLabel>
              <FieldDescription>
                {repo.private
                  ? "If the repository is made public, Airship stops running its jobs unless this is on."
                  : "This repository is public: anyone can open a pull request and run code on your machines."}
              </FieldDescription>
            </FieldContent>
            <Switch
              checked={allowPublic}
              id="allow-public"
              onCheckedChange={setAllowPublic}
            />
          </Field>
          <DialogFooter>
            <Button disabled={activate.isPending} type="submit">
              {activate.isPending ? "Activating…" : "Activate"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

const modeLabel: Record<Schemas["Pool"]["mode"], string> = {
  fixed: "Always",
  schedule: "Scheduled",
  usage: "After use",
};

function Pools() {
  const { org } = useOrg();
  const pools = $api.useQuery(
    "get",
    "/v1/orgs/{org}/pools",
    { params: { path: { org: org.login } } },
    { refetchInterval: POLL_MS }
  );
  const items = pools.data?.items ?? [];

  if (pools.isPending) {
    return <TableSkeleton />;
  }
  if (items.length === 0) {
    return (
      <EmptyRow>
        No warm pools. Add an <code className="font-mono">idle:</code> section
        to a runner in an activated repository to keep machines booted ahead of
        jobs.
      </EmptyRow>
    );
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Runner</TableHead>
          <TableHead>Mode</TableHead>
          <TableHead className="text-right">Wanted now</TableHead>
          <TableHead className="text-right">Idle</TableHead>
          <TableHead className="text-right">Running jobs</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((pool) => (
          <TableRow key={pool.id}>
            <TableCell>
              <div className="grid gap-0.5">
                <span className="font-mono text-[11px]">{pool.name}</span>
                <span className="text-muted-foreground">{pool.repository}</span>
              </div>
            </TableCell>
            <TableCell>
              {modeLabel[pool.mode]}
              {pool.mode === "usage" && pool.warmUntil ? (
                <span className="block text-muted-foreground">
                  warm until <RelativeTime value={pool.warmUntil} />
                </span>
              ) : null}
            </TableCell>
            <TableCell className="text-right font-mono tabular-nums">
              {pool.desired}
            </TableCell>
            <TableCell className="text-right font-mono tabular-nums">
              {pool.idle}
            </TableCell>
            <TableCell className="text-right font-mono tabular-nums">
              {pool.taken}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function OrganizationConfig() {
  const { org } = useOrg();
  const config = $api.useQuery("get", "/v1/orgs/{org}/runner-config", {
    params: { path: { org: org.login } },
  });

  if (config.isPending) {
    return <TableSkeleton />;
  }
  if (config.isError) {
    return (
      <EmptyRow>
        {errorMessage(config.error, "Could not read the .airship repository")}
      </EmptyRow>
    );
  }
  const { found, problem, restricted, runners } = config.data;
  if (!found) {
    return (
      <EmptyRow>
        No organization runners. Create a repository named{" "}
        <code className="font-mono">.airship</code> with a{" "}
        <code className="font-mono">.airship.global.yml</code> to share runner
        definitions across repositories, and give the Airship app access to it.
      </EmptyRow>
    );
  }
  return (
    <div className="grid gap-3">
      {problem ? <p className="text-destructive">{problem}</p> : null}
      <p className="text-muted-foreground">
        {restricted
          ? ".access.yml decides which repositories and people can use these runners."
          : "There is no .access.yml, so every activated repository can use these runners."}
      </p>
      <RunnerSummary runners={runners} />
    </div>
  );
}

function RunnerSummary({
  runners,
}: {
  runners: Schemas["ConfigRunner"][] | null;
}) {
  if (!runners?.length) {
    return null;
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Runner</TableHead>
          <TableHead>Labels</TableHead>
          <TableHead>Machines, in fallback order</TableHead>
          <TableHead>Warm pool</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {runners.map((runner) => (
          <TableRow key={runner.name}>
            <TableCell>
              <div className="grid gap-0.5">
                <span className="font-medium">{runner.name}</span>
                <span className="text-muted-foreground">
                  {runner.cloud === "aws" ? "AWS" : "On-prem"}
                  {runner.dataPlane ? ` · ${runner.dataPlane}` : ""}
                </span>
              </div>
            </TableCell>
            <TableCell className="font-mono text-[11px]">
              {(runner.labels ?? []).join(", ")}
            </TableCell>
            <TableCell className="font-mono text-[11px]">
              {(runner.machines ?? []).join(" → ")}
            </TableCell>
            <TableCell className="text-muted-foreground">
              {runner.idle ?? "—"}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

type ConfigFile = Schemas["ValidateConfig"]["file"];

const fileOptions = [
  { label: ".airship.yml", value: ".airship.yml" },
  { label: ".airship.global.yml", value: ".airship.global.yml" },
  { label: ".access.yml", value: ".access.yml" },
] as const satisfies readonly { label: string; value: ConfigFile }[];

function Validator() {
  const { org } = useOrg();
  const [file, setFile] = useState<ConfigFile>(".airship.yml");
  const [content, setContent] = useState(EXAMPLE);
  const validate = $api.useMutation(
    "post",
    "/v1/orgs/{org}/runner-config/validate"
  );
  const result = validate.data;

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    validate.mutate({
      body: { content, file },
      params: { path: { org: org.login } },
    });
  };

  return (
    <form className="grid gap-3" onSubmit={submit}>
      <FilterTabs
        label="Config file"
        onChange={(next) => {
          setFile(next);
          validate.reset();
        }}
        options={fileOptions}
        value={file}
      />
      <Field>
        <FieldLabel className="sr-only" htmlFor="config-content">
          File content
        </FieldLabel>
        <Textarea
          className="min-h-48 font-mono text-[11px]"
          id="config-content"
          onChange={(event) => setContent(event.target.value)}
          spellCheck={false}
          value={content}
        />
      </Field>
      <div className="flex items-center gap-3">
        <Button disabled={validate.isPending} type="submit" variant="outline">
          {validate.isPending ? "Checking…" : "Check"}
        </Button>
        {validate.isError ? (
          <span className="text-destructive">
            {errorMessage(validate.error, "Could not check the file")}
          </span>
        ) : null}
        {result?.valid ? <StatusDot label="Valid" tone="success" /> : null}
        {result?.problem ? (
          <span className="text-destructive">
            {result.problem.path ? (
              <code className="font-mono">{result.problem.path}: </code>
            ) : null}
            {result.problem.detail}
          </span>
        ) : null}
      </div>
      {result?.valid ? <RunnerSummary runners={result.runners} /> : null}
    </form>
  );
}
