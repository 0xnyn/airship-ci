import assert from "node:assert/strict";
import { test } from "node:test";

import { pageWindow, rangeLabel } from "./pagination.ts";

test("a page covers its slice of the list", () => {
  assert.deepEqual(pageWindow(130, 0, 25), {
    end: 25,
    page: 0,
    pageCount: 6,
    start: 0,
  });
  assert.deepEqual(pageWindow(130, 5, 25), {
    end: 130,
    page: 5,
    pageCount: 6,
    start: 125,
  });
});

test("a page past the end lands on the last page", () => {
  assert.equal(pageWindow(30, 9, 25).page, 1);
  assert.equal(pageWindow(30, -1, 25).page, 0);
});

test("an empty list still has one page", () => {
  assert.deepEqual(pageWindow(0, 3, 25), {
    end: 0,
    page: 0,
    pageCount: 1,
    start: 0,
  });
});

test("the range names the rows shown and how many there are", () => {
  assert.equal(rangeLabel(0, 25, 130), "1–25 of 130");
  assert.equal(rangeLabel(25, 50, null, true), "26–50 of 50+");
  assert.equal(rangeLabel(25, 40, null, false), "26–40 of 40");
  assert.equal(rangeLabel(0, 0, 0), "0");
});
