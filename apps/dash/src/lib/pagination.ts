/** Page sizes a list offers. */
export const PAGE_SIZES = [25, 50, 100] as const;
export type PageSize = (typeof PAGE_SIZES)[number];
export const DEFAULT_PAGE_SIZE: PageSize = 25;

/**
 * Which rows one page of a list shows. `page` is zero-based and is clamped
 * into range, so a list that shrank under the reader (a filter, a poll)
 * lands on its last page instead of an empty one.
 */
export function pageWindow(
  total: number,
  page: number,
  size: number
): { end: number; page: number; pageCount: number; start: number } {
  const pageCount = Math.max(1, Math.ceil(total / size));
  const current = Math.min(Math.max(0, page), pageCount - 1);
  const start = current * size;
  return {
    end: Math.min(total, start + size),
    page: current,
    pageCount,
    start,
  };
}

/** "1–25 of 130", or "51–100" with a "+" when more exist past what is known. */
export function rangeLabel(
  start: number,
  end: number,
  total: number | null,
  more = false
): string {
  if (end === 0) {
    return "0";
  }
  const range = `${start + 1}–${end}`;
  if (total !== null) {
    return `${range} of ${total}`;
  }
  return more ? `${range} of ${end}+` : `${range} of ${end}`;
}
