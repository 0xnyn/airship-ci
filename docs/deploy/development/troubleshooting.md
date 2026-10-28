# Troubleshooting development

Everything that commonly goes wrong on a developer machine, by symptom.
Deployment problems are in [stage-0/operate.md](../stage-0/operate.md).

---

## Setup and tools

| Symptom | Fix |
| --- | --- |
| `make setup` stops at `doctor` | Install what it lists. Node must be 24 or newer ([1-prerequisites.md](1-prerequisites.md)) |
| `docker installed, but the daemon is not running` | Start Docker Desktop, OrbStack or Colima |
| `command not found: pnpm` | `corepack enable`, or `npm i -g pnpm@11.22.0` |
| `go: command not found` after installing Go on Linux | `/usr/local/go/bin` is not on `PATH` |
| A `go tool` command is missing | Nothing is installed globally on purpose. Run it through `make`, or as `go tool -modfile=tools/go.mod <tool>` from `apps/api` |
| `pnpm install` fails on a native package | The workspace allow-lists which packages may run build scripts (`allowBuilds` in `pnpm-workspace.yaml`). A new dependency that needs one has to be added there |

## The API will not start

| Symptom | Fix |
| --- | --- |
| `load config: ... DATABASE_URL` | `apps/api/.env` is missing. `make env` |
| `connection refused` on `:5432` | `make db-up`. If another Postgres already owns 5432, stop it, or change the port in `docker-compose.yml` **and** `DATABASE_URL` |
| `DEV_LOGIN is only allowed with APP_ENV=development` | `APP_ENV` is set to something else — check your shell as well as `.env`; a real environment variable wins over the file |
| `CREDENTIALS_KEY is the key from .env.example` | You are not in development. Either set `APP_ENV=development`, or generate your own key with `openssl rand -base64 32` |
| `PUBLIC_URL must be https outside development` | Same cause: `APP_ENV` is not `development` |
| `GITHUB_CLIENT_ID, GITHUB_PRIVATE_KEY and GITHUB_WEBHOOK_SECRET are required outside development` | Same cause |
| It starts, but the port is already in use | Something else is on `:8080`. Set `PORT` in `apps/api/.env`, and `NEXT_PUBLIC_API_URL` in `apps/dash/.env.local` to match |

## Database

| Symptom | Fix |
| --- | --- |
| `relation ... does not exist`, or a column missing after pulling | A migration was edited in place, which happens pre-production. `make db-reset` |
| `make db-reset` does nothing | It asks for confirmation first; answer `y` |
| Migrations fail with `permission denied for schema public` | Only on a Supabase-style database: the `airship` role needs `grant usage, create on schema public` ([stage-0/local.md](../stage-0/local.md)) |
| You want to look at the data and have no `psql` | `make db-psql` goes through the container |
| Two databases, and you are not sure which you are on | `make db-up` is `:5432`; `make supabase-up` is `:54322`. `DATABASE_URL` in `apps/api/.env` decides |

## Code generation

| Symptom | Fix |
| --- | --- |
| `pnpm check:generated` fails | Run `pnpm generate`, **stage the result** (it compares the working tree against the index), and commit what changed |
| A generated file has your hand edits in it | Revert it and change the source instead ([6-making-changes.md](6-making-changes.md#the-one-rule)) |
| `src/env.ts` missing, or the dashboard's env types are stale | `pnpm install` (it runs `varlock codegen`), or `pnpm --filter @airship/dash env:generate` |
| The dashboard's types do not know about a new endpoint | The chain stopped somewhere: `make generate` and watch which step fails |
| sqlc errors on a new query | The `-- name: Foo :one` header is missing or malformed |

## The dashboard

| Symptom | Fix |
| --- | --- |
| A CORS error in the browser console | `CORS_ORIGINS` must contain the dashboard's origin **exactly**. Keep `http://localhost:3000` in the list when you add a tunnel hostname |
| `Sign in with GitHub` is missing, only the development button shows | The API has no `GITHUB_CLIENT_SECRET` — `/v1/auth/config` returns a null `githubSignInUrl` |
| GitHub sign-in returns to the sign-in page | The dashboard and the API are on different sites, so the `SameSite=Lax` cookie is not sent. Use development sign-in, or a named tunnel ([4-github.md](4-github.md#named-tunnel-for-github-sign-in)) |
| Links on the marketing site go to production | Expected. Set `PUBLIC_APP_URL=http://localhost:3000` in `apps/home/.env` |
| A page is blank with no error | Check the API is up (`/healthz`) and that you are signed in — most pages need a session |

## Webhooks and GitHub

| Symptom | Fix |
| --- | --- |
| Deliveries fail with `401` | `GITHUB_WEBHOOK_SECRET` differs from the app's |
| Deliveries fail with `502` or time out | The tunnel is down or its URL changed; `make dev` is not running |
| Deliveries are `200` but nothing appears | Confirm which database the API is using, and that migrations ran |
| No `workflow_job` deliveries at all | The app is not subscribed to **Workflow job** |
| A repository never shows up | The app is installed on a different account, or on a subset that excludes it |

## Launching machines

| Symptom | Fix |
| --- | --- |
| The runner stays `pending` | No cloud connected, or the repository is not activated under **Repositories** |
| Nothing happens and **Events** is empty | The label does not start with `airship-`, so it was never looked up |
| `capacity.reached` | **Settings → Most runners at once** is lower than the queue |
| A cloud error in **Events** | It is the cloud's own message — quota, IAM, or an image that does not exist in that region |
| The machine launches but the job never starts | The runner cannot reach GitHub from its subnet, or the image has no network route out |
| `cache.unavailable` | Run `make cacheproxy` once before `make dev` to embed the proxy binary; or the cloud has no object storage (Azure, Daytona), where it is expected |
| An instance is still running after the job finished | The reconciler is not running: `make dev` stopped, or the database is unreachable. Every machine still powers itself off at `RUNNER_MAX_LIFETIME` |

## Tests

| Symptom | Fix |
| --- | --- |
| Tests are skipped | `TEST_DATABASE_URL` is not exported in that shell. `make test` sets it |
| Tests pass for you and fail on another machine | Where the `CI` environment variable is set, skipped database tests turn into failures. Export `TEST_DATABASE_URL` and rerun |
| `make test-pg17` cannot start | Something owns `:55417`. `make test-pg17 PG17_PORT=55418` |
| A live cloud test is skipped | Its `AIRSHIP_LIVE_*` variables are not set. `make test-live CLOUD=…` loads `apps/api/.env.live` |
| A test is flaky under `-race` | It probably belongs in `internal/fleet` with a deterministic lifecycle assertion rather than a sleep |

## Ports, all in one place

| Port | What |
| --- | --- |
| 3000 | Dashboard |
| 4000 | Marketing site |
| 4100 | Documentation site (`make docs`) |
| 5432 | Postgres (`make db-up`) |
| 5055–5058 | Cloud emulators and stand-ins (moto, GCP, Daytona, Azure) |
| 8080 | API |
| 54322 | Local Supabase Postgres (`make supabase-up`) |
| 55417 | Throwaway Postgres 17 (`make test-pg17`) |

---

Still stuck? `make help` lists every target, and the **Events** page in the
dashboard explains most runner problems in the cloud's own words.
