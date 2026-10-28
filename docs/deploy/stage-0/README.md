# Stage 0 — the pilot

**What this is.** The runbook that takes Airship from this repository to a
real GitHub Actions job running on a machine your control plane launched,
on hosting you do not operate. Six steps, in order, each ending in a check
that either passes or tells you what is wrong.

**Audience.** Whoever is standing up the pilot. No cloud or Kubernetes
experience needed — everything here is a web dashboard, `psql` and `curl`.

**Time.** About 90 minutes of work, plus up to an hour of waiting for DNS
and for Supabase to finish provisioning. You can do steps 1–3 and come
back.

**Cost.** About $50/month, itemised in
[1-accounts.md](1-accounts.md#what-it-costs).

> **Already running it locally?** [local.md](local.md) sets up a local
> database that matches this one (Supabase, Postgres 17), so a problem you
> hit in the pilot reproduces on your machine.

---

## What you end up with

```
   app.example.com ──► Vercel (apps/dash)
                              │  the browser calls the API directly, with the
                              │  session cookie (same site: example.com)
   api.example.com ──► Render web service airship-api × 2 ──► api.github.com
                              ▲            │                ──► the pilot customer's AWS
          GitHub webhooks ────┤            │ TLS over the internet
          runner machines ────┘            ▼
                                   Supabase Postgres 17
```

Four accounts, one domain, and no servers of your own:

| Piece | Where | Why not somewhere else |
| --- | --- | --- |
| Database | Supabase | Managed Postgres 17 with backups, a SQL editor and no VPC to build. The pooler and Data API need two specific settings, which step 3 covers |
| API | Render | The API is a **long-running process** with background loops. Render runs one container continuously. Serverless platforms, Vercel included, only run code during a request, so the loops would never fire |
| Dashboard | Vercel | A static Next.js build; nothing with state in it |
| Runner machines | The customer's own AWS account | Always. Airship never launches with its own credentials |

---

## The six steps

Do them in this order. Each one needs something from the one before.

| | Step | You create | Time |
| --- | --- | --- | --- |
| 1 | [Accounts and the domain](1-accounts.md) | Four accounts, two DNS names, the credentials key | 20 min + DNS wait |
| 2 | [The GitHub App](2-github-app.md) | The app Airship acts as, and five secret values | 15 min |
| 3 | [The database on Supabase](3-supabase.md) | A project, the `airship` role, `DATABASE_URL` | 20 min |
| 4 | [The API on Render](4-render.md) | The web service, every variable, the custom domain | 20 min |
| 5 | [The dashboard on Vercel](5-vercel.md) | The project, its one variable, the custom domain | 10 min |
| 6 | [The first real job](6-first-job.md) | A test organization, a connected cloud, a green job | 15 min |

Then: [env.md](env.md) is the reference for every value you pasted, and
[operate.md](operate.md) is what to do once it is live — releases,
rollbacks, backups, rotating a secret, and what the common failures look
like.

---

## Collect these as you go

Steps 1–3 produce everything the API needs: eleven environment variables,
from the eight sources below. Keep them in your password manager — not in a
file in the repository — and paste them into Render in step 4.

| Value | Comes from |
| --- | --- |
| `api.example.com`, `app.example.com` | Step 1, the domain you own |
| `CREDENTIALS_KEY` | Step 1, `openssl rand -base64 32`. **Back this one up separately** |
| `GITHUB_APP_SLUG` | Step 2, the app's URL |
| `GITHUB_CLIENT_ID` | Step 2, the app's page |
| `GITHUB_CLIENT_SECRET` | Step 2, generated once and never shown again |
| `GITHUB_WEBHOOK_SECRET` | Step 2, `openssl rand -hex 32` |
| `GITHUB_PRIVATE_KEY` | Step 2, the `.pem` file you download |
| `DATABASE_URL` | Step 3, the session pooler string |

[env.md](env.md) has the same list with, for each one, the exact page it
comes from, what it is used for, and what breaks when it is wrong.

---

## Three things that bite people

Read these now; all three are cheap to get right and expensive to debug.

1. **The dashboard and the API must be on the same registrable domain.**
   `app.example.com` and `api.example.com` are fine. The API sets a
   `SameSite=Lax` session cookie, so a dashboard on `*.vercel.app` with an
   API on `*.onrender.com` is a different site and sign-in silently loops
   back to the sign-in page. Custom domains are a requirement, not a
   finishing touch.
2. **Supabase's session pooler, port 5432 — never the transaction pooler
   on 6543.** The API elects its reconciler with a session-level advisory
   lock. Transaction mode can send the lock and the unlock to different
   server connections, leaving the lock held where nothing can release it:
   **the reconciler then stops on every replica**, machines are never
   terminated and the bill climbs quietly.
   [3-supabase.md](3-supabase.md#1-the-session-pooler-never-the-transaction-pooler)
   has the detail.
3. **Never a free or scale-to-zero instance for the API.** The launcher and
   reconciler are loops inside the process, not HTTP handlers. An instance
   that sleeps when idle stops launching runners and stops cleaning them
   up.

---

## When to move to stage 1

Stage 0 is a real deployment, not a toy: two replicas, managed backups,
TLS everywhere. Move to [stage 1](../stage-1/) when one of these becomes
true, not before:

- a customer requires the control plane inside their own VPC or account;
- you need private database networking, or a compliance regime that names
  AWS;
- Render's shared outbound IP ranges are not acceptable to a customer who
  allow-lists;
- the bill on managed hosting passes what the equivalent on AWS costs.

[stage-1/README.md](../stage-1/README.md) lists what carries over (the
domain, the GitHub App, the schema, the data) and what gets rebuilt.
