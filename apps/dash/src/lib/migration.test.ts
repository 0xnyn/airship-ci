import assert from "node:assert/strict";
import { test } from "node:test";

import {
  changeCloud,
  fillTargets,
  jobsByKey,
  machineGroup,
  nameProblem,
  newDraft,
  runnerFor,
  runnerNotes,
  servedLabels,
  toPlan,
  unusedRunners,
  type WorkflowJob,
  type WorkflowScan,
} from "./migration.ts";

function job(overrides: Partial<WorkflowJob>): WorkflowJob {
  return {
    editable: true,
    id: "build",
    image: "ubuntu2404",
    kind: "hosted",
    line: 5,
    matrix: false,
    name: "",
    platform: "linux-x64",
    reason: "",
    runsOn: ["ubuntu-latest"],
    ...overrides,
  };
}

const scan: WorkflowScan = {
  defaultBranch: "main",
  repository: "acme/web",
  sharedLabels: ["airship-shared"],
  skipped: [],
  workflows: [
    {
      jobs: [
        job({ id: "lint" }),
        job({ id: "test", image: "ubuntu2204", runsOn: ["ubuntu-22.04"] }),
        job({ id: "arm", platform: "linux-arm64" }),
        job({ id: "win", image: "", platform: "windows" }),
        job({ editable: false, id: "dyn", kind: "expression", platform: "" }),
      ],
      name: "CI",
      path: ".github/workflows/ci.yml",
      problem: null,
    },
  ],
};

test("a new draft suggests the hosted Linux jobs only", () => {
  assert.deepEqual(newDraft(scan).selected, [
    ".github/workflows/ci.yml#lint",
    ".github/workflows/ci.yml#test",
    ".github/workflows/ci.yml#arm",
  ]);
});

test("jobs that need the same machine share a runner", () => {
  const draft = fillTargets(newDraft(scan), jobsByKey(scan), "aws", []);
  assert.deepEqual(
    draft.runners.map((runner) => [
      runner.name,
      runner.instanceType,
      runner.machineImage,
    ]),
    [
      ["linux", "c7i.xlarge", "ubuntu2404"],
      ["linux-2204", "c7i.xlarge", "ubuntu2204"],
      ["linux-arm", "c7g.xlarge", "ubuntu2404"],
    ]
  );
  assert.equal(
    draft.targets[".github/workflows/ci.yml#arm"],
    "airship-linux-arm"
  );
  assert.deepEqual(unusedRunners(draft), []);
});

test("targets already chosen are kept, stale ones refilled", () => {
  const draft = {
    ...newDraft(scan),
    targets: {
      ".github/workflows/ci.yml#lint": "airship-shared",
      ".github/workflows/ci.yml#test": "airship-gone",
    },
  };
  const filled = fillTargets(draft, jobsByKey(scan), "gcp", servedLabels(scan));
  assert.equal(
    filled.targets[".github/workflows/ci.yml#lint"],
    "airship-shared"
  );
  assert.equal(
    filled.targets[".github/workflows/ci.yml#test"],
    "airship-linux-2204"
  );
  assert.equal(filled.runners[0]?.instanceType, "n2-standard-4");
});

test("the plan carries moves and trims empty fields", () => {
  const draft = fillTargets(newDraft(scan), jobsByKey(scan), "runloop", []);
  const plan = toPlan(draft);
  assert.equal(plan.moves.length, 3);
  assert.deepEqual(plan.moves[0], {
    job: "lint",
    label: "airship-linux",
    workflow: ".github/workflows/ci.yml",
  });
  assert.equal(plan.runners[0]?.machineImage, undefined);
  assert.equal(plan.runners[0]?.region, undefined);
});

test("moving a runner to another cloud swaps its shape", () => {
  const arm = runnerFor(machineGroup(job({ platform: "linux-arm64" })), "aws");
  assert.equal(changeCloud(arm, "azure").instanceType, "Standard_D4ps_v5");
  const sandbox = changeCloud(arm, "e2b");
  assert.equal(sandbox.instanceType, "base");
  assert.equal(sandbox.machineImage, "");
  assert.deepEqual(runnerNotes(sandbox, [job({ platform: "linux-arm64" })]), [
    "E2B machines are x64 only",
  ]);
});

test("runner names must make valid labels", () => {
  assert.equal(nameProblem("linux", []), null);
  assert.ok(nameProblem("Linux Box", []));
  assert.ok(nameProblem("linux", ["linux"]));
});
