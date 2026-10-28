/**
 * The analytics API buckets jobs by UTC day. Labelling those buckets in
 * the browser's zone would shift every bar west of Greenwich to the day
 * before, so days are always formatted in UTC.
 */
export function formatUTCDay(iso: string, locale?: string): string {
  return new Intl.DateTimeFormat(locale, {
    day: "numeric",
    month: "short",
    timeZone: "UTC",
  }).format(new Date(iso));
}

/** The YYYY-MM-DD key of a UTC day bucket. */
export function utcDayKey(date: Date): string {
  return date.toISOString().slice(0, 10);
}
