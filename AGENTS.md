# Airship

Self-hosted GitHub Actions runners on the customer's AWS, Google Cloud,
Microsoft Azure or Daytona account, built to Cirun's model under Airship's own names
(`airship.yaml`, `airship-` labels, the `.airship` organization repository;
Cirun's names are not read) plus a developer API cirun-py can drive: a Go
control plane that holds
the customer's sealed access key and launches EC2 instances itself, a
Next.js dashboard, and a cache proxy that runs on each machine. Read
`docs/architecture.md` before changing the runner lifecycle or anything in
`internal/fleet`, and `docs/airship-yaml.md` for what the config format must
keep doing. Check the Cirun docs in `../reference/cirun/docs` before
changing user-facing behaviour: parity with them is the goal.

Go + TypeScript monorepo run by Turborepo and pnpm. The Go API is the source
of truth for every type that crosses the network; the TypeScript client is
generated from it and must never be edited by hand.

## Layout

| Path | What it is |
| --- | --- |
| `apps/api` | Go control plane (`cmd/api`), migrations (`cmd/migrate`) and the runner-side cache proxy (`cmd/cacheproxy`): Huma v2 on `net/http`, pgx, sqlc, goose, AWS SDK v2, Google Cloud compute client, Azure SDK for Go (armcompute, armnetwork) |
| `apps/dash` | Next.js 16 dashboard (App Router, React Compiler, Tailwind 4) |
| `apps/home` | Marketing site: Astro 7, static, plain scoped CSS (tokens in `src/layouts/layout.astro`), on :4000. All copy, links and numbers live in `src/content/` (`site.ts`, `home.ts`, `pricing.ts`, `prices.ts`) and flow into the components as props; graphics are drawn in code (`src/components/graphics`). The social image and video (`public/og`) are rendered from `og/card.html` by `pnpm --filter @airship/home og` (Playwright + ffmpeg); rerun it after changing the card |
| `apps/docs` | User-facing documentation: Mintlify, MDX, on :4100 (`make docs`). Navigation and theme in `docs.json`. `api-reference/openapi.json` is **generated** from the API's own document by `scripts/openapi.mjs`, which keeps the `/api` paths and rewrites the handlers' wording for an API audience; `scripts/check.mjs` (`pnpm lint`) checks navigation, links, frontmatter and the names the docs may not use. `apps/docs/README.md` has the rules |
| `packages/client` | `@airship/client`: types generated from the API's OpenAPI document, plus openapi-fetch and TanStack Query helpers |
| `packages/ui` | `@airship/ui`: shared shadcn/ui components and styles |
| `packages/typescript-config` | Shared `tsconfig` presets |
| `deploy/` | Production env templates (`production/`), the Render blueprint (`render/`) and a single-server Docker Compose stack (`compose/`). Guides: `docs/deploy/` — `development/` (a blank machine to a real job: prerequisites, first run, every local env var, test GitHub org and tunnel, the daily workflow, tests, troubleshooting), `stage-0/` (the pilot: Supabase, Render, Vercel, and `env.md` for where each production value comes from), `stage-1/` (AWS), `alternatives/` (GCP, Azure, one server, PlanetScale) |
| `supabase/` | Local Supabase for development (`config.toml`, `seed.sql`): Postgres 17, matching the pilot's hosted project. `make supabase-up`; `docs/deploy/stage-0/local.md` |

## How types flow

```
migrations/*.sql ─┐
internal/db/queries/*.sql ─► sqlc ─► internal/db (Go)          [pnpm generate]
                                         │
Go handlers + request/response structs ─► Huma ─► apps/api/openapi.json
                                                       │
                          openapi-typescript ◄─────────┘
                                 │
packages/client/src/generated/api.ts ─► apps/dash ($api.useQuery, api.GET)
                                 │
                                 └─► apps/docs/api-reference/openapi.json
                                     (the documented subset, Mintlify renders it)
```

`pnpm generate` runs the whole chain in order (Turborepo `^generate`).
`pnpm dev` keeps it live: air rebuilds the API and rewrites `openapi.json`,
the client's watcher regenerates `api.ts`, and Next.js picks up the types.
`pnpm check:generated` fails if any generated file is stale.

## Commands

Run from the repo root. A `Makefile` wraps the same commands (`make help`;
`make setup` for a fresh clone, `make test-pg17` for Supabase's Postgres 17).

| Command | Does |
| --- | --- |
| `pnpm db:up` | Start Postgres in Docker |
| `make supabase-up` | Start a local Supabase Postgres 17 on :54322 instead, matching the pilot's database (`docs/deploy/stage-0/local.md`) |
| `pnpm db:migrate` | Apply migrations (`db:rollback` undoes one) |
| `pnpm db:new <name> sql` | Create a new numbered migration |
| `pnpm dev` | API on :8080 (docs at /docs), dashboard on :3000, live type regeneration |
| `pnpm generate` | sqlc → openapi.json → TypeScript client |
| `pnpm lint` | Ultracite (Biome) for TS/JSON/CSS, golangci-lint for Go |
| `pnpm format` | Auto-fix both |
| `pnpm typecheck` / `pnpm test` / `pnpm build` | Across every package |
| `pnpm check` | Everything to run before pushing |

Filter to one package with `pnpm turbo run <task> --filter=@airship/api`.

## Adding an endpoint

1. Schema change? `pnpm db:new add_thing sql`, write the `-- +goose Up` and
   `-- +goose Down` sections, then `pnpm db:migrate`.
2. Write the query in `apps/api/internal/db/queries/*.sql` with a
   `-- name: Foo :one|:many|:exec|:execrows` header.
3. Add handlers in a feature package under `apps/api/internal/` (see
   `internal/console`): request/response structs with `json`, `doc` and
   validation tags (`minLength`, `maximum`, `enum`, `format`), registered with
   `huma.Register` and a unique camelCase `OperationID`. Register the package
   in `internal/server/server.go`.
4. `pnpm generate` (or just save while `pnpm dev` runs).
5. Call it from the dashboard: `$api.useQuery("get", "/v1/things")`,
   `$api.useMutation("post", "/v1/things")`, or `await unwrap(api.GET(...))`
   in server code.

## Conventions

- API response types are separate from sqlc's `db.*` structs (`fromDB`), so
  the database can change without changing the API.
- Feature packages depend on a narrow `Store` interface, not `*db.Queries`.
- Tests that touch SQL run against real Postgres (`internal/testsupport/pgtest`,
  one database per test, needs `TEST_DATABASE_URL`), a fake GitHub
  (`internal/testsupport/fakegithub`) and a fake cloud (`internal/cloud/fake`);
  `internal/e2e` drives the whole HTTP surface. Races and lifecycle edge
  cases belong in `internal/fleet` tests. The EC2 provider also runs
  against moto (`TestAgainstEmulator`, needs `AIRSHIP_AWS_ENDPOINT`); the
  Compute Engine provider against `internal/cloud/gcp/gcptest`, a fake of the
  REST API driven through the real SDK; the Azure provider against
  `internal/cloud/azure/azuretest`, a fake of Resource Manager driven
  through the real SDK. The Daytona provider runs against
  `internal/cloud/daytona/daytonatest`, a fake of Daytona's API and toolbox
  driven through Daytona's generated Go clients, and opt-in against a real
  organization (`TestAgainstLiveDaytona`, needs `AIRSHIP_LIVE_DAYTONA_API_KEY`).
- Every state change to `runners` is a conditional UPDATE on the expected
  state; never read-then-write. Capacity (`installations.max_runners`) is
  enforced under a row lock on the installation.
- Config files are parsed in `internal/runnerconfig` (pure, no I/O) and
  planned in `internal/fleet/config.go`; see `docs/airship-yaml.md` for the
  format and `docs/architecture.md` for the trust rules.
- Cloud credentials are sealed with `internal/secrets` and opened only for
  the duration of a provider call; never log or return them.
- `internal/cloud` is the provider boundary: `fleet` must not import the
  cloud SDKs. A new cloud is a new `cloud.Provider` (and `cloud.Cacher` if
  it has object storage, `cloud.Sweeper` if a failed launch can leave
  resources behind that the inventory scan should sweep). Azure and
  Daytona have no object storage the proxy speaks, so `cache: true` on
  them launches without the proxy and records a `cache.unavailable` warning;
  the fleet tests register their fakes through `fake.WithoutCache` for that.
- Errors: return `huma.Error404NotFound(...)` and friends for client errors;
  wrap everything else with `fmt.Errorf("...: %w", err)`. Huma renders RFC 9457
  problem details, which the client exposes as `ApiError` / `error.detail`.
- Go struct and type names become OpenAPI schema names and then TypeScript
  names (`Schemas["Profile"]`), so name them for API consumers.
- Config is environment variables only (`internal/config`); local values live
  in `apps/api/.env` (copy `.env.example`). `CREDENTIALS_KEY` is required.
  The dashboard's env is declared and typed in `apps/dash/.env.schema` via varlock.
- Never edit `apps/api/internal/db/*.go` (except `queries/`),
  `apps/api/openapi.json`, `packages/client/src/generated/` or
  `apps/docs/api-reference/openapi.json`.
- `apps/docs` is the product's public voice: it covers AWS, Google Cloud and
  Azure, and names no other vendor or product. `pnpm lint` in that package
  fails if one appears, including in the generated spec.
- Go tools (sqlc, goose, golangci-lint, air) are pinned in
  `apps/api/tools/go.mod` and run with `go tool -modfile=tools/go.mod <tool>`;
  nothing needs a global install besides Go, Node, pnpm and Docker.

## Code standards (TypeScript)

This project uses **Ultracite**, a zero-config Biome preset. Run `pnpm format`
before committing; the pre-commit hook fixes staged files automatically.

### Core Principles

Write code that is **accessible, performant, type-safe, and maintainable**. Focus on clarity and explicit intent over brevity.

#### Type Safety & Explicitness

- Use explicit types for function parameters and return values when they enhance clarity
- Prefer `unknown` over `any` when the type is genuinely unknown
- Use const assertions (`as const`) for immutable values and literal types
- Leverage TypeScript's type narrowing instead of type assertions
- Use meaningful variable names instead of magic numbers - extract constants with descriptive names

#### Modern JavaScript/TypeScript

- Use arrow functions for callbacks and short functions
- Prefer `for...of` loops over `.forEach()` and indexed `for` loops
- Use optional chaining (`?.`) and nullish coalescing (`??`) for safer property access
- Prefer template literals over string concatenation
- Use destructuring for object and array assignments
- Use `const` by default, `let` only when reassignment is needed, never `var`

#### Async & Promises

- Always `await` promises in async functions - don't forget to use the return value
- Use `async/await` syntax instead of promise chains for better readability
- Handle errors appropriately in async code with try-catch blocks
- Don't use async functions as Promise executors

#### React & JSX

- Use function components over class components
- Call hooks at the top level only, never conditionally
- Specify all dependencies in hook dependency arrays correctly
- Use the `key` prop for elements in iterables (prefer unique IDs over array indices)
- Nest children between opening and closing tags instead of passing as props
- Don't define components inside other components
- Use semantic HTML and ARIA attributes for accessibility:
  - Provide meaningful alt text for images
  - Use proper heading hierarchy
  - Add labels for form inputs
  - Include keyboard event handlers alongside mouse events
  - Use semantic elements (`<button>`, `<nav>`, etc.) instead of divs with roles

#### Error Handling & Debugging

- Remove `console.log`, `debugger`, and `alert` statements from production code
- Throw `Error` objects with descriptive messages, not strings or other values
- Use `try-catch` blocks meaningfully - don't catch errors just to rethrow them
- Prefer early returns over nested conditionals for error cases

#### Code Organization

- Keep functions focused and under reasonable cognitive complexity limits
- Extract complex conditions into well-named boolean variables
- Use early returns to reduce nesting
- Prefer simple conditionals over nested ternary operators
- Group related code together and separate concerns

#### Security

- Add `rel="noopener"` when using `target="_blank"` on links
- Avoid `dangerouslySetInnerHTML` unless absolutely necessary
- Don't use `eval()` or assign directly to `document.cookie`
- Validate and sanitize user input

#### Performance

- Avoid spread syntax in accumulators within loops
- Use top-level regex literals instead of creating them in loops
- Prefer specific imports over namespace imports
- Avoid barrel files (index files that re-export everything)
- Use proper image components (e.g., Next.js `<Image>`) over `<img>` tags

#### Framework-Specific Guidance

**Next.js:**

- Use Next.js `<Image>` component for images
- Use `next/head` or App Router metadata API for head elements
- Use Server Components for async data fetching instead of async Client Components

**React 19+:**

- Use ref as a prop instead of `React.forwardRef`

**Solid/Svelte/Vue/Qwik:**

- Use `class` and `for` attributes (not `className` or `htmlFor`)

---

### Testing

- Write assertions inside `it()` or `test()` blocks
- Avoid done callbacks in async tests - use async/await instead
- Don't use `.only` or `.skip` in committed code
- Keep test suites reasonably flat - avoid excessive `describe` nesting

### When Biome Can't Help

Biome will catch most mechanical issues automatically. Focus your attention on:

1. **Business logic correctness** - Biome can't validate your algorithms
2. **Meaningful naming** - Use descriptive names for functions, variables, and types
3. **Architecture decisions** - Component structure, data flow, and API design
4. **Edge cases** - Handle boundary conditions and error states
5. **User experience** - Accessibility, performance, and usability considerations
6. **Documentation** - Add comments for complex logic, but prefer self-documenting code

---

Most formatting and common issues are automatically fixed by Biome. Run `pnpm exec ultracite fix` before committing to ensure compliance.
