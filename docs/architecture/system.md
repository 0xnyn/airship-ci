# Airship: system architecture

**What this is.** Airship runs GitHub Actions jobs on ephemeral virtual
machines inside *your* cloud account, or on ephemeral sandboxes in your
Daytona organization. One job, one machine, destroyed when the job ends. You connect a cloud credential once, commit a `.cirun.yml`,
and write `runs-on: cirun-<label>`.

**Audience.** Engineers and architects evaluating Airship. Each document in
the set reads on its own; this one is the map.

---

## The document set

| Document | Answers |
| --- | --- |
| **system.md** (this one) | How the whole thing fits together, what runs where, how a job becomes a machine, how it fails safely |
| [aws.md](aws.md) | What Airship does inside an AWS account, call by call |
| [gcp.md](gcp.md) | The same for Google Cloud, and where Compute Engine forced different decisions |
| [daytona.md](daytona.md) | The same for Daytona, where a job gets a sandbox rather than a VM, and what that changes |
| [aws-setup.md](aws-setup.md) | Standing it up on AWS: IAM, VPC, quotas, first green job |
| [gcp-setup.md](gcp-setup.md) | Standing it up on Google Cloud: APIs, service account, roles, quotas |
| [daytona-setup.md](daytona-setup.md) | Standing it up on Daytona: an API key, a region, a tier, first green job |
| [security.md](security.md) | Trust boundaries, credential handling, job isolation, and the exposures we state plainly |
| [operations.md](operations.md) | Running the control plane: deployment, HA, scaling, key rotation, incident runbook |

Reference material that sits underneath this set: [`../cirun-yml.md`](../cirun-yml.md)
(the config file format), [`../caching.md`](../caching.md) (the cache
accelerator), [`../architecture.md`](../architecture.md) (the internal
engineering notes, including every lifecycle invariant in full).

---

## The shape of the system

There are two planes, and the line between them is the only trust boundary
that matters.

```
                     ┌─────────────────────── GitHub ───────────────────────┐
                     │  webhooks   workflow_job · push · installation ·     │
                     │             installation_repositories · repository   │
                     │  REST       JIT configs · job and runner lookups ·   │
                     │             file reads · delivery redelivery · PRs   │
                     └────▲──────────────────────────────────┬──────────────┘
                          │                                  │
   runner registers with  │                                  │  events in
   its single-use JIT     │                                  ▼
   config, runs one job   │     ┌────────── Airship control plane ──────────┐
                          │     │  api         webhook receiver             │
                          │     │              dashboard API                │
                          │     │              developer API (/api/v1)      │
                          │     │              runner API (binary + grants) │
                          │     │  launcher    on every replica             │
                          │     │  reconciler  one leader, advisory lock    │
                          │     │  Postgres    the only state               │
                          │     │  dashboard   Next.js                      │
                          │     └───────────────────┬───────────────────────┘
                          │                         │  RunInstances / instances.insert
                          │                         │  Describe / List
                          │                         │  Terminate / Delete
┌─────────── your cloud account ──────────────────── ▼ ──────────────────────┐
│                                                                            │
│   one VM per job, self-destructing          cache bucket (S3, AWS only)    │
│   ┌────────────────────────────────┐        ┌───────────────────────────┐  │
│   │ actions/runner  — exactly 1 job│ ─────► │ airship-cache-<region>-…  │  │
│   │ cache proxy     — 127.0.0.1    │        │ 7-day lifecycle rule      │  │
│   │ boot script     — hard deadline│        └───────────────────────────┘  │
│   └────────────────────────────────┘                                       │
│                                                                            │
└────────────────────────────────────────────────────────────────────────────┘
```

| | Control plane | Data plane |
| --- | --- | --- |
| Runs where | Airship's deployment (or yours — one container plus Postgres) | Your cloud account, your VPC/VPC network; or your Daytona organization |
| Holds | Installation metadata, runner and job rows, sealed cloud credentials | Nothing durable. The machine is gone after one job |
| Talks to | GitHub, your cloud's control API, your runners | GitHub, your cache bucket, whatever your job reaches |
| Never does | Log in to your machines. There is no SSH key, no agent, no inbound port (on Daytona it writes one file and starts one command through the sandbox's toolbox API, and that is all) | Hold your cloud credential. It gets a scoped, hour-long cache grant at most |

Your code, your artifacts and your secrets stay in your account and in
GitHub. The control plane sees job *metadata* — repository, labels, job ID,
state — and never job content.

---

## Components

All of the control plane is one Go binary (`apps/api`), plus a Next.js
dashboard and a small proxy binary that ships inside the same image for
runner machines to download.

| Component | What it does | Where it runs |
| --- | --- | --- |
| **api** | The GitHub webhook receiver, the dashboard API, the Cirun-compatible developer API, the runner API (cache proxy binary and cache credentials) | Control plane, every replica |
| **launcher** | Claims pending runners under the installation's capacity cap, mints the GitHub registration, calls your cloud | Control plane, every replica |
| **reconciler** | Twelve sequential steps that make the database agree with GitHub and with your cloud | Control plane, one leader at a time |
| **redeliverer** | GitHub never retries a failed webhook; this scans the app's delivery log every two minutes and asks for redelivery with backoff | Control plane, one replica |
| **role refresher** | Re-checks dashboard roles against GitHub, so removing someone from your org revokes their access within the hour | Control plane |
| **Postgres** | The only state in the system. 13 tables | Control plane |
| **dashboard** | Repositories, Cloud, Runners, Jobs, API keys, Teams, Analytics, Events, Settings | Next.js, served next to the API |
| **boot script** | Rendered per launch; installs `actions/runner`, registers it with a single-use config, runs one job, powers the machine off (stops the sandbox, on Daytona) | Your VM, as user data / startup script; your Daytona sandbox, started by the control plane |
| **cache proxy** | Serves GitHub's cache API from an S3 bucket in your account, on `127.0.0.1` only | Your VM, when `extra_config.cache: true` |

The provider boundary is deliberate: `internal/cloud` is an interface with
four methods (`Verify`, `Launch`, `List`, `Terminate`) plus an optional
`Cacher`. The scheduler cannot import a cloud SDK. A new cloud is a new
implementation of that interface and nothing else; Daytona, whose
"machines" are containers that run nothing at boot, fit behind it without
a change to the scheduler.

---

## The life of a job

A typical timeline, from GitHub's webhook to a terminated machine:

```
 0.0s   GitHub           workflow_job: queued, labels [cirun-build--1234]
 0.0s   webhook          signature checked, installation resolved
 0.1s   plan             read .cirun.yml at the job's commit; build the
                         fallback chain (machine shapes × regions × spot)
 0.1s   runners row      state=pending, spec frozen, labels recorded
 0.1s   launcher         claim under max_runners (row lock on installation)
                         → state=launching, 5-minute lease
 0.3s   GitHub           mint JIT config (single-use, repo-scoped, labelled)
                         store the GitHub runner ID *before* calling the cloud
 0.4s   re-check         is the job still queued? cancelled or taken → no machine
 0.5s   your cloud       RunInstances / instances.insert, idempotency key
                         derived from the runner ID
 2.0s   runners row      state=running, instance_id recorded
~40s    your VM          boot script: install runner, trust cache CA,
                         write .env, start ./run.sh with the JIT config
~50s    GitHub           workflow_job: in_progress → state=busy
  …     job runs
  —     GitHub           workflow_job: completed → state=finished (completed)
  +0s   your cloud       TerminateInstances / instances.delete
  +0s   your VM          the runner exits, the boot script's trap powers off,
                         and the machine terminates on shutdown anyway
 +2m    GitHub           delete the runner registration (only if not busy)
```

Two things in that sequence are load-bearing:

**A runner is not bound to the job that created it.** GitHub hands any
queued job to any idle runner with matching labels. So Airship creates one
runner per queued job and learns which job it *actually* ran from the
`in_progress` event. Every autoscaler that assumes otherwise double-books
or leaks machines.

**Registration precedes the machine.** The JIT config is minted and its
GitHub runner ID stored before the cloud is called. Nothing can boot
without a stored registration to clean up, and a registration whose machine
never arrived is deleted two minutes later — unless GitHub says it is busy,
in which case the machine exists after all and the runner is revived.

---

## Runner states

```
   pending ──claim──► launching ──launch ok──► running ──job starts──► busy
      │          lease 5m,           instance_id        in_progress      │
      │          JIT minted          recorded           webhook          │
      └──────────────────────────────────────────────────────────────────┴──► finished
```

`finished` always records why:

| Reason | Meaning | Machine |
| --- | --- | --- |
| `completed` | The job ended, as GitHub reported it | Terminated at once |
| `cancelled` | The job went away before a machine was needed | Never created |
| `idle` | Online 10 minutes without taking a job | Terminated |
| `launch_failed` | The cloud refused the launch | Never created |
| `launch_unconfirmed` | The cloud's answer was lost; a machine may exist | Settled by the next inventory scan |
| `lease_expired` | A launcher claimed the runner and died mid-launch | Settled by the next inventory scan |
| `lost` | The instance vanished and GitHub never said the job ended | Gone |
| `max_lifetime` | Hit the 8-hour ceiling, or a pool runner was retired at half of it | Terminated |
| `deleted` | The GitHub App was uninstalled | Terminated |

Every transition is a conditional `UPDATE ... WHERE state = <expected>`.
Concurrent webhooks, launcher passes on different replicas and reconciler
passes can race freely: the loser's update matches no rows. There is no
read-then-write anywhere in the runner lifecycle.

---

## What keeps this honest

Cloud calls are at-least-once and their answers can be lost. The design
rests on rules, not on any single call succeeding exactly once.

1. **The database is the only state, and each runner row says what is known
   about its machine.** `instance_id` is set once the cloud confirmed a
   launch; `instance_gone_at` once the cloud listed the region without it,
   after a grace period. A runner whose machine is genuinely unknown has
   neither, and only the inventory scan may decide.
2. **A launch is idempotent per runner.** AWS gets a `ClientToken` and GCP a
   `requestId` derived from the runner ID; GCP instances are named after the
   runner and looked up by name before every insert. A retried call returns
   the first machine. It never makes a second.
3. **One machine serves at most one job, and a machine Airship did not make
   is never touched.** Instances carry the installation as a tag (AWS) or
   label (GCP). Listing filters by it; terminating reads the instance first
   and refuses when the tag is not the installation's.
4. **Registration precedes the machine, and is bounded.** See above.
5. **Ambiguity is resolved by observation, not assumption.** A transport
   error after the request was sent, a lost poll, a timed-out operation —
   each is settled by looking the machine up. Only when that also fails is
   the launch `launch_unconfirmed`, and such a runner keeps its registration
   and is revived the moment its machine is listed. GitHub's word counts as
   an observation too: an `in_progress` event naming a runner Airship gave
   up on revives it as `busy` on the spot.
6. **Nothing is retried that GitHub would not run again.** GitHub re-queues
   a job only while no runner has picked it up. A job whose runner dies
   mid-run fails and is not retried, so Airship retries only jobs still
   `queued`.
7. **Every machine has its own deadline.** The boot script powers the
   machine off when the runner exits and at the maximum lifetime, and runs
   once per boot. A control plane that disappears entirely leaves no machine
   running forever.

The full argument for each, including the edge cases they were written for,
is in [`../architecture.md`](../architecture.md).

---

## Cost and capacity controls

| Control | Default | Effect |
| --- | --- | --- |
| `max_runners` per installation | 50 (1–10,000) | Hard ceiling on live runners across every repository. Enforced under a row lock on the installation, so launchers on several replicas cannot both take the last slot |
| `RUNNER_MAX_LIFETIME` | 8h | The machine shuts itself off at this point whether or not the control plane is reachable. GitHub's default job timeout is 6h, so this leaves room |
| `RUNNER_IDLE_TIMEOUT` | 10m | A machine that boots, registers and never gets a job is reaped |
| Failed launches per job | 3 (or one per fallback attempt, whichever is more) | A broken config cannot spin forever |
| Idle pool backoff | 10m after 3 failures | A pool whose launches keep failing stops retrying |
| One VM per job | always | No machine is reused, so no job inherits another's state — and no machine lingers between jobs unless you asked for a pool |

Nothing in a `.cirun.yml` can exceed `max_runners`. That is the number to
set before you hand the file to a team.

---

## Idle pools

Cold-start is a VM boot plus a runner install — tens of seconds. `idle:`
keeps runners booted and registered ahead of demand, in three forms:

```yaml
idle: {replicas: 2}                     # always two warm

idle:                                   # overlapping windows take the most
  schedule:
    - {day: mon-fri, time: 9-17, timezone: Europe/Berlin, replicas: 4}
    - {day: sat-sun, time: 0-24, replicas: 1}

idle:                                   # warm for a while after demand
  usage_based: {trigger: runner_allocated, replicas: 2, duration: 30}
```

A pool runner that takes a job is replaced straight away — it is still one
machine per job. A pool runner that has waited half the maximum lifetime
(4 hours by default) is retired and replaced, so a job that lands on it
still has the other half. Pools count toward `max_runners`.

Pools are read from the **default branch** only, since they exist before
any job does; a push to the default branch rebuilds them within a reconcile
pass. Organization-wide pools live in a `.cirun/.cirun.global.yml`
repository and register on the organization in a runner group of their own,
visible to the repositories their `.access.yml` policies allow.

---

## The configuration model

| File | Where | Read at | Governs |
| --- | --- | --- | --- |
| `.cirun.yml` | each activated repository, root | the job's own commit | machine shapes, regions, disks, env, cache, idle pools |
| `.cirun.global.yml` | the `.cirun` repository | default branch | organization-wide runners and pools |
| `.access.yml` | the `.cirun` repository | default branch | who may use which organization runner |

Three rules shape what a file can do:

- **Only labels starting with `cirun-` are looked up.** Jobs on other labels
  cost no API calls and no money.
- **A file only acts in an activated repository.** Activation is an explicit
  admin action per repository; public repositories need a second, separate
  choice (`allow_public`), which is where fork pull requests are gated.
- **A job reads the file at its own commit**, so a branch can try a new
  machine type before it is merged — and a job is planned once, when GitHub
  first reports it queued. Editing the file afterwards does not change how a
  waiting job is served.

Fallbacks are zipped lists: `instance_type`, `machine_image`, `region` and
`preemptible` each take a value or a list, and attempt *N* uses the *N*th
entry of each. Two runners sharing a label are one fallback chain in file
order. Capacity failures start the chain over; a missing image or an
unavailable instance type stops once every attempt has been tried.

Full reference: [`../cirun-yml.md`](../cirun-yml.md).

---

## Multi-tenancy and scale

- **The tenant is the GitHub App installation** — one organization or user
  account. Every query is scoped by installation ID. Instances are tagged
  and listed by it, so two installations can share one cloud account without
  seeing each other's machines.
- **Replicas are horizontal.** Launchers on every replica claim work with
  row locks (`FOR UPDATE SKIP LOCKED`); the reconciler elects one leader per
  pass through a Postgres advisory lock. Losing the leader costs one pass.
- **GitHub rate limits are respected structurally.** An installation has
  5,000 REST calls an hour. The reconciler asks GitHub about at most two
  stale jobs per installation per pass, so one installation's backlog cannot
  exhaust its own budget or delay another's recovery, and launch concurrency
  is capped at 8 to avoid GitHub's secondary limits on POST bursts.
- **Cloud calls are bounded.** Every call has a timeout; a launch is cut off
  30 seconds before its lease expires so a runner is never given up while
  its launch is still in flight.

| Loop | Interval | Leader only |
| --- | --- | --- |
| launcher | 2s, plus woken immediately by a webhook | no |
| reconciler (12 steps) | 15s | yes |
| webhook redeliverer | 2m | one replica |
| role refresher | 1m, re-checking roles older than 1h | no |

---

## Failure model

| What fails | What happens |
| --- | --- |
| A webhook is lost | The redeliverer asks GitHub to send it again; the reconciler also asks GitHub directly about jobs queued 10 minutes without news |
| The control plane dies mid-launch | The lease expires after 5 minutes; the job is retried, and if the machine did launch, the inventory scan revives the runner and the duplicate idles out |
| The control plane is down entirely | No new runners. Running jobs finish. Every machine still powers itself off at its own deadline — nothing runs forever |
| The cloud's answer to a launch is lost | The launch is `launch_unconfirmed`; the machine is found (or not) by the next inventory scan, and the job is retried meanwhile |
| An instance disappears | The runner is `lost` and its job retried if it was still queued; a busy runner's job ends as GitHub reports it |
| A machine is stopped by hand | It runs no job and still costs its disk, so the scan treats it as gone and removes it |
| Your credential is revoked | Launches fail with a credential error on the dashboard's Events page; existing machines still hit their own deadlines |
| `.cirun.yml` is broken | The job is skipped with a reason that names the field. Nothing launches |
| A region is out of capacity | The next fallback attempt, then the next subnet/zone, then the chain again — up to the attempt cap |
| Postgres is lost | This is the one unsurvivable failure. See [operations.md](operations.md) for backup and what `CREDENTIALS_KEY` loss means |

---

## Interfaces

| Surface | Auth | For |
| --- | --- | --- |
| Dashboard API `/v1/...` | GitHub OAuth session cookie, HttpOnly, `SameSite=Lax`, Origin-checked on writes | The web dashboard |
| Developer API `/api/v1/...` | `asa_` bearer token, stored hashed, scoped to one installation. Accepts no session cookie | Automation; wire-compatible with [cirun-py](https://github.com/AktechLabs/cirun-py) |
| Runner API `/v1/runner/...`, `/v1/assets/...` | `asr_` token, one per cache-enabled runner, dead when the runner finishes | Runner machines fetching the cache proxy and its credentials |
| Webhook `/v1/github/webhook` | HMAC signature | GitHub |

The Go API is the source of truth for every type on the wire. The
TypeScript client is generated from its OpenAPI document and never edited
by hand:

```
SQL ──sqlc──► Go structs ──Huma──► openapi.json ──openapi-typescript──► @airship/client ──► Next.js
```

CI fails if any generated file is stale.

---

## What has actually run

We are precise about this, because "it compiles" is not "it works".

| Exercised | How |
| --- | --- |
| **A real GitHub job on a real runner, Linux** | The rendered boot script, unmodified, in a fresh `ubuntu:22.04` container: installed packages, fetched the cache proxy, obtained a grant, trusted the CA, wrote the runner's `.env`, started `actions/runner` 2.338.2 with a single-use JIT registration and ran one `workflow_dispatch` job on a private repository. `actions/cache/save` wrote 90 MB as a multipart upload; `restore` hit by exact key and by `restore-keys` prefix with matching checksums; unknown keys missed. Only `shutdown` was stubbed, because a container cannot power itself off |
| **The EC2 provider, cache bucket and federation grant** | Against moto in CI: request shapes, tags, default VPC and security group, lifecycle rules, termination |
| **The Compute Engine provider** | Through the real Google SDK against a hand-written fake of the REST API: request IDs, zone operations, per-zone lists with unreachable zones, capacity and quota errors, dropped connections, hung operations, image families, machine images, subnetworks, routers |
| **Three real GitHub jobs on real Daytona sandboxes: image with Docker, snapshot, volume** | Through `Provider.Launch` against `app.daytona.io` on 2026-10-27, with a JIT config minted on a private repository and `docker: true`: the sandbox was built from `ubuntu:22.04`, started, scripted and labelled in 42 s; a second `Launch` returned the same sandbox; the runner was online on GitHub 85 s in; a `workflow_dispatch` job ran every step green (`uname`, `actions/checkout@v4`, `docker run --rm alpine:3.20` on the Docker 28.3.3 daemon the script started in the sandbox, `sudo`); the runner deregistered after its one job, the script stopped the sandbox, Daytona deleted it 2 m 8 s in, and the organization listed nothing afterwards. Two more jobs ran green on a sandbox from the `daytona-medium` snapshot (3 s to launch, running as a sudoer) and on one with a Daytona volume mounted at `/mnt/probe` (`mountpoint-s3`, writable). Allow lists, GPUs and the dedicated region were sent and refused by Daytona for this organization (tier, credits, region), each a clear permanent error. The failure modes ran against Daytona too: five launches of one runner racing got one sandbox (the rest a retryable organization-quota error), a launch cancelled while its sandbox was `creating` left nothing behind, a sandbox orphaned before its label was hidden inside the boot grace, reported gone past it and adopted by a relaunch, and `Terminate` on a runner mid-job returned in 500 ms, had the sandbox gone within a minute and was a no-op when repeated. The opt-in `TestAgainstLiveDaytona` separately verified the key, a repeated launch, listing, the ownership refusal and the self-destruct path. In CI the provider runs through Daytona's generated clients against a fake of its API and toolbox |
| **Boot scripts** | Linux checked with `bash -n`; both Windows scripts parsed by PowerShell 7.4.6 itself |
| **Races and lifecycle edge cases** | `internal/fleet` tests against real Postgres, a fake GitHub and a fake cloud; `internal/e2e` drives the whole HTTP surface |

| Not yet exercised | Consequence |
| --- | --- |
| A launch on real EC2 | The opt-in `TestAgainstLiveAWS` launches and terminates one instance when credentials are set. The first real deployment exercises exactly the `RunInstances` call moto accepted |
| A launch in a real GCP project | The GCP provider should be treated as unexercised against Google, however thorough the fake. The opt-in `TestAgainstLiveGCP` launches one `e2-micro` and deletes it. See [gcp.md](gcp.md) for the specific things to watch on a first launch |
| A Windows machine | The scripts parse; no Windows VM has booted them |
| A GPU sandbox that started, a sandbox in a dedicated Daytona region, or a sandbox-level allow list | Every request was sent and refused for this organization (no GPU credits; a dedicated region of Windows hosts only, which connect now refuses up front; a tier below 3 for allow lists). Each needs a paid change to the organization. The provider's handling of a started GPU sandbox and an accepted allow list are the Daytona paths not yet seen. See [daytona.md](daytona.md) |

---

## Questions that come up on calls

**Does Airship have access to my cloud account?**
Yes — that is the model. You give it a credential scoped to launching and
destroying machines, it holds that credential sealed, and it opens it only
for the duration of one API call. [security.md](security.md) says exactly
what the credential needs and what it is used for; [aws-setup.md](aws-setup.md),
[gcp-setup.md](gcp-setup.md) and [daytona-setup.md](daytona-setup.md) give
the least-privilege policies.

**Can a developer run up a bill?**
Anyone who can push to an activated repository can choose machine types for
that branch's jobs — the same authority they already have over what a
workflow does. `max_runners` is the ceiling, the 8-hour lifetime is the
per-machine ceiling, and the Jobs and Analytics pages show what ran.

**Does my code touch Airship's infrastructure?**
No. Your code is checked out on your machine, in your account, by a runner
that talks to GitHub directly. The control plane sees job metadata only.

**What happens if Airship goes away mid-job?**
The job finishes — the runner talks to GitHub, not to us. The machine then
powers itself off at its own deadline. No orphan survives indefinitely.

**Can I run the control plane myself?**
Yes: one container plus Postgres. See [operations.md](operations.md).
