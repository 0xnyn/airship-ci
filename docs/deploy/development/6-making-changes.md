# Step 6 — Making changes

**What this is.** What you actually do, from "I want to change something"
to "it is merged": where code goes, how generated code works, migrations,
and what to run before you push.

**There is no CI in this repository.** Nothing runs checks for you when you
push, so `make check` before pushing is the whole safety net. Keep that in
mind while reading.

**Audience.** Anyone about to touch the code. Read the first two sections
even if you skip the rest.

---

## The one rule

**The Go API is the source of truth for every type that crosses the
network.** The TypeScript client is generated from it. So you never edit
these by hand — `make check-generated` fails if they are stale or
hand-edited:

| Never edit | Generated from | By |
| --- | --- | --- |
| `apps/api/internal/db/*.go` (except `queries/`) | `migrations/*.sql` + `internal/db/queries/*.sql` | sqlc |
| `apps/api/openapi.json` | the Go handlers and their request/response structs | Huma |
| `packages/client/src/generated/` | `openapi.json` | openapi-typescript |
| `apps/docs/api-reference/openapi.json` | `openapi.json` | `apps/docs/scripts/openapi.mjs` |
| `apps/dash/src/env.ts` | `apps/dash/.env.schema` | varlock, at install |

```
migrations/*.sql ─┐
queries/*.sql ────┴─► sqlc ──► internal/db (Go)
                                   │
Go handlers + structs ─► Huma ──► apps/api/openapi.json
                                       │
          openapi-typescript ◄─────────┤
                  │                    └──► apps/docs/api-reference/openapi.json
   packages/client/src/generated/api.ts
                  │
                  └──► apps/dash  ($api.useQuery, api.GET)
```

`pnpm generate` runs that whole chain in order. While `make dev` is
running, it happens on save.

---

## The daily loop

```bash
make dev          # leave it running: it rebuilds and regenerates on save
# ... edit ...
make check        # before you push: lint, typecheck, test, generated files
```

`make check` is lint, typecheck, tests and the generated-file check in one.
Nothing else will run them for you, so run it before every push. Individual
pieces, when `check` is too slow to iterate on:

| Command | Runs |
| --- | --- |
| `make lint` / `make format` | Biome (TS, JSON, CSS, Markdown) and golangci-lint. `format` fixes what it can |
| `make typecheck` | `tsc` and `go vet` |
| `make test` / `make test-go` / `make test-ts` | Everything / only Go (with the race detector) / only TypeScript |
| `make generate` | The chain above |
| `make check-generated` | Fails if a generated file is stale |

**Caveat 1: `check-generated` compares the working tree against the index.**
So the order is: `pnpm generate`, then **stage** what changed
(`git add -A`), then run it. Unstaged regenerated files look like drift.

**Caveat 2: a database is needed for most Go tests.** `make test` sets
`TEST_DATABASE_URL` for you; `pnpm test` does not
([7-tests.md](7-tests.md)).

---

## Adding an endpoint, end to end

The shape of most changes in this repository. Say you are adding something
the dashboard can list.

### 1. A migration, if the schema changes

```bash
make db-new NAME=add_thing     # creates apps/api/migrations/0000N_add_thing.sql
```

Write both halves. goose needs them, and a `Down` you have not run is a
`Down` that does not work — `make db-rollback` then `make db-migrate`
proves it:

```sql
-- +goose Up
create table things (
    id uuid primary key default uuidv7(),
    installation_id bigint not null references installations(id) on delete cascade,
    name text not null,
    created_at timestamptz not null default now()
);

-- +goose Down
drop table things;
```

```bash
make db-migrate        # apply it
make db-status         # what is applied
make db-rollback       # undo the last one
```

`uuidv7()` is built into Postgres 18; the first migration defines an
equivalent when it is missing, so it works on 17 too — use it for every
primary key.

> **While Airship is pre-production**, existing migrations are sometimes
> edited in place rather than added to. goose only applies migrations it
> has not recorded, so after pulling such a change your database is stale
> in a way nothing will tell you about: `make db-reset`.

### 2. The query

In `apps/api/internal/db/queries/*.sql`, with a name header that tells sqlc
what to generate:

```sql
-- name: ListThings :many
select * from things where installation_id = $1 order by created_at desc;
```

`:one`, `:many`, `:exec`, `:execrows`. Then `make generate` writes the
typed Go method.

### 3. The handler

In a feature package under `apps/api/internal/` — read
`internal/console` first; it is the model to copy.

```go
// Thing is what the API returns. Note: NOT db.Thing.
type Thing struct {
    ID        string    `doc:"Identifier"  json:"id"       format:"uuid"`
    Name      string    `doc:"Display name" json:"name"    maxLength:"64"`
    CreatedAt time.Time `doc:"When it was created" json:"createdAt"`
}

huma.Register(api, huma.Operation{
    OperationID: "listThings",   // unique, camelCase: it becomes the client's method name
    Method:      http.MethodGet,
    Path:        "/v1/things",
    Summary:     "List things",
    Tags:        []string{"Things"},
}, h.listThings)
```

Then register the package in `internal/server/server.go`.

Four conventions that are not optional:

| | |
| --- | --- |
| **API types are separate from sqlc's** | Convert with a `fromDB` function. The database can then change without changing the API |
| **Feature packages depend on a narrow `Store` interface**, not `*db.Queries` | It keeps the package testable and its dependencies visible |
| **Names become API names** | A Go struct name becomes an OpenAPI schema name and then a TypeScript name (`Schemas["Thing"]`). Name them for API consumers, not for the database |
| **Errors**: `huma.Error404NotFound(...)` and friends for client errors; wrap everything else with `fmt.Errorf("...: %w", err)` | Huma renders RFC 9457 problem details, which the client surfaces as `ApiError` / `error.detail` |

Validation lives in the struct tags — `minLength`, `maximum`, `enum`,
`format`, `pattern` — so it appears in the OpenAPI document and in the
generated types, and is enforced before your handler runs.

### 4. Regenerate

```bash
make generate     # or just save, with make dev running
```

### 5. Call it from the dashboard

```ts
const things = $api.useQuery("get", "/v1/things");
const create = $api.useMutation("post", "/v1/things");
const data = await unwrap(api.GET("/v1/things"));   // in server code
```

The paths and types are checked against the generated client, so a typo
is a compile error rather than a 404 at runtime.

---

## Where state changes are special

Two rules in `internal/fleet` exist because several API replicas run at
once, and breaking them causes bugs that only appear under load:

- **Every state change to `runners` is a conditional `UPDATE` on the
  expected state.** Never read-then-write.
- **Capacity (`installations.max_runners`) is enforced under a row lock on
  the installation**, so two replicas can never both take the last slot.

Races and lifecycle edge cases belong in `internal/fleet`'s tests. Read
[architecture.md](../../architecture.md) before changing the
runner lifecycle, and
[airship-yaml.md](../../airship-yaml.md) before changing config parsing.

A new cloud is a new `cloud.Provider` in `internal/cloud` — and `fleet`
must not import a cloud SDK. That boundary is what keeps the lifecycle
testable against fakes.

---

## Formatting, linting and commits

Formatting is not a matter of taste here:

| | |
| --- | --- |
| TypeScript, JSON, CSS, Markdown | **Ultracite**, a zero-config Biome preset. `make format` fixes, `make lint` checks |
| Go | `gofmt` and `golangci-lint` |
| **On commit** | A husky `pre-commit` hook runs `lint-staged`: Ultracite fixes staged TS/JSON/CSS/MD files, `gofmt -w` rewrites staged Go files |

Because the hook **rewrites files that are already staged**, check the
result (`git status`) if a commit looks different from what you staged.

> A formatter running over a file with unresolved merge-conflict markers
> can mangle it. Resolve conflicts first, then commit.

Commit messages follow Conventional Commits, as the history shows —
`feat:`, `fix:`, `docs:`, `refactor:`, `chore:`, with an optional scope
(`feat(dev): …`). Nothing enforces it mechanically; the history is easier
to read when it is kept up.

---

## Before you push

Branch off `main`, and run these yourself — nothing runs them for you:

| | Command | Catches |
| --- | --- | --- |
| Always | `make check` | Lint, typecheck, every test, and stale generated files |
| Touched a **query or a migration** | `make test-pg17` | Something only Postgres 18 has. Production runs **17** on Supabase, so this is the check that stops a deployment breaking while local tests pass |
| Touched a **migration** | `make db-rollback && make db-migrate` | A `Down` section that does not work |
| Touched the **AWS provider** | `make test-aws-emulator` | The provider's real SDK calls, against moto |
| Touched the **dashboard build** | `make build` | A type or build error Next.js only finds in a production build |
| Touched **`apps/docs`** | `make docs-check` | Broken navigation, links, frontmatter, and vendor names the docs may not use |

`make check` plus the one or two rows that apply is a couple of minutes,
and it is the only thing between a mistake and `main`.

The first thing a broken `main` breaks is a **deployment**: Render builds
the API image and runs the migration on every push to `main`
([stage-0/operate.md](../stage-0/operate.md)), and a failed pre-deploy
migration stops that deploy. It will not corrupt anything — the previous
version keeps serving — but you will find out from a deploy log rather
than from a check.

---

## Documentation that goes with a change

| Change | Also update |
| --- | --- |
| A new or changed endpoint | Nothing by hand: the OpenAPI document and the docs' copy of it are generated |
| A config-file field | [airship-yaml.md](../../airship-yaml.md) |
| Runner lifecycle or trust rules | [architecture/](../../architecture/) |
| A new environment variable | `apps/api/.env.example`, `deploy/production/api.env.example`, [3-env.md](3-env.md) and [stage-0/env.md](../stage-0/env.md). **Every variable the code reads belongs in a template**, with its default |
| User-facing behaviour | `apps/docs` (Mintlify). `pnpm lint` there checks navigation, links, frontmatter — and **refuses vendor names** other than AWS, Google Cloud and Azure. `make docs` previews it on :4100 |

---

## Cheat sheet

```bash
make dev                      # the loop
make db-new NAME=add_thing    # a migration
make generate                 # sqlc → openapi.json → client
make format                   # fix formatting
make check                    # everything, before a push
make db-reset                 # start the database over
make help                     # every target

pnpm turbo run test --filter=@airship/api            # one task, one package
make images NEXT_PUBLIC_API_URL=https://api.example.com   # both production images
make clean                    # build output and Turborepo caches
```

Next: [step 7 — the tests](7-tests.md).
