# Runner config files

Profiles in the dashboard map one label to one machine. Config files let a
repository define its runners in code instead: several machine shapes to
fall back through, job environment variables, and warm pools that keep
machines booted ahead of jobs. The format follows
[Cirun's](https://docs.cirun.io/reference/yaml) closely, so moving from one
to the other is mostly renaming files and labels.

```yaml
# .airship.yml, at the root of the repository
runners:
  - name: build
    cloud: aws
    instance_type: [c7i.xlarge, m7i.xlarge, c7a.xlarge] # tried in order
    machine_image: resolve:ssm:/aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id
    region: us-east-1
    preemptible: [true, true, false] # zipped with instance_type
    labels: [airship-build]
    extra_config:
      BlockDeviceMappings:
        - VolumeSize: 100
          VolumeType: gp3
          Throughput: 250
      env_vars:
        CARGO_INCREMENTAL: "0"
    idle:
      replicas: 2
```

```yaml
# .github/workflows/ci.yml
jobs:
  test:
    runs-on: airship-build--${{ github.run_id }}
```

## Turning it on

1. Give the Airship GitHub App access to the repository.
2. On **Runner config** in the dashboard, an admin activates it. A config
   file never launches anything in a repository nobody activated, so
   committing one cannot spend your cloud budget by itself.
3. Commit `.airship.yml` and use one of its labels in `runs-on`.

Only labels starting with `airship-` are looked up in config files. Jobs on
other labels (`ubuntu-latest`, your own runners) cost no GitHub API calls.

## One-line runners

In an activated repository, a label can name the machine itself, with no
config file:

```yaml
runs-on: airship-aws-c7i-xlarge--${{ github.run_id }}   # c7i.xlarge
runs-on: airship-aws-t4g-large-ubuntu2204-spot           # Graviton, Ubuntu 22.04, spot
runs-on: airship-aws-m7i-metal-24xl                      # m7i.metal-24xl
runs-on: airship-onprem-4vcpu-8gb                        # Docker data plane
```

AWS labels are `airship-aws-` and the instance type with `.` written as `-`, then
optionally `ubuntu2204` or `ubuntu2404` (Canonical's current image for the
type's architecture; default: the agent's image, Ubuntu 24.04) and `spot`,
in either order. They launch on any connected AWS data plane, with a 75 GiB
root volume. A definition in a config file whose labels include the same
label always wins.

Pull requests from forks cannot use one-line labels: a fork can edit
`runs-on`, so it would choose its own machine. Their jobs need a label from
the default branch's config file.

## Which file a job reads

A job reads `.airship.yml` **at its own commit**, so a branch can try a new
machine type before it is merged. There are two exceptions:

- **Pull requests from forks** read the file on the default branch. Code
  from outside the repository never chooses the machines it runs on or the
  environment it gets.
- **Warm pools** are read from the default branch only, since they exist
  before any job does. Pushes to the default branch update them within a
  reconcile pass.

A job is planned once, when GitHub first reports it queued. Editing the file
afterwards does not change how a waiting job is served.

## Fields

| Field | Required | Notes |
| --- | --- | --- |
| `name` | yes | Unique in the file. Letters, digits, `.`, `_`, `-`. |
| `cloud` | yes | `aws`, or `on_prem` for the Docker provider. |
| `instance_type` | yes | EC2 type, or `<n>vcpu-<n>gb` for Docker (`4vcpu-8gb`). |
| `machine_image` | no | AMI ID or `resolve:ssm:<parameter>` on AWS; container image on Docker. Default: the agent's. |
| `region` | no | Launch only on data planes whose agent reports this region. |
| `preemptible` | no | Spot capacity. Falls back to on-demand when spot is short (agent default). |
| `labels` | yes | `runs-on` labels this runner serves. Each must start with `airship-`. |
| `data_plane` | no | Airship extension: launch only on the data plane with this name. |
| `extra_config.BlockDeviceMappings` | no | One entry for the root volume: `VolumeSize` (8-16384 GiB), `VolumeType` (gp3, gp2, io1, io2; default gp3), `Iops`, `Throughput` (gp3 only). `DeviceName` is accepted and ignored: Airship always sizes the image's root device. Volumes are always encrypted. |
| `extra_config.env_vars` | no | Up to 50 variables for every job. Not secret: they are in your repository. Names starting with `GITHUB_`, `RUNNER_` or `ACTIONS_` are refused. |
| `extra_config.executor: docker` + `privileged: true` | no | `on_prem` only: run the container privileged for docker-in-docker. The agent must also set `AIRSHIP_DOCKER_ALLOW_PRIVILEGED=true`, because a privileged container is root on the host. |
| `idle` | no | Warm pool; see below. |

The architecture comes from the instance type: Graviton families (`c7g`,
`m8g`, `t4g`, ...) are `arm64`, everything else `x64`. Runners register with
`self-hosted`, `linux`, the architecture and their labels, so `runs-on:
[self-hosted, arm64, airship-build]` works as you would expect, and a job
asking for `arm64` skips any x64 fallback.

### Fallbacks

`instance_type`, `machine_image`, `region` and `preemptible` each take a
value or a list. Lists are zipped: the first attempt uses the first entry of
every list, and a single value applies to every attempt. Lists of different
lengths are an error.

When a launch fails, the next attempt is tried. Two runners that share a
label are also one fallback chain, in file order. If several connected data
planes match an attempt's cloud and region, each one is a further fallback.
After the last attempt, a failure the agent marks retryable (throttling, a
spot shortage) starts over from the first, until the job has had three
failed launches or one per attempt, whichever is more. Other failures (a
missing image, a type the account cannot launch) stop once every attempt
has been tried.

### Suffixes

`runs-on: airship-build--${{ github.run_id }}` makes the runner register
with the suffixed label as well, so GitHub can only hand it a job from that
run. Everything from `--` on is ignored when matching the file. Do not use
a suffix with a warm pool: pool runners exist before the run, so they cannot
carry its ID.

## Warm pools

`idle:` keeps runners booted and registered, so a job starts in seconds
instead of waiting for a machine. A pool runner that takes a job is replaced
straight away; it is still one machine per job. Exactly one form:

```yaml
idle:
  replicas: 2            # always
```

```yaml
idle:
  schedule:              # working hours; overlapping windows take the most
    - day: mon-fri
      time: 9-17         # from 9:00 until 17:00
      timezone: Europe/Berlin
      replicas: 4
    - day: sat-sun
      time: 0-24
      replicas: 1
```

```yaml
idle:
  usage_based:           # stay warm for a while after jobs arrive
    trigger: runner_allocated
    replicas: 2
    duration: 30         # minutes after the last job started
```

A pool whose runners keep failing to launch pauses for ten minutes after
three failures. Pools count toward the data plane's runner limit, so a pool
larger than the limit leaves no room for on-demand runners.

## Organization runners

To share definitions, create a repository named `.airship` in the
organization, give the app access to it, and add `.airship.global.yml`
(same format, without `idle`). Any activated repository can then use its
labels. Repository definitions are tried before organization ones.

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
  - resource: gpu-runner    # a runner name from .airship.global.yml
    policies: [ml-team]
```

A runner with no `access_control` entry is open to every activated
repository. A restricted runner can only be launched for a repository named
in one of its policies, by a person the policy lists (by login, team or
role); a policy that lists nobody admits everyone on that repository. The
person is whoever started the run, or re-ran it. If `.access.yml` does not
parse, every organization runner is closed until it is fixed.

## When a job is not run

The **Jobs** page says why, with the detail from the file:

| Reason | Meaning |
| --- | --- |
| Not activated | The repository has not been activated on **Runner config**. |
| Config problem | `.airship.yml` does not parse; the detail names the field. |
| No runner serves the labels | No definition covers every `airship-` label of the job, or none can satisfy its other labels. |
| Access denied | `.access.yml` does not let this person, repository or event use the runner. |
| No data plane | No connected data plane matches the runner's cloud, region or `data_plane`. |
| Public repository | The repository is public and was not activated with public access allowed. |

**Runner config** also has a checker that validates a file before you commit
it.

## Developer API

Scripts can list, activate and deactivate repositories with an API token,
which an organization owner creates under **Runner config → API tokens**.
A token belongs to one organization and is shown once.

```sh
curl -H "Authorization: Bearer $AIRSHIP_TOKEN" https://api.airship.example/v1/api/repositories
curl -X PUT -H "Authorization: Bearer $AIRSHIP_TOKEN" -H "Content-Type: application/json" \
  -d '{"allowPublic": false}' https://api.airship.example/v1/api/repositories/my-repo/activation
curl -X DELETE -H "Authorization: Bearer $AIRSHIP_TOKEN" \
  https://api.airship.example/v1/api/repositories/my-repo/activation
```

Repositories are named without the owner. The API accepts only the bearer
token, never a dashboard session.

Cirun's routes are served too (`GET /api/v1/repo`, and `POST /api/v1/repo`
with `{"repository": "owner/name", "active": true}`), so Cirun's own client
works with an Airship token:

```sh
CIRUN_API_ENDPOINT=https://api.airship.example/api/v1 CIRUN_API_KEY=asa_... \
  cirun repo add acme/api
```

Cirun's `access-control` endpoints, which open a pull request against the
`.cirun` repository, are not served: Airship's app only reads repository
contents. Edit `.access.yml` in the `.airship` repository instead. Revoking a token takes effect on the next
request. The full reference is under **Developer API** at `/docs` on the
API.

## Caching

Set **CacheBucket** to `Enabled` in the AWS data plane stack. It creates
an encrypted, private S3 bucket whose entries expire after
**CacheRetentionDays** (default 7), and gives runners access to it through
their instance profile: a role this stack creates, or the
**RunnerRoleArn** you supplied. Runners need no keys or secrets.

Put the stack's `CacheBucketName` output in an organization variable named
`AIRSHIP_CACHE_BUCKET`, then use Cirun's S3 cache action (any action that
uses the AWS SDK's default credentials works):

```yaml
- uses: cirunlabs/cache/s3@0d5005be5be8cb3f20e624125e3fca0dc68eb384  # v5.0.5-cirun.1
  with:
    path: ~/.cache/go-build
    key: ${{ runner.os }}-${{ runner.arch }}-go-${{ hashFiles('**/go.sum') }}
    s3-bucket: ${{ vars.AIRSHIP_CACHE_BUCKET }}
    s3-region: us-east-1        # the data plane's region
    s3-prefix: ${{ github.repository }}
    s3-ttl-days: 0              # the stack manages expiry
```

Things to know:

- Every job on the data plane can read and write the whole bucket. Unlike
  GitHub's cache there is no per-branch scope, so a job that runs untrusted
  code (a fork's pull request in a public repository) could plant entries a
  main-branch build restores. Keep public repositories on a data plane
  without a cache, or use `s3-prefix` values those jobs cannot guess.
- Runners require IMDSv2 with one network hop, so steps in a `container:`
  job cannot reach the instance credentials. Run cache steps on the host.
- On Docker data planes, use the same action with an S3-compatible bucket
  (R2, MinIO) and keys from repository secrets, as Cirun documents.

## Differences from Cirun

The same as Cirun: per-repository activation, the file format and its
fallback and suffix rules, idle runners per repository, organization
runners with `.access.yml`, one-line labels, the repository API, and S3
caching through `cirunlabs/cache/s3`.

Different, on purpose:

- **Clouds.** AWS and on-prem Docker. `gcp`, `azure`, `openstack`,
  `oracle`, `vast_ai` and `gpu` are refused with an error rather than
  ignored. Each new cloud is a provider in the agent with its own launch,
  tagging, inventory and IAM-scoping code, and none of it can be trusted
  without being run against that cloud. GPU instances on AWS (`g6.xlarge`,
  `p5.48xlarge`) work today: they are ordinary instance types with an image
  that has the drivers.
- **One-line labels and forks.** Fork pull requests cannot use them (see
  above).
- **`provision_via: pull_from_api`.** Not supported. Cirun needs it because
  it calls your cloud from outside and some images cannot take user data;
  the instance then fetches its registration using an API token baked into
  the image. Anyone holding that token can fetch runner registrations,
  which receive your organization's jobs and secrets. The Airship agent
  runs in your account and always passes user data to EC2 itself, so every
  image with cloud-init works without it.
- **Automatic caching** (`extra_config.cache: true`, which redirects
  `actions/cache` to S3 through a proxy on the runner). Not supported: it
  means running an unofficial implementation of GitHub's internal cache
  protocol on every runner, which GitHub can change without notice. The
  S3 action above gives the same storage with one line changed per cache
  step.
- **Developer API.** Repository activation works with Cirun's client;
  `access-control` does not, since it needs write access to repositories.
- **`data_plane`** pins a runner to one of your data planes.
- **Windows and macOS** runners are not supported.
