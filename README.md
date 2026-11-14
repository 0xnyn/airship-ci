<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="apps/docs/logo/dark.svg">
  <img alt="Airship" src="apps/docs/logo/light.svg" height="40">
</picture>

### Self-hosted GitHub Actions runners in your own cloud, configured as code.

Every job gets a fresh machine in your AWS, Google Cloud, Azure or Daytona
account, and the machine is destroyed when the job ends.

[Quickstart](#quickstart) ·
[`airship.yaml`](docs/airship-yaml.md) ·
[Architecture](docs/architecture.md) ·
[Local development](docs/deploy/development/README.md) ·
[Deploying](docs/deploy/README.md)

<br>

<img alt="The Airship dashboard: runners by state, jobs, success rate, queue time and runner minutes for the last 24 hours, with recent jobs and events" src="docs/images/dashboard.png">

</div>

## Contents

- [How it works](#how-it-works)
- [Quickstart](#quickstart)
- [Features](#features)
- [The dashboard](#the-dashboard)
- [Supported clouds](#supported-clouds)
- [Repository layout](#repository-layout)
- [Running locally](#running-locally)
- [Creating the GitHub App](#creating-the-github-app)
- [Commands](#commands)
- [Testing](#testing)
- [Deploying](#deploying)

## How it works

1. **Connect a cloud.** Paste an access key under **Cloud**. It is verified,
   sealed with `CREDENTIALS_KEY` and never shown again.
2. **Activate a repository** and commit an `airship.yaml` that says which
   machines its jobs run on.
3. **Point a workflow at it** with
   `runs-on: airship-<label>--${{ github.run_id }}`.

When GitHub queues the job, its `workflow_job` webhook reaches the control
plane. The control plane mints a just-in-time runner registration and
launches an instance in your account with your key. The runner registers,
runs exactly that one job and terminates. Postgres is the only state, and
every lifecycle transition is a conditional update. Webhooks, launchers and
the reconciler can therefore race safely, and several API replicas can run
at once.

```mermaid
sequenceDiagram
    participant GH as GitHub
    participant CP as Airship control plane
    participant Cloud as Your cloud account
    GH->>CP: workflow_job queued (webhook)
    CP->>CP: Read airship.yaml, pick a machine
    CP->>Cloud: Launch instance (JIT runner config)
    Cloud->>GH: Runner registers, runs the one job
    GH->>CP: workflow_job completed
    CP->>Cloud: Terminate instance
```

Machines never need inbound access. Credentials are opened only for the
length of a cloud call. The full design, including the runner state machine
and its invariants, is in [docs/architecture.md](docs/architecture.md).

## Quickstart

Add `airship.yaml` to the root of an activated repository:

```yaml
# airship.yaml
runners:
  - name: build
    cloud: aws
    instance_type: [c7i.xlarge, m7i.xlarge]   # tried in order
    machine_image: ami-0123456789abcdef0
    region: us-east-1
    preemptible: [true, false]                 # spot first, on-demand if it fails
    labels: [airship-build]
    extra_config:
      cache: true                              # S3-backed actions/cache
```

Then target it from a workflow:

```yaml
# .github/workflows/ci.yml
jobs:
  test:
    runs-on: airship-build--${{ github.run_id }}
    steps:
      - uses: actions/checkout@v4
      - run: make test
```

Or skip the file entirely with a one-line runner:

```yaml
runs-on: airship-aws-c7g-xlarge-ubuntu2404-spot--${{ github.run_id }}
```

The complete format is in [docs/airship-yaml.md](docs/airship-yaml.md).

## Features

**Configuration as code**
- An `airship.yaml` per repository, with fallback lists for instance type,
  image, region and spot.
- `extra_config` for EBS volumes (`BlockDeviceMappings`), `env_vars`,
  `cache` and `provision_via: pull_from_api`. Windows runners are configured
  with `run_as: interactive`, `runner_user` and `runner_path`, and GPU
  machines with `gpu`.
- One-line runners that need no file, and `--suffix` labels for a unique
  runner per job.

**Organizations**
- Organization-wide runners in a `.airship` repository
  (`airship.global.yaml`), with `access.yaml` policies for which
  repositories and teams may use them.
- Teams, admin and member roles taken from GitHub, and a per-installation
  runner cap.

**Speed and cost**
- Idle runner pools: `replicas`, `schedule` and `usage_based`, per
  repository or organization-wide.
- Spot or preemptible machines, falling back to on-demand.
- A cache proxy on each machine that serves `actions/cache`, the `setup-*`
  actions and `docker/build-push-action` (`url_v2`) from an S3 bucket in
  your AWS account. See [docs/caching.md](docs/caching.md).

**Developer API**
- `repo`, `cloud-connect`, `access-control` and `runner-provision`
  endpoints, authenticated with bearer-token API keys. Interactive docs are
  served at `/docs` on the API.

## The dashboard

The dashboard covers Repositories, Cloud, Runners, Jobs, Analytics, Events,
Teams, API keys and Settings, in dark and light themes.

<table>
  <tr>
    <td width="50%" valign="top">
      <b>Runners</b><br>
      Every machine Airship launched: its state, definition, instance type, region and lifetime.<br><br>
      <img alt="Runners page listing busy, idle, launching and waiting runners on AWS and Google Cloud" src="docs/images/runners.png">
    </td>
    <td width="50%" valign="top">
      <b>Jobs</b><br>
      Every workflow job GitHub reported, the runner it ran on, and why a job was skipped.<br><br>
      <img alt="Jobs page listing queued, running, successful and skipped jobs with queue time and duration" src="docs/images/jobs.png">
    </td>
  </tr>
  <tr>
    <td width="50%" valign="top">
      <b>Repositories</b><br>
      Activate repositories and see whether each <code>airship.yaml</code> is valid.<br><br>
      <img alt="Repositories page with activated repositories, their config status and one config problem" src="docs/images/repositories.png">
    </td>
    <td width="50%" valign="top">
      <b>Analytics</b><br>
      Jobs, failures, queue time and runner minutes by day and by repository.<br><br>
      <img alt="Analytics page with a 30-day bar chart of jobs on Airship versus other runners" src="docs/images/analytics.png">
    </td>
  </tr>
  <tr>
    <td width="50%" valign="top">
      <b>Cloud</b><br>
      Connect AWS, Google Cloud, Azure or Daytona. Credentials are sealed and never shown again.<br><br>
      <img alt="Cloud page with AWS and Google Cloud connected and verified" src="docs/images/cloud.png">
    </td>
    <td width="50%" valign="top">
      <b>Events</b><br>
      What Airship did and why: launches, fallbacks, terminations and config problems, kept for 30 days.<br><br>
      <img alt="Events page listing runner launches and terminations by repository" src="docs/images/events.png">
    </td>
  </tr>
</table>

<sub>Screenshots show sample data.</sub>

## Supported clouds

| Cloud | Runs jobs on | Cache proxy | Setup guide |
| --- | --- | --- | --- |
| AWS | EC2 instances | S3 | [Console](docs/architecture/aws-setup-console.md) · [CLI](docs/architecture/aws-setup-cli.md) |
| Google Cloud | Compute Engine instances | — | [Console](docs/architecture/gcp-setup-console.md) · [CLI](docs/architecture/gcp-setup-cli.md) |
| Microsoft Azure | Virtual machines | — | [Portal](docs/architecture/azure-setup-console.md) · [CLI](docs/architecture/azure-setup-cli.md) |
| Daytona | Sandboxes | — | [Guide](docs/architecture/daytona-setup.md) |

Each guide assumes no prior experience with that cloud. The cache proxy
uses S3, so on the other clouds `cache: true` launches without it and
records a `cache.unavailable` warning, and the job still runs. A cloud is an implementation of the
`cloud.Provider` interface in `apps/api/internal/cloud`, so another can be
added.

**Connecting AWS:** under **Cloud**, paste an IAM user's access key. The
user needs `AmazonEC2FullAccess`. For `extra_config.cache` it also needs S3
bucket management on `airship-cache-*` and `sts:GetFederationToken`; the
exact policy is in [docs/caching.md](docs/caching.md). Runners launch in
the region a definition names, or else in the default region you pick
(`eu-west-1` unless you change it). They go into the region's default VPC
with an egress-only security group that Airship creates.

## Repository layout

A Go and TypeScript monorepo, run by Turborepo and pnpm.

| Path | What it is |
| --- | --- |
| `apps/api` | Go control plane (`cmd/api`), migrations (`cmd/migrate`) and the runner-side cache proxy (`cmd/cacheproxy`) |
| `apps/dash` | Next.js 16 dashboard |
| `apps/home` | Marketing site (Astro) |
| `apps/docs` | User-facing documentation (Mintlify); the API reference is generated from `openapi.json` |
| `packages/client` | TypeScript client generated from the API's OpenAPI document |
| `packages/tokens` | Design tokens shared by every app |
| `packages/ui` | Shared shadcn/ui components |
| `deploy/` | Production env templates, Render blueprint, Docker Compose stack |
| `docs/` | Design docs, cloud setup guides and deployment guides |

The Go API is the source of truth for every type on the wire:

```
SQL ──sqlc──► Go structs ──Huma──► openapi.json ──openapi-typescript──► @airship/client ──► Next.js
```

`pnpm generate` rebuilds the chain and `pnpm dev` keeps it live.
`pnpm check:generated` fails if a generated file is stale.

## Running locally

You need Go 1.27+, Node 24+, pnpm 11 and Docker.

```bash
pnpm install
cp apps/api/.env.example apps/api/.env   # DEV_LOGIN=true and a CREDENTIALS_KEY are set here
pnpm db:up
pnpm db:migrate
pnpm dev
```

Open http://localhost:3000 and choose **Development sign-in**. It signs you
in as an admin of a demo organization, `acme`. The API's interactive docs
are at http://localhost:8080/docs.

Jobs only arrive once a [GitHub App](#creating-the-github-app) is
configured, and launches need real cloud credentials under **Cloud**. The
full guide is [docs/deploy/development/](docs/deploy/development/README.md).
It covers going from a blank machine and a test GitHub organization to a
real job on a real cloud machine, plus every environment variable, the
daily workflow and troubleshooting.

## Creating the GitHub App

Create an app at **GitHub → Settings → Developer settings → GitHub Apps**:

- **Callback URL**: `{PUBLIC_URL}/v1/auth/github/callback`
- **Webhook URL**: `{PUBLIC_URL}/v1/github/webhook`, with a random secret.
  For local development, expose port 8080 with a tunnel (e.g.
  `cloudflared tunnel --url localhost:8080`) and use that as `PUBLIC_URL`.
- **Repository permissions**:

  | Permission | Access | Why |
  | --- | --- | --- |
  | Administration | Read & write | Register and remove repository runners |
  | Actions | Read | Look up jobs and runs |
  | Contents | Read (write for the access-control API) | Read `airship.yaml`; open pull requests on `.airship` |
  | Pull requests | Write | Open pull requests on `.airship` |
  | Metadata | Read | Required by GitHub |

- **Organization permissions**:

  | Permission | Access | Why |
  | --- | --- | --- |
  | Members | Read | Tell admins from members; team rules in `access.yaml` |
  | Self-hosted runners | Read & write | Idle runners from `airship.global.yaml`, which register on the organization in runner groups |

- **Subscribe to events**: Workflow job; Push (to refresh idle runners when
  `airship.yaml` changes); Repository (renames and visibility changes).

Then set `GITHUB_APP_SLUG`, `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`,
`GITHUB_WEBHOOK_SECRET` and `GITHUB_PRIVATE_KEY_FILE` (or
`GITHUB_PRIVATE_KEY`) in `apps/api/.env`.

## Commands

Run `make setup` once after cloning, then `make dev`. `make help` lists
every target; each one wraps one of these pnpm scripts.

| Command | Does |
| --- | --- |
| `pnpm dev` | API with hot reload, dashboard, live type generation |
| `pnpm generate` | sqlc → openapi.json → TypeScript client |
| `pnpm lint` / `pnpm format` | Ultracite (Biome) for TypeScript, golangci-lint for Go |
| `pnpm typecheck` / `pnpm test` / `pnpm build` | Across all packages |
| `pnpm check` | Everything to run before pushing |
| `pnpm db:up` / `db:down` | Start or stop the Compose Postgres (local Supabase is `make supabase-up` / `make supabase-down`) |
| `pnpm db:migrate` / `db:rollback` | Apply or undo migrations |
| `make down` | Stop every local container, whichever database is up |
| `make db-destroy` / `supabase-destroy` | Delete a local database and its volume (asks first) |
| `make docs` | Preview the documentation site on :4100 |

## Testing

Go tests that use the database need `TEST_DATABASE_URL`, and each test
creates its own database. Without it they are skipped, or fail where `CI`
is set.

```bash
make test   # sets it to the local Postgres
TEST_DATABASE_URL='postgres://airship:airship@localhost:5432/airship?sslmode=disable' pnpm test
```

The fleet tests run against a fake cloud and a fake GitHub. Each provider
also has its own harness:

- **EC2**: [moto](https://github.com/getmoto/moto), or a real account,
  where one small instance is launched and terminated again.
- **Compute Engine** and **Azure**: fakes of their REST APIs, driven
  through the real SDKs.
- **Daytona**: a fake of its API, or a real organization.

```bash
uvx --from 'moto[server]' moto_server -p 5055 &
cd apps/api && AIRSHIP_AWS_ENDPOINT=http://localhost:5055 go test -run TestAgainstEmulator ./internal/cloud/aws

cd apps/api && AIRSHIP_LIVE_AWS_ACCESS_KEY_ID=AKIA... AIRSHIP_LIVE_AWS_SECRET_ACCESS_KEY=... \
  AIRSHIP_LIVE_AWS_REGION=eu-west-1 go test -v -run TestAgainstLiveAWS ./internal/cloud/aws
```

Every suite is described in
[docs/deploy/development/7-tests.md](docs/deploy/development/7-tests.md).

## Deploying

The guides are in [docs/deploy/](docs/deploy/README.md):

| Stage | Stack | Guide |
| --- | --- | --- |
| **Stage 0**, the pilot (start here) | Supabase Postgres, API on Render, dashboard on Vercel | [Six-step runbook](docs/deploy/stage-0/README.md) · [Where each env var comes from](docs/deploy/stage-0/env.md) |
| **Stage 1** | AWS: ECS Fargate, RDS, Secrets Manager | [stage-1/aws.md](docs/deploy/stage-1/aws.md) |
| Alternatives | Google Cloud, Azure, a single Docker Compose server, PlanetScale | [alternatives/](docs/deploy/alternatives/README.md) |

All of them share the [production checklist](docs/deploy/checklist.md).
Postgres 17 or newer is required; 18 is recommended.

The control plane is one container plus Postgres:

```bash
docker build -t airship-api apps/api
docker run --rm --entrypoint migrate -e DATABASE_URL=... airship-api up   # once per release
docker run -p 8080:8080 -e DATABASE_URL=... -e PUBLIC_URL=https://... -e CREDENTIALS_KEY=... airship-api
```

Things to get right in production:

- **`PUBLIC_URL` must be https.** Runner machines fetch the cache proxy and
  its credentials from it.
- **`CREDENTIALS_KEY` is a base64 32-byte key you keep**
  (`openssl rand -base64 32`). Losing it loses every connected cloud. To
  rotate it, list the new key first and the old one after a comma, deploy,
  reconnect each cloud, then drop the old key.
- **Serve the dashboard on the same site as the API**, e.g.
  `app.example.com` and `api.example.com`, so the session cookie is sent.
  `NEXT_PUBLIC_API_URL` is inlined when `next build` runs, so set it in the
  build environment. A production build with the `localhost` default fails
  on purpose.
- **Replicas are safe.** Launchers claim runners with row locks, and the
  reconciler elects a leader through a Postgres advisory lock.
- **Build with the Dockerfile.** The image embeds the cache proxy for
  `linux/amd64` and `linux/arm64`. A plain `go build` of the API leaves it
  out, and cache-enabled runners then run their jobs without acceleration.
