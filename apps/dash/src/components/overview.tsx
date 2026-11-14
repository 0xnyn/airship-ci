"use client";

import type { Schemas } from "@airship/client";
import { buttonVariants } from "@airship/ui/components/button";
import { Skeleton } from "@airship/ui/components/skeleton";
import { cn } from "@airship/ui/lib/utils";
import { Check } from "lucide-react";
import type { Route } from "next";
import Link from "next/link";
import { useState } from "react";

import { $api } from "@/lib/api";
import { formatDuration, formatInteger } from "@/lib/format";
import { CodeBlock } from "./copy-button";
import { EmptyRow, ErrorRow } from "./data-table";
import { EventSheet, JobSheet } from "./details";
import { DocsLink } from "./docs-link";
import { EventRow } from "./event-row";
import { useOrg } from "./org-context";
import { PageHeader, SectionHeading } from "./page-header";
import { MoreLink, Panel, PanelList, Stat, StatGrid } from "./panel";
import { JobStatus } from "./status";
import { RelativeTime } from "./time";

const POLL_MS = 10_000;
const PERCENT = 100;
const RECENT = 6;
const MINUTES_PER_HOUR = 60;

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
        description="Fleet and job activity, last 24 hours"
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
      <div className="grid gap-x-6 gap-y-8 lg:grid-cols-2">
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

  return (
    <div className="grid gap-8">
      <RunnersNow org={org.login} runners={overview.runners} />
      <section aria-labelledby="last-day" className="grid gap-3">
        <SectionHeading id="last-day">Last 24 hours</SectionHeading>
        <StatGrid label="Jobs">
          <Stat
            hint={`${formatInteger(overview.jobs)} across all runners`}
            label="Jobs on Airship"
            value={formatInteger(overview.airshipJobs)}
          />
          <Stat
            dot={overview.failed > 0 ? "bg-danger" : "bg-success"}
            hint={`${formatInteger(overview.failed)} failed, ${formatInteger(overview.queued)} waiting`}
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
            hint={`About ${formatInteger(Math.round(overview.runnerMinutes / MINUTES_PER_HOUR))} machine hours`}
            label="Runner minutes"
            value={formatInteger(Math.round(overview.runnerMinutes))}
          />
        </StatGrid>
      </section>
    </div>
  );
}

const runnerStates = [
  {
    dot: "bg-faint",
    hint: "No free slot yet",
    key: "pending",
    label: "Waiting",
  },
  {
    dot: "bg-warning",
    hint: "Registering and booting",
    key: "starting",
    label: "Launching",
  },
  {
    dot: "bg-success",
    hint: "Booted, no job yet",
    key: "idle",
    label: "Idle",
  },
  { dot: "bg-active", hint: "Running a job", key: "busy", label: "Busy" },
] as const;

/**
 * The fleet as it stands, drawn like the landing page's runner panel: a
 * header with the live dot, one cell per state, and a bar splitting the
 * fleet by state so the mix reads at a glance.
 */
function RunnersNow({
  org,
  runners,
}: {
  org: string;
  runners: Schemas["Overview"]["runners"];
}) {
  const total = runnerStates.reduce(
    (sum, state) => sum + runners[state.key],
    0
  );

  return (
    <section aria-labelledby="runners-now">
      <Panel className="relative">
        <div className="flex items-center justify-between gap-4 border-b px-4 py-3.5 md:px-5">
          <h2
            className="flex items-center gap-2 font-semibold text-base tracking-tight"
            id="runners-now"
          >
            Runners now
            <span className="figure rounded-full bg-muted px-1.5 font-normal text-muted-foreground text-xs">
              {formatInteger(total)}
            </span>
          </h2>
          <MoreLink href={`/${org}/runners`}>View runners</MoreLink>
        </div>
        <div className="grid grid-cols-2 lg:grid-cols-4">
          {runnerStates.map((state, index) => (
            <div
              className={cn(
                "border-border",
                index % 2 === 1 && "border-l",
                index >= 2 && "max-lg:border-t",
                index === 2 && "lg:border-l"
              )}
              key={state.key}
            >
              <Stat
                dot={state.dot}
                hint={state.hint}
                label={state.label}
                value={formatInteger(runners[state.key])}
              />
            </div>
          ))}
        </div>
        <div
          aria-hidden="true"
          className="mx-4 mb-4 flex h-1.5 gap-0.5 overflow-hidden rounded-full bg-muted md:mx-5 md:mb-5"
        >
          {total > 0
            ? runnerStates.map((state) =>
                runners[state.key] > 0 ? (
                  <span
                    className={cn(
                      "h-full transition-[flex-grow] duration-500 ease-smooth",
                      state.dot
                    )}
                    key={state.key}
                    style={{ flexGrow: runners[state.key] }}
                  />
                ) : null
              )
            : null}
        </div>
      </Panel>
    </section>
  );
}

function StatsSkeleton() {
  return (
    <div className="grid gap-8">
      <Skeleton className="h-36 rounded-lg" />
      <Skeleton className="h-28 rounded-lg" />
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
  const setup = (step: string) => `/${org.login}/setup?step=${step}`;
  const steps = [
    {
      action: "Repositories",
      description: "Grant it access on GitHub",
      done: true,
      href: setup("github"),
      title: "Install the GitHub App",
    },
    {
      action: "Connect",
      description: "Runners launch in the account you connect",
      done: clouds.length > 0,
      href: setup("cloud"),
      title: "Connect a cloud",
    },
    {
      action: "Activate",
      description:
        "Airship only reads airship.yaml in repositories you activate",
      done: repositories.length > 0,
      href: setup("activate"),
      title: "Activate a repository",
    },
    {
      action: "Move workflows",
      description: "Airship writes the airship.yaml and runs-on changes",
      done: false,
      href: setup("workflows"),
      title: "Move your workflows",
    },
  ];

  return (
    <Panel className="relative">
      <section aria-labelledby="getting-started" className="grid gap-4 p-5">
        <div className="grid gap-1">
          <h2
            className="font-semibold text-base tracking-tight"
            id="getting-started"
          >
            Get started
          </h2>
          <p className="text-muted-foreground text-xs">
            {steps.filter((step) => step.done).length} of {steps.length} done
          </p>
        </div>
        <Link
          className={cn(
            buttonVariants({ size: "sm" }),
            "absolute top-4 right-5"
          )}
          href={`/${org.login}/setup` as Route}
        >
          Open setup
        </Link>
        <ol className="grid gap-px overflow-hidden rounded-md border bg-border">
          {steps.map((step, index) => (
            <li className="flex items-start gap-3 bg-card p-3" key={step.title}>
              <span
                aria-hidden="true"
                className={cn(
                  "figure grid size-5 shrink-0 place-items-center rounded-full border text-[11px] text-muted-foreground",
                  step.done && "border-transparent bg-success text-background"
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
                  {step.action}
                </Link>
              )}
            </li>
          ))}
        </ol>
      </section>
    </Panel>
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
  const [openId, setOpenId] = useState<number | null>(null);
  const open = items.find((job) => job.id === openId) ?? null;

  return (
    <section aria-labelledby="recent-jobs" className="grid content-start gap-3">
      <SectionHeading
        action={<MoreLink href={`/${org.login}/jobs`}>All jobs</MoreLink>}
        id="recent-jobs"
      >
        Recent jobs
      </SectionHeading>
      {jobs.isPending ? <Skeleton className="h-72 rounded-lg" /> : null}
      {jobs.isError ? (
        <ErrorRow error={jobs.error} fallback="Could not load jobs" />
      ) : null}
      {jobs.isSuccess && items.length === 0 ? (
        <EmptyRow>No jobs yet. Push to an activated repository.</EmptyRow>
      ) : null}
      {items.length > 0 ? (
        <PanelList>
          {items.map((job) => (
            <li key={job.id}>
              <button
                className="grid w-full grid-cols-[1fr_auto] items-center gap-3 px-4 py-3 text-left text-xs outline-none transition-colors hover:bg-muted focus-visible:bg-muted focus-visible:ring-1 focus-visible:ring-ring focus-visible:ring-inset"
                onClick={() => setOpenId(job.id)}
                type="button"
              >
                <div className="grid min-w-0 gap-1">
                  <span className="truncate font-medium text-sm">
                    {job.workflowName ? (
                      <span className="text-muted-foreground">
                        {job.workflowName} /{" "}
                      </span>
                    ) : null}
                    {job.name}
                  </span>
                  <span className="truncate font-mono text-[11px] text-muted-foreground">
                    {job.repository}
                  </span>
                </div>
                <div className="grid justify-items-end gap-1">
                  <JobStatus job={job} />
                  <span className="text-muted-foreground text-xs tabular-nums">
                    <RelativeTime value={job.queuedAt} />
                  </span>
                </div>
              </button>
            </li>
          ))}
        </PanelList>
      ) : null}
      <JobSheet job={open} onClose={() => setOpenId(null)} />
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
  const [openId, setOpenId] = useState<string | null>(null);
  const open = items.find((event) => event.id === openId) ?? null;

  return (
    <section
      aria-labelledby="recent-events"
      className="grid content-start gap-3"
    >
      <SectionHeading
        action={<MoreLink href={`/${org.login}/events`}>All events</MoreLink>}
        id="recent-events"
      >
        Recent events
      </SectionHeading>
      {events.isPending ? <Skeleton className="h-72 rounded-lg" /> : null}
      {events.isError ? (
        <ErrorRow error={events.error} fallback="Could not load events" />
      ) : null}
      {events.isSuccess && items.length === 0 ? (
        <EmptyRow>No events yet</EmptyRow>
      ) : null}
      {items.length > 0 ? (
        <PanelList>
          {items.map((event) => (
            <EventRow
              compact
              event={event}
              key={event.id}
              onOpen={() => setOpenId(event.id)}
            />
          ))}
        </PanelList>
      ) : null}
      <EventSheet event={open} onClose={() => setOpenId(null)} />
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
      - airship-build`;
  const workflow = `jobs:
  build:
    runs-on: airship-build--\${{ github.run_id }}
    steps:
      - uses: actions/checkout@v5`;
  return (
    <section aria-labelledby="usage" className="grid gap-3">
      <SectionHeading id="usage">Define a runner</SectionHeading>
      <p className="max-w-prose text-muted-foreground text-xs/relaxed">
        Commit this at the root of an activated repository, then point{" "}
        <code className="font-mono">runs-on</code> at one of its labels. The{" "}
        <code className="font-mono">--run_id</code> suffix gives each run its
        own machine. <DocsLink page="airship.yaml" />
      </p>
      <div className="grid gap-3 lg:grid-cols-2">
        <CodeBlock
          file="airship.yaml"
          label="Copy airship.yaml"
          value={config}
        />
        <CodeBlock
          file=".github/workflows/ci.yml"
          label="Copy workflow snippet"
          value={workflow}
        />
      </div>
    </section>
  );
}
