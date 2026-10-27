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
import { useState } from "react";

import { $api } from "@/lib/api";
import { formatDuration, secondsBetween } from "@/lib/format";
import { sourceLabel } from "@/lib/labels";

import {
  EmptyRow,
  ErrorRow,
  FilterTabs,
  LoadMore,
  TableSkeleton,
} from "./data-table";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";
import { RunnerState } from "./status";
import { RelativeTime } from "./time";

const POLL_MS = 5000;
const PAGE_SIZE = 50;

type Filter = "active" | "all";

const filters = [
  { label: "Active", value: "active" },
  { label: "All", value: "all" },
] as const satisfies readonly { label: string; value: Filter }[];

export function Runners() {
  const { org } = useOrg();
  const [filter, setFilter] = useState<Filter>("active");
  const runners = $api.useInfiniteQuery(
    "get",
    "/v1/orgs/{org}/runners",
    {
      params: {
        path: { org: org.login },
        query: { active: filter === "active", limit: PAGE_SIZE },
      },
    },
    {
      getNextPageParam: (page) => page.nextCursor ?? undefined,
      initialPageParam: "",
      refetchInterval: POLL_MS,
    }
  );
  const items = runners.data?.pages.flatMap((page) => page.items ?? []) ?? [];

  return (
    <>
      <PageHeader
        description="Each runner is a fresh machine in your cloud account, registered for one job and destroyed after it. Airship starts one per queued job, up to the organization's limit, plus any idle pools your config files keep ready."
        title="Runners"
      />
      <FilterTabs
        label="Runner filter"
        onChange={setFilter}
        options={filters}
        value={filter}
      />
      {runners.isPending ? <TableSkeleton /> : null}
      {runners.isError ? (
        <ErrorRow error={runners.error} fallback="Could not load runners" />
      ) : null}
      {runners.isSuccess && items.length === 0 ? (
        <EmptyRow>
          {filter === "active"
            ? "No runners are active. One starts when a job asks for a cirun- label in an activated repository."
            : "No runners yet."}
        </EmptyRow>
      ) : null}
      {items.length > 0 ? (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>State</TableHead>
              <TableHead>Runner</TableHead>
              <TableHead>Definition</TableHead>
              <TableHead>Machine</TableHead>
              <TableHead className="text-right">Lifetime</TableHead>
              <TableHead className="text-right">Created</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((runner) => (
              <RunnerRow key={runner.id} runner={runner} />
            ))}
          </TableBody>
        </Table>
      ) : null}
      <LoadMore
        fetchNextPage={runners.fetchNextPage}
        hasNextPage={runners.hasNextPage}
        isFetchingNextPage={runners.isFetchingNextPage}
      />
    </>
  );
}

function RunnerRow({ runner }: { runner: Schemas["Runner"] }) {
  return (
    <TableRow>
      <TableCell className="max-w-64">
        <div className="grid gap-0.5">
          <RunnerState runner={runner} />
          {runner.error ? (
            <span className="truncate text-destructive" title={runner.error}>
              {runner.error}
            </span>
          ) : null}
        </div>
      </TableCell>
      <TableCell className="max-w-72">
        <div className="grid gap-0.5">
          <span className="truncate font-mono text-[11px]">{runner.name}</span>
          <span className="truncate text-muted-foreground">
            {runner.repository}
            {runner.jobId ? ` · job ${runner.jobId}` : ""}
          </span>
        </div>
      </TableCell>
      <TableCell>
        <div className="grid gap-0.5">
          <span className="font-mono text-[11px]">{runner.definition}</span>
          <span className="text-muted-foreground">
            {sourceLabel[runner.source] ?? runner.source}
            {runner.warm ? " · idle pool" : ""}
          </span>
        </div>
      </TableCell>
      <TableCell>
        <div className="grid gap-0.5">
          <span className="font-mono text-[11px]">
            {runner.instanceType || "—"}
            {runner.spot ? " spot" : ""}
          </span>
          <span className="truncate font-mono text-[11px] text-muted-foreground">
            {runner.cloud} {runner.region}
            {runner.instanceId ? ` · ${runner.instanceId}` : ""}
          </span>
        </div>
      </TableCell>
      <TableCell className="text-right font-mono tabular-nums">
        {runner.runningAt
          ? formatDuration(secondsBetween(runner.runningAt, runner.finishedAt))
          : "—"}
      </TableCell>
      <TableCell className="text-right text-muted-foreground">
        <RelativeTime value={runner.createdAt} />
      </TableCell>
    </TableRow>
  );
}
