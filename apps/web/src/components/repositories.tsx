"use client";

import type { Schemas } from "@airship/client";
import { Badge } from "@airship/ui/components/badge";
import { Button, buttonVariants } from "@airship/ui/components/button";
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
  FieldError,
  FieldLabel,
} from "@airship/ui/components/field";
import { Input } from "@airship/ui/components/input";
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
import { ExternalLink } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";
import { toast } from "sonner";

import { $api, invalidatePath } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";

import { EmptyRow, FilterTabs, TableSkeleton } from "./data-table";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";
import { StatusDot } from "./status";
import { RelativeTime } from "./time";

const POLL_MS = 15_000;
const REPOSITORIES = "/v1/orgs/{org}/repositories";
const ACTIVATION = "/v1/orgs/{org}/repositories/{repo}/activation";
const POOLS = "/v1/orgs/{org}/pools";
const SHA_PREFIX = 7;
const GITHUB_URL = /^https:\/\/github\.com\//;

const EXAMPLE = `runners:
  - name: build
    cloud: aws
    instance_type: [c7i.xlarge, m7i.xlarge]
    machine_image: ubuntu2404
    region: eu-west-1
    preemptible: [true, false]
    labels: [cirun-build]
    extra_config:
      BlockDeviceMappings:
        - DeviceName: /dev/sda1
          VolumeSize: 100
          VolumeType: gp3
    idle:
      replicas: 1
`;

export function Repositories() {
  return (
    <>
      <PageHeader
        description={
          <>
            Airship only reads the <code className="font-mono">.cirun.yml</code>{" "}
            of repositories you activate here, so a file alone never launches a
            machine. Jobs in an activated repository that ask for a{" "}
            <code className="font-mono">cirun-</code> label get a fresh runner
            each.
          </>
        }
        title="Repositories"
      />
      <ConnectGitHub />
      <Activate />
      <Section title="Activated repositories">
        <ActivatedList />
      </Section>
      <Section title="Idle runner pools">
        <Pools />
      </Section>
      <Section title="Organization runners">
        <OrganizationConfig />
      </Section>
      <Section title="Check a config file">
        <Validator />
      </Section>
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

function Step({
  children,
  description,
  number,
  title,
}: {
  children: ReactNode;
  description: ReactNode;
  number: number;
  title: string;
}) {
  return (
    <section
      aria-labelledby={`step-${number}`}
      className="grid gap-4 border p-4 md:grid-cols-[auto_1fr] md:gap-6"
    >
      <span
        aria-hidden="true"
        className="grid size-6 place-items-center border font-mono text-[11px]"
      >
        {number}
      </span>
      <div className="grid gap-3">
        <div className="grid gap-1">
          <h2 className="font-medium text-sm" id={`step-${number}`}>
            {title}
          </h2>
          <p className="max-w-prose text-muted-foreground text-xs/relaxed">
            {description}
          </p>
        </div>
        {children}
      </div>
    </section>
  );
}

function ConnectGitHub() {
  const config = $api.useQuery("get", "/v1/auth/config");
  const installUrl = config.data?.installUrl;

  return (
    <Step
      description="The app is installed on this organization. Grant it access to every repository whose jobs should run here, including the .cirun repository if you define organization runners."
      number={1}
      title="Connect GitHub"
    >
      <div>
        {installUrl ? (
          <a
            className={buttonVariants({ variant: "outline" })}
            href={installUrl}
            rel="noopener noreferrer"
            target="_blank"
          >
            Manage app on GitHub
            <ExternalLink aria-hidden="true" />
          </a>
        ) : (
          <p className="text-muted-foreground text-xs">
            No GitHub App is configured for this deployment.
          </p>
        )}
      </div>
    </Step>
  );
}

function Activate() {
  const { isAdmin, org } = useOrg();
  const [name, setName] = useState("");
  const [problem, setProblem] = useState<string | null>(null);
  const [activating, setActivating] = useState<Schemas["Repository"] | null>(
    null
  );
  const repos = $api.useQuery("get", REPOSITORIES, {
    params: { path: { org: org.login } },
  });

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const typed = name.trim().replace(GITHUB_URL, "");
    const [owner, repoName] = typed.includes("/")
      ? typed.split("/", 2)
      : [org.login, typed];
    if (owner?.toLowerCase() !== org.login.toLowerCase()) {
      setProblem(`${typed} is not in ${org.login}.`);
      return;
    }
    const match = (repos.data?.items ?? []).find(
      (repo) =>
        repo.fullName.toLowerCase() === `${org.login}/${repoName}`.toLowerCase()
    );
    if (!match?.accessible) {
      setProblem(
        `The app cannot see ${org.login}/${repoName}. Grant it access on GitHub first.`
      );
      return;
    }
    if (match.activated) {
      setProblem(`${match.fullName} is already active.`);
      return;
    }
    setProblem(null);
    setActivating(match);
  };

  return (
    <Step
      description="Enter a repository to activate Airship on it. Its runners are defined in a .cirun.yml at the root of the repository, read at each job's commit."
      number={2}
      title="Activate a repository"
    >
      {isAdmin ? (
        <form className="grid gap-2" onSubmit={submit}>
          <div className="flex gap-2">
            <Field
              className="flex-1"
              data-invalid={problem !== null || undefined}
            >
              <FieldLabel className="sr-only" htmlFor="activate-repo">
                Repository
              </FieldLabel>
              <Input
                aria-invalid={problem !== null || undefined}
                autoComplete="off"
                className="font-mono"
                id="activate-repo"
                onChange={(event) => {
                  setName(event.target.value);
                  setProblem(null);
                }}
                placeholder={`${org.login}/repo`}
                required
                spellCheck={false}
                value={name}
              />
            </Field>
            <Button
              disabled={repos.isPending || name.trim() === ""}
              type="submit"
            >
              Activate
            </Button>
          </div>
          {problem ? <FieldError>{problem}</FieldError> : null}
        </form>
      ) : (
        <p className="text-muted-foreground text-xs">
          Only organization owners can activate repositories.
        </p>
      )}
      <ActivateDialog
        onClose={() => {
          setActivating(null);
          setName("");
        }}
        repo={activating}
      />
    </Step>
  );
}

function ActivatedList() {
  const { isAdmin, org } = useOrg();
  const repos = $api.useQuery(
    "get",
    REPOSITORIES,
    { params: { path: { org: org.login } } },
    { refetchInterval: POLL_MS }
  );
  const items = (repos.data?.items ?? []).filter((repo) => repo.activated);
  const visible = (repos.data?.items ?? []).length;

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
        {visible === 0
          ? "The app cannot see any repositories yet. Grant it access on GitHub."
          : `None of the ${visible} repositories the app can see is activated.`}
      </EmptyRow>
    );
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Repository</TableHead>
          <TableHead>Status</TableHead>
          <TableHead>.cirun.yml on the default branch</TableHead>
          <TableHead className="text-right">Checked</TableHead>
          <TableHead className="w-0 text-right">Active</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((repo) => (
          <TableRow key={repo.id}>
            <TableCell className="max-w-72">
              <div className="grid gap-0.5">
                <span className="flex items-center gap-2">
                  <a
                    className="inline-flex items-center gap-1 truncate font-medium hover:underline"
                    href={`https://github.com/${repo.fullName}`}
                    rel="noopener noreferrer"
                    target="_blank"
                  >
                    {repo.fullName}
                    <ExternalLink
                      aria-hidden="true"
                      className="size-3 shrink-0 text-muted-foreground"
                    />
                  </a>
                  {repo.private ? null : (
                    <Badge variant="outline">Public</Badge>
                  )}
                </span>
                <span className="font-mono text-[11px] text-muted-foreground">
                  {repo.defaultBranch}
                  {repo.allowPublic ? " · runs while public" : ""}
                </span>
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
            <TableCell className="text-right">
              <ActiveSwitch disabled={!isAdmin} repo={repo} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function RepositoryStatus({ repo }: { repo: Schemas["Repository"] }) {
  if (!repo.accessible) {
    return <StatusDot label="App lost access" tone="danger" />;
  }
  if (!(repo.private || repo.allowPublic)) {
    return <StatusDot label="Paused while public" tone="warning" />;
  }
  if (repo.config?.error) {
    return <StatusDot label="Config problem" tone="warning" />;
  }
  return <StatusDot label="Active" tone="success" />;
}

function ConfigStatus({ repo }: { repo: Schemas["Repository"] }) {
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
        None; jobs can still use one-line labels and organization runners
      </span>
    );
  }
  return (
    <span className="font-mono text-[11px]">
      {config.sha.slice(0, SHA_PREFIX)}
    </span>
  );
}

function ActiveSwitch({
  disabled,
  repo,
}: {
  disabled: boolean;
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
      await invalidatePath(queryClient, REPOSITORIES, POOLS);
    },
  });

  return (
    <Switch
      aria-label={`Deactivate ${repo.fullName}`}
      checked
      disabled={disabled || deactivate.isPending}
      onCheckedChange={() =>
        deactivate.mutate({
          params: { path: { org: org.login, repo: repo.id } },
        })
      }
    />
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
          ? `Activated ${activated.fullName}, but its .cirun.yml has a problem`
          : `Activated ${activated.fullName}`
      );
      await invalidatePath(queryClient, REPOSITORIES, POOLS);
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
              Jobs asking for a <code className="font-mono">cirun-</code> label
              will launch the machines its{" "}
              <code className="font-mono">.cirun.yml</code> defines, in your
              connected cloud account, and its idle pools start right away. Pull
              requests from forks always use the file on the default branch,
              never their own.
            </DialogDescription>
          </DialogHeader>
          <Field orientation="horizontal">
            <FieldContent>
              <FieldLabel htmlFor="allow-public">
                Keep running jobs while it is public
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
    POOLS,
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
        No idle pools. Add an <code className="font-mono">idle:</code> section
        to a runner in an activated repository to keep machines booted and
        registered ahead of jobs. Pools are read from the default branch.
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
        {errorMessage(config.error, "Could not read the .cirun repository")}
      </EmptyRow>
    );
  }
  const { found, problem, restricted, runners } = config.data;
  if (!found) {
    return (
      <EmptyRow>
        No organization runners. Create a repository named{" "}
        <code className="font-mono">.cirun</code> with a{" "}
        <code className="font-mono">.cirun.global.yml</code> to share runner
        definitions across repositories, and give the app access to it.
      </EmptyRow>
    );
  }
  return (
    <div className="grid gap-3">
      {problem ? <p className="text-destructive text-xs">{problem}</p> : null}
      <p className="text-muted-foreground text-xs">
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
          <TableHead>Idle pool</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {runners.map((runner) => (
          <TableRow key={runner.name}>
            <TableCell className="font-medium">{runner.name}</TableCell>
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
  { label: ".cirun.yml", value: ".cirun.yml" },
  { label: ".cirun.global.yml", value: ".cirun.global.yml" },
  { label: ".access.yml", value: ".access.yml" },
] as const satisfies readonly { label: string; value: ConfigFile }[];

function Validator() {
  const { org } = useOrg();
  const [file, setFile] = useState<ConfigFile>(".cirun.yml");
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
      <div className="flex items-center gap-3 text-xs">
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
