# Step 6 — The first real job

**What this is.** Proving the whole path: install the app, sign in, connect
a cloud, run one job on one real EC2 instance, and watch the machine
disappear again. Until this passes, the pilot is not deployed — it is
configured.

**Time.** 15 minutes, of which about three is the instance booting.

**Prerequisites.** Steps 4 and 5 green, and an AWS account you can create
an IAM user in.

---

## 1. Install the app

On the GitHub App's page (**Settings → Developer settings → GitHub Apps →
Airship**): **Install App** → pick the organization → **Only select
repositories** and choose one test repository.

Use a throwaway repository with a trivial workflow in it. A real one works,
but the first job should not be something anybody is waiting on.

Then check **Advanced → Recent Deliveries** on the app: the `installation`
delivery should be `200`. A `502` or a timeout means the webhook URL is
wrong or the API is down; a `401` means `GITHUB_WEBHOOK_SECRET` on Render
differs from the app's.

---

## 2. Sign in

Open `https://app.example.com` and choose **Sign in with GitHub**. You
should land on the dashboard with your organization selected.

| If instead | Then |
| --- | --- |
| You land back on the sign-in page | The dashboard and API are not on the same registrable domain, so the session cookie was not sent. This is the one thing stage 0 cannot work around — see [1-accounts.md, section 3](1-accounts.md#3-two-hostnames-on-one-domain) |
| GitHub says "redirect_uri mismatch" | The app's Callback URL is not exactly `https://api.example.com/v1/auth/github/callback` |
| The dashboard loads but is empty of organizations | The app is installed on an account you are not a member of, or the installation webhook never arrived (check Recent Deliveries) |

---

## 3. Connect the AWS account

Under **Cloud**, in the **AWS** card: **Connect AWS**. It asks for an
access key, a secret key and a default region.

Create the IAM user first:
[architecture/aws-setup.md, step 1](../../architecture/aws-setup.md#step-1--create-the-iam-user-aws-admin).
For a pilot smoke test, `AmazonEC2FullAccess` in a scratch account is
fine; the scoped policy in that guide is what a shared account should use.

| Field | Value |
| --- | --- |
| **Access key** | The IAM user's, `AKIA…` |
| **Secret key** | The IAM user's secret |
| **Default region** | `eu-west-1` unless you have a reason. Runner definitions that name no region, and every one-line label, launch here |

Airship verifies the key with STS before saving it, so a bad key is
rejected on the spot. Once saved, the key is **sealed with
`CREDENTIALS_KEY`** and never shown again.

Then set the concurrency cap under **Settings → Most runners at once**:
start at `10`. It is the hard ceiling on live runners across every
repository, held under a row lock, so it is also your protection against a
runaway workflow spending the customer's money.

---

## 4. Activate the repository

Under **Repositories**, activate the test repository.

This is deliberate: a config file never launches anything in a repository
nobody activated, and jobs in inactive repositories cost no API calls.

---

## 5. Run a job

No `airship.yaml` is needed for this one. A one-line label describes the
machine by itself:

```yaml
# .github/workflows/airship.yml
name: airship smoke test
on: workflow_dispatch

jobs:
  smoke:
    runs-on: airship-aws-t3-medium-ubuntu2404--${{ github.run_id }}
    steps:
      - run: echo "hello from $(uname -a)"
      - run: nproc && free -h
```

Commit it, then run it from the repository's **Actions** tab
(**airship smoke test → Run workflow**).

The `--${{ github.run_id }}` suffix is what makes the runner single-use:
it makes the label unique to this run, so the machine Airship launches
takes this job and no other.

### What should happen

| | Where | Within |
| --- | --- | --- |
| The job appears, queued | Dashboard → **Jobs** | seconds |
| A runner appears as `pending`, then `launching` | Dashboard → **Runners** | seconds |
| An EC2 instance appears, tagged `airship` | AWS console, your region | ~30 s |
| The runner goes `active`, the job starts | Both | 1–3 min (boot and runner registration) |
| The job goes green | GitHub Actions | |
| The runner disappears, the instance terminates | Dashboard → **Runners**, AWS console | within one reconcile pass (15 s) of the job finishing |

**The last row is the one to watch.** Launching is the easy half; the
reconciler terminating the machine afterwards is what stops the bill
growing. If the instance is still running a minute after the job finished,
the reconciler is not running — and the first thing to check is that
`DATABASE_URL` is on port **5432**, not 6543
([3-supabase.md](3-supabase.md#1-the-session-pooler-never-the-transaction-pooler)).

### If the job stays queued

**Events** in the dashboard records why, with the cloud's own error
message. The usual causes:

| Event | Cause |
| --- | --- |
| nothing at all | The `workflow_job` webhook never arrived: check Recent Deliveries, and that **Workflow job** is subscribed on the app |
| `capacity.reached` | `max_runners` is lower than the jobs queued |
| an EC2 error about vCPU quota | A new AWS account's regional limit. Request an increase, or use a smaller type |
| `UnauthorizedOperation` | The IAM policy is missing `ec2:RunInstances` |
| `cache.unavailable` | Only with `cache: true`; the job still runs, without acceleration |

---

## 6. Then write a real config

The one-line label is for smoke tests. Real runners are defined in
`airship.yaml` at the root of each activated repository, which is where
disk, spot, caching, idle pools and non-AWS clouds live:

```yaml
# airship.yaml
runners:
  - name: build
    cloud: aws
    instance_type: [c7i.xlarge, m7i.xlarge]
    machine_image: ubuntu2404
    region: eu-west-1
    labels: [airship-build]
    extra_config:
      cache: true
```

```yaml
    runs-on: airship-build--${{ github.run_id }}
```

The full format is [airship-yaml.md](../../airship-yaml.md). Caching needs
runner machines to reach `PUBLIC_URL` over 443, which they can here because
it is a public address.

---

## Go-live checklist

One green job means the path works. Before a customer depends on it, work
through [checklist.md](../checklist.md) — it is short, and the items it
flags for stage 0 are the ones that bite in week two: the
`CREDENTIALS_KEY` backup, a restore you have actually tried, alerts, and
someone who owns bumping `RUNNER_VERSION`.

Then [operate.md](operate.md) is the day-to-day: releases, rollbacks,
rotating a secret, and what the failure modes look like in the logs.
