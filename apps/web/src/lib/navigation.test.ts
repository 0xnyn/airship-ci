import assert from "node:assert/strict";
import { test } from "node:test";

import { safeNext } from "./navigation.ts";

test("safeNext keeps local paths and rejects everything else", () => {
  assert.equal(
    safeNext("/acme/jobs?status=queued"),
    "/acme/jobs?status=queued"
  );
  for (const unsafe of [
    null,
    "",
    "acme",
    "//evil.example",
    "/\\evil.example",
    "https://evil.example",
  ]) {
    assert.equal(safeNext(unsafe), "/");
  }
});
