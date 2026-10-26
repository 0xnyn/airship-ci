import assert from "node:assert/strict";
import { test } from "node:test";

import { formatDuration, secondsBetween } from "./format.ts";

test("formatDuration picks a compact unit", () => {
  assert.equal(formatDuration(0), "0s");
  assert.equal(formatDuration(42.4), "42s");
  assert.equal(formatDuration(185), "3m 05s");
  assert.equal(formatDuration(4320), "1h 12m");
  assert.equal(formatDuration(-1), "—");
  assert.equal(formatDuration(Number.NaN), "—");
});

test("secondsBetween measures to now when the end is missing", () => {
  const start = "2026-01-01T00:00:00Z";
  assert.equal(secondsBetween(start, "2026-01-01T00:01:30Z"), 90);
  const now = new Date("2026-01-01T00:00:10Z").getTime();
  assert.equal(secondsBetween(start, null, now), 10);
});
