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

```
Ctrl-C            stops make dev; Postgres keeps running in Docker
make db-down      stops Postgres, keeping its data
make db-up        starts it again
make db-reset     deletes the database and migrates a fresh one (asks first)
```

`make db-reset` is the fix for `relation ... does not exist` or a missing
column after pulling: while Airship is pre-production, existing migrations
are sometimes edited in place, and goose only applies migrations it has not
already recorded.

---

## Checkpoint

- [ ] `make dev` runs with no errors.
- [ ] http://localhost:8080/healthz says `"status":"ok"`.
- [ ] http://localhost:3000 signs you in as **Local Developer**, in `acme`.
- [ ] http://localhost:8080/docs lists the API's operations.

Next: [step 3 — the environment files](3-env.md), or skip straight to
[step 6 — making changes](6-making-changes.md) if you only need to work on
level 1 and want to get going.
