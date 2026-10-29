# Setting up Airship on Google Cloud — in the console

**What this is.** The path from an empty Google Cloud project to a green CI
job on your own Compute Engine instances, entirely through the Cloud console
and the Airship dashboard. **No terminal is needed from start to finish.**

**Prefer a terminal?** [gcp-setup-cli.md](gcp-setup-cli.md) is the same
setup as `gcloud` commands. Pick one guide and stay in it — they do not
interleave.

**Audience.** Whoever owns the GCP project and whoever owns the GitHub
organization. **No prior Google Cloud experience is assumed.**

**Time.** 30–45 minutes, plus any wait on a quota increase (Step 5) or an
org-policy exception (Step 0) — both depend on someone other than you, so
read ahead and ask early.

**Status.** Read the status note at the top of [gcp.md](gcp.md) before you
plan around this. The Compute Engine provider is thoroughly tested against a
faithful fake of Google's REST API and has never run against Google. The
first launch in a real project is the thing to watch.

---

## Orientation — if you have never operated Google Cloud

Airship's job is to start a fresh machine for each CI job and delete it when
the job ends. To do that in your project it needs five things to exist and be
reachable. Nothing is deployed into your project, there is no agent and no
bootstrap script. You hand Airship one key file and it calls the Compute
Engine API.

| The thing | In one sentence | Why Airship needs it | Step |
| --- | --- | --- | --- |
| **A project** | GCP's unit of isolation and billing — everything below lives inside exactly one, and projects have globally unique IDs | Machines launch here and you pay for them directly | [below](#getting-a-project-if-you-need-one) |
| **The Compute Engine API, enabled** | Google ships every API switched off per project; you turn on the ones you use | Without it, every call fails — including the credential check | [1](#step-1--enable-the-api-gcp-admin) |
| **A service account with a JSON key** | A robot identity belonging to the project; its key is a downloaded file that authenticates API calls | How Airship proves it may create and delete machines | [2](#step-2--create-the-service-account-gcp-admin), [4](#step-4--create-the-key-gcp-admin) |
| **IAM roles on that account** | Roles are named bundles of permissions, granted on the project | Creating an instance is not permitted by default, even for a project's own service account | [3](#step-3--grant-roles-gcp-admin) |
| **Quotas** | Per-project caps, mostly per-region, on how much you can run | The ceiling on concurrent runners — almost always the real limit, not Airship | [5](#step-5--check-the-quotas-that-will-bite-gcp-admin) |

Vocabulary that appears constantly below:

- **Compute Engine** is Google's virtual machine service. An **instance** is
  one machine; a **machine type** like `n2-standard-8` names its shape.
- A **region** (`us-central1`) is a geographic area; a **zone**
  (`us-central1-b`) is one data centre inside it. GPUs and disks are *zonal*,
  which matters in Step 8.
- Every project gets a **`default` VPC network** with a subnet in every
  region, already wired for egress. Airship uses it unless told otherwise.
- A **network tag** is a label used by firewall rules. Airship tags every
  runner `airship-runner` and adds **no firewall rules of its own** —
  instances are never addressable from the internet.

### Two things that differ from AWS, if that is your reference point

- The unit is a **project**, not an account. Projects are cheap, so a
  dedicated `acme-ci` project is the easy version of "isolate CI".
- A **service account** is the normal way for software to authenticate —
  there is no equivalent of an IAM user with an access key. Its **JSON key
  file** is the credential, and many organizations forbid creating one. That
  is Step 0's first row, and it is the most common hard stop in this guide.

### What this will cost

You pay Google directly, by the second (after a one-minute minimum), for
machines that only exist while a job runs. Airship adds nothing that idles.
Approximate on-demand prices in `us-central1` — the console's machine-type
picker shows a live estimate, and other regions run 10–30% higher:

| Machine type | vCPU / RAM | Roughly | A 10-minute job |
| --- | --- | --- | --- |
| `e2-micro` | 2 shared / 1 GiB | $0.01/hr | a fraction of a cent |
| `e2-standard-4` | 4 / 16 GiB | $0.13/hr | about 2¢ |
| `n2-standard-8` | 8 / 32 GiB | $0.39/hr | about 6¢ |
| `n2-standard-16` | 16 / 64 GiB | $0.78/hr | about 13¢ |
| `n1-standard-8` + `nvidia-tesla-t4` | 8 / 30 GiB + T4 | $0.73/hr | about 12¢ |

Disks bill per gigabyte-**month**, so a 100 GiB `pd-ssd` that lives fifteen
minutes costs well under a cent. `preemptible: true` (Spot) takes 60–90% off
the machine price in exchange for Google being allowed to reclaim it. A new
account usually has **$300 of credit** that covers a lot of first-week
experimenting.

The two numbers that bound your bill are `max_runners` (Airship's ceiling on
concurrent machines) and your `CPUS` quota (Google's). Add a budget alert as
a third, independent net: **Billing** → **Budgets & alerts** → **Create
budget**, monthly, at an amount that would surprise you.

---

## Before you start

| You need | Notes |
| --- | --- |
| A GCP project, ideally dedicated to CI | Same argument as on AWS: the IAM grant becomes uninteresting and the bill becomes legible |
| Permission to create a service account **key** | The blocker, more often than not. See [Step 0](#step-0--check-your-organization-policies-gcp-admin) |
| Admin on the GitHub organization | To install the app and activate repositories. If that is not you, Steps 6–8 need the person it is |
| A region or zone | `us-central1` (any zone) or `us-central1-b` (pinned). The field is called `region` and accepts either |
| A concurrency number | Becomes `max_runners`. Start at 10 |
| A control plane | Airship-hosted, or your own ([operations.md](operations.md)) |

### Getting a project, if you need one

The project picker in the top bar → **New project**. Name it `acme-ci`; note
the **project ID** it generates (globally unique, often with digits appended
— it is the ID, not the name, that everything below wants). Then **Billing**
→ link a billing account, because an unbilled project cannot create
instances.

---

## Step 0 — Check your organization policies *(GCP admin)*

An **organization policy** is a constraint set above the project, by whoever
administers your Google Cloud organization, which a project cannot override.
If your project stands alone (you signed up with a personal account and made
one project), you have none of these and can skip to Step 1. If your company
has a cloud platform team, read this now rather than discovering it in Step 4.

Three constraints commonly block this setup:

| Constraint | Effect on Airship | What to do |
| --- | --- | --- |
| `iam.disableServiceAccountKeyCreation` | **Hard blocker.** Airship authenticates with a service account key file and nothing else — no workload identity federation, no attached identity | Grant an exception for this one service account, or run the control plane yourself inside a project where keys are allowed |
| `compute.vmExternalIpAccess` | Blocks the default path, which gives each runner an external address | Use `enable_public_ip: false` on every definition. Airship then creates a Cloud Router and NAT per region/network; make sure Step 3's `networkAdmin` role is granted |
| `compute.requireShieldedVm` | Requires *all* Shielded VM options, including Secure Boot. Airship enables vTPM and integrity monitoring on UEFI images but leaves **Secure Boot off**, because the GPU driver path loads unsigned kernel modules | Grant an exception for the CI project, or confirm with us before relying on GPU runners there |

Also worth knowing: `compute.requireOsLogin` is harmless (Airship never logs
in — there is no SSH key and no inbound access at all), and
`compute.disableSerialPortAccess` only affects your own debugging.

**IAM & Admin** → **Organization policies**, then search for each constraint
name above. "Inherited / Enforced" is the thing to read. An error about not
belonging to an organization means there are no policies in your way.

---

## Step 1 — Enable the API *(GCP admin)*

Google ships every API disabled per project. Compute Engine is the only one
Airship calls.

**APIs & Services** → **Enable APIs and services** → search *Compute Engine
API* → **Enable**. The first enable on a new project takes a minute or two
and may say so.

Airship's credential check does a `projects.get` through the Compute API,
which is also how an un-enabled API surfaces as a clear error at connect
time rather than as a failed launch an hour later.

---

## Step 2 — Create the service account *(GCP admin)*

A **service account** is an identity for software rather than a person. It
belongs to the project, has an email-shaped name, and holds no permissions
until Step 3 grants some.

**IAM & Admin** → **Service Accounts** → **Create service account**. Name
`airship-runners`; the email is generated for you. You can set the role on
the second screen of this wizard instead of doing Step 3 separately.

> Note the shape of that email:
> `airship-runners@PROJECT.iam.gserviceaccount.com`. If you later see a
> permission error naming an account you do not recognise, the project part
> tells you where it came from.

---

## Step 3 — Grant roles *(GCP admin)*

A **role** is a named bundle of permissions; granting one on the project is
called a *binding*. Pick one of two approaches.

### Option A — the quick start

**Compute Admin** covers everything Airship does, including routers. In a
dedicated CI project this is a reasonable end state and is what the
configuration reference assumes for cross-project launches.

**IAM & Admin** → **IAM** → **Grant access** → principal is the service
account's email → role **Compute Admin** → **Save**.

### Option B — least privilege

On the same **Grant access** screen, one principal with several roles:

| Role | When you need it |
| --- | --- |
| **Compute Instance Admin (v1)** | Always — instances, disks, images, zones, operations |
| **Compute Network Admin** | Only if any definition uses `enable_public_ip: false` (creating the Cloud Router + NAT) |
| **Service Account User** | Only if any definition sets `extra_config.serviceAccounts` |

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

IAM changes are eventually consistent — a grant can take up to a minute to
take effect, so a permission error immediately after granting is worth one
retry before you start debugging.

If the connect step fails with a permission error on `projects.get`, add
**Compute Viewer** alongside — role contents shift over time and the
credential check is the one call that must work before anything else can.

**Shared VPC.** If runners launch into a host project's network, grant the
service account **Compute Network User** on the specific subnet in the host
project, and reference the subnetwork by its full path:

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

A **service account key** is a JSON file containing a private key. Anyone
holding it can act as that service account, so treat it like a password: it
goes into Airship and nowhere else — not a repository, not a Dockerfile, not
Slack.

**IAM & Admin** → **Service Accounts** → click `airship-runners` → **Keys**
tab → **Add key** → **Create new key** → **JSON** → **Create**. Your browser
downloads the file; it is the only copy.

Airship validates it is a `service_account` key, verifies it against the
Compute API, seals it with AES-256-GCM and opens it only for the duration of
a single call. It is never returned by any endpoint and never placed on a
runner machine.

**Delete your local copy once it is connected** (Step 7), and empty your
Downloads folder if the console put it there. If you lose it, delete the key
and create another — that costs nothing.

**Check it worked:** open the file in a text editor. It should begin
`{"type": "service_account", "project_id": …`. If it says `authorized_user`,
you have an application-default-credentials file rather than a key, and
Airship will reject it.

---

## Step 5 — Check the quotas that will bite *(GCP admin)*

A **quota** is a per-project cap, usually per-region, that Google applies by
default. They exist to stop a runaway script costing you a fortune, and they
are the limit on concurrent runners far more often than Airship is. **Read
this step before you need it:** an increase is a request Google approves,
usually within minutes to a few hours, occasionally a business day.

### Do the arithmetic

*(vCPUs per machine) × (max concurrent runners) = the `CPUS` quota you need.*

An `n2-standard-8` is 8 vCPUs, so 10 concurrent runners need **80**. The
number at the end of a standard machine type *is* its vCPU count, which
makes this easier than on AWS. On a new project the regional default is
often low enough to allow two or three of those.

### Look at what you have

**IAM & Admin** → **Quotas & System Limits**, filter the service to *Compute
Engine API*, then search for `CPUS`. The **Dimensions** column tells you
which region each row is for.

| Metric | Why it matters |
| --- | --- |
| `CPUS` | The real ceiling on concurrent runners, per region |
| `CPUS_ALL_REGIONS` | A second, global ceiling that catches people out |
| `PREEMPTIBLE_CPUS` | For `preemptible: true` |
| `DISKS_TOTAL_GB`, `SSD_TOTAL_GB` | 100 GiB per runner × 10 runners is 1 TiB |
| `IN_USE_ADDRESSES` | External IPs. `enable_public_ip: false` removes this ceiling |
| `NVIDIA_*_GPUS` | Usually 0 until you request an increase |
| `ROUTERS` | Only with private runners; one per network per region |

### Ask for more

Tick the row → **Edit quotas** (or **Request higher quota**) → enter the new
limit and a one-line reason. "Self-hosted CI runners for our GitHub
organization" is the whole answer. Track it on the same page.

Ask for a number with room in it — a quota is a ceiling, not a reservation,
and you are never billed for headroom. Remember `CPUS_ALL_REGIONS` as well
as the regional one; raising only the regional quota and then hitting the
global one is a confusing afternoon. GPU quotas start at zero and are the
slowest to be approved, so file that one days before you need it.

### Then set `max_runners` below it

So the ceiling shows up as a dashboard message rather than a stream of
failed inserts, and the eleventh job queues instead of failing.

---

## Step 6 — Install the GitHub App *(GitHub org admin)*

Identical to the AWS path — the app's permissions are about GitHub, not
about your cloud. See
[aws-setup-console.md, Step 4](aws-setup-console.md#step-4--install-the-github-app-github-org-admin)
for the permission table, and for the **Subscribe to events** setting
(**Workflow job**, **Push**, **Repository**) that silently breaks everything
if it is empty. Installing the app activates nothing by itself; Step 8 is
the switch that matters.

---

## Step 7 — Connect Google Cloud *(GitHub org admin)*

**Cloud → Google Cloud** in the dashboard, paste the **whole contents** of
the key file (open it in a text editor and copy everything, braces included)
and choose the default location — a region (`us-central1`) or a zone
(`us-central1-b`).

On submit, Airship reads the project through the Compute API to prove the
key works *and* that the API is enabled, then **echoes back the project ID
and the service account's email** — read them, and confirm they are the
project and identity you meant. That is the moment a key from the wrong
project gets caught.

If it is rejected: a complaint about the key *type* means you pasted the
wrong file (Step 4's check); a permission error on `projects.get` means the
API is off (Step 1) or the role did not land (Step 3). Nothing is stored on
a failure, so fix and retry. Now delete your local copy of the key.

One credential per cloud per installation — you can have AWS, GCP, Azure and
Daytona connected at the same time, and each runner definition names its own
`cloud:`. Rotating the key within the same project is always allowed;
swapping it for another project's key, or disconnecting, is refused while
runners launched with it are alive, because without it their instances could
not be deleted.

---

## Step 8 — Activate repositories and commit a config

**Repositories →** toggle on. Public repositories need the separate
public-access choice, and an `airship.yaml` in a repository nobody activated
launches nothing. Start with a private repository for your first job.

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

The `--${{ github.run_id }}` suffix makes the runner register under a label
unique to that workflow run, so GitHub can only hand it a job from that run.
Use it everywhere except with idle pools, which exist before a run does.
Only labels starting with `airship-` are looked up at all, so jobs on
`ubuntu-latest` are untouched and you can migrate one workflow at a time.

Choosing the shape, if you have no idea: start at `n2-standard-8` for a
typical test suite, `e2-standard-4` for something small like a linter, and
change it once you have seen a real job's `nproc` and timings. The `e2`
family is the cheapest, `n2`/`n2d` the usual general-purpose choice, `c2`
for compute-bound builds. Note the boot disk **defaults to 75 GiB of
pd-balanced**. Full reference: [the configuration reference](../airship-yaml.md).

Two GCP-specific notes:

- **`region` takes a zone or a region.** A region lets Airship try zones in
  a per-runner order and move on when one is out of capacity — usually what
  you want. Pin a zone when a resource is zonal: GPUs, or an `existingDisk`.
- **A zonal `existingDisk` pins the launch to its zone**, so a `region:`
  that disagrees is a config error rather than a failed insert.

**Before you commit,** the dashboard's **Repositories** page has a config
checker that validates a file and shows the machines it would plan.

---

## Step 9 — Verify

Push, then watch in this order. The first job is slower than the rest — a
cold machine downloads the runner and your toolchain, which is what a custom
image removes.

| Where | What you should see |
| --- | --- |
| Airship → **Jobs** | The job queued within a second or two, with its planned machine |
| Airship → **Runners** | `pending → launching → running → busy`, instance ID shown as `zone/name` |
| **Compute Engine** → **VM instances** | One instance, labelled, network tag `airship-runner`, no service account unless you asked for one |
| GitHub → the job log | Labels `self-hosted`, `x64`, `gcp`, `cpu`, `airship-build--…` |
| **Compute Engine** → **VM instances** after the job | Gone. Not `TERMINATED` — deleted, disks and all |
| Airship → **Events** | Every launch failure and degradation, with Google's own message |

The region/zone column matters: a machine in `us-central1` is listed there
and nowhere else. When a machine boots but never registers, read the
instance → **Logs** → **Serial port 1 (console)**.

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
| Watch the bill by label | Billing → Reports, grouped by the `airship-repository` label. Labels take about a day to appear in billing data |
| Remember: no cache acceleration on GCP yet | `cache: true` launches without it and records a warning. Use GitHub's own cache meanwhile. See [gcp.md](gcp.md#caching) |
| Run `TestAgainstLiveGCP` once | It is the cheapest way to turn "tested against a faithful fake" into "accepted by Google" |

---

## Troubleshooting

Start at Airship's **Events** page for every one of these: it records every
launch failure and every degradation with Google's own message quoted. These
causes are the same whichever guide you followed.

### Reading a Google Cloud error

Google's error codes read as sentences, and sort into three kinds:

- **`PERMISSION_DENIED`, `…forbidden`** — a missing role (Step 3), a
  disabled API (Step 1) or an org policy (Step 0). The message names the
  permission it wanted, like `compute.instances.create`.
- **`QUOTA_EXCEEDED`** — a quota. Yours to raise (Step 5). Note it arrives
  *after* the insert was accepted, which is why Airship waits for the
  operation rather than trusting the initial 200.
- **`ZONE_RESOURCE_POOL_EXHAUSTED`** — Google has run out of that shape in
  that zone right now. Nothing is misconfigured; Airship rotates zones.

### Table

| Symptom | Cause | Fix |
| --- | --- | --- |
| Job stays queued and **no job events appear at all** | GitHub is sending no webhooks — most often an app subscribed to no events | The app's **Permissions & events** → **Subscribe to events** needs **Workflow job**, **Push** and **Repository**. Then **Advanced** → **Recent Deliveries**. Connecting a cloud and activating repositories keep working without webhooks, which is why it *looks* healthy |
| Connect fails: *key file type is …* | Not a service account key (an OAuth client, or an ADC file) | Create a key as in Step 4 |
| Connect fails on `projects.get` | API not enabled, or the role lacks it | Step 1; add **Compute Viewer** |
| Cannot create a key at all | `iam.disableServiceAccountKeyCreation` | Step 0. There is no key-free path today |
| Launch fails: `ZONE_RESOURCE_POOL_EXHAUSTED` | That zone is out of that shape | Expected and handled — Airship rotates zones. If every zone fails, add shapes to `instance_type` or a second `region` |
| Launch fails: `QUOTA_EXCEEDED` | A regional quota | Raise it (Step 5); lower `max_runners` meanwhile. Check `CPUS_ALL_REGIONS` too |
| Launch fails: *router airship-nat … is on another network* | A router of that name exists on a different network | Rename or move it. Airship refuses to reuse it rather than boot a machine with no egress |
| Launch fails: *router … has no NAT* | Something created a bare router with that name | Add a NAT to it, or delete it so Airship can make it |
| Launch fails: external IP refused | `compute.vmExternalIpAccess` | `enable_public_ip: false` everywhere, plus **Compute Network Admin** |
| Launch fails: Shielded VM required | `compute.requireShieldedVm` wants Secure Boot | Step 0 — Airship deliberately leaves Secure Boot off |
| Launch fails: `RESOURCE_IN_USE_BY_ANOTHER_RESOURCE` | A read-write `existingDisk` is held by another runner | Expected: treated as retryable, and the job waits its turn |
| Launch fails: *disk … is in zone X, outside region Y* | A zonal `existingDisk` and a conflicting `region:` | Pin the zone, or use a regional disk |
| Launch fails naming an unknown machine type | That shape does not exist in that region | Not every type is in every region. Check the machine-type list with the zone selected |
| Instance exists, runner never registers | No egress | Private instance without a NAT, or a NAT on the wrong network. Check the routers list, then the instance's serial console output |
| Instance sits `TERMINATED` | The machine powered itself off; Compute Engine does not delete on shutdown | Expected. The next inventory pass deletes it. If it lingers, the credential or the inventory scan is failing — check Events |
| Runner finished `launch_unconfirmed` | The insert's answer was lost | Handled: the job is retried, the scan reconciles the machine. Repeated occurrences mean a network problem between control plane and Google |
| `cache: true` does nothing | Expected on GCP | Events records `cache.unavailable`. [gcp.md](gcp.md#caching) |
| You cannot find a resource you just made | The console is scoped to another project, region or zone | Check the project picker in the top bar, then the zone column |

---

## Turning it off

As on AWS: deactivate a repository to stop it; uninstall the GitHub App to
stop everything (every runner is finished with reason `deleted` and its
instance removed). Disconnecting the credential is refused while runners
launched with it are alive.

Left behind by design, to delete by hand if you are done: any
`airship-nat` **Cloud Router** (**Network services** → **Cloud NAT**), and
the service account itself. A Cloud Router with a NAT **does** bill while it
exists, unlike AWS's free security group, so this is worth doing rather than
leaving. Then check **Compute Engine** → **Disks** for anything unattached:
Airship deletes disks with their instances, so that list should be empty. If
the project was dedicated to CI, shutting the whole project down is the
cleanest end state.

---

## Glossary

| Term | What it means here |
| --- | --- |
| **ADC** | Application Default Credentials — the file `gcloud auth` writes for your own use. *Not* a service account key; Airship rejects it |
| **Compute Engine** | Google's virtual machine service. One instance per CI job |
| **Disk type** | `pd-standard` (slow, cheap), `pd-balanced`, `pd-ssd` (fast). A faster disk is often a cheaper job |
| **IAM** | Identity and Access Management — principals, roles and the bindings that join them |
| **Instance / machine type** | A machine's shape, e.g. `n2-standard-8`: family `n2`, 8 vCPU. The trailing number is the vCPU count |
| **Label** | A key/value tag on a resource. `airship-installation` is how Airship recognises its own machines; labels also drive billing reports |
| **Network tag** | A name firewall rules match on. Every runner gets `airship-runner` |
| **NAT / Cloud Router** | What gives a machine with no external IP a route out. Airship creates one per region/network for `enable_public_ip: false`, and it bills while it exists |
| **Organization policy** | A constraint set above the project that the project cannot override. See Step 0 |
| **Preemptible / Spot** | 60–90% cheaper, reclaimable by Google at short notice (`preemptible: true`) |
| **Project** | GCP's unit of isolation and billing. Its **ID** (globally unique) is what commands want, not its display name |
| **Quota** | A per-project, usually per-region cap. The usual real limit on concurrent runners |
| **Region / zone** | `us-central1` is a geographic area; `us-central1-b` is one data centre in it. GPUs and disks are zonal |
| **Role** | A named bundle of permissions, e.g. Compute Instance Admin (v1) |
| **Service account** | A robot identity owned by the project, named like an email address |
| **Service account key** | The JSON file that authenticates as a service account. The credential you give Airship |
| **Shared VPC** | A network owned by a host project and used by others. Needs Compute Network User on the subnet |
| **VPC network** | Your private network. Every project starts with one called `default`, with a subnet per region |
