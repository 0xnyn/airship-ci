# Step 7 — The tests

**What this is.** Every test suite in the repository, what each one needs,
and how to run it. Also what a test is *not* allowed to depend on, which is
why they are fast.

---

## The normal case

```bash
make db-up      # once, if Postgres is not already running
make test       # every package's tests
```

`make test` sets `TEST_DATABASE_URL` to the local Postgres for you. With
pnpm you export it yourself:

```bash
export TEST_DATABASE_URL='postgres://airship:airship@localhost:5432/airship?sslmode=disable'
pnpm test
```

| Command | Runs |
| --- | --- |
| `make test` | Everything, through Turborepo |
| `make test-go` | Only the API's Go tests, **with the race detector** |
| `make test-ts` | Only the TypeScript tests (`node --test`, no framework) |
| `make test-pg17` | The API's Go tests on a throwaway Postgres 17 |

---

## How database tests work

Tests that touch SQL create **their own database per test** from
`TEST_DATABASE_URL`, migrate it, and drop it afterwards
(`internal/testsupport/pgtest`). So they neither see nor disturb your
development data, and they can run in parallel.

| Without `TEST_DATABASE_URL` | |
| --- | --- |
| Normally | Those tests **skip**, with a message |
| Where the `CI` environment variable is set | They **fail** |

That asymmetry is deliberate: on an unattended machine a missing database
should never be mistaken for a pass. **Locally it means a green
`pnpm test` can be hiding every database test** — which is why `make test`
sets the variable for you, and why you should use it rather than
`pnpm test`.

`go test` reads its variables **from the shell, never from
`apps/api/.env`**. If a test behaves differently from `make dev`, that is
usually why.

---

## What stands in for the outside world

Nothing in the default test run talks to the internet. Three fakes make
that possible:

| Fake | Package | Stands in for |
| --- | --- | --- |
| **Postgres** | `internal/testsupport/pgtest` | A real database — not a fake, a real one per test |
| **GitHub** | `internal/testsupport/fakegithub` | `api.github.com`: installations, JIT configs, job and runner endpoints |
| **A cloud** | `internal/cloud/fake` | A `cloud.Provider` that launches and terminates nothing. `fake.WithoutCache` models Azure and Daytona, which have no object storage the cache proxy speaks |

And the layers they are used at:

| Suite | Where | What it covers |
| --- | --- | --- |
| Unit | throughout `internal/` | Config parsing (`internal/runnerconfig`, pure, no I/O), formatting, labels |
| **Lifecycle** | `internal/fleet` | Launching, reconciling, capacity, leases, **races and lifecycle edge cases** — this is where a concurrency bug belongs as a test |
| **End to end** | `internal/e2e` | The whole HTTP surface, driven as a client: sign-in, API tokens, webhooks, the console |
| Providers | `internal/cloud/<cloud>` | Each provider against a fake of that cloud's API, driven through the **real SDK** |
| TypeScript | `apps/dash/src/lib`, `packages/client/src` | Pure helpers: formatting, navigation, error mapping, URL handling |

The provider fakes are worth knowing about, because they mean you can
develop a cloud provider with no cloud account:

| Cloud | Fake | Needs |
| --- | --- | --- |
| AWS | **moto**, an external emulator | Opt-in: `make test-aws-emulator` |
| Google Cloud | `internal/cloud/gcp/gcptest`, a fake of the REST API | Nothing — part of `pnpm test` |
| Azure | `internal/cloud/azure/azuretest`, a fake of Resource Manager | Nothing |
| Daytona | `internal/cloud/daytona/daytonatest`, a fake of the API and toolbox | Nothing |

---

## Postgres 17

Production on Supabase runs **17**; your local Postgres is **18**. A query
or migration that quietly needs an 18-only feature would pass locally and
break a deployment, so:

```bash
make test-pg17                 # throwaway postgres:17-alpine on :55417
make test-pg17 PG17_PORT=55418 # if that port is taken
```

Nothing runs this for you: run it yourself after writing a query or a
migration. It is the cheapest insurance in the repository — two minutes
against a deployment that fails on a database you cannot reproduce.

For a database that matches hosted Supabase more closely still — its own
Postgres 17 build, the same `airship` role, the same lockdown — see
[stage-0/local.md](../stage-0/local.md).

---

## Opt-in suites

### The AWS emulator

```bash
make test-aws-emulator      # starts and stops moto; needs uvx
```

By hand:

```bash
uvx --from 'moto[server]' moto_server -p 5055 &
cd apps/api
AIRSHIP_AWS_ENDPOINT=http://localhost:5055 go test -run TestAgainstEmulator ./internal/cloud/aws
```

### The live cloud suites

**They create real resources that cost real money.** Most clean up after
themselves; Azure's leaves its resource group and network behind, as a real
installation would.

```bash
cp apps/api/.env.live.example apps/api/.env.live   # fill in one cloud
make test-live CLOUD=aws        # or gcp, azure, daytona
```

| Suite | Needs |
| --- | --- |
| `TestAgainstLiveAWS` | `AIRSHIP_LIVE_AWS_ACCESS_KEY_ID`, `_SECRET_ACCESS_KEY`, `_REGION`. `AmazonEC2FullAccess` and a default VPC |
| `TestAgainstLiveGCP` | `AIRSHIP_LIVE_GCP_KEY_FILE` (a service account JSON key, kept outside the repository), `_LOCATION`. Compute Admin, Compute Engine API on |
| `TestAgainstLiveAzure` | `AIRSHIP_LIVE_AZURE_TENANT_ID`, `_CLIENT_ID`, `_CLIENT_SECRET`, `_SUBSCRIPTION_ID`, optionally `_REGION` |
| `TestAgainstLiveDaytona` | `AIRSHIP_LIVE_DAYTONA_API_KEY`, optionally `_API_URL`, `_REGION` |

`make test-live` loads `apps/api/.env.live` if it exists; without it, it
uses what is exported in your shell. Plain
`go test -run <name> ./internal/cloud/<cloud>` only ever uses the shell.
Use a dedicated account or project.

### Windows boot script

One test parses the Windows runner boot script with PowerShell's own
parser. It skips itself unless `pwsh` is on `PATH` or `AIRSHIP_PWSH` points
at it.

---

## Running one test

```bash
cd apps/api
go test -run TestLauncherClaimsOneRunner ./internal/fleet
go test -race -count=1 ./internal/fleet          # no cache, race detector
go test -v ./internal/e2e

# TypeScript
pnpm --filter @airship/dash test
node --test 'apps/dash/src/lib/format.test.ts'
```

`-count=1` is the way to defeat Go's test cache when you are chasing
something flaky.

---

## Writing tests

Go, as the existing ones do:

- `t.Parallel()` where the test allows it.
- Take a database with `pgtest`, which gives you a migrated one and cleans
  up.
- Use `fakegithub` and `internal/cloud/fake` rather than any network.
- Put a concurrency or lifecycle case in `internal/fleet`, and an
  HTTP-surface case in `internal/e2e`.

TypeScript:

- Assertions inside `it()` or `test()`.
- `async`/`await`, never a done callback.
- No `.only` or `.skip` in committed code.
- Keep suites flat; avoid deep `describe` nesting.

---

## Checkpoint

- [ ] `make test` passes on a fresh clone.
- [ ] `make test-pg17` passes.
- [ ] You know why a test skipped instead of running (`TEST_DATABASE_URL`).

Back to [making changes](6-making-changes.md), or on to
[troubleshooting](troubleshooting.md).
