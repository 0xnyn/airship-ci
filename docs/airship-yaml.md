# `airship.yaml` reference

Runners are defined in `airship.yaml` at the root of each activated
repository. The fields below are the ones Airship acts on for AWS, Google
Cloud and Daytona. Anything else is an error that names the field.

```yaml
runners:
  - name: build
    cloud: aws
    instance_type: [c7i.xlarge, m7i.xlarge, c7a.xlarge]   # tried in order
    machine_image: ami-0123456789abcdef0                   # or ubuntu2204 / ubuntu2404
    region: us-east-1
    preemptible: [true, true, false]                       # zipped with instance_type
    labels: [airship-build]
    extra_config:
      BlockDeviceMappings:
        - DeviceName: /dev/sda1
          VolumeSize: 100
          VolumeType: gp3
          Iops: 6000
          Throughput: 1000
          VolumeInitializationRate: 300
      env_vars:
        CARGO_INCREMENTAL: "0"
      cache: true
    idle:
      replicas: 2
```

```yaml
runners:
  - name: gpu
    cloud: gcp
    instance_type: n1-standard-4
    machine_image: ubuntu-2204-lts                         # a family, project:image, or a URL
    region: us-central1-b                                  # a zone, or a region to pick any zone
    gpu: nvidia-tesla-t4
    preemptible: true
    labels: [airship-gpu]
    extra_config:
      baseDisk: {diskSizeGb: 100}
      enable_public_ip: false
```

```yaml
runners:
  - name: sandbox
    cloud: daytona
    instance_type: 4vcpu-8gb                               # a shape, or the name of a Daytona snapshot
    machine_image: "ghcr.io/acme/ci:2026.10"               # a container image with a tag or digest
    region: us
    labels: [airship-sandbox]
    extra_config:
      disk: 10                                             # GB; snapshots fix their own
      docker: true                                         # a Docker daemon inside the sandbox
      domain_allow_list: "github.com, *.githubusercontent.com, registry.npmjs.org"
```

```yaml
# .github/workflows/ci.yml
jobs:
  test:
    runs-on: airship-build--${{ github.run_id }}
```

## Turning it on

1. Install the GitHub App on the repository.
2. Connect AWS or Google Cloud under **Cloud**.
3. Activate the repository under **Repositories**. A config file never
   launches anything in a repository nobody activated.
4. Commit `airship.yaml` and use one of its labels in `runs-on`.

Only labels starting with `airship-` are looked up. Jobs on other labels cost
no API calls.

## Fields

| Field | Required | Notes |
| --- | --- | --- |
| `name` | yes | Unique in the file. Letters, digits, `.`, `_`, `-`. |
| `cloud` | yes | `aws`, `gcp`, `azure` or `daytona`. `do`, `openstack`, `oracle`, `vast_ai` and `on_prem` are recognized and refused with "not supported yet". |
| `instance_type` | yes | EC2 instance type (`c7i.xlarge`), Compute Engine machine type (`n2-standard-4`), Azure VM size (`Standard_D4s_v5`, as the API spells it), or on Daytona a sandbox shape (`2vcpu-4gb`: vCPUs and GB of memory, built from `machine_image`) or the name of a snapshot you made in Daytona (which fixes image and resources). Or a list. |
| `machine_image` | no | AWS: AMI ID, `ubuntu2204` or `ubuntu2404` (Canonical's newest for the architecture). GCP: an image or family name (`ubuntu-2204-lts`, looked up in your project and then in Google's public project for that prefix), `project:image`, `projects/P/global/images/N`, `projects/P/global/machineImages/N` or either one's URL, plus the two aliases. Azure: a marketplace URN (`Canonical:0001-com-ubuntu-server-jammy:22_04-lts-gen2:latest`, or the same as a mapping `{publisher, offer, sku, version}` with `version` defaulting to `latest`), a managed image ID (`/subscriptions/.../providers/Microsoft.Compute/images/N`) or a compute gallery image ID (`.../galleries/G/images/N`, with `/versions/X.Y.Z` to pin one; a specialized image boots with its own accounts), plus the two aliases (Canonical's Ubuntu for the architecture). Daytona: a container image with a tag or digest (`ubuntu:24.04`, `ghcr.io/org/ci@sha256:...`; `latest` is refused, Windows images too), plus the two aliases; must be empty when `instance_type` is a snapshot. Or a list. Default `ubuntu2204` (`ubuntu:22.04` on Daytona). A Windows image gets a Windows boot script. |
| `region` | no | AWS region; GCP zone (`us-central1-b`) or region (`us-central1`, any zone with capacity); Azure region as the API spells it (`eastus`, `uksouth`); Daytona region ID (`us`, `eu`, or a dedicated region of your organization). Or a list. Default: the connected cloud's default. |
| `preemptible` | no | Spot capacity, or a list. Default `false`. On Daytona spot is for GPU sandboxes only, so it needs `gpu`. |
| `labels` | yes | `runs-on` labels this runner serves. Each must start with `airship-`. |
| `gpu` | no | A GPU name. Installs the NVIDIA driver on Ubuntu images that lack one and adds the `gpu` label. On GCP the name is the accelerator attached (`nvidia-tesla-t4`, `nvidia-l4`), one per machine, unless the machine type has its own (`a2`, `a3`, `g2`). On Azure the VM size decides (`NC`, `ND`, `NV`, `NG` families); the name is a flag. On Daytona it is one of its GPU types (`H100`, `H200`, `B200`, `B300`, `RTX-PRO-6000`, `RTX-4090`, `RTX-5090`, `MI355X`), one per sandbox, with the host's driver. Not needed for NVIDIA's images. `false`, `no` or `null` turns it off. |
| `extra_config` | no | See below. |
| `idle` | no | Idle runners; see below. |

The architecture comes from the instance type: Graviton families (`c7g`,
`m8g`, `t4g`, ...), Arm machine families (`t2a`, `c4a`) and Azure sizes
with a `p` in their feature letters (`Standard_D2pls_v5`,
`Standard_E4ps_v5`) are `arm64`, everything else `x64`; Daytona sandboxes
are `x64`. Runners register with
`self-hosted`, the architecture, the cloud name, `cpu` or `gpu`, `airship`, and their
labels. The runner adds its own OS label.

### `extra_config`

| Key | Notes |
| --- | --- |
| `BlockDeviceMappings` | One entry for the root volume. `VolumeSize` (8-16384 GiB, default 75), `VolumeType` (gp2, gp3, io1, io2; default gp2), `Iops` (gp3: 3000-16000; io1 and io2: required; not for gp2), `Throughput` (gp3 only, 125-1000 MiB/s, at most a quarter MiB/s per IOPS, so `Throughput: 1000` needs `Iops: 4000`), `VolumeInitializationRate` (100-300 MiB/s, snapshot-backed AMIs). Combinations EC2 would refuse are config errors rather than failed launches. `DeviceName` is accepted and ignored: the AMI's root device is always the one sized. Volumes are always encrypted. |
| `env_vars` | Up to 50 variables for every job. Not secret. Names starting with `GITHUB_`, `RUNNER_` or `ACTIONS_` are refused. |
| `cache` | `true` runs the cache proxy on the machine; see [caching.md](caching.md). Linux runners on AWS only; on GCP, Azure and Daytona the runner launches without it and the activity log records a `cache.unavailable` warning. |
| `provision_via` | `pull_from_api`: the instance gets no user data; your image fetches the boot script from `GET /api/runner-provision?runner_name=<Name tag>` with an API token, once. Not on Daytona, where a sandbox runs nothing at boot and Airship starts the runner itself. |
| `run_as` | `interactive`: on Windows, a local account logs in automatically and runs the runner in its desktop session. |
| `runner_user` | Windows account for `run_as: interactive` (default `runner`). |
| `runner_path` | Windows install path (default `C:\actions-runner`). |
| `SubnetId` | AWS. Launch in this subnet instead of the default VPC's. |
| `SecurityGroupIds` | AWS. Up to 5 groups instead of the `airship-runners` group. |
| `IamInstanceProfile` | AWS. Name or ARN. Every job on the runner can use it. |
| `baseDisk` | GCP. `diskSizeGb` (10-65536, default 75) and `diskType` (`pd-balanced` by default, `pd-ssd`, or a `zones/.../diskTypes/...` path) of the boot disk. |
| `additionalDisk` | GCP. `diskSizeGb` and `diskType` of an empty second disk, attached raw; the job formats and mounts it. A `diskType` written as a zonal path (`projects/P/zones/Z/diskTypes/pd-balanced`) is read for its type only; the disk is created in whichever zone the runner launches in. |
| `existingDisk` | GCP. Attach a disk you already have: `source` (`projects/P/zones/Z/disks/N`, or `regions/R/disks/N` for a regional disk), optional `deviceName`, `mode` (`READ_WRITE`, the default, or `READ_ONLY`), `interface` (`SCSI`, `NVME`); `type` and `diskSizeGb` are accepted and ignored, since the disk already exists. The disk is never deleted with the machine. A zonal disk pins the runner to its zone, so a `region` that says otherwise is a config error. A `READ_WRITE` disk fits one machine at a time: while another runner holds it, launches fail as retryable and the job waits for its turn. |
| `project_id` | GCP. Launch in this project instead of the service account's. The account needs Compute Admin there too. |
| `networkInterfaces` | GCP. Up to 8 of `network`, `subnetwork`, `nicType` (`GVNIC`, `VIRTIO_NET`, `IDPF`), `stackType` and one `accessConfigs` entry, as Compute Engine spells them. Without it the default network with an external address. |
| `serviceAccounts` | GCP. One `email` with `scopes`. Every job on the runner can use it. |
| `enable_public_ip` | GCP and Azure. `false` launches without an external address; the first such launch in a region creates a NAT (on GCP a Cloud Router and NAT on the runner's network: `airship-nat` on the default network, `airship-nat-<network>` on another, found through the `subnetwork` when only that is named; on Azure a NAT gateway `airship-nat-<region>` with a static address of its own, attached to the `airship-runners-<region>` network's subnet), which every later runner there shares. Azure's default is a public address per machine, since Airship's subnet is private, as Azure makes new networks' subnets by default for API versions released after March 31, 2026. |
| `scheduling` | GCP. `onHostMaintenance: MIGRATE` keeps a runner alive through host maintenance (the default for machines that can); `TERMINATE` is the default for GPUs and spot. `MIGRATE` with `preemptible` or a GPU is a config error, as Compute Engine would refuse it. |
| `storageProfile` | Azure. `osDisk.diskSizeGB` (30-4095; default 75 for Linux, the image's own size for Windows) and `osDisk.managedDisk.storageAccountType` (`Standard_LRS`, `StandardSSD_LRS`, `Premium_LRS`, ...; default the image's) of the OS disk, as Azure spells them. |
| `disk` | Daytona. Disk of a sandbox built from an image, in GB (default 10, the smallest tier's per-sandbox maximum; larger tiers allow more). A snapshot fixes its own disk, so `disk` with only snapshot variants is a config error. |
| `docker` | Daytona. `true` installs Docker (static 28.3.3, when the image has none) and starts a daemon in the sandbox before the runner, for jobs that use containers; the `runner` user is in the `docker` group. If the daemon does not come up the job runs without Docker and the boot log says so. |
| `volumes` | Daytona. Up to 10 of `volume` (a Daytona volume's name or ID), `mount_path` (absolute) and optional `subpath`, mounted into the sandbox as an S3-backed FUSE filesystem (`mountpoint-s3`), writable and shared between sandboxes. |
| `network_allow_list` | Daytona. Comma-separated IPv4 CIDRs the sandbox may reach, at most 10. Tier 3 and above only: on Tier 1 and 2 Daytona's organization-wide restriction applies and a sandbox-level list is refused (a permanent launch error). One of `network_allow_list` and `domain_allow_list`, not both. |
| `domain_allow_list` | Daytona. Comma-separated domains and wildcards (`github.com, *.githubusercontent.com`) the sandbox may reach, at most 100; same tier rule as `network_allow_list`. Enforcement is strict, so list everything the runner and the job fetch: `github.com`, `api.github.com`, `*.actions.githubusercontent.com`, `pipelines.actions.githubusercontent.com`, `objects.githubusercontent.com`, the image's package mirrors, and `download.docker.com` with `docker: true`. |

Keys of another cloud are refused by name (`baseDisk is a GCP setting;
this runner's cloud is aws`), so a definition copied between clouds fails
on the first wrong key rather than silently ignoring it.

### Fallbacks

`instance_type`, `machine_image`, `region` and `preemptible` each take a
value or a list. Lists are zipped: the first attempt uses the first entry
of every list, and a single value applies to every attempt. Lists of
different lengths are an error.

When a launch fails, the next attempt is tried. Two runners that share a
label are also one fallback chain, in file order. After the last attempt, a
failure the cloud marks retryable (capacity, throttling) starts over from
the first, until the job has had three failed launches or one per attempt,
whichever is more. Other failures (a missing image, a type the account
cannot launch) stop once every attempt has been tried.

### Suffixes

`runs-on: airship-build--${{ github.run_id }}` makes the runner register
with the suffixed label instead of the plain one, so GitHub can only hand
it a job from that run. Everything from `--` on is ignored when matching
the file. Matrix jobs can use
`airship-build--${{ matrix.version }}-${{ github.run_id }}`. Do not use a
suffix with idle runners: they exist before the run, so they cannot carry
its ID.

## One-line runners

In an activated repository, a label can name the machine itself, with no
config file:

```yaml
runs-on: airship-aws-t2-large-ubuntu2204--${{ github.run_id }}   # t2.large, Ubuntu 22.04
runs-on: airship-aws-c7g-xlarge-spot                             # Graviton, spot
runs-on: airship-aws-m7i-metal-24xl                              # m7i.metal-24xl
```

The label is `airship-aws-` and the instance type with `.` written as `-`,
then optionally `ubuntu2204` (the default) or `ubuntu2404` and `spot`, in
either order. The family must look like one of EC2's (`c7i`, `c7i-flex`,
`u7i-12tb`, `mac2-m2`); a label that puts `spot` or the image token
elsewhere is not a one-line runner. They launch in the connected cloud's
default region with a 75 GiB root volume. A definition in a config file
whose labels include the same label always wins. A job that also asks for
`windows` is only served by a definition with a custom AMI: the Ubuntu
images are Linux.

## Which file a job reads

A job reads `airship.yaml` at its own commit, so a branch (or a pull
request) can try a new machine type before it is merged. Idle runners are
the exception: they are read from the default branch only, since they
exist before any job does. Pushes to the default branch update them within
a reconcile pass.

A job is planned once, when GitHub first reports it queued. Editing the
file afterwards does not change how a waiting job is served.

## Idle runners

`idle:` keeps runners booted and registered, so a job starts in seconds. A
pool runner that takes a job is replaced straight away; it is still one
machine per job. An idle runner is replaced halfway through the maximum
runner lifetime (after 4 hours by default), so a job it takes still has
the other half before the machine's own deadline. Exactly one form:

```yaml
idle:
  replicas: 2            # always
```

```yaml
idle:
  schedule:              # overlapping windows take the most
    - day: mon-fri
      time: 9-17         # from 9:00 until 17:00
      timezone: Europe/Berlin
      replicas: 4
    - day: sat-sun
      time: 0-24
      replicas: 1         # required in every window
```

```yaml
idle:
  usage_based:           # stay warm for a while after jobs arrive
    trigger: runner_allocated
    replicas: 2
    duration: 30         # minutes after the last job started
```

A pool whose runners keep failing to launch pauses for ten minutes after
three failures. Pools count toward the organization's `max_runners`.

Idle runners in `airship.global.yaml` are organization-wide: they register
on the organization, in a runner group named `airship-<runner name>` that
is visible to every activated private repository, or only to the
repositories the runner's `access.yaml` policies name. Any such repository
can take one; the GitHub App needs the organization's **Self-hosted
runners** write permission for this.

## Organization runners

Create a repository named `.airship` in the organization, give the app
access to it, and add `airship.global.yaml` (same format, `idle` included).
Any activated repository can then use its labels. Repository definitions
are tried before organization ones.

Restrict who can use them with `access.yaml` in the same repository:

```yaml
version: 1
policies:
  - id: ml-team
    repo: training          # a repository in this organization
    teams: [ml]             # team slugs
    roles: [admin, maintain] # repository roles
    users: [alice]
    pull_request: false     # pull_request workflows need true
access_control:
  - resource: gpu-runner    # a runner name from airship.global.yaml
    policies: [ml-team]
```

A runner with no `access_control` entry is open to every activated
repository. A restricted runner can only be launched for a repository
named in one of its policies, by a person the policy lists (by login, team
or role); a policy that lists nobody admits everyone on that repository.
The person is whoever started the run, or re-ran it. If `access.yaml` does
not parse, every organization runner is closed until it is fixed.

The developer API edits this file for you, through a pull request:
`PUT /api/v1/access-control`.

## When a job is not run

The **Jobs** page says why:

| Reason | Meaning |
| --- | --- |
| Not activated | The repository has not been activated under **Repositories**. Activating it plans the jobs still queued. |
| Config problem | `airship.yaml` does not parse; the detail names the field. |
| No runner serves the labels | No definition covers every `airship-` label of the job, or none can satisfy its other labels. |
| Access denied | `access.yaml` does not let this person, repository or event use the runner. |
| No cloud | The runner's cloud is not connected. Connecting one plans the jobs still queued. |
| Public repository | The repository is public and was not activated with public access allowed. Activating it that way plans the jobs still queued. |

**Repositories** also has a checker that validates a file before you commit
it.

## Developer API

Tokens are created under **API keys** and sent as a bearer token:

```sh
export AIRSHIP_API=https://api.example.com/api/v1
export AIRSHIP_API_KEY=asa_...
curl -X POST $AIRSHIP_API/repo -H "Authorization: Bearer $AIRSHIP_API_KEY" \
  -H "Content-Type: application/json" -d '{"repository": "acme/api", "active": true}'
curl -X POST $AIRSHIP_API/cloud-connect -H "Authorization: Bearer $AIRSHIP_API_KEY" \
  -H "Content-Type: application/json" -d '{"cloud": "daytona", "credentials": {"api_key": "dtn_...", "region": "us"}}'
```

| Route | Does |
| --- | --- |
| `GET /api/v1/repo` | List repositories |
| `POST /api/v1/repo` `{"repository": "owner/name", "active": true}` | Activate or deactivate |
| `GET /api/v1/cloud-connect` | List connected clouds |
| `POST /api/v1/cloud-connect` `{"cloud": "aws", "credentials": {"access_key", "secret_key", "region"}}` or `{"cloud": "gcp", "credentials": {<the service account key file's fields>, "region"}}` or `{"cloud": "azure", "credentials": {"subscription_id", "tenant_id", "client_id", "client_secret", "region"}}` or `{"cloud": "daytona", "credentials": {"api_key", "api_url" (optional), "region"}}` | Connect |
| `GET /api/v1/access-control` | Read `access.yaml` |
| `PUT /api/v1/access-control` | Open a pull request changing `access.yaml` |
| `GET /api/runner-provision?runner_name=` | Boot script for `provision_via: pull_from_api` |

The full reference is under **Developer API** at `/docs` on the API.
