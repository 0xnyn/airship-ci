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
import { DEFAULT_PAGE_SIZE, type PageSize } from "@/lib/pagination";
import { CloudLogo } from "./cloud-logo";
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
import { RunnerSheet } from "./details";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";
import { RunnerState } from "./status";
import { RelativeTime } from "./time";

const POLL_MS = 5000;

type Filter = "active" | "all";

const filters = [
  { label: "Active", value: "active" },
  { label: "All", value: "all" },
] as const satisfies readonly { label: string; value: Filter }[];

export function Runners() {
  const { org } = useOrg();
  const [filter, setFilter] = useState<Filter>("active");
  const [pageSize, setPageSize] = useState<PageSize>(DEFAULT_PAGE_SIZE);
  const [page, setPage] = useState(0);
  const detail = useDetailParam();
  const runners = $api.useInfiniteQuery(
    "get",
    "/v1/orgs/{org}/runners",
    {
      params: {
        path: { org: org.login },
        query: { active: filter === "active", limit: pageSize },
      },
    },
    {
      getNextPageParam: (last) => last.nextCursor ?? undefined,
      initialPageParam: "",
      pageParamName: "cursor",
      refetchInterval: POLL_MS,
    }
  );
  const { pagination, rows } = useCursorPages(
    runners,
    pageSize,
    setPageSize,
    page,
    setPage
  );
  const all = runners.data?.pages.flatMap((each) => each.items ?? []) ?? [];
  const open = all.find((runner) => runner.id === detail.value) ?? null;

  return (
    <>
      <PageHeader
        description="Machines Airship launched in your cloud"
        title="Runners"
      />
      <FilterTabs
        label="Runner filter"
        onChange={(next) => {
          setFilter(next);
          setPage(0);
        }}
        options={filters}
        value={filter}
      />
      {runners.isPending ? <TableSkeleton /> : null}
      {runners.isError ? (
        <ErrorRow error={runners.error} fallback="Could not load runners" />
      ) : null}
      {runners.isSuccess && rows.length === 0 ? (
        <EmptyRow>
          {filter === "active"
            ? "No active runners. One launches as soon as a job is queued for an airship- label."
            : "No runners yet"}
        </EmptyRow>
      ) : null}
      {rows.length > 0 ? (
        <Table footer={<Pagination {...pagination} />}>
          <TableHeader>
            <TableRow>
              <TableHead className="hidden sm:table-cell">State</TableHead>
              <TableHead>Runner</TableHead>
              <TableHead className="hidden lg:table-cell">Definition</TableHead>
              <TableHead className="hidden md:table-cell">Machine</TableHead>
              <TableHead className="hidden text-right lg:table-cell">
                Lifetime
              </TableHead>
              <TableHead className="text-right">Created</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((runner) => (
              <RunnerRow
                key={runner.id}
                onOpen={() => detail.open(runner.id)}
                runner={runner}
                selected={open?.id === runner.id}
              />
            ))}
          </TableBody>
        </Table>
      ) : null}
      <RunnerSheet onClose={detail.close} runner={open} />
    </>
  );
}

function RunnerRow({
  onOpen,
  runner,
  selected,
}: {
  onOpen: () => void;
  runner: Schemas["Runner"];
  selected: boolean;
}) {
  return (
    <ClickableRow
      label={`${runner.name} details`}
      onOpen={onOpen}
      selected={selected}
    >
      <TableCell className="hidden max-w-64 sm:table-cell">
        <div className="grid gap-0.5">
          <RunnerState runner={runner} />
          {runner.error ? (
            <span className="truncate text-destructive" title={runner.error}>
              {runner.error}
            </span>
          ) : null}
        </div>
      </TableCell>
      <TableCell className="w-full max-w-0 sm:w-auto sm:max-w-72">
        <div className="grid gap-0.5">
          <span className="truncate font-medium font-mono text-xs">
            {runner.name}
          </span>
          <span className="sm:hidden">
            <RunnerState runner={runner} />
          </span>
          <span className="truncate text-muted-foreground">
            {runner.repository}
            {runner.jobId ? ` · job ${runner.jobId}` : ""}
          </span>
        </div>
      </TableCell>
      <TableCell className="hidden lg:table-cell">
        <div className="grid gap-0.5">
          <span className="font-mono text-xs">{runner.definition}</span>
          <span className="text-muted-foreground">
            {sourceLabel[runner.source] ?? runner.source}
            {runner.warm ? " · idle pool" : ""}
          </span>
        </div>
      </TableCell>
      <TableCell className="hidden md:table-cell">
        <div className="grid gap-0.5">
          <span className="font-mono text-xs">
            {runner.instanceType || "—"}
            {runner.spot ? " spot" : ""}
          </span>
          <span className="flex min-w-0 items-center gap-1.5 font-mono text-[11px] text-muted-foreground">
            <CloudLogo className="size-3.5" name={runner.cloud} />
            <span className="sr-only">{runner.cloud}</span>
            <span className="truncate">
              {runner.region}
              {runner.instanceId ? ` · ${runner.instanceId}` : ""}
            </span>
          </span>
        </div>
      </TableCell>
      <TableCell className="hidden text-right tabular-nums lg:table-cell">
        {runner.runningAt
          ? formatDuration(secondsBetween(runner.runningAt, runner.finishedAt))
          : "—"}
      </TableCell>
      <TableCell className="text-right text-muted-foreground tabular-nums">
        <RelativeTime value={runner.createdAt} />
      </TableCell>
    </ClickableRow>
  );
}
