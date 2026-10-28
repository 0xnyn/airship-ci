/**
 * The toast id for a failed query: one per API path, so a page that polls
 * updates its toast in place instead of stacking a new one every tick.
 */
export function queryErrorToastId(queryKey: readonly unknown[]): string {
  return `query-error:${String(queryKey[1] ?? queryKey[0] ?? "unknown")}`;
}
