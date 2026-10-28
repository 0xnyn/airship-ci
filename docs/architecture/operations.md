# Running the control plane

**What this is.** How to deploy, scale, upgrade and operate Airship's
control plane — whether that is us running it for you or you running it
yourself.

**Audience.** Whoever gets paged. Also customers who want the control plane
inside their own perimeter.

Step-by-step deployment is in [../deploy/](../deploy/README.md):
[development/](../deploy/development/) for a local setup, then
[stage 0](../deploy/stage-0/) for a pilot (Supabase, Render, Vercel) and
[stage 1](../deploy/stage-1/aws.md) on AWS, with Google Cloud, Azure and
one server in [alternatives/](../deploy/alternatives/). The
[production checklist](../deploy/checklist.md) is shared, and
[where every variable's value comes from](../deploy/stage-0/env.md)
complements the Configuration section below.

---

## The deployment

One container and one Postgres database. That is the whole topology.

```
                  GitHub webhooks (443)        runner machines (443)
                         │                   fetch the cache proxy and
                         │                   its hour-long credentials
                         ▼                            │
              ┌──────────────────────────────┐        │
              │  load balancer / ingress     │ ◄──────┘
              └──────────────┬───────────────┘
                             │
         ┌───────────────────┴───────────────────┐
         │                                       │
   ┌─────▼──────┐                        ┌───────▼────┐
   │  api × N   │  each replica runs:    │  api       │
   │            │   · HTTP handlers      │            │
   │            │   · launcher           │            │
   │            │   · reconciler         │            │
   │            │     (leader only)      │            │
   └─────┬──────┘                        └───────┬────┘
         │                                       │
         └──────────────┬────────────────────────┘
                        ▼
              ┌────────────────────┐        ┌──────────────────────┐
              │  Postgres          │        │  dashboard (Next.js) │
              │  the only state    │        │  app.example.com     │
              └────────────────────┘        └──────────────────────┘
```

```bash
docker build -t airship-api apps/api

# once per release, before rolling the replicas
docker run --rm --entrypoint migrate -e DATABASE_URL=... airship-api up

docker run -p 8080:8080 \
  -e DATABASE_URL=... \
  -e PUBLIC_URL=https://api.example.com \
  -e DASH_URL=https://app.example.com \
  -e CREDENTIALS_KEY=... \
  -e GITHUB_CLIENT_ID=... -e GITHUB_CLIENT_SECRET=... \
  -e GITHUB_WEBHOOK_SECRET=... -e GITHUB_PRIVATE_KEY_FILE=/run/secrets/app.pem \
  airship-api
```

The image is `distroless/static`, runs as nonroot, and embeds the cache
proxy for `linux/amd64` and `linux/arm64` — runner machines download it from
the API. A plain `go build` of the API alone leaves the proxy out, and
cache-enabled runners then run their jobs without acceleration (recorded on
the Events page, never a failed job).

---

## Configuration

Everything is environment variables. Nothing is read from a config file in
production.

### Required

| Variable | Notes |
| --- | --- |
| `DATABASE_URL` | Postgres connection string |
| `CREDENTIALS_KEY` | Base64 32-byte key that seals customers' cloud credentials. `openssl rand -base64 32`. **Losing it loses every connected cloud.** A comma-separated list enables rotation (new key first) |
| `PUBLIC_URL` | Where GitHub and runner machines reach the API. **Must be `https` outside development** — enforced at startup |
| `GITHUB_CLIENT_ID`, `GITHUB_WEBHOOK_SECRET`, and `GITHUB_PRIVATE_KEY` or `GITHUB_PRIVATE_KEY_FILE` | Required outside development. A single-line `GITHUB_PRIVATE_KEY` may carry the PEM with literal `\n` |

### Commonly set

| Variable | Default | Notes |
| --- | --- | --- |
| `APP_ENV` | `development` | Set to `production`. The image sets it already |
| `PORT` | `8080` | |
| `DASH_URL` | `http://localhost:3000` | The dashboard; where sign-in returns to |
| `CORS_ORIGINS` | `http://localhost:3000` | Comma-separated browser origins allowed to call the API |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `GITHUB_APP_SLUG` | — | Names the app in its install URL |
| `GITHUB_CLIENT_SECRET` | — | Needed for dashboard sign-in (not for launching) |
| `GITHUB_API_URL`, `GITHUB_WEB_URL` | `api.github.com`, `github.com` | Point at GitHub Enterprise |

### Tuning

| Variable | Default | What it changes |
| --- | --- | --- |
| `RUNNER_VERSION` | `2.338.2` | The `actions/runner` release machines install. **GitHub stops sending jobs to runners more than 30 days behind a release**, so this needs bumping with releases |
| `RUNNER_MAX_LIFETIME` | `8h` | The hard deadline a machine enforces on itself. The one bound that can cut a legitimate job short — GitHub's default job timeout is 6h, so the default leaves room, but a workflow with a longer `timeout-minutes` needs this raised |
| `RUNNER_IDLE_TIMEOUT` | `10m` | How long a launched runner waits for a job before it is reaped |
| `RECONCILE_INTERVAL` | `15s` | How often the leader runs its twelve steps |
| `LAUNCH_INTERVAL` | `2s` | Launcher polling floor; a webhook wakes it immediately anyway |

### Refused in production

| Variable | Behaviour |
| --- | --- |
| `DEV_LOGIN` | Accepted only with `APP_ENV=development`. Startup fails otherwise |
| `CREDENTIALS_KEY` set to the published example key | Startup fails, naming `.env.example` |
| `PUBLIC_URL` on plain HTTP | Startup fails |
| `AWS_ENDPOINT`, `GCP_ENDPOINT`, `AZURE_ENDPOINT`, `DAYTONA_ENDPOINT` | Test-only; point the providers at moto or a fake API |

These are startup failures on purpose: a misconfigured control plane should
not come up at all rather than come up insecurely.

### The dashboard

`apps/dash` is a Next.js app. `NEXT_PUBLIC_API_URL` is **inlined when `next
build` runs**, so it must be set in the build environment — a production
build with the `localhost` default fails deliberately. Serve the dashboard
and the API on the same site (`app.example.com` and `api.example.com`) so
the session cookie is sent.

---

## Networking requirements

| Direction | From | To | Why |
| --- | --- | --- | --- |
| inbound | GitHub | `PUBLIC_URL/v1/github/webhook` | Job events. Lose this and jobs are only picked up by the stale-job sweep |
| inbound | browsers | `PUBLIC_URL`, `DASH_URL` | Dashboard and API |
| inbound | **runner machines** | `PUBLIC_URL` | Only for `cache: true`: the proxy binary and its hour-long credentials |
| outbound | control plane | `api.github.com` | JIT configs, job and runner lookups, file reads, redelivery |
| outbound | control plane | AWS EC2/S3/STS endpoints, `compute.googleapis.com`, `oauth2.googleapis.com`, `management.azure.com` and `login.microsoftonline.com`, `app.daytona.io` and `proxy.app.daytona.io` (or your Daytona API URL and the toolbox proxy it reports) | Launching, listing, terminating, cache grants; on Daytona also writing and starting the boot script inside each sandbox |
| outbound | control plane | Postgres | — |

The third row is the one people miss: **if the control plane is not
reachable from your runner subnets, caching cannot work.** Everything else
continues to function; the Events page records `cache.unavailable`.

---

## High availability

| Concern | Behaviour |
| --- | --- |
| **Several replicas** | Supported and recommended. Launchers run on every replica and claim work with row locks and `FOR UPDATE SKIP LOCKED`, within `max_runners` counted under a lock on the installation row — two replicas can never both take the last slot |
| **The reconciler** | One leader per pass, elected with `pg_try_advisory_lock`. The steps are individually safe to run concurrently; the lock only avoids duplicate GitHub calls |
| **Losing the leader** | Costs one pass. The next replica to tick takes the lock |
| **Losing a replica mid-launch** | The runner's 5-minute lease (renewed only by the launcher that holds it) expires, the job is retried, and if the machine did launch, the inventory scan revives the original runner. The duplicate idles out after 10 minutes |
| **Losing every replica** | No new runners. Running jobs finish — the runner talks to GitHub, not to us. **Every machine still powers itself off at its own deadline**, so nothing runs forever |
| **Rolling deploys** | Requests in flight finish during a 30-second drain; background loops stop at once. Launches already in flight are covered by the lease |

---

## The background loops

| Loop | Interval | Leader-only | Does |
| --- | --- | --- | --- |
| launcher | 2s, plus woken by a webhook | no | Claims pending runners under capacity; registers with GitHub; calls the cloud, renewing the runner's lease while the call runs. Concurrency 8, batch 50 |
| reconciler | 15s | **yes** | The twelve steps below |
| webhook redeliverer | 2m | one replica | GitHub never retries a failed delivery; this scans the app's delivery log and asks for redelivery with backoff |
| role refresher | 1m | no | Re-checks dashboard roles older than an hour, 100 at a time |

The reconciler's steps, in order, every pass:

```
1  expire leases                  7  refresh stale jobs
2  scan instances (inventory)     8  refresh configs
3  terminate finished runners     9  refresh organization configs
4  reap idle runners             10  balance pools
5  reap lost busy runners        11  expire sessions
6  deregister runners            12  expire events
```

Each step collects its own error and the pass continues, so one failing
cloud credential does not stop idle pools from balancing. Every call inside
them is bounded: because the steps run in sequence, one hung API call would
otherwise stall every runner's lifecycle.

Timings worth knowing when reading logs:

| Constant | Default |
| --- | --- |
| Launch lease | 5m, renewed every half lease while the cloud call runs |
| One launch attempt | at most 15m (an Azure Windows machine plus its Run Command can take minutes); cut off 30s before the last renewed lease expires |
| Instance grace before "gone" | 2m |
| Busy runner written off after its instance vanished | 10m |
| Deregister from GitHub after finishing | 2m |
| Repeat a termination the cloud still lists | 5m |
| Ask GitHub about a job queued without news | 10m, max 2 per installation per pass |
| Failed launches per job | 3, or one per fallback attempt |
| Event retention | 30 days |
| Cache grant TTL / bucket retention | 1h / 7 days |
| Azure orphan sweep (interfaces and addresses no VM uses) | after each inventory pass, at most every 5m per scope, for parts older than 15m |

---

## GitHub rate limits

An installation gets 5,000 REST calls an hour. The structural protections:

- The stale-job sweep asks about at most **two jobs per installation per
  pass**, so one installation's backlog costs it a few hundred calls an hour
  and cannot delay another installation's recovery.
- Launch concurrency is capped at **8**, because GitHub's secondary limits
  punish bursts of POSTs.
- Registrations are deleted lazily (2 minutes after finishing) rather than
  immediately, which also avoids a second call for a runner that turns out
  to be busy.
- Webhooks, not polling, are the primary signal. The sweeps exist for lost
  deliveries, not as the main path.

If an installation is being throttled, the symptom is runners sitting in
`pending` with retry warnings in the logs and a visible delay on the Jobs
page — not failed jobs.

---

## Database

| Operation | How |
| --- | --- |
| Migrate | `migrate up` (goose), once per release, before rolling replicas. Every migration has a `Down` section |
| Backup | Ordinary Postgres backups. The database holds all state, including sealed credentials |
| Restore | Restore Postgres, then confirm `CREDENTIALS_KEY` matches the one that sealed the credentials — otherwise every cloud needs reconnecting |
| Size | Small. Rows per runner and per job, plus events pruned at 30 days. Thousands of jobs a day is megabytes |
| Schema | 13 tables: `users`, `sessions`, `installations`, `memberships`, `cloud_credentials`, `api_tokens`, `repositories`, `idle_pools`, `runners`, `jobs`, `events`, `webhook_scan`, `failed_deliveries` |

**If you lose Postgres entirely:** running jobs finish, every machine
destroys itself at its deadline, and you are left with instances to sweep by
tag (`airship:installation` on AWS, `airship-installation` on GCP, Azure and
Daytona) if any were mid-life. On Azure everything is in the `airship-runners`
resource group.
Reinstalling and reconnecting rebuilds everything else from GitHub. This is
the one failure the design does not absorb, which is why it is the one thing
to back up.

---

## Key rotation

**`CREDENTIALS_KEY`** — zero-downtime, two deploys:

1. Set `CREDENTIALS_KEY="<new>,<old>"` (new key first) and deploy. New seals
   use the new key; existing blobs still open with the old one.
2. Reconnect each cloud credential (dashboard or developer API). Each
   reconnect re-seals under the new key.
3. Set `CREDENTIALS_KEY="<new>"` and deploy. The old key is gone.

**A customer's cloud key** — rotating within the same account or project is
always allowed, any time: connect the new key over the old one. Swapping to a
*different* account, or disconnecting, is refused while runners launched with
the old credential are alive, since without it their machines could not be
terminated. Wait for them to finish.

**The GitHub App private key** — add the new key in GitHub, deploy with
`GITHUB_PRIVATE_KEY_FILE` pointing at it, then delete the old one in GitHub.

---

## Creating the GitHub App

Create it at **GitHub → Settings → Developer settings → GitHub Apps**:

| Setting | Value |
| --- | --- |
| Callback URL | `{PUBLIC_URL}/v1/auth/github/callback` |
| Webhook URL | `{PUBLIC_URL}/v1/github/webhook`, with a random secret |
| Repository permissions | Administration **read & write**, Actions **read**, Contents **read** (write only if the access-control API should open PRs on `.airship`), Pull requests **write** (same), Metadata **read** |
| Organization permissions | Members **read**, Self-hosted runners **read & write** |
| Subscribe to events | **Workflow job**, **Push**, **Repository** |

For local development, expose port 8080 with a tunnel
(`cloudflared tunnel --url localhost:8080`) and use that as `PUBLIC_URL`.

---

## Observability

| Signal | Where |
| --- | --- |
| Health | `GET /healthz` → `{status, database, version}`. `degraded` when Postgres does not answer within 2 seconds. Use it as the load balancer's check |
| Logs | Structured `slog`, **JSON outside development**, at `LOG_LEVEL`. Every request logged; panics recovered and logged. No credential value is ever logged |
| Per-installation activity | The dashboard's **Events** page: launch failures with the cloud's own message, degraded caches, config errors, credential problems. 30-day retention. This is the first place to look |
| Job and runner history | **Jobs** and **Runners** pages, with finish reasons |
| Usage | **Analytics** page |
| API reference | `/docs` on the API, generated from the same OpenAPI document as the TypeScript client |

Log lines worth alerting on: `reconcile` errors that persist across passes,
`runner lease expired` in volume (launches are not completing), repeated
`launch` errors for one installation (usually a credential or a quota), and
`could not register runner` (GitHub rate limiting or a permissions change).

---

## Incident runbook

| Symptom | First checks | Likely cause |
| --- | --- | --- |
| **No jobs appear at all** | GitHub App → Advanced → recent deliveries; `/healthz` | Webhook URL or secret wrong, or the control plane is unreachable from GitHub. The redeliverer recovers lost deliveries once reachability is restored |
| **Jobs appear but nothing launches** | Cloud page (**Credentials rejected** once a check finds the cloud refuses the key), then the Events page | A credential revoked or expired since it was connected, `max_runners` reached, or a cloud quota |
| **Everything is slow to start** | Reconciler error logs; GitHub rate limit headers | Rate limiting, or a reconcile pass failing before the launcher's wake path |
| **Runners stuck in `pending`** | `max_runners` versus live runners; launcher logs | Capacity cap, or every launch failing and retrying |
| **Runners finish `idle` repeatedly** | The job's labels versus the runner's | A `--${{ github.run_id }}` suffix mismatch, or jobs cancelled during boot |
| **Runners finish `launch_unconfirmed` repeatedly** | Network path to the cloud API | Lost answers. Handled correctly, but it points at a real network problem |
| **Instances in the cloud with no runner row** | Instance tags; the inventory scan's logs | Normally terminated within a pass. Persisting means the credential cannot list or terminate — check the IAM policy (on Azure, the role must be on the subscription, since the list is subscription-wide) |
| **Stopped VMs or powered-off instances linger (GCP, Azure)** | Events page; the inventory scan's logs | They are deleted by the scan, not by the cloud. Lingering means the scan cannot list or delete in that region — usually an expired Azure client secret or a narrowed role |
| **Orphaned Azure interfaces or addresses** | `sweep orphaned resources` warnings | The sweep removes them after 15 minutes. Persisting means the role lacks `networkInterfaces/delete` or `publicIPAddresses/delete` |
| **Caching silently off** | Events page for `cache.unavailable` | A missing S3 permission, a control plane built without the proxy binary, or runners that cannot reach `PUBLIC_URL` |
| **Dashboard sign-in loops** | `DASH_URL`, `CORS_ORIGINS`, cookie domain | The API and dashboard are not on the same site, so the session cookie is not sent |
| **Reconciler not running** | Whether any replica holds the advisory lock | A replica holding the lock while wedged. Restart it; the next replica takes over |

**Emergency stop for one installation:** set its `max_runners` to 1, or
deactivate its repositories. Running jobs finish; nothing new launches.

**Emergency stop for everything:** scale the API to zero. Jobs in flight
finish, and every machine destroys itself at its own deadline. Nothing is
left running indefinitely, with or without you.

---

## Capacity planning

The control plane is not the bottleneck; your cloud quotas are. For scale:

| Dimension | Notes |
| --- | --- |
| Replicas | Two for availability. The launcher batches 50 runners per pass with concurrency 8, so one replica handles a lot of churn |
| Postgres | A small managed instance, **version 17 or newer, 18 recommended** (on 17 the first migration defines the `uuidv7()` that 18 has built in). Connection pooling via pgx; indexes exist for every lifecycle query path |
| Runners per installation | `max_runners`, 1–10,000. Keep it below your cloud quotas so the ceiling is a dashboard message, not a wall of cloud errors |
| Jobs per hour | Bounded in practice by GitHub's 5,000 calls/hour per installation. Each runner costs a handful of calls |
| Regions and clouds | Free. Each definition names its own; the inventory scan lists per (credential, region) |
