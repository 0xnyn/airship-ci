const LOCAL_API = /^https?:\/\/(localhost|127\.0\.0\.1|\[::1\])(:\d+)?(\/|$)/i;

/**
 * Whether an API URL points at the developer's own machine. A production
 * build with such a URL would ship a dashboard calling localhost from
 * every visitor's browser, so the build refuses it.
 */
export function isLocalApiUrl(url: string): boolean {
  return LOCAL_API.test(url);
}
