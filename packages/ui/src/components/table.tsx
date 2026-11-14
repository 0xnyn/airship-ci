"use client";

import { cn } from "@airship/ui/lib/utils";
import type * as React from "react";

function Table({
  className,
  footer,
  ...props
}: React.ComponentProps<"table"> & {
  /** Sits under the rows inside the same frame, such as pagination. */
  footer?: React.ReactNode;
}) {
  return (
    <div
      className="relative w-full overflow-hidden rounded-lg border bg-card"
      data-slot="table-container"
    >
      <div className="w-full overflow-x-auto">
        <table
          className={cn("w-full caption-bottom text-xs", className)}
          data-slot="table"
          {...props}
        />
      </div>
      {footer ? (
        <div className="border-t" data-slot="table-footer-bar">
          {footer}
        </div>
      ) : null}
    </div>
  );
}

function TableHeader({ className, ...props }: React.ComponentProps<"thead">) {
  return (
    <thead
      className={cn(
        "bg-muted/30 [&_tr]:border-b [&_tr]:hover:bg-transparent",
        className
      )}
      data-slot="table-header"
      {...props}
    />
  );
}

function TableBody({ className, ...props }: React.ComponentProps<"tbody">) {
  return (
    <tbody
      className={cn("[&_tr:last-child]:border-0", className)}
      data-slot="table-body"
      {...props}
    />
  );
}

function TableFooter({ className, ...props }: React.ComponentProps<"tfoot">) {
  return (
    <tfoot
      className={cn(
        "border-t bg-muted/50 font-medium [&>tr]:last:border-b-0",
        className
      )}
      data-slot="table-footer"
      {...props}
    />
  );
}

function TableRow({ className, ...props }: React.ComponentProps<"tr">) {
  return (
    <tr
      className={cn(
        "border-border/70 border-b transition-colors duration-150 hover:bg-muted/40 has-aria-expanded:bg-muted/50 data-[state=selected]:bg-muted",
        className
      )}
      data-slot="table-row"
      {...props}
    />
  );
}

function TableHead({ className, ...props }: React.ComponentProps<"th">) {
  return (
    <th
      className={cn(
        "h-9 whitespace-nowrap px-4 text-left align-middle font-medium text-muted-foreground text-xs [&:has([role=checkbox])]:pr-0",
        className
      )}
      data-slot="table-head"
      {...props}
    />
  );
}

function TableCell({ className, ...props }: React.ComponentProps<"td">) {
  return (
    <td
      className={cn(
        "whitespace-nowrap px-4 py-2.5 align-middle [&:has([role=checkbox])]:pr-0",
        className
      )}
      data-slot="table-cell"
      {...props}
    />
  );
}

function TableCaption({
  className,
  ...props
}: React.ComponentProps<"caption">) {
  return (
    <caption
      className={cn("mt-4 text-muted-foreground text-xs", className)}
      data-slot="table-caption"
      {...props}
    />
  );
}

export {
  Table,
  TableBody,
  TableCaption,
  TableCell,
  TableFooter,
  TableHead,
  TableHeader,
  TableRow,
};
