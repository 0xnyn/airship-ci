# Architecture

Airship runs GitHub Actions jobs on ephemeral EC2 instances in the
customer's AWS account. It holds the customer's access key (sealed), reads
`.cirun.yml` from activated repositories, and launches, watches and
terminates instances itself. This is Cirun's model; the first version of
this project ran an agent in the customer's account instead, and the
differences below are the result of moving to Cirun's.

```
                 webhooks (workflow_job, push, installation, repository)
   GitHub  ───────────────────────────────────────────►  Control plane
     ▲  ▲        REST: JIT configs, runner/job lookups,      ├─ api: webhooks, dashboard API, developer API, runner API
     │  │        file reads, redelivery, PRs on .cirun       ├─ launcher (every replica)
     │  └────────────────────────────────────────────────────┤─ reconciler (one leader via advisory lock)
     │                                                        ├─ Postgres: the only state
     │ runner registers with its JIT config                   └─ Next.js dashboard
     │ and runs exactly one job                                      ▲   │ RunInstances / DescribeInstances /
     │                                                 cache grants  │   │ TerminateInstances with the
  ┌──┴──────────────── customer account ─────────────────────────────┴───▼──────── customer's access key
  │  EC2 instance (one per job, self-destructs)        S3 bucket (cache)     │
  └──────────────────────────────────────────────────────────────────────────┘
```

## Components

All Go code lives in `apps/api`.

- `cmd/api` serves the GitHub webhook (`internal/webhook`), the dashboard
  API (`internal/auth`, `internal/console`), the Cirun-compatible developer
  API (`internal/console/apitokens.go`) and the runner API
  (`internal/runnerapi`). It also runs the launcher, the reconciler and the
  webhook redeliverer.
- `internal/fleet` owns the runner lifecycle. Everything that changes
  runner state goes through it.
- `internal/cloud` is the provider interface; `cloud/aws` is EC2 and S3;
  `cloud/bootscript` renders the Linux and Windows boot scripts;
  `cloud/fake` is what tests launch on.
- `internal/runnerconfig` parses `.cirun.yml`, `.cirun.global.yml` and
  `.access.yml` (pure, no I/O).
- `internal/secrets` seals cloud credentials under `CREDENTIALS_KEY`.
- `cmd/cacheproxy` and `internal/cacheproxy` run on runner machines; see
  [caching.md](caching.md).
- `internal/github` is a small GitHub App client.

## Runner lifecycle

```
pending ──claim──► launching ──RunInstances ok──► running ──job starts──► busy
   │        (lease 5m,             (instance_id             (in_progress        │
   │         JIT minted)            recorded)                webhook)            │
   └───────────────────────────────────────────────────────────────────────────┴──► finished
```

`finished` records why: `completed`, `cancelled` (the job finished before a
runner was needed), `idle` (online 10 minutes without a job),
`launch_failed`, `lease_expired` (a launcher claimed it and died),
`lost` (its instance vanished and GitHub never said the job ended),
`max_lifetime` (8 hours), or `deleted` (the app was uninstalled).

Every transition is a conditional `UPDATE ... WHERE state = <expected>`, so
concurrent webhooks, launcher passes and reconciler passes cannot apply a
transition twice or undo each other.

- **Planning.** When a job is first seen queued, its plan is stored: the
  attempts that could serve it, in fallback order. Each attempt is a
  definition, one of its machine shapes, the installation's credential for
  that cloud, and the region (the shape's, or the credential's default).
  The Nth failed launch moves to attempt N; a job gets at most 3 failed
  launches, or one per attempt if the plan is longer. Failures the cloud
  marks as not retryable (a missing AMI, an unsupported type) stop once
  every attempt was tried. Editing a file never changes a job already
  waiting.
- **Capacity.** Each installation has `max_runners`. A claim locks the
  installation row, counts live runners and takes pending ones with
  `FOR UPDATE SKIP LOCKED`, so launchers on several replicas never exceed
  it.
- **Launching.** The launcher mints a JIT config (a single-use, repository
  scoped runner registration carrying the labels), renders the boot script
  with it, and calls `RunInstances` with a `ClientToken` derived from the
  runner ID, so a retried call returns the same instance. The instance
  launches with `InstanceInitiatedShutdownBehavior=terminate`, IMDSv2
  required with hop limit 1, an encrypted root volume, no inbound security
  group rules, and a hard `shutdown` deadline; every exit path of the boot
  script powers it off.
- **Inventory.** The reconciler lists instances per (credential, region)
  by the `airship:installation` tag and compares them with the runners
  table: instances for finished or unknown runners are terminated, runners
  whose instance disappeared are `lost` (and their job retried), and a
  runner whose lease expired but whose instance did launch is revived.
  Listing errors leave state alone: an instance missing from a failed
  list is never presumed gone.
- **Termination.** A finished runner's instance is terminated at once when
  the completed webhook arrives, and again by the reconciler if the cloud
  still lists it. Deleting from GitHub happens 2 minutes later, only if
  GitHub says the runner is not busy.
- **Lost webhooks.** GitHub never redelivers failed webhooks; one replica
  scans the app's delivery log every two minutes and asks for redelivery
  with backoff. The reconciler also asks GitHub about jobs queued for 10
  minutes without news, two per installation per pass.
- **Idle runners** are `idle_pools`, rebuilt from the default branch's
  file on push (and hourly). Pool runners register with the definition's
  plain labels and have no trigger job; a queued job whose labels a pool's
  runners carry reserves one instead of creating a runner, and the pool
  refills at once. Usage-based pools stay warm for `duration` after one of
  their definition's runners starts a job.

## What a job reads

1. Only jobs with a `cirun-` label cost anything; others are ignored.
2. The repository must be activated (public ones with public access
   allowed): a config file never launches anything in a repository nobody
   activated.
3. The run attempt is fetched to learn who started it and whether the code
   is from a fork. `.cirun.yml` is read at the job's commit, or on the
   default branch for a fork.
4. One-line labels that no definition covers become runners of their own,
   except for forks. Organization runners from `.cirun/.cirun.global.yml`
   are tried after the repository's and filtered by `.access.yml`; a broken
   `.access.yml` closes every organization runner.
5. Each attempt needs the installation's connected cloud; without one the
   job is skipped with `no_cloud` and retried when a cloud is connected.

## Security model

- **Credentials.** Access keys are verified with STS before they are stored,
  sealed with AES-256-GCM under `CREDENTIALS_KEY`, opened only for the
  duration of a cloud call, and never returned by any endpoint. Disconnecting
  a cloud is refused while runners launched with it are alive, because
  without the key their instances could not be terminated.
- **Tenancy.** Every query is scoped by GitHub installation ID; instances
  are tagged and listed by it.
- **Dashboard sessions.** GitHub OAuth, HttpOnly `SameSite=Lax` cookies,
  an Origin check on every write. Organization owners are admins, members
  read-only, nobody else gets in; roles are re-checked hourly.
- **Developer API.** `asa_` bearer tokens, stored hashed, scoped to one
  installation. The `/api/v1` routes accept no session cookie.
- **Runner API.** `asr_` cache tokens, one per cache-enabled runner, dead
  once the runner finishes; they buy credentials for that repository's
  prefix in that installation's bucket and nothing else.
- **Boot script.** Everything interpolated is validated against a strict
  pattern first; job variables travel as base64 and are decoded on the
  machine.
- **Config files.** A file only acts in an activated repository. Fork pull
  requests read the default branch's file and cannot use one-line labels,
  so outside code cannot pick machines or set job variables. Anyone who can
  push to an activated repository can pick machines for that branch's
  jobs, as with any workflow; `max_runners` bounds what that can cost.
- **Images.** Any AMI the account can launch is allowed, as on Cirun. The
  default is Canonical's current Ubuntu 22.04.

## Differences from Cirun

Kept: the file format and names, fallback and suffix rules, idle runners,
organization runners with `.access.yml`, one-line labels, `provision_via`,
the developer API, Windows fields, S3-backed caching.

Different, on purpose:

- **Clouds.** AWS only. `gcp`, `azure`, `do`, `openstack`, `oracle`,
  `vast_ai` and `on_prem` are refused with "not supported yet" rather than
  ignored. Each is a provider implementation; none can be trusted unrun.
- **Spot fallback.** A spot shortage fails the attempt and moves to the
  next entry of the lists, as the docs describe. There is no silent
  on-demand fallback inside an attempt.
- **Idle runners in `.cirun.global.yml`** are refused. An organization
  pool would have to register on the organization, where any repository
  could take it, activated or not. Define them per repository.
- **One-line labels and forks.** Fork pull requests cannot use them.
- **Cache prefix.** Each repository's cache lives under its own prefix in
  the bucket, and runner credentials are scoped to it. On Cirun one bucket
  is shared across the customer's repositories.
- **Credential model.** Only access keys today, as Cirun documents. The
  provider interface would take an assumed role.
- **`SubnetId`, `SecurityGroupIds`, `IamInstanceProfile`** in
  `extra_config` are Airship additions (Cirun passes unknown AWS keys
  through to the API; these are the ones that matter).

## Known limitations

- **Untested against a live runner.** The EC2 provider runs against moto in
  CI, and every protocol in the cache proxy runs against fakes. Nothing
  here has launched a real instance or run a real `actions/cache` step yet.
- **JIT config in user data.** Anyone in the account who can call
  `ec2:DescribeInstanceAttribute` can read it until the instance registers.
  `provision_via: pull_from_api` avoids it, at the cost of an API token in
  the image.
- **Windows.** The boot script is written to the documented
  `run_as`/`runner_user`/`runner_path` behaviour and not run.
- **Cache interception.** Relies on the runner honouring `https_proxy` from
  `.env` (documented by GitHub) and on the results host staying
  `results-receiver.actions.githubusercontent.com`.
- **One credential per cloud per installation.** Cirun has the same.
