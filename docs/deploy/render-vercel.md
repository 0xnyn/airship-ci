# Deploying Airship on Render and Vercel

**What this is.** Airship's control plane with the API on a Render web
service and the dashboard on Vercel, using a managed Postgres from
elsewhere ([Supabase](supabase.md) or [PlanetScale](planetscale.md)). It
goes from nothing to a green job using the two platforms' dashboards, with
an optional Render Blueprint for the API.

**Audience.** Whoever deploys Airship and would rather not run a cloud
account for it.

**Time.** About 45 minutes, most of it creating the database and waiting
for DNS.

Read [README.md](README.md) first. Its "Before you start" section (domain,
credentials key, GitHub App) applies here unchanged.

---

## The result

```
   app.example.com ──► Vercel (apps/dash, static + Next.js)
                              │  browser calls the API directly, with the
                              │  session cookie (same site: example.com)
   api.example.com ──► Render web service airship-api × 2 ──► api.github.com
                              ▲            │                  ──► customers' clouds
          GitHub webhooks ────┤            │ TLS over the internet
          runner machines ────┘            ▼
                                  Postgres 17+ (Supabase or PlanetScale)
```

The dashboard is a client-side app: the browser talks to the API, and the
Vercel deployment never talks to the API itself. So Vercel only has to
serve the pages, and everything with state lives on Render and the
database.

---

## Three rules for this setup

1. **The API runs on Render, never on Vercel.** The launcher, reconciler
   and webhook redeliverer are background loops inside the API process.
   Vercel Functions only run while a request is in flight, so the loops
   would never run. A Render web service is one long-running container,
   which is what the API needs.
2. **A paid Render instance type, always on.** The free type spins down
   when idle, which stops the loops, and it has no pre-deploy command, which
   is how migrations run here. Two instances is the recommendation (any
   paid type allows manual scaling).
3. **Connect to Postgres through a session, not a transaction pooler.** The
   reconciler and redeliverer are elected with session-level advisory
   locks. On Supabase that means the **session pooler** on port 5432, never
   the transaction pooler on 6543. The reason is in
   [planetscale.md, "The one rule"](planetscale.md#the-one-rule-port-5432-never-6432).

---

## Step 1 — The database

Create it first. The API's pre-deploy migration needs it.

| Database | Guide | Notes |
| --- | --- | --- |
| Supabase | [supabase.md](supabase.md) | Postgres 17, which Airship supports |
| PlanetScale | [planetscale.md](planetscale.md) | Pick the AWS region nearest your Render region |

Use the same city as your Render region:

| Render region | Supabase / PlanetScale (AWS) region |
| --- | --- |
| Oregon | `us-west-2` |
| Ohio | `us-east-2` |
| Virginia | `us-east-1` |
| Frankfurt | `eu-central-1` |
| Singapore | `ap-southeast-1` |

Every launch and reconcile pass makes several round trips to the database,
so a cross-continent database makes everything slower.

Keep the connection string to hand for step 2. **Render cannot reach IPv6
addresses**, so on Supabase it is the session pooler's string, not the
direct connection's (which is IPv6-only without Supabase's IPv4 add-on).

---

## Step 2 — The API on Render

### Create the service

**New → Web Service**, connect the repository, then:

| Setting | Value |
| --- | --- |
| Name | `airship-api` |
| Language | **Docker** |
| Branch | `main` |
| Region | The region from step 1. It cannot be changed later |
| Root Directory | leave empty |
| Dockerfile Path | `./apps/api/Dockerfile` |
| Docker Build Context Directory | `./apps/api` |
| Docker Command | leave empty (the image runs the API) |
| Instance type | Any paid type. The smallest (512 MB) is enough |
| Pre-Deploy Command | `/usr/local/bin/migrate up` |
| Health Check Path | `/healthz` |
| Auto-Deploy | **After CI checks pass** |

- The image is distroless: it has no shell. That's why the pre-deploy
  command is the binary's full path with no `sh -c`. Render's command
  replaces the image's entrypoint, so this runs `migrate`, not the API.
- The pre-deploy command runs once per deploy, on its own instance, before
  any new API instance starts. If it fails, the deploy stops and the
  previous version keeps serving. This matches the release order in
  [README.md](README.md#releasing-a-new-version).
- Under **Build Filters**, include `apps/api/**` so dashboard-only changes
  don't redeploy the API.

### Environment

Add these under **Environment**. Render encrypts environment values at
rest and has no separate secret store, so the secrets go here too. Keep
the `CREDENTIALS_KEY` backup outside Render, as the checklist asks.

| Key | Value |
| --- | --- |
| `DATABASE_URL` | From step 1, with `sslmode=require` (or `verify-full`) and `pool_max_conns=4` |
| `PUBLIC_URL` | `https://api.example.com` |
| `DASH_URL` | `https://app.example.com` |
| `CORS_ORIGINS` | `https://app.example.com` |
| `LOG_LEVEL` | `info` |
| `CREDENTIALS_KEY` | `openssl rand -base64 32` (README step 2) |
| `GITHUB_APP_SLUG` | The app's slug |
| `GITHUB_CLIENT_ID` | The app's Client ID |
| `GITHUB_CLIENT_SECRET` | The app's client secret |
| `GITHUB_WEBHOOK_SECRET` | The webhook secret |
| `GITHUB_PRIVATE_KEY` | The whole `.pem`. Render accepts multi-line values |

Set nothing else. In particular, never set `DEV_LOGIN` or any `*_ENDPOINT`
variable (see [`api.env.example`](../../deploy/production/api.env.example)).

### Scaling and shutdown

- **Settings → Scaling**: 2 instances. Render replaces them one at a time
  on each deploy and cancels the deploy if a new one fails its health
  check.
- **Shutdown delay**: Render waits 30 seconds after `SIGTERM` by default,
  exactly as long as the API drains. Raise it to 45 for some margin
  (`maxShutdownDelaySeconds` in the Blueprint below, or the API's
  Update-service endpoint).

### Custom domain

**Settings → Custom Domains → Add** `api.example.com`, then create the
`CNAME` record Render shows (pointing at `airship-api.onrender.com`).
Render issues the certificate once DNS resolves.

`airship-api.onrender.com` keeps working alongside it. Nothing uses that
address, and GitHub sign-in fails from it (it isn't on the dashboard's
site), so point nothing at it.

### Optional: as a Blueprint

The same service is described in
[`deploy/render/render.yaml`](../../deploy/render/render.yaml). Use it with
**New → Blueprint**, setting the Blueprint path to
`deploy/render/render.yaml`. Render asks for each secret (`sync: false`)
when it first creates the service.

---

## Step 3 — The dashboard on Vercel

**Add New → Project**, import the repository, then:

| Setting | Value |
| --- | --- |
| Root Directory | `apps/dash` |
| Framework Preset | Next.js (detected) |
| Build, install and output commands | leave as detected |
| Node.js Version (Settings → Build and Deployment) | **24.x**, as `engines` in the root `package.json` asks |

Vercel installs from the repository root, because `apps/dash` is part of
the pnpm workspace, and builds the `@airship/client` and `@airship/ui`
sources along with the app (`transpilePackages`). The generated API client
is committed, so the build doesn't need Go.

Environment variables, for **Production** and **Preview**:

| Key | Value | Why |
| --- | --- | --- |
| `NEXT_PUBLIC_API_URL` | `https://api.example.com` | Baked into the JavaScript at build time. Varlock (`apps/dash/.env.schema`) fails the build without it. Changing it means a redeploy |
| `ENABLE_EXPERIMENTAL_COREPACK` | `1` | Makes Vercel use the pnpm version pinned in `packageManager` (`pnpm@11.22.0`). Vercel would otherwise pick a pnpm from the lockfile version, which can ignore settings in `pnpm-workspace.yaml` such as `allowBuilds` |

Then **Settings → Domains → Add** `app.example.com` and create the record
Vercel shows.

**Preview deployments can't sign in.** They're served from `*.vercel.app`,
which is a different site from `api.example.com`, so the session cookie is
never sent. `CORS_ORIGINS` doesn't list them either. Previews are still
useful for checking a page renders. To skip them, add an Ignored Build
Step of `[ "$VERCEL_ENV" != "production" ]`, or `npx turbo-ignore` to
skip builds where `apps/dash` and its packages didn't change.

The `output: "standalone"` setting in `next.config.ts` is for the Docker
image. Vercel uses its own build output and ignores it.

---

## Step 4 — Verify

```bash
curl -s https://api.example.com/healthz
# {"status":"ok","database":"ok","version":"dev"}
```

`version` is `dev` because Render builds the image without the `VERSION`
build argument. The deploy's commit is on Render's **Events** page. To have
`/healthz` report it, build and push the image from CI with
`--build-arg VERSION=...` and deploy it as an image-backed Render service
instead.

The first deploy's **Pre-Deploy** log ends with
`goose: successfully migrated database to version: N`. If it shows the
API's startup logs instead, the pre-deploy command isn't set to
`/usr/local/bin/migrate up`.

Then work through the **Verification** items in the
[production checklist](README.md#production-checklist): webhook deliveries
in the GitHub App, GitHub sign-in on `https://app.example.com`, and one
real job.

---

## How this setup meets the checklist

| Checklist item | Here |
| --- | --- |
| Secrets in a secret store | Render's encrypted environment. Restrict who can see the service's environment with Render's workspace roles |
| At least one replica always running | 2 instances of a paid type. Render has no scale-to-zero for paid web services |
| Health check on `/healthz` | Health Check Path |
| 30 s after `SIGTERM` | Render's default 30, raised to 45 |
| Migrations before rollout, once | Pre-Deploy Command |
| Database reachable only from the API | Supabase: Network Restrictions to Render's outbound ranges ([supabase.md](supabase.md#step-5--network-and-tls)). Render's ranges are shared by every Render service in the region, so TLS and the password remain the real protection |
| API reaches the internet | Render services have outbound internet by default |

---

## Releasing

Push to `main`. After CI passes, Render builds the API image, runs the
migration and replaces the instances one at a time. Vercel builds and
promotes the dashboard on the same push.

The two platforms deploy independently, so the dashboard is usually live
a few minutes before the new API. That's fine for most releases. When a
dashboard change needs a new endpoint, ship the API change first and the
dashboard change in a later push.

**Rolling back.** Render: **Events → Rollback** on an earlier deploy.
Vercel: **Deployments → Instant Rollback**. Neither undoes a migration.
Check the release notes and run `migrate down` only if the previous
version can't run against the new schema (from your machine, with the
same `DATABASE_URL`, using `go tool -modfile=tools/go.mod goose` or the
API image).

---

## Operations

| Item | How |
| --- | --- |
| Logs | Render **Logs** for `airship-api` (filter on `"level":"ERROR"`). Render log streams can forward to a syslog-compatible provider |
| Alerts | Render deploy-failure notifications, plus the log lines in [operations.md, "Observability"](../architecture/operations.md#observability) through your log provider |
| Rotating a secret | Change it under **Environment** and choose **Save, rebuild and deploy** (or **Save and deploy**). Instances only read the environment at start |
| Static egress IP | Render's outbound addresses are shared ranges per region, listed on the service's **Connect → Outbound** tab. If a GitHub Enterprise Server or a customer allow-lists by IP, they have to allow the whole range |
| Upgrading Postgres | See the database guide |

---

## Tearing it down

Delete the Vercel project, then **Settings → Delete Web Service** for
`airship-api` on Render, then the database. Runner machines already
launched keep running until their job ends, and every one powers itself
off at `RUNNER_MAX_LIFETIME` at the latest, with or without the control
plane.
