import { cn } from "@airship/ui/lib/utils";
import type { Route } from "next";
import Link from "next/link";
import type { ComponentProps, ReactNode } from "react";

/** A framed surface on the page: the dashboard's card. */
export function Panel({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      className={cn("overflow-hidden rounded-lg border bg-card", className)}
      {...props}
    />
  );
}

/** A framed list: rows divided by hairlines, the frame drawn once. */
export function PanelList({ className, ...props }: ComponentProps<"ul">) {
  return (
    <ul
      className={cn(
        "overflow-hidden rounded-lg border bg-card [&>li:last-child]:border-b-0 [&>li]:border-border/70 [&>li]:border-b",
        className
      )}
      {...props}
    />
  );
}

/** A row of figures divided by hairlines, as the landing page's fleet panel draws its stats. */
export function StatGrid({
  children,
  className,
  label,
}: {
  children: ReactNode;
  className?: string;
  label: string;
}) {
  return (
    <section
      aria-label={label}
      className={cn(
        "grid grid-cols-2 gap-px overflow-hidden rounded-lg border bg-border lg:grid-cols-4",
        className
      )}
    >
      {children}
    </section>
  );
}

/** One figure: a mono label, the reading, and a line of context. */
export function Stat({
  dot,
  hint,
  label,
  value,
}: {
  /** A signal class (`bg-success`) for the dot beside the label. */
  dot?: string;
  hint?: string;
  label: string;
  value: string;
}) {
  return (
    <div className="grid content-start gap-2 bg-card p-4 md:p-5">
      <span className="flex items-center gap-1.5 font-mono text-[11px] text-muted-foreground tracking-label">
        {dot ? (
          <span
            aria-hidden="true"
            className={cn("size-1.5 rounded-full", dot)}
          />
        ) : null}
        {label}
      </span>
      <span className="figure font-medium text-[26px] leading-none tracking-display">
        {value}
      </span>
      {hint ? (
        <span className="truncate text-muted-foreground text-xs">{hint}</span>
      ) : null}
    </div>
  );
}

/** A quiet link beside a section heading. */
export function MoreLink({
  children,
  href,
}: {
  children: ReactNode;
  href: string;
}) {
  return (
    <Link
      className="font-mono text-[11px] text-muted-foreground tracking-label transition-colors hover:text-foreground"
      href={href as Route}
    >
      {children} →
    </Link>
  );
}
