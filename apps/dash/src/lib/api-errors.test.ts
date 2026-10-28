import assert from "node:assert/strict";
import { test } from "node:test";

import { errorMessage, errorStatus } from "./api-errors.ts";

test("errorMessage names the first invalid field", () => {
  const problem = {
    detail: "validation failed",
    errors: [{ location: "body.image", message: "expected length >= 1" }],
    status: 422,
  };
  assert.equal(errorMessage(problem, "x"), "image: expected length >= 1");
  assert.equal(errorStatus(problem), 422);
});

test("errorMessage falls back to the detail, then the default", () => {
  assert.equal(
    errorMessage({ detail: "profile has active runners" }, "x"),
    "profile has active runners"
  );
  assert.equal(errorMessage(new Error("network down"), "x"), "network down");
  assert.equal(errorMessage(undefined, "Request failed"), "Request failed");
  assert.equal(errorStatus("nope"), undefined);
});
