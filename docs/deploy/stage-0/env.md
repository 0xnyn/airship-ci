# Where every environment variable comes from

**What this is.** Each variable Airship reads in production, **what to put
in it and where that value is generated or found**, and what breaks when it
is wrong. The lists of defaults live in
[operations.md, "Configuration"](../../architecture/operations.md#configuration)
and in
[`deploy/production/api.env.example`](../../../deploy/production/api.env.example);
this page is about provenance.

**Audience.** Whoever is filling in a platform's environment screen with a
password manager open in the other window.

It applies to any platform. Stage 0 pastes these into Render; stage 1 puts
the secret ones in Secrets Manager and the rest in the task definition.

---

## The worksheet

Fill this in locally, then paste the whole block into Render's
**Environment → Add from .env**. Delete the file afterwards — it is not
something to keep, let alone commit.

```bash
# --- yours, from step 1 --------------------------------------------------
PUBLIC_URL=https://api.example.com
DASH_URL=https://app.example.com
CORS_ORIGINS=https://app.example.com
LOG_LEVEL=info
CREDENTIALS_KEY=            # openssl rand -base64 32
# --- from step 2, the GitHub App -----------------------------------------
GITHUB_APP_SLUG=
GITHUB_CLIENT_ID=           # Iv23li...
GITHUB_CLIENT_SECRET=
GITHUB_WEBHOOK_SECRET=      # openssl rand -hex 32
GITHUB_PRIVATE_KEY=         # the whole .pem
# --- from step 3, Supabase -----------------------------------------------
DATABASE_URL=               # session pooler, port 5432
```

Eleven variables. Anything not in this block is either set by the platform
or has a default that is already right.

---

## The API

### Required — the API will not start without these

| Variable | Where the value comes from | What breaks when it's wrong |
| --- | --- | --- |
| `DATABASE_URL` | [Step 3](3-supabase.md#step-2--a-role-for-airship): Supabase **Connect → Session pooler**, with the user swapped to `airship.<ref>` and the password you generated. **Port 5432.** | Missing: startup fails naming the variable. Port 6543: everything appears to work, but the reconciler's advisory lock leaks and **machines are never terminated**. Wrong host or user: `Tenant or user not found` |
| `CREDENTIALS_KEY` | [Step 1](1-accounts.md#4-generate-the-credentials-key): `openssl rand -base64 32`, once, kept in your password manager | Missing: startup fails. The published example key: startup fails naming `.env.example`. **Changed or lost: every stored cloud credential is unreadable** and every customer has to reconnect. Rotation is `"<new>,<old>"` → reconnect → `"<new>"` |
| `GITHUB_CLIENT_ID` | [Step 2](2-github-app.md#collect-the-five-values): the app's **About → Client ID**, `Iv23li…`. Not the numeric App ID | Missing: startup fails outside development. Wrong: GitHub refuses every token request, so nothing launches and nobody signs in |
| `GITHUB_WEBHOOK_SECRET` | Step 2: the `openssl rand -hex 32` you pasted into the app's **Webhook secret** field | Mismatched: every delivery is rejected with `401` and **no job is ever picked up** except by the stale-job sweep |
| `GITHUB_PRIVATE_KEY` *or* `GITHUB_PRIVATE_KEY_FILE` | Step 2: **Generate a private key** downloads a `.pem`. Paste the file (Render takes multi-line) or put it on one line with literal `\n`; or mount it and point `_FILE` at the path | Missing: startup fails. Malformed: every GitHub call fails to sign, so no runner registers |
| `PUBLIC_URL` | Your API hostname from [step 1](1-accounts.md#3-two-hostnames-on-one-domain), `https://api.example.com` | Plain `http`: startup fails outside development. Wrong host: GitHub's webhooks and sign-in callback go nowhere, and runner machines cannot fetch the cache proxy |

### Should always be set

| Variable | Where the value comes from | What breaks when it's wrong |
| --- | --- | --- |
| `DASH_URL` | Your dashboard hostname, `https://app.example.com` | Sign-in redirects to the wrong place (default: `localhost:3000`) |
| `CORS_ORIGINS` | **Exactly** `DASH_URL`. Comma-separated if there is more than one | The dashboard's calls fail in the browser with a CORS error and the UI looks empty |
| `GITHUB_APP_SLUG` | Step 2: the slug in the app's URL, `github.com/settings/apps/<slug>` | The dashboard's "Install" link goes to a 404. Nothing else |
| `GITHUB_CLIENT_SECRET` | Step 2: **Generate a new client secret**, shown once | **Nobody can sign in** to the dashboard. Launching still works — it uses the private key, not this |
| `LOG_LEVEL` | `info`. `debug` while diagnosing | `debug` in production is noisy and logs every GitHub call |

### Set by the platform — leave them alone

| Variable | Who sets it |
| --- | --- |
| `APP_ENV` | The API image sets `production`. Setting it to anything else re-enables development behaviour |
| `PORT` | Render and Cloud Run assign it; the API listens on whatever they say. The image defaults to `8080` |

### Tuning — defaults are right until they aren't

| Variable | Default | Change it when |
| --- | --- | --- |
| `RUNNER_VERSION` | `2.338.2` | A new [actions/runner release](https://github.com/actions/runner/releases) appears. **GitHub stops sending jobs to runners more than 30 days behind the latest release**, so this is the one default with an expiry date |
| `RUNNER_MAX_LIFETIME` | `8h` | A customer's job has `timeout-minutes` longer than 8 hours. This is a hard self-destruct on the machine, so raising it raises the worst-case bill |
| `RUNNER_IDLE_TIMEOUT` | `10m` | Rarely. Lower it to reclaim machines whose job never arrived sooner |
| `RECONCILE_INTERVAL` | `15s` | Rarely |
| `LAUNCH_INTERVAL` | `2s` | Rarely; a webhook wakes the launcher immediately anyway |
| `GITHUB_API_URL`, `GITHUB_WEB_URL` | `api.github.com`, `github.com` | The customer is on GitHub Enterprise Server |

### Never set in production

| Variable | What happens |
| --- | --- |
| `DEV_LOGIN` | **Startup fails** unless `APP_ENV=development`. It is a password-less sign-in |
| `AWS_ENDPOINT`, `GCP_ENDPOINT`, `AZURE_ENDPOINT`, `DAYTONA_ENDPOINT` | Launches are sent to a local emulator instead of the real cloud. Nothing fails — jobs just never get a machine |

These are startup failures on purpose: a misconfigured control plane should
not come up at all rather than come up insecurely.

---

## The dashboard

| Variable | Where the value comes from | What breaks when it's wrong |
| --- | --- | --- |
| `NEXT_PUBLIC_API_URL` | Your API hostname, `https://api.example.com` | **Inlined at build time**, not read at runtime: changing it needs a rebuild. A production build with the localhost default fails deliberately (varlock, `apps/dash/.env.schema`). Pointing it at the wrong host makes every call fail in the browser |
| `ENABLE_EXPERIMENTAL_COREPACK` | `1`, on Vercel only | Vercel may build with a pnpm version that ignores `pnpm-workspace.yaml` settings, which can fail the install |

The dashboard holds **no secrets**. Every privileged call goes to the API
with the session cookie, so there is nothing else to set — and nothing in
the dashboard's bundle worth extracting.

---

## Local development

Different file, different values:
`apps/api/.env` (from `apps/api/.env.example`, created by `make env`) and
`apps/dash/.env.local`. The local defaults need no editing — `DEV_LOGIN=true`
and a public credentials key get you a working dashboard with a demo
organization.

Two things to know:

- The `CREDENTIALS_KEY` in `.env.example` is **public**, and the API
  refuses to start with it outside `APP_ENV=development`. A copy-paste from
  local to production fails loudly instead of sealing real credentials with
  a published key.
- `go test` reads its variables from the shell, never from `apps/api/.env`.

[local.md](local.md) covers running against a local Supabase, which is the
closest local match to this pilot.
[development/3-env.md](../development/3-env.md) goes through every local
variable the same way this page does the production ones.

---

## Checking what is actually set

```bash
# Is the database reachable, and which build is running?
curl -s https://api.example.com/healthz
# {"status":"ok","database":"ok","version":"..."}

# Does the API think GitHub is configured? A null githubSignInUrl is what
# makes the dashboard show no "Sign in with GitHub" button.
curl -s https://api.example.com/v1/auth/config
# {"githubSignInUrl":"https://api.example.com/v1/auth/github/login",
#  "installUrl":"https://github.com/apps/<slug>/installations/new",
#  "devSignIn":false}
```

`{"status":"degraded","database":"down"}` means the pool cannot connect:
`DATABASE_URL`, or Supabase's network restrictions. The API never returns a
secret's value through any endpoint, so the rest is read from the
platform's own environment screen.

A variable changed on Render needs a restart or deploy to take effect —
instances read the environment only at start.
