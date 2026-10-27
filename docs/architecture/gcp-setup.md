# Setting up Airship on Google Cloud

**What this is.** The path from an empty Google Cloud project to a green CI
job on your own Compute Engine instances, including the organization
policies that are most likely to stand in the way.

**Audience.** Whoever owns the GCP project and whoever owns the GitHub
organization.

**Status.** Read the status note at the top of [gcp.md](gcp.md) before you
plan around this. The Compute Engine provider is thoroughly tested against a
faithful fake of Google's REST API and has never run against Google. The
first launch in a real project is the thing to watch, and
`TestAgainstLiveGCP` closes most of that gap for the price of one
`e2-micro`'s minutes.

---

## Before you start

| You need | Notes |
| --- | --- |
| A GCP project, ideally dedicated to CI | Same argument as on AWS: the IAM grant becomes uninteresting and the bill becomes legible |
| Permission to create a service account **key** | The blocker, more often than not. See the org-policy section below |
| Admin on the GitHub organization | To install the app and activate repositories |
| A region or zone | `us-central1` (any zone) or `us-central1-b` (pinned). Cirun's field is called `region` and takes either |
| A concurrency number | Becomes `max_runners`. Start at 10 |
| A control plane | Airship-hosted, or your own ([operations.md](operations.md)) |

---

## Step 0 — Check your organization policies *(GCP admin)*

Three org-policy constraints commonly block this setup. Find out now rather
than in Step 5.

| Constraint | Effect on Airship | What to do |
| --- | --- | --- |
| `iam.disableServiceAccountKeyCreation` | **Hard blocker.** Airship authenticates with a service account key file and nothing else — no workload identity federation, no attached identity | Grant an exception for this one service account, or run the control plane yourself inside a project where keys are allowed |
| `compute.vmExternalIpAccess` | Blocks the default path, which gives each runner an external address | Use `enable_public_ip: false` on every definition. Airship then creates a Cloud Router and NAT per region/network; make sure Step 3's `networkAdmin` role is granted |
| `compute.requireShieldedVm` | Requires *all* Shielded VM options, including Secure Boot. Airship enables vTPM and integrity monitoring on UEFI images but leaves **Secure Boot off**, because the GPU driver path loads unsigned kernel modules | Grant an exception for the CI project, or confirm with us before relying on GPU runners there |

Also worth knowing: `compute.requireOsLogin` is harmless (Airship never logs
in — there is no SSH key and no inbound access at all), and
`compute.disableSerialPortAccess` only affects your own debugging.

```bash
gcloud resource-manager org-policies list --project "$PROJECT"
```

---

## Step 1 — Enable the API *(GCP admin)*

```bash
PROJECT=my-ci-project
gcloud services enable compute.googleapis.com --project "$PROJECT"
```

That is the only API Airship calls. Its credential check does a
`projects.get` through the Compute API, which is also how an un-enabled API
surfaces as a clear error at connect time rather than as a failed launch an
hour later.

---

## Step 2 — Create the service account *(GCP admin)*

```bash
gcloud iam service-accounts create airship-runners \
  --display-name "Airship GitHub Actions runners" --project "$PROJECT"
SA=airship-runners@$PROJECT.iam.gserviceaccount.com
```

---

## Step 3 — Grant roles *(GCP admin)*

### Option A — the quick start

```bash
gcloud projects add-iam-policy-binding "$PROJECT" \
  --member "serviceAccount:$SA" --role roles/compute.admin
```

`roles/compute.admin` covers everything Airship does, including routers. In
a dedicated CI project this is a reasonable end state and is what the
`airship.yaml` reference assumes for cross-project launches.

### Option B — least privilege

```bash
# Instances, disks, images, zones, operations
gcloud projects add-iam-policy-binding "$PROJECT" \
  --member "serviceAccount:$SA" --role roles/compute.instanceAdmin.v1

# Only if any definition uses enable_public_ip: false
#   (creating the Cloud Router + NAT)
gcloud projects add-iam-policy-binding "$PROJECT" \
  --member "serviceAccount:$SA" --role roles/compute.networkAdmin

# Only if any definition sets extra_config.serviceAccounts
gcloud projects add-iam-policy-binding "$PROJECT" \
  --member "serviceAccount:$SA" --role roles/iam.serviceAccountUser
```

The exact calls those roles have to cover:

| Call | Needed for |
| --- | --- |
| `compute.projects.get` | The credential check at connect time |
| `compute.regions.get` | Listing a region's zones for zone selection |
| `compute.instances.insert / get / list / delete` | The launch, idempotency lookup, inventory scan, teardown |
| `compute.zoneOperations.get` (wait) | Waiting for an insert to finish |
| `compute.images.get`, `compute.images.getFromFamily`, `compute.machineImages.get` | Resolving `machine_image` |
| `compute.disks.use` | Attaching an `existingDisk` |
| `compute.subnetworks.get` / `.use` | A definition that names a subnetwork |
| `compute.routers.get / .insert`, `compute.regionOperations.get` | `enable_public_ip: false` |
| `iam.serviceAccounts.actAs` | `extra_config.serviceAccounts` |

If the connect step fails with a permission error on `projects.get`, add
`roles/compute.viewer` alongside — role contents shift over time and the
credential check is the one call that must work before anything else can.

**Shared VPC.** If runners launch into a host project's network, grant the
service account `roles/compute.networkUser` on the specific subnet in the
host project, and reference the subnetwork by its full path:

```yaml
extra_config:
  networkInterfaces:
    - subnetwork: projects/HOST/regions/us-central1/subnetworks/ci
```

Airship reads the network *from* the subnetwork when only the subnetwork is
named, which is also how it knows where a NAT belongs.

**Another project.** `extra_config.project_id: other-project` launches
elsewhere; the same service account needs the roles above in that project
too, and each project becomes its own inventory scope.

---

## Step 4 — Create the key *(GCP admin)*

```bash
gcloud iam service-accounts keys create airship-sa.json \
  --iam-account "$SA" --project "$PROJECT"
```

This file is the credential. Airship validates it is a `service_account`
key, verifies it against the Compute API, seals it with AES-256-GCM and
opens it only for the duration of a single call. It is never returned by any
endpoint and never placed on a runner machine.

Delete your local copy once it is connected.

---

## Step 5 — Check the quotas that will bite *(GCP admin)*

```bash
gcloud compute regions describe us-central1 --project "$PROJECT" \
  --format "table(quotas:format='table(metric,limit,usage)')"
```

| Metric | Why it matters |
| --- | --- |
| `CPUS` | The real ceiling on concurrent runners, per region |
| `CPUS_ALL_REGIONS` | A second, global ceiling that catches people out |
| `PREEMPTIBLE_CPUS` | For `preemptible: true` |
| `DISKS_TOTAL_GB`, `SSD_TOTAL_GB` | 100 GiB per runner × 10 runners is 1 TiB |
| `IN_USE_ADDRESSES` | External IPs. `enable_public_ip: false` removes this ceiling |
| `NVIDIA_*_GPUS` | Usually 0 until you request an increase |
| `ROUTERS` | Only with private runners; one per network per region |

Arithmetic as on AWS: *(vCPUs per machine) × (max concurrent runners)*. An
`n2-standard-16` is 16 vCPUs. Then set `max_runners` below what the quota
allows, so the ceiling shows up as a dashboard message rather than a stream
of failed inserts.

---

## Step 6 — Install the GitHub App *(GitHub org admin)*

Identical to the AWS path — the app's permissions are about GitHub, not
about your cloud. See [aws-setup.md](aws-setup.md#step-4--install-the-github-app-github-org-admin)
for the permission table and why each one is needed.

---

## Step 7 — Connect Google Cloud *(GitHub org admin)*

**Dashboard:** **Cloud → Google Cloud**, paste the contents of
`airship-sa.json` and choose the default location — a region
(`us-central1`) or a zone (`us-central1-b`).

**Or the developer API:**

```bash
export CIRUN_API_ENDPOINT=https://api.example.com/api/v1
export CIRUN_API_KEY=asa_...
cirun cloud connect gcp --key-file airship-sa.json
```

On submit, Airship reads the project through the Compute API to prove the
key works *and* that the API is enabled, then echoes back the project ID and
the service account's email so you can confirm it is the identity you meant.

One credential per cloud per installation — you can have AWS and GCP
connected at the same time, and each runner definition names its own
`cloud:`. Rotating the key within the same project is always allowed;
swapping it for another project's key, or disconnecting, is refused while
runners launched with it are alive, because without it their instances could
not be deleted.

---

## Step 8 — Activate repositories and commit a config

Activation works exactly as on AWS: **Repositories →** toggle on, public
repositories need the separate public-access choice, and an `airship.yaml` in
a repository nobody activated launches nothing.

```yaml
runners:
  - name: build
    cloud: gcp
    instance_type: [n2-standard-8, n2d-standard-8]
    machine_image: ubuntu-2204-lts
    region: us-central1              # any zone with capacity
    labels: [airship-build]
    extra_config:
      baseDisk: {diskSizeGb: 100, diskType: pd-ssd}
      enable_public_ip: false        # egress via the Cloud NAT Airship makes

  - name: gpu
    cloud: gcp
    instance_type: n1-standard-8
    gpu: nvidia-tesla-t4
    region: us-central1-b            # pinned: GPU availability is zonal
    preemptible: true
    labels: [airship-gpu]
```

```yaml
jobs:
  test:
    runs-on: airship-build--${{ github.run_id }}
```

Two GCP-specific notes:

- **`region` takes a zone or a region.** A region lets Airship try zones in
  a per-runner order and move on when one is out of capacity — usually what
  you want. Pin a zone when a resource is zonal: GPUs, or an `existingDisk`.
- **A zonal `existingDisk` pins the launch to its zone**, so a `region:`
  that disagrees is a config error rather than a failed insert.

---

## Step 9 — Verify

| Where | What you should see |
| --- | --- |
| Airship → **Jobs** | The job queued within a second or two, with its planned machine |
| Airship → **Runners** | `pending → launching → running → busy`, instance ID shown as `zone/name` |
| `gcloud compute instances list --filter="labels.airship-installation:*"` | One instance, labelled, network tag `airship-runner`, no service account unless you asked for one |
| GitHub → the job log | Labels `self-hosted`, `x64`, `gcp`, `cpu`, `airship-build--…` |
| `gcloud compute instances list` after the job | Gone. Not `TERMINATED` — deleted, disks and all |
| Airship → **Events** | Every launch failure and degradation, with Google's own message |

```bash
# Everything Airship has running, with what it is for
gcloud compute instances list --project "$PROJECT" \
  --filter="labels.airship-installation:*" \
  --format="table(name,zone,status,labels.airship-runner-id)"

# The NAT, if you used enable_public_ip: false
gcloud compute routers list --project "$PROJECT" --filter="name~airship-nat"
```

**The first-week test, GCP edition:** cancel a queued job and a running job,
and confirm no instance survives either — then stop a runner by hand from
the console and confirm the next inventory pass (within ~15 seconds plus a
2-minute grace) deletes it. A hand-stopped machine runs no job and still
bills for its disk, which is why it is treated as gone.

---

## Going to production

| Step | Why |
| --- | --- |
| Set `max_runners` under your `CPUS` quota | The one number that bounds total spend |
| Bake a custom image | Boot the runner tarball and your toolchain from your own image or a machine image; `machine_image` accepts `project:image`, a full path, or a machine image |
| `enable_public_ip: false` as the default | One NAT per region, no per-instance external IPs, no `IN_USE_ADDRESSES` ceiling, and nothing addressable from the internet |
| Write firewall rules against the `airship-runner` network tag | Airship adds none of its own, so this is your lever and it applies to every future runner automatically |
| Pin GPU definitions to a zone | GPU stock is zonal; a pinned zone fails fast instead of walking the region |
| Idle pools where latency matters | `idle: {replicas: 2}` or `schedule:` windows; they count toward `max_runners` |
| Don't give runners a service account unless a job needs one | The default is none, which means nothing in a job can mint a Google credential from the metadata server |
| Remember: no cache acceleration on GCP yet | `cache: true` launches without it and records a warning. Use GitHub's own cache meanwhile. See [gcp.md](gcp.md#caching) |
| Run `TestAgainstLiveGCP` once | It is the cheapest way to turn "tested against a faithful fake" into "accepted by Google" |

---

## Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| Connect fails: *key file type is …* | Not a service account key (an OAuth client, or an ADC file) | Create a key as in Step 4 |
| Connect fails on `projects.get` | API not enabled, or the role lacks it | `gcloud services enable compute.googleapis.com`; add `roles/compute.viewer` |
| Cannot create a key at all | `iam.disableServiceAccountKeyCreation` | Step 0. There is no key-free path today |
| Launch fails: `ZONE_RESOURCE_POOL_EXHAUSTED` | That zone is out of that shape | Expected and handled — Airship rotates zones. If every zone fails, add shapes to `instance_type` or a second `region` |
| Launch fails: `QUOTA_EXCEEDED` | A regional quota | Raise it; lower `max_runners` meanwhile. Note it arrives *after* the insert was accepted, which is why Airship waits for the operation |
| Launch fails: *router airship-nat … is on another network* | A router of that name exists on a different network | Rename or move it. Airship refuses to reuse it rather than boot a machine with no egress |
| Launch fails: *router … has no NAT* | Something created a bare router with that name | Add a NAT to it, or delete it so Airship can make it |
| Launch fails: external IP refused | `compute.vmExternalIpAccess` | `enable_public_ip: false` everywhere, plus `networkAdmin` |
| Launch fails: Shielded VM required | `compute.requireShieldedVm` wants Secure Boot | Step 0 — Airship deliberately leaves Secure Boot off |
| Launch fails: `RESOURCE_IN_USE_BY_ANOTHER_RESOURCE` | A read-write `existingDisk` is held by another runner | Expected: treated as retryable, and the job waits its turn |
| Launch fails: *disk … is in zone X, outside region Y* | A zonal `existingDisk` and a conflicting `region:` | Pin the zone, or use a regional disk |
| Instance exists, runner never registers | No egress | Private instance without a NAT, or a NAT on the wrong network. Check `gcloud compute routers list` |
| Instance sits `TERMINATED` | The machine powered itself off; Compute Engine does not delete on shutdown | Expected. The next inventory pass deletes it. If it lingers, the credential or the inventory scan is failing — check Events |
| Runner finished `launch_unconfirmed` | The insert's answer was lost | Handled: the job is retried, the scan reconciles the machine. Repeated occurrences mean a network problem between control plane and Google |
| `cache: true` does nothing | Expected on GCP | Events records `cache.unavailable`. [gcp.md](gcp.md#caching) |

---

## Turning it off

As on AWS: deactivate a repository to stop it; uninstall the GitHub App to
stop everything (every runner is finished with reason `deleted` and its
instance removed). Disconnecting the credential is refused while runners
launched with it are alive.

Left behind by design, to delete by hand if you are done:

```bash
gcloud compute routers list  --project "$PROJECT" --filter="name~airship-nat"
gcloud compute routers delete airship-nat --region us-central1 --project "$PROJECT"
gcloud iam service-accounts delete "$SA" --project "$PROJECT"
```

---

## Appendix — the whole GCP side in one go

```bash
#!/usr/bin/env bash
set -euo pipefail
PROJECT=my-ci-project
REGION=us-central1
SA="airship-runners@$PROJECT.iam.gserviceaccount.com"

gcloud services enable compute.googleapis.com --project "$PROJECT"

gcloud iam service-accounts create airship-runners \
  --display-name "Airship GitHub Actions runners" --project "$PROJECT"

gcloud projects add-iam-policy-binding "$PROJECT" \
  --member "serviceAccount:$SA" --role roles/compute.instanceAdmin.v1
# private runners only:
gcloud projects add-iam-policy-binding "$PROJECT" \
  --member "serviceAccount:$SA" --role roles/compute.networkAdmin

gcloud compute regions describe "$REGION" --project "$PROJECT" \
  --format "table(quotas:format='table(metric,limit,usage)')"

gcloud iam service-accounts keys create airship-sa.json \
  --iam-account "$SA" --project "$PROJECT"   # paste into Cloud → Google Cloud
```

Then: install the app, connect the key, activate a repository, commit the
config from Step 8.
