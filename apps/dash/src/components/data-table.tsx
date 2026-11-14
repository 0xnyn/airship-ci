"use client";

import { Button } from "@airship/ui/components/button";
import { Skeleton } from "@airship/ui/components/skeleton";
import { TableRow } from "@airship/ui/components/table";
import { cn } from "@airship/ui/lib/utils";
import { ChevronLeft, ChevronRight } from "lucide-react";
import type { Route } from "next";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import {
  type ComponentProps,
  type ReactNode,
  useEffect,
  useState,
} from "react";
import { errorMessage } from "@/lib/api-errors";
import {
  DEFAULT_PAGE_SIZE,
  PAGE_SIZES,
  type PageSize,
  pageWindow,
  rangeLabel,
} from "@/lib/pagination";

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

/** The bar under a table: rows per page, where you are, and back and forward. */
export function Pagination({
  canNext,
  canPrev,
  label,
  onNext,
  onPageSize,
  onPrev,
  pageSize,
  pending = false,
}: PaginationProps) {
  return (
    <div className="flex h-11 items-center justify-between gap-3 px-3 text-muted-foreground text-xs">
      <label className="flex items-center gap-2">
        <span className="hidden sm:inline">Rows per page</span>
        <select
          aria-label="Rows per page"
          className="h-7 rounded-md border bg-background px-1.5 text-foreground text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring"
          onChange={(event) =>
            onPageSize(Number(event.target.value) as PageSize)
          }
          value={pageSize}
        >
          {PAGE_SIZES.map((size) => (
            <option key={size} value={size}>
              {size}
            </option>
          ))}
        </select>
      </label>
      <div className="flex items-center gap-1">
        <span aria-live="polite" className="figure mr-2 tabular-nums">
          {label}
        </span>
        <Button
          aria-label="Previous page"
          disabled={!canPrev || pending}
          onClick={onPrev}
          size="icon-sm"
          variant="ghost"
        >
          <ChevronLeft />
        </Button>
        <Button
          aria-label="Next page"
          disabled={!canNext || pending}
          onClick={onNext}
          size="icon-sm"
          variant="ghost"
        >
          <ChevronRight />
        </Button>
      </div>
    </div>
  );
}

export interface PaginationProps {
  canNext: boolean;
  canPrev: boolean;
  label: string;
  onNext: () => void;
  onPageSize: (size: PageSize) => void;
  onPrev: () => void;
  pageSize: PageSize;
  pending?: boolean;
}

/**
 * Pages over a list the client already holds in full. `resetKey` changes
 * (a filter, a search) send the reader back to the first page.
 */
export function useClientPages<T>(
  items: readonly T[],
  resetKey?: unknown
): { pagination: PaginationProps; rows: T[] } {
  const [page, setPage] = useState(0);
  const [pageSize, setPageSize] = useState<PageSize>(DEFAULT_PAGE_SIZE);
  // biome-ignore lint/correctness/useExhaustiveDependencies: resets per key.
  useEffect(() => {
    setPage(0);
  }, [resetKey]);
  const slice = pageWindow(items.length, page, pageSize);
  return {
    pagination: {
      canNext: slice.page < slice.pageCount - 1,
      canPrev: slice.page > 0,
      label: rangeLabel(slice.start, slice.end, items.length),
      onNext: () => setPage(slice.page + 1),
      onPageSize: (size) => {
        setPageSize(size);
        setPage(0);
      },
      onPrev: () => setPage(slice.page - 1),
      pageSize,
    },
    rows: items.slice(slice.start, slice.end),
  };
}

interface CursorQuery<Page> {
  data?: { pages: Page[] };
  fetchNextPage: () => Promise<unknown>;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
}

/**
 * Pages over a cursor-paginated list one server page at a time: the API
 * returns `limit` rows and a cursor, so going forward fetches the next page
 * once and going back shows the ones already fetched.
 */
export function useCursorPages<Page extends { items: unknown[] | null }>(
  query: CursorQuery<Page>,
  pageSize: PageSize,
  onPageSize: (size: PageSize) => void,
  page: number,
  setPage: (page: number) => void
): { pagination: PaginationProps; rows: NonNullable<Page["items"]> } {
  const pages = query.data?.pages ?? [];
  const current = Math.min(page, Math.max(pages.length - 1, 0));
  const rows = (pages[current]?.items ?? []) as NonNullable<Page["items"]>;
  const start = current * pageSize;
  const fetchedAhead = current < pages.length - 1;
  return {
    pagination: {
      canNext: fetchedAhead || query.hasNextPage,
      canPrev: current > 0,
      label: rangeLabel(
        start,
        start + rows.length,
        null,
        fetchedAhead || query.hasNextPage
      ),
      onNext: async () => {
        if (!fetchedAhead) {
          await query.fetchNextPage();
        }
        setPage(current + 1);
      },
      onPageSize: (size) => {
        onPageSize(size);
        setPage(0);
      },
      onPrev: () => setPage(current - 1),
      pageSize,
      pending: query.isFetchingNextPage,
    },
    rows,
  };
}

const INTERACTIVE = "a,button,input,select,textarea,label,[role=menuitem]";

/**
 * A table row that opens its detail: click anywhere on it, or focus it and
 * press Enter. Links and buttons inside it keep doing their own thing.
 */
export function ClickableRow({
  className,
  label,
  onOpen,
  selected = false,
  ...props
}: Omit<ComponentProps<typeof TableRow>, "onClick" | "onKeyDown"> & {
  label: string;
  onOpen: () => void;
  selected?: boolean;
}) {
  return (
    <TableRow
      aria-label={label}
      className={cn(
        "cursor-pointer outline-none focus-visible:bg-muted/60 focus-visible:ring-1 focus-visible:ring-ring focus-visible:ring-inset",
        className
      )}
      data-state={selected ? "selected" : undefined}
      onClick={(event) => {
        const target = event.target as HTMLElement;
        if (target.closest(INTERACTIVE) && target !== event.currentTarget) {
          return;
        }
        onOpen();
      }}
      onKeyDown={(event) => {
        if (
          event.target === event.currentTarget &&
          (event.key === "Enter" || event.key === " ")
        ) {
          event.preventDefault();
          onOpen();
        }
      }}
      tabIndex={0}
      {...props}
    />
  );
}

/**
 * Which row's detail is open, kept in the URL (`?detail=…`) so a detail
 * can be linked to, and Back closes it.
 */
export function useDetailParam(): {
  close: () => void;
  open: (id: string) => void;
  value: string | null;
} {
  const router = useRouter();
  const pathname = usePathname();
  const params = useSearchParams();
  const value = params.get("detail");
  const go = (next: string | null) => {
    const query = new URLSearchParams(params.toString());
    if (next === null) {
      query.delete("detail");
    } else {
      query.set("detail", next);
    }
    const search = query.toString();
    router.replace(`${pathname}${search ? `?${search}` : ""}` as Route, {
      scroll: false,
    });
  };
  return { close: () => go(null), open: go, value };
}
