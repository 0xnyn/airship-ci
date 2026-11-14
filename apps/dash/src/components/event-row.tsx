"use client";

import type { Schemas } from "@airship/client";
import { cn } from "@airship/ui/lib/utils";
import { CircleX, Info, type LucideIcon, TriangleAlert } from "lucide-react";

import { RelativeTime } from "./time";

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

/**
 * One event, as a list item. A leading icon carries the level. With
 * `onOpen` the whole row is a button that opens its detail.
 */
export function EventRow({
  compact = false,
  event,
  onOpen,
  selected = false,
}: {
  compact?: boolean;
  event: Schemas["Event"];
  onOpen?: () => void;
  selected?: boolean;
}) {
  const level = levelIcon[event.level];
  const Icon = level.icon;
  const body = (
    <>
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
    </>
  );
  const layout = cn(
    "grid w-full grid-cols-[auto_1fr_auto] gap-x-3 gap-y-1 px-4 text-left text-xs transition-colors hover:bg-muted",
    compact ? "py-3" : "py-3.5",
    selected && "bg-muted"
  );
  if (onOpen) {
    return (
      <li>
        <button
          className={cn(
            layout,
            "outline-none focus-visible:bg-muted focus-visible:ring-1 focus-visible:ring-ring focus-visible:ring-inset"
          )}
          onClick={onOpen}
          type="button"
        >
          {body}
        </button>
      </li>
    );
  }
  return <li className={layout}>{body}</li>;
}
