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
  body: "Get fully managed GitHub Actions runners in your own cloud with autoscaling, build caching, warm runner pools, and more.",
  buttons: {
    primary: { href: appUrl, label: "Start for free" },
    secondary: { href: docsUrl, label: "Read the docs" },
  } satisfies Record<string, Link>,
  cards: [
    {
      description:
        "Run CI in your AWS, GCP, or Azure account using existing cloud credits.",
      icon: "cloud",
      title: "Cloud Native",
    },
    {
      description:
        "Right-size instances and automatically scale runners to match demand.",
      icon: "file", // TODO: get better icon for this
      title: "Faster CI",
    },
    {
      description:
        "Speed up builds with caching, warm pools, and optimized compute.",
      icon: "spark", // TODO: get better icon for this
      title: "Faster Builds",
    },
    {
      description:
        "No runner provisioning, management, or maintenance required.",
      icon: "layers", // TODO: get better icon for this
      title: "Managed Runners",
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
  label: "Run your CI in your cloud", // TODO: remove this from UI
  sub: "AWS · Google Cloud · Azure", // TODO: remove this from UI
  title: "Run your CI in your cloud",
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
      "From simple tests to complex builds, everything you need to run and scale CI in your own cloud.",
    label: "Features",
    title: "Built for every workload",
  } satisfies Heading,
  items: [
    {
      description:
        "Run CI across AWS, GCP, and Azure with one configuration. Switch clouds without rewriting your workflows.",
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
      title: "Multi-cloud support",
    },
    {
      description:
        "Cut compute costs with Spot instances and automatically fall back to on-demand when capacity runs out.",
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
      title: "Spot with fallback",
    },
    {
      description:
        "Keep runners ready for incoming jobs with warm pools that scale to your schedule and workload.",
      id: "idle",

      preview: {
        footnote: "A job takes a warm runner; a new one boots behind it.",
        heading: "Idle runners · `mon-fri 9-17 → 4`, otherwise 1",
        hourly: officeHours,
        kind: "schedule",
      },
      title: "Warm runner pools",
    },
    {
      description:
        "Run GPU-intensive CI workloads on the accelerators you need, with automatic driver setup.",
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
        "Run CI on Linux, Windows, or ARM using your own machine images, dependencies, and configurations.",
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
      title: "Custom environments",
    },
    {
      description:
        "Speed up repeated builds with GitHub Actions-compatible caching backed by storage in your own cloud.",
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
      title: "Build caching",
    },
    {
      description:
        "Automate repository setup, cloud connections, and access management with a simple API.",
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
      title: "Developer API",
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
        "Airship automatically provisions runners in your cloud as GitHub Actions jobs are queued.",
      footnote: "Labels without the airship- prefix cost no API calls.", // TODO: remove this from UI
      title: "On-Demand Provisioning",
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
        "Airship securely provisions and manages runners in your account.",
      footnote: "Give it a role scoped to what Airship launches.", // TODO: remove this from UI
      title: "Secure Cloud Access",
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
      description: "Airship automatically cleans up idle or orphaned machines.",
      footnote: "Idle 10 minutes, or 8 hours old: the machine goes.", // TODO: remove this from UI
      title: "Automatic Runner Cleanup",
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
        "Configure runner machine types, regions, operating systems, and images to match your workloads.",
      footnote: "Change cloud: in airship.yaml and nothing else.", // TODO: remove this from UI
      title: "Multi-Cloud Support",
      visual: {
        clouds: [
          {
            accent: "var(--cloud-aws)",
            name: "AWS",
            status: "connected",
            sub: "EC2 · S3 cache",
          },
          {
            accent: "var(--cloud-gcp)",
            name: "Google Cloud",
            status: "connected",
            sub: "Compute Engine · GPUs",
          },
          {
            accent: "var(--cloud-azure)",
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
      "Run GitHub Actions on AWS, GCP, or Azure without managing the underlying infrastructure.",
    label: "How it works",
    title: "Fully Managed GitHub Actions Runners",
  } satisfies Heading,
};

/* ─────────────────────────── Security ─────────────────────────── */

export const security = {
  badges: [
    {
      icon: "lock",
      label: "ISOLATED BUILDS",
      note: "Each job runs on its own machine",
    },
    {
      icon: "network",
      label: "YOUR NETWORK",
      note: "Run within your own VPCs and subnets",
    },
    {
      icon: "one-job",
      label: "EPHEMERAL RUNNERS",
      note: "Automatically terminated after each job",
    },
    {
      icon: "key",
      label: "SCOPED CACHE ACCESS",
      note: "Temporary access to designated cache storage",
    },
    {
      icon: "server",
      label: "SELF-HOSTED CONTROL PLANE",
      note: "Deploy the control plane in your environment",
    },
  ] satisfies { icon: IconName; label: string; note: string }[],
  footnote:
    "Runners register with a just-in-time config for a single job, so a machine can't pick up work it wasn't launched for. Only labels starting with airship- are looked up, and only in repositories someone activated.", // TODO: remove this from UI
  heading: {
    accent: "green",
    description:
      "Run GitHub Actions within your own cloud environment, without exposing your code or secrets to third-party runners.",
    label: "Security",
    title: "Secure by Design",
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
