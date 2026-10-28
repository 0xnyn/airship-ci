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
pinned in `apps/api/tools/go.mod` and run through `go tool`. `make doctor`
checks the four tools above.

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
make setup     # checks tools, pnpm install, env files, starts and migrates Postgres
make dev
```

`make setup` is the same as running these by hand:

```bash
make doctor                                 # Go, Node, pnpm, Docker present and recent
pnpm install                                # also generates apps/dash/src/env.ts
make env                                    # copies each .env.example that has no copy yet
pnpm db:up                                  # Postgres 18 in Docker on :5432
pnpm db:migrate
```

The copied `apps/api/.env` has `DEV_LOGIN=true` and a development
credentials key, so nothing needs filling in for this level. `make env`
never overwrites a file that exists, so it is safe to rerun after pulling.

| URL | What |
| --- | --- |
| http://localhost:3000 | Dashboard. Choose **Development sign-in**: you are an admin of a demo organization, `acme` |
| http://localhost:8080/docs | API reference, generated from the same OpenAPI document as the TypeScript client |
| http://localhost:8080/healthz | Health check |
| http://localhost:4000 | Marketing site (`apps/home`) |

`make dev` (which is `pnpm dev`) runs these through Turborepo:

- **The API under air.** Every save of a `.go` or `.sql` file rebuilds the
  binary and rewrites `apps/api/openapi.json`.
- **The client generator.** It watches `openapi.json` and regenerates
  `packages/client/src/generated/api.ts`.
- **Next.js.** It picks up the new types, so a Go change reaches the
  dashboard's types without a manual step.
- **Astro**, for the marketing site.

To run one piece: `make dev-api` (the API and the client generator),
`make dev-dash` or `make dev-home`.

### The env files

| File | Read by | Committed | Created by `make env` |
| --- | --- | --- | --- |
| `apps/api/.env.example` | Nobody. The template for `apps/api/.env`, with every variable the API reads | yes | |
| `apps/api/.env` | The API and `migrate` (godotenv), so `make dev` and `make db-migrate`. Real environment variables win. Never read by `go test` | no | yes |
| `apps/dash/.env.schema` | varlock: types, validation and defaults for the dashboard's env | yes | |
| `apps/dash/.env.example` | Nobody. The template for `apps/dash/.env.local` | yes | |
| `apps/dash/.env.local` | varlock, over the schema's defaults. Only needed when the API is not on `localhost:8080` | no | yes |
| `apps/api/.env.live.example` | Nobody. The template for `apps/api/.env.live`: credentials for the live cloud suites | yes | |
| `apps/api/.env.live` | `make test-live` only. Never the API, `make dev` or plain `go test` | no | no, opt in by copying |
| `apps/home/.env.example` | Nobody. The template for `apps/home/.env`, every line commented out | yes | |
| `apps/home/.env` | Astro, at dev and build time. Only needed to point the site's links at your local dashboard | no | yes |
| `deploy/production/*.env.example` | Nobody. What production sets, for the [deploy guides](deploy/README.md) | yes | |
| `deploy/compose/.env.example` | Nobody. The template for the single-server stack's `.env` ([server.md](deploy/server.md)) | yes | |

Every variable the code reads is in one of these templates, with its
default. Variables only tests read are listed at the bottom of
`apps/api/.env.example`, the live suites' credentials are in
`apps/api/.env.live.example`, and both are covered in [Tests](#tests).
Nothing reads any other env file, such as a `.env.local` at the repository
root: a key kept there reaches no code. Two kinds of variable are in no
file on purpose:

- **Set by the platform:** `NODE_ENV` (Next.js and Vercel), `CI` (GitHub
  Actions, which makes database tests fail instead of skipping), and `PORT`
  on platforms that assign one.
- **Set on runner machines:** `AIRSHIP_URL` and `AIRSHIP_RUNNER_TOKEN`,
  which the boot script hands to the cache proxy.

The development `CREDENTIALS_KEY` in `.env.example` is public. The API
refuses to start with it outside `APP_ENV=development`, so it cannot end up
sealing real credentials by accident.

### Day-to-day commands

Run from the repository root. `make help` lists every target. Each one
wraps a pnpm script, so the right-hand column works too.

| `make` | Or | Does |
| --- | --- | --- |
| `make dev` | `pnpm dev` | Everything above |
| `make generate` | `pnpm generate` | sqlc, then `openapi.json`, then the TypeScript client, in order |
| `make lint` / `make format` | `pnpm lint` / `pnpm format` | Ultracite (Biome) for TypeScript, JSON, CSS and Markdown; golangci-lint for Go |
| `make typecheck` / `make test` / `make build` | `pnpm typecheck` / `test` / `build` | Across every package |
| `make check` | `pnpm check` | Everything CI runs, including `check-generated` |
| `make db-up` / `make db-down` | `pnpm db:up` / `db:down` | Start or stop Postgres (`db-down` keeps the data) |
| `make db-migrate` / `make db-rollback` | `pnpm db:migrate` / `db:rollback` | Apply all migrations, or undo the last one |
| `make db-status` | `pnpm --filter @airship/api db:status` | Which migrations are applied |
| `make db-new NAME=add_thing` | `pnpm db:new add_thing sql` | Create the next numbered migration |
| `make db-psql` | `docker compose exec postgres psql -U airship` | A SQL prompt on the local database, no local `psql` needed |
| `make db-reset` | see below | Delete the local database and migrate a fresh one (asks first) |
| `make images NEXT_PUBLIC_API_URL=https://...` | the `docker build` lines in [deploy/README.md](deploy/README.md) | Both production images, tagged with the commit |
| `make clean` | | Delete build output and Turborepo caches |
| | `pnpm turbo run <task> --filter=@airship/api` | One task for one package |

To start over with an empty database (what `make db-reset` does):

```bash
docker compose down --volumes && pnpm db:up && pnpm db:migrate
```

Do this after pulling a change that rewrote existing migrations, which
happens while Airship is pre-production: goose only applies migrations it
has not recorded, so an edited one never reaches an existing database.

CLAUDE.md, "Adding an endpoint", walks through a change that crosses the
whole stack, from migration to dashboard.

### Tests

```bash
make db-up
make test        # sets TEST_DATABASE_URL to the local Postgres for you
```

With pnpm, export it yourself:

```bash
export TEST_DATABASE_URL='postgres://airship:airship@localhost:5432/airship?sslmode=disable'
pnpm test
```

`make test-go` runs only the API's tests (with the race detector), and
`make test-ts` only the TypeScript ones.

Tests that touch SQL create their own database from `TEST_DATABASE_URL`, one
per test. Without the variable they are skipped locally. In CI they fail
instead, so a missing database is never mistaken for a pass. `go test`
reads its variables from the shell, never from `apps/api/.env`.

**Postgres 17.** Production on Supabase runs Postgres 17, and local
Postgres is 18. `make test-pg17` starts a throwaway Postgres 17 container
on :55417, runs the API's tests against it and removes it. CI does the same
on every push. Run it after writing a query or migration that might use
something only 18 has.

The cloud providers have extra suites that need opting into:

| Suite | Runs against | How |
| --- | --- | --- |
| `TestAgainstEmulator` (AWS) | moto on :5055 | `make test-aws-emulator` (starts and stops moto; needs `uvx`). By hand: `uvx --from 'moto[server]' moto_server -p 5055 &` then `AIRSHIP_AWS_ENDPOINT=http://localhost:5055 go test -run TestAgainstEmulator ./internal/cloud/aws` |
| GCP, Azure, Daytona fakes | In-process fakes of each API, driven through the real SDKs | Part of `pnpm test`, nothing to set |
| `TestAgainstLiveAWS` | A real account: one small instance, terminated again | `AIRSHIP_LIVE_AWS_ACCESS_KEY_ID`, `AIRSHIP_LIVE_AWS_SECRET_ACCESS_KEY`, `AIRSHIP_LIVE_AWS_REGION` |
| `TestAgainstLiveGCP` | A real project | `AIRSHIP_LIVE_GCP_KEY_FILE` (a service account JSON key), `AIRSHIP_LIVE_GCP_LOCATION` |
| `TestAgainstLiveAzure` | A real subscription | `AIRSHIP_LIVE_AZURE_TENANT_ID`, `_CLIENT_ID`, `_CLIENT_SECRET`, `_SUBSCRIPTION_ID`, `_REGION` |
| `TestAgainstLiveDaytona` | A real organization | `AIRSHIP_LIVE_DAYTONA_API_KEY`, optionally `_API_URL`, `_REGION` |
| Windows boot script parse | PowerShell's parser | Skipped unless `pwsh` is on `PATH` or `AIRSHIP_PWSH` points at it |

Run a live suite with `make test-live CLOUD=aws` (or `gcp`, `azure`,
`daytona`). It loads `apps/api/.env.live` if it exists, so the easiest
setup is:

```bash
cp apps/api/.env.live.example apps/api/.env.live   # fill in one cloud
make test-live CLOUD=daytona
```

Without the file, it uses the variables exported in your shell. Plain
`go test -v -run <name> ./internal/cloud/<cloud>` from `apps/api` only
ever uses the shell. The Docker build ignores `.env.*` and `*.pem`, so
neither reaches an image.
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
make tunnel    # cloudflared tunnel --url http://localhost:8080
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

Leave `DASH_URL`, `CORS_ORIGINS` and the dashboard's `NEXT_PUBLIC_API_URL` on
localhost. Point the app's webhook and callback URLs at the tunnel. A quick
tunnel's URL changes every time it starts, so both need updating each time.

Restart `make dev`. Under the app's **Advanced → Recent Deliveries**, the
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
DASH_URL=https://app.dev.example.com
CORS_ORIGINS=https://app.dev.example.com,http://localhost:3000

# apps/dash/.env.local
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
  leaves it out. Run `make cacheproxy` once before `make dev` to include
  it. Without it, jobs still run, without
  acceleration, and Events records `cache.unavailable`.
- **Machines outlive your laptop.** If you stop `make dev` mid-job, the job
  still finishes, and every machine powers itself off at
  `RUNNER_MAX_LIFETIME` (8h by default). Set `RUNNER_MAX_LIFETIME=1h` in
  `apps/api/.env` while experimenting.
- **Use a dedicated account or project.** It keeps test instances easy to
  find and the bill easy to read.

---

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `make setup` stops at `doctor` | Install what it lists as missing. Node must be 24 or newer |
| `load config: ... DATABASE_URL` at startup | `apps/api/.env` is missing. `make env` |
| `connection refused` on :5432 | `pnpm db:up`. If another Postgres already uses 5432, stop it or change the port in `docker-compose.yml` and `DATABASE_URL` |
| `src/env.ts` missing, or env types out of date | `pnpm install` (it runs `varlock codegen`), or `pnpm --filter @airship/dash env:generate` |
| `pnpm check:generated` fails | Run `pnpm generate` and commit what changed. Never edit generated files by hand |
| Tests skipped | `TEST_DATABASE_URL` is not exported in the shell that runs them. `make test` sets it |
| `relation ... does not exist` or a missing column after pulling | A migration was rewritten. `make db-reset` |
| `make test-pg17` fails to start | Something else uses :55417. `make test-pg17 PG17_PORT=55418` |
| Links on the marketing site go to production | Expected. Set `PUBLIC_APP_URL` in `apps/home/.env` to use your local dashboard |
| Webhook deliveries fail with 401 | `GITHUB_WEBHOOK_SECRET` differs from the app's |
| Webhook deliveries fail with 502 or time out | The tunnel is down, or its URL changed. Update the app's webhook URL |
| GitHub sign-in returns to the sign-in page | Dashboard and API are on different sites. Use a named tunnel, or Development sign-in |
| `DEV_LOGIN is only allowed with APP_ENV=development` | `APP_ENV` is set to something else in your shell or `.env` |
