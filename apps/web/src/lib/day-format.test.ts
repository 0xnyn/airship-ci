import assert from "node:assert/strict";
import { test } from "node:test";

import { formatUTCDay, utcDayKey } from "./day-format.ts";

test("day buckets are labelled in UTC whatever the browser's zone", () => {
  // Midnight UTC on March 1st is still February 28th in New York; the
  // label must say March 1st.
  assert.equal(formatUTCDay("2026-03-01T00:00:00Z", "en-US"), "Mar 1");
  assert.equal(formatUTCDay("2026-12-31T23:59:59Z", "en-US"), "Dec 31");
  assert.equal(utcDayKey(new Date("2026-03-01T00:00:00Z")), "2026-03-01");
});
