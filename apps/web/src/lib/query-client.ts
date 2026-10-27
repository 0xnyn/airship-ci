import {
  environmentManager,
  QueryCache,
  QueryClient,
} from "@tanstack/react-query";
import { toast } from "sonner";

import { errorMessage, errorStatus } from "./api-errors";
import { queryErrorToastId } from "./query-errors";

const STALE_TIME_MS = 30_000;
const UNAUTHORIZED = 401;
const MAX_RETRIES = 2;
const ME_PATH = "/v1/me";

function makeQueryClient() {
  const client: QueryClient = new QueryClient({
    defaultOptions: {
      queries: {
        // Client errors will not fix themselves; only retry the rest.
        retry: (failures, error) => {
          const status = errorStatus(error);
          if (status !== undefined && status < 500) {
            return false;
          }
          return failures < MAX_RETRIES;
        },
        staleTime: STALE_TIME_MS,
      },
    },
    queryCache: new QueryCache({
      onError: async (error, query) => {
        if (errorStatus(error) !== UNAUTHORIZED) {
          // One toast per failing query, updated in place: pages poll, and
          // an API outage must not stack a new toast every few seconds.
          toast.error(errorMessage(error, "Request failed"), {
            id: queryErrorToastId(query.queryKey),
          });
          return;
        }
        // The session expired. Refetching the user makes the shell notice
        // and send them to sign in; a toast on top of that is noise.
        if (query.queryKey[1] !== ME_PATH) {
          await client.invalidateQueries({ queryKey: ["get", ME_PATH] });
        }
      },
    }),
  });
  return client;
}

let browserQueryClient: QueryClient | undefined;

/** One client per request on the server, one shared client in the browser. */
export function getQueryClient() {
  if (environmentManager.isServer()) {
    return makeQueryClient();
  }
  browserQueryClient ??= makeQueryClient();
  return browserQueryClient;
}
