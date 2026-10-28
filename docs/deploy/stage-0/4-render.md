# Step 4 — The API on Render

**What this is.** Airship's control plane as a Render web service: two
always-on instances, migrations as a pre-deploy command, and a custom
domain. This is the step where every value you have collected gets used.

**Time.** 20 minutes, plus DNS.

**Prerequisites.** Steps 1–3: the domain, the GitHub App's five values,
`CREDENTIALS_KEY`, and `DATABASE_URL`.

---

## Two rules for this service

1. **The API runs on Render, not on Vercel.** The launcher, reconciler and
   webhook redeliverer are background loops inside the API process. Vercel
   Functions only run while a request is in flight, so the loops would
   never run. A Render web service is one long-running container, which is
   what the API needs.
2. **A paid instance type, always on.** The free type spins down when
   idle, which stops those loops, and it has no pre-deploy command, which
   is how migrations run here. Two instances is the recommendation (any
   paid type allows manual scaling).

---

## Create the service

**New → Web Service**, connect this repository, then:

| Setting | Value |
| --- | --- |
| Name | `airship-api` |
| Language | **Docker** |
| Branch | `main` |
| Region | The region from step 1. **It cannot be changed later** |
| Root Directory | leave empty |
| Dockerfile Path | `./apps/api/Dockerfile` |
| Docker Build Context Directory | `./apps/api` |
| Docker Command | leave empty (the image runs the API) |
| Instance type | Any paid type. The smallest (512 MB) is enough |
| Pre-Deploy Command | `/usr/local/bin/migrate up` |
| Health Check Path | `/healthz` |
| Auto-Deploy | **On commit**. (There are no repository checks to wait for: this repository has no CI, so `make check` before pushing is the gate — [development/6-making-changes.md](../development/6-making-changes.md#before-you-push)) |

- The image is distroless: it has no shell. That's why the pre-deploy
  command is the binary's full path with no `sh -c`. Render's command
  replaces the image's entrypoint, so this runs `migrate`, not the API.
- The pre-deploy command runs once per deploy, on its own instance, before
  any new API instance starts. If it fails, the deploy stops and the
  previous version keeps serving. This matches the release order in
  [README.md](../README.md#releasing-a-new-version).
- Under **Build Filters**, include `apps/api/**` so dashboard-only changes
  don't redeploy the API.

---

## The environment

Add these under **Environment**. Render encrypts environment values at
rest and has no separate secret store, so the secrets go here too. Keep the
`CREDENTIALS_KEY` backup outside Render, as the
[checklist](../checklist.md#configuration) asks.

| Key | Value | From |
| --- | --- | --- |
| `DATABASE_URL` | The session-pooler string | [Step 3](3-supabase.md#step-2--a-role-for-airship) |
| `PUBLIC_URL` | `https://api.example.com` | Your domain |
| `DASH_URL` | `https://app.example.com` | Your domain |
| `CORS_ORIGINS` | `https://app.example.com` | Exactly `DASH_URL` |
| `LOG_LEVEL` | `info` | |
| `CREDENTIALS_KEY` | The `openssl rand -base64 32` output | [Step 1](1-accounts.md#4-generate-the-credentials-key) |
| `GITHUB_APP_SLUG` | e.g. `airship-yourco` | [Step 2](2-github-app.md#collect-the-five-values) |
| `GITHUB_CLIENT_ID` | `Iv23li…` | Step 2 |
| `GITHUB_CLIENT_SECRET` | | Step 2 |
| `GITHUB_WEBHOOK_SECRET` | The `openssl rand -hex 32` in the app's form | Step 2 |
| `GITHUB_PRIVATE_KEY` | The whole `.pem`. Render accepts multi-line values | Step 2 |

**Set nothing else.** In particular never `DEV_LOGIN` (the API refuses to
start) or any `*_ENDPOINT` variable (they send launches to an emulator
instead of the real cloud). `APP_ENV` and `PORT` are already right: the
image sets `APP_ENV=production`, and Render assigns `PORT`, which the API
listens on.

[env.md](env.md) is the reference for all of them — what each one is for,
and what breaks when it is wrong. The template is
[`deploy/production/api.env.example`](../../../deploy/production/api.env.example).

---

## Scaling and shutdown

- **Settings → Scaling**: 2 instances. Render replaces them one at a time
  on each deploy and cancels the deploy if a new one fails its health
  check.
- **Shutdown delay**: Render waits 30 seconds after `SIGTERM` by default,
  exactly as long as the API drains. Raise it to 45 for some margin
  (`maxShutdownDelaySeconds` in the Blueprint below).

---

## The custom domain

**Settings → Custom Domains → Add** `api.example.com`, then create the
`CNAME` record Render shows (pointing at `airship-api.onrender.com`).
Render issues the certificate once DNS resolves. On Cloudflare the record
must be **DNS only**, not proxied.

`airship-api.onrender.com` keeps working alongside it. Nothing uses that
address, and GitHub sign-in fails from it (it isn't on the dashboard's
site), so point nothing at it.

---

## Optional: as a Blueprint

The same service is described in
[`deploy/render/render.yaml`](../../../deploy/render/render.yaml). Use it
with **New → Blueprint**, setting the Blueprint path to
`deploy/render/render.yaml`. Render asks for each secret (`sync: false`)
when it first creates the service. Change `region` and the three URLs in
the file first.

---

## Verify

```bash
curl -s https://api.example.com/healthz
# {"status":"ok","database":"ok","version":"dev"}
```

- `"database":"ok"` means `DATABASE_URL` is right.
  `{"status":"degraded","database":"down"}` means the pool can't connect —
  check the string, and the network restrictions in
  [3-supabase.md, step 5](3-supabase.md#step-5--network-and-tls).
- `version` is `dev` because Render builds the image without the `VERSION`
  build argument. The deploy's commit is on Render's **Events** page. To
  have `/healthz` report it, build and push the image yourself with
  `--build-arg VERSION=...` (`make images`) and deploy it as an
  image-backed Render service instead.

The first deploy's **Pre-Deploy** log ends with
`goose: successfully migrated database to version: N` — or, since step 3
already applied them, `goose: no migrations to run`. If it shows the API's
startup logs instead, the pre-deploy command isn't set to
`/usr/local/bin/migrate up`.

Then go back to Supabase and close the database down:
[3-supabase.md, step 5](3-supabase.md#step-5--network-and-tls), using the
addresses on Render's **Connect → Outbound** tab.

---

## Checkpoint

- [ ] `curl https://api.example.com/healthz` returns
      `"status":"ok","database":"ok"`.
- [ ] Two instances are running, on a paid type.
- [ ] The pre-deploy log shows goose, not the API.
- [ ] The custom domain has a certificate (Render shows it as verified).
- [ ] Supabase's network restrictions now list Render's outbound ranges.

Next: [step 5 — the dashboard on Vercel](5-vercel.md).

---

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| Deploy fails instantly, logs mention `load config` | A required variable is missing. The message names it |
| `CREDENTIALS_KEY is the key from .env.example` | You pasted the development key. Generate your own |
| `PUBLIC_URL must be https outside development` | `http://` in `PUBLIC_URL`, or the variable is unset so it defaulted to localhost |
| `DEV_LOGIN is only allowed with APP_ENV=development` | `DEV_LOGIN` is set. Remove it |
| `GITHUB_CLIENT_ID, GITHUB_PRIVATE_KEY and GITHUB_WEBHOOK_SECRET are required outside development` | One of the three is missing or empty |
| Build fails looking for `go.mod` | Docker Build Context is not `./apps/api` |
| Health check never passes, logs look fine | Health Check Path is not `/healthz` |

More, including what the runtime failures look like, in
[operate.md](operate.md#when-something-is-wrong).
