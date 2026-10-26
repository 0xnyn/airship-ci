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
| Runner definitions can live in the repository, as Cirun does it. | Cirun's `.cirun.yml` model (fallback lists, `--suffix` labels, idle pools, an org `.cirun` repository with access policies) is a better fit than dashboard profiles for teams that review infrastructure in pull requests. We kept its format and changed what it trusts; see [Runner config files](#runner-config-files). |
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

- **Capacity.** A claim locks the data plane row, then counts active
  runners against the data plane's `max_runners` and each profile's, and
  picks pending runners with `FOR UPDATE SKIP LOCKED`. A test starts 40
  concurrent claims against a limit of 3; without the lock it launches 24.
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
- **Plans and retries.** When a job is first seen queued, Airship stores
  its plan: the attempts that could serve it, in fallback order (a profile
  is a one-attempt plan). The Nth failed launch moves to attempt N, so a
  capacity error on one instance type tries the next. A job gets at most
  3 failed launches, or one per attempt if the plan is longer; failures the
  agent marks as not retryable stop after every attempt was tried once.
  Each runner keeps a copy of the machine it was created for, so editing a
  profile or a config file never changes a runner in flight. When GitHub
  rate-limits runner registration, the runner is not claimed again until
  its `Retry-After` has passed.
- **Cleanup order.** A finished runner is removed from GitHub after 2 minutes,
  and only if GitHub says it is not busy.

## Runner config files

`internal/runnerconfig` parses `.airship.yml`, `.airship.global.yml` and
`.access.yml` (pure functions, no I/O) and turns a job's labels into
attempts. `internal/fleet/config.go` decides what a job gets:

1. Dashboard profiles match first. Otherwise only jobs with an `airship-`
   label go further, so other jobs cost no API calls.
2. The repository must be activated (a `repositories` row an admin
   created), and a public one must have been activated with public access
   allowed.
3. The run attempt is fetched to learn the event, who started it, and
   whether its code comes from a fork. The file is read at the job's
   commit, or on the default branch for a fork. Reads at a commit are
   cached until evicted; branch reads for a minute (and dropped on push).
4. One-line labels (`airship-aws-c7i-xlarge`) that no file defines become
   runners of their own, except for forks, which can edit `runs-on`.
   Organization runners from the `.airship` repository are added after
   the repository's own, then filtered by `.access.yml`, checking team
   membership and repository role with GitHub only when a policy needs it.
   A broken `.access.yml` closes every organization runner.
5. Each attempt becomes one plan entry per connected data plane whose agent
   reported a matching provider and region.

The plan, and the reason when there is none, is stored on the job. The
dashboard shows the reason, the stale-job reconciler reuses the plan, and
a later config change cannot reroute a job that is already waiting.

**Warm pools** are `idle_pools` rows, rebuilt from the default branch's file
when a push webhook marks the repository stale (and hourly regardless).
The reconciler keeps each pool at the size its schedule wants now, holding
the pool row lock so two passes cannot both fill one gap. Pool runners have
no trigger job and are exempt from the idle reaper. A queued job whose
labels a pool's runners carry reserves one (sets its trigger job) instead
of creating a runner, and the pool refills at once. GitHub may still give
the job a different idle pool runner; the reserved one is then returned to
the pool rather than left to time out. Trimming a pool drops pending
runners outright and detaches running ones before deregistering them, so a
job cannot reserve a runner that is being removed. Pools are repository
scoped on purpose: a runner registered on the organization could be taken
by repositories that were never activated or that `.access.yml` excludes.

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
- **Developer API.** `asa_` bearer tokens, stored as SHA-256, scoped to one
  installation, created and revoked only by admins. The `/v1/api` routes
  accept no session cookie, so a page in a signed-in browser cannot drive
  them. Tokens stop working when the installation is suspended or removed.
- **Webhooks.** `X-Hub-Signature-256` is verified in constant time before
  parsing. Bodies are capped at 25 MB.
- **Boot script.** Everything interpolated into the instance's user data is
  validated against a strict pattern first, since it runs as root. Job
  environment variables from config files are written to the runner's
  `.env` as base64 that is decoded on the machine, so no value is ever
  shell; the agent rechecks names and line breaks itself.
- **Config files.** A config file only acts in a repository an admin
  activated. Fork pull requests read the default branch's file and cannot
  use one-line labels, so outside code cannot pick machines or set job
  variables. Variables cannot use the
  runner's own prefixes (`GITHUB_`, `RUNNER_`, `ACTIONS_`). Privileged
  containers need the Docker host's operator to opt in on the agent. Anyone
  who can push a branch to an activated repository can pick the machines for
  that branch's jobs, as with any Actions workflow; the data plane's
  `max_runners` bounds what that can cost. Files
  are capped at 64 KiB and parsed strictly, with YAML alias expansion
  bounded.
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
- **Cache is opt-in and unscoped.** The stack's cache bucket is shared by
  every job on the data plane, with no per-branch isolation. We do not run
  an actions/cache protocol proxy on runners (runs-on and Cirun's automatic
  mode do); jobs use an S3 cache action instead.
- **Wake-ups are per process.** Long polls are woken in-process and otherwise
  recheck every 2 seconds. Postgres `LISTEN/NOTIFY` would make this instant
  across replicas.
- **Linux only.** No Windows or macOS runners.
- **Config files cover AWS and Docker only.** Other Cirun clouds, GPUs on
  Docker and `provision_via` are refused; see
  [the differences from Cirun](runner-config.md#differences-from-cirun).
- **Developer API tokens are organization-wide.** A token can activate any
  repository the app can reach; there are no per-repository scopes.
- **No organization warm pools.** See [Runner config files](#runner-config-files).
- **Config reads cost API calls.** Each queued config job reads its run
  attempt and the file at its commit (cached per run and commit), and the
  organization files at most once a minute per replica.
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
