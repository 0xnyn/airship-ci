# Step 2 — The first run

**What this is.** Clone to a working dashboard, what each command actually
does, and a tour of what you are looking at.

**Time.** 10 minutes, most of it `pnpm install`.

**Prerequisite.** [Step 1](1-prerequisites.md): `make doctor` passes.

---

## Clone and set up

```bash
git clone <this repository> airship && cd airship
make setup
make dev
```

`make setup` is five steps you can also run by hand, which is worth reading
once so the failures make sense later:

| | Command | What it does |
| --- | --- | --- |
| 1 | `make doctor` | Go, Node, pnpm and Docker present and recent enough |
| 2 | `pnpm install` | Installs every package in the workspace. Also runs `varlock codegen`, which writes `apps/dash/src/env.ts` — generated, and gitignored |
| 3 | `make env` | Copies each `.env.example` to the real file **if it does not already exist**. Never overwrites, so it is safe to rerun after pulling |
| 4 | `pnpm db:up` | Starts Postgres 18 in Docker on `:5432` and waits for it to be healthy |
| 5 | `pnpm db:migrate` | Applies the migrations |

The env files it creates need **no editing** for level 1: the API's comes
with `DEV_LOGIN=true` and a working (public) credentials key.
[Step 3](3-env.md) goes through every variable in them, including the ones
that are empty on purpose.

---

## What `make dev` runs

One command, four processes, through Turborepo:

| Process | Port | What it does on every save |
| --- | --- | --- |
| **The API**, under [air](https://github.com/air-verse/air) | 8080 | Rebuilds the Go binary on every `.go` or `.sql` change, and rewrites `apps/api/openapi.json` |
| **The client generator** | — | Watches `openapi.json` and regenerates `packages/client/src/generated/api.ts` |
| **Next.js** | 3000 | Picks up those new types, so a Go change reaches the dashboard's types with no manual step |
| **Astro** | 4000 | The marketing site |

That chain is the thing to understand about this repository: **you change
Go, and TypeScript types follow.** [6-making-changes.md](6-making-changes.md)
covers it properly.

To run one piece: `make dev-api` (the API plus the client generator),
`make dev-dash`, `make dev-home`, or `make docs` for the documentation site
on `:4100`.

---

## Where everything is

| URL | What |
| --- | --- |
| http://localhost:3000 | **The dashboard.** Choose **Development sign-in** |
| http://localhost:8080/docs | The API reference, generated from the same OpenAPI document as the TypeScript client |
| http://localhost:8080/healthz | `{"status":"ok","database":"ok","version":"dev"}` |
| http://localhost:4000 | The marketing site |
| http://localhost:4100 | The documentation site, only with `make docs` |

---

## A tour of the dashboard

Click **Development sign-in**. There is no password: it signs you in as a
local user called `dev` ("Local Developer") and makes you an admin of every
installation in the database. When there are none — your first run — it
creates a demo organization called **`acme`** so there is something to look
at.

Worth knowing now: this works *in addition* to real GitHub sign-in. After
[step 4](4-github.md) installs the app on a real organization, development
sign-in makes you an admin of **that** too, which saves going through
GitHub every time you restart.

What you will see, and why most of it is empty:

| Page | On a fresh database | Fills up when |
| --- | --- | --- |
| **Repositories** | Empty | A real GitHub App is installed ([step 4](4-github.md)). Repositories must be **activated** here before Airship will launch anything for them |
| **Cloud** | Nothing connected | You connect a credential ([step 5](5-real-machines.md)). This is where a customer's AWS key goes — sealed, and never shown again |
| **Jobs** | Empty | A `workflow_job` webhook arrives from GitHub |
| **Runners** | Empty | A job matches an `airship-` label and a machine is launched |
| **Events** | Empty | Anything happens. **This is the page to read when something does not work**: every launch, failure and cache warning lands here with the cloud's own error message |
| **Settings** | `Most runners at once` | — it is the hard ceiling on live runners, enforced under a row lock |
| **API keys** | Empty | You create one, for the developer API that `cirun-py` drives |
| **Teams**, **Analytics** | Empty | Memberships and jobs exist |

Nothing here can launch a machine yet: there is no cloud credential and no
real GitHub installation. That is the whole point of levels 2 and 3.

---

## Looking at the database

```bash
make db-psql           # psql in the container, no local psql needed
\dt                    # the tables
select * from installations;
\q
```

The schema is one migration, `apps/api/migrations/00001_init.sql`, and it
is readable: installations, repositories, runners, jobs, events, users,
sessions, cloud credentials.

---

## Stopping and starting

`Ctrl-C` stops `make dev`. It does **not** stop Docker: the database keeps
running, which is usually what you want, because the next `make dev` finds
it already up.

```bash
make down      # stops every local container, keeping the data
```

That is the one to remember, because the obvious alternative is a trap.
`make db-down` is `docker compose down`, so it only knows about the
Postgres in `docker-compose.yml`. Local Supabase is run by the Supabase
CLI instead, so Compose cannot see its containers and `make db-down`
leaves them running — successfully, with no hint that it missed anything.
`make down` stops both, and the pg17 container an interrupted
`make test-pg17` can leave behind.

| Started with | Stopped by | Keeps the data |
| --- | --- | --- |
| `make db-up` — Postgres 18 on `:5432` | `make db-down`, `make down` | yes |
| `make supabase-up` — Postgres 17 on `:54322` ([stage-0/local.md](../stage-0/local.md)) | `make supabase-down`, `make down` | yes |

`docker ps` is the second opinion: `make down` prints what is still up
when it finishes, and a `supabase_db_airship` or `airship-postgres-1` in
that list means one did not stop.

---

## Starting over

Two levels, and the difference matters because the gentler one is easy to
mistake for the other: **reset** empties the database and leaves it
running, **destroy** removes the volume so nothing is left on the machine.

| | Does | Asks first |
| --- | --- | --- |
| `make db-reset` | Recreates the Compose Postgres, empty, and migrates | yes |
| `make supabase-reset` | Recreates the Supabase database, empty, re-seeds and migrates | **no** — the CLI resets a local database immediately |
| `make db-destroy` | Stops the Compose Postgres and deletes its volume | yes |
| `make supabase-destroy` | Stops Supabase, deletes its volume and the CLI's local state | yes |

`make db-reset` is the fix for `relation ... does not exist` or a missing
column after pulling: while Airship is pre-production, existing migrations
are sometimes edited in place, and goose only applies migrations it has not
already recorded.

The destroy pair exists because stopping is not deleting. `make down` and
`make supabase-down` deliberately keep the data — Supabase even says so,
printing `"backup":true` — so the volume outlives the containers and the
next `make supabase-up` brings the old rows back. If that is not what you
wanted, `make supabase-destroy` is the command, and it is safe to run when
the database is already stopped.

Nothing in either database is precious. `make db-up db-migrate` rebuilds
the Compose one from the migrations, `make supabase-up` rebuilds the
Supabase one, and development sign-in recreates the `acme` demo
organization on your next visit.

---

## Checkpoint

- [ ] `make dev` runs with no errors.
- [ ] http://localhost:8080/healthz says `"status":"ok"`.
- [ ] http://localhost:3000 signs you in as **Local Developer**, in `acme`.
- [ ] http://localhost:8080/docs lists the API's operations.

Next: [step 3 — the environment files](3-env.md), or skip straight to
[step 6 — making changes](6-making-changes.md) if you only need to work on
level 1 and want to get going.
