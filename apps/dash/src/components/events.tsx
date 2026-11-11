"use client";

import type { Schemas } from "@airship/client";
import { cn } from "@airship/ui/lib/utils";
import { CircleX, Info, type LucideIcon, TriangleAlert } from "lucide-react";
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

const levelIcon: Record<
  Schemas["Event"]["level"],
  { className: string; icon: LucideIcon; label: string }
> = {
  error: {
    className: "bg-danger-surface text-danger-foreground",
    icon: CircleX,
    label: "Error",
  },
  info: {
    className: "bg-muted text-muted-foreground",
    icon: Info,
    label: "Info",
  },
  warning: {
    className: "bg-warning-surface text-warning-foreground",
    icon: TriangleAlert,
    label: "Warning",
  },
};

/** One event, as a list item. A leading icon carries the level. */
export function EventRow({
  compact = false,
  event,
}: {
  compact?: boolean;
  event: Schemas["Event"];
}) {
  const level = levelIcon[event.level];
  const Icon = level.icon;
  return (
    <li
      className={cn(
        "grid grid-cols-[auto_1fr_auto] gap-x-3 gap-y-1 px-4 text-xs transition-colors hover:bg-muted",
        compact ? "py-3" : "py-3.5"
      )}
    >
      <span
        className={cn(
          "row-span-2 mt-px grid size-5 place-items-center rounded-full",
          level.className
        )}
        title={level.label}
      >
        <Icon aria-hidden="true" className="size-3" />
        <span className="sr-only">{level.label}</span>
      </span>
      <span className={cn("text-pretty text-sm", compact && "line-clamp-2")}>
        {event.message}
      </span>
      <span className="whitespace-nowrap text-muted-foreground text-xs tabular-nums">
        <RelativeTime value={event.createdAt} />
      </span>
      <span className="col-span-2 col-start-2 truncate font-mono text-[11px] text-muted-foreground">
        <span className="text-soft-foreground">{event.kind}</span>
        {event.repository ? ` · ${event.repository}` : ""}
      </span>
    </li>
  );
}
