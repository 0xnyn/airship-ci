import assert from "node:assert/strict";
import { test } from "node:test";

import { queryErrorToastId } from "./query-errors.ts";

test("polling queries share one toast per API path", () => {
  const first = queryErrorToastId([
    "get",
    "/v1/orgs/{org}/jobs",
    { params: {} },
  ]);
  const again = queryErrorToastId([
    "get",
    "/v1/orgs/{org}/jobs",
    { params: { status: "queued" } },
  ]);
  assert.equal(first, again);
  assert.notEqual(first, queryErrorToastId(["get", "/v1/orgs/{org}/runners"]));
  assert.equal(queryErrorToastId([]), "query-error:unknown");
});
