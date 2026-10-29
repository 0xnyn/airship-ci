# Deploying Airship

**What this is.** Where **Airship itself** runs: the API, the dashboard and
the database — on your own machine, and then in production. The production
guides are organised in two stages, because what you should run during the
pilot is not what you should run with paying customers on it.

**Audience.** Whoever runs Airship, from a first clone to operating it.
Running it day to day is in
[architecture/operations.md](../architecture/operations.md).

---

## Start here

| Where you are running it | Guide |
| --- | --- |
| **Your own machine** | **[development/](development/README.md)** — a blank laptop to a real job on a real cloud machine, in seven steps: the tools, the first run, every environment variable, a test GitHub organization and a tunnel, real machines, the daily workflow, and the tests |
| **The pilot, now** | [stage-0/](stage-0/README.md) — Supabase, Render, Vercel |
| **AWS, later** | [stage-1/](stage-1/README.md) — ECS Fargate, RDS |

New to the codebase? Everything you need is in
[development/](development/README.md), starting from `git clone`.

---

## Which production stage am I on?

| | [Stage 0 — the pilot](stage-0/) | [Stage 1 — AWS](stage-1/) |
| --- | --- | --- |
| **Use it when** | Now. First customers, one region, nothing to migrate yet | The pilot has outgrown managed hosting: compliance, a VPC, private database networking, or AWS committed spend |
| **API** | A Render web service, ×2 | ECS Fargate behind an ALB, ×2 |
| **Dashboard** | Vercel | ECS Fargate behind the same ALB |
| **Database** | [Supabase](stage-0/3-supabase.md) Postgres 17 | RDS for PostgreSQL 18, Multi-AZ |
| **Secrets** | Render's encrypted environment | Secrets Manager |
| **You operate** | Nothing. No VPC, no load balancer, no CLI | A VPC, subnets, an ALB, ECR, ECS, RDS |
| **Cost** | ~$50/month ([1-accounts.md](stage-0/1-accounts.md#what-it-costs)) | ~$250/month and up |
| **Time to stand up** | About 90 minutes | About a day |

**Start at [stage-0/README.md](stage-0/README.md).** It is an ordered
runbook: six steps, each with the exact values to paste, and a checkpoint
at the end of each one. It assumes nothing about your machine — the local
setup is [development/](development/README.md) and is independent of it. Nothing in it is thrown away when you move to
stage 1 — the domain, the GitHub App, the schema and the data all carry
over.

Other platforms, for a customer who has to self-host or who is already on
another cloud, are in [alternatives/](alternatives/): Google Cloud, Azure,
a single server with Docker Compose, and PlanetScale as the database.

---

## Two different clouds

These guides are about where **Airship** runs. Where **runner machines**
run is each customer's choice, made by connecting a credential in the
dashboard; see the setup guides for
[AWS](../architecture/aws-setup-console.md), [Google Cloud](../architecture/gcp-setup-console.md),
[Azure](../architecture/azure-setup-console.md) and [Daytona](../architecture/daytona-setup.md).
Each cloud has two guides: a `-console` one that clicks through the web
console and assumes no experience with that cloud, and a `-cli` one that is
the same path as commands ([aws](../architecture/aws-setup-cli.md),
[gcp](../architecture/gcp-setup-cli.md), [azure](../architecture/azure-setup-cli.md)).

The two are independent. A control plane on Render can launch EC2
instances, and the reverse. The control plane needs **no cloud permissions
of its own**: it launches with the credentials customers connect, never
with the identity of the platform it runs on.

Daytona is a place to run runners, not to host services, so it has no
guide here.

---

## What gets deployed

```
                     ┌──────────── your domain ─────────────┐
 browsers ──────────►│ app.example.com     api.example.com  │◄─── GitHub webhooks
                     └───────┬──────────────────┬───────────┘◄─── runner machines (cache)
                             │                  │
                     ┌───────▼──────┐   ┌───────▼──────┐
                     │ dashboard    │   │ api × 2+     │──► api.github.com
                     │ apps/dash    │   │ apps/api     │──► customers' clouds
                     │ :3000        │   │ :8080        │
                     └──────────────┘   └───────┬──────┘
                                                │
                                        ┌───────▼──────┐
                                        │ Postgres     │  the only state
                                        └──────────────┘
```

| Piece | Image | Notes |
| --- | --- | --- |
| API | `apps/api/Dockerfile` (context `apps/api`) | distroless, nonroot, `:8080`. Holds `/usr/local/bin/api` (the default entrypoint) and `/usr/local/bin/migrate`. Embeds the cache proxy for `linux/amd64` and `linux/arm64` |
| Migrations | Same image, entrypoint `/usr/local/bin/migrate`, argument `up` | Run once per release, **before** rolling out the API. Idempotent |
| Dashboard | `apps/dash/Dockerfile` (context: repository root) | Next.js standalone server, nonroot, `:3000`. `NEXT_PUBLIC_API_URL` is a **build argument**, baked in at build time |
| Postgres | **17 or newer**, managed; 18 where the platform offers it | The schema uses `uuidv7()`, built into Postgres 18. On 17 the first migration defines an equivalent. `make test-pg17` checks 17 |

Stage 0 builds both images on the platforms themselves (Render from the
Dockerfile, Vercel with its own Next.js build), so there is nothing to
build by hand. Stage 1 builds and pushes them:

```bash
VERSION=$(git rev-parse --short HEAD)
docker build -t airship-api:$VERSION --build-arg VERSION=$VERSION apps/api
docker build -t airship-dash:$VERSION -f apps/dash/Dockerfile \
  --build-arg NEXT_PUBLIC_API_URL=https://api.example.com .
```

Most platforms run on `linux/amd64`. On an Apple Silicon machine add
`--platform linux/amd64` to both builds, or build them on a Linux machine.
`make images NEXT_PUBLIC_API_URL=https://...` runs both lines.

---

## The environment variables

Every variable both pieces read, **with where each value comes from**, is
in [stage-0/env.md](stage-0/env.md). It applies to any platform: stage 1
reads the same variables out of Secrets Manager instead of Render.

The templates themselves are
[`deploy/production/api.env.example`](../../deploy/production/api.env.example)
and [`deploy/production/dash.env.example`](../../deploy/production/dash.env.example).
The full reference with defaults is in
[operations.md, "Configuration"](../architecture/operations.md#configuration).

---

## Before you go live

Work through [checklist.md](checklist.md), whichever stage you are on. It
marks which items are stage 1 only.

---

## Releasing a new version

The same on every platform:

1. Build and push both images, tagged with the commit. On stage 0 the
   platforms do this from a push to `main`.
2. Run the migration with the new API image and wait for it to succeed.
   Replicas of the previous release keep serving while it runs, so check a
   release's new migrations for anything the old code would trip over (a
   dropped column, a new `NOT NULL` without a default) and split those
   across two releases.
3. Roll out the API. Replicas drain for 30 seconds. Launches in flight on a
   replica that stops are covered by a 5-minute lease and are picked up
   again by another replica.
4. Roll out the dashboard, if it changed.

To roll back, deploy the previous images. Undo a migration only if the
release notes say the previous version cannot run against the new schema
(`migrate down` reverts one migration at a time).
