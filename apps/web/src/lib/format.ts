const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

const relative = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
const dateTime = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "short",
});
const integer = new Intl.NumberFormat();

/** "3 minutes ago", "in 2 hours". */
export function timeAgo(iso: string, now: number = Date.now()): string {
  const delta = new Date(iso).getTime() - now;
  const abs = Math.abs(delta);
  if (abs < MINUTE) {
    return relative.format(Math.round(delta / SECOND), "second");
  }
  if (abs < HOUR) {
    return relative.format(Math.round(delta / MINUTE), "minute");
  }
  if (abs < DAY) {
    return relative.format(Math.round(delta / HOUR), "hour");
  }
  return relative.format(Math.round(delta / DAY), "day");
}

/** Absolute timestamp for tooltips and titles. */
export function formatDateTime(iso: string): string {
  return dateTime.format(new Date(iso));
}

/** Compact duration: "42s", "3m 05s", "1h 12m". */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) {
    return "—";
  }
  const whole = Math.round(seconds);
  if (whole < 60) {
    return `${whole}s`;
  }
  const minutes = Math.floor(whole / 60);
  if (minutes < 60) {
    return `${minutes}m ${String(whole % 60).padStart(2, "0")}s`;
  }
  return `${Math.floor(minutes / 60)}h ${String(minutes % 60).padStart(2, "0")}m`;
}

/** Seconds between two timestamps, or until now when `end` is missing. */
export function secondsBetween(
  start: string,
  end: string | null | undefined,
  now: number = Date.now()
): number {
  const stop = end ? new Date(end).getTime() : now;
  return (stop - new Date(start).getTime()) / SECOND;
}

export function formatInteger(value: number): string {
  return integer.format(value);
}
