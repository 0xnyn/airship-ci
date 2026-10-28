"use client";

import { formatDateTime, timeAgo } from "@/lib/format";

/** A relative timestamp with the exact time on hover. */
export function RelativeTime({ value }: { value: string | null | undefined }) {
  if (!value) {
    return <span className="text-muted-foreground">—</span>;
  }
  return (
    <time
      dateTime={value}
      suppressHydrationWarning
      title={formatDateTime(value)}
    >
      {timeAgo(value)}
    </time>
  );
}
