# Step 2 — The GitHub App

**What this is.** The GitHub App Airship acts as. It is how Airship learns
a job is queued, registers a runner for it, and signs users in. Creating it
produces five of the values step 4 asks for.

**Time.** 15 minutes.

**Prerequisite.** Step 1: the GitHub organization and the two hostnames.
You do **not** need anything deployed yet — deliveries fail until the API
is up, and nothing is lost by that before the app is installed anywhere.

> **This is the production app.** Keep it separate from the
> `airship-dev-<you>` app on your machine
> ([development/4-github.md](../development/4-github.md)), so your laptop
> never receives a pilot customer's webhooks.

---

## Create it

**Your organization → Settings → Developer settings → GitHub Apps → New
GitHub App.**

| Field | Value |
| --- | --- |
| **GitHub App name** | `Airship` (or `Airship <yourcompany>` if the name is taken). The slug GitHub derives from it becomes `GITHUB_APP_SLUG` |
| **Homepage URL** | `https://app.example.com` |
| **Callback URL** | `https://api.example.com/v1/auth/github/callback` |
| Expire user authorization tokens | leave **on** |
| Request user authorization (OAuth) during installation | leave **off** |
| **Setup URL** | leave empty |
| **Webhook → Active** | **on** |
| **Webhook URL** | `https://api.example.com/v1/github/webhook` |
| **Webhook secret** | `openssl rand -hex 32` — generate it now and keep the output |
| **Where can this GitHub App be installed?** | **Any account**, if organizations other than yours will use this Airship. Otherwise **Only on this account** |

Both URLs are on `api.example.com`, the **API**, not the dashboard. The
callback is where GitHub returns a signed-in user; the API then redirects
the browser to `DASH_URL`.

### Permissions

**Repository permissions:**

| Permission | Access | What it is for |
| --- | --- | --- |
| Administration | **Read & write** | Registering and removing repository-level self-hosted runners |
| Actions | **Read** | Reading job and workflow state |
| Contents | **Read** | Reading `airship.yaml` from the repository and from `.airship` |
| Pull requests | **Write** | The access-control API opens pull requests against `.airship` |
| Metadata | **Read** | Mandatory; GitHub selects it for you |

Give Contents **Read & write** as well if the access-control API should
commit to `.airship` rather than only propose changes.

**Organization permissions:**

| Permission | Access | What it is for |
| --- | --- | --- |
| Members | **Read** | Mapping GitHub organization members to Airship users |
| Self-hosted runners | **Read & write** | Registering and removing organization-level runners |

**Subscribe to events:** **Workflow job**, **Push**, **Repository**.

- **Workflow job** is the one that matters: every queued job arrives
  through it. Without it nothing launches except through the stale-job
  sweep.
- **Push** notices changes to `airship.yaml`.
- **Repository** notices repositories being created, renamed or deleted.

Click **Create GitHub App**.

---

## Collect the five values

You land on the app's settings page. Each of these goes into Render in
step 4 — put them in your password manager as you go.

| Value | Where exactly | Notes |
| --- | --- | --- |
| `GITHUB_APP_SLUG` | The page's URL: `github.com/settings/apps/`**`airship-yourco`** — or **Public page** at the bottom | The slug, **not** the display name. Lower case, hyphens. The dashboard builds its "Install" link from it |
| `GITHUB_CLIENT_ID` | **About → Client ID** | Starts `Iv23li…`. Not the numeric "App ID" above it — that one is unused |
| `GITHUB_CLIENT_SECRET` | **Client secrets → Generate a new client secret** | **Shown once.** Copy it before leaving the page. Lose it and you generate another; the old one keeps working until you delete it |
| `GITHUB_WEBHOOK_SECRET` | The `openssl rand -hex 32` you pasted into the form | Keep the same string. The API rejects every delivery whose signature does not match |
| `GITHUB_PRIVATE_KEY` | **Private keys → Generate a private key** | Downloads a `.pem`. Also shown once — GitHub keeps only the public half |

### Getting the private key into Render

The `.pem` is multi-line. Two ways to carry it, both supported:

```bash
# Render's environment accepts multi-line values: paste the file as-is.
cat ~/Downloads/airship.2026-10-28.private-key.pem | pbcopy

# Or, for a platform that only takes single-line values, with literal \n:
awk 'BEGIN{ORS="\\n"} {print}' ~/Downloads/airship.*.private-key.pem | pbcopy
```

The API accepts either form — it replaces literal `\n` with real newlines
before parsing. Keep the file out of the repository: `*.pem` is gitignored,
and the Docker build ignores it, but the safest place is your password
manager with the rest.

---

## Do not install it yet

The app has no installations, which is correct for now. Step 6 installs it
on a test organization, once the API exists to receive the webhooks.

If you install it now, GitHub records failed deliveries until step 4 is
done. They are harmless, and GitHub retries, but the clean order is:
deploy, then install.

---

## Checkpoint

- [ ] The app exists under your organization.
- [ ] Callback URL is `https://api.example.com/v1/auth/github/callback`.
- [ ] Webhook URL is `https://api.example.com/v1/github/webhook`, Active,
      with a secret you still have.
- [ ] Permissions match the three tables above, and **Workflow job** is
      subscribed.
- [ ] Five values in your password manager: slug, client ID, client secret,
      webhook secret, private key.

Next: [step 3 — the database on Supabase](3-supabase.md).
