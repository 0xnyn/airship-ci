"use client";

import type { Schemas } from "@airship/client";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@airship/ui/components/table";
import { ExternalLink } from "lucide-react";
import { useState } from "react";

import { $api } from "@/lib/api";
import { formatDuration, secondsBetween } from "@/lib/format";
import { skipReasonLabel } from "@/lib/labels";

import { EmptyRow, FilterTabs, LoadMore, TableSkeleton } from "./data-table";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";
import { JobStatus } from "./status";
import { RelativeTime } from "./time";

const POLL_MS = 10_000;
const PAGE_SIZE = 50;

type StatusFilter = "all" | Schemas["Job"]["status"];

const filters = [
  { label: "All", value: "all" },
  { label: "Queued", value: "queued" },
  { label: "Running", value: "in_progress" },
  { label: "Completed", value: "completed" },
] as const satisfies readonly { label: string; value: StatusFilter }[];

export function Jobs() {
  const { org } = useOrg();
  const [status, setStatus] = useState<StatusFilter>("all");
  const query = {
    limit: PAGE_SIZE,
    ...(status === "all" ? {} : { status }),
  };
  const jobs = $api.useInfiniteQuery(
    "get",
    "/v1/orgs/{org}/jobs",
    { params: { path: { org: org.login }, query } },
    {
      getNextPageParam: (page) => page.nextCursor ?? undefined,
      initialPageParam: "",
      refetchInterval: POLL_MS,
    }
  );
  const items = jobs.data?.pages.flatMap((page) => page.items ?? []) ?? [];

  return (
    <>
      <PageHeader
        description="Every workflow job GitHub reported for repositories the app can see, including ones Airship did not run and why."
        title="Jobs"
      />
      <FilterTabs
        label="Job status"
        onChange={setStatus}
        options={filters}
        value={status}
      />
      {jobs.isPending ? <TableSkeleton /> : null}
      {jobs.isSuccess && items.length === 0 ? (
        <EmptyRow>
          No jobs yet. They appear here as soon as GitHub queues a workflow job
          in a repository the app can see.
        </EmptyRow>
      ) : null}
      {items.length > 0 ? (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Status</TableHead>
              <TableHead>Job</TableHead>
              <TableHead>Runs on</TableHead>
              <TableHead className="text-right">Queue</TableHead>
              <TableHead className="text-right">Duration</TableHead>
              <TableHead className="text-right">Queued</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((job) => (
              <JobRow job={job} key={job.id} />
            ))}
          </TableBody>
        </Table>
      ) : null}
      <LoadMore
        fetchNextPage={jobs.fetchNextPage}
        hasNextPage={jobs.hasNextPage}
        isFetchingNextPage={jobs.isFetchingNextPage}
      />
    </>
  );
}

function JobRow({ job }: { job: Schemas["Job"] }) {
  const wait = job.startedAt
    ? formatDuration(secondsBetween(job.queuedAt, job.startedAt))
    : "—";
  const duration = job.startedAt
    ? formatDuration(secondsBetween(job.startedAt, job.completedAt))
    : "—";

  return (
    <TableRow>
      <TableCell>
        <JobStatus job={job} />
      </TableCell>
      <TableCell className="max-w-80">
        <div className="grid gap-0.5">
          <span className="flex items-center gap-1 truncate font-medium">
            {job.htmlUrl ? (
              <a
                className="inline-flex items-center gap-1 truncate hover:underline"
                href={job.htmlUrl}
                rel="noopener noreferrer"
                target="_blank"
              >
                {job.name}
                <ExternalLink
                  aria-hidden="true"
                  className="size-3 shrink-0 text-muted-foreground"
                />
                <span className="sr-only">(opens GitHub)</span>
              </a>
            ) : (
              job.name
            )}
          </span>
          <span className="truncate text-muted-foreground">
            {job.repository}
            {job.headBranch ? ` · ${job.headBranch}` : ""}
            {job.workflowName ? ` · ${job.workflowName}` : ""}
          </span>
        </div>
      </TableCell>
      <TableCell className="max-w-64">
        <div className="grid gap-0.5">
          <span className="truncate font-mono text-[11px]">
            {(job.labels ?? []).join(", ") || "—"}
          </span>
          <RunsOnDetail job={job} />
        </div>
      </TableCell>
      <TableCell className="text-right font-mono tabular-nums">
        {wait}
      </TableCell>
      <TableCell className="text-right font-mono tabular-nums">
        {duration}
      </TableCell>
      <TableCell className="text-right text-muted-foreground">
        <RelativeTime value={job.queuedAt} />
      </TableCell>
    </TableRow>
  );
}

function RunsOnDetail({ job }: { job: Schemas["Job"] }) {
  if (job.skipReason) {
    return (
      <span
        className="text-amber-700 dark:text-amber-400"
        title={job.skipDetail ?? undefined}
      >
        Not run by Airship: {skipReasonLabel[job.skipReason] ?? job.skipReason}
        {job.skipDetail ? (
          <span className="block truncate text-muted-foreground">
            {job.skipDetail}
          </span>
        ) : null}
      </span>
    );
  }
  if (job.onAirship) {
    return (
      <span className="truncate text-muted-foreground">
        Airship · {job.runnerName}
      </span>
    );
  }
  if (job.runnerName) {
    return (
      <span className="truncate text-muted-foreground">{job.runnerName}</span>
    );
  }
  return null;
}
