import assert from "node:assert/strict";
import { test } from "node:test";

import {
  isActiveSection,
  isFlowPath,
  nextFor,
  safeNext,
} from "./navigation.ts";

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

test("the post-sign-in destination keeps the query string", () => {
  assert.equal(
    nextFor("/acme/jobs", "?status=queued"),
    "/acme/jobs?status=queued"
  );
  assert.equal(
    nextFor("/acme/jobs", "status=queued"),
    "/acme/jobs?status=queued"
  );
  assert.equal(nextFor("/acme/jobs", ""), "/acme/jobs");
  assert.equal(nextFor("/acme/jobs", "?"), "/acme/jobs");
});

test("sections match the path case-insensitively and by prefix", () => {
  assert.equal(isActiveSection("/ACME/jobs", "/acme", "/jobs"), true);
  assert.equal(isActiveSection("/acme/jobs/123", "/acme", "/jobs"), true);
  assert.equal(isActiveSection("/acme/jobsx", "/acme", "/jobs"), false);
  assert.equal(isActiveSection("/ACME", "/acme", ""), true);
  assert.equal(isActiveSection("/acme/jobs", "/acme", ""), false);
});

test("guided flows are recognised under any casing of the login", () => {
  assert.equal(isFlowPath("/acme/setup", "/acme"), true);
  assert.equal(isFlowPath("/ACME/repositories/new", "/acme"), true);
  assert.equal(isFlowPath("/acme/repositories/migrate", "/acme"), true);
  assert.equal(isFlowPath("/acme/cloud/connect", "/acme"), true);
  assert.equal(isFlowPath("/acme/repositories", "/acme"), false);
  assert.equal(isFlowPath("/acme/cloud", "/acme"), false);
  assert.equal(isFlowPath("/acme/setupx", "/acme"), false);
});
