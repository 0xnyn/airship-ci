"use client";

import type { Schemas } from "@airship/client";
import { cn } from "@airship/ui/lib/utils";
import { useState } from "react";

import { $api } from "@/lib/api";

import {
  EmptyRow,
  ErrorRow,
  FilterTabs,
  LoadMore,
  TableSkeleton,
} from "./data-table";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";
import { PanelList } from "./panel";
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
        description="What Airship did. Kept for 30 days."
        title="Events"
      />
      <FilterTabs
        label="Event level"
        onChange={setLevel}
        options={filters}
        value={level}
      />
      {events.isPending ? <TableSkeleton /> : null}
      {events.isError ? (
        <ErrorRow error={events.error} fallback="Could not load events" />
      ) : null}
      {events.isSuccess && items.length === 0 ? (
        <EmptyRow>
          {level === "all" ? "No events yet" : "No events at this level"}
        </EmptyRow>
      ) : null}
      {items.length > 0 ? (
        <PanelList>
          {items.map((event) => (
            <EventRow event={event} key={event.id} />
          ))}
        </PanelList>
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
  error: "bg-danger",
  info: "bg-transparent",
  warning: "bg-warning",
};

/** One event, as a list item. The left edge carries the level. */
export function EventRow({
  compact = false,
  event,
}: {
  compact?: boolean;
  event: Schemas["Event"];
}) {
  return (
    <li
      className={cn(
        "relative grid grid-cols-[1fr_auto] gap-x-4 gap-y-1 px-4 text-xs transition-colors hover:bg-muted/40",
        compact ? "py-3" : "py-3.5"
      )}
    >
      <span
        aria-hidden="true"
        className={cn(
          "absolute inset-y-2 left-0 w-[3px] rounded-r-full",
          levelBar[event.level]
        )}
      />
      <span
        className={cn("text-pretty text-[13px]", compact && "line-clamp-2")}
      >
        {event.message}
      </span>
      <span className="whitespace-nowrap font-mono text-[11px] text-muted-foreground">
        <RelativeTime value={event.createdAt} />
      </span>
      <span className="col-span-2 truncate font-mono text-[11px] text-muted-foreground">
        <span className="text-soft-foreground">{event.kind}</span>
        {event.repository ? ` · ${event.repository}` : ""}
      </span>
    </li>
  );
}
