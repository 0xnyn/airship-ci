"use client";

import type { Schemas } from "@airship/client";
import { buttonVariants } from "@airship/ui/components/button";
import { Skeleton } from "@airship/ui/components/skeleton";
import { cn } from "@airship/ui/lib/utils";
import { Check } from "lucide-react";
import type { Route } from "next";
import Link from "next/link";
import { $api } from "@/lib/api";
import { formatDuration, formatInteger } from "@/lib/format";
import { CodeBlock } from "./copy-button";
import { ErrorRow } from "./data-table";
import { EventRow } from "./events";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";
import { JobStatus } from "./status";
import { RelativeTime } from "./time";

const POLL_MS = 10_000;
const PERCENT = 100;
const RECENT = 6;

export function Overview() {
  const { org } = useOrg();
  const path = { params: { path: { org: org.login } } };
  const overview = $api.useQuery("get", "/v1/orgs/{org}/overview", path, {
    refetchInterval: POLL_MS,
  });
  const clouds = $api.useQuery("get", "/v1/orgs/{org}/clouds", path);
  const repos = $api.useQuery("get", "/v1/orgs/{org}/repositories", path);

  const cloudItems = clouds.data?.items ?? [];
  const activated = (repos.data?.items ?? []).filter((repo) => repo.activated);
  const ready = cloudItems.length > 0 && activated.length > 0;

  return (
    <>
      <PageHeader
        description="Jobs and runners across every activated repository in the last 24 hours."
        title="Dashboard"
      />
      {clouds.isSuccess && repos.isSuccess && !ready ? (
        <GettingStarted clouds={cloudItems} repositories={activated} />
      ) : null}
      {overview.isError ? (
        <ErrorRow
          error={overview.error}
          fallback="Could not load the overview"
        />
      ) : null}
      {overview.data ? <Stats overview={overview.data} /> : null}
      {overview.isPending ? <StatsSkeleton /> : null}
      <div className="grid gap-8 lg:grid-cols-2">
        <RecentJobs />
        <RecentEvents />
      </div>
      {ready ? <Usage /> : null}
    </>
  );
}

function Stats({ overview }: { overview: Schemas["Overview"] }) {
  const { org } = useOrg();
  const finished = overview.succeeded + overview.failed;
  const successRate =
    finished > 0
      ? `${Math.round((overview.succeeded / finished) * PERCENT)}%`
      : "—";
  const { runners } = overview;

  return (
    <div className="grid gap-6">
      <section
        aria-label="Jobs"
        className="grid grid-cols-2 gap-px bg-border ring-1 ring-border lg:grid-cols-4"
      >
        <Stat
          hint={`${formatInteger(overview.jobs)} queued in total`}
          label="Jobs on Airship"
          value={formatInteger(overview.airshipJobs)}
        />
        <Stat
          hint={`${formatInteger(overview.queued)} waiting, ${formatInteger(overview.inProgress)} running`}
          label="Success rate"
          value={successRate}
        />
        <Stat
          hint={`p90 ${overview.airshipJobs > 0 ? formatDuration(overview.queueP90Seconds) : "—"}`}
          label="Median queue time"
          value={
            overview.airshipJobs > 0
              ? formatDuration(overview.queueP50Seconds)
              : "—"
          }
        />
        <Stat
          hint="Time jobs spent on your machines"
          label="Runner minutes"
          value={formatInteger(Math.round(overview.runnerMinutes))}
        />
      </section>
      <section aria-labelledby="runners-now" className="grid gap-3">
        <div className="flex items-baseline justify-between">
          <h2 className="font-medium text-sm" id="runners-now">
            Runners now
          </h2>
          <Link
            className="text-muted-foreground text-xs hover:text-foreground"
            href={`/${org.login}/runners` as Route}
          >
            View runners
          </Link>
        </div>
        <div className="grid grid-cols-2 gap-px bg-border ring-1 ring-border lg:grid-cols-4">
          <Stat
            hint="Waiting for a free slot"
            label="Pending"
            value={formatInteger(runners.pending)}
          />
          <Stat
            hint="Registering and launching"
            label="Launching"
            value={formatInteger(runners.starting)}
          />
          <Stat
            hint="Booted, waiting for a job"
            label="Idle"
            value={formatInteger(runners.idle)}
          />
          <Stat
            hint="Running a job"
            label="Busy"
            value={formatInteger(runners.busy)}
          />
        </div>
      </section>
    </div>
  );
}

function Stat({
  hint,
  label,
  value,
}: {
  hint: string;
  label: string;
  value: string;
}) {
  return (
    <div className="grid gap-1 bg-card p-4">
      <span className="text-muted-foreground text-xs">{label}</span>
      <span className="font-medium font-mono text-2xl tabular-nums tracking-tight">
        {value}
      </span>
      <span className="truncate text-[11px] text-muted-foreground">{hint}</span>
    </div>
  );
}

function StatsSkeleton() {
  return (
    <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
      <Skeleton className="h-24" />
      <Skeleton className="h-24" />
      <Skeleton className="h-24" />
      <Skeleton className="h-24" />
    </div>
  );
}

function GettingStarted({
  clouds,
  repositories,
}: {
  clouds: Schemas["Cloud"][];
  repositories: Schemas["Repository"][];
}) {
  const { org } = useOrg();
  const steps = [
    {
      description:
        "The app is installed on this organization. Give it access to the repositories whose jobs should run here.",
      done: true,
      href: `/${org.login}/repositories`,
      title: "Install the GitHub App",
    },
    {
      description:
        "Paste an IAM access key under Cloud. Runners launch as EC2 instances in that account.",
      done: clouds.length > 0,
      href: `/${org.login}/cloud`,
      title: "Connect AWS",
    },
    {
      description:
        "Only activated repositories can launch machines, so a config file alone never spends your budget.",
      done: repositories.length > 0,
      href: `/${org.login}/repositories`,
      title: "Activate a repository",
    },
    {
      description:
        "Define runners in code and point a workflow at one of their labels.",
      done: false,
      href: `/${org.login}/repositories`,
      title: "Commit a .cirun.yml",
    },
  ];

  return (
    <section
      aria-labelledby="getting-started"
      className="grid gap-3 border p-4"
    >
      <h2 className="font-medium text-sm" id="getting-started">
        Get started
      </h2>
      <ol className="grid gap-3">
        {steps.map((step, index) => (
          <li className="flex items-start gap-3" key={step.title}>
            <span
              aria-hidden="true"
              className={cn(
                "grid size-5 shrink-0 place-items-center border font-mono text-[10px]",
                step.done && "border-emerald-500 bg-emerald-500 text-white"
              )}
            >
              {step.done ? <Check className="size-3" /> : index + 1}
            </span>
            <div className="grid flex-1 gap-0.5">
              <span
                className={cn(
                  "font-medium text-xs",
                  step.done && "text-muted-foreground line-through"
                )}
              >
                {step.title}
                <span className="sr-only">{step.done ? " (done)" : ""}</span>
              </span>
              <span className="text-muted-foreground text-xs/relaxed">
                {step.description}
              </span>
            </div>
            {step.done ? null : (
              <Link
                className={buttonVariants({ size: "sm", variant: "outline" })}
                href={step.href as Route}
              >
                Open
              </Link>
            )}
          </li>
        ))}
      </ol>
    </section>
  );
}

function RecentJobs() {
  const { org } = useOrg();
  const jobs = $api.useQuery(
    "get",
    "/v1/orgs/{org}/jobs",
    { params: { path: { org: org.login }, query: { limit: RECENT } } },
    { refetchInterval: POLL_MS }
  );
  const items = jobs.data?.items ?? [];

  return (
    <section aria-labelledby="recent-jobs" className="grid content-start gap-3">
      <div className="flex items-baseline justify-between">
        <h2 className="font-medium text-sm" id="recent-jobs">
          Recent jobs
        </h2>
        <Link
          className="text-muted-foreground text-xs hover:text-foreground"
          href={`/${org.login}/jobs` as Route}
        >
          All jobs
        </Link>
      </div>
      {jobs.isPending ? <Skeleton className="h-40" /> : null}
      {jobs.isError ? (
        <ErrorRow error={jobs.error} fallback="Could not load jobs" />
      ) : null}
      {jobs.isSuccess && items.length === 0 ? (
        <p className="border border-dashed p-6 text-center text-muted-foreground text-xs">
          No jobs yet. Push to an activated repository to see them here.
        </p>
      ) : null}
      {items.length > 0 ? (
        <ul className="divide-y border">
          {items.map((job) => (
            <li
              className="grid grid-cols-[1fr_auto] items-center gap-3 px-3 py-2 text-xs"
              key={job.id}
            >
              <div className="grid gap-0.5">
                <span className="truncate font-medium">
                  {job.workflowName ? `${job.workflowName} / ` : ""}
                  {job.name}
                </span>
                <span className="truncate text-muted-foreground">
                  {job.repository}
                </span>
              </div>
              <div className="grid justify-items-end gap-0.5">
                <JobStatus job={job} />
                <span className="text-muted-foreground">
                  <RelativeTime value={job.queuedAt} />
                </span>
              </div>
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}

function RecentEvents() {
  const { org } = useOrg();
  const events = $api.useQuery(
    "get",
    "/v1/orgs/{org}/events",
    { params: { path: { org: org.login }, query: { limit: RECENT } } },
    { refetchInterval: POLL_MS }
  );
  const items = events.data?.items ?? [];

  return (
    <section
      aria-labelledby="recent-events"
      className="grid content-start gap-3"
    >
      <div className="flex items-baseline justify-between">
        <h2 className="font-medium text-sm" id="recent-events">
          Recent events
        </h2>
        <Link
          className="text-muted-foreground text-xs hover:text-foreground"
          href={`/${org.login}/events` as Route}
        >
          All events
        </Link>
      </div>
      {events.isPending ? <Skeleton className="h-40" /> : null}
      {events.isError ? (
        <ErrorRow error={events.error} fallback="Could not load events" />
      ) : null}
      {events.isSuccess && items.length === 0 ? (
        <p className="border border-dashed p-6 text-center text-muted-foreground text-xs">
          Nothing has happened yet.
        </p>
      ) : null}
      {items.length > 0 ? (
        <ul className="divide-y border">
          {items.map((event) => (
            <EventRow event={event} key={event.id} />
          ))}
        </ul>
      ) : null}
    </section>
  );
}

function Usage() {
  const config = `runners:
  - name: build
    cloud: aws
    instance_type: c7i.xlarge
    machine_image: ubuntu2404
    preemptible: true
    labels:
      - cirun-build`;
  const workflow = `jobs:
  build:
    runs-on: cirun-build--\${{ github.run_id }}
    steps:
      - uses: actions/checkout@v5`;
  return (
    <section aria-labelledby="usage" className="grid gap-3">
      <h2 className="font-medium text-sm" id="usage">
        Define a runner and use it
      </h2>
      <p className="max-w-prose text-muted-foreground text-xs/relaxed">
        Commit a <code className="font-mono">.cirun.yml</code> at the root of an
        activated repository, then set{" "}
        <code className="font-mono">runs-on</code> to one of its labels. The{" "}
        <code className="font-mono">--run_id</code> suffix keeps each run on its
        own machine.
      </p>
      <div className="grid gap-3 lg:grid-cols-2">
        <CodeBlock label="Copy .cirun.yml" value={config} />
        <CodeBlock label="Copy workflow snippet" value={workflow} />
      </div>
    </section>
  );
}
