/**
 * Only same-site paths are allowed as a post-sign-in destination; anything
 * else ("//evil.com", "https://...") falls back to the home page.
 */
export function safeNext(next: string | null | undefined): string {
  if (!next?.startsWith("/") || next.startsWith("//") || next.includes("\\")) {
    return "/";
  }
  return next;
}
