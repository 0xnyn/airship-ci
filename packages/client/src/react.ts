import createQueryHooks from "openapi-react-query";

import type { ApiClient } from "./index";

/**
 * Wraps a client in TanStack Query hooks whose paths, params and results are
 * all checked against the API.
 *
 * @example
 * const $api = createApiHooks(api);
 * const { data } = $api.useQuery("get", "/v1/projects");
 * const create = $api.useMutation("post", "/v1/projects");
 */
export function createApiHooks(client: ApiClient) {
  return createQueryHooks(client);
}

export type ApiHooks = ReturnType<typeof createApiHooks>;
