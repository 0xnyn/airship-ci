# Airship on Daytona

**What this is.** Everything Airship does inside a Daytona organization:
the API calls, the sandboxes it creates, and the places where a sandbox's
model (a container that runs nothing at boot and has no power button)
forced different decisions from the VM clouds.

**Audience.** Platform engineers deciding whether to hand Airship a Daytona
API key. For the hands-on path, read [daytona-setup.md](daytona-setup.md)
next.

**Status — read this first.** The Daytona provider has run against the real
service. On 2026-10-27, through `Provider.Launch` and nothing else, a
sandbox built from `ubuntu:22.04` was created, started, scripted and
labelled in 42 s; a second launch of the same runner returned the same
sandbox; an `actions/runner` 2.338.2 registered with GitHub 85 s after the
launch began using a real single-use JIT config; a `workflow_dispatch` job
ran every step green, including `actions/checkout@v4`, `sudo` and `docker
run --rm alpine:3.20` against the Docker 28.3.3 daemon the boot script
started inside the sandbox; the runner deregistered after its one job, the
script stopped the sandbox, and Daytona had deleted it 2 m 8 s after launch.
The organization listed no sandbox afterwards. The opt-in
`TestAgainstLiveDaytona` separately verified the key, a repeated launch,
listing, the ownership refusal and the self-destruct path, and in CI the
provider runs through Daytona's generated Go clients against a fake of its
API and of the toolbox API.

Later the same day the remaining fields were sent too. A sandbox launched
from Daytona's `daytona-medium` **snapshot** in 3 s, ran as its `daytona`
user with the script re-executing itself under `sudo`, showed the shape as
cgroup limits (`cpu.max 200000 100000`, `memory.max` 4 GiB), registered
and ran a second real job green, and self-destructed. A sandbox with a
**volume** named by its Daytona name mounted it at `/mnt/probe` as a
`mountpoint-s3` FUSE filesystem, writable, and ran a third real job green
that wrote to it. A **domain allow list** was refused by Daytona for this
organization's tier (`Network access is restricted and cannot be
overridden at the sandbox level`, 400), classified permanent; its rules
below come from Daytona's network-limits documentation. A **GPU** request,
spot and non-spot, was refused for want of GPU credits (400), classified
permanent. The organization's **dedicated region** `us-west-4` passed
verification and refused both an image build (`Declarative builds are not
available to your organization in region us-west-4`, 403) and a snapshot
not published there (400), both permanent; creating a snapshot there was
refused too (`No runners are configured in region 'us-west-4' for sandbox
class 'container'`), so this region hosts no Linux sandbox for this
organization at all and the path could not be taken further. A snapshot created by Airship's own key from `ubuntu:22.04` (`POST
/snapshots`, 1 CPU, 2 GB, 5 GB, active in about a minute) launched a
sandbox in 5 s that booted as root with a one-CPU cgroup and
self-destructed. A GPU sandbox has therefore never *started* for Airship,
and no sandbox has run in a dedicated region; everything else has.

---

## Invariants this provider keeps

These are the rules the rest of the control plane relies on (the general
ones are in [`../architecture.md`](../architecture.md#invariants)); each
is tested against the fake in CI and the marked ones were observed against
Daytona itself.

1. **One sandbox per runner, found by name.** The sandbox name is the
   runner name, unique in the organization. Every launch looks the name up
   first; a create that conflicts, times out or fails with a 5xx is
   followed by another lookup. Eight launches of one runner racing on the
   fake produce one sandbox and the same ID for every caller; against
   Daytona, five callers racing on one runner got one sandbox (the three
   that got past Daytona's organization-wide memory check all received
   the same ID), and a repeated launch returned the same sandbox, three
   times out of three. *(observed)*
2. **A sandbox the control plane did not label is never touched, and one
   it labelled for another installation is never deleted.** Listing
   filters on `airship-installation` and checks the label on each item;
   `Terminate` reads the sandbox and refuses a wrong label. *(observed)*
3. **No runner is started twice in one sandbox.** The session is read
   before the start command is submitted, and the script itself takes a
   lock directory before it does anything, before even arming its exit
   trap, so a second start exits without stopping the first. The lock
   directory was present, root-owned, in the snapshot sandbox. *(observed)*
4. **The boot label is the last step of a launch and the only sign of a
   runner.** A sandbox without it is a launch in progress for ten minutes
   (longer than the provider's own steps can take once the sandbox exists:
   four minutes to start, then four toolbox calls and three label tries
   of thirty seconds each, about seven and a half minutes at worst), then
   a leak the scan deletes. It is never
   revived, because nothing will ever start in it. A sandbox created and
   started but never labelled, left as a control plane that died between
   the create and the label leaves it, was hidden from `List` inside the
   grace, reported `stopped` past it, and adopted by a relaunch of its
   runner: the same ID came back, booted and labelled. *(observed)*
5. **Nothing from this provider is ambiguous.** A launch that cannot
   finish deletes its sandbox and fails retryably. The control plane's
   `launch_unconfirmed` path exists for clouds whose machines boot on their
   own; a Daytona sandbox does not. A launch whose context was cancelled
   2.5 s in, with the sandbox still `creating`, failed retryably and left
   nothing by its name 5 s later. *(observed)*
6. **Every sandbox ends on its own.** The script stops PID 1 on every exit
   path, the sandbox is created to delete itself on stop, the runner runs
   under `timeout` at the maximum lifetime, and the sandbox's TTL is fifteen
   minutes past that. Four sandboxes launched for real were all gone within
   twenty seconds of their script ending, and `Terminate` on a sandbox
   whose runner was in the middle of a job returned in 500 ms, had the
   sandbox gone within a minute, and did nothing, without error, when
   called again; GitHub marked that runner offline about two minutes
   later and ended its job six minutes later, so a job cut off this way
   finishes on GitHub's clock, not the sandbox's. *(observed)*
7. **Every call is bounded.** Thirty seconds per request, one minute per
   list, four minutes to start, two minutes at the HTTP client. The
   launcher's own bound sits outside them: it renews the runner's lease
   while a launch runs, for up to fifteen minutes, which a Daytona launch
   never needs. A hung Daytona cannot stall the reconciler.
8. **The API key never leaves the control plane.** It travels in one
   request header to Daytona's API and to the toolbox proxy, is never
   written into a sandbox, a log, an error or a response, and the dashboard
   shows it masked to its first and last three characters.
9. **What Daytona refuses, the definition cannot ask for.** Both allow lists
   at once, an IPv6 CIDR, more than ten CIDRs or a hundred domains, spot
   without a GPU, `latest` images, resources with a snapshot, Windows
   images and `provision_via` are config errors before any launch, with
   the message naming the field.

---

## The picture

```
┌────────────────── your Daytona organization ──────────────────────────────┐
│                                                                           │
│  region us (shared)      region eu (shared)      your dedicated region    │
│  ┌─────────────────────────────────────────────┐                          │
│  │ sandbox  airship-<runner>   (name = runner) │  labels                  │
│  │   PID 1: daytona's daemon, waiting          │   airship-installation   │
│  │   built FROM <image> at <n>vcpu-<m>gb,      │   airship-runner-id      │
│  │     or from your snapshot                   │   airship-repository     │
│  │   /tmp/airship-boot-<runner>.sh  (written)  │   airship-boot=started   │
│  │   session airship-runner: bash <script>     │     (set last)           │
│  │   actions/runner as user `runner`, one job  │                          │
│  │   dockerd inside, when docker: true         │                          │
│  │   auto-stop off · delete-on-stop on · TTL   │                          │
│  └─────────────────────────────────────────────┘                          │
│                                                                           │
│  No cache bucket — Daytona has no object storage. See "Caching".          │
└───────────────────────────────────────────────────────────────────────────┘
          ▲                                  ▲
          │ api-keys/current · regions       │ toolbox: proxy.app.daytona.io/toolbox/<sandbox>
          │ sandbox create/get/list/delete   │   process/session · process/execute ·
          │ sandbox labels                   │   session exec (runAsync)
          └──────────── Airship control plane ────────────────────────────────
                    holds one sealed organization API key
```

---

## Why a sandbox is not a VM

Four differences shaped the provider. Each explains a decision that looks
odd next to the AWS, GCP and Azure paths.

| Daytona | Consequence in Airship |
| --- | --- |
| **Nothing runs at boot.** A sandbox starts with Daytona's daemon as PID 1 and waits for commands; there is no user data, no startup script | The control plane boots the runner itself: it writes the boot script into the sandbox over the toolbox API and starts it in a process session. A launch is a sequence of steps (create, wait, write, start, label), each of which checks for its own earlier success, rather than one call |
| **There is no power button.** A process in the sandbox cannot `shutdown`; stopping the daemon (PID 1) stops the sandbox | Every exit path of the boot script ends with `kill -TERM 1`. The sandbox is created with `autoDeleteInterval: 0`, so a stopped sandbox is deleted at once: that is the `InstanceInitiatedShutdownBehavior=terminate` of this cloud. Auto-stop is disabled, since it would stop a sandbox mid-job |
| **Names are unique per organization**, and creating a taken name is a `409` | The runner's name *is* the sandbox name, so a repeated launch becomes a lookup, and a conflict, a 5xx or a lost answer is settled by reading the name back. A sandbox under that name that does not carry the runner's label is an error, not a match |
| **A sandbox without a started runner is worth nothing**, unlike a VM whose user data will run eventually | The provider never reports an *ambiguous* launch. Whatever it cannot finish it deletes and fails as retryable; the control plane makes a fresh runner. The `airship-boot` label is set last, and the inventory scan treats an unlabelled sandbox as a launch in progress for ten minutes, then as a leak to delete, never as something to revive |

Daytona builds a sandbox from a one-line Dockerfile (`FROM <image>`) and
caches the result, so the first launch of a new image pays for a build and
later ones start in seconds. A snapshot you prepared in Daytona skips the
build and fixes the resources.

---

## What Airship creates in your organization

| Resource | Name | Created when | Destroyed |
| --- | --- | --- | --- |
| Sandbox | the runner's name (`airship-...`) | One per job (or warm pool slot) | When the job ends: the script stops it and Daytona deletes it. Also at its TTL (the maximum lifetime plus fifteen minutes), and by the inventory scan when it is stopped, errored or unlabelled past the grace |
| Process session | `airship-runner` inside the sandbox | At boot | With the sandbox. Never deleted by Airship: deleting a session kills its processes, which would kill the runner |

No images, snapshots, volumes, API keys or anything else. Snapshots and
volumes a definition names must exist already.

Every sandbox carries:

| Label | Value |
| --- | --- |
| `airship-installation` | your GitHub installation ID — **the tenancy key**: listing filters on it, deleting refuses a sandbox without it |
| `airship-runner-id` | the runner's UUID |
| `airship-repository` | `owner/name` |
| `airship-boot` | `started`, set last, once the boot script is running. A sandbox without it has no runner |

---

## The launch, step by step

1. **Look the name up.** `GET /sandbox/<name>`. Found with the runner's
   label: continue with it (and return at once if it already carries
   `airship-boot`). Found without the label: fail. Not found: create.
2. **Create.** `POST /sandbox` with the labels, `target` (the region),
   `autoStopInterval: 0`, `autoDeleteInterval: 0`, `ttlMinutes` (the
   maximum lifetime plus fifteen minutes), and either `snapshot` or
   `buildInfo.dockerfileContent: FROM <image>` with `cpu`, `memory` and
   `disk`; plus `gpu: 1` and `gpuType` for a GPU definition, `spot` with it,
   `volumes`, `networkAllowList` and `domainAllowList`. A `409`, a 5xx or a
   lost answer is followed by step 1 again. A sandbox Daytona placed in a
   region other than the one asked for is deleted and the launch fails,
   because the inventory scan would never list it under its scope.
3. **Wait for `started`.** Polling every two seconds, up to four minutes
   (the launcher's fifteen-minute bound on a launch sits outside it). `build_failed` is
   permanent (the definition can never launch); `error`, `destroyed` (for
   instance after Daytona's own queue timed out waiting for capacity) and
   a timeout are retryable; `stopped` or `archived` here is an error.
4. **Open the session.** `POST /process/session` with `sessionId:
   airship-runner`; a `409` means an earlier attempt made it.
5. **Write the script.** One `POST /process/execute`: `printf` the script
   from base64 into `/tmp/airship-boot-<runner>.sh`, mode `0700`.
6. **Start it once.** Read the session; if it already runs `nohup bash
   <script>`, do nothing; otherwise submit that command with `runAsync`.
   Processes in a session outlive the request, and the session lives as
   long as the sandbox.
7. **Label.** `PUT /sandbox/<id>/labels` with the existing labels plus
   `airship-boot: started`, retried three times on transport and 5xx
   errors since failing here throws a booting sandbox away.

Any failure after step 2 first re-reads the sandbox: if it now carries the
boot label for this runner, another launcher racing on the same runner
finished the boot, and this launch returns that sandbox as its result.
Otherwise it deletes the sandbox (best effort, twenty seconds) and fails
the launch; the error is retryable for transport, capacity, quota
and 5xx failures and permanent otherwise. Daytona reports a shape above
your tier's per-sandbox maximum as a `400` whose message says `exceeds
maximum allowed per sandbox`; that is permanent, where other `exceeds` and
`quota` messages are waits for capacity.

The script itself, before anything else, takes a lock directory
(`/var/lib/airship-boot`) and only then arms its exit trap, so a second
start (a launch repeated after a lost answer in step 6) exits without
stopping the sandbox under the first.

---

## What the sandbox runs

The Daytona boot script is a sibling of the Linux one with the power
management replaced:

| Step | Detail |
| --- | --- |
| Become root | Toolbox commands run as the image's user: root on a plain image, a sudoer on Daytona's own snapshots. The script re-executes itself under `sudo -n` if needed |
| Install what is missing | `curl`, `tar`, `gzip`, `sudo`, `git`, `ca-certificates` via `apt-get` if absent; an image without `apt-get` that lacks them is refused with a clear message. Debian and Ubuntu images are the expected base |
| A `runner` user | Created with passwordless `sudo`, as on a GitHub-hosted runner |
| GPU | Nothing to install: a GPU sandbox has the host's driver. `nvidia-smi -L` is logged |
| Docker (`docker: true`) | Static Docker 28.3.3 from `download.docker.com` when the image has none, `iptables` via apt, `dockerd` started under `setsid nohup`, up to a minute for it to answer `docker info`, `runner` added to the `docker` group. If any of it fails the job still runs, without Docker, and the boot log says so |
| actions/runner | Reused if the image baked the requested version into `/opt/actions-runner`; downloaded otherwise, `installdependencies.sh` run |
| `.env` | Job variables from the definition, carried as base64 |
| Run | `timeout --kill-after=60 <max lifetime> sudo --user=runner env ACTIONS_RUNNER_INPUT_JITCONFIG=... ./run.sh`. The runner removes the config from its environment before any step runs |
| Exit | `kill -TERM 1`, then `kill -KILL 1` a minute later. The sandbox stops; Daytona deletes it |

Inside a sandbox `nproc` and `free` report the **host** (48 CPUs and
377 GB in our run), not the shape: CPU and memory are cgroup limits
(`/sys/fs/cgroup/cpu.max` read `200000 100000` and `memory.max` 4 GiB in
the `daytona-medium` sandbox). A build
that sizes its thread pool from `nproc` will oversubscribe a `2vcpu-4gb`
sandbox; pass the parallelism explicitly.

---

## Shapes, images and snapshots

Cirun has no Daytona cloud, so its fields were given meanings rather than
copied:

```yaml
runners:
  - name: build
    cloud: daytona
    instance_type: 4vcpu-8gb                 # a shape: vCPUs and GB of memory
    machine_image: "ghcr.io/acme/ci:2026.10" # a container image, tag or digest
    region: us
    labels: [airship-build]
    extra_config:
      disk: 10                               # GB; default 10
      docker: true

  - name: golden
    cloud: daytona
    instance_type: ci-golden                 # a snapshot you made in Daytona
    labels: [airship-golden]                   # image, cpu, memory and disk are its own
```

| Field | Daytona meaning |
| --- | --- |
| `instance_type` | `<n>vcpu-<m>gb` builds a sandbox of that size from `machine_image`; any other name is a snapshot in your organization, which fixes image and resources. A list is a fallback chain, as on the other clouds |
| `machine_image` | A container image with a tag or digest. `ubuntu2204` and `ubuntu2404` are the aliases shared with the other clouds (`ubuntu:22.04`, `ubuntu:24.04`); the default is `ubuntu:22.04`. `latest` is refused so a definition says what it runs; Windows images are refused; empty with a snapshot |
| `region` | A Daytona region ID: `us` or `eu` (shared), or a dedicated region of your organization. Default: the one chosen at connect time. Snapshots are per region, and a dedicated region may not offer image builds (ours answered `Declarative builds are not available to your organization in region us-west-4`), so a definition for one usually names a snapshot published there |
| `gpu` | One of Daytona's GPU types (`H100`, `H200`, `B200`, `B300`, `RTX-PRO-6000`, `RTX-4090`, `RTX-5090`, `MI355X`), one per sandbox; adds the `gpu` label |
| `preemptible` | Spot capacity, GPU sandboxes only (Daytona offers no spot CPU sandboxes), so it needs `gpu` |
| `extra_config.disk` | Disk in GB for a sandbox built from an image; default 10. Your tier sets the per-sandbox maximum (10 GB on the entry tier). Refused when every variant is a snapshot |
| `extra_config.docker` | Start a Docker daemon in the sandbox |
| `extra_config.volumes` | Up to ten Daytona volumes: `volume` (name or ID), `mount_path`, optional `subpath`. Volumes are S3-backed and shared between sandboxes |
| `extra_config.network_allow_list`, `domain_allow_list` | One egress policy per sandbox: up to 10 IPv4 CIDRs, or up to 100 domains and wildcards, never both. **Tier 3 and 4 only**: on Tier 1 and 2 the organization's restriction applies and Daytona refuses a sandbox-level list, which Airship reports as a permanent launch error. Where allowed, enforcement is strict: the runner needs `github.com`, `api.github.com`, `*.actions.githubusercontent.com`, `pipelines.actions.githubusercontent.com` and `objects.githubusercontent.com`, the image needs its package mirrors, and `docker: true` needs `download.docker.com` |
| `provision_via`, the Windows keys | Refused: a sandbox runs nothing at boot, and runs Linux containers only |

Runners register with `self-hosted`, `x64`, `daytona`, `cpu` or `gpu`, and
the definition's labels; a job asking for `windows` never matches a Daytona
runner, whatever its image.

---

## Caching

`cache: true` on a Daytona runner launches **without** the cache proxy and
records a `cache.unavailable` warning on the Events page, as on GCP and Azure. Daytona
has no object storage to back it. The job runs normally against GitHub's
own cache.

---

## Keeping the organization clean

Same loop as the other clouds, with Daytona's states mapped:

| It sees | It does |
| --- | --- |
| A sandbox labelled for the installation with `airship-boot` and no live runner row | Deletes it |
| A runner row whose sandbox is no longer listed (after a two-minute grace) | Marks the runner `lost`; retries its job if still queued |
| A `stopped`, `archived`, `paused`, `error` or `build_failed` sandbox | Treats it as gone and deletes it: nothing runs in it and nothing will |
| A `stopping` or `destroying` sandbox | Waits |
| A sandbox without `airship-boot`, younger than ten minutes | Leaves it alone: a launch may still be at work (its steps take about seven and a half minutes at worst once the sandbox exists) |
| A sandbox without `airship-boot`, older than ten minutes | Treats it as gone and deletes it: a crashed launch left it, and no runner will ever start in it |
| A runner whose lease expired but whose labelled sandbox exists | Revives it, after GitHub confirms the registration still exists |
| A list that fails | Nothing. No sandbox is presumed gone |

Listing is `GET /sandbox` filtered by the installation label, two hundred
per page, and keeps the sandboxes whose `target` is the scanned region; the
label on each item is checked again, since the filter is a request and the
label is the proof. Daytona's list is **eventually consistent**: a new
sandbox appears a few seconds after creation (four seconds in our runs),
which the two-minute instance grace absorbs. Deleting reads the sandbox
first and refuses one whose `airship-installation` label is not the
installation's.

Every call is bounded: thirty seconds per request, one minute for a whole
list, two minutes at the HTTP client.

---

## How Daytona errors are classified

| Class | Examples | Behaviour |
| --- | --- | --- |
| **Retryable** | `429`, `408`, any 5xx, a transport error; a `400`, `403` or `409` whose message speaks of quota, capacity, a limit exceeded or too many requests (`Total memory limit exceeded. Maximum allowed: 10GiB`, the organization's total, which Daytona charges creates in flight against); a sandbox that reached `error` or was destroyed while starting (Daytona's queue timed out); a start that took more than four minutes | The chain starts over, up to the attempt cap |
| **Permanent** | `build_failed` (the image does not exist or does not build); a shape above the tier's per-sandbox maximum (`exceeds maximum allowed per sandbox`); an allow list on a tier that forbids them (`Network access is restricted and cannot be overridden at the sandbox level`); no GPU credits (`Organization doesn't have GPU credits`); a region without builds or without the snapshot; a `400` on the request body; a sandbox placed in another region | Next fallback; stop once every attempt has been tried |
| **Credential** | `401`, `403` on the key itself, a key with no organization, an expired key, a key without `write:sandboxes` or `delete:sandboxes` | Refused at connect time; at launch time the failure is on the Events page with Daytona's message. Airship then checks the credential again (at most every five minutes) and, if the cloud refuses it, the Cloud page shows **Credentials rejected** with the cloud's message and Events records `cloud.credentials_rejected`; reconnecting, or the cloud accepting it again, clears it |
| **Never ambiguous** | — | A launch the provider cannot finish deletes its sandbox and fails retryably. There is no `launch_unconfirmed` on Daytona |

---

## Daytona features weighed and left out

Each was read in the API (v0.226.1) and, where the organization allows,
tried; none is used, for the reason given.

| Feature | What it offers | Why Airship does not use it |
| --- | --- | --- |
| Webhooks (`sandbox.state.updated`, `sandbox.created`, snapshot and volume events, delivered through Svix) | A push when a sandbox changes state, instead of the inventory scan noticing | Endpoints are configured per organization in Svix's portal, not per API key, so Airship cannot register its own URL at connect time; the events carry only the state, which the scan already reads every cycle; and a push would add a second source of truth for runner state next to the lease and the list. This organization has webhooks uninitialized (`Webhook initialization status not found`). The two-minute instance grace is the cost of not having them |
| Warm pools (`/warm-pools`: pre-started sandboxes of a snapshot per region) | A sandbox from a snapshot ready before the launch | The route answers `Cannot GET` on `app.daytona.io` for this organization, and Airship's own warm pool (`idle_pool`) already keeps registered runners waiting, which a pre-started sandbox without a runner would not be |
| Secret vault (`secrets` on create: env var name to vault secret) | Credentials mounted as environment variables | The one credential a sandbox needs, the JIT config, is single-use and per launch; a vault entry per launch would be one more object to create and delete for no gain, and the toolbox write it replaces is already visible only to the key holder |
| `kvm`, `linkedSandbox`, `outboundProxyUrl`, `networkBlockAll`, `public`, preview links, SSH access | Nested virtualization, sandbox-to-sandbox links, an egress proxy, no network at all, a public port, browser or SSH access to the sandbox | A runner needs outbound HTTPS to GitHub and nothing inbound; Docker runs in the privileged container without KVM. `networkBlockAll` is the one network control a Tier 1 or 2 organization could set and it would cut the runner off |
| Audit logs (`/organizations/<id>/audit`) | Who did what to the organization | Daytona's dashboard shows them; Airship records its own events per runner |
| Windows and `linux-vm` classes | Windows Server 2025 sandboxes, KVM Linux VMs | Gated by tier (Windows) or absent from the organization's class list (VMs); see the limits below |

---

## Limits to check before you scale

| Limit | Why it bites |
| --- | --- |
| Your tier's concurrent sandboxes and total vCPU, memory and disk | The real ceiling on concurrent runners. Set `max_runners` under it so the ceiling is a dashboard message rather than a stream of `exceeds` errors |
| Per-sandbox maximum disk, CPU and memory | A shape above it is a permanent config error, reported with Daytona's message. 10 GB disk on the entry tier |
| API key expiry | Daytona keys can expire. Verification refuses an expired key; one that expires later fails every launch from then on, so reconnect with a new key before that date |
| GPU credits | A GPU sandbox needs GPU credits (spot credits for `preemptible: true`) in the organization's wallet; without them Daytona refuses the create and Airship moves to the next fallback. With them, spot GPU sandboxes may wait in Daytona's queue and be destroyed when it times out; Airship retries on the next attempt |
| Network tier | Tier 1 and 2 cannot set a sandbox-level allow list; Tier 3 and 4 can, strictly. `GET /organizations/<id>/usage` shows the per-region quotas Daytona enforces |
| Dedicated regions and sandbox classes | A region in the organization's list may host a sandbox class other than `container` (`GET /organizations/<id>/available-sandbox-classes` says which: ours lists `us` for containers, `us-west-4` for Windows only, and a GPU-capable region that is not a valid `target`). Connecting with a region that hosts no containers is refused with the regions that do. Snapshots are per region and image builds may be unavailable in a dedicated one; the shared `us` and `eu` are always there |
| Windows and `linux-vm` sandboxes | Daytona also offers Windows Server 2025 sandboxes (`windows-small` to `windows-xlarge`) and KVM Linux VMs (`daytona-vm-*`), but Windows is refused for Tier 1 and 2 organizations (`Windows sandboxes are not available for Tier 1 and Tier 2 organizations`) and the VM class is not in this organization's class list. Airship runs Linux containers on Daytona; a Windows job belongs on an EC2, Compute Engine or Azure image |
| GPU capacity | `GET /organizations/<id>/gpu-capacity` reports Daytona's fleet per type on demand and spot (hundreds of H100 and RTX-PRO-6000, a few dozen H200, RTX-4090 and RTX-5090, no B300 or MI355X when we looked); it is the fleet, not the organization's quota, and a sandbox still needs GPU credits |

---

## What to watch on first use

1. **The first launch of each new image.** Daytona builds it (`FROM
   <image>`) before the sandbox starts; a build that takes longer than
   four minutes fails the launch as retryable, and the next attempt finds
   the cached build.
2. **A GPU sandbox.** The request has been sent and refused for credits, in the shared region and in the GPU-capable one;
   none has started. The first one that does is the thing to verify, with
   the Events page open, in particular that `nvidia-smi` appears in the
   boot log.
3. **`nproc` inside the sandbox.** It is the host's. Builds that derive
   parallelism from it need `-j` set explicitly.
4. **The organization's membership.** Anyone with an organization API key
   can run commands in any sandbox through the toolbox API; see
   [security.md](security.md#known-exposures-stated-plainly), exposure 8.
