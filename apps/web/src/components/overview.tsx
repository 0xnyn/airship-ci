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
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";

const POLL_MS = 10_000;
const PERCENT = 100;

export function Overview() {
  const { org } = useOrg();
  const path = { params: { path: { org: org.login } } };
  const overview = $api.useQuery("get", "/v1/orgs/{org}/overview", path, {
    refetchInterval: POLL_MS,
  });
  const dataPlanes = $api.useQuery("get", "/v1/orgs/{org}/data-planes", path);
  const profiles = $api.useQuery("get", "/v1/orgs/{org}/profiles", path);

  const planes = dataPlanes.data?.items ?? [];
  const profileItems = profiles.data?.items ?? [];
  const ready =
    planes.some((plane) => plane.status === "online") &&
    profileItems.length > 0;

  return (
    <>
      <PageHeader
        description="Jobs and runners across every repository in the last 24 hours."
        title="Overview"
      />
      {dataPlanes.isSuccess && profiles.isSuccess && !ready ? (
        <GettingStarted planes={planes} profiles={profileItems} />
      ) : null}
      {overview.data ? <Stats overview={overview.data} /> : <StatsSkeleton />}
      {ready && profileItems[0] ? (
        <Usage label={profileItems[0].label} />
      ) : null}
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
          hint="Time jobs spent on Airship runners"
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
            hint="Waiting for data plane capacity"
            label="Pending"
            value={formatInteger(runners.pending)}
          />
          <Stat
            hint="Launched, not yet online"
            label="Starting"
            value={formatInteger(runners.starting)}
          />
          <Stat
            hint="Online, waiting for a job"
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
  planes,
  profiles,
}: {
  planes: Schemas["DataPlane"][];
  profiles: Schemas["Profile"][];
}) {
  const { org } = useOrg();
  const steps = [
    {
      description:
        "A data plane is the agent that runs in your cloud account and launches runners there.",
      done: planes.length > 0,
      href: `/${org.login}/data-planes`,
      title: "Create a data plane",
    },
    {
      description:
        "Run the agent with the token you were given. It only makes outbound connections.",
      done: planes.some((plane) => plane.status === "online"),
      href: `/${org.login}/data-planes`,
      title: "Start the agent",
    },
    {
      description:
        "A profile is a runs-on label and the machine Airship starts for it.",
      done: profiles.length > 0,
      href: `/${org.login}/profiles`,
      title: "Create a runner profile",
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

function Usage({ label }: { label: string }) {
  const snippet = `jobs:\n  build:\n    runs-on: ${label}\n    steps:\n      - uses: actions/checkout@v5`;
  return (
    <section aria-labelledby="usage" className="grid gap-3">
      <h2 className="font-medium text-sm" id="usage">
        Use it in a workflow
      </h2>
      <p className="text-muted-foreground text-xs/relaxed">
        Set <code className="font-mono">runs-on</code> to a profile label in any
        private repository the GitHub App can see.
      </p>
      <CodeBlock label="Copy workflow snippet" value={snippet} />
    </section>
  );
}
