# Setting up Airship on Google Cloud — from the CLI

**What this is.** The same setup as
[gcp-setup-console.md](gcp-setup-console.md), as `gcloud` commands. Terse on
purpose: it assumes you know your way around a shell and a GCP project.

**New to Google Cloud?** Use
[gcp-setup-console.md](gcp-setup-console.md) instead. It covers the same
ground with clicks and explains what each resource is. Pick one guide and
stay in it — they do not interleave.

**Three steps have no CLI**, because they are web-only: installing the
GitHub App (Step 6), creating the API token Steps 7 and 8 authenticate with,
and **requesting a quota increase** (Step 5 — Google has no `gcloud` for
it).

**Status.** The Compute Engine provider is thoroughly tested against a
faithful fake of Google's REST API and has never run against Google. Read
the status note at the top of [gcp.md](gcp.md). `TestAgainstLiveGCP` closes
most of that gap for the price of one `e2-micro`'s minutes.

---

## Prerequisites

```bash
# macOS; elsewhere see https://cloud.google.com/sdk/docs/install
brew install --cask google-cloud-sdk

gcloud auth login
gcloud config list            # confirm which project and account you are on
```

```bash
export PROJECT=acme-ci-20260101      # the project ID, not its display name
export REGION=us-central1
export SA="airship-runners@$PROJECT.iam.gserviceaccount.com"
```

Creating the project, if you need one:

```bash
gcloud projects create "$PROJECT" --name "Acme CI"
gcloud billing projects link "$PROJECT" --billing-account 0X0X0X-0X0X0X-0X0X0X
```

An unbilled project cannot create instances.

---

## Step 0 — Organization policies

Three constraints commonly block this setup, and the first is a hard stop:

| Constraint | Effect |
| --- | --- |
| `iam.disableServiceAccountKeyCreation` | **Hard blocker.** Airship authenticates with a service account key file and nothing else — no workload identity federation, no attached identity |
| `compute.vmExternalIpAccess` | Blocks the default path. Use `enable_public_ip: false` everywhere, plus `roles/compute.networkAdmin` |
| `compute.requireShieldedVm` | Wants Secure Boot, which Airship deliberately leaves **off** (the GPU driver path loads unsigned kernel modules). vTPM and integrity monitoring are on |

```bash
gcloud resource-manager org-policies list --project "$PROJECT"
```

Empty output, or an error about not belonging to an organization, means
nothing is in your way. `compute.requireOsLogin` is harmless — Airship
never logs in, and there is no SSH key or inbound access at all.

---

## Step 1 — Enable the API

```bash
gcloud services enable compute.googleapis.com --project "$PROJECT"
gcloud services list --enabled --project "$PROJECT" | grep compute
```

The only API Airship calls. Its credential check does a `projects.get`
through it, which is how an un-enabled API surfaces at connect time rather
than as a failed launch an hour later.

---

## Step 2 — The service account

```bash
gcloud iam service-accounts create airship-runners \
  --display-name "Airship GitHub Actions runners" --project "$PROJECT"

gcloud iam service-accounts list --project "$PROJECT"
```

---

## Step 3 — Roles

### Option A — the quick start

```bash
gcloud projects add-iam-policy-binding "$PROJECT" \
  --member "serviceAccount:$SA" --role roles/compute.admin
```

Covers everything including routers. Reasonable in a dedicated CI project,
and what the configuration reference assumes for cross-project launches.

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

The calls those roles have to cover: `compute.projects.get` (the connect
check), `compute.regions.get`, `compute.instances.insert/get/list/delete`,
`compute.zoneOperations.get`, `compute.images.get` /
`.getFromFamily` / `compute.machineImages.get`, `compute.disks.use`,
`compute.subnetworks.get` / `.use`, `compute.routers.get` / `.insert` with
`compute.regionOperations.get`, and `iam.serviceAccounts.actAs`.

```bash
gcloud projects get-iam-policy "$PROJECT" \
  --flatten="bindings[].members" --filter="bindings.members:$SA" \
  --format="table(bindings.role)"
```

IAM is eventually consistent — a grant can take up to a minute, so a
permission error immediately after granting is worth one retry. If connect
fails on `projects.get`, add `roles/compute.viewer` alongside: role contents
shift over time and that is the one call that must work before anything else.

**Shared VPC** needs `roles/compute.networkUser` on the specific subnet in
the host project, and the subnetwork by full path:

```yaml
extra_config:
  networkInterfaces:
    - subnetwork: projects/HOST/regions/us-central1/subnetworks/ci
```

Airship reads the network *from* the subnetwork when only the subnetwork is
named, which is also how it knows where a NAT belongs.
`extra_config.project_id` launches in another project; the same service
account needs these roles there too, and each project becomes its own
inventory scope.

---

## Step 4 — The key

```bash
gcloud iam service-accounts keys create airship-sa.json \
  --iam-account "$SA" --project "$PROJECT"

head -c 60 airship-sa.json   # {"type": "service_account", "project_id": ...
```

If it says `authorized_user` you have an ADC file, not a key, and Airship
will reject it.

Airship validates the type, verifies it against the Compute API, seals it
with AES-256-GCM and opens it only for the duration of a single call. It is
never returned by any endpoint and never placed on a runner machine. Treat
the file like a password, and `rm` it once Step 7 is done.

---

## Step 5 — Quotas

*(vCPUs per machine) × (max concurrent runners)*. An `n2-standard-8` is 8
vCPUs — the trailing number *is* the vCPU count — so 10 concurrent runners
need 80.

```bash
gcloud compute regions describe "$REGION" --project "$PROJECT" \
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

**Increases are console-only** — there is no `gcloud` command. **IAM &
Admin** → **Quotas & System Limits** → tick the row → **Edit quotas**.
"Self-hosted CI runners for our GitHub organization" is the whole
justification. Raise `CPUS_ALL_REGIONS` as well as the regional one; GPU
quotas start at zero and are slowest to approve.

Then set `max_runners` below the quota, so the ceiling is a dashboard
message rather than a stream of failed inserts.

---

## Step 6 — Install the GitHub App *(no CLI — web only)*

See
[aws-setup-console.md, Step 4](aws-setup-console.md#step-4--install-the-github-app-github-org-admin)
for the permission table — the app's permissions are about GitHub, not your
cloud.

**If you run your own control plane,** the app's **Permissions & events** →
**Subscribe to events** must include **Workflow job**, **Push** and
**Repository**. An app subscribed to no events looks healthy — you can
connect a cloud and activate repositories — and jobs never appear.

---

## Step 7 — Connect Google Cloud

Create an API token in the dashboard first, under **API keys** *(web only)*.
The credentials object takes the **fields of the key file itself**, so pass
it through directly:

```bash
export AIRSHIP_API=https://api.example.com
export AIRSHIP_TOKEN=asa_...

python3 - <<'PY' > /tmp/connect.json
import json
key = json.load(open("airship-sa.json"))
key["region"] = "us-central1"          # optional: a zone or a region
print(json.dumps({"cloud": "gcp", "credentials": key}))
PY

curl -fsS -X POST "$AIRSHIP_API/api/v1/cloud-connect" \
  -H "Authorization: Bearer $AIRSHIP_TOKEN" \
  -H 'Content-Type: application/json' --data @/tmp/connect.json

rm /tmp/connect.json airship-sa.json
```

Check what is connected:

```bash
curl -fsS "$AIRSHIP_API/api/v1/cloud-connect" -H "Authorization: Bearer $AIRSHIP_TOKEN"
```

On submit Airship reads the project through the Compute API — proving both
that the key works and that the API is enabled — then **echoes back the
project ID and the service account's email**. Read them and confirm they are
the project and identity you meant.

A complaint about the key *type* means the wrong file; a permission error on
`projects.get` means the API is off (Step 1) or the role did not land (Step
3). Nothing is stored on a failure.

One credential per cloud per installation. Rotating the key within the same
project is always allowed; swapping it for another project's key, or
disconnecting, is refused while runners launched with it are alive.

---

## Step 8 — Activate repositories

```bash
curl -fsS "$AIRSHIP_API/v1/api/repositories" \
  -H "Authorization: Bearer $AIRSHIP_TOKEN"

curl -fsS -X PUT "$AIRSHIP_API/v1/api/repositories/api/activation" \
  -H "Authorization: Bearer $AIRSHIP_TOKEN" \
  -H 'Content-Type: application/json' -d '{}'
```

The path takes the **repository name alone**, not `owner/repo`, matched
case-insensitively within the installation. A public repository needs
`-d '{"allowPublic": true}'`, because its fork pull requests run untrusted
code on your machines. `DELETE` the same path to deactivate.

An `airship.yaml` in a repository nobody activated launches nothing, which
is why this is a separate step.

---

## Step 9 — The two files

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

- **`region` takes a zone or a region.** A region lets Airship try zones in
  a per-runner order and move on when one is out of capacity. Pin a zone for
  zonal resources: GPUs, or an `existingDisk`.
- **A zonal `existingDisk` pins the launch to its zone**, so a conflicting
  `region:` is a config error rather than a failed insert.
- The boot disk **defaults to 75 GiB of pd-balanced**.
- The `--${{ github.run_id }}` suffix registers the runner under a label
  unique to that run. Use it everywhere except idle pools.

Full reference: [the configuration reference](../airship-yaml.md).

---

## Step 10 — Verify

```bash
# everything Airship has running, with what it is for
gcloud compute instances list --project "$PROJECT" \
  --filter="labels.airship-installation:*" \
  --format="table(name,zone,status,labels.airship-runner-id)"

# the NAT, if you used enable_public_ip: false
gcloud compute routers list --project "$PROJECT" --filter="name~airship-nat"

# a machine that booted but never registered
gcloud compute instances get-serial-port-output NAME --zone ZONE --project "$PROJECT"
```

After the job the instance is **gone** — not `TERMINATED`, deleted, disks and
all. Alongside: **Jobs** shows it queued within a second or two; **Runners**
shows `pending → launching → running → busy` with the instance ID as
`zone/name`; the job log shows labels `self-hosted`, `x64`, `gcp`, `cpu`,
`airship-build--…`.

**The first-week test, GCP edition:** cancel a queued job and a running job
and confirm no instance survives either, then stop a runner by hand and
confirm the next inventory pass (~15 seconds plus a 2-minute grace) deletes
it. A hand-stopped machine runs no job and still bills for its disk.

---

## Troubleshooting

Identical causes whichever guide you followed, so they live in one place:
[gcp-setup-console.md § Troubleshooting](gcp-setup-console.md#troubleshooting).
It sorts Google's error codes by what they mean — `PERMISSION_DENIED`,
`QUOTA_EXCEEDED`, `ZONE_RESOURCE_POOL_EXHAUSTED` — and covers the empty
webhook subscription, the org-policy blockers and the NAT conflicts.

Start at **Events** for all of them.

---

## Turning it off

```bash
curl -fsS -X DELETE "$AIRSHIP_API/v1/api/repositories/api/activation" \
  -H "Authorization: Bearer $AIRSHIP_TOKEN"
```

Uninstalling the GitHub App stops everything: every runner is finished with
reason `deleted` and its instance removed. Disconnecting the credential is
refused while runners launched with it are alive.

Left behind by design — and a Cloud Router with a NAT **bills while it
exists**, so do clear it up:

```bash
gcloud compute routers list  --project "$PROJECT" --filter="name~airship-nat"
gcloud compute routers delete airship-nat --region "$REGION" --project "$PROJECT"
gcloud iam service-accounts delete "$SA" --project "$PROJECT"

# should be empty: Airship deletes disks with their instances
gcloud compute disks list --project "$PROJECT" --filter="-users:*"
```

If the project was dedicated to CI, shutting it down is the cleanest end
state.

---

## Appendix — the whole GCP side in one go

```bash
#!/usr/bin/env bash
set -euo pipefail
PROJECT=my-ci-project
REGION=us-central1
SA="airship-runners@$PROJECT.iam.gserviceaccount.com"

gcloud config list            # confirm the project and account first

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
  --iam-account "$SA" --project "$PROJECT"   # feed this into Step 7
```

Then: install the app (Step 6), connect the key (Step 7), activate a
repository (Step 8), commit the two files from Step 9.
