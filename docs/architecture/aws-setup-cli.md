# Setting up Airship on AWS — from the CLI

**What this is.** The same setup as
[aws-setup-console.md](aws-setup-console.md), as commands. Terse on purpose:
it assumes you know your way around a shell and an AWS account.

**New to AWS?** Use [aws-setup-console.md](aws-setup-console.md) instead. It
covers the same ground with clicks, explains what each resource is, and
needs no terminal at all. Pick one guide and stay in it — they do not
interleave.

**Two steps have no CLI**, because they are web-only: installing the GitHub
App (Step 4) and creating the API token that Steps 5 and 6 authenticate
with. Both are called out where they fall.

**Time.** 15 minutes, plus any wait on a quota increase.

---

## Prerequisites

```bash
# macOS; elsewhere see
# https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html
brew install awscli

aws configure                 # your *admin* key, a default region, output: json
aws sts get-caller-identity   # confirm the account before changing anything
```

Variables the rest of this guide uses:

```bash
export REGION=eu-west-1
export IAM_USER=airship-runners
```

> `aws sts get-caller-identity` is the command to reach for whenever you are
> unsure which account or identity a terminal is pointed at.

### Before you pick a region: check for an organization SCP

A **service control policy** set in an organization's management account
overrides IAM entirely and commonly restricts EC2 to an approved region
list. It is invisible from inside a member account until a launch fails. If
you did not create this account yourself, find out first — this loop
answers it in one shot, using the runner key from Step 1:

```bash
for r in us-east-1 us-west-2 eu-west-1 eu-central-1 ap-southeast-2; do
  printf '%-16s ' "$r"
  aws ec2 describe-instances --region "$r" --max-items 1 >/dev/null 2>&1 \
    && echo allowed || echo denied
done
```

A denial whose message contains *explicit deny in a service control policy*
is an SCP and no change on your side can fix it. One that says *no
identity-based policy allows* is your own missing policy (Step 1).

### A budget alarm, while you think of it

Independent of everything below, and the only control that notices when your
assumptions were wrong:

```bash
cat > /tmp/budget.json <<JSON
{ "BudgetName": "airship-ci", "BudgetLimit": {"Amount": "400", "Unit": "USD"},
  "TimeUnit": "MONTHLY", "BudgetType": "COST" }
JSON
aws budgets create-budget --account-id "$(aws sts get-caller-identity --query Account --output text)" \
  --budget file:///tmp/budget.json
```

---

## Step 1 — The IAM user, its policy and its key

Two choices of policy. **Option A** is `AmazonEC2FullAccess` — defensible in
an account dedicated to CI. **Option B** is scoped to exactly what the
provider calls, and its `TerminateInstances` condition means the credential
**cannot destroy an instance Airship did not tag**, which is what makes
sharing an account with your own workloads safe.

```bash
aws iam create-user --user-name "$IAM_USER"
```

### Option A

```bash
aws iam attach-user-policy --user-name "$IAM_USER" \
  --policy-arn arn:aws:iam::aws:policy/AmazonEC2FullAccess
```

### Option B

```bash
cat > airship-policy.json <<'JSON'
{
  "Version": "2012-10-17",
  "Statement": [
    { "Sid": "Describe", "Effect": "Allow",
      "Action": ["ec2:DescribeInstances", "ec2:DescribeImages", "ec2:DescribeSubnets",
                 "ec2:DescribeSecurityGroups", "sts:GetCallerIdentity"],
      "Resource": "*" },
    { "Sid": "LaunchRunners", "Effect": "Allow",
      "Action": ["ec2:RunInstances"], "Resource": "*" },
    { "Sid": "TagOnCreate", "Effect": "Allow",
      "Action": ["ec2:CreateTags"], "Resource": "*",
      "Condition": { "StringEquals": {
        "ec2:CreateAction": ["RunInstances", "CreateSecurityGroup"] } } },
    { "Sid": "RunnerSecurityGroup", "Effect": "Allow",
      "Action": ["ec2:CreateSecurityGroup"], "Resource": "*" },
    { "Sid": "TerminateOnlyOurOwn", "Effect": "Allow",
      "Action": ["ec2:TerminateInstances"], "Resource": "*",
      "Condition": { "StringLike": {
        "ec2:ResourceTag/airship:installation": "*" } } }
  ]
}
JSON

aws iam put-user-policy --user-name "$IAM_USER" \
  --policy-name airship --policy-document file://airship-policy.json
```

`file://` is relative to your working directory, which is the usual reason
this line fails.

**Cache acceleration** adds three statements — `s3:CreateBucket`,
`ListBucket`, `GetBucketLocation`, `PutBucketPublicAccessBlock`,
`PutLifecycleConfiguration` on `arn:aws:s3:::airship-cache-*`; `s3:GetObject`,
`PutObject`, `AbortMultipartUpload`, `ListMultipartUploadParts` on
`arn:aws:s3:::airship-cache-*/*`; and `sts:GetFederationToken` on `*`. Those
object permissions are the ceiling on what any runner can do with the cache.
See [`../caching.md`](../caching.md).

**`extra_config.IamInstanceProfile`** additionally needs `iam:PassRole` on
that specific role, conditioned on
`iam:PassedToService = ec2.amazonaws.com`.

**A non-default EBS encryption KMS key** in your account additionally needs
`kms:CreateGrant`, `kms:Decrypt` and `kms:GenerateDataKeyWithoutPlaintext`
on it. Volumes are always encrypted, so this is not optional.

### The key

```bash
aws iam create-access-key --user-name "$IAM_USER"
```

Keep the output — the secret is shown once. Paste it in Step 5.

### Verify both halves

The key and the policy fail independently, and only one of them is visible
at connect time:

```bash
aws iam list-access-keys        --user-name "$IAM_USER"   # one key, Status Active
aws iam list-attached-user-policies --user-name "$IAM_USER"
aws iam list-user-policies      --user-name "$IAM_USER"   # inline, for Option B
```

> Airship's connect check uses `sts:GetCallerIdentity`, which **requires no
> permissions at all**. A successful connect proves the key is real and
> names the right account; it cannot prove the key may launch anything. A
> user with a valid key and no policy connects cleanly, then fails at first
> launch with `UnauthorizedOperation ... because no identity-based policy
> allows ...`.

So prove the permissions against EC2 directly. A dry run is free and
launches nothing:

```bash
# in a subshell, so the runner key does not leak into the rest of your session
(
  export AWS_ACCESS_KEY_ID=AKIA...  AWS_SECRET_ACCESS_KEY=...

  aws sts get-caller-identity                                   # user/airship-runners
  aws ec2 describe-instances --region "$REGION" --max-items 5    # a (possibly empty) list

  AMI=$(aws ec2 describe-images --region "$REGION" --owners 099720109477 \
    --filters 'Name=name,Values=ubuntu/images/hvm-ssd*/ubuntu-jammy-22.04-amd64-server-*' \
              'Name=state,Values=available' \
    --query 'sort_by(Images,&CreationDate)[-1].ImageId' --output text)

  aws ec2 run-instances --dry-run --region "$REGION" \
    --image-id "$AMI" --instance-type t3.small
)
```

`DryRunOperation` is the answer you want. `UnauthorizedOperation` names what
is missing.

---

## Step 2 — The network

Airship launches into the region's **default VPC**, rotating availability
zones per runner so one zone's capacity shortage does not fail a job.

```bash
aws ec2 describe-vpcs --region "$REGION" \
  --filters Name=isDefault,Values=true --query 'Vpcs[].VpcId' --output text
```

A `vpc-…` means you are done. Empty output means it was deleted; either:

```bash
aws ec2 create-default-vpc --region "$REGION"
```

or point each definition at your own subnet:

```yaml
extra_config:
  SubnetId: subnet-0123456789abcdef0
  SecurityGroupIds: [sg-0123456789abcdef0]   # optional; default is an egress-only group Airship makes
```

A **private subnet works** but needs a route out (NAT gateway or
equivalent): the runner downloads `actions/runner`, talks to GitHub and,
with caching on, fetches the proxy from your control plane. No route out
gives you the most confusing failure here — the instance launches and the
runner never appears. Destinations: [aws.md](aws.md#networking). Worth
adding: a gateway VPC endpoint for S3 if you enable caching.

Airship opens **no inbound ports, ever** — so there is also no SSH into a
runner. Use the job log, or the instance's console output.

---

## Step 3 — Quotas

*(vCPUs per runner) × (max concurrent runners)*. A `c7i.2xlarge` is 8 vCPUs,
so 10 concurrent runners need 80; a fresh account often has 32. On-demand
and spot count under **separate** quotas.

```bash
# Running On-Demand Standard (A, C, D, H, I, M, R, T, Z) vCPUs
aws service-quotas get-service-quota --service-code ec2 \
  --quota-code L-1216C47A --region "$REGION" \
  --query 'Quota.[QuotaName,Value]' --output text

# All Standard Spot Instance Requests (vCPUs)
aws service-quotas get-service-quota --service-code ec2 \
  --quota-code L-34B43A08 --region "$REGION" \
  --query 'Quota.[QuotaName,Value]' --output text

# Anything else by name, e.g. GPU families
aws service-quotas list-service-quotas --service-code ec2 --region "$REGION" \
  --query "Quotas[?contains(QuotaName,'On-Demand G')].[QuotaCode,QuotaName,Value]" --output table
```

Request an increase:

```bash
aws service-quotas request-service-quota-increase --service-code ec2 \
  --quota-code L-1216C47A --desired-value 160 --region "$REGION"

aws service-quotas list-requested-service-quota-change-history \
  --service-code ec2 --region "$REGION" --output table      # track it
```

Ask for room — a quota is a ceiling, not a reservation, and you are never
billed for headroom. Also check EBS storage (gp3/gp2 TiB per region) and GPU
quotas, which start at zero and are slowest to approve.

Then set Airship's `max_runners` **below** the quota, so the ceiling is a
dashboard message rather than a wall of EC2 failures.

---

## Step 4 — Install the GitHub App *(no CLI — web only)*

See [aws-setup-console.md, Step 4](aws-setup-console.md#step-4--install-the-github-app-github-org-admin)
for the permission table.

**If you run your own control plane,** the one setting that breaks
everything silently: the app's **Permissions & events** → **Subscribe to
events** must include **Workflow job**, **Push** and **Repository**. An app
subscribed to no events looks healthy — you can connect a cloud and activate
repositories, because that is the dashboard talking to Airship — and jobs
simply never appear.

---

## Step 5 — Connect AWS

Create an API token in the dashboard first, under **API keys** *(web only)*.
Then:

```bash
export AIRSHIP_API=https://api.example.com
export AIRSHIP_TOKEN=asa_...

curl -fsS -X POST "$AIRSHIP_API/api/v1/cloud-connect" \
  -H "Authorization: Bearer $AIRSHIP_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"cloud": "aws",
       "credentials": {"access_key": "AKIA...",
                       "secret_key": "...",
                       "region": "eu-west-1"}}'
```

`region` is optional and sets the default used when a definition names none.

Check what is connected:

```bash
curl -fsS "$AIRSHIP_API/api/v1/cloud-connect" \
  -H "Authorization: Bearer $AIRSHIP_TOKEN"
```

On submit Airship verifies the credential with `sts:GetCallerIdentity`, then
seals and stores it, and **echoes back the account ID and principal ARN** —
read them and confirm they are the account and user you meant. The secret is
never returned again.

One credential per cloud per installation. Rotating the key within the same
account is always allowed; swapping it for a different account's key, or
disconnecting, is refused while runners launched with it are alive, since
without that key their instances could not be terminated.

---

## Step 6 — Activate repositories

Activation is deliberately separate from everything else: an `airship.yaml`
in a repository nobody activated launches nothing, so a committed config
file can never start spending money on its own.

```bash
curl -fsS "$AIRSHIP_API/v1/api/repositories" \
  -H "Authorization: Bearer $AIRSHIP_TOKEN"                 # what the app can see

curl -fsS -X PUT "$AIRSHIP_API/v1/api/repositories/api/activation" \
  -H "Authorization: Bearer $AIRSHIP_TOKEN" \
  -H 'Content-Type: application/json' -d '{}'
```

The path takes the **repository name alone**, not `owner/repo`, matched
case-insensitively within the installation.

A public repository needs a second, explicit choice, because its fork pull
requests run untrusted code on your machines:

```bash
curl -fsS -X PUT "$AIRSHIP_API/v1/api/repositories/api/activation" \
  -H "Authorization: Bearer $AIRSHIP_TOKEN" \
  -H 'Content-Type: application/json' -d '{"allowPublic": true}'
```

Deactivate with `DELETE` on the same path. Start with a private repository
for your first job.

---

## Step 7 — The two files

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

The lists are zipped into a fallback chain: spot `c7i.2xlarge`, then the
same shape on-demand, then a different family. **The disk defaults to 75 GiB
of gp2** when you set no `BlockDeviceMappings`. Full reference:
[the configuration reference](../airship-yaml.md).

`.github/workflows/ci.yml`:

```yaml
jobs:
  test:
    runs-on: airship-build--${{ github.run_id }}
    steps:
      - uses: actions/checkout@v4
      - run: nproc && free -g && df -h /
```

The `--${{ github.run_id }}` suffix registers the runner under a label
unique to that run, so GitHub can only hand it a job from that run. Use it
everywhere **except** idle pools, which exist before a run does. Only labels
starting with `airship-` are looked up at all, so `ubuntu-latest` jobs are
untouched and you can migrate one workflow at a time.

---

## Step 8 — Verify

```bash
# live runners, with what each is for
aws ec2 describe-instances --region "$REGION" \
  --filters 'Name=tag-key,Values=airship:installation' \
            'Name=instance-state-name,Values=pending,running' \
  --query 'Reservations[].Instances[].[InstanceId,InstanceType,State.Name,Tags[?Key==`airship:repository`].Value|[0]]' \
  --output table

# the security group it made: expect no ingress rules at all
aws ec2 describe-security-groups --region "$REGION" \
  --filters Name=group-name,Values=airship-runners \
  --query 'SecurityGroups[].IpPermissions' --output json

# a machine that booted but never registered
aws ec2 get-console-output --region "$REGION" --instance-id i-… --output text
```

Alongside: **Jobs** shows the job queued within a second or two of the
webhook; **Runners** shows `pending → launching → running → busy`; the job
log shows labels `self-hosted`, `x64`, `aws`, `cpu`, `airship-build--…`; and
the instance is terminated within seconds of the job finishing. **Events**
records every launch failure and every silent degradation with the cloud's
own message.

**A deliberate first-week test:** cancel a job while it is queued, and
cancel another mid-run. Confirm no instance survives either.

---

## Troubleshooting

The failure modes and their causes are identical whichever guide you
followed, so they live in one place:
[aws-setup-console.md § Troubleshooting](aws-setup-console.md#troubleshooting).
It covers the empty webhook subscription, the missing policy, the
organization SCP, quota errors and capacity errors, with the AWS error codes
sorted by what they mean.

Start at **Events** for all of them.

---

## Turning it off

```bash
# nothing new launches; running jobs finish
curl -fsS -X DELETE "$AIRSHIP_API/v1/api/repositories/api/activation" \
  -H "Authorization: Bearer $AIRSHIP_TOKEN"
```

Uninstalling the GitHub App stops everything: every runner is finished with
reason `deleted` and its instance terminated. Disconnecting the credential
is refused while runners launched with it are alive.

Then, by hand:

```bash
aws iam delete-access-key --user-name "$IAM_USER" --access-key-id AKIA...
aws iam delete-user-policy --user-name "$IAM_USER" --policy-name airship
aws iam delete-user --user-name "$IAM_USER"

aws ec2 delete-security-group --region "$REGION" \
  --group-id "$(aws ec2 describe-security-groups --region "$REGION" \
      --filters Name=group-name,Values=airship-runners \
      --query 'SecurityGroups[0].GroupId' --output text)"

# cache buckets, whose objects expire after 7 days anyway
aws s3 ls | grep airship-cache-

# should be empty: a detached volume still bills
aws ec2 describe-volumes --region "$REGION" \
  --filters Name=status,Values=available --query 'Volumes[].VolumeId' --output text
```

---

## Appendix — the whole AWS side in one go

```bash
#!/usr/bin/env bash
set -euo pipefail
REGION=eu-west-1
IAM_USER=airship-runners

aws sts get-caller-identity   # confirm the account before changing anything

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

aws iam create-user --user-name "$IAM_USER"
aws iam put-user-policy --user-name "$IAM_USER" \
  --policy-name airship --policy-document file:///tmp/airship-policy.json

aws ec2 describe-vpcs --region "$REGION" --filters Name=isDefault,Values=true \
  --query 'Vpcs[].VpcId' --output text | grep -q vpc- \
  || aws ec2 create-default-vpc --region "$REGION"

aws service-quotas get-service-quota --service-code ec2 \
  --quota-code L-1216C47A --region "$REGION" \
  --query 'Quota.[QuotaName,Value]' --output text

aws iam create-access-key --user-name "$IAM_USER"   # feed this into Step 5
```

Then: install the app (Step 4), connect the key (Step 5), activate a
repository (Step 6), commit the two files from Step 7.
