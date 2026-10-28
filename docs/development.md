# Development setup

**What this is.** Everything needed to run Airship on your own machine, from
a fresh clone to a real GitHub job running on a machine that your local
control plane launched.

**Audience.** Anyone working on the code. Deploying Airship for real is
covered in [deploy/](deploy/README.md).

There are three levels. Most work needs only the first.

| Level | You get | You need |
| --- | --- | --- |
| [1. Dashboard and API](#level-1--dashboard-and-api) | The full dashboard with a demo organization, the API and its docs, every test | Go, Node, pnpm, Docker |
| [2. Real GitHub](#level-2--real-github) | Webhooks from a real organization, GitHub sign-in | A development GitHub App and a tunnel |
| [3. Real machines](#level-3--real-machines) | A real job on a real EC2 instance, Compute Engine VM, Azure VM or Daytona sandbox | A cloud account to launch into |

---

## Prerequisites

| Tool | Version | Notes |
| --- | --- | --- |
| Go | 1.27+ | `go.mod` names `toolchain go1.27.3`; an older Go downloads it automatically when `GOTOOLCHAIN=auto` (the default) |
| Node | 24+ | |
| pnpm | 11.22 | `corepack enable` picks the version in `package.json`, or `npm i -g pnpm@11.22.0` |
| Docker | any recent | For Postgres. Docker Desktop, OrbStack or Colima all work |

Nothing else is installed globally. sqlc, goose, golangci-lint and air are
pinned in `apps/api/tools/go.mod` and run through `go tool`.

Optional, for the levels below:

| Tool | For |
| --- | --- |
| [`cloudflared`](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/) or [`ngrok`](https://ngrok.com) | Level 2: a public https URL for GitHub webhooks |
| [`uv`](https://docs.astral.sh/uv/) | Running [moto](https://github.com/getmoto/moto), the AWS emulator |
| `aws`, `gcloud`, `az` CLIs | Level 3, creating credentials |

---

## Level 1 — Dashboard and API

```bash
git clone <this repository> airship && cd airship
pnpm install                              # also generates apps/web/src/env.ts
cp apps/api/.env.example apps/api/.env    # DEV_LOGIN=true and a development key are set
pnpm db:up                                # Postgres 18 in Docker on :5432
pnpm db:migrate
pnpm dev
```

| URL | What |
| --- | --- |
| http://localhost:3000 | Dashboard. Choose **Development sign-in**: you are an admin of a demo organization, `acme` |
| http://localhost:8080/docs | API reference, generated from the same OpenAPI document as the TypeScript client |
| http://localhost:8080/healthz | Health check |

`pnpm dev` runs three things through Turborepo:

- **The API under air.** Every save of a `.go` or `.sql` file rebuilds the
  binary and rewrites `apps/api/openapi.json`.
- **The client generator.** It watches `openapi.json` and regenerates
  `packages/client/src/generated/api.ts`.
- **Next.js.** It picks up the new types, so a Go change reaches the
  dashboard's types without a manual step.

### The env files

| File | Read by | Committed |
| --- | --- | --- |
| `apps/api/.env.example` | Nobody. The template for `apps/api/.env`, with every variable documented | yes |
| `apps/api/.env` | `pnpm dev` and `pnpm db:migrate` (godotenv). Real environment variables win | no |
| `apps/web/.env.schema` | varlock: types, validation and defaults for the dashboard's env | yes |
| `apps/web/.env.example` | Nobody. The template for `apps/web/.env.local` | yes |
| `apps/web/.env.local` | varlock, over the schema's defaults. Only needed when the API is not on `localhost:8080` | no |
| `deploy/production/*.env.example` | Nobody. What production sets, for the [deploy guides](deploy/README.md) | yes |

The development `CREDENTIALS_KEY` in `.env.example` is public. The API
refuses to start with it outside `APP_ENV=development`, so it cannot end up
sealing real credentials by accident.

### Day-to-day commands

Run from the repository root.

| Command | Does |
| --- | --- |
| `pnpm dev` | Everything above |
| `pnpm generate` | sqlc, then `openapi.json`, then the TypeScript client, in order |
| `pnpm lint` / `pnpm format` | Ultracite (Biome) for TypeScript, JSON, CSS and Markdown; golangci-lint for Go |
| `pnpm typecheck` / `pnpm test` / `pnpm build` | Across every package |
| `pnpm check` | Everything CI runs, including `check:generated` |
| `pnpm db:up` / `pnpm db:down` | Start or stop Postgres (`db:down` keeps the volume) |
| `pnpm db:migrate` / `pnpm db:rollback` | Apply all migrations, or undo the last one |
| `pnpm db:new <name> sql` | Create the next numbered migration |
| `pnpm turbo run <task> --filter=@airship/api` | One task for one package |

To start over with an empty database:

```bash
docker compose down --volumes && pnpm db:up && pnpm db:migrate
```

CLAUDE.md, "Adding an endpoint", walks through a change that crosses the
whole stack, from migration to dashboard.

### Tests

```bash
export TEST_DATABASE_URL='postgres://airship:airship@localhost:5432/airship?sslmode=disable'
pnpm test
```

Tests that touch SQL create their own database from `TEST_DATABASE_URL`, one
per test. Without the variable they are skipped locally. In CI they fail
instead, so a missing database is never mistaken for a pass. `go test`
reads its variables from the shell, never from `apps/api/.env`.

The cloud providers have extra suites that need opting into:

| Suite | Runs against | How |
| --- | --- | --- |
| `TestAgainstEmulator` (AWS) | moto on :5055 | `uvx --from 'moto[server]' moto_server -p 5055 &` then `AIRSHIP_AWS_ENDPOINT=http://localhost:5055 go test -run TestAgainstEmulator ./internal/cloud/aws` |
| GCP, Azure, Daytona fakes | In-process fakes of each API, driven through the real SDKs | Part of `pnpm test`, nothing to set |
| `TestAgainstLiveAWS` | A real account: one small instance, terminated again | `AIRSHIP_LIVE_AWS_ACCESS_KEY_ID`, `AIRSHIP_LIVE_AWS_SECRET_ACCESS_KEY`, `AIRSHIP_LIVE_AWS_REGION` |
| `TestAgainstLiveGCP` | A real project | `AIRSHIP_LIVE_GCP_KEY_FILE` (a service account JSON key), `AIRSHIP_LIVE_GCP_LOCATION` |
| `TestAgainstLiveAzure` | A real subscription | `AIRSHIP_LIVE_AZURE_TENANT_ID`, `_CLIENT_ID`, `_CLIENT_SECRET`, `_SUBSCRIPTION_ID`, `_REGION` |
| `TestAgainstLiveDaytona` | A real organization | `AIRSHIP_LIVE_DAYTONA_API_KEY`, optionally `_API_URL`, `_REGION` |

Run the live suites from `apps/api` with `go test -v -run <name> ./internal/cloud/<cloud>`.
They create real resources that cost money, and they clean up after
themselves.

---

## Level 2 — Real GitHub

Webhooks need a public https URL. GitHub sign-in also needs the dashboard
and the API on the same site, because the session cookie lives on the API's
host and is `SameSite=Lax`. That gives two setups:

| Setup | Webhooks | Dashboard sign-in |
| --- | --- | --- |
| **Quick tunnel** for the API only | yes | Development sign-in, which shows the demo `acme` organization, not your real one |
| **Named tunnel** on a domain you own, one hostname for the API and one for the dashboard | yes | GitHub sign-in, with your real organization |

The quick tunnel is enough to see webhooks arrive and runners get created.
To use the dashboard against a real installation, use the named tunnel.

### Creating a development GitHub App

Create one app per developer, separate from production, so webhooks reach
your machine and nobody else's. Create it under **GitHub → Settings →
Developer settings → GitHub Apps → New GitHub App**, owned by a test
organization if you can:

| Setting | Value |
| --- | --- |
| Name | `airship-dev-<you>` (the slug becomes `GITHUB_APP_SLUG`) |
| Homepage URL | Anything |
| Callback URL | `{PUBLIC_URL}/v1/auth/github/callback` |
| Webhook URL | `{PUBLIC_URL}/v1/github/webhook` |
| Webhook secret | `openssl rand -hex 32` |
| Repository permissions | Administration **read & write**, Actions **read**, Contents **read** (**write** to try the access-control API's pull requests), Pull requests **write**, Metadata **read** |
| Organization permissions | Members **read**, Self-hosted runners **read & write** |
| Subscribe to events | **Workflow job**, **Push**, **Repository** |
| Where can it be installed | Only on this account |

Then on the app's page:

1. Note the **Client ID**, and generate a **client secret**.
2. **Generate a private key**. Save the `.pem` as `apps/api/github-app.pem`
   (`*.pem` is gitignored).
3. **Install App** on your test organization and pick a repository or two.

### Quick tunnel

```bash
cloudflared tunnel --url http://localhost:8080
# prints https://<random>.trycloudflare.com
```

In `apps/api/.env`:

```bash
PUBLIC_URL=https://<random>.trycloudflare.com
GITHUB_APP_SLUG=airship-dev-you
GITHUB_CLIENT_ID=Iv23li...
GITHUB_CLIENT_SECRET=...
GITHUB_WEBHOOK_SECRET=...
GITHUB_PRIVATE_KEY_FILE=./github-app.pem
```

Leave `WEB_URL`, `CORS_ORIGINS` and the dashboard's `NEXT_PUBLIC_API_URL` on
localhost. Point the app's webhook and callback URLs at the tunnel. A quick
tunnel's URL changes every time it starts, so both need updating each time.

Restart `pnpm dev`. Under the app's **Advanced → Recent Deliveries**, the
deliveries should now show `200`. The API logs each one.

### Named tunnel (GitHub sign-in)

With a Cloudflare-managed domain (other tunnels work the same way, given two
hostnames on one domain):

```bash
cloudflared tunnel login
cloudflared tunnel create airship-dev
cloudflared tunnel route dns airship-dev api.dev.example.com
cloudflared tunnel route dns airship-dev app.dev.example.com
cat > ~/.cloudflared/airship-dev.yml <<'EOF'
tunnel: airship-dev
credentials-file: /Users/you/.cloudflared/<tunnel-id>.json
ingress:
  - hostname: api.dev.example.com
    service: http://localhost:8080
  - hostname: app.dev.example.com
    service: http://localhost:3000
  - service: http_status:404
EOF
cloudflared tunnel --config ~/.cloudflared/airship-dev.yml run
```

Set the URLs to match:

```bash
# apps/api/.env
PUBLIC_URL=https://api.dev.example.com
WEB_URL=https://app.dev.example.com
CORS_ORIGINS=https://app.dev.example.com,http://localhost:3000

# apps/web/.env.local
NEXT_PUBLIC_API_URL=https://api.dev.example.com
```

Open https://app.dev.example.com and choose **Sign in with GitHub**.
Because the URLs never change, the GitHub App only needs setting up once.

---

## Level 3 — Real machines

With Level 2 running, connect a cloud under **Cloud** in the dashboard. Each
cloud has a setup guide covering the credential it needs, the network, and
the quotas:

| Cloud | Guide | Smallest useful test |
| --- | --- | --- |
| AWS | [architecture/aws-setup.md](architecture/aws-setup.md) | `runs-on: airship-aws-t3-medium-ubuntu2404--${{ github.run_id }}` |
| Google Cloud | [architecture/gcp-setup.md](architecture/gcp-setup.md) | `cloud: gcp` with `e2-standard-2` |
| Azure | [architecture/azure-setup.md](architecture/azure-setup.md) | `cloud: azure` with `Standard_D2s_v5` |
| Daytona | [architecture/daytona-setup.md](architecture/daytona-setup.md) | `cloud: daytona`, no image needed |

Then activate a repository under **Repositories**, commit an `airship.yaml`
([airship-yaml.md](airship-yaml.md)) and a workflow, and push. The job
appears under **Jobs** and the machine under **Runners**. Failures land on
**Events** with the cloud's own error message.

Things that differ from production, worth knowing while testing:

- **Caching** (`extra_config.cache: true`) needs runner machines to reach
  `PUBLIC_URL`, so it works through a tunnel. It also needs the proxy binary
  embedded in the API. `pnpm dev` builds the API with `go build`, which
  leaves it out. Run `pnpm --filter @airship/api build:cacheproxy` once
  before `pnpm dev` to include it. Without it, jobs still run, without
  acceleration, and Events records `cache.unavailable`.
- **Machines outlive your laptop.** If you stop `pnpm dev` mid-job, the job
  still finishes, and every machine powers itself off at
  `RUNNER_MAX_LIFETIME` (8h by default). Set `RUNNER_MAX_LIFETIME=1h` in
  `apps/api/.env` while experimenting.
- **Use a dedicated account or project.** It keeps test instances easy to
  find and the bill easy to read.

---

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `load config: ... DATABASE_URL` at startup | `apps/api/.env` is missing. `cp apps/api/.env.example apps/api/.env` |
| `connection refused` on :5432 | `pnpm db:up`. If another Postgres already uses 5432, stop it or change the port in `docker-compose.yml` and `DATABASE_URL` |
| `src/env.ts` missing, or env types out of date | `pnpm install` (it runs `varlock codegen`), or `pnpm --filter web env:generate` |
| `pnpm check:generated` fails | Run `pnpm generate` and commit what changed. Never edit generated files by hand |
| Tests skipped | `TEST_DATABASE_URL` is not exported in the shell that runs them |
| Webhook deliveries fail with 401 | `GITHUB_WEBHOOK_SECRET` differs from the app's |
| Webhook deliveries fail with 502 or time out | The tunnel is down, or its URL changed. Update the app's webhook URL |
| GitHub sign-in returns to the sign-in page | Dashboard and API are on different sites. Use a named tunnel, or Development sign-in |
| `DEV_LOGIN is only allowed with APP_ENV=development` | `APP_ENV` is set to something else in your shell or `.env` |
