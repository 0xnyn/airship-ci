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
import { DEFAULT_PAGE_SIZE, type PageSize } from "@/lib/pagination";

import {
  ClickableRow,
  EmptyRow,
  ErrorRow,
  FilterTabs,
  Pagination,
  TableSkeleton,
  useCursorPages,
  useDetailParam,
} from "./data-table";
import { JobSheet } from "./details";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";
import { JobStatus } from "./status";
import { RelativeTime } from "./time";

const POLL_MS = 10_000;

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
  const [pageSize, setPageSize] = useState<PageSize>(DEFAULT_PAGE_SIZE);
  const [page, setPage] = useState(0);
  const detail = useDetailParam();
  const query = {
    limit: pageSize,
    ...(status === "all" ? {} : { status }),
  };
  const jobs = $api.useInfiniteQuery(
    "get",
    "/v1/orgs/{org}/jobs",
    { params: { path: { org: org.login }, query } },
    {
      getNextPageParam: (last) => last.nextCursor ?? undefined,
      initialPageParam: "",
      pageParamName: "cursor",
      refetchInterval: POLL_MS,
    }
  );
  const { pagination, rows } = useCursorPages(
    jobs,
    pageSize,
    setPageSize,
    page,
    setPage
  );
  const all = jobs.data?.pages.flatMap((each) => each.items ?? []) ?? [];
  const open = all.find((job) => String(job.id) === detail.value) ?? null;

  return (
    <>
      <PageHeader
        description="Every workflow job GitHub reported"
        title="Jobs"
      />
      <FilterTabs
        label="Job status"
        onChange={(next) => {
          setStatus(next);
          setPage(0);
        }}
        options={filters}
        value={status}
      />
      {jobs.isPending ? <TableSkeleton /> : null}
      {jobs.isError ? (
        <ErrorRow error={jobs.error} fallback="Could not load jobs" />
      ) : null}
      {jobs.isSuccess && rows.length === 0 ? (
        <EmptyRow>
          No jobs yet. They appear as soon as GitHub queues one in a repository
          the app can see.
        </EmptyRow>
      ) : null}
      {rows.length > 0 ? (
        <Table footer={<Pagination {...pagination} />}>
          <TableHeader>
            <TableRow>
              <TableHead>Status</TableHead>
              <TableHead>Job</TableHead>
              <TableHead className="hidden md:table-cell">Runs on</TableHead>
              <TableHead className="hidden text-right lg:table-cell">
                Queue
              </TableHead>
              <TableHead className="hidden text-right lg:table-cell">
                Duration
              </TableHead>
              <TableHead className="text-right">Queued</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((job) => (
              <JobRow
                job={job}
                key={job.id}
                onOpen={() => detail.open(String(job.id))}
                selected={open?.id === job.id}
              />
            ))}
          </TableBody>
        </Table>
      ) : null}
      <JobSheet job={open} onClose={detail.close} />
    </>
  );
}

function JobRow({
  job,
  onOpen,
  selected,
}: {
  job: Schemas["Job"];
  onOpen: () => void;
  selected: boolean;
}) {
  const wait = job.startedAt
    ? formatDuration(secondsBetween(job.queuedAt, job.startedAt))
    : "—";
  const duration = job.startedAt
    ? formatDuration(secondsBetween(job.startedAt, job.completedAt))
    : "—";

  return (
    <ClickableRow
      label={`${job.name} details`}
      onOpen={onOpen}
      selected={selected}
    >
      <TableCell>
        <JobStatus job={job} />
      </TableCell>
      <TableCell className="max-w-80">
        <div className="grid gap-0.5">
          <span className="flex items-center gap-1 truncate font-medium text-[13px]">
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
      <TableCell className="hidden max-w-64 md:table-cell">
        <div className="grid gap-0.5">
          <span className="truncate font-mono text-xs">
            {(job.labels ?? []).join(", ") || "—"}
          </span>
          <RunsOnDetail job={job} />
        </div>
      </TableCell>
      <TableCell className="hidden text-right tabular-nums lg:table-cell">
        {wait}
      </TableCell>
      <TableCell className="hidden text-right tabular-nums lg:table-cell">
        {duration}
      </TableCell>
      <TableCell className="text-right text-muted-foreground tabular-nums">
        <RelativeTime value={job.queuedAt} />
      </TableCell>
    </ClickableRow>
  );
}

export function RunsOnDetail({ job }: { job: Schemas["Job"] }) {
  if (job.skipReason) {
    return (
      <span
        className="text-warning-foreground"
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
