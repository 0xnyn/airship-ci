# `.cirun.yml` reference

Runners are defined in `.cirun.yml` at the root of each activated
repository. The format is Cirun's; the fields below are the ones Airship
acts on for AWS. Anything else is an error that names the field.

```yaml
runners:
  - name: build
    cloud: aws
    instance_type: [c7i.xlarge, m7i.xlarge, c7a.xlarge]   # tried in order
    machine_image: ami-0123456789abcdef0                   # or ubuntu2204 / ubuntu2404
    region: us-east-1
    preemptible: [true, true, false]                       # zipped with instance_type
    labels: [cirun-build]
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
# .github/workflows/ci.yml
jobs:
  test:
    runs-on: cirun-build--${{ github.run_id }}
```

## Turning it on

1. Install the GitHub App on the repository.
2. Connect AWS under **Cloud**.
3. Activate the repository under **Repositories**. A config file never
   launches anything in a repository nobody activated.
4. Commit `.cirun.yml` and use one of its labels in `runs-on`.

Only labels starting with `cirun-` are looked up. Jobs on other labels cost
no API calls.

## Fields

| Field | Required | Notes |
| --- | --- | --- |
| `name` | yes | Unique in the file. Letters, digits, `.`, `_`, `-`. |
| `cloud` | yes | `aws`. Cirun's other clouds are refused with "not supported yet". |
| `instance_type` | yes | EC2 instance type, or a list. |
| `machine_image` | no | AMI ID, `ubuntu2204` or `ubuntu2404` (Canonical's newest for the architecture), or a list. Default `ubuntu2204`. A Windows AMI gets a Windows boot script. |
| `region` | no | AWS region, or a list. Default: the connected cloud's default region. |
| `preemptible` | no | Spot capacity, or a list. Default `false`. |
| `labels` | yes | `runs-on` labels this runner serves. Each must start with `cirun-`. |
| `gpu` | no | Any GPU name. Installs the NVIDIA driver on Ubuntu images that lack one and adds the `gpu` label. Not needed for NVIDIA's AMIs. `false`, `no` or `null` turns it off. |
| `extra_config` | no | See below. |
| `idle` | no | Idle runners; see below. |

The architecture comes from the instance type: Graviton families (`c7g`,
`m8g`, `t4g`, ...) are `arm64`, everything else `x64`. Runners register
with `self-hosted`, the architecture, `aws`, `cpu` or `gpu`, and their
labels. The runner adds its own OS label.

### `extra_config`

| Key | Notes |
| --- | --- |
| `BlockDeviceMappings` | One entry for the root volume. `VolumeSize` (8-16384 GiB, default 75), `VolumeType` (gp2, gp3, io1, io2; default gp2, as Cirun), `Iops` (gp3: 3000-16000; io1 and io2: required; not for gp2), `Throughput` (gp3 only, 125-1000 MiB/s, at most a quarter MiB/s per IOPS, so `Throughput: 1000` needs `Iops: 4000`), `VolumeInitializationRate` (100-300 MiB/s, snapshot-backed AMIs). Combinations EC2 would refuse are config errors rather than failed launches. `DeviceName` is accepted and ignored: the AMI's root device is always the one sized. Volumes are always encrypted. |
| `env_vars` | Up to 50 variables for every job. Not secret. Names starting with `GITHUB_`, `RUNNER_` or `ACTIONS_` are refused. |
| `cache` | `true` runs the cache proxy on the machine; see [caching.md](caching.md). |
| `provision_via` | `pull_from_api`: the instance gets no user data; your image fetches the boot script from `GET /api/runner-provision?runner_name=<Name tag>` with an API token, once. |
| `run_as` | `interactive`: on Windows, a local account logs in automatically and runs the runner in its desktop session. |
| `runner_user` | Windows account for `run_as: interactive` (default `runner`). |
| `runner_path` | Windows install path (default `C:\actions-runner`). |
| `SubnetId` | Launch in this subnet instead of the default VPC's. |
| `SecurityGroupIds` | Up to 5 groups instead of the `airship-runners` group. |
| `IamInstanceProfile` | Name or ARN. Every job on the runner can use it. |

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

`runs-on: cirun-build--${{ github.run_id }}` makes the runner register
with the suffixed label instead of the plain one, so GitHub can only hand
it a job from that run. Everything from `--` on is ignored when matching
the file. Matrix jobs can use
`cirun-build--${{ matrix.version }}-${{ github.run_id }}`. Do not use a
suffix with idle runners: they exist before the run, so they cannot carry
its ID.

## One-line runners

In an activated repository, a label can name the machine itself, with no
config file:

```yaml
runs-on: cirun-aws-t2-large-ubuntu2204--${{ github.run_id }}   # t2.large, Ubuntu 22.04
runs-on: cirun-aws-c7g-xlarge-spot                             # Graviton, spot
runs-on: cirun-aws-m7i-metal-24xl                              # m7i.metal-24xl
```

The label is `cirun-aws-` and the instance type with `.` written as `-`,
then optionally `ubuntu2204` (the default) or `ubuntu2404` and `spot`, in
either order. The family must look like one of EC2's (`c7i`, `c7i-flex`,
`u7i-12tb`, `mac2-m2`); a label that puts `spot` or the image token
elsewhere is not a one-line runner. They launch in the connected cloud's
default region with a 75 GiB root volume. A definition in a config file
whose labels include the same label always wins. A job that also asks for
`windows` is only served by a definition with a custom AMI: the Ubuntu
images are Linux.

## Which file a job reads

A job reads `.cirun.yml` at its own commit, so a branch (or a pull
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

Idle runners in `.cirun.global.yml` are organization-wide: they register
on the organization, in a runner group named `airship-<runner name>` that
is visible to every activated private repository, or only to the
repositories the runner's `.access.yml` policies name. Any such repository
can take one; the GitHub App needs the organization's **Self-hosted
runners** write permission for this.

## Organization runners

Create a repository named `.cirun` in the organization, give the app
access to it, and add `.cirun.global.yml` (same format, `idle` included).
Any activated repository can then use its labels. Repository definitions
are tried before organization ones.

Restrict who can use them with `.access.yml` in the same repository:

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
  - resource: gpu-runner    # a runner name from .cirun.global.yml
    policies: [ml-team]
```

A runner with no `access_control` entry is open to every activated
repository. A restricted runner can only be launched for a repository
named in one of its policies, by a person the policy lists (by login, team
or role); a policy that lists nobody admits everyone on that repository.
The person is whoever started the run, or re-ran it. If `.access.yml` does
not parse, every organization runner is closed until it is fixed.

The developer API edits this file for you, through a pull request:
`cirun access-control add-repo-to-resources`.

## When a job is not run

The **Jobs** page says why:

| Reason | Meaning |
| --- | --- |
| Not activated | The repository has not been activated under **Repositories**. Activating it plans the jobs still queued. |
| Config problem | `.cirun.yml` does not parse; the detail names the field. |
| No runner serves the labels | No definition covers every `cirun-` label of the job, or none can satisfy its other labels. |
| Access denied | `.access.yml` does not let this person, repository or event use the runner. |
| No cloud | The runner's cloud is not connected. Connecting one plans the jobs still queued. |
| Public repository | The repository is public and was not activated with public access allowed. Activating it that way plans the jobs still queued. |

**Repositories** also has a checker that validates a file before you commit
it.

## Developer API

Tokens are created under **API keys**. [cirun-py](https://github.com/AktechLabs/cirun-py)
works unchanged:

```sh
export CIRUN_API_ENDPOINT=https://api.example.com/api/v1
export CIRUN_API_KEY=asa_...
cirun repo add acme/api
cirun cloud connect aws --access-key AKIA... --secret-key ...
cirun access-control add-repo-to-resources --org acme --repo api --resources gpu-runner --teams ml
```

| Route | Does |
| --- | --- |
| `GET /api/v1/repo` | List repositories |
| `POST /api/v1/repo` `{"repository": "owner/name", "active": true}` | Activate or deactivate |
| `GET /api/v1/cloud-connect` | List connected clouds |
| `POST /api/v1/cloud-connect` `{"cloud": "aws", "credentials": {"access_key", "secret_key", "region"}}` | Connect |
| `GET /api/v1/access-control` | Read `.access.yml` |
| `PUT /api/v1/access-control` | Open a pull request changing `.access.yml` |
| `GET /api/runner-provision?runner_name=` | Boot script for `provision_via: pull_from_api` |

The full reference is under **Developer API** at `/docs` on the API.
