import assert from "node:assert/strict";
import { test } from "node:test";

import { matchRepositories, parseRepositoryInput } from "./repository-name.ts";

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

const repos = [
  { fullName: "acme/api" },
  { fullName: "acme/api-gateway" },
  { fullName: "acme/web" },
];

test("typing searches anywhere in the name", () => {
  assert.deepEqual(
    matchRepositories(repos, "api", "acme").items.map((r) => r.fullName),
    ["acme/api", "acme/api-gateway"]
  );
  assert.equal(matchRepositories(repos, "  ", "acme").items.length, 3);
});

test("a pasted name or URL matches that repository exactly", () => {
  for (const typed of ["acme/api", "https://github.com/ACME/api.git"]) {
    const found = matchRepositories(repos, typed, "acme");
    assert.deepEqual(
      found.items.map((r) => r.fullName),
      ["acme/api"]
    );
    assert.equal(found.missing, null);
  }
});

test("a pasted repository the app cannot see says so", () => {
  assert.equal(
    matchRepositories(repos, "acme/secret", "acme").missing,
    "Airship cannot see acme/secret"
  );
  assert.equal(
    matchRepositories(repos, "other/api", "acme").missing,
    "other/api is not in acme."
  );
});
