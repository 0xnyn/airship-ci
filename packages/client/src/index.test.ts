import assert from "node:assert/strict";
import { test } from "node:test";

import { ApiError, unwrap } from "./index.ts";

const ok = new Response(null, { status: 200 });
const notFound = new Response(null, { status: 404 });

test("unwrap returns data on success", async () => {
  const data = await unwrap(
    Promise.resolve({ data: { id: "1" }, response: ok })
  );
  assert.deepEqual(data, { id: "1" });
});

test("unwrap throws ApiError with the problem details", async () => {
  const request = Promise.resolve({
    error: { detail: "project not found", status: 404, title: "Not Found" },
    response: notFound,
  });
  await assert.rejects(unwrap(request), (error: unknown) => {
    assert.ok(error instanceof ApiError);
    assert.equal(error.status, 404);
    assert.equal(error.message, "project not found");
    return true;
  });
});
