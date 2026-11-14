import type { Schemas } from "@airship/client";

/**
 * The migration assistant's planning, kept apart from React so it can be
 * tested: which jobs to suggest, which runners they need, and the draft a
 * person can leave and come back to.
 */

export type Cloud = Schemas["MigrationRunner"]["cloud"];
export type WorkflowJob = Schemas["WorkflowJob"];
export type WorkflowScan = Schemas["WorkflowScan"];
type Platform = WorkflowJob["platform"];

/** The steps of the assistant, in order. */
export const STEPS = ["repository", "jobs", "runners", "review"] as const;
export type Step = (typeof STEPS)[number];

/** A runner definition being drafted. */
export interface RunnerDraft {
  cloud: Cloud;
  instanceType: string;
  machineImage: string;
  name: string;
  preemptible: boolean;
  region: string;
}

/** Everything a person has chosen for one repository so far. */
export interface Draft {
  runners: RunnerDraft[];
  /** Jobs to move, by jobKey. */
  selected: string[];
  step: Step;
  /** The label each selected job moves to, by jobKey. */
  targets: Record<string, string>;
  updatedAt: number;
  version: 1;
}

export const LABEL_PREFIX = "airship-";

/** Display names, as the Cloud page titles them. */
export const cloudTitle: Record<Cloud, string> = {
  aws: "Amazon Web Services",
  azure: "Microsoft Azure",
  daytona: "Daytona",
  e2b: "E2B",
  gcp: "Google Cloud",
  lelantos: "Lelantos",
  modal: "Modal",
  namespace: "Namespace",
  novita: "Novita",
  runloop: "Runloop",
  tenki: "Tenki",
};

export const allClouds = Object.keys(cloudTitle) as Cloud[];

/** Clouds whose machines can be Arm. */
const armClouds: readonly Cloud[] = ["aws", "gcp", "azure"];

/** Clouds that take the ubuntu2204 / ubuntu2404 image aliases. */
const aliasClouds: readonly Cloud[] = [
  "aws",
  "gcp",
  "azure",
  "daytona",
  "namespace",
  "tenki",
  "modal",
];

/**
 * A four-vCPU machine on each cloud: the size of GitHub's standard Linux
 * runner for public repositories, so jobs see no slowdown when they move.
 */
const shapes: Record<Cloud, { arm64?: string; x64: string }> = {
  aws: { arm64: "c7g.xlarge", x64: "c7i.xlarge" },
  azure: { arm64: "Standard_D4ps_v5", x64: "Standard_D4s_v5" },
  daytona: { x64: "4vcpu-8gb" },
  e2b: { x64: "base" },
  gcp: { arm64: "t2a-standard-4", x64: "n2-standard-4" },
  lelantos: { x64: "base" },
  modal: { x64: "4vcpu-16gb" },
  namespace: { x64: "4vcpu-16gb" },
  novita: { x64: "base" },
  runloop: { x64: "medium" },
  tenki: { x64: "4vcpu-16gb" },
};

/** Suggested machine shapes per cloud, for the instance type picker. */
export const shapeSuggestions: Record<Cloud, readonly string[]> = {
  aws: [
    "c7i.large",
    "c7i.xlarge",
    "c7i.2xlarge",
    "c7i.4xlarge",
    "m7i.xlarge",
    "c7g.xlarge",
    "c7g.2xlarge",
  ],
  azure: [
    "Standard_D2s_v5",
    "Standard_D4s_v5",
    "Standard_D8s_v5",
    "Standard_D4ps_v5",
  ],
  daytona: ["2vcpu-4gb", "4vcpu-8gb", "8vcpu-16gb"],
  e2b: ["base", "base:4vcpu"],
  gcp: [
    "n2-standard-2",
    "n2-standard-4",
    "n2-standard-8",
    "c3-standard-4",
    "t2a-standard-4",
  ],
  lelantos: ["base", "base:4vcpu"],
  modal: ["2vcpu-8gb", "4vcpu-16gb", "8vcpu-32gb"],
  namespace: ["2vcpu-8gb", "4vcpu-16gb", "8vcpu-32gb"],
  novita: ["base", "base:4vcpu"],
  runloop: ["small", "medium", "large", "x-large"],
  tenki: ["2vcpu-8gb", "4vcpu-16gb", "8vcpu-32gb"],
};

/** A job's identity across a repository. */
export function jobKey(workflow: string, job: string): string {
  return `${workflow}#${job}`;
}

export function splitJobKey(key: string): { job: string; workflow: string } {
  const at = key.lastIndexOf("#");
  return { job: key.slice(at + 1), workflow: key.slice(0, at) };
}

/** The label a definition named `name` serves. */
export function labelFor(name: string): string {
  return `${LABEL_PREFIX}${name.toLowerCase()}`;
}

/** Whether a job is suggested by default: a GitHub-hosted Linux job. */
export function suggested(job: WorkflowJob): boolean {
  return (
    job.editable &&
    job.kind === "hosted" &&
    (job.platform === "linux-x64" || job.platform === "linux-arm64")
  );
}

/** The kind of machine a job needs, which decides the runner it shares. */
export interface MachineGroup {
  arm: boolean;
  image: string;
  /** The definition name suggested for the group. */
  name: string;
  platform: Platform;
}

export function machineGroup(job: WorkflowJob): MachineGroup {
  if (job.platform === "windows") {
    return { arm: false, image: "", name: "windows", platform: "windows" };
  }
  if (job.platform === "macos") {
    return { arm: false, image: "", name: "macos", platform: "macos" };
  }
  const arm = job.platform === "linux-arm64";
  const image = job.image || "ubuntu2404";
  const name = `linux${arm ? "-arm" : ""}${image === "ubuntu2204" ? "-2204" : ""}`;
  return { arm, image, name, platform: arm ? "linux-arm64" : "linux-x64" };
}

/** A definition for `group` on `cloud`, with the cloud's default shape. */
export function runnerFor(group: MachineGroup, cloud: Cloud): RunnerDraft {
  const shape = shapes[cloud];
  const arm = group.arm && armClouds.includes(cloud);
  return {
    cloud,
    instanceType: arm && shape.arm64 ? shape.arm64 : shape.x64,
    machineImage: aliasClouds.includes(cloud) ? group.image : "",
    name: group.name,
    preemptible: false,
    region: "",
  };
}

/** Moves a definition to another cloud, keeping what still applies. */
export function changeCloud(runner: RunnerDraft, cloud: Cloud): RunnerDraft {
  const arm = runner.instanceType === shapes[runner.cloud].arm64;
  const image = runner.machineImage;
  const keepsImage = aliasClouds.includes(cloud) || image === "";
  return {
    ...runner,
    cloud,
    instanceType:
      arm && armClouds.includes(cloud)
        ? (shapes[cloud].arm64 ?? shapes[cloud].x64)
        : shapes[cloud].x64,
    machineImage: keepsImage && aliasClouds.includes(cloud) ? image : "",
    preemptible: armClouds.includes(cloud) ? runner.preemptible : false,
    region: "",
  };
}

/** Warnings worth showing beside a definition before it is written. */
export function runnerNotes(
  runner: RunnerDraft,
  jobs: WorkflowJob[]
): string[] {
  const notes: string[] = [];
  const platforms = new Set(jobs.map((job) => machineGroup(job).platform));
  if (platforms.has("linux-arm64") && !armClouds.includes(runner.cloud)) {
    notes.push(`${cloudTitle[runner.cloud]} machines are x64 only`);
  }
  if (platforms.has("windows") && !runner.machineImage) {
    notes.push("Set a Windows image, or these jobs get Ubuntu");
  }
  if (platforms.has("macos")) {
    notes.push("Airship has no macOS images; these jobs will run on Linux");
  }
  return notes;
}

/**
 * Gives every selected job a target, adding a definition for each kind of
 * machine that has none yet. Choices already made are kept.
 */
export function fillTargets(
  draft: Draft,
  jobs: Map<string, WorkflowJob>,
  cloud: Cloud,
  labels: readonly string[]
): Draft {
  const runners = [...draft.runners];
  const targets: Record<string, string> = {};
  const served = new Set([
    ...labels.map((label) => label.toLowerCase()),
    ...runners.map((runner) => labelFor(runner.name)),
  ]);
  for (const key of draft.selected) {
    const job = jobs.get(key);
    if (!job) {
      continue;
    }
    const current = draft.targets[key];
    if (current && served.has(current)) {
      targets[key] = current;
      continue;
    }
    const group = machineGroup(job);
    const label = labelFor(group.name);
    if (!served.has(label)) {
      runners.push(runnerFor(group, cloud));
      served.add(label);
    }
    targets[key] = label;
  }
  return { ...draft, runners, targets };
}

/** Definitions no selected job runs on. */
export function unusedRunners(draft: Draft): RunnerDraft[] {
  const used = new Set(Object.values(draft.targets));
  return draft.runners.filter((runner) => !used.has(labelFor(runner.name)));
}

/** The request body for preview and pull request. */
export function toPlan(draft: Draft): Schemas["MigrationPlan"] {
  const moves = draft.selected.flatMap((key) => {
    const label = draft.targets[key];
    return label ? [{ ...splitJobKey(key), label }] : [];
  });
  return {
    moves,
    runners: draft.runners.map((runner) => ({
      cloud: runner.cloud,
      instanceType: runner.instanceType.trim(),
      machineImage: runner.machineImage.trim() || undefined,
      name: runner.name,
      preemptible: runner.preemptible || undefined,
      region: runner.region.trim() || undefined,
    })),
  };
}

/** A fresh draft that suggests every GitHub-hosted Linux job. */
export function newDraft(scan: WorkflowScan): Draft {
  const selected: string[] = [];
  for (const workflow of scan.workflows) {
    for (const job of workflow.jobs) {
      if (suggested(job)) {
        selected.push(jobKey(workflow.path, job.id));
      }
    }
  }
  return {
    runners: [],
    selected,
    step: "jobs",
    targets: {},
    updatedAt: Date.now(),
    version: 1,
  };
}

/** Every job in a scan, by jobKey. */
export function jobsByKey(scan: WorkflowScan): Map<string, WorkflowJob> {
  const out = new Map<string, WorkflowJob>();
  for (const workflow of scan.workflows) {
    for (const job of workflow.jobs) {
      out.set(jobKey(workflow.path, job.id), job);
    }
  }
  return out;
}

/** Labels the repository's own file and the organization already serve. */
export function servedLabels(scan: WorkflowScan): string[] {
  const own = (scan.config?.runners ?? []).flatMap(
    (runner) => runner.labels ?? []
  );
  return [...new Set([...own, ...scan.sharedLabels])];
}

const NAME = /^[a-z0-9][a-z0-9._-]{0,62}$/;

/** Why a definition name cannot be used, or null. */
export function nameProblem(
  name: string,
  others: readonly string[]
): string | null {
  if (!NAME.test(name)) {
    return "Lowercase letters, digits, '.', '_' and '-'";
  }
  if (others.includes(name)) {
    return "Another runner has this name";
  }
  return null;
}

// Drafts live in the browser, per organization and repository: a
// convenience for coming back later, never the source of truth.
const STORAGE_PREFIX = "airship:migrate:";

function storageKey(org: string, repo: number): string {
  return `${STORAGE_PREFIX}${org}:${repo}`;
}

function isDraft(value: unknown): value is Draft {
  return (
    typeof value === "object" &&
    value !== null &&
    (value as Draft).version === 1 &&
    Array.isArray((value as Draft).selected) &&
    Array.isArray((value as Draft).runners)
  );
}

export function loadDraft(org: string, repo: number): Draft | null {
  try {
    const raw = localStorage.getItem(storageKey(org, repo));
    const parsed: unknown = raw ? JSON.parse(raw) : null;
    return isDraft(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

export function saveDraft(org: string, repo: number, draft: Draft): void {
  try {
    localStorage.setItem(
      storageKey(org, repo),
      JSON.stringify({ ...draft, updatedAt: Date.now() })
    );
  } catch {
    // Storage may be full or blocked; the draft just is not kept.
  }
}

export function clearDraft(org: string, repo: number): void {
  try {
    localStorage.removeItem(storageKey(org, repo));
  } catch {
    // Nothing to clear.
  }
}

/** Repositories with a draft in progress, most recent first. */
export function listDrafts(
  org: string
): { repo: number; step: Step; updatedAt: number }[] {
  const out: { repo: number; step: Step; updatedAt: number }[] = [];
  try {
    const prefix = `${STORAGE_PREFIX}${org}:`;
    for (let index = 0; index < localStorage.length; index += 1) {
      const key = localStorage.key(index);
      if (!key?.startsWith(prefix)) {
        continue;
      }
      const repo = Number(key.slice(prefix.length));
      const draft = loadDraft(org, repo);
      if (draft && Number.isSafeInteger(repo)) {
        out.push({ repo, step: draft.step, updatedAt: draft.updatedAt });
      }
    }
  } catch {
    return [];
  }
  return out.sort((a, b) => b.updatedAt - a.updatedAt);
}
