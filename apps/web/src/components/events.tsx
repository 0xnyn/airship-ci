"use client";

import type { Schemas } from "@airship/client";
import { cn } from "@airship/ui/lib/utils";
import { useState } from "react";

import { $api } from "@/lib/api";

import { EmptyRow, FilterTabs, LoadMore, TableSkeleton } from "./data-table";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";
import { RelativeTime } from "./time";

const POLL_MS = 10_000;
const PAGE_SIZE = 50;

type LevelFilter = "all" | Schemas["Event"]["level"];

const filters = [
  { label: "All", value: "all" },
  { label: "Warnings", value: "warning" },
  { label: "Errors", value: "error" },
] as const satisfies readonly { label: string; value: LevelFilter }[];

export function Events() {
  const { org } = useOrg();
  const [level, setLevel] = useState<LevelFilter>("all");
  const query = {
    limit: PAGE_SIZE,
    ...(level === "all" ? {} : { level }),
  };
  const events = $api.useInfiniteQuery(
    "get",
    "/v1/orgs/{org}/events",
    { params: { path: { org: org.login }, query } },
    {
      getNextPageParam: (page) => page.nextCursor ?? undefined,
      initialPageParam: "",
      refetchInterval: POLL_MS,
    }
  );
  const items = events.data?.pages.flatMap((page) => page.items ?? []) ?? [];

  return (
    <>
      <PageHeader
        description="What Airship did and why: launches, failures, clouds connected, repositories activated, config files that stopped parsing."
        title="Events"
      />
      <FilterTabs
        label="Event level"
        onChange={setLevel}
        options={filters}
        value={level}
      />
      {events.isPending ? <TableSkeleton /> : null}
      {events.isSuccess && items.length === 0 ? (
        <EmptyRow>
          {level === "all"
            ? "Nothing has happened yet."
            : "No events at this level in the last 30 days."}
        </EmptyRow>
      ) : null}
      {items.length > 0 ? (
        <ul className="divide-y border">
          {items.map((event) => (
            <EventRow event={event} key={event.id} />
          ))}
        </ul>
      ) : null}
      <LoadMore
        fetchNextPage={events.fetchNextPage}
        hasNextPage={events.hasNextPage}
        isFetchingNextPage={events.isFetchingNextPage}
      />
    </>
  );
}

const levelBar: Record<Schemas["Event"]["level"], string> = {
  error: "bg-destructive",
  info: "bg-border",
  warning: "bg-amber-500",
};

/** One event, as a list item. The left edge carries the level. */
export function EventRow({ event }: { event: Schemas["Event"] }) {
  return (
    <li className="grid grid-cols-[3px_1fr_auto] gap-3 py-2 pr-3 text-xs">
      <span
        aria-hidden="true"
        className={cn("self-stretch", levelBar[event.level])}
      />
      <div className="grid gap-0.5">
        <span className="text-pretty">{event.message}</span>
        <span className="truncate font-mono text-[11px] text-muted-foreground">
          {event.kind}
          {event.repository ? ` · ${event.repository}` : ""}
        </span>
      </div>
      <span className="whitespace-nowrap text-muted-foreground">
        <RelativeTime value={event.createdAt} />
      </span>
    </li>
  );
}
