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

import { EmptyRow, FilterTabs, LoadMore, TableSkeleton } from "./data-table";
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
  const path = { org: org.login };
  const runners = $api.useInfiniteQuery(
    "get",
    "/v1/orgs/{org}/runners",
    {
      params: {
        path,
        query: { active: filter === "active", limit: PAGE_SIZE },
      },
    },
    {
      getNextPageParam: (page) => page.nextCursor ?? undefined,
      initialPageParam: "",
      refetchInterval: POLL_MS,
    }
  );
  const dataPlanes = $api.useQuery("get", "/v1/orgs/{org}/data-planes", {
    params: { path },
  });
  const planeNames = new Map(
    (dataPlanes.data?.items ?? []).map((plane) => [plane.id, plane.name])
  );
  const items = runners.data?.pages.flatMap((page) => page.items ?? []) ?? [];

  return (
    <>
      <PageHeader
        description="Each runner is a fresh machine registered for one job and destroyed after it. Airship starts one per queued job, up to each profile's and data plane's limit, plus any warm pools your config files keep ready."
        title="Runners"
      />
      <FilterTabs
        label="Runner filter"
        onChange={setFilter}
        options={filters}
        value={filter}
      />
      {runners.isPending ? <TableSkeleton /> : null}
      {runners.isSuccess && items.length === 0 ? (
        <EmptyRow>
          {filter === "active"
            ? "No runners are active. Airship starts one when a job asks for a profile's label."
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
              <TableHead>Data plane</TableHead>
              <TableHead className="text-right">Lifetime</TableHead>
              <TableHead className="text-right">Created</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((runner) => (
              <RunnerRow
                dataPlane={planeNames.get(runner.dataPlaneId)}
                key={runner.id}
                runner={runner}
              />
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

const sourceLabel: Record<Schemas["Runner"]["source"], string> = {
  organization: ".airship.global.yml",
  profile: "Profile",
  repository: ".airship.yml",
};

function RunnerRow({
  dataPlane,
  runner,
}: {
  dataPlane: string | undefined;
  runner: Schemas["Runner"];
}) {
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
          </span>
        </div>
      </TableCell>
      <TableCell>
        <div className="grid gap-0.5">
          <span className="font-mono text-[11px]">{runner.profileLabel}</span>
          <span className="text-muted-foreground">
            {sourceLabel[runner.source]}
            {runner.instanceType ? ` · ${runner.instanceType}` : ""}
            {runner.warm ? " · warm pool" : ""}
          </span>
        </div>
      </TableCell>
      <TableCell>
        <div className="grid gap-0.5">
          <span>{dataPlane ?? "—"}</span>
          {runner.instanceId ? (
            <span className="font-mono text-[11px] text-muted-foreground">
              {runner.instanceId}
            </span>
          ) : null}
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
