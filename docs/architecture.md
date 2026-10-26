# Architecture

Airship runs GitHub Actions jobs on ephemeral machines in the customer's own
cloud account. We operate the control plane: it receives GitHub webhooks,
decides which runners should exist, and talks to GitHub. The customer runs the
data plane: a small agent that starts and destroys machines in their account.
Their code, secrets, caches and build artifacts never pass through us.

```
                 webhooks (workflow_job, installation)
   GitHub  ───────────────────────────────────────────►  Control plane (we run it)
     ▲  ▲        REST: JIT configs, runner/job lookups,      ├─ api: webhooks, dashboard API, agent API
     │  │        deregistration, webhook redelivery          ├─ reconciler (one leader via advisory lock)
     │  └────────────────────────────────────────────────────┤─ Postgres: the only state
     │                                                        └─ Next.js dashboard
     │ runner registers with its JIT config                          ▲
     │ and runs exactly one job                                      │ POST /v1/agent/sync (long poll,
     │                                                               │ outbound HTTPS only)
  ┌──┴──────────────── customer account ─────────────────────────────┴───┐
  │  EC2 instance / container  ◄── launch, terminate ──  agent (Fargate  │
  │  (one per job, self-destructs)                       or Docker host) │
  └──────────────────────────────────────────────────────────────────────┘
```

## What we learned from prior art

We read the source of [ubicloud](https://github.com/ubicloud/ubicloud),
runs-on, and two smaller schedulers (zoomies, superci) before writing code.
The decisions below are where they agreed, or where one of them had a bug
the others avoided.

| Decision | Why |
| --- | --- |
| A runner is not bound to the job that triggered it. | GitHub assigns any queued job with matching labels to any idle runner. Ubicloud and runs-on both treat "one queued job ⇒ one runner" as demand, not as a binding. Binding them causes starvation when GitHub hands job A's runner to job B. We record the trigger job only for accounting and hand off the trigger when this happens. |
| Repository-scoped JIT runners in runner group 1. | JIT configs are single-use, carry their own labels, and need no long-lived registration token on the machine. Repository scope needs only `administration: write` on the repo, not org-wide admin. |
| Check `busy` before deleting a runner from GitHub. | Deleting a busy runner kills a job mid-step. Ubicloud learned this the hard way. |
| Job state only moves forward. | Webhooks arrive out of order and are redelivered. `queued → in_progress → completed` is enforced in SQL (`job_status_rank`), so a late `queued` never resurrects a finished job. |
| The agent only makes outbound calls. | Customers will not open inbound ports to a vendor. The agent long-polls the control plane; there is no callback channel. |
| Level-triggered sync. | Every sync carries the agent's full inventory and gets back the full set of actions. A lost response is repaired by the next sync rather than by a retry protocol. |
| Unknown inventory never authorizes a delete. | If the agent cannot list instances (cloud API error, first sync after restart) it says so, and the control plane treats every runner as still possibly alive. Treating "I don't know" as "nothing exists" would mark live runners lost and relaunch duplicates. |
| Machines destroy themselves. | Each instance gets a hard `shutdown` deadline and `InstanceInitiatedShutdownBehavior=terminate`, and the boot script powers off on every exit path. A control plane outage cannot leave customer machines running forever. |
| Tag-scoped IAM, no `iam:PassRole` by default. | The agent's role can only create and terminate instances tagged with its own data plane ID, in one VPC. Runners get no AWS credentials unless the customer opts in with an instance profile. |
| Public repositories are refused by default. | Anyone can open a pull request against a public repository and run arbitrary code on the customer's machines. A profile must opt in. |
| Redeliver failed webhooks ourselves. | GitHub never redelivers automatically. Every two minutes one API replica reads the app's delivery log down to the last delivery it saw, records failures in Postgres, and asks GitHub to resend them with exponential backoff until one succeeds. As a second net, the reconciler asks GitHub directly about jobs that have been queued for 10 minutes without news (at most two per installation per pass, so one backlog cannot eat an installation's rate limit). |

## Components

All Go code lives in `apps/api`.

- `cmd/api` serves three surfaces from one binary: the GitHub webhook
  (`internal/webhook`), the dashboard API (`internal/auth`,
  `internal/console`), and the agent API (`internal/agentapi`). It also runs
  the reconciler and the webhook redeliverer.
- `cmd/agent` is the data plane. `internal/agent` holds the sync loop;
  `internal/agent/aws` and `internal/agent/docker` are the providers.
- `internal/fleet` owns the runner lifecycle. Everything that changes
  runner state goes through it.
- `internal/github` is a small GitHub App client (JWT, installation token
  cache, the handful of REST calls we need, OAuth for sign-in).
- `internal/agentproto` is the wire format between agent and control plane.
  It is shared Go code, so both sides agree by construction.

## Runner lifecycle

```
pending ──claim──► assigned ──instance reported──► running ──job starts──► busy
   │      (JIT minted,        (agent lists it)           (in_progress         │
   │       lease 5m)                                       webhook)            │
   └──────────────────────────────────────────────────────────────────────────┴──► finished
```

`finished` records why: `completed`, `cancelled` (the job finished before a
runner was needed), `idle` (online 10 minutes without a job), `launch_failed`,
`lease_expired` (handed to an agent that never reported an instance),
`lost` (busy but the instance vanished and GitHub never said the job ended),
`max_lifetime` (8 hours), or `deleted`.

Every transition is a conditional `UPDATE ... WHERE state = <expected>`, so
concurrent webhooks, syncs and reconciler passes cannot apply a transition
twice or undo each other. A few cases needed specific handling:

- **Capacity.** `max_runners` is checked while holding a row lock on the
  profile, and pending runners are claimed with `FOR UPDATE SKIP LOCKED`. A
  test starts 40 concurrent claims against a limit of 3; without the lock it
  launches 24.
- **Late reports.** If an agent reports an instance after its runner's lease
  expired, and the runner has not been deregistered from GitHub yet, it is
  revived instead of orphaned (it may already be running a job). The retry
  runner queued for the same job is cancelled if it has not been claimed,
  so the profile stays within `max_runners`.
- **Profile deletion.** Deleting a profile locks it, checks it has no active
  runners, and deletes it in one transaction. Finished runners keep their
  history and are still removed from GitHub.
- **Lost responses.** The agent acknowledges every runner it receives in
  its next sync. A runner assigned before a sync arrived but not
  acknowledged was in a response that never reached the agent; it goes back
  to `pending` and is handed out again with a fresh registration, without
  costing a launch attempt.
- **Retries.** A job gets at most 3 failed launches. Retryable failures (no
  capacity, throttling) go back to `pending`; others fail the runner. When
  GitHub rate-limits runner registration, the runner is not claimed again
  until its `Retry-After` has passed.
- **Cleanup order.** A finished runner is removed from GitHub after 2 minutes,
  and only if GitHub says it is not busy.

## The agent protocol

`POST /v1/agent/sync` with `Authorization: Bearer asd_...`. The control plane
stores only a SHA-256 hash of the token.

The request carries the agent version and provider, every instance the agent
can see (by runner ID), whether that list is trustworthy, the runners it has
received in the last 15 minutes, launch failures since the last sync, how
many launches it will accept now, and how long it is willing to wait (up to
25s). The response lists runners to launch, each with
its JIT config, and instances to terminate.

While there is nothing to do, the control plane holds the request open. It
wakes early when a webhook creates demand or finishes a runner, and rechecks
every 2 seconds regardless, so wake-ups do not depend on the request landing
on the API replica that got the webhook.

## Security model

- **Tenancy.** Every query is scoped by GitHub installation ID. A data plane
  token resolves to one data plane in one installation; failure reports are
  checked against the data plane that owns the runner.
- **Dashboard sessions.** GitHub OAuth, HttpOnly `SameSite=Lax` cookies
  (Secure outside development), 14-day expiry, and an Origin check on every
  state-changing request. Roles come from GitHub: organization owners and the
  owner of a personal account are Airship admins, other active organization
  members are read-only, and nobody else gets in. GitHub also lists an
  installation to outside collaborators on a single repository; they are
  refused. Roles are re-checked with GitHub hourly, so removing someone from
  the organization revokes their access without waiting for their session
  to expire.
- **Webhooks.** `X-Hub-Signature-256` is verified in constant time before
  parsing. Bodies are capped at 25 MB.
- **Boot script.** Everything interpolated into the instance's user data is
  validated against a strict pattern first, since it runs as root.
- **Instances.** IMDSv2 required with hop limit 1, encrypted gp3 root
  volumes, egress-only security group.
- **Images.** The agent role can only launch AMIs owned by Canonical or the
  customer's own account (`ImageOwners` in the template widens this). CI
  publishes the agent image with its digest; customers should deploy
  `AgentImage` as `ghcr.io/...@sha256:...` so a moved tag cannot change
  what runs in their account.
- **Idempotent launches.** EC2's `ClientToken` is derived from the runner
  ID, subnet attempt and market, and the first subnet is chosen by hashing
  the runner ID, so an agent that retries a launch gets the same instance
  back rather than a second one.

## Known limitations

These are deliberate scope cuts, not oversights.

- **JIT config in user data.** On AWS, anyone in the customer's account who
  can call `ec2:DescribeInstanceAttribute` can read a runner's JIT config
  until the instance registers. The config is single-use and scoped to one
  repository; the fix is to fetch it from the control plane at boot with an
  instance identity document, which needs a second endpoint.
- **Default AMI.** The default is stock Ubuntu 24.04, which has no Docker and
  installs the runner at boot (about 30-60s). Customers who need `docker` in
  jobs, or faster starts, set a custom AMI per profile. Publishing a
  prebuilt image is the obvious next step.
- **One agent per data plane.** The control plane assumes a single agent sees
  the whole inventory. The CloudFormation template enforces one task.
- **No cache.** runs-on ships an S3-backed actions/cache server. We do not.
- **Wake-ups are per process.** Long polls are woken in-process and otherwise
  recheck every 2 seconds. Postgres `LISTEN/NOTIFY` would make this instant
  across replicas.
- **Linux only.** No Windows or macOS runners.
- **AWS is emulated in CI, not real.** Two checks stand in for a live
  account. The provider's real SDK calls run against moto, which checks
  request shapes, tags, filters and termination but ignores `ClientToken`
  (a unit test covers idempotency). The template is deployed into moto's
  CloudFormation, and the agent policy it produces is evaluated with
  [iam-simulate](https://github.com/cloud-copilot/iam-simulate) against
  every authorization the agent's calls need, per resource and condition
  key as AWS's Service Authorization Reference lists them, plus the
  attacks it must refuse. Neither emulator models IAM eventual consistency,
  service quotas, real spot capacity, or EC2 accepting the boot script, so
  the first real deployment is still the first end-to-end run. iam-simulate
  is AGPL; it is a test-only dependency and never shipped.
