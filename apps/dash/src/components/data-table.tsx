"use client";

import { Button } from "@airship/ui/components/button";
import { Skeleton } from "@airship/ui/components/skeleton";
import { cn } from "@airship/ui/lib/utils";
import type { ReactNode } from "react";
import { errorMessage } from "@/lib/api-errors";

/** A segmented control for list filters. */
export function FilterTabs<T extends string>({
  label,
  onChange,
  options,
  value,
}: {
  label: string;
  onChange: (value: T) => void;
  options: readonly { label: string; value: T }[];
  value: T;
}) {
  return (
    <fieldset
      aria-label={label}
      className="flex w-fit gap-0.5 rounded-lg border bg-muted p-0.5"
    >
      {options.map((option) => (
        <button
          aria-pressed={option.value === value}
          className={cn(
            "h-7 rounded-md px-3 font-medium text-[13px] text-muted-foreground transition-colors duration-150 hover:text-foreground",
            option.value === value &&
              "bg-background text-foreground shadow-xs ring-1 ring-border dark:bg-accent dark:ring-0"
          )}
          key={option.value}
          onClick={() => onChange(option.value)}
          type="button"
        >
          {option.label}
        </button>
      ))}
    </fieldset>
  );
}

export function TableSkeleton() {
  return (
    <div className="grid gap-px overflow-hidden rounded-lg border">
      <Skeleton className="h-9 rounded-none" />
      <Skeleton className="h-12 rounded-none opacity-70" />
      <Skeleton className="h-12 rounded-none opacity-50" />
      <Skeleton className="h-12 rounded-none opacity-30" />
    </div>
  );
}

/**
 * What a list shows when it has nothing in it: a dashed frame over the
 * landing page's halftone, so an empty page still has texture.
 */
export function EmptyRow({
  action,
  children,
}: {
  /** The one next thing to do, under the message. */
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="relative grid place-items-center overflow-hidden rounded-lg border border-dashed px-6 py-14 text-center">
      <div
        aria-hidden="true"
        className="mask-[radial-gradient(ellipse_60%_80%_at_50%_50%,black,transparent)] absolute inset-0 bg-halftone"
      />
      <p className="relative max-w-sm text-pretty text-muted-foreground text-sm/relaxed">
        {children}
      </p>
      {action ? <div className="relative mt-4">{action}</div> : null}
    </div>
  );
}

/** What a page shows in place of its data when the request failed. */
export function ErrorRow({
  error,
  fallback,
}: {
  error: unknown;
  fallback: string;
}) {
  return (
    <p
      className="flex items-center justify-center gap-2 rounded-lg border border-danger/25 bg-danger-surface p-5 text-center text-danger-foreground text-xs"
      role="alert"
    >
      {errorMessage(error, fallback)}
    </p>
  );
}

export function LoadMore({
  fetchNextPage,
  hasNextPage,
  isFetchingNextPage,
}: {
  fetchNextPage: () => unknown;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
}) {
  if (!hasNextPage) {
    return null;
  }
  return (
    <div className="flex justify-center">
      <Button
        disabled={isFetchingNextPage}
        onClick={() => fetchNextPage()}
        variant="outline"
      >
        {isFetchingNextPage ? "Loading…" : "Load more"}
      </Button>
    </div>
  );
}
