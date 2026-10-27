import assert from "node:assert/strict";
import { test } from "node:test";

import { parseRepositoryInput } from "./repository-name.ts";

test("repository names are read from names, owner/name and pasted URLs", () => {
  for (const typed of [
    "api",
    "acme/api",
    "ACME/api",
    "https://github.com/acme/api",
    "https://github.com/acme/api.git",
    "https://github.com/acme/api/",
    "http://www.github.com/acme/api.git/",
    "  acme/api  ",
  ]) {
    assert.deepEqual(
      parseRepositoryInput(typed, "acme"),
      { name: "api" },
      typed
    );
  }
});

test("repositories outside the organization and junk are refused", () => {
  assert.deepEqual(parseRepositoryInput("other/api", "acme"), {
    problem: "other/api is not in acme.",
  });
  assert.deepEqual(parseRepositoryInput("", "acme"), {
    problem: "Type a repository name.",
  });
  assert.deepEqual(parseRepositoryInput("a/b/c", "acme"), {
    problem: "a/b/c is not a repository name.",
  });
});
