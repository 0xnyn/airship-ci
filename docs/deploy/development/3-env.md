# Step 3 — The environment files

**What this is.** Every environment variable this repository reads in
development: which file it belongs in, **what to put in it locally**, and
why so many of them are empty in the templates.

**The short version: for level 1 you do not have to edit anything.**
`make env` creates files that already work. This page is for when you reach
level 2, or when something is not behaving and you want to know which
variable owns it.

> Deploying for real reads the same variables from different places. Where
> their **production** values come from is
> [stage-0/env.md](../stage-0/env.md).

---

## Which file is which

| File | Read by | Committed | Created by `make env` |
| --- | --- | --- | --- |
| `apps/api/.env.example` | Nobody. The template, listing every variable the API reads | yes | |
| **`apps/api/.env`** | The API and `migrate` (via godotenv), so `make dev` and `make db-migrate`. **Real environment variables win over it.** Never read by `go test` | no | yes |
| `apps/dash/.env.schema` | varlock: the types, validation and defaults for the dashboard's env | yes | |
| `apps/dash/.env.example` | Nobody. The template for `.env.local` | yes | |
| **`apps/dash/.env.local`** | varlock, over the schema's defaults. Only needed when the API is not on `localhost:8080` | no | yes |
| `apps/home/.env.example` | Nobody. The template, every line commented out | yes | |
| **`apps/home/.env`** | Astro, at dev and build time. Only to point the site's links at your local dashboard | no | yes |
| `apps/api/.env.live.example` | Nobody. The template for the live cloud suites' credentials | yes | |
| **`apps/api/.env.live`** | `make test-live` only. Never the API, `make dev` or plain `go test` | no | no — opt in by copying |
| `deploy/production/*.env.example` | Nobody. What a deployment sets ([stage-0/env.md](../stage-0/env.md)) | yes | |
| `deploy/compose/.env.example` | Nobody. The template for the one-server stack ([alternatives/server.md](../alternatives/server.md)) | yes | |

**Nothing reads any other env file.** A `.env.local` at the repository root,
for instance, reaches no code. `.gitignore` keeps every real one out of git
(`.env*`, with the `*.example` and `.env.schema` templates allowed back
in), and the Docker builds ignore `.env.*` and `*.pem`, so neither reaches
an image.

---

## Why most of them are empty

Three different reasons, and it matters which:

| In the template | Means | Example |
| --- | --- | --- |
| A real value | Already correct for local development. Leave it | `DATABASE_URL`, `DEV_LOGIN=true`, `CREDENTIALS_KEY` |
| **Commented out** with `#` | Optional. The code has a default, and uncommenting it *changes* behaviour | `RUNNER_VERSION`, `GITHUB_APP_SLUG`, every `*_ENDPOINT` |
| `KEY=` with nothing after it | Required in production, deliberately blank here so nobody ships a shared secret | only in `deploy/production/api.env.example`, never in the local templates |

So an empty-looking local file is not an unfinished one. The rule in
`apps/home/.env.example` is worth repeating, because it is the one place
empty bites: **leave a line commented rather than empty.** `PUBLIC_APP_URL=`
sets the value to the empty string and replaces the default; `#
PUBLIC_APP_URL=…` leaves the default alone.

---

## `apps/api/.env` — the one that matters

### Core

| Variable | Put this | Notes |
| --- | --- | --- |
| `APP_ENV` | `development` | The only value that allows `DEV_LOGIN` and plain-http URLs. The API refuses to start with `DEV_LOGIN` set under any other value |
| `PORT` | `8080` | |
| `DATABASE_URL` | `postgres://airship:airship@localhost:5432/airship?sslmode=disable` | Matches `docker-compose.yml` (`make db-up`). For the Supabase-matching Postgres 17 instead, `postgres://airship:airship@127.0.0.1:54322/postgres?sslmode=disable` ([stage-0/local.md](../stage-0/local.md)) |
| `LOG_LEVEL` | `info` | `debug` logs every GitHub call — useful when webhooks misbehave. Logs are human-readable text in development, JSON elsewhere |

### URLs

| Variable | Put this | Change it when |
| --- | --- | --- |
| `PUBLIC_URL` | `http://localhost:8080` | [Step 4](4-github.md): it becomes your tunnel's https URL, because GitHub and runner machines have to reach it from the internet |
| `DASH_URL` | `http://localhost:3000` | Only with a named tunnel for the dashboard too |
| `CORS_ORIGINS` | `http://localhost:3000` | Comma-separated. Keep `http://localhost:3000` in the list even when you add a tunnel hostname, or your local dashboard stops working |

### Sign-in

| Variable | Put this | Notes |
| --- | --- | --- |
| `DEV_LOGIN` | `true` | The password-less **Development sign-in** button. Signs you in as `dev` and makes you an admin of every installation. **Startup fails if this is set outside `APP_ENV=development`** — that guard is deliberate |

### Credential sealing

| Variable | Put this | Notes |
| --- | --- | --- |
| `CREDENTIALS_KEY` | The value already in the template | A base64 32-byte key that seals cloud credentials with AES-GCM. **The one in `.env.example` is public**, and the API refuses to start with it when `APP_ENV` is not `development`, so it can never seal a real customer's key. Generate your own with `openssl rand -base64 32` if you prefer |

If you change this key, every credential already stored locally becomes
unreadable — reconnect the cloud, or `make db-reset`.

### The GitHub App — empty until step 4

Commented out in the template on purpose: without them the dashboard still
works through `DEV_LOGIN`, and only GitHub sign-in, webhooks and launching
are off.

| Variable | Where its value comes from |
| --- | --- |
| `GITHUB_APP_SLUG` | Your app's URL: `github.com/settings/apps/`**`airship-dev-you`** |
| `GITHUB_CLIENT_ID` | The app's **About → Client ID**, `Iv23li…`. Not the numeric App ID |
| `GITHUB_CLIENT_SECRET` | **Client secrets → Generate a new client secret**. Shown once |
| `GITHUB_WEBHOOK_SECRET` | The `openssl rand -hex 32` you paste into the app's **Webhook secret** field |
| `GITHUB_PRIVATE_KEY_FILE` | `./github-app.pem` — the key you download, saved in `apps/api/`. Paths are relative to `apps/api`. `*.pem` is gitignored |
| `GITHUB_PRIVATE_KEY` | The alternative: the PEM itself, on one line with literal `\n`. Use the `_FILE` form locally; it is less error-prone |
| `GITHUB_API_URL`, `GITHUB_WEB_URL` | Leave commented. Only for GitHub Enterprise Server, or a fake GitHub in tests |

[Step 4](4-github.md) creates the app and fills these in.

### Runner lifecycle

All commented out, all with sensible defaults. Two are worth knowing while
experimenting:

| Variable | Default | Why you might set it locally |
| --- | --- | --- |
| `RUNNER_MAX_LIFETIME` | `8h` | **Set it to `1h`.** It is the deadline a machine enforces on *itself*, so it is your insurance against leaving an instance running after you close your laptop |
| `RUNNER_IDLE_TIMEOUT` | `10m` | How long a launched runner waits for a job before it is reaped |
| `RUNNER_VERSION` | `2.338.2` | The `actions/runner` release machines install |
| `RECONCILE_INTERVAL` | `15s` | How often finished machines are terminated and idle ones reaped. Lower it to watch the reconciler work |
| `LAUNCH_INTERVAL` | `2s` | A webhook wakes the launcher anyway |

### Cloud emulators

| Variable | For |
| --- | --- |
| `AWS_ENDPOINT` | moto, the AWS emulator, on `:5055` |
| `GCP_ENDPOINT`, `AZURE_ENDPOINT`, `DAYTONA_ENDPOINT` | Stand-in APIs on `:5056`, `:5058`, `:5057` |

Leave them commented unless you are deliberately pointing a provider at a
fake. **A deployment that sets one of these launches nothing and reports no
error**, which is why the production checklist calls them out.

---

## `apps/dash/.env.local`

One variable, and only when the API is not on `localhost:8080`:

| Variable | Put this |
| --- | --- |
| `NEXT_PUBLIC_API_URL` | `http://localhost:8080`, or your tunnel's https URL in [step 4](4-github.md)'s named-tunnel setup |

Two things make this one unusual:

- It is **inlined into the JavaScript when `next build` runs**, not read at
  runtime. In `make dev` that is invisible; in a build, changing it needs a
  rebuild.
- The dashboard's env is **declared and typed** in `apps/dash/.env.schema`,
  which varlock validates at startup and at build, and from which it
  generates `apps/dash/src/env.ts`. A missing or malformed value fails
  loudly rather than becoming `undefined` at runtime. A production build
  refuses a `localhost` value on purpose.

The dashboard holds **no secrets**. Every privileged call goes to the API
with the session cookie.

---

## `apps/home/.env`

Four optional `PUBLIC_*` values, all commented out, which only change
where the marketing site's buttons point:

| Variable | Set it to | Effect |
| --- | --- | --- |
| `PUBLIC_APP_URL` | `http://localhost:3000` | The pricing plans' "Start for free" and "Connect a cloud" go to your local dashboard instead of production |
| `PUBLIC_DOCS_URL` | `http://localhost:4100` | Docs links go to your local Mintlify preview |
| `PUBLIC_API_URL` | `http://localhost:8080` | The developer-API example on the page |
| `PUBLIC_CAL_URL` | Another Cal booking page | "Get started" and "Talk to us" book a call there instead of `https://cal.com/airship/quick-chat` |

Astro inlines `PUBLIC_*` at build time, so a static build keeps whatever
was set when it was built.

---

## Variables in no file, on purpose

| Variable | Set by |
| --- | --- |
| `NODE_ENV` | Next.js, and Vercel |
| `CI` | Any automated runner that sets it. It makes database tests **fail** instead of skipping, so a missing database is never mistaken for a pass on an unattended machine |
| `PORT` | Platforms that assign one (Render, Cloud Run) |
| `TEST_DATABASE_URL` | You, in the shell — `go test` never reads `apps/api/.env`. `make test` sets it for you ([7-tests.md](7-tests.md)) |
| `AIRSHIP_URL`, `AIRSHIP_RUNNER_TOKEN` | The boot script, on each runner machine, for the cache proxy |

---

## Checkpoint

- [ ] `apps/api/.env` exists and `make dev` starts without a `load config`
      error.
- [ ] You know which file to edit when you reach [step 4](4-github.md):
      `apps/api/.env`.
- [ ] You have not committed any of them: `git status` shows no `.env`
      files.

Next: [step 4 — real GitHub](4-github.md), or
[step 6 — making changes](6-making-changes.md).
