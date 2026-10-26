import { createApiClient } from "@airship/client";
import { createApiHooks } from "@airship/client/react";
import type { QueryClient } from "@tanstack/react-query";

import { ENV } from "@/env";

/**
 * Typed fetch client for the Go API. The session lives in an HttpOnly
 * cookie on the API's origin, so every request includes credentials.
 */
export const api = createApiClient(ENV.NEXT_PUBLIC_API_URL, {
  credentials: "include",
});

/** TanStack Query hooks over `api`, e.g. `$api.useQuery("get", "/v1/me")`. */
export const $api = createApiHooks(api);

/** Marks every cached query for an API path stale, whatever its params. */
export function invalidatePath(queryClient: QueryClient, ...paths: string[]) {
  return queryClient.invalidateQueries({
    predicate: (query) => paths.includes(String(query.queryKey[1])),
  });
}
