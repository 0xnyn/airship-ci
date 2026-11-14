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

/** Where to return after signing in: the current path with its query. */
export function nextFor(pathname: string, search: string): string {
  if (search === "" || search === "?") {
    return pathname;
  }
  return `${pathname}${search.startsWith("?") ? "" : "?"}${search}`;
}

/**
 * Whether a navigation section is the current one. Organization logins
 * are case-insensitive on GitHub, so /ACME/jobs is the jobs section of
 * /acme.
 */
export function isActiveSection(
  pathname: string,
  base: string,
  href: string
): boolean {
  const current = pathname.toLowerCase();
  const target = `${base}${href}`.toLowerCase();
  if (href === "") {
    return current === target || current === `${target}/`;
  }
  return current === target || current.startsWith(`${target}/`);
}

/** Paths under an organization that are guided flows, shown full screen. */
const FLOW_PATHS = [
  "/setup",
  "/repositories/new",
  "/repositories/migrate",
  "/cloud/connect",
] as const;

/**
 * Whether a path is a guided flow, which takes the whole window: no
 * sidebar, just the steps and a way out.
 */
export function isFlowPath(pathname: string, base: string): boolean {
  return FLOW_PATHS.some((path) => isActiveSection(pathname, base, path));
}
