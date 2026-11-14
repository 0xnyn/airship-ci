import assert from "node:assert/strict";
import { test } from "node:test";

import { diffLines, hunks } from "./line-diff.ts";

test("a changed line shows as removed then added, the rest kept", () => {
  const lines = diffLines(
    "jobs:\n  a:\n    runs-on: ubuntu-latest\n",
    "jobs:\n  a:\n    runs-on: airship-linux\n"
  );
  assert.deepEqual(
    lines.map((line) => `${line.kind} ${line.text}`),
    [
      "keep jobs:",
      "keep   a:",
      "remove     runs-on: ubuntu-latest",
      "add     runs-on: airship-linux",
    ]
  );
  assert.equal(lines[2]?.before, 3);
  assert.equal(lines[3]?.after, 3);
});

test("a new file is all additions", () => {
  const lines = diffLines("", "a\nb\n");
  assert.deepEqual(
    lines.map((line) => line.kind),
    ["add", "add"]
  );
});

test("long unchanged runs fold, keeping context around changes", () => {
  const before = Array.from({ length: 20 }, (_, i) => `line ${i}`).join("\n");
  const after = before.replace("line 10", "changed");
  const out = hunks(diffLines(before, after), 2);
  assert.deepEqual(
    out.map((hunk) => (hunk.kind === "fold" ? hunk.count : hunk.lines.length)),
    [8, 6, 7]
  );
});
