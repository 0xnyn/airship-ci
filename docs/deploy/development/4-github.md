# Step 4 — Real GitHub

**What this is.** A throwaway GitHub organization, a GitHub App of your
own, and a tunnel so that GitHub's webhooks reach the API on your laptop.
After this, queued jobs in your test repository show up in your local
dashboard.

**Time.** 20 minutes.

**Prerequisite.** [Step 2](2-first-run.md): `make dev` works.

---

## Why a separate organization

Three reasons, and they are all the same reason:

- **Webhooks go to one place per app.** If you point the production app's
  webhook at your laptop, the pilot's deliveries come to you instead.
- A GitHub App with **Administration: read & write** can register and
  remove self-hosted runners on every repository it is installed on. You do
  not want that pointed at anything real while you are learning.
- A test organization keeps the blast radius of a mistake to repositories
  that do not matter.

So: **one organization per developer, one app per developer, neither shared
with production.** They are free.

---

## 1. Create the test organization

[github.com/organizations/plan](https://github.com/organizations/plan) →
**Free**.

| Field | Value |
| --- | --- |
| Organization name | `airship-dev-<you>` — it has to be globally unique |
| Contact email | yours |
| This organization belongs to | **My personal account** |

Then create a repository in it:

| Field | Value |
| --- | --- |
| Name | `runner-test` |
| Visibility | **Private** is fine; Actions works on private repositories |
| Add a README | yes, so the default branch exists |

Add a workflow you can trigger by hand — you will come back and change the
`runs-on` line in [step 5](5-real-machines.md):

```yaml
# .github/workflows/smoke.yml
name: smoke
on: workflow_dispatch

jobs:
  hello:
    runs-on: ubuntu-latest
    steps:
      - run: echo "hello from GitHub's runner"
```

Run it once from the **Actions** tab to confirm Actions is enabled on the
repository.

---

## 2. Start a tunnel, and keep the URL

The app's webhook URL has to exist before you create the app, so the tunnel
comes first.

```bash
make tunnel     # cloudflared tunnel --url http://localhost:8080
# prints https://<random-words>.trycloudflare.com
```

Leave it running in its own terminal, alongside `make dev`. Copy that URL — it
is `{PUBLIC_URL}` everywhere below.

**A quick tunnel's URL changes every time it starts**, and both the app's
URLs and `apps/api/.env` have to be updated when it does. If that becomes
annoying, the [named tunnel](#named-tunnel-for-github-sign-in) below is
stable. `ngrok http 8080` works the same way.

---

## 3. Create your GitHub App

In the **test organization** → **Settings → Developer settings → GitHub
Apps → New GitHub App**:

| Field | Value |
| --- | --- |
| **GitHub App name** | `airship-dev-<you>`. The slug GitHub derives becomes `GITHUB_APP_SLUG` |
| **Homepage URL** | anything, e.g. `http://localhost:3000` |
| **Callback URL** | `{PUBLIC_URL}/v1/auth/github/callback` |
| Request user authorization (OAuth) during installation | off |
| **Webhook → Active** | **on** |
| **Webhook URL** | `{PUBLIC_URL}/v1/github/webhook` |
| **Webhook secret** | `openssl rand -hex 32` — generate it now, keep the output |
| **Where can this GitHub App be installed?** | **Only on this account** |

**Repository permissions:**

| Permission | Access | For |
| --- | --- | --- |
| Administration | **Read & write** | Registering repository-level runners |
| Actions | **Read** | Job and workflow state |
| Contents | **Read** | Reading `airship.yaml` |
| Pull requests | **Write** | The access-control API's pull requests |
| Metadata | **Read** | Mandatory |

Give Contents **Read & write** too if you want to try the access-control
API committing to `.airship`.

**Organization permissions:** Members **Read**, Self-hosted runners
**Read & write**.

**Subscribe to events:** **Workflow job**, **Push**, **Repository**.
Without *Workflow job* nothing launches except through the stale-job sweep.

Create it, then on the app's page:

1. Note the **Client ID** (`Iv23li…`).
2. **Generate a new client secret** and copy it — shown once.
3. **Generate a private key**, and save the `.pem` as
   `apps/api/github-app.pem` (`*.pem` is gitignored).
4. **Install App** → the test organization → **Only select repositories** →
   `runner-test`.

---

## 4. Fill in `apps/api/.env`

```bash
PUBLIC_URL=https://<random-words>.trycloudflare.com
GITHUB_APP_SLUG=airship-dev-you
GITHUB_CLIENT_ID=Iv23li...
GITHUB_CLIENT_SECRET=...
GITHUB_WEBHOOK_SECRET=...
GITHUB_PRIVATE_KEY_FILE=./github-app.pem
```

Leave `DASH_URL`, `CORS_ORIGINS` and the dashboard's `NEXT_PUBLIC_API_URL`
on localhost. Restart `make dev` — the API reads its environment at start.

---

## 5. Verify

| Check | Where | Expected |
| --- | --- | --- |
| The app's deliveries | GitHub App → **Advanced → Recent Deliveries** | `200` for the `installation` event. Press **Redeliver** on it to retry |
| The API's logs | the `make dev` terminal | A line per delivery |
| The dashboard | **Repositories** | `runner-test` appears, inactive |

Sign in again (**Development sign-in** is fine) and you will be an admin of
the real installation too: development sign-in makes you an admin of every
installation in the database, not just the demo one.

### What works now, and what does not

| | |
| --- | --- |
| ✅ Webhooks arrive; **Jobs** and **Repositories** fill up | |
| ✅ Runners get **created** as `pending` when a job matches an `airship-` label | |
| ❌ Nothing **launches** | No cloud credential yet — [step 5](5-real-machines.md) |
| ❌ GitHub sign-in loops back to the sign-in page | Expected with a quick tunnel: the dashboard is on `localhost` and the API is on `trycloudflare.com`, which are different sites, so the `SameSite=Lax` session cookie is never sent. Use development sign-in, or the named tunnel below |

---

## Named tunnel, for GitHub sign-in

Only needed to test **Sign in with GitHub** against your real organization,
or to stop re-editing URLs. It needs a domain on Cloudflare (other tunnels
work the same way, given two hostnames on one domain):

```bash
cloudflared tunnel login
cloudflared tunnel create airship-dev
cloudflared tunnel route dns airship-dev api.dev.example.com
cloudflared tunnel route dns airship-dev app.dev.example.com
cat > ~/.cloudflared/airship-dev.yml <<'YAML'
tunnel: airship-dev
credentials-file: /Users/you/.cloudflared/<tunnel-id>.json
ingress:
  - hostname: api.dev.example.com
    service: http://localhost:8080
  - hostname: app.dev.example.com
    service: http://localhost:3000
  - service: http_status:404
YAML
cloudflared tunnel --config ~/.cloudflared/airship-dev.yml run
```

Then:

```bash
# apps/api/.env
PUBLIC_URL=https://api.dev.example.com
DASH_URL=https://app.dev.example.com
CORS_ORIGINS=https://app.dev.example.com,http://localhost:3000

# apps/dash/.env.local
NEXT_PUBLIC_API_URL=https://api.dev.example.com
```

Point the app's Callback and Webhook URLs at `api.dev.example.com`, restart
`make dev`, and open https://app.dev.example.com. Because the URLs never
change again, the app only needs setting up once.

This is the same same-site rule a deployment lives by
([stage-0/1-accounts.md](../stage-0/1-accounts.md#3-two-hostnames-on-one-domain)).

---

## Checkpoint

- [ ] A test organization with one test repository.
- [ ] Your own GitHub App, installed on it.
- [ ] A tunnel running, and its URL in `PUBLIC_URL`.
- [ ] Recent Deliveries shows `200`s.
- [ ] **Repositories** in the dashboard lists `runner-test`.

Next: [step 5 — real machines](5-real-machines.md).

---

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| Deliveries fail with `401` | `GITHUB_WEBHOOK_SECRET` differs from the app's. They have to be the same string |
| Deliveries fail with `502`, or time out | The tunnel is down, `make dev` is not running, or the quick tunnel's URL changed. Update the app's webhook URL |
| Deliveries are `200` but the dashboard stays empty | A different database than you think: check `DATABASE_URL`, and that migrations ran |
| `GITHUB_CLIENT_ID… are required outside development` | `APP_ENV` is not `development` in your shell or `.env` |
| GitHub sign-in returns to the sign-in page | Different sites. Use development sign-in, or the named tunnel |
| The app's page shows no Client ID | You are looking at the *installation* page. Go to Settings → Developer settings → GitHub Apps → your app |
