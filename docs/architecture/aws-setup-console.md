# Setting up Airship on AWS — in the console

**What this is.** The path from an empty AWS account to a green CI job on
your own hardware, entirely through the AWS console and the Airship
dashboard. **No terminal is needed from start to finish.**

**Prefer a terminal?** [aws-setup-cli.md](aws-setup-cli.md) is the same
setup as commands, written for someone already comfortable with the AWS
CLI. Pick one guide and stay in it — they do not interleave.

**Audience.** Whoever owns the AWS account and whoever owns the GitHub
organization — often two people, so each step says which hat it needs. **No
prior AWS experience is assumed.**

**Time.** 30–45 minutes of real work. Add a day if you need a quota increase
(Step 3) — that one waits on AWS, so read ahead and file it early.

---

## Orientation — if you have never operated AWS

Airship's job is to start a fresh machine for each CI job and destroy it
when the job ends. To do that in your account it needs five things to exist
and be reachable. That is the whole integration: there is no
CloudFormation stack to deploy, no agent to install, no VPC peering, no
bootstrap script. You hand Airship one key and it calls the EC2 API.

| The thing | In one sentence | Why Airship needs it | Step |
| --- | --- | --- | --- |
| **An account** | The billing and isolation boundary — everything below lives inside exactly one | Machines launch here and you pay for them directly | [0](#step-0--get-the-account-ready-aws-admin) |
| **A region** | A geographic cluster of data centres, e.g. `eu-west-1` is Ireland. Resources in one region are invisible to another | Runners launch in a region you choose, per runner definition | [0](#step-0--get-the-account-ready-aws-admin) |
| **An IAM user with an access key** | IAM is AWS's permissions system; a "user" here is a robot identity, and its access key is a username/password pair for the API | This is how Airship proves it is allowed to launch and terminate machines | [1](#step-1--create-the-iam-user-aws-admin) |
| **A VPC with subnets** | A private network inside the region. A subnet is a slice of it pinned to one data centre ("availability zone") | Every machine has to be plugged into a network to boot | [2](#step-2--check-the-network-aws-admin) |
| **Service quotas** | Per-account, per-region caps AWS puts on how much you can run | The ceiling on how many runners can be alive at once — almost always the real limit, not Airship | [3](#step-3--raise-the-quotas-that-will-bite-aws-admin) |

Two pieces of vocabulary that appear constantly below:

- **EC2** is AWS's virtual machine service. An **instance** is one machine.
  An **instance type** like `c7i.2xlarge` names its shape — family,
  generation, size.
- A **security group** is a firewall attached to an instance. Airship makes
  one with **no inbound rules at all**; runners dial out to GitHub and are
  never dialled into.

### What this will cost

You pay AWS directly, by the second, for machines that only exist while a
job runs. Airship adds nothing that idles. Approximate on-demand prices in
`us-east-1` — the EC2 console shows the current price for any shape, and
other regions differ by 10–30%:

| Shape | vCPU / RAM | Roughly | A 10-minute job |
| --- | --- | --- | --- |
| `t3.small` | 2 / 2 GiB | $0.02/hr | under a cent |
| `t3.medium` | 2 / 4 GiB | $0.04/hr | under a cent |
| `c7i.2xlarge` | 8 / 16 GiB | $0.36/hr | about 6¢ |
| `c7i.4xlarge` | 16 / 32 GiB | $0.71/hr | about 12¢ |
| `g5.xlarge` (GPU) | 4 / 16 GiB + A10G | $1.00/hr | about 17¢ |

Disks are billed per gigabyte-**month**, so a 100 GiB volume that lives for
fifteen minutes costs a fraction of a cent. `preemptible: true` (spot
capacity) takes 60–70% off the instance price in exchange for AWS being
allowed to reclaim the machine.

The two numbers that bound your bill are `max_runners` (how many machines
can be alive at once, enforced by Airship) and your vCPU quota (enforced by
AWS). Step 0.3 adds a budget alarm as a third, independent net.

---

## Before you start

| You need | Notes |
| --- | --- |
| An AWS account, or the ability to make one | A **separate account for CI** is the recommendation — it makes the IAM policy uninteresting, the bill legible, and the blast radius a boundary rather than an argument. Step 0.1 covers both paths |
| Admin on the GitHub organization | To install the app and activate repositories. If that is not you, Steps 4–6 need the person it is |
| A region | One to start. Airship launches per-definition, so you can add more in a line of YAML later |
| A concurrency number | The most runners you want alive at once. This becomes `max_runners`. Start at 10 and raise it |
| A control plane | Airship-hosted, or your own container plus Postgres ([operations.md](operations.md)) |

---

## Step 0 — Get the account ready *(AWS admin)*

Skip any part of this you already have. If you run AWS day to day, the only
thing here worth reading is 0.4.

### 0.1 — Decide which account

**Recommended: a dedicated account for CI.** Runners execute whatever is in
your repositories, and a separate account means a mistake cannot reach
anything else you run. It also makes "what is CI costing us?" a single
number.

- **Already on AWS Organizations?** Console → **AWS Organizations** → **AWS
  accounts** → **Add an AWS account** → *Create an AWS account*. Give it a
  name like `acme-ci` and an email address nobody uses for anything else
  (`aws-ci@acme.com` — a group alias is ideal). It appears in a minute or
  two, and you reach it with **Switch role** or through IAM Identity Center.
- **Not on Organizations, and this is your first AWS account?** Sign up at
  [aws.amazon.com](https://aws.amazon.com/) with a credit card and a phone
  number. Verification takes a few minutes.
- **Using an existing shared account?** Fine, and supported — go with
  **Option B** in Step 1, whose `TerminateInstances` condition stops the
  credential from touching anything Airship did not create.

### 0.2 — Stop using the root user

The email address you signed up with is the **root user**. It can do
anything, including closing the account, and it cannot be restricted. Use it
twice — now, and never again.

1. Sign in as root. Top-right account menu → **Security credentials**.
2. Under **Multi-factor authentication (MFA)** → **Assign MFA device**.
   Any authenticator app works. Do this before anything else.
3. Create yourself a human admin identity and use *that* from now on:
   **IAM** → **Users** → **Create user**, name it after yourself, tick
   *Provide user access to the AWS Management Console*, then **Attach
   policies directly** → `AdministratorAccess`. (On a larger estate, IAM
   Identity Center with SSO is the better answer, but it is not required for
   anything here.)
4. **Save the sign-in details the last screen shows you** — the console
   sign-in URL, the user name and the password — or hit **Download .csv**.
   The password is shown once.
5. Give yourself a friendlier sign-in URL while you are here: the **IAM**
   dashboard's right-hand panel → **Account Alias** → **Create** →
   `acme-ci`. Your URL becomes
   `https://acme-ci.signin.aws.amazon.com/console` instead of
   `https://<account-id>.signin.aws.amazon.com/console`.
6. Sign out of root, open that URL, and sign in as the new user. It may ask
   you to set a new password on first sign-in.
7. Assign **this** user an MFA device too, the same way: **IAM** → **Users**
   → your user → **Security credentials** → **Assign MFA device**. Root's
   MFA protects root only.

**Clicked past the password screen?** It cannot be retrieved — set a new
one: **IAM** → **Users** → your user → **Security credentials** tab →
**Console sign-in** → **Manage console access**.

### Two users, two kinds of credential

Worth getting straight now, because the words overlap and the consequences
do not:

| User | Who it is | Its credential | Made in |
| --- | --- | --- | --- |
| Your admin user | You, a human, signing in to a browser | A console **password**, plus MFA | 0.2, here |
| `airship-runners` | A robot. No console access at all | An **access key** — an ID and a secret — and nothing else | [Step 1](#step-1--create-the-iam-user-aws-admin) |

Creating a user and creating its credential are two separate actions in IAM,
so a new user has no way to authenticate until you add one of the above.

> **Never create an access key for the root user**, and never give one to
> Airship. If anyone hands you root access keys, delete them — Step 1 makes
> the right kind of key instead.

### 0.3 — Set a budget alarm

This is the safety net that does not depend on anyone getting a
configuration right. Two minutes:

1. Console → **Billing and Cost Management** → **Budgets** → **Create
   budget**.
2. *Use a template (simplified)* → **Monthly cost budget**.
3. Enter an amount that would be surprising — if you expect $200, set $400 —
   and an email address.

AWS emails you at 85% and 100% of it. Airship cannot spend money you have
not allowed it to (`max_runners` and quotas both cap it), but a budget alarm
is the one control that notices when your assumptions were wrong.

### 0.4 — Choose a region

Pick one and use it for everything in this guide. What matters, in order:

1. **Close to your developers**, because a human waiting on CI feels the
   latency more than the machine does.
2. **Close to whatever your tests talk to** — a staging database, an
   artifact registry — since cross-region traffic is slow and billed.
3. **Data residency**, if you have rules about it. Nothing from inside a job
   leaves the region unless your job sends it.

Common choices: `us-east-1` (N. Virginia — cheapest, widest instance
selection, and where new types land first), `us-west-2` (Oregon),
`eu-west-1` (Ireland), `eu-central-1` (Frankfurt), `ap-south-1` (Mumbai).
Everything below uses `eu-west-1`; substitute yours.

> The region selector sits in the console's top-right corner, and **it
> changes what you see.** An instance launched in Ireland simply does not
> appear while the console is pointed at Oregon. This is the single most
> common source of "but I created it!" confusion.

### If your account is in an AWS Organization

A **service control policy** (SCP) is a rule set in the organization's
*management* account that caps what member accounts may do. It overrides
IAM entirely: a user with `AdministratorAccess` still cannot do what an SCP
denies, and you cannot see or change the policy from inside your own
account. Restricting EC2 to an approved region list is the most common one,
and it is usually invisible until a launch fails.

If you did not create this account yourself, ask whoever runs the
organization which regions are permitted **before** choosing one. A launch
blocked this way fails with `UnauthorizedOperation ... with an explicit deny
in a service control policy`, which no change on your side can fix.

---

## Step 1 — Create the IAM user *(AWS admin)*

Airship authenticates as an IAM user with a long-lived access key, which it
verifies with STS, seals with AES-256-GCM, and opens only for the duration
of a single API call.

An **access key** is two strings: an ID (`AKIA…`, not secret) and a secret
(shown exactly once). Together they are a password for the AWS API, so they
go straight into Airship and nowhere else — not a repository, not a
Dockerfile, not Slack.

A **policy** is the JSON document that says what an identity may do. You
have two reasonable choices.

### Option A — the quick start

Attach the AWS-managed **`AmazonEC2FullAccess`** policy, plus the cache
statements below if you want caching. In an account dedicated to CI this is
a perfectly defensible end state: there is nothing else in the account to
over-permit against.

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

Reading that, statement by statement: look things up; start machines; label
what it starts; make the no-inbound firewall; and destroy **only** machines
carrying its own label.

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

### Create the user

IAM is global, so the region selector does not matter for this step.

1. Console search bar → **IAM** → **Users** → **Create user**.
2. User name `airship-runners`. **Leave *Provide user access to the AWS
   Management Console* unchecked** — this identity is for the API only, and a
   robot does not need a login page.
3. **Next**, then set permissions:
   - *Option A:* **Attach policies directly** → search
     `AmazonEC2FullAccess` → tick it.
   - *Option B:* skip this screen (**Next** with nothing selected; a warning
     about no permissions is expected), create the user, then open it →
     **Permissions** tab → **Add permissions** → **Create inline policy** →
     the **JSON** tab → paste the document above → **Next** → name it
     `airship` → **Create policy**.
4. **Create user**.

### Create its access key

1. Open the user → **Security credentials** tab → **Access keys** →
   **Create access key**.
2. For *use case*, choose **Third-party service** (or **Other**),
   acknowledge the recommendation, **Next**, **Create access key**.
3. **Copy both values now**, or **Download .csv file**. The secret is shown
   on this screen and never again — if you lose it, delete the key and
   create another, which costs nothing.

### Check it worked

Check **both halves**: the key, and the policy. They fail independently and
only one of them is visible at connect time. **IAM** → **Users** →
`airship-runners` must show:

- a row under **Permissions** — either `AmazonEC2FullAccess` or an inline
  policy named `airship`. **An empty Permissions tab is the single most
  common reason a correctly connected credential launches nothing.**
- one active access key under **Security credentials**.

> **Why Airship's green check is not enough.** Step 5 verifies the
> credential with `sts:GetCallerIdentity`, which **requires no permissions
> at all** — it answers "who am I?" for any valid key. So a successful
> connect proves the key is real and names the right account and principal.
> It cannot prove the key may launch anything. A user with a valid key and
> no policy connects cleanly and then fails at the first launch with
> `UnauthorizedOperation: ... because no identity-based policy allows ...`,
> which the Events page shows verbatim.

> **If your account sets a default EBS encryption KMS key of its own** (not
> the AWS-managed `aws/ebs` key), the user also needs `kms:CreateGrant`,
> `kms:Decrypt` and `kms:GenerateDataKeyWithoutPlaintext` on that key.
> Volumes are always encrypted, so this is not optional. If you do not know
> whether your account does this, it almost certainly does not — a fresh
> account does not.

---

## Step 2 — Check the network *(AWS admin)*

Every machine needs to be plugged into a network. In AWS that is a **VPC**
(a private network inside one region) divided into **subnets** (one per
availability zone — a distinct data centre within the region).

Every account normally ships with a **default VPC** in every region, already
wired to the internet. Airship uses it without being asked, launching into
every default subnet and rotating availability zones per runner so that one
zone running out of capacity does not fail the job. Usually this step is
thirty seconds of confirming something already exists.

Search → **VPC** → **Your VPCs**. Check the region in the top-right first,
then look for a row whose **Default VPC** column says **Yes**.

**Found one?** Done; move to Step 3.

**Nothing listed?** The default VPC was deleted at some point (common in
accounts built by a platform team). Either recreate it — **VPC** → **Your
VPCs** → **Actions** → **Create default VPC** — or point Airship at your own
subnet per runner definition:

```yaml
extra_config:
  SubnetId: subnet-0123456789abcdef0
  SecurityGroupIds: [sg-0123456789abcdef0]   # optional; default is an egress-only group Airship makes
```

**A private subnet works** and needs no inbound anything, but it does need a
route out — a NAT gateway or equivalent — because the runner downloads
`actions/runner`, talks to GitHub, and (with caching on) fetches the proxy
from your control plane. A subnet with no route to the internet produces the
most confusing failure in this guide: the instance launches fine and the
runner never appears. The full destination list is in
[aws.md](aws.md#networking). Two things pay for themselves there: a **gateway
VPC endpoint for S3** if you enable caching, and a custom AMI with the runner
and your toolchain baked in, which removes the apt traffic entirely.

Airship opens **no inbound ports, ever.** The security group it creates has
no ingress rules, which also means there is no SSH into a runner — read a
job's logs in GitHub instead, or the instance's console output in EC2.

---

## Step 3 — Raise the quotas that will bite *(AWS admin)*

A **service quota** (historically "limit") is a per-account, per-region cap
AWS applies by default. They exist to stop a runaway script costing you a
fortune, and they are the limit on concurrent runners far more often than
Airship is. **Read this step before you need it:** an increase is a request
that AWS approves, usually in minutes to a few hours but occasionally a
business day.

### Do the arithmetic

*(vCPUs per runner) × (max concurrent runners) = the quota you need.*

A `c7i.2xlarge` is 8 vCPUs, so 10 concurrent runners need **80**. A fresh
account often has **32** — four of those runners, or two `c7i.4xlarge`. The
size is the number after the dot: `large` = 2 vCPU, `xlarge` = 4, `2xlarge` =
8, `4xlarge` = 16, doubling as it goes.

On-demand and spot are counted under **separate quotas**, so if you use
`preemptible: true` you need both.

### Look at what you have

Search → **Service Quotas** → **AWS services** → **Amazon Elastic Compute
Cloud (Amazon EC2)**, then search the quota list for `Running On-Demand
Standard`. Quotas are per-region — check the selector. The two that matter:

| Quota | For |
| --- | --- |
| Running On-Demand Standard (A, C, D, H, I, M, R, T, Z) vCPUs | Ordinary runners |
| All Standard Spot Instance Requests (vCPUs) | `preemptible: true` |

### Ask for more

Open the quota → **Request increase at account level** → enter the new value
→ **Request**. Track it under **Quota request history**.

Ask for a number with room in it — doubling your arithmetic is normal and
costs nothing, because a quota is a ceiling, not a reservation. You are
billed for machines you run, never for headroom. Modest increases on a
healthy account are often automatic; large jumps on a brand-new account may
come back asking what you are building, and "self-hosted CI runners for our
GitHub organization" is the whole answer.

Also worth a look: **EBS storage** quotas (gp3/gp2 TiB per region — 100 GiB
per runner × 50 runners is 5 TiB), and **GPU** quotas, which are zero until
you ask and are the slowest to be approved.

### Then set `max_runners` below it

Set Airship's `max_runners` **below** what your quotas allow. Then hitting
the ceiling is a clear message on the dashboard rather than a wall of EC2
failures, and Airship queues the eleventh job instead of failing it.

---

## Step 4 — Install the GitHub App *(GitHub org admin)*

Install the Airship app on your organization and grant it the repositories
you plan to use (you can start with one). Installing the app does not
activate anything or launch anything — Step 6 is the switch that matters.
The permissions it asks for, and why:

| Permission | Why |
| --- | --- |
| Repository → Administration: read & write | Register and remove repository runners |
| Repository → Actions: read | Look up jobs and runs |
| Repository → Contents: read | Read `airship.yaml` at a job's commit |
| Repository → Metadata: read | Required by GitHub |
| Organization → Members: read | Tell admins from members; team rules in `access.yaml` |
| Organization → Self-hosted runners: read & write | Organization-wide idle runners in `airship.global.yaml` |
| *(optional)* Contents: write, Pull requests: write | Only so the access-control API can open PRs against the `.airship` repository |

**If you run your own control plane, you create the app yourself** — and
there is one setting that is easy to miss and breaks everything silently.
Under the app's **Permissions & events** → **Subscribe to events**, tick:

- **Workflow job** — this is what tells Airship a job queued. Without it
  nothing ever launches
- **Push**
- **Repository**

An app subscribed to no events looks completely healthy: you can sign in,
connect a cloud and activate repositories, because all of that is the
dashboard talking to Airship. Jobs simply never appear. Callback URL,
webhook URL, secret and the rest are listed in
[operations.md](operations.md) and in the repository README.

---

## Step 5 — Connect AWS *(GitHub org admin)*

Open **Cloud → AWS** in the dashboard, paste the access key ID and secret
from Step 1, and choose the default region from Step 0.4 (where runners
launch when a definition names none).

What happens on submit: the credential is verified with
`sts:GetCallerIdentity`, and only then sealed and stored. **You get the
account ID and the principal ARN echoed back** — read them, and confirm they
are the account and user you meant. That is the moment a key pasted from the
wrong account gets caught. The secret is never returned by any endpoint
again.

If it is rejected, the key was mistyped or has been deactivated; nothing is
stored, so just try again. A key that verifies here but lacks a permission
fails later, at launch, with the missing action named in **Events** — see
the box in Step 1's *Check it worked*.

One credential per cloud per installation. Rotating the key *within the same
account* is allowed any time. Swapping it for a **different account's** key,
or disconnecting entirely, is refused while runners launched with it are
still alive — without that key their instances could not be terminated.

---

## Step 6 — Activate repositories *(GitHub org admin)*

**Repositories →** toggle each one on. This is deliberately a separate,
explicit step: an `airship.yaml` in a repository nobody activated launches
nothing, so a committed config file can never start spending money on its
own.

Public repositories need a **second** choice, "allow public access", because
their fork pull requests run untrusted code on your machines. That is the
gate; `access.yaml` and `max_runners` are the bounds. Start with a private
repository for your first job.

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

Read as a list of attempts in order, that says: try a spot `c7i.2xlarge`
(cheap, reclaimable); if spot capacity is gone, the same shape on-demand;
if that shape is unavailable in the region, a different family. Plus 100 GiB
of fast gp3 disk, and S3-backed cache acceleration.

Choosing the shape, if you have no idea: start at `c7i.2xlarge` (8 vCPU /
16 GiB) for a typical test suite, `t3.small` for something small like a
linter, and change it once you have seen a real job's `nproc` and timings.
Compute-heavy builds want the `c` families; anything that holds a lot in
memory wants `m` or `r`. Note the **disk defaults to 75 GiB of gp2** if you
set no `BlockDeviceMappings`, which is more than most jobs need. Details and
the full list of knobs: [the configuration reference](../airship-yaml.md).

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

Push, then watch in this order. The first job is slower than the rest —
a cold machine downloads the runner and your toolchain, which is what the
custom AMI under [Going to production](#going-to-production) removes.

| Where | What you should see |
| --- | --- |
| Airship → **Jobs** | The job appears as queued within a second or two of GitHub's webhook, with the definition and machine shape it planned |
| Airship → **Runners** | A runner moving `pending → launching → running → busy`, with its instance ID |
| EC2 console, filter tag `airship:installation` | One instance, `Name` = the runner's name, your tags on it and its volume, no inbound security group rules |
| GitHub → the job log | Steps running on a self-hosted runner, with labels `self-hosted`, `x64`, `aws`, `cpu`, `airship-build--…` |
| GitHub → Settings → Actions → Runners | The runner present during the job, gone a couple of minutes after |
| EC2 console again | The instance terminated within seconds of the job finishing |
| Airship → **Events** | The whole story, including anything that silently degraded — a cache that was unavailable, for instance |

To filter by tag in the EC2 console: **Instances** → the search box → **Add
filter** → *Tags* → key `airship:installation`. Terminated instances stay
visible, greyed out, for about an hour — that is normal, and useful.

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
| Watch the bill by tag | Cost Explorer grouped by `airship:repository` needs no extra instrumentation. Enable the tag first: **Billing** → **Cost allocation tags** → activate `airship:repository`, which takes about a day to start populating |
| Alarm on CloudTrail | `RunInstances`, `TerminateInstances` and `GetFederationToken` by the Airship user — a cheap tripwire |

---

## Troubleshooting

Start at Airship's **Events** page for every one of these: it records every
launch failure, every degraded cache and every config error, with the
cloud's own message quoted. The left column below is what you will see
there or on the Jobs page. These causes are the same whichever guide you
followed.

### Reading an AWS error

AWS error codes are terse but honest, and they sort into three kinds:

- **`Unauthorized…`, `AccessDenied`, `AuthFailure`** — permissions or the
  key itself. The message names the action it wanted; compare with Step 1.
- **`…LimitExceeded`** — a quota. Yours to raise (Step 3).
- **`Insufficient…Capacity`** — AWS has run out of that shape in that zone
  right now. Nothing is misconfigured; Airship already rotates zones and
  falls back through your `instance_type` list.

### Table

| Symptom | Cause | Fix |
| --- | --- | --- |
| Job stays queued and **no job events appear at all** — Events shows only `cloud.connected` / `repository.activated` | GitHub is sending no webhooks. Either the app is subscribed to no events, or deliveries are failing | Check the app's **Permissions & events** → **Subscribe to events**: **Workflow job** (the one that matters), **Push** and **Repository**. Then **Advanced** → **Recent Deliveries** for 401s (secret mismatch) or 502s/timeouts (unreachable). Dashboard actions like activating a repository still work without webhooks, which is why the setup *looks* healthy |
| Job stays queued, Jobs page says **Not activated** | The repository was never activated | Activate it (Step 6). Jobs still queued are planned again automatically |
| **No cloud** | No AWS credential on the installation | Connect one (Step 5). Jobs skipped for this reason are re-planned on connect |
| **No runner serves the labels** | No definition covers every `airship-` label of the job | Check the label spelling and that the definition is in the file *at the job's commit* — editing it on another branch changes nothing for this job |
| **Config problem** | `airship.yaml` does not parse | The detail names the field. Use the dashboard's checker |
| **Public repository** | Public repo not activated for public access | Activate it that way, knowing fork PRs then run on your machines |
| **Access denied** | `access.yaml` does not admit this person, repository or event | Check the policy; a broken `access.yaml` closes *every* organization runner until fixed |
| Launch fails: *this region has no default VPC* | Exactly that | Create one, or set `SubnetId` (Step 2) |
| Launch fails: `VcpuLimitExceeded`, `InstanceLimitExceeded` | Service quota | Raise the quota (Step 3); lower `max_runners` meanwhile |
| Launch fails: `InsufficientInstanceCapacity` repeatedly | The AZ or the whole region is short of that shape | Airship already rotates AZs and falls back; add shapes to `instance_type`, or a second `region` |
| Launch fails: `UnauthorizedOperation … no identity-based policy allows …` | The user has **no policy attached at all**, so the key authenticates and can do nothing | IAM → Users → `airship-runners` → **Permissions** → **Add permissions**. No need to reconnect: the existing key picks the policy up in seconds |
| Launch fails: `UnauthorizedOperation` naming one action | A policy is attached but missing that action | The message names it. Compare against Step 1, add it, relaunch |
| Launch fails: `UnauthorizedOperation … with an explicit deny in a service control policy: arn:aws:organizations::…` | An **SCP** on the organization forbids it, most often outside an allowed region list. No IAM policy can override it | Use a permitted region, or have the organization's management account change the SCP. See [Step 0](#if-your-account-is-in-an-aws-organization) |
| Launch fails: `AuthFailure`, `InvalidClientTokenId` | The key was deleted or deactivated | Make a new key (Step 1) and reconnect. The credential shows as invalid on the Cloud page |
| Launch fails: `InvalidParameterValue` naming the instance type | That shape does not exist in that region | Not every type is in every region. Check the EC2 console's instance-type list with the region selected |
| Instance launches, runner never appears in GitHub | The machine cannot reach GitHub, or the AMI is missing something | A private subnet with no route out (Step 2), or a custom AMI without `curl`/`tar`. Read the instance's console output: EC2 → the instance → **Actions** → **Monitor and troubleshoot** → **Get system log** |
| Runner appears, job never starts | Label mismatch between the registration and `runs-on` | Compare the labels on the Runners page with the job's |
| Jobs run but caching does nothing | Expected on GCP, Azure and Daytona; on AWS check Events | Events records `cache.unavailable` with the reason — usually a missing S3 permission or a control plane built without the proxy binary |
| Runner finished `idle` | It booted and no job arrived within 10 minutes | Usually a suffix mismatch, or the job was cancelled while the machine booted |
| Runner finished `launch_unconfirmed` | The cloud's answer was lost | Expected and handled: the job is retried and the scan reconciles the machine. Repeated occurrences mean a network problem between the control plane and EC2 |
| An instance you did not expect | Check its tags | Untagged instances are not Airship's. Tagged ones with no live runner are terminated by the next inventory pass |
| You cannot find a resource you just made | The console is pointed at another region | Check the top-right selector (Step 0.4) |

---

## Turning it off

| To | Do |
| --- | --- |
| Stop one repository | Deactivate it. Its running jobs finish; nothing new launches |
| Stop everything, keep the setup | Set `max_runners` to 1, or deactivate all repositories |
| Disconnect AWS | Only possible once no runners launched with that credential are alive — otherwise their instances could not be terminated. Wait for them to finish, then disconnect |
| Remove Airship entirely | Uninstall the GitHub App. Every runner is finished with reason `deleted` and its instance terminated. Then delete the IAM user, and by hand: the `airship-runners` security group in each VPC, and the `airship-cache-*` buckets (whose objects expire after 7 days anyway) |

Nothing Airship makes keeps costing money once the instances are gone: a
security group is free, and cache objects expire themselves. The one thing
worth checking after you tear down is **EC2 → Elastic Block Store → Volumes**
for anything `available` rather than `in-use` — a detached volume still
bills. Airship deletes volumes with their instances, so this should be empty.

---

## Glossary

| Term | What it means here |
| --- | --- |
| **Access key** | An ID (`AKIA…`) and a secret that together authenticate API calls. What you give Airship |
| **AMI** | Amazon Machine Image — the disk image a machine boots from. Baking your own is the biggest speed win |
| **ARN** | Amazon Resource Name — the globally unique ID of anything in AWS, like `arn:aws:iam::123…:user/airship-runners` |
| **Availability zone (AZ)** | One data centre within a region. Airship rotates them so one zone's capacity shortage does not fail a job |
| **EBS** | Elastic Block Store — the disks attached to instances. Billed per GB-month; always encrypted here |
| **EC2** | Elastic Compute Cloud — virtual machines. One instance per CI job |
| **IAM** | Identity and Access Management — users, roles and the policies that permit actions |
| **Instance type** | A machine's shape, e.g. `c7i.2xlarge`: family `c7i`, size `2xlarge` (8 vCPU) |
| **On-demand / spot** | Full price and always yours, versus 60–70% cheaper and reclaimable at short notice (`preemptible: true`) |
| **Policy** | The JSON document listing allowed actions. Managed (AWS-written) or inline (yours) |
| **Region** | A geographic group of AZs, e.g. `eu-west-1`. Resources are region-scoped and invisible across regions |
| **Root user** | The sign-up identity. Unrestrictable — give it MFA and then leave it alone |
| **S3** | Object storage. Holds cache entries, in `airship-cache-*` buckets with a 7-day expiry |
| **SCP** | Service control policy — an organization-level rule that overrides IAM. See Step 0 |
| **Security group** | An instance's firewall. Airship's has no inbound rules at all |
| **Service quota** | A per-account, per-region cap. The usual real limit on concurrent runners |
| **STS** | Security Token Service — issues temporary credentials and answers "who am I?" (`GetCallerIdentity`) |
| **Subnet** | A slice of a VPC in one AZ. Machines get their addresses here |
| **Tag** | A key/value label. `airship:installation` is how Airship recognises its own machines |
| **VPC** | Virtual Private Cloud — your private network within a region |
