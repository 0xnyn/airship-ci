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
    <div className="grid content-start gap-2.5 bg-card p-4 md:p-5">
      <span className="flex items-center gap-1.5 font-medium text-muted-foreground text-sm">
        {dot ? (
          <span
            aria-hidden="true"
            className={cn("size-1.5 rounded-full", dot)}
          />
        ) : null}
        {label}
      </span>
      <span className="figure font-semibold text-[28px] leading-none tracking-[-0.03em]">
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
      className="text-muted-foreground text-sm transition-colors hover:text-foreground"
      href={href as Route}
    >
      {children} →
    </Link>
  );
}

/**
 * A settings card: a framed section with a title, a body, and a muted footer
 * bar that holds a note on the left and the card's actions on the right.
 */
export function SettingsCard({
  className,
  ...props
}: ComponentProps<"section">) {
  return (
    <section
      className={cn("overflow-hidden rounded-lg border bg-card", className)}
      {...props}
    />
  );
}

/** A settings card's header: an optional icon, the title and one line. */
export function SettingsCardHeader({
  action,
  description,
  icon,
  id,
  title,
}: {
  action?: ReactNode;
  description?: ReactNode;
  icon?: ReactNode;
  id?: string;
  title: ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-start gap-x-4 gap-y-3 p-5 md:px-6">
      {icon}
      <div
        className={cn(
          "grid min-w-0 gap-1",
          // With an icon, phones put icon and action on the first line and
          // give the text the full width; without one, the text leads and
          // the action wraps under it.
          icon
            ? "order-last w-full sm:order-none sm:w-auto sm:flex-1"
            : "flex-1 basis-64"
        )}
      >
        <h2 className="font-semibold text-base tracking-tight" id={id}>
          {title}
        </h2>
        {description ? (
          <p className="max-w-prose text-pretty text-muted-foreground text-sm leading-relaxed">
            {description}
          </p>
        ) : null}
      </div>
      {action ? (
        <div className={cn("flex shrink-0 gap-2", icon && "ml-auto sm:ml-0")}>
          {action}
        </div>
      ) : null}
    </div>
  );
}

/** The padded content of a settings card. */
export function SettingsCardBody({
  className,
  ...props
}: ComponentProps<"div">) {
  return <div className={cn("px-5 pb-5 md:px-6", className)} {...props} />;
}

/** The footer bar: a quiet note on the left, the actions on the right. */
export function SettingsCardFooter({
  children,
  note,
}: {
  children?: ReactNode;
  note?: ReactNode;
}) {
  return (
    <div className="flex min-h-14 flex-wrap items-center justify-between gap-x-4 gap-y-2 border-t bg-muted px-5 py-3 md:px-6">
      <div className="min-w-0 text-pretty text-muted-foreground text-sm">
        {note}
      </div>
      {children ? (
        <div className="ml-auto flex shrink-0 items-center gap-2">
          {children}
        </div>
      ) : null}
    </div>
  );
}

/** Label-over-value facts in a row, as a settings card lists what it holds. */
export function Facts({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <dl
      className={cn(
        "grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2 lg:grid-cols-3",
        className
      )}
    >
      {children}
    </dl>
  );
}

export function Fact({
  children,
  label,
  mono = false,
}: {
  children: ReactNode;
  label: string;
  mono?: boolean;
}) {
  return (
    <div className="grid min-w-0 gap-1">
      <dt className="text-muted-foreground text-xs">{label}</dt>
      <dd className={cn("truncate text-sm", mono && "font-mono text-[13px]")}>
        {children}
      </dd>
    </div>
  );
}

/** A titled block inside a detail sheet. */
export function DetailSection({
  children,
  title,
}: {
  children: ReactNode;
  title: string;
}) {
  return (
    <section className="grid gap-1.5">
      <h3 className="font-medium text-muted-foreground text-xs">{title}</h3>
      {children}
    </section>
  );
}

/** A detail sheet's facts: one label-and-value row each, in one frame. */
export function DetailFacts({ children }: { children: ReactNode }) {
  return (
    <dl className="divide-y overflow-hidden rounded-lg border text-sm">
      {children}
    </dl>
  );
}

export function DetailFact({
  children,
  label,
  mono = false,
}: {
  children: ReactNode;
  label: string;
  mono?: boolean;
}) {
  return (
    <div className="flex min-h-9 items-center justify-between gap-4 px-3 py-1.5">
      <dt className="shrink-0 text-muted-foreground">{label}</dt>
      <dd
        className={cn(
          "min-w-0 truncate text-right",
          mono && "font-mono text-[13px]"
        )}
      >
        {children}
      </dd>
    </div>
  );
}
