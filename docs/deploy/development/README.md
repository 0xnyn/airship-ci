# Development — running Airship on your own machine

**What this is.** The path from a laptop that has never seen this
repository to a real GitHub Actions job running on a real cloud machine
that **your** copy of Airship launched. In order, with every value spelled
out.

**Audience.** Someone starting from zero. No prior knowledge of this
codebase, of Go, or of GitHub Actions runners is assumed. If you have used
this repository before, the summary is `make setup && make dev`.

**Time.** 20 minutes to a working dashboard. An hour or so to the real job.

---

## What Airship is, in one paragraph

GitHub Actions normally runs jobs on GitHub's own machines. Airship runs
them on **the customer's** cloud account instead: it watches for queued
jobs, launches a machine in their AWS, Google Cloud, Azure or Daytona
account, registers it with GitHub as a self-hosted runner, and terminates
it when the job is done. Airship holds the customer's cloud credential,
sealed, and acts through a GitHub App. So there are always three parties:
**GitHub**, **Airship's control plane** (this repository) and **a cloud
account**. Most of what feels complicated in this setup is just making all
three reachable from your laptop.

Read [architecture/system.md](../../architecture/system.md) when you want
the real picture. You do not need it to get started.

---

## Three levels

Each level adds one of those three parties. **Most work only needs level
1**, which needs no accounts at all.

| Level | You get | You need | Steps |
| --- | --- | --- | --- |
| **1. Dashboard and API** | The whole dashboard against a demo organization, the API and its docs, every test | Go, Node, pnpm, Docker | [1](1-prerequisites.md), [2](2-first-run.md), [3](3-env.md) |
| **2. Real GitHub** | Webhooks from a real organization, real GitHub sign-in | A throwaway GitHub organization, your own GitHub App, a tunnel | [4](4-github.md) |
| **3. Real machines** | A real job on a real EC2 instance (or GCP, Azure, Daytona) | A cloud account, and a few cents | [5](5-real-machines.md) |

Level 1 is not a mock. It is the real API, the real dashboard and the real
database, with a development sign-in standing in for GitHub and nothing to
launch into.

---

## The files here

| | |
| --- | --- |
| [1-prerequisites.md](1-prerequisites.md) | The four tools, per operating system, and what is deliberately **not** installed globally |
| [2-first-run.md](2-first-run.md) | Clone, `make setup`, `make dev`, what runs where, a tour of the dashboard, and **how to stop and wipe it all again** (`make down`) — the two local databases also stop separately, with different commands |
| [3-env.md](3-env.md) | **Every environment variable in every file**: what to put, where it comes from, and which ones to leave alone |
| [4-github.md](4-github.md) | A throwaway test organization, your own GitHub App, and a tunnel so webhooks reach your laptop |
| [5-real-machines.md](5-real-machines.md) | Connecting a cloud and running a real job, without a surprising bill |
| [6-making-changes.md](6-making-changes.md) | **The daily loop**: where code goes, the code generation chain, migrations, and what to run before you push — there is no CI, so this one matters |
| [7-tests.md](7-tests.md) | Every test suite and how to run it, from `make test` to the live cloud ones |
| [troubleshooting.md](troubleshooting.md) | Every symptom in one place |

Deploying Airship for real is a different folder:
[stage 0](../stage-0/) for a pilot, [stage 1](../stage-1/) for AWS.

---

## The shortest useful path

```bash
git clone <this repository> airship && cd airship
make setup     # checks tools, installs, creates env files, starts and migrates Postgres
make dev       # API on :8080, dashboard on :3000, marketing site on :4000
make down      # when you are done: stops every container it started
```

Open http://localhost:3000 and choose **Development sign-in**. You are an
admin of a demo organization called `acme`.

If `make setup` fails, it says which tool is missing;
[1-prerequisites.md](1-prerequisites.md) installs them. If something else
goes wrong, [troubleshooting.md](troubleshooting.md) is organised by
symptom.

---

## How to find your way around the code

| Path | What it is |
| --- | --- |
| `apps/api` | **The control plane.** Go. Everything that matters happens here: `internal/fleet` (the runner lifecycle), `internal/cloud` (one package per cloud), `internal/console` (the dashboard's API), `internal/webhook`, `migrations` |
| `apps/dash` | The dashboard. Next.js, React, Tailwind. Talks to the API with a generated, typed client |
| `apps/home` | The marketing site. Astro. All copy lives in `src/content/` |
| `apps/docs` | The public documentation. Mintlify and MDX |
| `packages/client` | `@airship/client`: **generated** from the API's OpenAPI document. Never edited by hand |
| `packages/ui` | Shared shadcn/ui components |
| `docs/` | These documents. `architecture/` is the design, `deploy/` is how to run it |
| `supabase/` | A local Postgres 17 matching the pilot's database ([stage-0/local.md](../stage-0/local.md)) |

The one rule to absorb before changing anything: **the Go API is the source
of truth for every type that crosses the network.** The TypeScript client is
generated from it. [6-making-changes.md](6-making-changes.md) explains what
that means in practice.
