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
    <fieldset aria-label={label} className="flex w-fit border">
      {options.map((option) => (
        <button
          aria-pressed={option.value === value}
          className={cn(
            "px-3 py-1.5 text-muted-foreground text-xs hover:text-foreground",
            option.value === value && "bg-muted font-medium text-foreground"
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
    <div className="grid gap-2">
      <Skeleton className="h-8" />
      <Skeleton className="h-10" />
      <Skeleton className="h-10" />
      <Skeleton className="h-10" />
    </div>
  );
}

export function EmptyRow({ children }: { children: ReactNode }) {
  return (
    <p className="border border-dashed p-10 text-center text-muted-foreground text-xs">
      {children}
    </p>
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
      className="border border-destructive/30 bg-destructive/10 p-6 text-center text-destructive text-xs"
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
