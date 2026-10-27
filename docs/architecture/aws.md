# Airship on AWS

**What this is.** Everything Airship does inside an AWS account: which API
calls it makes, which resources it creates, how it bounds them, and how it
behaves when EC2 says no.

**Audience.** Cloud and platform engineers who have to approve the IAM
policy and explain the blast radius. For the hands-on path, read
[aws-setup.md](aws-setup.md) next.

**Status.** The EC2 provider runs against [moto](https://github.com/getmoto/moto)
in CI (request shapes, tags, default VPC and security group, lifecycle
rules, termination) and the rendered Linux boot script has run a real
GitHub job end to end. A launch on real EC2 has not been part of CI; an
opt-in test (`TestAgainstLiveAWS`) launches and terminates one instance
against a real account when credentials are supplied. The first real
deployment exercises exactly the `RunInstances` call moto accepted.

---

## The picture

```
┌──────────────────────── your AWS account ─────────────────────────────────┐
│                                                                           │
│  region us-east-1                          region eu-west-1               │
│  ┌──────────────────────────────────┐      ┌────────────────────────────┐  │
│  │ default VPC (or your SubnetId)   │      │ …                          │  │
│  │                                  │      │                            │  │
│  │  subnet-a   subnet-b   subnet-c  │      │                            │  │
│  │     │                            │      │                            │  │
│  │     ▼                            │      │                            │  │
│  │  ┌────────────────────────────┐  │      │                            │  │
│  │  │ i-0abc…  one job, one VM   │  │      │                            │  │
│  │  │  sg: airship-runners       │  │      │                            │  │
│  │  │      no inbound rules      │  │      │                            │  │
│  │  │  IMDSv2 required, hops=1   │  │      │                            │  │
│  │  │  root EBS, encrypted       │  │      │                            │  │
│  │  │  shutdown ⇒ terminate      │  │      │                            │  │
│  │  └─────────────┬──────────────┘  │      │                            │  │
│  └────────────────┼─────────────────┘      └────────────────────────────┘  │
│                   │ cache traffic (optional)                               │
│                   ▼                                                        │
│  ┌──────────────────────────────────────────┐                              │
│  │ s3://airship-cache-us-east-1-<hash>      │  private, public access      │
│  │   expire objects after 7 days            │  blocked, SSE-S3,            │
│  │   abort stale multipart after 1 day      │  owner-checked on every call │
│  └──────────────────────────────────────────┘                              │
│                                                                           │
└───────────────────────────────────────────────────────────────────────────┘
          ▲                                     ▲
          │ RunInstances, DescribeInstances,     │ GetFederationToken
          │ TerminateInstances, DescribeImages,  │ (hour-long, prefix-scoped)
          │ DescribeSubnets, CreateSecurityGroup │
          └──────────── Airship control plane ───┘
                    holds one sealed IAM access key
```

---

## What Airship creates in your account

| Resource | Name / identity | Created when | Destroyed |
| --- | --- | --- | --- |
| EC2 instance | `Name` = the runner's name (`airship-…`) | One per job (or per warm pool slot) | When the job ends, by the control plane and by the machine itself |
| EBS root volume | tagged like its instance | With the instance | With the instance (`DeleteOnTermination`) |
| Security group | `airship-runners`, one per VPC | First launch in a VPC that names no group | Never — it is empty of inbound rules and harmless. Delete it by hand if you stop using Airship |
| S3 bucket | `airship-cache-<region>-<12 hex>` | First cache-enabled launch in a region | Never. Objects expire after 7 days |

That is the whole list. No roles, no instance profiles, no VPCs, no NAT
gateways, no Elastic IPs, no CloudFormation stacks, no Lambda, no
EventBridge rules. Nothing is left running between jobs unless you
configured an idle pool.

Every instance and volume carries four tags:

| Tag | Value | Used for |
| --- | --- | --- |
| `airship:installation` | your GitHub installation ID | **Tenancy.** Listing filters on it; terminating refuses an instance without it |
| `airship:runner-id` | the runner's UUID | Matching an instance back to its row |
| `airship:repository` | `owner/name` | Cost allocation, your own queries |
| `Name` | the runner's name | The EC2 console |

Those tags are how Airship can share an account with your own workloads
safely: it only ever lists and terminates what it tagged.

---

## The launch call

One `RunInstances` per runner, `MinCount` = `MaxCount` = 1:

| Field | Value | Why |
| --- | --- | --- |
| `ClientToken` | `<runner-id>-<subnet attempt>-<spot>` | Idempotency. A retried call returns the instance the first call made instead of creating a second |
| `ImageId` | resolved AMI (see below) | — |
| `InstanceType` | from `instance_type` | — |
| `SubnetId` | one default subnet, rotated per runner | Spreads across AZs; survives one AZ being out of capacity |
| `SecurityGroupIds` | `airship-runners`, or yours | No inbound rules; the default egress rule stays |
| `InstanceInitiatedShutdownBehavior` | `terminate` | The machine's own `shutdown` destroys it, with or without us |
| `MetadataOptions` | IMDSv2 required, endpoint enabled, hop limit 1 | A job in a container cannot reach the instance metadata service |
| `BlockDeviceMappings` | the AMI's root device, sized, **always encrypted**, `DeleteOnTermination` | — |
| `TagSpecifications` | the four tags, on instance *and* volume | Tenancy, see above |
| `UserData` | the rendered boot script, base64, ≤ 16 KiB | Omitted entirely with `provision_via: pull_from_api` |
| `InstanceMarketOptions` | spot, one-time, interruption = terminate | Only when `preemptible: true` |
| `IamInstanceProfile` | only if `extra_config.IamInstanceProfile` names one | Opt-in; every job on that runner can use it |

**Subnet rotation.** With no `SubnetId` override, Airship describes the
region's default subnets (`default-for-az=true`), sorts them by AZ and picks
a starting index from a hash of the runner ID — deterministically, because a
repeated launch must send the *same* request with the *same* client token or
EC2 answers `IdempotentParameterMismatch` instead of returning the original
instance. Capacity-shaped failures (`InsufficientInstanceCapacity`,
`Unsupported`, `InsufficientFreeAddressesInSubnet`, `SpotMaxPriceTooLow`)
move to the next subnet; anything else stops and hands the job to the next
fallback attempt in its plan.

**Lost answers.** If the SDK call fails without an answer from EC2 — a
timeout, a reset connection — the request may still have been received.
Airship describes instances by client token to find out. If even that fails,
the launch is recorded as `launch_unconfirmed`: the runner keeps its GitHub
registration, the job is retried on a fresh runner, and the next inventory
scan either revives the original (its machine turned up) or writes it off.
No instance is ever presumed absent because a call failed.

---

## Networking

**Default path.** The region's default VPC, every default subnet, and an
`airship-runners` security group Airship creates once per VPC with **no
inbound rules** and the default allow-all egress rule. Runner machines get a
public IP from the default subnet's auto-assign setting and reach GitHub
directly.

**Your own network.** Two `extra_config` keys take over:

```yaml
extra_config:
  SubnetId: subnet-0123456789abcdef0      # launch here instead
  SecurityGroupIds: [sg-aaa, sg-bbb]      # up to 5, instead of airship-runners
```

With `SubnetId` set, Airship launches only there — no AZ rotation, so an AZ
capacity shortage becomes a launch failure and moves to the next fallback.
If that subnet is private, it needs a NAT gateway or equivalent: runners
make only outbound connections, but they need these:

| Destination | Why | Needed when |
| --- | --- | --- |
| `github.com`, `*.githubusercontent.com` | Download `actions/runner`; `actions/checkout` uses `codeload.github.com` | Always |
| `*.actions.githubusercontent.com` (incl. `pipelines.` and `results-receiver.`) | The runner's own control channel, logs and results | Always |
| `archive.ubuntu.com`, `security.ubuntu.com` | `installdependencies.sh` on a stock Ubuntu AMI | Always on stock images; bake a custom AMI to avoid it |
| your control plane's `PUBLIC_URL` (443) | Fetch the cache proxy binary and its hour-long credentials | `cache: true` only |
| `s3.<region>.amazonaws.com` | The cache bucket. A gateway VPC endpoint for S3 keeps this off the internet and off your NAT bill | `cache: true` only |
| whatever your jobs need | npm, PyPI, Docker Hub, your registries | Your call |

There is **no inbound requirement at all**. No SSH, no port 22, no bastion,
no VPN. Airship never connects *to* a runner; the runner connects out.

---

## Images

| `machine_image` | Resolves to |
| --- | --- |
| *(omitted)* or `ubuntu2204` | Canonical's newest Ubuntu 22.04 for the instance's architecture, from Canonical's AWS account (`099720109477`), state `available`, newest `CreationDate` first |
| `ubuntu2404` | the same for 24.04 |
| `ami-0123456789abcdef0` | exactly that AMI |
| a list | each entry is one fallback attempt |

Resolution results are cached for an hour per (credential, region, image,
architecture).

Three checks happen before the launch, so a bad combination is a config
error rather than a failed EC2 call:

- **Architecture.** The architecture comes from the instance type (Graviton
  families `c7g`, `m8g`, `t4g`, … and Arm families `t2a`, `c4a` are `arm64`;
  everything else `x64`). An AMI whose architecture is known and differs is
  refused by name.
- **Root device.** The AMI's own root device name is what gets sized, so
  `DeviceName` in `BlockDeviceMappings` is accepted and ignored.
- **Platform.** A Windows AMI gets the Windows boot script (PowerShell, run
  by EC2Launch as SYSTEM), including the `run_as: interactive` variant that
  logs a local account into a desktop session.

Custom AMIs are fully supported and are the right answer for slow installs:
bake the runner tarball at `/opt/actions-runner` with a matching `.version`
file and the boot script skips the download entirely.

---

## Storage

```yaml
extra_config:
  BlockDeviceMappings:
    - VolumeSize: 100           # 8–16384 GiB, default 75
      VolumeType: gp3           # gp2 (default, as Cirun), gp3, io1, io2
      Iops: 6000                # gp3 3000–16000; io1/io2 required
      Throughput: 1000          # gp3 only, 125–1000 MiB/s
      VolumeInitializationRate: 300   # 100–300 MiB/s, snapshot-backed AMIs
```

One entry, the root volume, always encrypted, always deleted with the
instance. Combinations EC2 would refuse are rejected as config errors — for
instance `Throughput: 1000` needs `Iops: 4000`, because EBS allows at most
a quarter MiB/s of throughput per IOPS.

**The default is gp2 at 75 GiB, matching Cirun.** On anything disk-bound —
a Docker build, a large cache restore, a monorepo checkout — gp3's default
125 MiB/s is usually the ceiling you are hitting, not the network and not
the CPU. `VolumeType: gp3`, `Iops: 6000`, `Throughput: 1000` is the single
highest-value line in most `airship.yaml` files.

---

## Spot, GPUs and instance profiles

**Spot.** `preemptible: true` launches a one-time spot instance whose
interruption behaviour is `terminate`. A preempted machine is destroyed, its
job fails as GitHub reports it, and the inventory scan cleans the row. Spot
pairs naturally with fallback lists:

```yaml
instance_type: [c7i.4xlarge, c7i.4xlarge, m7i.4xlarge]
preemptible:   [true,        false,       false]
```

— try spot, then on-demand of the same shape, then a different family.

**GPUs.** `gpu: nvidia-tesla-t4` (any name) adds the `gpu` label and, on
Ubuntu images without a driver, installs one via `ubuntu-drivers` at boot.
NVIDIA's own AMIs need no driver install and are faster to boot.

**Instance profiles.** `IamInstanceProfile: my-profile` attaches a role you
control. Note what it means: *every step of every job on that runner* can
use it, including a fork pull request's code if the repository is public and
activated for public access. Treat it as you would a secret in the
repository.

---

## Caching

`extra_config.cache: true` gives drop-in `actions/cache` acceleration backed
by an S3 bucket in your own account, in the runner's region — no size limit
beyond your bucket, and no egress out of your account.

```
runner step          cache proxy (127.0.0.1)            your S3 bucket
───────────          ───────────────────────            ──────────────
actions/cache  ──►   terminates TLS for the             reserve/commit as a
                     results host only; answers         multipart upload,
                     reserve/upload/finalize/lookup     8 × 64 MiB in parallel
                     itself                        ──►  straight from disk
                     everything else on that host
                     is forwarded to GitHub
                     untouched; every other host
                     is tunnelled uninspected
```

| Step | Detail |
| --- | --- |
| Bucket | `airship-cache-<region>-<hash>`, created on first use, **owner-checked on every call** so a same-named bucket in another account is refused. Private, public access blocked, SSE-S3, objects expire after 7 days, incomplete multipart uploads aborted after 1 day |
| Credentials | The machine gets a one-off `asr_` token in its boot script. The proxy trades it at `GET /v1/runner/cache-grant` for an **STS federation token** — valid an hour, refreshed before expiry, narrowed by an inline policy to that bucket. **Your access key never reaches the machine** |
| Ceiling | A federation token can never exceed the IAM user's own permissions, so the S3 statements in your policy are the hard ceiling on what any runner can do |
| Trust | The proxy generates its own CA on the machine, valid one day, trusted only there (system store plus `NODE_EXTRA_CA_CERTS`) |
| Compatibility | `actions/cache`, `actions/setup-*` (`cache: npm` and friends), `Swatinem/rust-cache`, `astral-sh/setup-uv`, anything on `@actions/cache`. `docker/build-push-action` needs `url_v2=http://127.0.0.1:6420/` and `driver-opts: network=host` |
| Degradation | If the bucket cannot be created, the binary is missing from the build, or the proxy does not start, **the job still runs** without acceleration and the dashboard's Events page records why |

Two things to know before you turn it on:

- **Scope.** As on Cirun, every repository in the organization shares the
  bucket and there is no per-branch isolation: a pull request's job can
  write an entry a main-branch job restores. Keep public repositories with
  fork pull requests off cache runners, or make their keys unguessable.
- **Containers.** Steps in a `container:` job inherit `https_proxy` but not
  the host's trust store. Run cache steps on the host.

Full detail, including the exact IAM statements: [`../caching.md`](../caching.md).

---

## Keeping the account clean

The reconciler holds an inventory loop per (credential, region), every 15
seconds:

| It sees | It does |
| --- | --- |
| An instance tagged for the installation with no live runner row | Terminates it. This is the orphan sweeper |
| A runner row whose instance is no longer listed (after a 2-minute grace period) | Marks the runner `lost`; retries its job if the job is still queued |
| A *stopped* instance | Treats it as gone and terminates it — it runs no job and still bills for its disk |
| A runner whose lease expired, or whose launch was unconfirmed, but whose instance *did* launch | Revives the runner, after GitHub confirms the registration still exists |
| A list that **fails** | Nothing. An instance missing from a failed list is never presumed gone |

Termination happens twice over: once the moment the `completed` webhook
arrives, and again from the reconciler if the cloud still lists the
instance 5 minutes later. And a third time by the machine itself, which
powers off when the runner exits and at its 8-hour deadline, with
`InstanceInitiatedShutdownBehavior=terminate` turning that into a delete.

---

## How EC2 errors are classified

| Class | Examples | Behaviour |
| --- | --- | --- |
| **Capacity, per-AZ** | `InsufficientInstanceCapacity`, `Unsupported`, `InsufficientFreeAddressesInSubnet`, `SpotMaxPriceTooLow` | Next subnet, then the job's next fallback attempt |
| **Retryable** | `InstanceLimitExceeded`, `VcpuLimitExceeded`, `RequestLimitExceeded`, `Throttling`, `InternalError`, `ServiceUnavailable`, `MaxSpotInstanceCountExceeded` | The chain starts over, up to the attempt cap (3, or one per attempt) |
| **Ambiguous** | timeout, connection reset, EOF *after* the request was sent | Describe by client token; if that fails, `launch_unconfirmed` and the scan settles it |
| **Permanent** | a missing AMI, an instance type the account cannot launch, a bad subnet | Move to the next fallback; stop once every attempt has been tried |
| **Credential** | `AuthFailure`, `InvalidClientTokenId`, `SignatureDoesNotMatch`, `ExpiredToken` | The credential is marked invalid in the dashboard; nothing is retried blindly |

Every message that reaches the dashboard carries the EC2 error code and
message. No credential value is ever echoed into an error.

---

## Quotas to check before you scale

| Quota | Why it bites | Where |
| --- | --- | --- |
| Running On-Demand Standard vCPUs (`L-1216C47A`) | The real limit on concurrent runners. A `c7i.4xlarge` is 16 vCPUs — a default 32-vCPU account runs two | Service Quotas → EC2 |
| All Standard Spot Instance Requests | Same, for `preemptible: true` | Service Quotas → EC2 |
| Running On-Demand G/P instances | GPU runners have their own quota, often 0 by default | Service Quotas → EC2 |
| EBS gp3 / gp2 storage (TiB) | 100 GiB per runner × 50 concurrent runners is 5 TiB | Service Quotas → EBS |
| Default VPC present in the region | Airship says so explicitly if missing: create one or set `SubnetId` | per region |
| Free IPs in your subnets | `InsufficientFreeAddressesInSubnet` is treated as a capacity error and rotates AZs | your VPC |
| Rules per security group | Only if you supply your own groups | your VPC |

Airship's own `max_runners` should be set *below* what your quotas allow, so
you hit a clear dashboard error rather than a wall of EC2 failures.

---

## What this costs, and what bounds it

You pay AWS directly: instance-seconds, EBS gigabyte-months for the life of
each machine, the data your jobs transfer, and S3 storage for cache entries
(expired after 7 days). Airship adds no infrastructure that idles.

The bounds are structural: one machine per job and no reuse; a hard 8-hour
machine lifetime enforced by the machine itself; a 10-minute idle reaper for
machines that never get a job; `max_runners` as the concurrency ceiling;
pool runners retired at half the lifetime; and a cap on failed launches per
job so a broken config cannot loop.

The levers that actually move the bill are spot capacity via `preemptible`,
right-sizing `instance_type` per workflow rather than per repository, gp3
throughput (a faster job is a cheaper job), and cache acceleration to turn
minutes of download into seconds.

---

## Looking at it from inside the account

- **EC2 console**, filtered on tag `airship:installation` — every machine
  Airship has running, with its repository on each row.
- **Cost Explorer**, grouped by tag `airship:repository` — spend per
  repository, no extra instrumentation.
- **CloudTrail** — every call Airship made, under the IAM user you
  connected. `RunInstances`, `TerminateInstances` and `GetFederationToken`
  are the three worth an alarm.
- **Airship's Events page** — the same story from the control plane's side,
  including why a launch failed or why a cache was unavailable.

Regions are yours to choose, per runner definition, so data residency is a
`region:` line in `airship.yaml`. The control plane stores job metadata only;
nothing from inside a job leaves your account unless your job sends it.
