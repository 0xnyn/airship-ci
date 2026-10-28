# Setting up Airship on AWS

**What this is.** The path from an empty AWS account to a green CI job on
your own hardware, with the decisions called out where they matter and the
exact commands where they help.

**Audience.** Whoever owns the AWS account and whoever owns the GitHub
organization — often two people, so each step says which hat it needs.

**Time.** 20–30 minutes of real work, most of it waiting for a first job.

---

## Before you start

| You need | Notes |
| --- | --- |
| An AWS account, or a dedicated one | A **separate account for CI** is the recommendation. It makes the IAM policy uninteresting, the bill legible, and the blast radius a boundary rather than an argument |
| Admin on the GitHub organization | To install the app and activate repositories |
| A region | One to start. Airship launches per-definition, so you can add more in a line of YAML later. Cirun's default is `eu-west-1` |
| A concurrency number | What is the most runners you want alive at once? This becomes `max_runners`. Start low — 10 — and raise it |
| A control plane | Airship-hosted, or your own container plus Postgres ([operations.md](operations.md)) |

Nothing needs to be installed in your account. There is no CloudFormation
stack, no agent, no VPC peering and no bootstrap script. The whole
integration is one IAM access key.

---

## Step 1 — Create the IAM user *(AWS admin)*

Airship authenticates as an IAM user with a long-lived access key, which it
verifies with STS, seals with AES-256-GCM and opens only for the duration of
a single API call. Pick one of two policies.

### Option A — the quick start

Attach **`AmazonEC2FullAccess`**, plus the cache statements below if you
want caching. This is what Cirun documents and it gets you running in a
minute. In a dedicated CI account it is a perfectly defensible end state.

### Option B — scoped to exactly what Airship calls *(recommended for a shared account)*

This mirrors the provider's API surface one statement at a time. The
`TerminateInstances` condition is the interesting one: it means the
credential **cannot destroy an instance that Airship did not tag**, which is
what makes sharing an account with your own workloads safe.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "Describe",
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeInstances",
        "ec2:DescribeImages",
        "ec2:DescribeSubnets",
        "ec2:DescribeSecurityGroups",
        "sts:GetCallerIdentity"
      ],
      "Resource": "*"
    },
    {
      "Sid": "LaunchRunners",
      "Effect": "Allow",
      "Action": ["ec2:RunInstances"],
      "Resource": "*"
    },
    {
      "Sid": "TagOnCreate",
      "Effect": "Allow",
      "Action": ["ec2:CreateTags"],
      "Resource": "*",
      "Condition": {
        "StringEquals": {
          "ec2:CreateAction": ["RunInstances", "CreateSecurityGroup"]
        }
      }
    },
    {
      "Sid": "RunnerSecurityGroup",
      "Effect": "Allow",
      "Action": ["ec2:CreateSecurityGroup"],
      "Resource": "*"
    },
    {
      "Sid": "TerminateOnlyOurOwn",
      "Effect": "Allow",
      "Action": ["ec2:TerminateInstances"],
      "Resource": "*",
      "Condition": {
        "StringLike": { "ec2:ResourceTag/airship:installation": "*" }
      }
    }
  ]
}
```

Add these two statements **only if you want cache acceleration** (see
[`../caching.md`](../caching.md) for what they buy):

```json
    {
      "Sid": "CacheBuckets",
      "Effect": "Allow",
      "Action": [
        "s3:CreateBucket",
        "s3:ListBucket",
        "s3:GetBucketLocation",
        "s3:PutBucketPublicAccessBlock",
        "s3:PutLifecycleConfiguration"
      ],
      "Resource": "arn:aws:s3:::airship-cache-*"
    },
    {
      "Sid": "CacheObjectsAndGrants",
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:PutObject",
        "s3:AbortMultipartUpload",
        "s3:ListMultipartUploadParts"
      ],
      "Resource": "arn:aws:s3:::airship-cache-*/*"
    },
    {
      "Sid": "RunnerCacheCredentials",
      "Effect": "Allow",
      "Action": ["sts:GetFederationToken"],
      "Resource": "*"
    }
```

The object permissions above are the **ceiling** on what any runner can do
with the cache: a federation token can never exceed the issuing user's own
permissions, and the proxy's inline policy narrows it further to one bucket.

Add `iam:PassRole` scoped to the specific role **only** if you intend to use
`extra_config.IamInstanceProfile`:

```json
    {
      "Sid": "PassRunnerRole",
      "Effect": "Allow",
      "Action": ["iam:PassRole"],
      "Resource": "arn:aws:iam::<account>:role/<your-runner-role>",
      "Condition": { "StringEquals": { "iam:PassedToService": "ec2.amazonaws.com" } }
    }
```

### Create it

```bash
aws iam create-user --user-name airship-runners
aws iam put-user-policy --user-name airship-runners \
  --policy-name airship --policy-document file://airship-policy.json
aws iam create-access-key --user-name airship-runners
```

Keep the output. You paste it once; Airship never shows it again.

> **If your account sets a default EBS encryption KMS key of its own** (not
> the AWS-managed `aws/ebs` key), the user also needs `kms:CreateGrant`,
> `kms:Decrypt` and `kms:GenerateDataKeyWithoutPlaintext` on that key.
> Volumes are always encrypted, so this is not optional.

---

## Step 2 — Check the network *(AWS admin)*

The default path needs a **default VPC** in each region you will use.
Airship launches into every default subnet, rotating availability zones per
runner so one AZ running out of capacity does not fail the job.

```bash
aws ec2 describe-vpcs --region eu-west-1 \
  --filters Name=isDefault,Values=true --query 'Vpcs[].VpcId' --output text
```

Empty output? Either create one —

```bash
aws ec2 create-default-vpc --region eu-west-1
```

— or point Airship at your own subnet per runner definition:

```yaml
extra_config:
  SubnetId: subnet-0123456789abcdef0
  SecurityGroupIds: [sg-0123456789abcdef0]   # optional; default is an egress-only group Airship makes
```

**A private subnet works** and needs no inbound anything, but it does need a
route out — a NAT gateway or equivalent — because the runner downloads
`actions/runner`, talks to GitHub, and (with caching on) fetches the proxy
from your control plane. The full destination list is in
[aws.md](aws.md#networking). Two things pay for themselves there: a **gateway
VPC endpoint for S3** if you enable caching, and a custom AMI with the
runner and your toolchain baked in, which removes the apt traffic entirely.

Airship opens **no inbound ports, ever.** The security group it creates has
no ingress rules.

---

## Step 3 — Raise the quotas that will bite *(AWS admin)*

The limit on concurrent runners is almost always a vCPU quota, not Airship.

```bash
# Running On-Demand Standard (A, C, D, H, I, M, R, T, Z) vCPUs
aws service-quotas get-service-quota --service-code ec2 \
  --quota-code L-1216C47A --region eu-west-1 \
  --query 'Quota.[QuotaName,Value]' --output text

# All Standard Spot Instance Requests (vCPUs)
aws service-quotas get-service-quota --service-code ec2 \
  --quota-code L-34B43A08 --region eu-west-1 \
  --query 'Quota.[QuotaName,Value]' --output text

# Anything else by name, e.g. GPU families
aws service-quotas list-service-quotas --service-code ec2 --region eu-west-1 \
  --query "Quotas[?contains(QuotaName,'On-Demand G')].[QuotaCode,QuotaName,Value]" --output table
```

Do the arithmetic once: *(vCPUs per runner) × (max concurrent runners)*. A
`c7i.4xlarge` is 16 vCPUs, so 10 concurrent runners need a quota of 160. The
default on a fresh account is often 32 — two runners.

Also worth a look: EBS storage quotas (gp3/gp2 TiB per region), and GPU
quotas, which are usually zero until you ask.

Set Airship's `max_runners` **below** what your quotas allow. Then hitting
the ceiling is a clear message on the dashboard rather than a wall of EC2
failures.

---

## Step 4 — Install the GitHub App *(GitHub org admin)*

Install the Airship app on your organization and grant it the repositories
you plan to use (you can start with one). The permissions it asks for, and
why:

| Permission | Why |
| --- | --- |
| Repository → Administration: read & write | Register and remove repository runners |
| Repository → Actions: read | Look up jobs and runs |
| Repository → Contents: read | Read `airship.yaml` at a job's commit |
| Repository → Metadata: read | Required by GitHub |
| Organization → Members: read | Tell admins from members; team rules in `access.yaml` |
| Organization → Self-hosted runners: read & write | Organization-wide idle runners in `airship.global.yaml` |
| *(optional)* Contents: write, Pull requests: write | Only so the access-control API can open PRs against the `.airship` repository |

If you are running your own control plane, you create the app yourself —
callback URL, webhook URL, secret and event subscriptions are listed in
[operations.md](operations.md) and in the repository README.

---

## Step 5 — Connect AWS *(GitHub org admin)*

**Dashboard:** open **Cloud → AWS**, paste the access key ID and secret, and
choose the default region (where runners launch when a definition names
none).

**Or the developer API,** wire-compatible with
[cirun-py](https://github.com/AktechLabs/cirun-py):

```bash
export CIRUN_API_ENDPOINT=https://api.example.com/api/v1
export CIRUN_API_KEY=asa_...          # created under API keys in the dashboard
cirun cloud connect aws --access-key AKIA... --secret-key ...
```

What happens on submit: the credential is verified with
`sts:GetCallerIdentity`, and only then sealed and stored. You get the account
ID and the principal ARN echoed back so you can confirm it is the user you
meant. The secret is never returned by any endpoint again.

One credential per cloud per installation, as on Cirun. Rotating the key
*within the same account* is allowed any time. Swapping it for a **different
account's** key, or disconnecting entirely, is refused while runners launched
with it are still alive — without that key their instances could not be
terminated.

---

## Step 6 — Activate repositories *(GitHub org admin)*

**Repositories →** toggle each one on. This is deliberately a separate,
explicit step: an `airship.yaml` in a repository nobody activated launches
nothing, so a committed config file can never start spending money on its
own.

Public repositories need a **second** choice, "allow public access", because
their fork pull requests run untrusted code on your machines. That is the
gate; `access.yaml` and `max_runners` are the bounds.

```bash
cirun repo add acme/api        # same thing from the CLI
```

---

## Step 7 — Commit a config and a workflow *(any developer)*

`airship.yaml` at the repository root:

```yaml
runners:
  - name: build
    cloud: aws
    instance_type: [c7i.2xlarge, c7i.2xlarge, m7i.2xlarge]
    preemptible:   [true,        false,       false]
    region: eu-west-1
    labels: [airship-build]
    extra_config:
      BlockDeviceMappings:
        - VolumeSize: 100
          VolumeType: gp3
          Iops: 6000
          Throughput: 1000
      cache: true
```

That definition says: try a spot `c7i.2xlarge`, fall back to on-demand, then
to a different family; 100 GiB of fast gp3; S3-backed cache acceleration.

`.github/workflows/ci.yml`:

```yaml
jobs:
  test:
    runs-on: airship-build--${{ github.run_id }}
    steps:
      - uses: actions/checkout@v4
      - run: nproc && free -g && df -h /
```

The `--${{ github.run_id }}` suffix makes the runner register under a label
unique to that workflow run, so GitHub can only hand it a job from that run.
Use it everywhere **except** with idle pools, which exist before a run does
and so cannot carry its ID.

**Before you commit,** the dashboard's **Repositories** page has a config
checker that validates a file and shows the machines it would plan —
cheaper than a push.

Only labels starting with `airship-` are looked up at all. Jobs on
`ubuntu-latest` cost nothing and are untouched, so you can migrate one
workflow at a time.

---

## Step 8 — Verify *(both)*

Push, then watch in this order:

| Where | What you should see |
| --- | --- |
| Airship → **Jobs** | The job appears as queued within a second or two of GitHub's webhook, with the definition and machine shape it planned |
| Airship → **Runners** | A runner moving `pending → launching → running → busy`, with its instance ID |
| EC2 console, filter tag `airship:installation` | One instance, `Name` = the runner's name, your tags on it and its volume, no inbound security group rules |
| GitHub → the job log | Steps running on a self-hosted runner, with labels `self-hosted`, `x64`, `aws`, `cpu`, `airship-build--…` |
| GitHub → Settings → Actions → Runners | The runner present during the job, gone a couple of minutes after |
| EC2 console again | The instance terminated within seconds of the job finishing |
| Airship → **Events** | The whole story, including anything that silently degraded — a cache that was unavailable, for instance |

If the second job in the same repository restores a cache the first one
saved, caching is working. The job log will show `actions/cache` hitting
`127.0.0.1` rather than GitHub.

**A deliberate first-week test:** cancel a job while it is queued, and
cancel another mid-run. Then check that no instance survives either. That is
the behaviour everything else rests on.

---

## Going to production

| Step | Why |
| --- | --- |
| Set `max_runners` for real | Under your vCPU quota. It is the one number that bounds total spend |
| Bake a custom AMI | The single biggest cold-start win: runner tarball at `/opt/actions-runner` with a matching `.version`, plus your toolchain and Docker images. The boot script then skips the download and the apt install |
| Add idle pools where latency matters | `idle: {replicas: 2}` for the repository everyone waits on, or `schedule:` windows matching your working hours. Pools count toward `max_runners` |
| Move shared definitions to the organization | A `.airship` repository with `airship.global.yaml`, so teams stop copying machine shapes between repositories |
| Restrict the expensive ones | `access.yaml` policies on GPU and large-instance runners, by team, repository role or login |
| Turn on caching deliberately | Read the scope caveat in [aws.md](aws.md#caching) first: one bucket per organization, no per-branch isolation |
| Keep public repositories off shared runners | Or activate them with public access knowingly, and keep them away from cache runners and instance profiles |
| Watch the bill by tag | Cost Explorer grouped by `airship:repository` needs no extra instrumentation |
| Alarm on CloudTrail | `RunInstances`, `TerminateInstances` and `GetFederationToken` by the Airship user — a cheap tripwire |

---

## Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| Job stays queued, Jobs page says **Not activated** | The repository was never activated | Activate it. Jobs still queued are planned again automatically |
| **No cloud** | No AWS credential on the installation | Connect one. Jobs skipped for this reason are re-planned on connect |
| **No runner serves the labels** | No definition covers every `airship-` label of the job | Check the label spelling and that the definition is in the file *at the job's commit* |
| **Config problem** | `airship.yaml` does not parse | The detail names the field. Use the dashboard's checker |
| **Public repository** | Public repo not activated for public access | Activate it that way, knowing fork PRs then run on your machines |
| **Access denied** | `access.yaml` does not admit this person, repository or event | Check the policy; a broken `access.yaml` closes *every* organization runner until fixed |
| Launch fails: *this region has no default VPC* | Exactly that | `aws ec2 create-default-vpc`, or set `SubnetId` |
| Launch fails: `VcpuLimitExceeded`, `InstanceLimitExceeded` | Service quota | Raise the quota; lower `max_runners` meanwhile |
| Launch fails: `InsufficientInstanceCapacity` repeatedly | The AZ or the whole region is short of that shape | Airship already rotates AZs and falls back; add shapes to `instance_type`, or a second `region` |
| Launch fails: `UnauthorizedOperation` | Missing IAM action | The message names the action. Compare against Step 1 |
| Launch fails: `AuthFailure`, `InvalidClientTokenId` | The key was deleted or deactivated | Reconnect. The credential shows as invalid on the Cloud page |
| Instance launches, runner never appears in GitHub | The machine cannot reach GitHub, or the AMI is missing something | Private subnet with no route out, or a custom AMI without `curl`/`tar`. Check the instance's console output |
| Runner appears, job never starts | Label mismatch between the registration and `runs-on` | Compare the labels on the Runners page with the job's |
| Jobs run but caching does nothing | Expected on GCP, Azure and Daytona; on AWS check Events | Events records `cache.unavailable` with the reason — usually a missing S3 permission or a control plane built without the proxy binary |
| Runner finished `idle` | It booted and no job arrived within 10 minutes | Usually a suffix mismatch, or the job was cancelled while the machine booted |
| Runner finished `launch_unconfirmed` | The cloud's answer was lost | Expected and handled: the job is retried and the scan reconciles the machine. Repeated occurrences mean a network problem between the control plane and EC2 |
| An instance you did not expect | Check its tags | Untagged instances are not Airship's. Tagged ones with no live runner are terminated by the next inventory pass |

The **Events** page is the first place to look for all of these: it records
every launch failure, every degraded cache, every config error, with the
cloud's own message.

---

## Turning it off

| To | Do |
| --- | --- |
| Stop one repository | Deactivate it. Its running jobs finish; nothing new launches |
| Stop everything, keep the setup | Set `max_runners` to 1, or deactivate all repositories |
| Disconnect AWS | Only possible once no runners launched with that credential are alive — otherwise their instances could not be terminated. Wait for them to finish, then disconnect |
| Remove Airship entirely | Uninstall the GitHub App. Every runner is finished with reason `deleted` and its instance terminated. Then delete the IAM user, and by hand: the `airship-runners` security group in each VPC, and the `airship-cache-*` buckets (whose objects expire after 7 days anyway) |

---

## Appendix — the whole AWS side in one go

```bash
#!/usr/bin/env bash
set -euo pipefail
REGION=eu-west-1
USER=airship-runners

cat > /tmp/airship-policy.json <<'JSON'
{ "Version": "2012-10-17", "Statement": [
  { "Sid": "Describe", "Effect": "Allow", "Action": [
      "ec2:DescribeInstances","ec2:DescribeImages","ec2:DescribeSubnets",
      "ec2:DescribeSecurityGroups","sts:GetCallerIdentity"],
    "Resource": "*" },
  { "Sid": "LaunchRunners", "Effect": "Allow",
    "Action": ["ec2:RunInstances"], "Resource": "*" },
  { "Sid": "TagOnCreate", "Effect": "Allow", "Action": ["ec2:CreateTags"],
    "Resource": "*", "Condition": { "StringEquals": {
      "ec2:CreateAction": ["RunInstances","CreateSecurityGroup"] } } },
  { "Sid": "RunnerSecurityGroup", "Effect": "Allow",
    "Action": ["ec2:CreateSecurityGroup"], "Resource": "*" },
  { "Sid": "TerminateOnlyOurOwn", "Effect": "Allow",
    "Action": ["ec2:TerminateInstances"], "Resource": "*",
    "Condition": { "StringLike": {
      "ec2:ResourceTag/airship:installation": "*" } } }
] }
JSON

aws iam create-user --user-name "$USER"
aws iam put-user-policy --user-name "$USER" \
  --policy-name airship --policy-document file:///tmp/airship-policy.json

aws ec2 describe-vpcs --region "$REGION" --filters Name=isDefault,Values=true \
  --query 'Vpcs[].VpcId' --output text | grep -q vpc- \
  || aws ec2 create-default-vpc --region "$REGION"

aws service-quotas get-service-quota --service-code ec2 \
  --quota-code L-1216C47A --region "$REGION" \
  --query 'Quota.[QuotaName,Value]' --output text

aws iam create-access-key --user-name "$USER"   # paste this into Cloud → AWS
```

Then: install the app, connect the key, activate a repository, commit the
two files from Step 7.
