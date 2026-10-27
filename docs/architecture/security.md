# Security and trust model

**What this is.** What Airship holds, what it can do, what a job can reach,
and — stated plainly, in their own section — the exposures that come with
the model rather than being bugs in it.

**Audience.** Security reviewers. This document is written to be read
adversarially.

---

## Trust boundaries

```
┌─ GitHub ──────────────┐   ┌─ Airship control plane ─┐   ┌─ your cloud account ──────┐
│                       │   │                         │   │                           │
│ your source           │   │ job metadata            │   │ your runner VMs           │
│ your secrets          │   │ sealed cloud credential │   │ your cache bucket         │
│ your artifacts        │   │ sessions (hashed)       │   │ your network              │
│ your job logs         │   │ API tokens (hashed)     │   │                           │
│                       │   │                         │   │                           │
│ Airship can:          │   │ A job cannot reach      │   │ Airship can:              │
│  read .cirun.yml      │   │ this. There is no       │   │  launch a machine         │
│  read job/runner      │   │ inbound path from a     │   │  list machines it tagged  │
│    metadata           │   │ runner except the two   │   │  destroy machines it      │
│  register/remove      │   │ runner endpoints, and   │   │    tagged                 │
│    runners            │   │ those answer only to a  │   │  create one bucket and    │
│  open a PR on .cirun  │   │ live runner's own token │   │    one security group     │
│    (if you grant it)  │   │                         │   │                           │
└───────────────────────┘   └─────────────────────────┘   └───────────────────────────┘
       ▲                             │                            ▲
       │  the runner talks to        │  one API call at a time,   │
       │  GitHub directly, with      │  credential opened and     │
       │  a single-use registration  │  discarded per call        │
       └─────────────────────────────┴────────────────────────────┘
```

**What never crosses a boundary:** your source code, your Actions secrets,
your artifacts and your job logs never reach the control plane. Your cloud
credential never reaches a runner machine. Airship holds no SSH key for your
machines, and there is no inbound network path to them.

---

## What Airship stores

| Data | Where | Protection |
| --- | --- | --- |
| Cloud credential (AWS access key, GCP service account key, Daytona API key) | `cloud_credentials.secret` | Verified before storage, sealed with **AES-256-GCM** under `CREDENTIALS_KEY`, the blob **bound to its row** so it cannot be moved and opened elsewhere. Never returned by any endpoint |
| Dashboard session | `sessions` | Only a **SHA-256 of the cookie value** is stored. The cookie itself is HttpOnly, `Secure` outside development, `SameSite=Lax` |
| Developer API token (`asa_`) | `api_tokens` | Only a **SHA-256**, plus a display hint. Shown once at creation |
| Runner cache token (`asr_`) | `runners.cache_token_hash` | Only a SHA-256. Dead the moment the runner finishes |
| GitHub identity | `users` | GitHub ID, login, display name, avatar URL |
| Installation, repositories, runners, jobs | tables of the same names | Metadata only: IDs, labels, states, machine shapes, timestamps, reasons |
| Activity events | `events` | What happened and why, retained **30 days** |

| Data Airship never stores |
| --- |
| Source code, artifacts, build output |
| Actions secrets or variables |
| Job logs or step output |
| Any cloud credential in plaintext, at rest or in a log |
| Cache contents — those live in *your* bucket, in *your* account |

Validation errors are shaped so they cannot echo the value that failed: the
API's error details carry a location and a message only. Upstream failures
are reported without GitHub's or the database's raw error text.

---

## The cloud credential

This is the sharpest edge of the design, so it gets the most rules.

| Rule | Detail |
| --- | --- |
| **Verified before stored** | AWS with `sts:GetCallerIdentity`, GCP with a Compute `projects.get`, Daytona by reading the key's own record (it must belong to an organization, be unexpired and carry the sandbox write and delete permissions) and the organization's regions. A key that does not work is never written |
| **Sealed at rest** | AES-256-GCM under `CREDENTIALS_KEY`, a base64 32-byte key held by the control plane's environment, not in the database. The example key shipped for local development is explicitly refused in production |
| **Bound to its row** | The sealed blob's additional authenticated data ties it to its installation and cloud. Lifting the row into another installation's record makes it undecryptable |
| **Opened per call** | Decrypted, handed to the provider for one API call, discarded. It is never written to a log, an error, a metric or a response |
| **Never echoed** | No endpoint returns a secret. The dashboard shows the account ID and principal — the public half the cloud told us at verification (on Daytona the organization ID and the key's name, with the key itself masked to its first and last three characters) |
| **Rotation** | Replacing a key *within the same account or project* is always allowed. `CREDENTIALS_KEY` itself rotates by listing the new key first and the old one after a comma, redeploying, reconnecting each cloud, then dropping the old key |
| **Deletion is guarded** | Disconnecting a cloud — or replacing its key with one for a *different* account — is **refused while runners launched with it are alive**, because without that key their machines could not be terminated. The check and the delete take the installation lock, so no runner can slip in between |

What the credential can do is your choice, and it should be the minimum:
[aws-setup.md](aws-setup.md#step-1--create-the-iam-user-aws-admin) and
[gcp-setup.md](gcp-setup.md#step-3--grant-roles-gcp-admin) give
least-privilege policies, and [daytona-setup.md](daytona-setup.md#step-2--create-an-api-key-daytona-admin)
the two permissions a Daytona key needs. On AWS the terminate permission can be conditioned
on Airship's own tag, so the credential **cannot destroy anything Airship did
not create**.

---

## Job isolation

| Property | How |
| --- | --- |
| **One job per machine** | A runner registers with a single-use JIT configuration and exits after one job. No machine is ever reused, so no job can read another's checkout, caches on disk, environment or in-memory state |
| **The machine destroys itself** | Every exit path of the boot script powers the machine off: the job finishing, the runner crashing, the script failing, the hard deadline. On AWS `InstanceInitiatedShutdownBehavior=terminate` turns that into a delete; on GCP the reconciler deletes the `TERMINATED` machine, disks included; on Daytona the script stops the sandbox's daemon and the sandbox, created to delete itself on stop, is gone within seconds |
| **A deadline that does not depend on us** | `shutdown -P +<max lifetime>` is scheduled before anything else runs. On Daytona the runner runs under `timeout` with the same deadline and the sandbox carries a TTL fifteen minutes past it, so a sandbox whose script never ran is removed by Daytona itself. A control plane that disappears entirely leaves no machine running forever |
| **One boot, one registration** | The startup script writes a marker; a rebooted machine powers off rather than registering again (GCP and EC2Launch both re-run startup scripts). On Daytona the script takes a lock directory before anything else, so a repeated launch cannot start a second runner in the same sandbox |
| **No inbound access** | AWS: a security group with **no ingress rules**. GCP: no firewall rules added at all, plus a `airship-runner` network tag for your own policy. No SSH key is installed, by Airship or anyone else. Daytona: the toolbox API is the way in, and it is open to anyone holding one of the organization's API keys, so the organization's membership is the boundary (see exposure 8) |
| **Metadata service locked down** | AWS: IMDSv2 required with **hop limit 1**, so a process inside a container cannot reach instance metadata |
| **No cloud identity by default** | GCP machines get **no service account** unless a definition asks for one. AWS machines get no instance profile unless a definition asks for one. Daytona sandboxes never hold the organization's API key |
| **Encrypted disks** | AWS root volumes are always encrypted. GCP disks are encrypted by Google by default |

---

## The GitHub registration

| Property | Detail |
| --- | --- |
| Single-use | A JIT configuration, consumed by one registration, good for one job |
| Scoped | To the repository — or, for organization idle pools, to a runner group visible only to the repositories that pool's `.access.yml` policies allow |
| Minted before the machine | And its GitHub runner ID stored first, so nothing can boot without a stored registration to clean up |
| Removed after | Two minutes after the runner finishes, and **only if GitHub says the runner is not busy**. A busy runner means the machine exists after all, and the runner is revived rather than orphaned |
| Re-checked before launch | Registration takes a round trip; if the job was cancelled or started elsewhere in that window, no machine is created and the registration is removed at once |

---

## Who may spend your money

| Actor | Can | Bound by |
| --- | --- | --- |
| A GitHub org **owner** | Connect clouds, activate repositories, set `max_runners`, create API tokens | Being an owner. Roles are re-checked with GitHub hourly, so removing someone revokes access within the hour |
| An org **member** | Read the dashboard | Read-only |
| Anyone who can **push to an activated repository** | Choose machine types, regions and disk sizes for that branch's jobs | `max_runners`, the 8-hour machine lifetime, the attempt cap. This is the same authority they already have over what a workflow *does* |
| A **fork pull request** on a public repository | Nothing, unless that repository was activated **with public access allowed** — a separate, explicit choice | `.access.yml`, `max_runners`, and your judgement about what else is on those runners |
| A **holder of an `asa_` token** | Everything the developer API offers for one installation | The token's installation scope. Tokens are revocable in the dashboard |
| A **live runner's `asr_` token** | Fetch cache credentials for that installation's bucket, and nothing else | Dies when the runner finishes |

`max_runners` (default 50) is the number to set deliberately. It is enforced
under a row lock on the installation, so launchers on several replicas cannot
both take the last slot.

---

## Control plane surfaces

| Surface | Authentication | Hardening |
| --- | --- | --- |
| Dashboard API | GitHub OAuth session cookie | HttpOnly, `Secure`, `SameSite=Lax`; an **Origin check on every write**; CORS restricted to configured origins; roles re-checked hourly; read-only while GitHub has the app suspended |
| Developer API `/api/v1` | `asa_` bearer token, stored hashed | **Accepts no session cookie**, so a browser cannot be tricked into calling it |
| Runner API | `asr_` bearer token, stored hashed | Answers only for a live cache-enabled runner; returns credentials for that installation's bucket only |
| Webhook | HMAC signature over the body | Unsigned deliveries are rejected before any work is done |
| Everything | — | Security headers, panic recovery, request logging without secrets, bounded request timeouts |

Sign-in is GitHub OAuth only. There is no password to leak and no local
account to brute-force. The development-only sign-in shortcut is refused
outside `APP_ENV=development`, and `PUBLIC_URL` must be `https` in
production — both enforced at startup, not by convention.

---

## The boot script

The script runs as root (or SYSTEM). Anything interpolated into it would be
remote code execution on your machine if it could break its quoting, so
nothing is interpolated without validation:

| Value | Check |
| --- | --- |
| Runner version | `^[0-9]+\.[0-9]+\.[0-9]+$` |
| JIT configuration | base64 alphabet only |
| Control plane URL | strict URL pattern |
| Cache token | `^[A-Za-z0-9_-]{8,128}$` |
| Windows path / user / password | strict patterns; no spaces, no quotes |
| Job environment variables | Names validated; values containing NUL, CR or LF refused; the whole file travels **base64** and is decoded on the machine, so nothing in it is ever shell or PowerShell |
| Reserved names | Anything starting with `GITHUB_`, `RUNNER_` or `ACTIONS_` is refused — a config file must not be able to change how the runner talks to GitHub |

Up to 50 environment variables per definition. They are **not secret**: they
are visible in the launch metadata and to every job on the runner. Secrets
belong in GitHub Actions secrets, which never pass through Airship.

---

## Cache security

| Property | Detail |
| --- | --- |
| **Your key never leaves the control plane** | The machine gets a one-off `asr_` token and trades it for an STS **federation token**: one hour, refreshed before expiry, narrowed by an inline policy to that one bucket |
| **A hard ceiling** | A federation token can never exceed the issuing IAM user's own permissions. The S3 statements in your policy are the maximum a runner can do, whatever happens on the machine |
| **Bucket ownership is proven** | Every bucket call names the expected owner account, so a bucket of the same name in *another* account is refused rather than used |
| **Private by construction** | Public access blocked on every check (not just at creation), SSE-S3, objects expired after 7 days, incomplete multipart uploads aborted after a day |
| **The interception CA is local and short-lived** | Generated on the machine, valid one day, trusted only there. It never exists anywhere else, and the machine is destroyed after one job |
| **Narrow interception** | TLS is terminated for exactly one host (`results-receiver.actions.githubusercontent.com`) and only its `CacheService` calls are answered locally. Everything else on that host is forwarded to GitHub untouched; every other host is tunnelled without inspection |

---

## Known exposures, stated plainly

These follow from the model. None is a surprise; each has a mitigation, and
some of them are a deliberate price for Cirun parity.

**1. The JIT registration travels in instance metadata.**
On AWS, anyone in your account who can call `ec2:DescribeInstanceAttribute`
can read the user data until the runner registers. On GCP, anyone with
`compute.instances.get` can read the metadata the same way. The window is
seconds, the configuration is single-use, and using it would steal one job
rather than grant access to anything. `provision_via: pull_from_api` avoids
it entirely, at the cost of putting an API token in your image. On Daytona
the configuration is not in metadata: the control plane writes the boot
script into the sandbox's `/tmp` over the toolbox API, where it stays for
the sandbox's life, readable by anyone who can reach that sandbox's toolbox
(exposure 8); it is spent the moment the runner registers.

**2. The cache bucket is one namespace per organization.**
As on Cirun, there is no per-branch or per-repository isolation: a pull
request's job can write an entry a main-branch job later restores, and one
repository can restore another's keys. **Keep public repositories with fork
pull requests off cache runners**, or make their keys unguessable. The
control plane enforces the bucket, not the key space.

**3. Anyone who can push to an activated repository chooses machines.**
That includes an expensive one. It is the same authority they have over
workflow content, and `max_runners` is the bound. If that is not acceptable
for a repository, do not activate it.

**4. An instance profile or service account is available to every step.**
If a definition attaches one, every step of every job on that runner can use
it — including a fork pull request's code on a public repository activated
for public access. Treat it exactly as you would a repository secret.

**5. Any image the account can launch is allowed.**
As on Cirun, there is no image allowlist. A `.cirun.yml` can name any AMI or
Compute Engine image your credential can boot, or on Daytona any container
image Daytona can pull (with a tag or digest; `latest` is refused so a
definition says what it runs) or any snapshot of the organization.
Architecture mismatches are refused; trustworthiness is not something
Airship can judge.

**6. Two installations can share one cloud account.**
Isolation then rests on the installation tag/label: listing filters on it and
terminating refuses an instance without it. It holds, but a dedicated account
or project per installation is the stronger boundary and what we recommend.
On Daytona this matters more, because of exposure 8: use one organization
per installation.

**7. Losing `CREDENTIALS_KEY` loses every connected cloud.**
By design — it is the only thing that can open the sealed credentials.
Recovery is reconnecting each cloud. Back it up as you would a root key, and
rotate it with the two-key procedure rather than by replacement.

**8. A Daytona sandbox is reachable by everyone in the organization.**
Daytona's toolbox API lets any holder of an organization API key run a
command in any of the organization's sandboxes, and the sandbox is a
privileged container so that a Docker daemon can run inside it. Airship uses
that API for exactly two calls per launch (write the script, start it) and
never again, but it cannot close it: a member of your Daytona organization
can read a running job's checkout, environment and the single-use
registration in `/tmp`. Treat the organization's membership and its API keys
as you treat the people who can read your repository's Actions secrets, and
give Airship its own organization if that set differs.

**8. Postgres is the single point of truth.**
Lose it and you lose the mapping from runners to machines. Machines still
destroy themselves at their deadlines, so nothing runs forever, but you may
be left with instances to clean up by tag. [operations.md](operations.md)
covers backup.

---

## Tenancy

The tenant is the GitHub App installation — one organization or user
account.

- Every database query is scoped by installation ID.
- Every instance carries the installation as a tag (`airship:installation`)
  or label (`airship-installation`); every list filters on it.
- Every terminate **reads the instance first** and refuses when the tag is
  not the installation's, so an instance ID recorded wrongly — or reused by
  the cloud for someone else's machine — can never destroy the wrong thing.
- Cache buckets are named from a hash of the installation ID and
  owner-checked on every call.
- `asa_` tokens are scoped to one installation; `asr_` tokens to one runner.

---

## What we can tell you, and what we cannot

Straight answers for a security review:

**Where is my data?** Job metadata in the control plane's Postgres. Job
*content* — code, secrets, logs, artifacts, caches — stays in GitHub and in
your cloud account, in the region your `.cirun.yml` names.

**Who at Airship can see it?** Whoever administers the control plane
deployment, as with any hosted service. If that is not acceptable, run the
control plane yourself: one container plus Postgres
([operations.md](operations.md)). The same code, your infrastructure, your
`CREDENTIALS_KEY`.

**Do you hold standing access to my cloud?** Yes — a credential you scope
and can revoke at any time, held sealed and opened per call. The
least-privilege policies in the setup documents are the floor it can
operate on. On AWS, you can condition its terminate permission so it can
only ever destroy Airship's own instances.

**Can you read my repository?** Only what the GitHub App's permissions
allow: `.cirun.yml`, `.cirun.global.yml`, `.access.yml`, and job and runner
metadata. Contents is a read permission; write and pull-request permissions
are optional and only used by the access-control API when you ask it to open
a PR against your `.cirun` repository.

**What about certifications?** We do not claim any certification in this
document. Ask us directly and we will tell you exactly where we are rather
than point at a badge.

**What has actually been tested?** The security-relevant behaviours — the
ownership refusal on terminate, the credential binding, lease and revival
logic, the tag/label scoping, boot script validation — have tests, and the
cache path has run end to end with a real runner, a real GitHub job and an
S3 emulator. What has **not** run is a launch on real EC2 in CI, a Windows
machine, and anything at all against Google; Daytona has run three real
jobs through the real provider (image, snapshot, volume), with GPU and
allow-list requests sent and refused by the organization's tier and
credits. The status notes in
[aws.md](aws.md), [gcp.md](gcp.md) and [daytona.md](daytona.md) say so in detail, and we would rather
you heard it here than found it later.
