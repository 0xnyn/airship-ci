"use client";

import type { Schemas } from "@airship/client";
import { Button, buttonVariants } from "@airship/ui/components/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "@airship/ui/components/dropdown-menu";
import { Field, FieldLabel } from "@airship/ui/components/field";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@airship/ui/components/table";
import { Textarea } from "@airship/ui/components/textarea";
import {
  ArrowUpRight,
  Compass,
  GitPullRequest,
  MoreHorizontal,
  Plus,
} from "lucide-react";
import type { Route } from "next";
import Link from "next/link";
import { type FormEvent, type ReactNode, useEffect, useState } from "react";

import { $api } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";
import { listDrafts } from "@/lib/migration";
import { GitHubMark } from "./auth-frame";
import {
  EmptyRow,
  FilterTabs,
  Pagination,
  TableSkeleton,
  useClientPages,
} from "./data-table";
import { DocsLink } from "./docs-link";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";
import {
  SettingsCard,
  SettingsCardBody,
  SettingsCardFooter,
  SettingsCardHeader,
} from "./panel";
import { RepositoryTable } from "./repository-table";
import { StatusDot } from "./status";
import { RelativeTime } from "./time";

const POLL_MS = 15_000;
const POOLS = "/v1/orgs/{org}/pools";

const EXAMPLE = `runners:
  - name: build
    cloud: aws
    instance_type: [c7i.xlarge, m7i.xlarge]
    machine_image: ubuntu2404
    region: eu-west-1
    preemptible: [true, false]
    labels: [airship-build]
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
        action={<Actions />}
        description="Repositories Airship may run jobs for"
        title="Repositories"
      />
      <RepositoryTable />
      <Section title="Idle runner pools">
        <Pools />
      </Section>
      <Section title="Organization runners">
        <OrganizationConfig />
      </Section>
      <Validator />
    </>
  );
}

function Actions() {
  const { isAdmin, org } = useOrg();
  const config = $api.useQuery("get", "/v1/auth/config");
  const installUrl = config.data?.installUrl;
  const [drafts, setDrafts] = useState(0);

  useEffect(() => {
    setDrafts(listDrafts(org.login).length);
  }, [org.login]);

  return (
    <div className="flex items-center gap-2">
      <DropdownMenu>
        <DropdownMenuTrigger
          className={buttonVariants({ size: "icon", variant: "outline" })}
        >
          <MoreHorizontal />
          <span className="sr-only">More actions</span>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-64">
          {isAdmin ? (
            <DropdownMenuItem
              render={
                <Link href={`/${org.login}/repositories/migrate` as Route} />
              }
            >
              <GitPullRequest />
              Move workflows
              {drafts > 0 ? (
                <DropdownMenuShortcut className="tracking-normal">
                  {drafts} in progress
                </DropdownMenuShortcut>
              ) : null}
            </DropdownMenuItem>
          ) : null}
          <DropdownMenuItem
            render={<Link href={`/${org.login}/setup` as Route} />}
          >
            <Compass />
            Guided setup
          </DropdownMenuItem>
          {installUrl ? (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                render={
                  <a
                    href={installUrl}
                    rel="noopener noreferrer"
                    target="_blank"
                  />
                }
              >
                <GitHubMark className="size-4" />
                GitHub access
                <ArrowUpRight className="ml-auto text-muted-foreground" />
              </DropdownMenuItem>
            </>
          ) : null}
        </DropdownMenuContent>
      </DropdownMenu>
      {isAdmin ? (
        <Button
          nativeButton={false}
          render={<Link href={`/${org.login}/repositories/new` as Route} />}
        >
          <Plus />
          Add repository
        </Button>
      ) : null}
    </div>
  );
}

function Section({ children, title }: { children: ReactNode; title: string }) {
  return (
    <section className="grid gap-3">
      <h2 className="font-semibold text-base tracking-tight">{title}</h2>
      {children}
    </section>
  );
}

// The API refuses files over this many bytes.
const MAX_CONFIG_BYTES = 65_536;

const modeLabel: Record<Schemas["Pool"]["mode"], string> = {
  fixed: "Always",
  schedule: "Scheduled",
  usage: "After a job",
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
  const { pagination, rows } = useClientPages(items);

  if (pools.isPending) {
    return <TableSkeleton />;
  }
  if (items.length === 0) {
    return (
      <EmptyRow>
        No idle pools. Add an <code className="font-mono">idle:</code> section
        to a runner to keep machines ready before jobs arrive.{" "}
        <DocsLink page="Idle runners" />
      </EmptyRow>
    );
  }
  return (
    <Table className="text-sm" footer={<Pagination {...pagination} />}>
      <TableHeader>
        <TableRow>
          <TableHead>Runner</TableHead>
          <TableHead>Mode</TableHead>
          <TableHead className="text-right">Wanted</TableHead>
          <TableHead className="text-right">Idle</TableHead>
          <TableHead className="text-right">Running jobs</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((pool) => (
          <TableRow key={pool.id}>
            <TableCell>
              <div className="grid gap-0.5">
                <span className="font-medium font-mono text-[13px]">
                  {pool.name}
                </span>
                <span className="text-muted-foreground text-xs">
                  {pool.repository}
                </span>
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
            <TableCell className="text-right tabular-nums">
              {pool.desired}
            </TableCell>
            <TableCell className="text-right tabular-nums">
              {pool.idle}
            </TableCell>
            <TableCell className="text-right tabular-nums">
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
        No organization runners. Add an{" "}
        <code className="font-mono">airship.global.yaml</code> to a{" "}
        <code className="font-mono">.airship</code> repository to share
        definitions. <DocsLink page="Organization-wide runners" />
      </EmptyRow>
    );
  }
  return (
    <div className="grid gap-3">
      {problem ? (
        <p className="text-danger-foreground text-sm" role="alert">
          {problem}
        </p>
      ) : null}
      <p className="text-muted-foreground text-sm">
        {restricted
          ? "access.yaml decides which repositories and people can use these runners"
          : "No access.yaml, so every activated repository can use these runners"}{" "}
        <DocsLink page="Access control" />
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
  const { pagination, rows } = useClientPages(runners ?? []);
  if (!runners?.length) {
    return null;
  }
  return (
    <Table className="text-sm" footer={<Pagination {...pagination} />}>
      <TableHeader>
        <TableRow>
          <TableHead>Runner</TableHead>
          <TableHead>Labels</TableHead>
          <TableHead>Machines, in fallback order</TableHead>
          <TableHead>Idle pool</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((runner) => (
          <TableRow key={runner.name}>
            <TableCell className="font-medium">{runner.name}</TableCell>
            <TableCell className="font-mono text-xs">
              {(runner.labels ?? []).join(", ")}
            </TableCell>
            <TableCell className="font-mono text-xs">
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
  { label: "airship.yaml", value: "airship.yaml" },
  { label: "airship.global.yaml", value: "airship.global.yaml" },
  { label: "access.yaml", value: "access.yaml" },
] as const satisfies readonly { label: string; value: ConfigFile }[];

function Validator() {
  const { org } = useOrg();
  const [file, setFile] = useState<ConfigFile>("airship.yaml");
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

  let note: ReactNode = "Nothing is saved; this only reads the file";
  if (validate.isError) {
    note = (
      <span className="text-danger-foreground">
        {errorMessage(validate.error, "Could not check the file")}
      </span>
    );
  } else if (result?.valid) {
    note = <StatusDot label="Valid" tone="success" />;
  } else if (result?.problem) {
    note = (
      <span className="text-danger-foreground">
        {result.problem.path ? (
          <code className="font-mono">{result.problem.path}: </code>
        ) : null}
        {result.problem.detail}
      </span>
    );
  }

  return (
    <SettingsCard aria-labelledby="check-config">
      <form onSubmit={submit}>
        <SettingsCardHeader
          action={
            <FilterTabs
              label="Config file"
              onChange={(next) => {
                setFile(next);
                validate.reset();
              }}
              options={fileOptions}
              value={file}
            />
          }
          description="Paste a file to see what Airship reads from it"
          id="check-config"
          title="Check a config file"
        />
        <SettingsCardBody className="grid gap-4">
          <Field>
            <FieldLabel className="sr-only" htmlFor="config-content">
              File content
            </FieldLabel>
            <Textarea
              className="min-h-56 bg-background font-mono text-xs/relaxed"
              id="config-content"
              maxLength={MAX_CONFIG_BYTES}
              onChange={(event) => {
                setContent(event.target.value);
                validate.reset();
              }}
              spellCheck={false}
              value={content}
            />
          </Field>
          {result?.valid ? <RunnerSummary runners={result.runners} /> : null}
        </SettingsCardBody>
        <SettingsCardFooter note={note}>
          <Button disabled={validate.isPending} type="submit">
            {validate.isPending ? "Checking…" : "Check"}
          </Button>
        </SettingsCardFooter>
      </form>
    </SettingsCard>
  );
}
