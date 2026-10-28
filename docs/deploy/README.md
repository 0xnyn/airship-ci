# Deploying Airship to production

**What this is.** How to host Airship's control plane (the API and the
dashboard) for real use, with a step-by-step guide per platform and the
checklist every deployment has to pass, whichever platform it is on.

**Audience.** Whoever deploys and operates Airship. Running it day to day,
including alerts, scaling and the incident runbook, is in
[architecture/operations.md](../architecture/operations.md).

**Two different clouds.** These guides are about where **Airship itself**
runs. Where **runner machines** run is the customer's choice, made by
connecting a credential in the dashboard; see the setup guides for
[AWS](../architecture/aws-setup.md), [Google Cloud](../architecture/gcp-setup.md),
[Azure](../architecture/azure-setup.md) and [Daytona](../architecture/daytona-setup.md).
The two are independent. A control plane on Cloud Run can launch EC2
instances, and the reverse. The control plane needs **no cloud
permissions of its own**: it launches with the credentials customers
connect, never with the identity of the platform it runs on.

---

## Pick a guide

| Platform | Guide | API runs on | Database | Good for |
| --- | --- | --- | --- | --- |
| AWS | [aws.md](aws.md) | ECS Fargate behind an ALB | RDS for PostgreSQL | Teams already on AWS; the most control over networking |
| Google Cloud | [gcp.md](gcp.md) | Cloud Run | Cloud SQL for PostgreSQL | The least to operate |
| Azure | [azure.md](azure.md) | Container Apps | Azure Database for PostgreSQL flexible server | Teams already on Azure |
| One server | [server.md](server.md) | Docker Compose with Caddy | Postgres in a container | Small teams, trials, on-premises |

**PlanetScale instead of the cloud's own Postgres?** Any of the guides above
works with it. Follow [planetscale.md](planetscale.md) for the database and
skip the guide's database step. Connect on port 5432, never PgBouncer's
6432.

Daytona is a place to run runners, not to host services, so it has no guide
here. Daytona runners work from a control plane on any of the platforms
above.

---

## What gets deployed

```
                     ┌──────────── your domain ─────────────┐
 browsers ──────────►│ app.example.com     api.example.com  │◄─── GitHub webhooks
                     └───────┬──────────────────┬───────────┘◄─── runner machines (cache)
                             │                  │
                     ┌───────▼──────┐   ┌───────▼──────┐
                     │ dashboard    │   │ api × 2+     │──► api.github.com
                     │ apps/web     │   │ apps/api     │──► customers' clouds
                     │ :3000        │   │ :8080        │
                     └──────────────┘   └───────┬──────┘
                                                │
                                        ┌───────▼──────┐
                                        │ Postgres     │  the only state
                                        └──────────────┘
```

| Piece | Image | Notes |
| --- | --- | --- |
| API | `apps/api/Dockerfile` (context `apps/api`) | distroless, nonroot, `:8080`. Holds `/usr/local/bin/api` (the default entrypoint) and `/usr/local/bin/migrate`. Embeds the cache proxy for `linux/amd64` and `linux/arm64` |
| Migrations | Same image, entrypoint `/usr/local/bin/migrate`, argument `up` | Run once per release, **before** rolling out the API. Idempotent |
| Dashboard | `apps/web/Dockerfile` (context: repository root) | Next.js standalone server, nonroot, `:3000`. `NEXT_PUBLIC_API_URL` is a **build argument**, baked in at build time |
| Postgres | **18 or newer**, managed | Required: the schema uses `uuidv7()`, which Postgres added in 18. Migrations fail on 17 and older |

Build both images:

```bash
VERSION=$(git rev-parse --short HEAD)
docker build -t airship-api:$VERSION --build-arg VERSION=$VERSION apps/api
docker build -t airship-web:$VERSION -f apps/web/Dockerfile \
  --build-arg NEXT_PUBLIC_API_URL=https://api.example.com .
```

Most platforms run on `linux/amd64`. On an Apple Silicon machine add
`--platform linux/amd64` to both builds, or build in CI.

The variables each piece takes are in
[`deploy/production/api.env.example`](../../deploy/production/api.env.example)
and [`deploy/production/web.env.example`](../../deploy/production/web.env.example).
The full reference, with defaults, is in
[operations.md, "Configuration"](../architecture/operations.md#configuration).

---

## Before you start, on any platform

### 1. Two hostnames on one domain

Pick two names under a domain you control, for example `api.example.com`
and `app.example.com`. They must share a registrable domain: the session
cookie is set by the API and is `SameSite=Lax`, so the browser only sends it
from a dashboard on the same site.

The platform's default hostnames (`*.run.app`, `*.azurecontainerapps.io`,
`*.elb.amazonaws.com`) do **not** work for this, even for a trial. Each
service gets its own site under those, so sign-in loops back to the sign-in
page. Custom domains are a requirement, not a finishing touch.

### 2. The credentials key

```bash
openssl rand -base64 32
```

This becomes `CREDENTIALS_KEY`. It seals every customer's cloud credential
in the database. Store it in the platform's secret store, **and** keep a
copy somewhere that is not the same database or account, such as your
password manager. A database backup without this key cannot open a single
credential. Losing it means every customer has to reconnect their cloud.

### 3. The production GitHub App

Create a new app, separate from any development app, under the GitHub
organization that will own Airship (**Settings → Developer settings →
GitHub Apps → New GitHub App**):

| Setting | Value |
| --- | --- |
| Callback URL | `https://api.example.com/v1/auth/github/callback` |
| Webhook URL | `https://api.example.com/v1/github/webhook` |
| Webhook secret | `openssl rand -hex 32` |
| Repository permissions | Administration **read & write**, Actions **read**, Contents **read** (**write** if the access-control API should open pull requests on `.airship`), Pull requests **write**, Metadata **read** |
| Organization permissions | Members **read**, Self-hosted runners **read & write** |
| Subscribe to events | **Workflow job**, **Push**, **Repository** |
| Where can it be installed | **Any account** if other organizations will use your Airship; otherwise only this account |

Collect five values for the secret store: the app's slug, Client ID, a new
client secret, the webhook secret, and a generated private key (`.pem`).

You can create the app before anything is deployed. Deliveries fail until
the API is up. Nothing is lost by that before the app is installed
anywhere.

---

## Production checklist

Work through this for every new deployment. Each guide shows how to meet
each item on its platform.

**Configuration**

- [ ] `PUBLIC_URL` and `WEB_URL` are the https custom domains, and
      `CORS_ORIGINS` is exactly `WEB_URL`.
- [ ] `CREDENTIALS_KEY`, `DATABASE_URL`, `GITHUB_CLIENT_SECRET`,
      `GITHUB_WEBHOOK_SECRET` and the private key come from a secret store,
      not plain environment configuration.
- [ ] `CREDENTIALS_KEY` is backed up outside the platform.
- [ ] `DEV_LOGIN` and every `*_ENDPOINT` variable are unset. The API refuses
      to start otherwise for `DEV_LOGIN`, but the endpoints only send
      launches to the wrong place.
- [ ] The dashboard image was built with the production `NEXT_PUBLIC_API_URL`.

**Runtime**

- [ ] **At least one API replica is always running, with CPU always
      allocated.** The launcher and reconciler are background loops, not
      requests. Scale-to-zero, or CPU that is only allocated during
      requests, stops launches and leaves finished machines running until
      their own deadline. Two replicas is the recommendation.
- [ ] The load balancer's health check is `GET /healthz` on the API.
- [ ] The platform waits at least 30 seconds after `SIGTERM` before killing
      an API container, so requests in flight can finish.
- [ ] Migrations run as a separate step before each rollout, never from
      every replica at startup.

**Network**

- [ ] GitHub can reach `https://api.example.com/v1/github/webhook` from the
      internet.
- [ ] The API can reach the internet: `api.github.com`, and each cloud's
      API endpoints (the list is in
      [operations.md, "Networking requirements"](../architecture/operations.md#networking-requirements)).
      In a private subnet, that means a NAT gateway or equivalent.
- [ ] Runner machines can reach `PUBLIC_URL` over 443. This only matters for
      `cache: true`, but without it caching is silently off.
- [ ] Postgres is reachable only from the API (private networking or a
      firewall rule), with TLS where the platform offers it.

**Data**

- [ ] Postgres is version 18 or newer.
- [ ] Automated Postgres backups with point-in-time recovery are on.
- [ ] You have restored a backup once, into a scratch database, and opened
      the dashboard against it with the production `CREDENTIALS_KEY`.

**Verification**

- [ ] `curl https://api.example.com/healthz` returns `"status":"ok"` and
      the version you deployed.
- [ ] GitHub App → **Advanced → Recent Deliveries** shows `200`s.
- [ ] **Sign in with GitHub** on `https://app.example.com` lands on the
      dashboard, not back on the sign-in page.
- [ ] Install the app on a test organization, connect a cloud, activate a
      repository and run a job with
      `runs-on: airship-aws-t3-medium-ubuntu2404--${{ github.run_id }}` (or
      the equivalent for another cloud). The job goes green and the machine
      disappears from **Runners** within a few minutes.

**Ongoing**

- [ ] Someone owns bumping `RUNNER_VERSION`. GitHub stops sending jobs to
      runners more than 30 days behind the latest
      [actions/runner release](https://github.com/actions/runner/releases).
- [ ] Alerts on the log lines listed in
      [operations.md, "Observability"](../architecture/operations.md#observability).

---

## Releasing a new version

The same on every platform:

1. Build and push both images, tagged with the commit.
2. Run the migration job with the new API image and wait for it to
   succeed. Replicas of the previous release keep serving while it runs,
   so check a release's new migrations for anything the old code would
   trip over (a dropped column, a new `NOT NULL` without a default) and
   split those across two releases.
3. Roll out the API. Replicas drain for 30 seconds. Launches in flight on a
   replica that stops are covered by a 5-minute lease and are picked up
   again by another replica.
4. Roll out the dashboard, if it changed.

To roll back, deploy the previous images. Undo a migration only if the
release notes say the previous version cannot run against the new schema
(`migrate down` reverts one migration at a time).
