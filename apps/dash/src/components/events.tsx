"use client";

import type { Schemas } from "@airship/client";
import { useState } from "react";

import { $api } from "@/lib/api";
import { DEFAULT_PAGE_SIZE, type PageSize } from "@/lib/pagination";

import {
  EmptyRow,
  ErrorRow,
  FilterTabs,
  Pagination,
  TableSkeleton,
  useCursorPages,
  useDetailParam,
} from "./data-table";
import { EventSheet } from "./details";
import { EventRow } from "./event-row";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";

const POLL_MS = 10_000;

type LevelFilter = "all" | Schemas["Event"]["level"];

const filters = [
  { label: "All", value: "all" },
  { label: "Warnings", value: "warning" },
  { label: "Errors", value: "error" },
] as const satisfies readonly { label: string; value: LevelFilter }[];

export function Events() {
  const { org } = useOrg();
  const [level, setLevel] = useState<LevelFilter>("all");
  const [pageSize, setPageSize] = useState<PageSize>(DEFAULT_PAGE_SIZE);
  const [page, setPage] = useState(0);
  const detail = useDetailParam();
  const query = {
    limit: pageSize,
    ...(level === "all" ? {} : { level }),
  };
  const events = $api.useInfiniteQuery(
    "get",
    "/v1/orgs/{org}/events",
    { params: { path: { org: org.login }, query } },
    {
      getNextPageParam: (last) => last.nextCursor ?? undefined,
      initialPageParam: "",
      pageParamName: "cursor",
      refetchInterval: POLL_MS,
    }
  );
  const { pagination, rows } = useCursorPages(
    events,
    pageSize,
    setPageSize,
    page,
    setPage
  );
  const all = events.data?.pages.flatMap((each) => each.items ?? []) ?? [];
  const open = all.find((event) => event.id === detail.value) ?? null;

  return (
    <>
      <PageHeader
        description="What Airship did. Kept for 30 days."
        title="Events"
      />
      <FilterTabs
        label="Event level"
        onChange={(next) => {
          setLevel(next);
          setPage(0);
        }}
        options={filters}
        value={level}
      />
      {events.isPending ? <TableSkeleton /> : null}
      {events.isError ? (
        <ErrorRow error={events.error} fallback="Could not load events" />
      ) : null}
      {events.isSuccess && rows.length === 0 ? (
        <EmptyRow>
          {level === "all" ? "No events yet" : "No events at this level"}
        </EmptyRow>
      ) : null}
      {rows.length > 0 ? (
        <div className="overflow-hidden rounded-lg border bg-card">
          <ul className="divide-y divide-border/70">
            {rows.map((event) => (
              <EventRow
                event={event}
                key={event.id}
                onOpen={() => detail.open(event.id)}
                selected={open?.id === event.id}
              />
            ))}
          </ul>
          <div className="border-t">
            <Pagination {...pagination} />
          </div>
        </div>
      ) : null}
      <EventSheet event={open} onClose={detail.close} />
    </>
  );
}
