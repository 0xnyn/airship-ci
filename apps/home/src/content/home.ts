// Everything the landing page says and shows. Components only lay it out.
import { apiUrl, appUrl, docsUrl } from "./site";
import type { Backdrop, CodeFile, Heading, IconName, Link } from "./types";

/* ───────────────────────────── Hero ───────────────────────────── */

export type RunnerState =
  | "pending"
  | "launching"
  | "running"
  | "busy"
  | "finished";

export interface FleetRow {
  cloud: "AWS" | "GCP" | "Azure";
  job: string;
  label: string;
  region: string;
  repo: string;
  spot: boolean;
  state: RunnerState;
  time: string;
  type: string;
}

export interface Fleet {
  org: string;
  page: string;
  rows: FleetRow[];
  stats: { label: string; value: string }[];
}

export const hero = {
  backdrop: { angle: -28, focus: 0.66 } satisfies Backdrop,
  body: "Commit an `airship.yaml`, point `runs-on` at an `airship-` label, and every job gets its own machine in your cloud account. Airship launches it, watches it and tears it down.",
  buttons: {
    primary: { href: appUrl, label: "Start for free" },
    secondary: { href: docsUrl, label: "Read the docs" },
  } satisfies Record<string, Link>,
  cards: [
    {
      description:
        "Machines launch in your AWS, Google Cloud or Azure account. You pay the cloud, at your rates.",
      icon: "cloud",
      title: "Your account, your bill",
    },
    {
      description:
        "airship.yaml says what a runner is: machine type, image, region, spot, GPU. runs-on picks it.",
      icon: "file",
      title: "One file per repo",
    },
    {
      description:
        "Every job gets a new machine that registers for that one job and is gone when it ends.",
      icon: "spark",
      title: "Fresh machine per job",
    },
    {
      description:
        "List instance types in order. Spot capacity first, on-demand when the cloud says no.",
      icon: "layers",
      title: "Spot, then fall back",
    },
  ] satisfies { description: string; icon: IconName; title: string }[],
  fleet: {
    org: "acme",
    page: "Runners",
    rows: [
      {
        cloud: "AWS",
        job: "test (ubuntu, node 24)",
        label: "airship-build",
        region: "us-east-1",
        repo: "acme/web",
        spot: true,
        state: "busy",
        time: "2m 41s",
        type: "c7i.xlarge",
      },
      {
        cloud: "GCP",
        job: "train-smoke",
        label: "airship-gpu",
        region: "us-central1-b",
        repo: "acme/models",
        spot: true,
        state: "launching",
        time: "0m 12s",
        type: "g2-standard-8",
      },
      {
        cloud: "AWS",
        job: "e2e (shard 3/4)",
        label: "airship-build",
        region: "us-east-1",
        repo: "acme/web",
        spot: false,
        state: "running",
        time: "0m 38s",
        type: "m7i.xlarge",
      },
      {
        cloud: "Azure",
        job: "msbuild release",
        label: "airship-windows",
        region: "eastus",
        repo: "acme/desktop",
        spot: false,
        state: "busy",
        time: "6m 02s",
        type: "Standard_D4s_v5",
      },
      {
        cloud: "AWS",
        job: "lint",
        label: "airship-small",
        region: "us-west-2",
        repo: "acme/api",
        spot: true,
        state: "pending",
        time: "—",
        type: "t4g.medium",
      },
      {
        cloud: "AWS",
        job: "cargo build --release",
        label: "airship-arm",
        region: "eu-west-1",
        repo: "acme/engine",
        spot: true,
        state: "finished",
        time: "4m 19s",
        type: "c7g.2xlarge",
      },
    ],
    stats: [
      { label: "Busy", value: "14" },
      { label: "Idle pool", value: "2 / 2" },
      { label: "Spot", value: "71%" },
      { label: "Cap", value: "14 / 40" },
    ],
  } satisfies Fleet,
  label: "Self-hosted GitHub Actions runners",
  sub: "AWS · Google Cloud · Azure",
  title: "Your CI, on your cloud. Without running a single runner yourself.",
  workflow: {
    code: `
jobs:
  test:
    runs-on: airship-build--\${{ github.run_id }}
    steps:
      - uses: actions/checkout@v5
      - run: make test`,
    file: ".github/workflows/ci.yml",
  } satisfies CodeFile,
};

/* ─────────────────────────── Features ─────────────────────────── */

/** What the panel beside the accordion shows for one feature. */
export type FeaturePreview =
  | { kind: "code"; code: CodeFile }
  | {
      attempts: { ok: boolean; spot: boolean; type: string; why: string }[];
      heading: string;
      kind: "attempts";
    }
  | {
      footnote: string;
      heading: string;
      /** Idle runners kept warm in each hour of the day, 0-23. */
      hourly: number[];
      kind: "schedule";
    }
  | { code: CodeFile; kind: "code-and-output"; output: string[] }
  | { heading: string; items: { name: string; note: string }[]; kind: "list" }
  | { code: CodeFile; kind: "code-and-flow"; flow: string[] };

export interface Feature {
  description: string;
  id: string;
  preview: FeaturePreview;
  title: string;
}

const officeHours = Array.from({ length: 24 }, (_, h) =>
  h >= 9 && h < 17 ? 4 : 1
);

export const features = {
  backdrop: { angle: -30, focus: 0.55, intensity: 0.85 } satisfies Backdrop,
  heading: {
    accent: "gold",
    description:
      "Each repository describes its runners in airship.yaml. Airship turns that into machines.",
    label: "Features",
    title: "Everything a runner needs, in one file",
  } satisfies Heading,
  items: [
    {
      description:
        "The same airship.yaml launches EC2 instances, Compute Engine VMs or Azure VMs. Change cloud: and nothing else.",
      id: "clouds",
      preview: {
        code: {
          code: `
runners:
  - name: build
    cloud: aws                 # or gcp · azure
    instance_type: c7i.xlarge
    region: us-east-1
    labels: [airship-build]

  - name: windows
    cloud: azure
    instance_type: Standard_D4s_v5
    machine_image:
      publisher: MicrosoftWindowsServer
      offer: WindowsServer
      sku: 2022-datacenter-g2
    labels: [airship-windows]`,
          file: "airship.yaml",
        },
        kind: "code",
      },
      title: "Three clouds, one format",
    },
    {
      description:
        "List instance types, regions and spot flags in order. Airship walks the list until a cloud says yes, and records why the others said no.",
      id: "spot",
      preview: {
        attempts: [
          {
            ok: false,
            spot: true,
            type: "c7i.xlarge",
            why: "InsufficientInstanceCapacity",
          },
          {
            ok: false,
            spot: true,
            type: "m7i.xlarge",
            why: "MaxSpotInstanceCountExceeded",
          },
          {
            ok: true,
            spot: false,
            type: "c7a.xlarge",
            why: "i-0a3f…e21 running",
          },
        ],
        heading: "Launching `airship-build`",
        kind: "attempts",
      },
      title: "Spot first, on-demand when it runs out",
    },
    {
      description:
        "Keep a few machines booted so jobs start in seconds: always, in office hours, or for a while after work arrives. Per repository or for the whole organization.",
      id: "idle",
      preview: {
        footnote: "A job takes a warm runner; a new one boots behind it.",
        heading: "Idle runners · `mon-fri 9-17 → 4`, otherwise 1",
        hourly: officeHours,
        kind: "schedule",
      },
      title: "Warm runners, on a schedule",
    },
    {
      description:
        "Add gpu: and Airship attaches the accelerator and installs the NVIDIA driver on Ubuntu images that lack one. T4, L4, A100, H100 and up.",
      id: "gpu",
      preview: {
        code: {
          code: `
runners:
  - name: gpu
    cloud: gcp
    instance_type: n1-standard-8
    region: us-central1        # any zone with capacity
    gpu: nvidia-tesla-t4
    preemptible: true
    labels: [airship-gpu]`,
          file: "airship.yaml",
        },
        kind: "code-and-output",
        output: [
          "$ nvidia-smi --query-gpu=name,memory.total",
          "name, memory.total [MiB]",
          "Tesla T4, 15360 MiB",
        ],
      },
      title: "GPU runners",
    },
    {
      description:
        "Ubuntu by default; any AMI, Compute Engine image or Azure gallery image of your own. Windows images get a Windows boot script.",
      id: "images",
      preview: {
        heading: "`machine_image:`",
        items: [
          { name: "ubuntu2404", note: "Canonical, newest" },
          { name: "ami-0123…cdef0", note: "your AMI" },
          { name: "Windows Server 2022", note: "Windows boot script" },
          { name: "c7g · t4g · Ampere", note: "ARM64" },
          { name: "galleries/ci/images/build", note: "Azure gallery" },
          { name: "projects/acme/global/images/ci", note: "GCE image" },
        ],
        kind: "list",
      },
      title: "Windows, ARM and your own images",
    },
    {
      description:
        "On AWS, cache: true puts a proxy on the runner that serves actions/cache from an S3 bucket in your account, in the runner's region. No size limit but yours.",
      id: "cache",
      preview: {
        code: {
          code: `
- uses: actions/cache@v4      # unchanged
  with:
    path: ~/.cargo
    key: cargo-\${{ hashFiles('Cargo.lock') }}`,
          file: "ci.yml",
        },
        flow: [
          "actions/cache",
          "proxy on the runner",
          "S3 in your account, same region",
        ],
        kind: "code-and-flow",
      },
      title: "Cache in your own bucket",
    },
    {
      description:
        "Activate repositories, connect clouds and change access rules from scripts or your own tooling, with a scoped API token.",
      id: "api",
      preview: {
        code: {
          code: `
$ curl -H "Authorization: Bearer $AIRSHIP_TOKEN" \\
    ${apiUrl}/api/v1/repo \\
    -d '{"repository": "acme/web", "active": true}'

{"fullName": "acme/web", "activated": true, ...}`,
          file: "terminal",
        },
        kind: "code",
      },
      title: "A developer API",
    },
  ] satisfies Feature[],
};

/* ──────────────────────────── Clouds ──────────────────────────── */

/* ───────────────────────── How it works ───────────────────────── */

export type HowVisual =
  | {
      kind: "sequence";
      label: string;
      lanes: { id: string; name: string }[];
      steps: { from: string; text: string; to: string }[];
    }
  | {
      kind: "vault";
      open: { call: string; after: string; before: string };
      rows: { key: string; sealed?: boolean; value: string }[];
      seal: string;
    }
  | {
      kind: "lifecycle";
      reasons: string[];
      reasonsHeading: string;
      states: string[];
    }
  | {
      clouds: { accent: string; name: string; status: string; sub: string }[];
      kind: "clouds";
      options: string[];
      optionsHeading: string;
    };

export interface HowCard {
  backdrop: Backdrop;
  description: string;
  footnote: string;
  title: string;
  visual: HowVisual;
}

export const how = {
  button: { href: appUrl, label: "Connect a cloud" } satisfies Link,
  cards: [
    {
      backdrop: { angle: -15, focus: 0.3, intensity: 0.7 },
      description:
        "GitHub tells Airship a job wants an airship- label. Airship finds the runner in that repository's airship.yaml and launches it in your account, registered for exactly that job.",
      footnote: "Labels without the airship- prefix cost no API calls.",
      title: "A job asks. A machine answers.",
      visual: {
        kind: "sequence",
        label:
          "Sequence: GitHub queues a job, Airship launches a machine in your cloud, the runner registers for that job, runs it, and shuts down.",
        lanes: [
          { id: "github", name: "GitHub" },
          { id: "airship", name: "Airship" },
          { id: "cloud", name: "Your cloud" },
        ],
        steps: [
          { from: "github", text: "job queued: airship-build", to: "airship" },
          { from: "airship", text: "reads airship.yaml", to: "airship" },
          {
            from: "airship",
            text: "RunInstances c7i.xlarge · spot",
            to: "cloud",
          },
          {
            from: "cloud",
            text: "runner registers for this one job",
            to: "github",
          },
          { from: "github", text: "job runs", to: "cloud" },
          { from: "cloud", text: "shuts itself down", to: "cloud" },
        ],
      },
    },
    {
      backdrop: { angle: -40, focus: 0.75, intensity: 0.7 },
      description:
        "The access key you connect is encrypted, tied to its own database row, and opened only for the length of one cloud call. It is never logged and never sent back.",
      footnote: "Give it a role scoped to what Airship launches.",
      title: "Your key, sealed at rest",
      visual: {
        kind: "vault",
        open: {
          after: "→ dropped",
          before: "opened for",
          call: "RunInstances",
        },
        rows: [
          { key: "cloud", value: "aws · us-east-1" },
          { key: "access_key", sealed: true, value: "AKIA••••••••••••" },
          { key: "secret_key", sealed: true, value: "••••••••••••••••••••" },
        ],
        seal: "sealed · bound to this row",
      },
    },
    {
      backdrop: { angle: -25, focus: 0.5, intensity: 0.7 },
      description:
        "Every runner moves through one lifecycle, and every ending is recorded. A reconciler compares the cloud with the database and removes machines nobody is using, so a lost answer from the cloud doesn't become a bill.",
      footnote: "Idle 10 minutes, or 8 hours old: the machine goes.",
      title: "Nothing left running",
      visual: {
        kind: "lifecycle",
        reasons: [
          "completed",
          "cancelled",
          "idle",
          "launch_failed",
          "lost",
          "max_lifetime",
        ],
        reasonsHeading: "finished, and why",
        states: ["pending", "launching", "running", "busy", "finished"],
      },
    },
    {
      backdrop: { angle: -32, focus: 0.65, intensity: 0.7 },
      description:
        "Connect one cloud or all three. Each runner names its cloud, and machines launch with your credentials, in your regions, on your bill.",
      footnote: "Change cloud: in airship.yaml and nothing else.",
      title: "Runs where your account already is",
      visual: {
        clouds: [
          {
            accent: "#ffb36b",
            name: "AWS",
            status: "connected",
            sub: "EC2 · S3 cache",
          },
          {
            accent: "#8ab4ff",
            name: "Google Cloud",
            status: "connected",
            sub: "Compute Engine · GPUs",
          },
          {
            accent: "#6fd3ff",
            name: "Azure",
            status: "connected",
            sub: "Virtual Machines",
          },
        ],
        kind: "clouds",
        options: [
          "ubuntu2404",
          "Windows Server",
          "arm64",
          "nvidia-l4",
          "spot",
          "custom AMI",
          "gallery image",
        ],
        optionsHeading: "runs",
      },
    },
  ] satisfies HowCard[],
  heading: {
    accent: "blue",
    description:
      "Airship's control plane holds your sealed cloud key, listens to GitHub, and launches, watches and terminates machines in your account. Nothing long-lived runs on your side.",
    label: "How it works",
    title: "We run the fleet. You own the machines.",
  } satisfies Heading,
};

/* ─────────────────────────── Security ─────────────────────────── */

export const security = {
  badges: [
    {
      icon: "lock",
      label: "SEALED KEYS",
      note: "Encrypted at rest, opened per call",
    },
    {
      icon: "network",
      label: "YOUR NETWORK",
      note: "Your network, your subnets",
    },
    {
      icon: "one-job",
      label: "ONE JOB",
      note: "One machine per job, then gone",
    },
    {
      icon: "key",
      label: "SCOPED CACHE",
      note: "Short-lived grants to one bucket",
    },
    {
      icon: "server",
      label: "SELF-HOST",
      note: "Run the control plane yourself",
    },
  ] satisfies { icon: IconName; label: string; note: string }[],
  footnote:
    "Runners register with a just-in-time config for a single job, so a machine can't pick up work it wasn't launched for. Only labels starting with airship- are looked up, and only in repositories someone activated.",
  heading: {
    accent: "green",
    description:
      "Airship reads airship.yaml and job events from GitHub and makes cloud calls with your key. Your code, secrets and build output stay between GitHub and your machines.",
    label: "Security",
    title: "Your jobs run on machines you own",
  } satisfies Heading,
};

/* ────────────────────────── Get started ───────────────────────── */

export const getStarted = {
  backdrop: { angle: -35, focus: 0.7, intensity: 0.9 } satisfies Backdrop,
  button: { href: appUrl, label: "Start for free" } satisfies Link,
  code: {
    code: `
runners:
  - name: build
    cloud: aws
    instance_type: [c7i.xlarge, m7i.xlarge]
    preemptible: [true, false]
    labels: [airship-build]
    extra_config:
      cache: true`,
    file: "airship.yaml",
  } satisfies CodeFile,
  label: "Get started",
  steps: [
    "Install the GitHub App",
    "Connect a cloud",
    "Activate a repository",
    "Commit airship.yaml",
  ],
  title: "Your first runner in four steps.",
};
