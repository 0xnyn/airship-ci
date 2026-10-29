# Airship on Google Cloud

**What this is.** Everything Airship does inside a Google Cloud project:
the Compute Engine calls, the resources it creates, and the places where
Compute Engine's model forced different decisions from EC2's.

**Audience.** Cloud and platform engineers approving the service account's
roles. For the hands-on path, read [gcp-setup-console.md](gcp-setup-console.md) next.

**Status — read this first.** The Compute Engine provider is driven through
Google's real `cloud.google.com/go/compute` client against a hand-written
fake of the REST API, built from Google's reference: request IDs, zone
operations, per-zone lists with unreachable zones,
`ZONE_RESOURCE_POOL_EXHAUSTED`, quota errors arriving after the insert was
accepted, dropped connections, hung operations, image families, machine
images, subnetworks, routers. The tests cover idempotent relaunch, zone
fallback, pinned zones, every error class, lost answers, ownership checks on
delete, region-scoped listing, NAT creation per network, and Shielded VM
options following the image.

**No GCP project has been available to run against.** The exact request
bodies have therefore never been accepted by Google. Treat the GCP provider
as unexercised against the real API, however thorough the fake. The four
things to watch on a first real launch are listed at the end of this
document. An opt-in test (`TestAgainstLiveGCP`) launches one `e2-micro`,
checks idempotent relaunch, listing and the ownership refusal, and deletes
it — one machine's minutes, and the fastest way to close this gap.

---

## The picture

```
┌────────────────── your Google Cloud project ──────────────────────────────┐
│                                                                           │
│  region us-central1                                                       │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ zones us-central1-a / -b / -c / -f   (tried in a per-runner order)  │   │
│  │                                                                     │   │
│  │  ┌──────────────────────────────────┐                               │   │
│  │  │ instance  airship-<runner>       │  network tag airship-runner   │   │
│  │  │   one job, one VM                │  labels  airship-installation │   │
│  │  │   no service account (default)   │          airship-runner-id    │   │
│  │  │   automaticRestart: false        │  metadata startup-script,     │   │
│  │  │   vTPM + integrity monitoring    │           airship-repository  │   │
│  │  │   boot disk pd-balanced, deleted │                               │   │
│  │  │     with the instance            │                               │   │
│  │  └────────────────┬─────────────────┘                               │   │
│  │                   │ egress                                          │   │
│  │   with a public IP ├──────────────────────► internet                 │   │
│  │                   │                                                  │   │
│  │   enable_public_ip: false                                            │   │
│  │                   └──► Cloud Router + NAT  "airship-nat"             │   │
│  │                        (one per region per network, shared)          │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                           │
│  No cache bucket on GCP yet — see "Caching" below.                        │
└───────────────────────────────────────────────────────────────────────────┘
          ▲
          │ instances.insert / .get / .list / .delete · regions.get
          │ images.get / .getFromFamily · machineImages.get · projects.get
          │ routers.get / .insert · subnetworks.get · zoneOperations.wait
          └──────────────── Airship control plane ──────────────────────────
                      holds one sealed service account key
```

---

## Why Compute Engine is not EC2

Five differences shaped the provider. They are worth understanding, because
they explain decisions that look odd next to the AWS path.

| Compute Engine | Consequence in Airship |
| --- | --- |
| **Creating an instance is an asynchronous operation.** The insert is accepted, then capacity and quota failures arrive when the operation finishes | The provider waits for the zone operation (up to 3 minutes) before calling a launch successful. A failure *after* acceptance is read from the operation's error codes, not from the HTTP status |
| **Instances are named, not numbered** | The runner's name *is* the instance name, so a repeated launch becomes a lookup: if an instance with that name exists and carries the runner's label, it is the one. Belt and braces with the request ID |
| **Requests carry a `requestId`** | Derived deterministically from (runner, zone, attempt) with a UUIDv5. A retried insert returns the first operation instead of making a second machine |
| **A VM that powers itself off is not deleted.** It stays `TERMINATED` and keeps billing for its disk | The boot script's shutdown stops compute charges immediately; the inventory scan treats a `TERMINATED` (or `SUSPENDED`) machine as gone and deletes it, disks and all. Airship deliberately does **not** give the machine a service account that could delete itself |
| **Startup scripts run on every boot** | The Linux script writes `/var/lib/airship/booted` and the Windows script a marker file. A rebooted machine powers off instead of registering a second time — the JIT registration was single-use and its job is over |

Spot VMs are the one case that behaves like EC2: `provisioningModel: SPOT`
with `instanceTerminationAction: DELETE`, so a preempted machine is deleted
rather than left `TERMINATED`.

---

## What Airship creates in your project

| Resource | Name | Created when | Destroyed |
| --- | --- | --- | --- |
| Instance | the runner's name | One per job (or warm pool slot) | When the job ends; disks go with it (`autoDelete`) |
| Boot disk | the instance's | With the instance | With the instance |
| Extra data disk | device `airship-data` | Only with `extra_config.additionalDisk` | With the instance |
| Cloud Router + NAT | `airship-nat` on the default network, `airship-nat-<network>` on any other | First `enable_public_ip: false` launch in a region on that network | Never — later runners share it. Delete by hand if you stop using Airship |

No firewall rules, no service accounts, no custom networks, no images, no
instance templates, no MIGs, no Deployment Manager, nothing else.

Every instance carries:

| | Key | Value |
| --- | --- | --- |
| Label | `airship-installation` | your GitHub installation ID — **the tenancy key**: listing filters on it, deleting refuses an instance without it |
| Label | `airship-runner-id` | the runner's UUID |
| Metadata | `airship-repository` | `owner/name` (a label value cannot contain `/`, so the repository lives here) |
| Network tag | `airship-runner` | for *your* firewall rules. Airship adds none of its own |

That network tag is the hook for your network policy: write an egress rule
against `airship-runner` and it applies to every runner, present and future,
without Airship knowing about it.

---

## Zones, regions and fallback

The config field is called `region` but accepts a zone as well as a
region:

```yaml
region: us-central1-b     # pinned: this zone or nothing
region: us-central1       # any zone in the region with capacity
region: [us-central1, europe-west4]   # a fallback list, as on AWS
```

With a region, the provider reads the region's zone list (`regions.get`,
cached an hour) and tries zones in an order derived from the runner ID —
deterministic, because a retried launch must try the same zones in the same
order with the same request IDs to find a machine an earlier attempt made.

It moves to the next zone **only** for zone-specific failures:
`ZONE_RESOURCE_POOL_EXHAUSTED`, or a machine type the zone does not offer.
Everything else stops and hands the job to its next fallback attempt. A
pinned zone never rotates.

The control plane tracks runners per *region*, and the provider picks the
zone. One consequence worth knowing: a runner's stored region carries its
project when `extra_config.project_id` overrides the credential's
(`us-central1@my-other-project`), because the inventory scan lists one
(credential, region) at a time and the project has to be part of what it
lists.

**Listing is per zone, on purpose.** The SDK's aggregated list has no
`returnPartialSuccess` switch, so an unreachable zone *anywhere in the
world* fails the whole call. Airship lists each zone of the region
separately: only a zone of that region can fail the call, and when one does
the whole list fails rather than silently hiding that zone's instances — an
instance missing from a failed list is never presumed gone.

---

## Images

| `machine_image` | Resolves to |
| --- | --- |
| *(omitted)* or `ubuntu2204` | the `ubuntu-2204-lts` family (`-arm64` variant on Arm machines) |
| `ubuntu2404` | `ubuntu-2404-lts-amd64` / `ubuntu-2404-lts-arm64` |
| `ubuntu-2204-lts`, `debian-12`, `windows-2022`, `rocky-linux-9`, … | an image or family in **your** project first, then the public project the prefix implies (`ubuntu-os-cloud`, `debian-cloud`, `windows-cloud`, `cos-cloud`, `rocky-linux-cloud`, `centos-cloud`, `rhel-cloud`, `fedora-cloud`, `suse-cloud`) |
| `my-project:my-image` | an image, machine image or family in that project |
| `projects/P/global/images/N` | exactly that image |
| `projects/P/global/machineImages/N` | exactly that machine image — the instance is created from it with `sourceMachineImage`, so the machine image's own disks apply |
| either one's full self-link | the same |
| a list | each entry is one fallback attempt |

Resolution is cached an hour. An image whose architecture is known and
differs from the machine type's is refused before the launch, and a Windows
image gets the Windows startup script (`windows-startup-script-ps1`).

**Shielded VM.** When the image advertises `UEFI_COMPATIBLE`, instances
launch with vTPM and integrity monitoring enabled; Compute Engine refuses
those options on a BIOS image, so they are set only when the image supports
them. Secure Boot stays **off**: the GPU path loads unsigned kernel modules
and would break under it.

---

## Disks

```yaml
extra_config:
  baseDisk:       {diskSizeGb: 100, diskType: pd-ssd}     # boot disk
  additionalDisk: {diskSizeGb: 500, diskType: pd-ssd}     # empty, raw
  existingDisk:
    source: projects/P/zones/us-central1-b/disks/models
    mode: READ_ONLY
    interface: NVME
```

| | Default | Lifetime | Notes |
| --- | --- | --- | --- |
| `baseDisk` | 75 GiB, `pd-balanced` | deleted with the instance | 10–65536 GiB |
| `additionalDisk` | — | deleted with the instance | Attached raw as device `airship-data`; your job formats and mounts it |
| `existingDisk` | — | **never deleted with the instance** | A disk you already have. A *zonal* disk pins the runner to its zone, so a `region:` that says otherwise is a config error. A regional disk works across the region's zones |

Two deliberate behaviours:

- **`existingDisk` is attached with `autoDelete: false`.** The machine is
  Airship's; the disk is yours. Destroying the machine never takes your data
  with it.
- **A `READ_WRITE` disk fits one machine at a time.** Compute Engine
  serialises this itself with `RESOURCE_IN_USE_BY_ANOTHER_RESOURCE`, which
  Airship treats as *retryable*: the job waits its turn behind the runner
  currently holding the disk instead of failing.

A `diskType` written as a zonal path (`projects/P/zones/Z/diskTypes/pd-ssd`)
is read for its type name only — the zone in
that path is not necessarily where this runner lands, and disk types exist
in every zone.

---

## Networking

**Default path.** One interface on the project's `default` network with an
external address (`ONE_TO_ONE_NAT`, premium tier). Runners reach GitHub
directly. Airship creates no firewall rules; your network's existing rules
apply, and a runner needs no inbound traffic at all.

**Your own network.** Up to eight interfaces, spelled as Compute Engine
spells them:

```yaml
extra_config:
  networkInterfaces:
    - network: projects/P/global/networks/ci
      subnetwork: projects/P/regions/us-central1/subnetworks/ci-us-central1
      nicType: GVNIC          # or VIRTIO_NET, IDPF
      stackType: IPV4_ONLY
```

**Private runners.** `enable_public_ip: false` launches with no external
address and routes egress through a Cloud Router and NAT that Airship
creates on first use, on the network the first interface names — or on the
network its *subnetwork* belongs to, which Airship reads from the
subnetwork, because configs commonly name only the subnetwork:

| Network | Router and NAT name |
| --- | --- |
| `default` | `airship-nat` |
| any other | `airship-nat-<network>` (hashed if the name would exceed 63 characters) |

The NAT is created once per region per network with `AUTO_ONLY` addresses
over `ALL_SUBNETWORKS_ALL_IP_RANGES`, and every later runner there shares
it. A concurrent create from another launch is fine — the deterministic
request ID makes it the same request.

A router that already carries Airship's name is **read back and validated**,
not assumed: it is accepted only if it is on the runner's network *and* has
a NAT. Otherwise the launch fails naming the router, rather than booting a
machine that has no egress and looks configured. That is the one place
Airship would rather fail loudly than guess.

Egress a runner needs is the same list as on AWS — GitHub, `githubusercontent.com`,
the distro's apt mirrors, and whatever your jobs reach. See the table in
[aws.md](aws.md#networking); on GCP, Private Google Access and a NAT are the
levers for keeping it off the public internet.

---

## Machine shapes, spot and GPUs

The `scheduling` block Compute Engine wants is derived, not copied, because
the combinations it refuses are config errors in Airship:

| Case | `provisioningModel` | `onHostMaintenance` | `automaticRestart` |
| --- | --- | --- | --- |
| `preemptible: true` | `SPOT` + `instanceTerminationAction: DELETE` | `TERMINATE` | `false` |
| a GPU (attached or built into the family) | `STANDARD` | `TERMINATE` | `false` |
| `scheduling: {onHostMaintenance: MIGRATE}` | `STANDARD` | `MIGRATE` | `false` |
| everything else | `STANDARD` | `MIGRATE` | `false` |

`automaticRestart: false` always: a rebooted runner is of no use, since its
registration was single-use, so a host error must not bring it back.
`MIGRATE` together with `preemptible` or a GPU is a config error — Compute
Engine would refuse it.

**GPUs.** `gpu: nvidia-tesla-t4` attaches one accelerator of that type at
`zones/<zone>/acceleratorTypes/<type>`. Machine families that carry their
own accelerators (`a2`, `a3`, `a4`, `a4x`, `g2`, `g4`) must *not* get a
`guestAccelerators` entry, so Airship omits it for them and still applies
the GPU scheduling rules and the `gpu` label. On Ubuntu images without a
driver, one is installed at boot.

**Service accounts.** By default an Airship runner gets **no service
account** — nothing in a job can mint a Google credential from the metadata
server. Opting in is explicit:

```yaml
extra_config:
  serviceAccounts:
    - email: ci-builder@my-project.iam.gserviceaccount.com
      scopes: [https://www.googleapis.com/auth/devstorage.read_only]
```

Every step of every job on that runner can then use it. The service account
key Airship itself holds is never placed on a machine.

**Multi-project.** `extra_config.project_id` launches in a project other
than the service account's; the account needs Compute Admin there too. Each
project is its own inventory scope.

---

## Caching

`cache: true` on a GCP runner launches **without** the cache proxy and
records a `cache.unavailable` warning on the Events page. The job runs
normally, just without acceleration.

Cache acceleration is Linux runners on AWS only today. A Cloud Storage
backing would be an addition: the provider interface already has the seam for
it (`cloud.Cacher`), and the proxy is storage-agnostic apart from its S3
client. Until then, GCP runners can use `actions/cache` against GitHub's
own cache, or a bring-your-own-bucket action.

---

## Keeping the project clean

Same loop as AWS, with two GCP-specific behaviours:

| It sees | It does |
| --- | --- |
| An instance labelled for the installation with no live runner row | Deletes it |
| A runner row whose instance is no longer listed (after a 2-minute grace) | Marks the runner `lost`; retries its job if still queued |
| A `TERMINATED` or `SUSPENDED` instance | Treats it as gone and deletes it — it runs no job and still bills for its disk. **This is how a self-powered-off machine is reclaimed on GCP** |
| A runner whose lease expired or launch was unconfirmed, but whose instance exists | Revives it, after GitHub confirms the registration still exists |
| A list that fails, or a zone that is unreachable | Nothing. The whole list fails; no instance is presumed gone |

Deleting reads the instance first and refuses one whose
`airship-installation` label is not the installation's — an instance ID
recorded wrongly can never destroy something else.

**Every Compute call is bounded**, including the per-zone instance list. The
reconciler's twelve steps run in sequence, so one hung call would stall them
all. Because the SDK's generated list methods send their requests *without*
the caller's context, the provider gives the SDK an HTTP client with a
timeout of its own (3 minutes, above the 2 minutes an `operations.wait` may
legitimately block) and enforces the 1-minute list deadline from outside it.

---

## How Compute Engine errors are classified

| Class | Examples | Behaviour |
| --- | --- | --- |
| **Zone-specific** | `ZONE_RESOURCE_POOL_EXHAUSTED`, a machine type the zone lacks | Next zone; then the next fallback attempt |
| **Retryable** | `QUOTA_EXCEEDED`, `RATE_LIMIT_EXCEEDED`, `RESOURCE_NOT_READY`, `INTERNAL_ERROR`, `RESOURCE_IN_USE_BY_ANOTHER_RESOURCE` (an `existingDisk` held by another runner) | The chain starts over, up to the attempt cap |
| **Ambiguous** | a conflict, a 5xx, a transport error, a failed or timed-out `operations.wait` | Look the instance up by name; if it exists and is this runner's, that *is* the launch. Otherwise `launch_unconfirmed` and the scan settles it |
| **Permanent** | a missing image, a bad subnetwork, an invalid combination | Next fallback; stop once every attempt has been tried |
| **Credential** | the key is rejected, or the Compute Engine API is not enabled | Refused at connect time (`projects.get`), so such a key is never stored. At launch it is permanent: the job moves to its next fallback and then fails, with Google's message on the Events page. Airship then checks the credential again (at most every five minutes) and, if the cloud refuses it, the Cloud page shows **Credentials rejected** with the cloud's message and Events records `cloud.credentials_rejected`; reconnecting, or the cloud accepting it again, clears it |

An insert that Compute Engine **accepted and then failed** is read from the
finished operation's error codes — the service removes whatever it created,
so there is nothing to clean up.

---

## Quotas to check before you scale

| Quota | Why it bites |
| --- | --- |
| `CPUS` per region | The real limit on concurrent runners. An `n2-standard-16` is 16 vCPUs |
| `CPUS_ALL_REGIONS` | A second, global ceiling people forget |
| `PREEMPTIBLE_CPUS` per region | For `preemptible: true` |
| `DISKS_TOTAL_GB` / `SSD_TOTAL_GB` per region | 100 GiB per runner adds up fast |
| `IN_USE_ADDRESSES` per region | External IPs. `enable_public_ip: false` removes this ceiling entirely |
| GPU quotas (`NVIDIA_T4_GPUS`, `NVIDIA_L4_GPUS`, …) per region | Usually 0 until you ask |
| Routers per region, NATs per router | Only with private runners; one router per network per region |
| **Compute Engine API enabled** | Not a quota, but the first thing that fails. Airship's credential check (`projects.get`) catches it at connect time |

---

## What to watch on the first real launch

Because no request body has yet been accepted by Google, these four are the
specific things to verify on day one — all four are shapes the fake accepts
by construction:

1. **`startup-script` size.** The metadata ceiling is 256 KiB, far above the
   rendered script, but the cache-enabled Linux script is the largest and
   worth an eyeball.
2. **`guestAccelerators` type path.** `zones/<zone>/acceleratorTypes/<type>`
   — the form Google documents, unexercised against the API.
3. **`existingDisk` attachment.** The `source` path form, `autoDelete:
   false`, and the read-write serialisation behaviour.
4. **The Cloud Router insert.** Router plus NAT in one call, with the
   deterministic request ID, and the read-back-on-conflict path.

Run `TestAgainstLiveGCP` with a throwaway project and these close in
minutes. Until then, this document describes a provider that is thoroughly
tested against a faithful fake and not yet against Google.
