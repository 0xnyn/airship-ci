# Local development against Supabase

**What this is.** A local database that matches the pilot's: Supabase's own
Postgres 17, with the same `airship` role and the same lockdown. Run
Airship against it when you want a pilot problem to reproduce on your
machine.

**Audience.** Anyone working on the code who also has to keep the pilot
alive.

**You do not need this for everyday work.** The default local Postgres is
simpler and faster to start. Reach for this when the difference matters.

---

## Which local database?

| | `make db-up` (default) | `make supabase-up` |
| --- | --- | --- |
| What it is | `postgres:18-alpine` from `docker-compose.yml` | The Supabase CLI's stack, with everything but the database switched off |
| Version | Postgres **18** | Postgres **17**, the version hosted Supabase runs |
| Port | 5432 | **54322** |
| Connects as | `airship` (the image's only role) | `airship`, created by `supabase/seed.sql` exactly as [step 3](3-supabase.md#step-2--a-role-for-airship) creates it |
| Containers | 1 | 2: the database, plus the CLI's gateway, which fronts nothing. Auth, Storage, Realtime, Studio, the Data API, the pooler and the rest are off in `supabase/config.toml`, and `supabase status` lists them as stopped |
| Start time | seconds | seconds, after a one-off image pull |
| Use it for | Everything, by default | Reproducing something the pilot does: a Postgres 17 behaviour, an ownership or grant problem, a migration that 18 tolerates and 17 does not |

Both can run at the same time — different ports — so switching is a change
of one line in `apps/api/.env`.

---

## Install the CLI

| | |
| --- | --- |
| macOS | `brew install supabase/tap/supabase` |
| Windows | `scoop install supabase` |
| Anywhere | Nothing: the Makefile falls back to `pnpm dlx supabase`, which fetches it on demand |

A `supabase` on `PATH` is used if present, because `pnpm dlx` re-resolves
the package on every call. Override it with
`make supabase-up SUPABASE=/path/to/supabase`.

Docker has to be running, as it does for the default database.

---

## Start it

```bash
make supabase-up
```

That runs `supabase start` and then applies the migrations as the `airship`
role, printing the connection string. The first run pulls the database
image, which takes a few minutes.

Point the API at it, in `apps/api/.env`:

```bash
DATABASE_URL=postgres://airship:airship@127.0.0.1:54322/postgres?sslmode=disable
```

Then `make dev` as usual. Nothing else changes: the dashboard, the API's
docs and `DEV_LOGIN` all work the same.

| `make` | Does |
| --- | --- |
| `make supabase-up` | Start it and migrate |
| `make supabase-down` | Stop it, keeping the data |
| `make supabase-reset` | Throw the data away, recreate the database, re-seed and migrate — the equivalent of `make db-reset` |
| `make supabase-psql` | A SQL prompt on it, through local `psql` or the container |

`make supabase-reset` is what to run after pulling a change that rewrote an
existing migration, which happens while Airship is pre-production: goose
only applies migrations it has not recorded, so an edited one never reaches
an existing database.

---

## What this does and does not reproduce

| | Local | Hosted (the pilot) |
| --- | --- | --- |
| Postgres version | 17, the same major | 17 |
| `uuidv7()` from the first migration, not built in | yes | yes |
| Migrations run as `airship`, which owns every table | yes, from `supabase/seed.sql` | yes, from step 3 |
| A session-per-client connection, so advisory locks hold | yes (a direct connection) | yes (**the session pooler on 5432**) |
| Data API reachable | no — off in `config.toml` | no — off in step 3 |
| TLS, `sslmode=require` | no, `disable` over loopback | yes |
| The `airship.<ref>` user-name form | no, plain `airship` | yes, the pooler requires it |
| Pool-size limits, network restrictions, backups | no | yes |

The last two rows are the gap. A connection string that works locally can
still fail hosted on the user name, TLS or a network restriction — which is
why [step 3](3-supabase.md#step-6--apply-the-schema) checks the hosted
string with `psql` before Render ever uses it.

---

## Running the tests on 17

The tests take their database from `TEST_DATABASE_URL`, never from
`apps/api/.env`:

```bash
# Against this Supabase database
TEST_DATABASE_URL='postgres://airship:airship@127.0.0.1:54322/postgres?sslmode=disable' pnpm test
```

Tests create one database per test on that server, so they neither see nor
disturb your development data.

For a clean 17 check there is also `make test-pg17`, which starts a
throwaway `postgres:17-alpine` on :55417, runs the API's Go tests against
it and removes it. Use `test-pg17` for "does this work on 17", and this
Supabase database for "does this work the way the pilot's database does".
Run one of them before pushing anything that touches SQL — nothing checks
it for you.

---

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `command not found: supabase` | Install it, or let the fallback work — the Makefile uses `pnpm dlx supabase` when nothing is on `PATH` |
| `Docker is not running` | Start Docker Desktop, OrbStack or Colima |
| Port 54322 is in use | Another project's Supabase is running. `supabase stop --project-id <other>`, or change `port` in `supabase/config.toml` |
| `role "airship" already exists` on reset | Harmless: `supabase/seed.sql` guards against it. If you see it, the seed was edited |
| `permission denied for schema public` when migrating | The seed did not run. `make supabase-reset` |
| The API starts but every query fails | `apps/api/.env` still points at :5432 while only Supabase is up, or the reverse |
| `supabase start` pulls images every time | Expected on the first run per CLI version; after that they are cached |

---

## Switching back

```bash
make supabase-down
```

and restore `apps/api/.env`:

```bash
DATABASE_URL=postgres://airship:airship@localhost:5432/airship?sslmode=disable
```

Both databases keep their data between restarts, so you can move between
them without re-seeding.

The rest of the local setup — the three levels, the development GitHub App,
tunnels, the emulators — is in [development/](../development/).
