# Setting up Airship on Daytona

**What this is.** The path from a Daytona organization to a green CI job on
your own sandboxes. It is the shortest of the four setups: there is no
network to build, no IAM to write and no quota to request, only an API key,
a region and a tier.

**Audience.** Whoever owns the Daytona organization and whoever owns the
GitHub organization. **No prior Daytona experience is assumed** — and if you
have come from the AWS or Azure guides, this one is deliberately much
shorter, because there genuinely is less to do.

**Time.** 10–15 minutes, most of it waiting for a first job.

**Status.** Read the status note at the top of [daytona.md](daytona.md).
The Daytona provider has run three real GitHub jobs on real sandboxes
launched through the provider itself: from an image with Docker, from a
snapshot, and with a volume mounted. Allow lists, GPUs and the dedicated
region were sent and refused for this organization's tier, credits and
region, each as a clear permanent error; no GPU sandbox has started.

---

## Orientation — if you have never used Daytona

Daytona runs **sandboxes**: container-based machines started from a
container image, in a region, at a size. Airship starts one per CI job and
deletes it when the job ends. That is the whole integration — you hand
Airship one API key and it calls Daytona's API the same way you would.

| The thing | In one sentence | Why Airship needs it | Step |
| --- | --- | --- | --- |
| **An organization** | Daytona's unit of ownership and billing; sandboxes, keys and snapshots belong to one | Sandboxes launch here and you pay for them directly | [1](#step-1--pick-or-create-the-organization-daytona-admin) |
| **A tier** | Your plan. It caps the size of one sandbox *and* how many can run at once | Sets the largest shape and the real ceiling on concurrent runners | [1](#step-1--pick-or-create-the-organization-daytona-admin) |
| **An organization API key** | A token that authenticates API calls, with per-action permissions | How Airship proves it may create and delete sandboxes | [2](#step-2--create-an-api-key-daytona-admin) |
| **A region** | `us` or `eu` (shared), or a dedicated region of your organization | Where sandboxes run | [5](#step-5--connect-daytona-github-org-admin) |
| **An image or snapshot** *(optional)* | What a sandbox boots from. The default is `ubuntu:22.04` | Baking your toolchain in turns a minute of boot into seconds | [3](#step-3--prepare-an-image-or-a-snapshot-optional) |

### How this differs from AWS, GCP and Azure

- **A sandbox is a container, not a virtual machine.** It starts in seconds,
  and it shares a kernel with its host. The practical consequence is in Step
  6: `nproc` reports the *host's* CPU count, not your sandbox's limit, so
  build parallelism has to be pinned by hand.
- **There is no network to configure and no quota to request.** Your tier is
  the quota, and egress works by default. Egress restriction is a per-tier
  feature (`domain_allow_list`, Tier 3+), not something you build.
- **There is no cache acceleration.** `cache: true` launches without the
  proxy and records a warning; use GitHub's own cache.
- **The organization is the security boundary, and it is a real one.**
  Anyone holding one of the organization's API keys can run commands inside
  any of its sandboxes through Daytona's toolbox API — including a job that
  is running right now. That is why a CI-only organization matters more here
  than on the other clouds. See
  [security.md](security.md#known-exposures-stated-plainly), exposure 8.

### What this will cost

You pay Daytona directly for sandbox time, per second, against your plan and
your organization's credits. Airship adds nothing that idles: one sandbox
per job, deleted when the job ends, plus a hard 8-hour lifetime and a
10-minute idle reaper. Current rates and your remaining credits are in the
Daytona dashboard under the organization's billing page — check them there
rather than trusting a number written here.

The two things that bound the bill are `max_runners` (Airship's ceiling on
concurrent sandboxes) and your tier's own concurrent limit. Set the first
below the second, so hitting the ceiling is a dashboard message rather than
a stream of refusals. GPU sandboxes need GPU credits in the wallet and are
billed separately — an empty wallet is a clear error, not a surprise
invoice.

---

## Before you start

| You need | Notes |
| --- | --- |
| A Daytona organization, ideally dedicated to CI | Anyone with one of the organization's API keys can run commands in any of its sandboxes through Daytona's toolbox API, a running job's included. A CI-only organization makes that set exactly the people who may read your CI. See [security.md](security.md#known-exposures-stated-plainly), exposure 8 |
| A tier that fits your shapes | The tier sets the per-sandbox maximum CPU, memory and disk and the organization's concurrent total. The entry tier's per-sandbox disk is 10 GB, which is Airship's default `disk`. Tier 1 and 2 cannot set sandbox-level allow lists; GPU sandboxes need GPU credits in the wallet |
| Admin on the GitHub organization | To install the app and activate repositories. If that is not you, Steps 4–6 need the person it is |
| A region | `us` or `eu` (shared), or a dedicated region of your organization |
| A concurrency number | Becomes `max_runners`. Start under your tier's concurrent sandbox limit |
| A control plane | Airship-hosted, or your own ([operations.md](operations.md)) |

---

## Step 1 — Pick or create the organization *(Daytona admin)*

Sign in at [`app.daytona.io`](https://app.daytona.io), create an
organization for CI (or choose one), and find its tier limits under the
organization's settings. Write down three numbers before you go further,
because every later surprise traces back to one of them:

- **Maximum CPU, memory and disk per sandbox** — the biggest shape you can
  ask for in `instance_type`. Exceeding it is a *permanent* error, not
  something that retries.
- **Maximum concurrent sandboxes** — the ceiling `max_runners` must sit
  under.
- **Your tier number** — Tier 1 and 2 cannot set sandbox-level allow lists
  at all, so `domain_allow_list` in a definition will be refused.

If your organization has a dedicated region, note its ID: it appears in the
region list Airship checks at connect time and works as a `region:` value,
as long as it hosts the `container` sandbox class (a region of Windows hosts
is refused at connect time, naming the regions that run Linux).

---

## Step 2 — Create an API key *(Daytona admin)*

In the dashboard, **API Keys → Create**, as an **organization** key — not a
personal one, which Airship refuses because it belongs to no organization.
Airship needs two permissions and refuses a key without them at connect
time:

| Permission | Needed for |
| --- | --- |
| `write:sandboxes` | Creating sandboxes, setting their labels, reading them and listing them |
| `delete:sandboxes` | Deleting a sandbox when its job ends, when a launch cannot finish, and when the inventory scan finds a leak |

Add the volume and snapshot permissions only if your definitions mount
volumes or launch from snapshots. Nothing else: Airship never creates
images, snapshots, volumes or keys.

**Expiry.** Daytona keys can expire. Airship refuses an already-expired key
and otherwise stores the key as is; a key that expires later fails every
launch from that date, with Daytona's message on the Events page. Either
create the key without an expiry or diarise the reconnect.

The key is shown once — copy it now, and treat it like a password: it goes
into Airship and nowhere else, not a repository, not a Dockerfile, not
Slack. Airship verifies it by reading its own record (which also tells
Airship the organization), seals it with AES-256-GCM, opens it only for the
duration of one API call, and never returns it; the dashboard shows the
organization ID, the key's name and the key masked to its first and last
three characters.

If you lose it, create another and reconnect — that costs nothing, and a
second key of the same organization is always accepted.

---

## Step 3 — Prepare an image or a snapshot *(optional)*

Skip this for your first job; the default works. Come back when boot time
starts to annoy you.

Airship launches from a **container image** by default, `ubuntu:22.04`, and
installs what the runner needs at boot (a minute or so, Docker included).
Two ways to make launches faster:

- **Your own image.** Any image Daytona can pull, with a tag or digest.
  Bake `curl`, `git`, `sudo`, `ca-certificates`, your toolchain and, to
  skip the download, `actions/runner` into `/opt/actions-runner` with the
  version in `/opt/actions-runner/.version`. Debian or Ubuntu based images
  are expected: the boot script uses `apt-get` for anything missing. The
  first launch of a new image pays for Daytona's build of it; later ones
  start from the cache in seconds.
- **A snapshot.** Create one in Daytona (dashboard or `daytona snapshot
  create`) from an image, with the CPU, memory and disk you want. A
  definition names it as its `instance_type`, with no `machine_image` and
  no `disk`, since the snapshot fixes all three. Daytona's own snapshots
  (such as `daytona-medium`) run as a sudoer rather than root; the boot
  script handles both.

A private registry needs a registry entry in Daytona first, or the build
fails with "could not pull".

---

## Step 4 — Install the GitHub App *(GitHub org admin)*

Identical to the AWS path — the app's permissions are about GitHub, not
about your cloud. See
[aws-setup-console.md](aws-setup-console.md#step-4--install-the-github-app-github-org-admin)
for the permission table and why each one is needed. Installing the app
activates nothing by itself; Step 6 is the switch that matters.

---

## Step 5 — Connect Daytona *(GitHub org admin)*

**Dashboard:** **Cloud → Daytona**, paste the API key, leave the API URL at
`https://app.daytona.io/api` unless you run Daytona elsewhere, and choose
the default region.

**Or the developer API.** Create an API token in the dashboard under
**API keys**, then:

```bash
export AIRSHIP_API=https://api.example.com
export AIRSHIP_TOKEN=asa_...

curl -fsS -X POST "$AIRSHIP_API/api/v1/cloud-connect" \
  -H "Authorization: Bearer $AIRSHIP_TOKEN" -H 'Content-Type: application/json' \
  -d '{"cloud": "daytona", "credentials": {"api_key": "dtn_...", "region": "us"}}'
```

`api_url` is accepted alongside, for a self-hosted Daytona.

On submit, Airship reads the key's record (organization, expiry,
permissions) and the organization's regions, refuses a region the
organization cannot launch in, and **echoes back the organization ID and the
key's name** — read them, and confirm it is the organization you meant.

If it is rejected, the message says which of four things is wrong: a missing
permission (Step 2), a personal rather than organization key (Step 2), an
expired key (Step 2), or a region your organization cannot use (Step 1).
Nothing is stored on a failure, so fix and retry.

One credential per cloud per installation: AWS, GCP, Azure and Daytona can be
connected at the same time, and each runner definition names its own
`cloud:`. Replacing the key with another key of the same organization is
always allowed; a key of a different organization, or disconnecting, is
refused while runners launched with it are alive, because without it their
sandboxes could not be deleted.

---

## Step 6 — Activate repositories and commit a config

Activation works exactly as on the other clouds: **Repositories →** toggle
on, public repositories need the separate public-access choice, and an
`airship.yaml` in a repository nobody activated launches nothing. Start with
a private repository for your first job.

```yaml
runners:
  - name: build
    cloud: daytona
    instance_type: [4vcpu-8gb, 2vcpu-4gb]    # shapes, tried in order
    machine_image: ubuntu2404                # or any image with a tag or digest
    region: us
    labels: [airship-build]
    extra_config:
      disk: 10
      docker: true                           # jobs that use containers

  - name: golden
    cloud: daytona
    instance_type: ci-golden                 # a snapshot: image and size are its own
    labels: [airship-golden]

  - name: gpu
    cloud: daytona
    instance_type: 8vcpu-32gb
    gpu: H100
    preemptible: true                        # spot: GPU sandboxes only
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

Choosing the shape, if you have no idea: start at `4vcpu-8gb`, keep it
within your tier's per-sandbox maximum, and change it once you have seen a
real job's timings. Full list of knobs:
[configuration docs](../airship-yaml.md).

Three Daytona-specific notes:

- **`instance_type` is a shape or a snapshot.** `<n>vcpu-<m>gb` builds from
  `machine_image`; anything else is a snapshot name, and `machine_image`
  and `disk` must then be absent.
- **`nproc` inside a sandbox is the host's.** CPU and memory are cgroup
  limits. Pass build parallelism explicitly (`-j4`, `--max-workers`) or a
  `4vcpu-8gb` sandbox will try to run 48 compiler processes — which shows up
  as a job killed for memory, not as a clear error.
- **No cache acceleration.** `cache: true` launches without the proxy and
  records a warning; use GitHub's own cache.

**Before you commit,** the dashboard's **Repositories** page has a config
checker that validates a file and shows the sandboxes it would plan.

---

## Step 7 — Verify

| Where | What you should see |
| --- | --- |
| Airship → **Jobs** | The job queued within a second or two, with its planned shape |
| Airship → **Runners** | `pending → launching → running → busy`, instance ID the sandbox's ID |
| Daytona dashboard → Sandboxes | One sandbox named after the runner, labelled `airship-installation`, `airship-runner-id`, `airship-repository`, `airship-boot: started` |
| GitHub → the job log | `Machine name` is the sandbox ID; labels `self-hosted`, `x64`, `daytona`, `cpu`, `airship-build--…` |
| Daytona dashboard after the job | Gone. Not stopped: deleted |
| Airship → **Events** | Every launch failure and degradation, with Daytona's own message |

Timing to expect from a plain image, from our run: sandbox created, built,
started, scripted and labelled in under a minute (five seconds when the
image is cached and Docker is off); the runner online on GitHub about
ninety seconds after the launch began with `docker: true`; the sandbox gone
twenty seconds after the job ended. The first launch of a new image is the
slow one, because Daytona builds it; the next is from cache.

**The first-week test, Daytona edition:** cancel a queued job and a running
job, and confirm no sandbox survives either. Then stop a sandbox by hand from
the Daytona dashboard while its job runs: the job fails on GitHub, Daytona
deletes the sandbox at once (it is created to delete on stop), and the next
inventory pass marks the runner `lost`. Finally, create a sandbox by hand
with an `airship-installation` label and no `airship-boot` label: after ten
minutes the scan deletes it as a leak.

---

## Going to production

| Step | Why |
| --- | --- |
| Set `max_runners` under your tier's concurrent sandbox limit | The one number that bounds total spend, and the difference between a dashboard message and a stream of `exceeds` errors |
| Bake an image or make a snapshot | Boot in seconds instead of installing packages, Docker and the runner each time |
| One Daytona organization per GitHub installation | The toolbox API is organization-wide; the organization is the isolation boundary |
| `domain_allow_list` on definitions that need it (Tier 3+) | Restricts the sandbox's egress to GitHub and your registries. Enforcement is strict: include `github.com`, `api.github.com`, `*.actions.githubusercontent.com`, `pipelines.actions.githubusercontent.com`, `objects.githubusercontent.com`, the image's package mirrors, `download.docker.com` with `docker: true`, and whatever the job fetches. On Tier 1 and 2 leave it out: Daytona refuses it |
| Pin build parallelism | `nproc` is the host's |
| Idle pools where latency matters | `idle: {replicas: 2}` or `schedule:` windows; they count toward `max_runners` |
| A key without an expiry, or a reconnect reminder | An expired key fails every launch |
| Watch the first GPU launch | GPU requests have been sent and refused for credits; none has started |

---

## Troubleshooting

Start at Airship's **Events** page for every one of these: it records every
launch failure and every degradation with Daytona's own message quoted.

Daytona's errors sort into two kinds, and the difference decides what you
do. A **permanent** error means the request can never succeed as written —
a shape above your tier, an allow list your tier forbids, an image that does
not exist — and Airship fails the job rather than retrying. A **retryable**
error means capacity or timing, and Airship tries again on its own.

| Symptom | Cause | Fix |
| --- | --- | --- |
| Connect fails: *the key lacks the write:sandboxes permission* | A key created without it | Step 2 |
| Connect fails: *the key belongs to no organization* | A personal key | Create an organization key |
| Connect fails: *region "x" is not available to the organization; its regions are us, eu* | A typo, or a dedicated region of another organization | Use one of the listed regions |
| Connect fails: *region "us-west-4" hosts only windows sandboxes for the organization, and Airship runners are Linux containers; regions with Linux sandboxes: us* | The region is yours but hosts another sandbox class | Use a region from the list; a Windows job belongs on EC2 or Compute Engine |
| Launch fails: *exceeds maximum allowed per sandbox* | A shape above your tier's per-sandbox limit | Smaller shape, or a higher tier. This is permanent, not a wait |
| Launch fails with a quota or limit message and retries | The organization is at its concurrent limit | Expected; lower `max_runners` or raise the tier |
| Launch fails: *daytona could not build the image* | The image does not exist, has no such tag, or Daytona cannot pull it | Check the reference; private registries need a Daytona registry entry |
| Launch fails: *sandbox did not start in time* | A slow first build, or no capacity | Retried automatically; the next attempt finds the cached build |
| Launch fails: *sandbox was destroyed before it started: no capacity was free in time* | Daytona's queue timed out (spot GPUs mostly) | Retried on the next attempt; add a non-spot fallback |
| Launch fails: *the image lacks … and has no apt-get* | A non-Debian image without `curl`, `git`, `sudo` or `tar` | Bake them in, or use a Debian or Ubuntu base |
| Launch fails: *Network access is restricted and cannot be overridden at the sandbox level* | An allow list on Tier 1 or 2 | Remove `network_allow_list` / `domain_allow_list`, or move to Tier 3 |
| Launch fails: *Organization doesn't have GPU credits* (or *spot GPU credits*) | No GPU credits in the wallet | Add credits, or drop `gpu` |
| Launch fails: *Declarative builds are not available to your organization in region …*, *Snapshot … is not available in region …* or *No runners are configured in region … for sandbox class 'container'* | A dedicated region without builds, a snapshot published elsewhere, or a region that hosts no Linux sandboxes (ours hosts Windows only) | Create the snapshot in that region if it takes containers, or use a shared region |
| Runner never registers, sandbox exists | Egress blocked by an allow list | Add GitHub's hosts to `domain_allow_list` |
| `docker: true` but Docker is missing in the job | The daemon did not come up within a minute; the boot log says `docker unavailable` | The sandbox must be able to reach `download.docker.com`; check the allow list |
| A job runs out of memory at `-j$(nproc)` | `nproc` is the host's | Pin the parallelism |
| `cache: true` does nothing | Expected on Daytona | Events records `cache.unavailable`. [daytona.md](daytona.md#caching) |
| Every launch fails with an authentication message | The key expired or was revoked | Reconnect with a new key |

---

## Turning it off

As on the other clouds: deactivate a repository to stop it; uninstall the
GitHub App to stop everything (every runner is finished with reason
`deleted` and its sandbox removed). Disconnecting the credential is refused
while runners launched with it are alive.

Nothing is left behind by design: Airship creates sandboxes only, and
deletes them. Check the dashboard's Sandboxes list is empty, then revoke the
API key in the Daytona dashboard once you are done.

---

## Appendix — glossary

| Term | What it means here |
| --- | --- |
| **Credits** | Prepaid balance the organization spends on sandbox time. GPU sandboxes draw on a separate GPU credit balance |
| **Image** | A container image a sandbox boots from, e.g. `ubuntu:22.04`. Baking your toolchain in is the biggest speed win |
| **Organization** | Daytona's unit of ownership and billing, and the security boundary: one API key reaches every sandbox in it |
| **Organization API key** | The token Airship authenticates with. Needs `write:sandboxes` and `delete:sandboxes`; may expire |
| **Permanent vs retryable error** | Whether the request can ever succeed as written. Permanent fails the job; retryable is tried again |
| **Region** | `us` or `eu`, or a dedicated region of your organization. Must host the `container` sandbox class |
| **Sandbox** | One container-based machine. Airship runs one CI job per sandbox and then deletes it |
| **Sandbox class** | What kind of host a region runs. Airship needs `container`; a Windows region is refused at connect time |
| **Shape** | A size, written `<n>vcpu-<m>gb`. Must sit within your tier's per-sandbox maximum |
| **Snapshot** | A pre-built sandbox template that fixes image, CPU, memory and disk together. Named in `instance_type` instead of a shape |
| **Tier** | Your plan. Sets per-sandbox maximums, the concurrent sandbox limit, and whether allow lists are available |
| **Toolbox API** | Daytona's API for running commands inside a sandbox. Organization-wide, which is why a CI-only organization matters |
