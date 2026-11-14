"use client";

import type { Schemas } from "@airship/client";
import { buttonVariants } from "@airship/ui/components/button";
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@airship/ui/components/sheet";
import { Skeleton } from "@airship/ui/components/skeleton";
import { ArrowUpRight } from "lucide-react";
import type { ReactNode } from "react";

import { $api } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";
import { formatDuration, secondsBetween } from "@/lib/format";
import { skipReasonLabel, sourceLabel } from "@/lib/labels";

import { CloudLogo } from "./cloud-logo";
import { EventRow } from "./event-row";
import { useOrg } from "./org-context";
import { DetailFact, DetailFacts, DetailSection } from "./panel";
import { JobStatus, RunnerState } from "./status";
import { RelativeTime } from "./time";

const TIMELINE_LIMIT = 25;
const TIMELINE_POLL_MS = 5000;

/** A row's detail sheet, open while `item` is set. */
function DetailSheet<T>({
  children,
  item,
  onClose,
}: {
  children: (item: T) => ReactNode;
  item: T | null;
  onClose: () => void;
}) {
  return (
    <Sheet
      onOpenChange={(next) => (next ? null : onClose())}
      open={item !== null}
    >
      <SheetContent>{item === null ? null : children(item)}</SheetContent>
    </Sheet>
  );
}

/** Events about one runner or one job, newest first. */
function Timeline({
  filter,
  exclude,
}: {
  exclude?: string;
  filter: { job: number } | { runner: string };
}) {
  const { org } = useOrg();
  const events = $api.useQuery(
    "get",
    "/v1/orgs/{org}/events",
    {
      params: {
        path: { org: org.login },
        query: { ...filter, limit: TIMELINE_LIMIT },
      },
    },
    { refetchInterval: TIMELINE_POLL_MS }
  );
  const items = (events.data?.items ?? []).filter(
    (event) => event.id !== exclude
  );

  if (events.isPending) {
    return <Skeleton className="h-24" />;
  }
  if (events.isError) {
    return (
      <p className="text-danger-foreground text-sm">
        {errorMessage(events.error, "Could not load events")}
      </p>
    );
  }
  if (items.length === 0) {
    return <p className="text-muted-foreground text-sm">No events</p>;
  }
  return (
    <ul className="divide-y overflow-hidden rounded-lg border">
      {items.map((event) => (
        <EventRow compact event={event} key={event.id} />
      ))}
    </ul>
  );
}

export function JobSheet({
  job,
  onClose,
}: {
  job: Schemas["Job"] | null;
  onClose: () => void;
}) {
  return (
    <DetailSheet item={job} onClose={onClose}>
      {(item) => <JobDetail job={item} />}
    </DetailSheet>
  );
}

function JobDetail({ job }: { job: Schemas["Job"] }) {
  const wait = job.startedAt
    ? formatDuration(secondsBetween(job.queuedAt, job.startedAt))
    : "—";
  const duration = job.startedAt
    ? formatDuration(secondsBetween(job.startedAt, job.completedAt))
    : "—";
  return (
    <>
      <SheetHeader
        actions={
          job.htmlUrl ? (
            <a
              aria-label="Open on GitHub"
              className={buttonVariants({ size: "icon-sm", variant: "ghost" })}
              href={job.htmlUrl}
              rel="noopener noreferrer"
              target="_blank"
              title="Open on GitHub"
            >
              <ArrowUpRight />
            </a>
          ) : null
        }
      >
        <SheetTitle>{job.name}</SheetTitle>
        <SheetDescription className="flex items-center gap-2">
          <JobStatus job={job} />
        </SheetDescription>
      </SheetHeader>
      <SheetBody className="grid content-start gap-5">
        <DetailFacts>
          <DetailFact label="Repository">{job.repository}</DetailFact>
          <DetailFact label="Branch" mono>
            {job.headBranch ?? "—"}
          </DetailFact>
          <DetailFact label="Workflow">{job.workflowName ?? "—"}</DetailFact>
          <DetailFact label="Runs on" mono>
            {(job.labels ?? []).join(", ") || "—"}
          </DetailFact>
          <DetailFact label="Runner" mono>
            {job.runnerName ?? "—"}
          </DetailFact>
          <DetailFact label="On Airship">
            {job.onAirship ? "Yes" : "No"}
          </DetailFact>
          <DetailFact label="Queued">
            <RelativeTime value={job.queuedAt} />
          </DetailFact>
          <DetailFact label="Waited">{wait}</DetailFact>
          <DetailFact label="Ran for">{duration}</DetailFact>
          <DetailFact label="Conclusion">{job.conclusion ?? "—"}</DetailFact>
        </DetailFacts>
        {job.skipReason ? (
          <DetailSection title="Why Airship did not run it">
            <div className="grid gap-1 rounded-lg border border-warning/30 bg-warning-surface px-3 py-2.5 text-sm text-warning-foreground">
              <p className="font-medium">
                {skipReasonLabel[job.skipReason] ?? job.skipReason}
              </p>
              {job.skipDetail ? <p>{job.skipDetail}</p> : null}
            </div>
          </DetailSection>
        ) : null}
        <DetailSection title="Events">
          <Timeline filter={{ job: job.id }} />
        </DetailSection>
      </SheetBody>
    </>
  );
}

export function RunnerSheet({
  onClose,
  runner,
}: {
  onClose: () => void;
  runner: Schemas["Runner"] | null;
}) {
  return (
    <DetailSheet item={runner} onClose={onClose}>
      {(item) => <RunnerDetail runner={item} />}
    </DetailSheet>
  );
}

function RunnerDetail({ runner }: { runner: Schemas["Runner"] }) {
  return (
    <>
      <SheetHeader>
        <SheetTitle className="font-mono text-sm">{runner.name}</SheetTitle>
        <SheetDescription className="flex items-center gap-2">
          <RunnerState runner={runner} />
        </SheetDescription>
      </SheetHeader>
      <SheetBody className="grid content-start gap-5">
        {runner.error ? (
          <p className="rounded-lg border border-danger/25 bg-danger-surface px-3 py-2.5 text-danger-foreground text-sm">
            {runner.error}
          </p>
        ) : null}
        <DetailFacts>
          <DetailFact label="Repository">{runner.repository}</DetailFact>
          <DetailFact label="Definition" mono>
            {runner.definition}
          </DetailFact>
          <DetailFact label="Defined in">
            {sourceLabel[runner.source] ?? runner.source}
            {runner.warm ? ", idle pool" : ""}
          </DetailFact>
          <DetailFact label="Labels" mono>
            {(runner.labels ?? []).join(", ") || "—"}
          </DetailFact>
          <DetailFact label="Cloud">
            <span className="inline-flex items-center gap-1.5">
              <CloudLogo className="size-3.5" name={runner.cloud} />
              <span className="font-mono text-[13px]">{runner.region}</span>
            </span>
          </DetailFact>
          <DetailFact label="Machine" mono>
            {runner.instanceType || "—"}
            {runner.spot ? " (spot)" : ""}
          </DetailFact>
          <DetailFact label="Instance" mono>
            {runner.instanceId ?? "—"}
          </DetailFact>
          <DetailFact label="Job">{runner.jobId ?? "—"}</DetailFact>
          <DetailFact label="Created">
            <RelativeTime value={runner.createdAt} />
          </DetailFact>
          <DetailFact label="Lifetime">
            {runner.runningAt
              ? formatDuration(
                  secondsBetween(runner.runningAt, runner.finishedAt)
                )
              : "—"}
          </DetailFact>
          {runner.finishedAt ? (
            <DetailFact label="Finished">
              <RelativeTime value={runner.finishedAt} />
            </DetailFact>
          ) : null}
        </DetailFacts>
        <DetailSection title="Timeline">
          <Timeline filter={{ runner: runner.id }} />
        </DetailSection>
      </SheetBody>
    </>
  );
}

export function EventSheet({
  event,
  onClose,
}: {
  event: Schemas["Event"] | null;
  onClose: () => void;
}) {
  return (
    <DetailSheet item={event} onClose={onClose}>
      {(item) => <EventDetail event={item} />}
    </DetailSheet>
  );
}

function EventDetail({ event }: { event: Schemas["Event"] }) {
  let related: ReactNode = null;
  if (event.runnerId) {
    related = (
      <DetailSection title="Same runner">
        <Timeline exclude={event.id} filter={{ runner: event.runnerId }} />
      </DetailSection>
    );
  } else if (event.jobId) {
    related = (
      <DetailSection title="Same job">
        <Timeline exclude={event.id} filter={{ job: event.jobId }} />
      </DetailSection>
    );
  }
  return (
    <>
      <SheetHeader>
        <SheetTitle className="font-mono text-sm">{event.kind}</SheetTitle>
        <SheetDescription>
          <RelativeTime value={event.createdAt} />
        </SheetDescription>
      </SheetHeader>
      <SheetBody className="grid content-start gap-5">
        <p className="text-pretty text-sm leading-relaxed">{event.message}</p>
        <DetailFacts>
          <DetailFact label="Level">
            <span className="capitalize">{event.level}</span>
          </DetailFact>
          <DetailFact label="Repository">{event.repository ?? "—"}</DetailFact>
          <DetailFact label="Runner" mono>
            {event.runnerId ?? "—"}
          </DetailFact>
          <DetailFact label="Job" mono>
            {event.jobId ?? "—"}
          </DetailFact>
        </DetailFacts>
        {related}
      </SheetBody>
    </>
  );
}
