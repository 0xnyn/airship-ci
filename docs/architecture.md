# Architecture

Airship runs GitHub Actions jobs on ephemeral machines in the customer's
cloud account: EC2 instances on AWS, Compute Engine instances on Google
Cloud, virtual machines on Azure, or sandboxes on a sandbox cloud
(Daytona, Runloop, E2B, Novita, Lelantos, Namespace, Tenki, Modal). It holds the customer's
credentials (sealed), reads `airship.yaml`
from activated repositories, and launches, watches and terminates
instances itself. The first version of this project ran an agent in the
customer's account instead; the design below is the result of moving that
work into the control plane.

```
           webhooks (workflow_job, push, installation, installation_repositories, repository)
   GitHub  ───────────────────────────────────────────►  Control plane
     ▲  ▲        REST: JIT configs, runner/job lookups,      ├─ api: webhooks, dashboard API, developer API, runner API
     │  │        file reads, redelivery, PRs on .airship       ├─ launcher (every replica)
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
  API (`internal/auth`, `internal/console`), the developer
  API (`internal/console/apitokens.go`) and the runner API
  (`internal/runnerapi`). It also runs the launcher, the reconciler and the
  webhook redeliverer.
- `internal/fleet` owns the runner lifecycle. Everything that changes
  runner state goes through it.
- `internal/cloud` is the provider interface; `cloud/aws` is EC2 and S3;
  `cloud/gcp` is Compute Engine (with `gcp/gcptest`, a fake Compute API
  the real SDK is pointed at in tests); `cloud/daytona` is Daytona
  sandboxes, driven through Daytona's generated Go clients (with
  `daytona/daytonatest`, a fake of its API and of the toolbox API inside a
  sandbox); `cloud/runloop`, `cloud/e2b` (with Novita and Lelantos as
  flavours) and `cloud/namespace` speak REST or Connect JSON through
  `cloud/restapi`, each with a fake of its API; `cloud/tenki` and
  `cloud/modal` wrap the vendors' Go SDKs behind an interface faked in
  tests; `cloud/bootscript` renders the Linux,
  Windows and sandbox boot scripts; `cloud/fake` is what fleet tests
  launch on. [architecture/sandbox-providers.md](architecture/sandbox-providers.md)
  assesses the other sandbox providers.
- `internal/runnerconfig` parses `airship.yaml`, `airship.global.yaml` and
  `access.yaml` (pure, no I/O).
- `internal/secrets` seals cloud credentials under `CREDENTIALS_KEY`, each
  blob bound to its row; the variable may list several keys for rotation.
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
`launch_failed`, `launch_unconfirmed` (the cloud's answer to the launch
was lost; the machine may exist), `lease_expired` (a launcher claimed it
and died), `lost` (its instance vanished and GitHub never said the job
ended), `max_lifetime` (8 hours, or an idle pool runner replaced halfway
through them), or `deleted` (the app was uninstalled).

Every transition is a conditional `UPDATE ... WHERE state = <expected>`, so
concurrent webhooks, launcher passes and reconciler passes cannot apply a
transition twice or undo each other.

### Invariants

The cloud calls are at-least-once and their answers can be lost, so the
design rests on a few rules rather than on any call succeeding exactly
once:

1. **The database is the only state, and every runner row says what is
   known about its machine.** `instance_id` is set once the cloud
   confirmed a launch; `instance_gone_at` once the cloud listed the
   region without it after a grace period. A runner whose machine is
   unknown (`lease_expired`, `launch_unconfirmed`) has neither, and only
   the inventory scan may decide.
2. **A launch is idempotent per runner.** AWS gets a `ClientToken` and GCP
   a `requestId` derived from the runner ID, and GCP and Azure machines
   are named after the runner and looked up by name before every create.
   A retried call returns the first machine; it never makes a second one.
   On Azure a machine is several resources (address, interface, VM, disk)
   made in order, each a PUT by name, so a retry re-finds every part an
   earlier attempt made and a boot started by Run Command is checked for
   before it is started. Daytona
   sandboxes are named after the runner too (a name is unique in the
   organization) and looked up before every create, and the steps that
   follow creation there (write the script, start it, label the sandbox)
   each check for their own earlier success before acting.
3. **One machine serves at most one job, and a machine Airship did not
   make is never touched.** Instances carry the installation as a tag or
   label; `List` filters by it, and `Terminate` reads the instance and
   refuses when the tag or label is not the installation's. The inventory
   scan terminates what is listed for the installation but not known to
   the table.
4. **Registration precedes the machine, and is bounded.** The JIT config
   is minted first and the GitHub runner ID stored before the cloud is
   called, so a machine that boots can register and nothing can boot
   without a stored registration to clean up. A registration whose
   machine never came (or was never confirmed) is deleted from GitHub two
   minutes after the runner finished, unless GitHub says it is busy, in
   which case the machine exists after all and the runner is revived.
   Revival goes the other way too: the scan revives a given-up runner
   whose machine it lists only after GitHub confirms the registration
   still exists; a machine whose registration is gone can never take a
   job, so it is destroyed instead and the job keeps its retry. The
   launcher also checks the job once more after registering and before
   calling the cloud: a job cancelled or started elsewhere in that window
   gets no machine, and the registration is removed at once.
   The cloud call itself is bounded by the lease: while it runs the
   launcher renews the lease every half lease, up to `LaunchTimeout`
   (15 minutes, for clouds that take minutes to build a machine), and
   cuts the call off 30 seconds (a quarter of a short lease) before the
   last lease it knows of expires, so a runner is never given up while
   its launch is still in flight. A renewal that is refused (the runner
   was finished meanwhile: expired by a reconciler while the database was
   unreachable, or deleted) stops the call at once, so no machine is made
   for a runner nobody tracks; the providers settle a cut-off creation by
   looking the machine up.
5. **Ambiguity is resolved by observation, not assumption.** A transport
   error after the request was sent, a lost poll or a timed-out operation
   is settled by looking the machine up; only when even that fails is the
   launch `launch_unconfirmed`. On Azure a creation accepted but still in
   progress when the launch runs out of time is unconfirmed too: the
   machine is real, lists for the installation, and the next attempt (or
   the scan) adopts it. The Daytona provider never reports an
   unconfirmed launch: a sandbox nobody started a runner in is worth
   nothing, so a launch it cannot finish deletes what it made and fails as
   retryable instead. Such a runner keeps its registration and
   is revived when its machine is listed, exactly as a `lease_expired`
   one, while its job is retried on a new runner (the revival cancels the
   retry while it is still pending; if both come up, one idles out).
   GitHub's word counts as an observation too: a `workflow_job`
   `in_progress` naming a runner Airship gave up on without seeing its
   machine go (`lease_expired`, `launch_unconfirmed`, `lost`) revives it
   as `busy` on the spot, so a job running on such a machine is tracked
   even while the scan cannot list the scope, and even when Airship
   believes it already deleted the registration: GitHub refuses to delete
   a busy runner, but a job assigned in the instant between Airship's
   read and its delete is running on a live machine, and reviving the
   runner is what keeps the scan from destroying it. The revived runner drops
   its job trigger (a job has at most one live runner, and its retry may
   be live); the job row links to it instead.
6. **Nothing is retried that GitHub would not run again.** Checked
   against GitHub's documentation on 2026-10-27 rather than assumed: the
   self-hosted runners reference says "If the runner doesn't pick up the
   assigned job within 60 seconds, the job is re-queued so that a new
   runner can accept it" and "If the job remains queued for more than 24
   hours, the job will fail"; the limits reference puts self-hosted job
   execution at up to 5 days (`timeout-minutes` defaults to 360) and the
   queue at 24 hours; the autoscaling guide says "GitHub only assigns one
   job to a runner" for ephemeral runners and de-registers the runner
   after that job; the removal guide says an ephemeral runner is removed
   after a day without connecting (14 days otherwise). No GitHub document
   says what happens to a job whose runner dies mid-run, so that was
   observed on 2026-10-27 with a real ephemeral runner (a JIT
   registration in a container) holding a `workflow_dispatch` job that
   sleeps for thirty minutes: the container was SIGKILLed while the step
   ran; GitHub listed the runner `offline` but still `busy` within a
   minute and left the job `in_progress`; 9 minutes 35 seconds after the
   kill the job completed with conclusion `failure` on its first and
   only attempt, annotated "The self-hosted runner lost communication
   with the server. Verify the machine is running and has a healthy
   network connection", with the running step left `in_progress`, and
   the ephemeral registration was removed by GitHub at the same moment.
   Nothing was re-queued and no second attempt appeared: a job whose
   runner dies is failed, roughly ten minutes later, and only a person
   (or a workflow-level retry step) runs it again. Airship therefore
   retries only jobs still `queued` (a runner that never came online leaves its job
   queued, which GitHub will not re-route on its own before the 24 hour
   failure, so Airship's idle timeout replaces the runner and retries the
   job); a `lost` busy runner ends its job as GitHub reports it (the
   `BusyLostAfter` write-off at 10 minutes matches GitHub's own
   give-up time, and a runner GitHub reports `busy` is never reaped as
   idle, however long it has been `offline`), and nothing re-runs a job
   that failed. Registrations of runners that
   never connected are deleted by Airship, not left for GitHub's one-day
   removal.
7. **Every machine has its own deadline.** The boot script powers the
   machine off when the runner exits and at the maximum lifetime, and
   runs once per boot (a marker file makes a reboot shut down instead of
   starting another runner), so a lost control plane leaves no machine
   running forever. On GCP a powered-off machine is `TERMINATED` rather
   than deleted (Compute Engine has no delete-on-shutdown), so it stops
   costing compute at once but keeps its disk until the reconciler deletes
   it; Airship does not give the machine a service account that could
   delete itself. An Azure machine shut down from inside is `stopped`,
   still allocated and billed, until the reconciler lists it stopped and
   deletes it; its disk, interface and address go with it. A Daytona
   sandbox has no power button: its script stops
   the sandbox's daemon (PID 1) on every exit path, the sandbox is created
   to delete itself when stopped, and it carries a TTL fifteen minutes past
   the maximum lifetime, so Daytona removes a sandbox whose script never
   ran at all. The maximum lifetime (`RUNNER_MAX_LIFETIME`, 8 hours)
   is the one bound that can cut a legitimate job short: GitHub's default
   job timeout is 6 hours (`timeout-minutes: 360`), so the default leaves
   room, but a self-hosted job may run for up to 5 days when the workflow
   raises `timeout-minutes`, and such a workflow needs the lifetime raised
   with it. A pool
   runner is retired after half the lifetime idle so that a job it takes
   still has the other half.

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
  script powers it off. A transport error after the request was sent is
  settled by describing the instance by client token; if that fails too
  the launch is `launch_unconfirmed`.
- **Launching on GCP.** The same launch becomes `instances.insert` with a
  `requestId` derived from the runner ID, zone and attempt, after a lookup
  of the instance by name (the runner's name). The provider waits for the
  zone operation; a lost answer, failed poll or timed-out wait is settled
  by looking the instance up. A `region:` that is a zone pins the launch;
  a region tries its zones in an order derived from the runner ID and
  moves on only for zone-specific failures (`ZONE_RESOURCE_POOL_EXHAUSTED`,
  a machine type the zone lacks). Spot machines use `provisioningModel:
  SPOT` with `instanceTerminationAction: DELETE`; everything launches with
  `automaticRestart: false`, a vTPM and integrity monitoring when the image
  is UEFI-compatible (Compute Engine refuses them on a BIOS image), labels
  for the installation and runner, the boot script as `startup-script` (or
  `windows-startup-script-ps1`) metadata, and the network tag
  `airship-runner`. Startup scripts run on every boot, so the script
  leaves a marker and a rebooted machine powers off instead of running a
  second job. `enable_public_ip: false` creates one Cloud Router and NAT
  per region and network with its own deterministic request ID:
  `airship-nat` on the default network, and
  `airship-nat-<network>` on any other, including the network of a
  `subnetwork` named without its network (read from the subnetwork). A
  router by that name that already exists is read back, also when it is
  found only through the conflict a racing create reports, and accepted
  only on the right network and with a NAT; otherwise the launch fails
  naming it rather than booting a machine without egress. Every Compute
  call is bounded, the per-zone instance list included, since the
  reconciler's steps run in sequence and one hung call would stall them
  all: the SDK's generated list methods send their requests without the
  caller's context, so the provider gives the SDK an HTTP client with a
  timeout of its own (above the two minutes an `operations.wait` may
  block) and enforces the list deadline from outside it. A
  `diskType` written as a zonal path keeps only the type's name, since the
  runner's zone is chosen at launch. Deleting a machine deletes its disks
  (`autoDelete`).
- **Launching on Azure.** A machine is several Resource Manager resources
  in one resource group, `airship-runners`, that the provider creates in
  the subscription on first use, with a virtual network and security group
  per region (`airship-runners-<region>`, one `runners` subnet with the
  default outbound access off, no inbound rules) that every runner shares.
  Each launch, in order: a static public address (`<name>-ip`, unless
  `enable_public_ip: false`), a network interface (`<name>-nic`) on the
  subnet, then the virtual machine named after the runner, each a PUT by
  name so a retry re-finds what an earlier attempt made; the interface,
  address and OS disk carry `deleteOption: Delete` and go with the VM.
  The VM is looked up by name before any create (a VM with the name and
  another runner's tag is a permanent refusal). Creation is a
  long-running operation: a refusal before it was accepted deletes the
  interface and address; a failure after acceptance (`AllocationFailed`,
  quota, a marketplace image without accepted terms) leaves a `Failed`
  VM, which the provider deletes before failing the launch as retryable
  or permanent by the error code; a lost answer, failed poll or timed-out
  wait is settled by looking the VM up, and a VM still `Creating` is
  waited for, up to the launch's own bound, then reported ambiguous. The
  resource providers (`Microsoft.Compute`, `Microsoft.Network`) are
  registered by the SDK on first use. Linux images take the boot script
  as `customData` (cloud-init runs it once, at most 64 KB); Windows and
  specialized images cannot, so once the VM is `Succeeded` the provider
  starts the script through a managed Run Command (`airship-boot`,
  asynchronous, read back before it is written since re-writing one
  re-runs it; the script's marker makes a second run a no-op anyway). A
  VM that cannot be given its script is deleted rather than left to idle
  out. Spot machines use `priority: Spot` with `evictionPolicy: Delete`
  and no price cap. Trusted Launch is requested only for images that
  require it or Windows images that support it, with secure boot off (the
  GPU driver path loads unsigned modules). `enable_public_ip: false`
  creates one NAT gateway per region (`airship-nat-<region>` with a
  static address of its own) and attaches it to the subnet. The default
  is a public address per machine: the subnet is private
  (`defaultOutboundAccess: false`, which Azure also makes the default for
  new networks from API versions released after March 31, 2026), so a
  machine without an address and without a NAT would never reach GitHub. Listing uses the
  subscription's `virtualMachines?statusOnly=true` (the only list that
  carries the power state) filtered by resource group, region and the
  installation tag; a VM whose power state is stopped or deallocated
  lists as stopped, one `Deleting` as stopping, one `Failed` with no
  power state at all (never allocated) as stopped. Deleting a machine is
  `forceDeletion`, not waited for. The scan then sweeps interfaces and
  addresses of the installation in the region that no VM uses and that
  are older than fifteen minutes (a launch that died between making the
  interface and the VM leaves them), at most every five minutes per
  scope. Every call is bounded; the SDK's own retries are kept short so
  a deadline is spent on the call. The secret is a Microsoft Entra
  application (tenant, client ID, client secret) with the Contributor
  role on the subscription; `Verify` reads the
  subscription (an application that cannot see it gets a 404, reported
  as bad credentials), refuses a disabled one, and checks the default
  region is one the subscription offers.
- **Launching on Daytona.** A sandbox runs nothing at boot (Daytona's
  daemon is PID 1 and waits for commands), so the control plane boots the
  runner itself, in idempotent steps: look the sandbox up by the runner's
  name; create it if absent (labels for the installation, runner and
  repository; `target` the region; built from `FROM <image>` with the
  shape's CPU, memory and disk, or from the named snapshot; auto-stop off,
  delete-on-stop on, TTL past the lifetime; GPU type and spot, volumes and
  allow lists as the definition says), settling a conflict, a 5xx or a
  lost answer by looking the name up again and refusing a sandbox placed in
  another region; wait for it to be `started` (a failed image build is
  permanent, an error, a deletion or a timeout retryable); create the
  `airship-runner` process session (a session that exists is fine, and the
  session is never deleted, since deleting one kills its processes); write
  the boot script to `/tmp` over the toolbox API; start it in the session
  unless the session already runs that command; and last set the
  `airship-boot=started` label. The script itself takes a lock directory
  before arming its exit trap, so a second start backs out without stopping
  the sandbox under the first. Any failure after creation deletes the
  sandbox (best effort) and fails the launch; nothing from this provider is
  ambiguous. The instance ID is the sandbox ID.
- **Inventory.** The reconciler lists instances per (credential, region)
  by the `airship:installation` tag (AWS) or `airship-installation` label
  (GCP, one list per zone of the region, failing if any of those zones
  is unreachable; the aggregated list has no partial-success switch in
  the SDK and would fail on an outage anywhere in the world) and compares
  them with the runners table:
  instances for finished or unknown runners are terminated, runners whose
  instance disappeared are `lost` (and their job retried), a stopped
  instance counts as disappeared and is terminated too, and a runner
  whose lease expired or whose launch was unconfirmed but whose instance
  did launch is revived. Listing errors leave state alone: an instance
  missing from a failed list is never presumed gone. On GCP a region
  stored for a runner carries the project when `extra_config.project_id`
  overrides the credential's (`us-central1@my-project`), so each project
  is its own scope. On Daytona the scan lists the organization's sandboxes
  by the installation label, page by page, keeps those whose `target` is
  the region, and reports a `stopped`, `error`, `build_failed`, `archived`
  or `paused` sandbox as stopped (nothing runs in it and nothing will).
  A sandbox without the boot label has no runner: younger than ten minutes
  it is a launch still at work and is left alone; older it is a leak and
  is reported as stopped, which has the scan delete it, rather than being
  revived into an empty sandbox. Daytona's list is eventually consistent
  (a new sandbox appears a few seconds after its creation), which the
  two-minute instance grace absorbs.
- **Termination.** A finished runner's instance is terminated at once when
  the completed webhook arrives, and again by the reconciler if the cloud
  still lists it. Deleting from GitHub happens 2 minutes later, only if
  GitHub says the runner is not busy.
- **Lost webhooks.** GitHub never redelivers failed webhooks; one replica
  scans the app's delivery log every two minutes and asks for redelivery
  with backoff. The reconciler also asks GitHub about jobs queued for 10
  minutes without news, two per installation per pass.
- **Idle runners** are `idle_pools`, rebuilt from the default branch's
  file on push (and hourly): a repository's `airship.yaml` gives repository
  pools, the organization's `airship.global.yaml` organization pools whose
  runners register on the organization in a runner group of their own.
  Pool runners register with the definition's plain labels and have no
  trigger job; a queued job whose labels a pool's runners carry reserves
  one instead of creating a runner, and the pool refills at once. A
  reserved runner that gets no job within the idle timeout is reaped;
  one that waited idle for half the maximum lifetime is retired and
  replaced, so a job never lands on a machine about to hit its own
  deadline. Usage-based pools stay warm for `duration` after one of
  their definition's runners starts a job.

## What a job reads

1. Only jobs with a `airship-` label cost anything; others are ignored.
2. The repository must be activated (public ones with public access
   allowed): a config file never launches anything in a repository nobody
   activated.
3. The run attempt is fetched to learn who started it, for `access.yaml`.
   `airship.yaml` is read at the job's commit.
4. One-line labels that no definition covers become runners of their own.
   Organization runners from `.airship/airship.global.yaml` are tried after
   the repository's and filtered by `access.yaml`; a broken `access.yaml`
   closes every organization runner.
5. Each attempt needs the installation's connected cloud; without one the
   job is skipped with `no_cloud`. Connecting a cloud, activating the
   repository or unsuspending the installation plans the jobs skipped
   for that reason again, after asking GitHub whether they are still
   queued.

## Security model

- **Credentials.** Access keys are verified with STS (service account
  keys with `projects.get`, Azure service principals by reading the
  subscription and its regions, Daytona API keys by reading the key's own
  record, which must belong to an organization, be unexpired and carry the
  sandbox write and delete permissions, and the organization's regions,
  which must include the default) before they are stored, sealed with
  AES-256-GCM under `CREDENTIALS_KEY`, opened only for the duration of a
  cloud call, and never returned by any endpoint. The GCP identity shown
  is the service account's email; the project is the account ID. The
  Azure identity is the application (client) ID; the subscription is the
  account ID. The Daytona identity is the organization ID and the key's
  name. A sealed
  blob is bound to its row (installation and cloud, or runner), so it
  cannot be moved and opened elsewhere. Disconnecting a cloud, or
  replacing its key with one for another account, is refused while
  runners launched with it are alive, because without the key their
  instances could not be terminated; rotating a key within the account is
  allowed. Disconnecting and creating runners take the installation lock,
  so no runner can slip in between the check and the delete. Validation
  errors never echo the value that failed, and upstream failures are
  reported without GitHub's or the database's error text.
- **Tenancy.** Every query is scoped by GitHub installation ID; instances
  are tagged (AWS, Azure) or labelled (GCP, Daytona) and listed by it, and
  a terminate refuses an instance not marked for the installation. Two
  installations sharing one Azure subscription share the resource group
  and the per-region networks (tagged `airship-managed`, not per
  installation) and rest on the VM tag alone. Two installations sharing
  one Daytona organization rest on that label alone,
  and a sandbox's toolbox is open to anyone holding an API key of the
  organization: a Daytona organization per installation is the stronger
  boundary.
- **Dashboard sessions.** GitHub OAuth, HttpOnly `SameSite=Lax` cookies,
  an Origin check on every write. Organization owners are admins, members
  read-only, nobody else gets in; roles are re-checked hourly. While
  GitHub has the app suspended the dashboard is read-only too.
- **Developer API.** `asa_` bearer tokens, stored hashed, scoped to one
  installation. The `/api/v1` routes accept no session cookie.
- **Runner API.** `asr_` cache tokens, one per cache-enabled runner, dead
  once the runner finishes; they buy credentials for that installation's
  cache bucket and nothing else.
- **Boot script.** Everything interpolated is validated against a strict
  pattern first; job variables travel as base64 and are decoded on the
  machine.
- **Config files.** A file only acts in an activated repository, and a
  public repository only once activated with public access allowed, which
  is where fork pull requests are gated. Anyone who can push to an
  activated repository can pick machines for that branch's jobs, as with
  any workflow; `max_runners` bounds what that can cost.
- **Images.** Any AMI or Compute Engine image the account can launch is
  allowed; on Azure any marketplace image (whose terms the
  subscription has accepted) or a managed or gallery image of the
  connected subscription (another subscription's is refused by its ID),
  and on Daytona any container image Daytona can pull, with a tag or
  digest (`latest` is refused, so a definition names what it runs), or
  any snapshot of the organization. The default is Canonical's current
  Ubuntu 22.04 (the `ubuntu-2204-lts` family on GCP, the `22_04-lts-gen2`
  SKU on Azure, `ubuntu:22.04` on Daytona). An image whose architecture
  is known and differs from the machine's is refused before the launch;
  a marketplace image its publisher deprecated is refused too.
- **GCP machines** get no service account unless `serviceAccounts` sets
  one, and the service account key Airship holds is never on the machine.
  The JIT config travels in instance metadata, readable by anyone in the
  project with `compute.instances.get` until the runner registers, as the
  AWS user data is; `provision_via: pull_from_api` avoids it.
- **Azure machines** are provisioned with an `airship` account whose
  SSH key (Linux) is generated once per process and never written
  anywhere, or whose password (Windows) is random and discarded: nobody
  can log in. No managed identity is assigned, so a job cannot act on
  the subscription with the machine's credentials; the application
  secret Airship holds never reaches the machine. The JIT config travels
  as `customData` (readable by anyone with `virtualMachines/read` on the
  resource group until the runner registers, as AWS user data is) or in
  the Run Command's script (readable with `runCommands/read`);
  `provision_via: pull_from_api` avoids both. The security group has no
  rules of Airship's own: Azure's defaults deny inbound traffic from the
  internet and allow all outbound.
- **Daytona sandboxes** are containers Daytona runs as privileged (so a
  Docker daemon can run inside when `docker: true`), with the toolbox
  API as the only way in; Airship never opens a shell, it writes one file
  and starts one command. The boot script, JIT config included, is written
  to the sandbox over that API rather than placed in metadata, and sits in
  `/tmp` for the sandbox's life; the config is single use and spent the
  moment the runner registers. The customer's API key never enters the
  sandbox. Egress is restricted only when a definition sets the allow
  lists; Daytona's own network rules apply otherwise.

## Design decisions

The config format, fallback and suffix rules, idle runners per repository
and per organization, organization runners with `access.yaml`, one-line
labels (for forks too), files read at the job's commit, `provision_via`,
the developer API, the Windows fields and S3-backed caching with one
namespace per account bucket are all as documented in
[airship-yaml.md](airship-yaml.md). The choices worth explaining:

- **Clouds.** AWS, GCP, Azure, and the sandbox clouds Daytona, Runloop,
  E2B, Novita, Lelantos, Namespace, Tenki and Modal, chosen and sequenced as
  [architecture/sandbox-providers.md](architecture/sandbox-providers.md)
  explains. Those without regions have the one region `global`; Namespace
  has `us` and `eu`. `do`, `openstack`, `oracle`,
  `vast_ai` and `on_prem` are refused with "not supported yet" rather than
  ignored. Each is a provider implementation; none can be trusted unrun.
- **Azure gets a public address by default.** `enable_public_ip: false`
  creates a NAT gateway per region instead. A machine with neither relied
  on Azure's default outbound access, which Microsoft is retiring —
  subnets of networks created with API versions released after
  March 31, 2026 are private by default, and Airship's subnet is private by
  its own setting either way — so the other default would boot machines
  that cannot reach GitHub. Airship also creates a security group per
  region, with no rules of its own, so the subnet is explicit about
  allowing no inbound traffic. The OS disk defaults to 75 GB on Linux and
  the image's size on Windows, on the image's default disk kind. Windows
  and specialized images boot through a Run Command, because Azure runs no
  custom data on Windows. The resource group, networks and NAT are shared
  by every installation that connects the subscription.
- **Daytona** is not a machine cloud like the others, so its fields were
  given meanings of their own: `instance_type` is a sandbox shape
  (`2vcpu-4gb`) or a snapshot name, `machine_image` a container image,
  `region` a Daytona region ID, `gpu` one of Daytona's GPU types,
  `preemptible` spot GPU capacity. `extra_config` gains `disk`, `docker`,
  `volumes`, `network_allow_list` and `domain_allow_list`; `provision_via`
  and the Windows keys are refused there, since a sandbox runs nothing at
  boot and runs Linux containers only.
- **GCP `existingDisk`** is attached with `autoDelete: false`, so the
  machine's deletion never takes the customer's disk with it, and a zonal
  disk pins the launch to its zone. Compute Engine serializes `READ_WRITE`
  attachment itself (`RESOURCE_IN_USE_BY_ANOTHER_RESOURCE`), which Airship
  treats as retryable: the job waits for the runner holding the disk.
- **GCP `enable_public_ip: false`** creates the Cloud NAT on the network
  the first interface names (or its subnetwork belongs to); a second
  network in the same region gets a NAT of its own rather than an error,
  and a router with Airship's name that is on another network is reported,
  never reused.
- **GCP and sandbox caching.** `cache: true` on a GCP, Daytona, Runloop
  or E2B runner
  launches without the cache proxy (and records a warning). Cache
  acceleration is Linux runners on AWS only today; a Cloud Storage backing
  would be an addition behind the `cloud.Cacher` seam.
- **Names.** The file is `airship.yaml`, labels start with `airship-`,
  the organization repository is `.airship` with `airship.global.yaml`;
  runners are named `airship-...`, instances are tagged `airship:*`, the
  cache bucket is `airship-cache-*`.
- **Organization idle runners** register in a runner group per definition
  that is visible only to the activated repositories the definition's
  `access.yaml` policies allow. Per-person policy rules cannot apply to a
  runner booted before any job exists.
- **`SubnetId`, `SecurityGroupIds`, `IamInstanceProfile`** in
  `extra_config` are the AWS keys that matter, passed through explicitly
  rather than by forwarding arbitrary unknown keys to the API.

## What has run, and what has not

- **Runloop, E2B, Novita, Lelantos, Namespace, Tenki, Modal: fakes only.**
  Each provider runs on every `make test` against a fake of its API (built
  from the vendor's published spec, protos or SDK), and through the fleet
  and HTTP tests; none has launched a real sandbox yet. What a first live run must confirm is listed in
  [architecture/sandbox-providers.md](architecture/sandbox-providers.md).

- **The Linux boot script, unmodified, on a real runner with a real
  GitHub job.** The script `ProvisionScript` renders (JIT config inline,
  the cache section enabled) ran as-is in a fresh `ubuntu:22.04`
  container, with only `shutdown` stubbed because a container cannot
  power itself off. It installed its packages, fetched the cache proxy
  from the API's asset endpoint, obtained a grant with its runner token,
  put the proxy's CA in the system store, wrote the runner's `.env`,
  started actions/runner 2.338.2 with the single-use JIT registration and
  ran one `workflow_dispatch` job on a private repository. The job's
  steps saw `https_proxy`, `NODE_EXTRA_CA_CERTS` and `ACTIONS_CACHE_URL`;
  `actions/cache/save@v4` saved 90 MB (a multipart upload from the staged
  file), `actions/cache/restore@v4` restored it by exact key and by
  `restore-keys` prefix with matching checksums, and two unknown keys
  missed. Every step succeeded, the runner exited after its one job and
  the script reached its shutdown. The API and the bucket were stand-ins
  at the network edge: a small HTTP server answering the two endpoints
  the script and the proxy call, and moto for S3 (the proxy's
  `AIRSHIP_S3_ENDPOINT` override). An earlier run of the same job against
  a hand-built container had shown the runner's live-log websocket on the
  results host being refused by the interceptor; upgrades are now relayed.
  The proxy had also been driven by the `@actions/cache` library directly
  with a 140 MB block upload.
- **The EC2 provider, cache bucket and federation grant** run against moto
  (`TestAgainstEmulator`, `make test-aws-emulator`): request shapes, tags,
  the default VPC and security group, lifecycle rules, termination. moto does not model IAM,
  quotas, spot capacity or the boot script.
- **Boot scripts.** The Linux script is checked with `bash -n` in tests and
  its `.env` line executed, and the rendered script is what the container
  above ran. Both Windows scripts (background and `run_as: interactive`) are
  parsed by PowerShell itself in tests when `pwsh` is on the path or
  `AIRSHIP_PWSH` names one (checked with 7.4.6).
- **Not run:** a launch on real EC2 (no AWS account was available; the
  opt-in `TestAgainstLiveAWS` in `internal/cloud/aws` launches and
  terminates one instance when `AIRSHIP_LIVE_AWS_*` credentials are set),
  and a Windows machine. The first real deployment exercises exactly the
  `RunInstances` call moto accepted and the scripts above.
- **GCP: not run against Google.** The Compute Engine provider is driven
  through the real `cloud.google.com/go/compute` client against
  `gcp/gcptest`, a fake of the REST API written from Google's reference
  (request IDs, zone operations, per-zone lists with unreachable zones,
  `ZONE_RESOURCE_POOL_EXHAUSTED`, quota errors after accept, dropped
  connections, hung operations, image families and machine images,
  subnetworks, routers). The tests cover idempotent relaunch, zone
  fallback, pinned zones, every error class, lost answers, ownership
  checks on delete, region-scoped listing, NAT creation per network and
  Shielded VM options following the image. No GCP project was
  available, so the exact request bodies have not been accepted by
  Google; the first real launch is the thing to watch, in particular the
  `startup-script` size, the `guestAccelerators` type path for GPUs, the
  `existingDisk` attachment and the Cloud Router insert. The opt-in
  `TestAgainstLiveGCP` in `internal/cloud/gcp` launches one `e2-micro`,
  checks idempotent relaunch, listing and the ownership refusal, and
  deletes it when `AIRSHIP_LIVE_GCP_KEY_FILE` and
  `AIRSHIP_LIVE_GCP_LOCATION` are set; it costs one machine's minutes.
  Until it has run, the GCP provider should be treated as unexercised
  against Google, however thorough the fake is.
- **Azure: not run against Azure.** The provider is driven through the
  real `azure-sdk-for-go` clients (`armcompute` v8, `armnetwork` v8,
  `armresources`, `armsubscriptions`) against `azure/azuretest`, a fake
  of Resource Manager written from Microsoft's reference (case-insensitive
  paths, long-running operations through `Azure-AsyncOperation`,
  creations that fail after acceptance and leave a `Failed` VM, dropped
  answers that the SDK's retries then repeat, hung and failing operation
  polls, resources that cascade on delete by their `deleteOption`,
  subscriptions, locations, marketplace versions, managed and gallery
  images, networks, addresses, interfaces, NAT gateways, run commands).
  The tests cover verification (a subscription the principal cannot see,
  a disabled one, an unknown region), the request shapes for Linux,
  Windows (Trusted Launch, no custom data, a Run Command started once and
  not again on relaunch), spot and disk options, idempotent relaunch and
  the refusal of a foreign VM with the runner's name, every error class
  after and before acceptance with the interface and address cleaned up,
  lost answers settled by lookup, a creation still in progress reported
  ambiguous and adopted later, a `Failed` VM removed and relaunched, Run
  Command failures deleting the VM, NAT creation shared across launches,
  a network with Airship's name but not its tag refused, ownership
  checks on delete, listing by region, installation and state, a list
  that fails or hangs failing rather than reporting nothing, the orphan
  sweep's age, scope and rate limit, and image resolution for URNs
  (newest version for properties, `latest` left to Azure, deprecation,
  purchase plans), managed and gallery images (specialized ones with no
  OS profile, versions, architecture mismatches, another subscription).
  The machine's side of the lifecycle ran for real on 2026-10-27: the
  `customData` the provider builds for a Linux launch (captured through
  the fake, 6288 bytes raw and 8384 encoded, against the 65535 limit),
  carrying a JIT config minted with the labels Airship registers
  (`self-hosted`, the arch, `azure`, `cpu`, `airship`, the definition's
  own), ran unmodified in an `ubuntu:22.04` container with only
  `/sbin/shutdown` stubbed (and `sudo`, `curl` and CA certificates
  installed first, which Canonical's Azure images ship and the bare
  Docker image does not): it downloaded the runner, connected in about
  two minutes, took a real `workflow_dispatch` job whose steps checked
  the architecture, that the `env_vars` reach the job, that the runner
  can `sudo`, and that the JIT config is gone from the step's
  environment; the job succeeded, the runner exited after it, the script
  reached its `shutdown -P now` trap, and GitHub removed the ephemeral
  registration. No subscription was available, so no request body has
  been accepted by Azure itself; the first real launch is the thing to
  watch, in particular the
  `customData` size, the Trusted Launch and size combinations, the NAT
  gateway attachment, the Run Command on a Windows image, and whether
  `statusOnly=true` lists carry the power state as documented. The opt-in
  `TestAgainstLiveAzure` in `internal/cloud/azure` verifies a service
  principal, launches one `Standard_B2s`, checks idempotent relaunch,
  listing with power state, the ownership refusal, deletion with its
  interface and address, and an empty sweep, when
  `AIRSHIP_LIVE_AZURE_{SUBSCRIPTION_ID,TENANT_ID,CLIENT_ID,CLIENT_SECRET}`
  are set; it costs a machine's minutes and leaves the resource group and
  network behind. Until it has run, the Azure provider should be treated
  as unexercised against Azure, however thorough the fake is. The fleet
  tests exercise the Azure definition through the fake provider: the
  fields carried to a launch, fallbacks, the scan's sweep, and the lease
  renewal a slow launch depends on.
- **Daytona: run against Daytona, with a real job.** The provider is
  driven through Daytona's generated Go clients (`api-client-go` and
  `toolbox-api-client-go`, v0.226.1) against `daytona/daytonatest`, which
  runs as part of `make test`:
  name conflicts and lost create answers, image build failures, sandboxes
  that never start, toolbox and label failures with cleanup, pagination,
  state mapping, ownership checks on delete. It has also run against a
  real organization (`app.daytona.io`, API v0.226.1) twice on 2026-10-27.
  The opt-in `TestAgainstLiveDaytona` verified the key and refused an
  unknown region, launched a `2vcpu-4gb` sandbox built from `ubuntu:22.04`
  (created, started, script written and running, labelled, in 5 s),
  found a repeated launch returned the same sandbox, listed it after 4 s
  (the list is eventually consistent), refused a terminate by another
  installation, and watched the script's exit path for real: the runner
  could not register (the test's JIT config is not GitHub's), the script
  stopped PID 1, and Daytona deleted the sandbox 75 s after launch. A
  throwaway harness then launched a sandbox through `Provider.Launch`
  with a JIT config minted by `generate-jitconfig` on a private
  repository and `docker: true`: the sandbox was created, built, started,
  written to, started and labelled in 42 s, a second `Launch` of the same
  runner returned the same sandbox without starting a second runner, the
  runner (2.338.2) was online on GitHub 85 s after the launch began, a
  `workflow_dispatch` job ran on it with every step green (`uname`,
  `actions/checkout@v4`, `docker run --rm alpine:3.20` against the Docker
  28.3.3 daemon the script started inside the sandbox, `sudo`), the
  runner deregistered itself after its one job, the script stopped the
  sandbox, and Daytona had deleted it 2 m 8 s after launch; the
  organization listed no sandbox afterwards. Inside the sandbox PID 1 is
  `daytona`, and `nproc` and `free` report the host (48 CPUs, 377 GB),
  not the shape: CPU and memory are cgroup limits. Two more real jobs
  followed: one on a sandbox launched from Daytona's `daytona-medium`
  snapshot in 3 s (running as its `daytona` user, so the script's `sudo`
  re-execution ran for real; `cpu.max` showed the two CPUs), and one on a
  sandbox with a Daytona volume named by its name, mounted at
  `/mnt/probe` as a writable `mountpoint-s3` FUSE filesystem. The other
  fields were sent and refused: a domain allow list (this organization's
  tier forbids sandbox-level lists, 400), a GPU with and without spot (no
  GPU credits, 400), and the dedicated region `us-west-4` (no image
  builds there, 403; a snapshot not published there, 400); each was
  classified permanent, and the runnerconfig now refuses both allow lists
  at once, IPv6 CIDRs and over-long lists, as Daytona documents. A
  snapshot Airship's key created itself from `ubuntu:22.04` (1 CPU, 2 GB,
  5 GB) became active in about a minute and launched a sandbox in 5 s
  that booted, ran as root with a one-CPU cgroup, and self-destructed;
  creating a snapshot in `us-west-4` was refused (`No runners are
  configured in region 'us-west-4' for sandbox class 'container'`), so
  that region hosts no Linux sandbox for this organization. Racing eight
  launches of one runner against the fake under the race detector found
  that a late launcher's failed boot would delete a sandbox another
  launcher had just finished booting; a failed boot now re-reads the
  sandbox and, if it carries the boot label for this runner, returns it
  as the launch's result instead.
  The failure modes were then run against Daytona itself, the same day,
  through `Provider.Launch`, `List` and `Terminate` with the same shapes a
  reconciler would send. *Racing launches:* five callers launching one
  runner at once got one sandbox (one distinct ID, labelled `started`,
  one sandbox listed) and two got `Total memory limit exceeded. Maximum
  allowed: 10GiB` from Daytona, which counts creates in flight against the
  organization's total before it checks the name; that message is
  classified retryable, as a capacity wait, and the two callers would
  have found the sandbox by name on their next attempt. *A launch cut
  off mid-way* (its context cancelled 2.5 s in, while the sandbox was
  `creating`) returned `sandbox did not start in time (last state
  creating)`, retryable, deleted its sandbox so that nothing by that name
  remained 5 s later, and a relaunch of the same runner created a fresh
  one. *A crash mid-launch* (a sandbox created and started but never
  labelled, as a control plane that died between the create and the label
  leaves it) was invisible to `List` while younger than the boot grace,
  reported `stopped` once older, so the scan deletes it, and was adopted
  by a relaunch of the same runner: the same sandbox ID came back, booted
  and labelled. *Terminating a busy runner:* a real runner registered
  with a JIT config, a `workflow_dispatch` job that sleeps for ten
  minutes started on it, and `Terminate` was called while the job's step
  was running; it returned in 500 ms, the sandbox was gone from the
  organization within a minute, and a second `Terminate` of the same ID
  was a no-op without error. GitHub's side of that is slower: the runner
  showed `online` for about two minutes and the job `in_progress` for
  six, until a cancel requested right after the terminate took effect
  (`completed`, `cancelled`, 13:30:58 for a sandbox destroyed at 13:25);
  the ephemeral registration was removed by GitHub itself, and the job
  record keeps its step `in_progress` for good, since no runner ever
  reported it. This is why a terminated busy runner is not the same as a
  cancelled job: the job ends when GitHub gives up on the runner, not
  when the sandbox goes.
  The organization's own capability list
  (`GET /organizations/<id>/available-sandbox-classes`) was read: `us`
  hosts containers, `us-west-4` hosts Windows only, and a GPU-capable
  container region exists that is not accepted as a `target` (`Region
  not found`). A GPU create was sent to it and to `us`, built from an
  image so that the GPU was not a "resource with a snapshot"; both were
  refused for credits. A Windows sandbox from Daytona's `windows-small`
  snapshot in `us-west-4` was requested and refused for the tier
  (`Windows sandboxes are not available for Tier 1 and Tier 2
  organizations`). Verify now reads that class list, so a region that
  hosts no containers is refused at connect time with the regions that
  do, instead of failing every launch.
  **Not run:** a GPU sandbox that actually started (the organization has
  no GPU credits), a sandbox in a dedicated region (this organization's
  hosts no containers), and a sandbox-level allow list (Tier 3 and up).
  Each needs a paid change to the Daytona organization; the requests are
  built and the refusals pinned, so what remains unseen is Daytona's
  answer when it accepts.
- **JIT config in user data.** Anyone in the account who can call
  `ec2:DescribeInstanceAttribute` can read it until the instance registers.
  `provision_via: pull_from_api` avoids it, at the cost of an API token in
  the image.
- **One credential per cloud per installation.**
