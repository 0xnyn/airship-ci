import createFetchClient, { type ClientOptions } from "openapi-fetch";

import type { components, paths } from "./generated/api";

export type { components, operations, paths } from "./generated/api";

/** Every named schema in the API, e.g. `Schemas["Project"]`. */
export type Schemas = components["schemas"];

/** RFC 9457 problem details, the body of every error response. */
export type Problem = Schemas["ErrorModel"];

/** Creates a typed fetch client for the API at `baseUrl`. */
export function createApiClient(
  baseUrl: string,
  options: Omit<ClientOptions, "baseUrl"> = {}
) {
  return createFetchClient<paths>({ ...options, baseUrl });
}

export type ApiClient = ReturnType<typeof createApiClient>;

/** Thrown by `unwrap` when the API answers with an error status. */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: Partial<Problem>;

  constructor(status: number, problem: Partial<Problem>) {
    super(
      problem.detail ?? problem.title ?? `Request failed with status ${status}`
    );
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
  }
}

type FetchResult<T> =
  | { data: T; error?: never; response: Response }
  | { data?: never; error: unknown; response: Response };

/**
 * Resolves to the response body or throws an `ApiError`, so callers (and
 * TanStack Query) can use plain try/catch instead of checking `error`.
 *
 * @example
 * const project = await unwrap(api.GET("/v1/projects/{id}", { params: { path: { id } } }));
 */
export async function unwrap<T>(request: Promise<FetchResult<T>>): Promise<T> {
  const result = await request;
  if (result.error !== undefined || !result.response.ok) {
    const problem =
      typeof result.error === "object" && result.error !== null
        ? result.error
        : {};
    throw new ApiError(result.response.status, problem);
  }
  return result.data as T;
}
