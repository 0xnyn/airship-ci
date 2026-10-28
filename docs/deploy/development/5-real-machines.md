# Step 5 — Real machines

**What this is.** Connecting a cloud account to your local Airship and
running a GitHub Actions job on a machine it launches. This is the first
point where real money is involved — cents, if you follow the guardrails
first.

**Time.** 15 minutes, plus a few minutes per job for the machine to boot.

**Prerequisite.** [Step 4](4-github.md): webhooks arriving, the app
installed on your test repository.

---

## Set the guardrails first

Do these three things before you connect anything. They are the difference
between a 20-cent experiment and a forgotten instance.

| | Do this | Why |
| --- | --- | --- |
| 1 | In `apps/api/.env`: `RUNNER_MAX_LIFETIME=1h` | The deadline **each machine enforces on itself**. It holds even if you close your laptop, kill `make dev` or lose Wi-Fi — the machine powers itself off regardless. The 8h default is right for production and too long for learning |
| 2 | Use a **dedicated cloud account or project** | Test instances are easy to find, the bill is legible, and a mistake cannot touch anything else |
| 3 | After connecting, set **Settings → Most runners at once** to `2` | A hard ceiling, enforced under a row lock. A loop in a workflow cannot outrun it |

Restart `make dev` after editing `.env`.

---

## Pick a cloud

Any one of the four is enough to understand the whole lifecycle.

| Cloud | Credential it needs | Cheapest useful test | Setup guide |
| --- | --- | --- | --- |
| **AWS** | An IAM access key | `t3.medium`, a few cents an hour | [architecture/aws-setup.md](../../architecture/aws-setup.md) |
| Google Cloud | A service account JSON key | `e2-standard-2` | [architecture/gcp-setup.md](../../architecture/gcp-setup.md) |
| Azure | A service principal | `Standard_D2s_v5` | [architecture/azure-setup.md](../../architecture/azure-setup.md) |
| Daytona | An API key | No image to pick, fastest to start | [architecture/daytona-setup.md](../../architecture/daytona-setup.md) |

AWS is the best first choice: it is the most used, it has the one-line
labels that need no config file, and its emulated test suite
([7-tests.md](7-tests.md)) means you can debug most provider problems
without it.

Daytona is the cheapest and quickest if you just want to see the lifecycle
work — sandboxes start in seconds — but it has no object storage, so
`cache: true` is unavailable there and records a `cache.unavailable`
warning.

---

## Connect it (AWS)

Create the IAM user first:
[aws-setup.md, step 1](../../architecture/aws-setup.md#step-1--create-the-iam-user-aws-admin).
In a scratch account, `AmazonEC2FullAccess` is fine; the scoped policy in
that guide is what a shared account should use. Airship never assumes a
role or uses the machine's own identity — it is one long-lived access key,
verified with STS, sealed with AES-GCM, and opened only for the duration of
one API call.

Then, in the dashboard under **Cloud**, in the **AWS** card:
**Connect AWS**.

| Field | Value |
| --- | --- |
| **Access key** | `AKIA…` |
| **Secret key** | the secret |
| **Default region** | `eu-west-1`, or one near you. Runner definitions that name no region launch here |

A bad key is rejected immediately — the connect call verifies it with STS
before storing anything. Once stored it is never shown again, because it is
sealed with `CREDENTIALS_KEY` and only decrypted in memory.

---

## Activate the repository

**Repositories** → activate `runner-test`.

This is deliberate and catches people out: **a config file never launches
anything in a repository nobody activated.** Jobs in inactive repositories
cost no API calls at all.

---

## Run a job

No `airship.yaml` needed for the first one. On AWS, a **one-line label**
describes the machine by itself:

```yaml
# .github/workflows/smoke.yml
name: smoke
on: workflow_dispatch

jobs:
  hello:
    runs-on: airship-aws-t3-medium-ubuntu2404--${{ github.run_id }}
    steps:
      - run: echo "hello from $(uname -a)"
      - run: nproc && free -h
```

The label is `airship-aws-` then the instance type with `.` written as `-`,
then optional `ubuntu2204`/`ubuntu2404` and `spot` tokens. The
`--${{ github.run_id }}` suffix makes the label unique to this run, which
is what makes the runner single-use: the machine takes this job and no
other.

Only labels starting with `airship-` are ever looked up, so other jobs in
the repository cost nothing.

Commit, push, and run it from the **Actions** tab.

### What should happen

| | Where | Within |
| --- | --- | --- |
| The job appears, queued | **Jobs** | seconds |
| A runner appears: `pending` → `launching` | **Runners** | seconds |
| An instance appears, tagged `airship` | AWS console | ~30 s |
| The runner goes `active`; the job starts | both | 1–3 min (boot plus runner registration) |
| The job goes green | GitHub | |
| **The runner disappears and the instance terminates** | **Runners**, AWS console | within one reconcile pass (15 s) of the job finishing |

The last row is the one to watch. Launching is the easy half; the
reconciler cleaning up afterwards is what keeps the bill at cents. Watch it
happen once, so you recognise it when it stops.

### When it does not work

**Events** records why, with the cloud's own error message. That page
first, always.

| Event or symptom | Cause |
| --- | --- |
| Nothing in **Jobs** | The `workflow_job` webhook did not arrive — back to [step 4](4-github.md) |
| Runner stuck `pending` | No cloud connected, or the repository is not activated |
| `capacity.reached` | Your `Most runners at once` ceiling — raise it, or wait |
| A vCPU quota error | A new AWS account's regional limit. Request an increase, or use a smaller type |
| `UnauthorizedOperation` | The IAM policy is missing `ec2:RunInstances` |
| The job runs but is never marked complete | The runner cannot reach GitHub from its subnet |
| `cache.unavailable` | See the caching note below. The job still runs |

---

## A real `airship.yaml`

One-line labels are for smoke tests. Real runners are defined in
`airship.yaml` at the root of the activated repository, which is where
disk, spot, caching, idle pools and the other clouds live:

```yaml
# airship.yaml
runners:
  - name: build
    cloud: aws
    instance_type: [c7i.xlarge, m7i.xlarge]   # tried in order
    machine_image: ubuntu2404
    region: eu-west-1
    labels: [airship-build]
    extra_config:
      cache: true
```

```yaml
    runs-on: airship-build--${{ github.run_id }}
```

The full format, every field and what is refused, is in
[airship-yaml.md](../../airship-yaml.md).

---

## Two things that differ from production

**Caching needs an extra build step.** `extra_config.cache: true` works
through a tunnel, because runner machines reach `PUBLIC_URL` over the
internet — but it also needs the cache-proxy binary embedded in the API,
and `make dev` builds the API with a plain `go build`, which leaves it out.
Run `make cacheproxy` once before `make dev` to include it. Without it,
jobs still run — just without acceleration — and **Events** records
`cache.unavailable`.

**Machines outlive your laptop.** Stop `make dev` mid-job and the job still
finishes: the runner talks to GitHub directly, not to Airship. What stops
without a control plane is the *cleanup*, which is exactly why
`RUNNER_MAX_LIFETIME` exists.

---

## Before you walk away

```bash
# Nothing of Airship's still running?
aws ec2 describe-instances --region eu-west-1 \
  --filters 'Name=tag-key,Values=airship' 'Name=instance-state-name,Values=running,pending' \
  --query 'Reservations[].Instances[].[InstanceId,InstanceType,LaunchTime]' --output table
```

The dashboard's **Runners** page should be empty too. If an instance is
still there with no runner next to it, the reconciler is not running — and
the first thing to check is that `make dev` is up and the database is
reachable.

---

## Checkpoint

- [ ] `RUNNER_MAX_LIFETIME=1h` while you experiment.
- [ ] A cloud connected under **Cloud**, and `Most runners at once` set low.
- [ ] `runner-test` activated.
- [ ] One job green on a machine your laptop launched.
- [ ] **Runners** empty, and no instance left running, after it finished.

Next: [step 6 — making changes](6-making-changes.md).
