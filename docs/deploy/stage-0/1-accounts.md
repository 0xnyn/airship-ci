# Step 1 — Accounts and the domain

**What this is.** Everything to sign up for before anything is deployed,
which plan to pick and why, plus the one secret to generate now. Nothing
here depends on Airship's code, so it can all be done before the repository
is ready.

**Time.** 20 minutes, then up to an hour of waiting for DNS to propagate
(usually minutes).

---

## 1. The four accounts

Create all four now. Three of the four need a paid plan, for reasons that
are about how Airship works, not about support tiers.

| Service | Plan | Why that plan | Sign up |
| --- | --- | --- | --- |
| **GitHub** organization | Free is fine | Owns the GitHub App Airship acts as (step 2). Use a real organization, not a personal account, so the app survives one person leaving | [github.com/organizations/plan](https://github.com/organizations/plan) |
| **Supabase** | **Pro** ($25/mo) | Free projects **pause when inactive** and have no backups. A paused database stops the control plane dead. Pro also unlocks the point-in-time recovery add-on the [checklist](../checklist.md#data) asks for | [supabase.com/dashboard](https://supabase.com/dashboard) |
| **Render** | Any **paid** instance type ($7/mo each, ×2) | The free type **spins down when idle**, which stops the background loops, and it has no pre-deploy command, which is how migrations run. Any paid type allows manual scaling | [dashboard.render.com](https://dashboard.render.com) |
| **Vercel** | Hobby works; **Pro** if the pilot is commercial | The dashboard is a static build with no state. Vercel's Hobby plan forbids commercial use, so a paying pilot means Pro | [vercel.com/signup](https://vercel.com/signup) |

Sign in to Render and Vercel **with GitHub**, using an account that can see
this repository. Both deploy by watching the repository, so this saves
connecting it twice.

Two more things, which are not accounts:

| | What | When |
| --- | --- | --- |
| A **domain** you can edit DNS for | Two hostnames on it (below) | Steps 4 and 5 |
| An **AWS account** you can launch instances in | Step 6 runs one real job on one real EC2 instance, to prove the whole path. Use a scratch account; it costs cents | Step 6 |

The AWS account is **not** where Airship runs — it is standing in for the
pilot customer's account, the way every customer's will.
[architecture/aws-setup.md](../../architecture/aws-setup.md) covers the
credential it needs.

---

## 2. Pick one region, everywhere

Every launch and every reconcile pass makes several round trips to the
database. A database on another continent from the API makes all of it
slower for no benefit.

Pick a city close to you and the pilot customer, then use the matching
region in each service:

| City | Render region | Supabase (AWS) region |
| --- | --- | --- |
| Oregon | Oregon | `us-west-2` |
| Ohio | Ohio | `us-east-2` |
| Virginia | Virginia | `us-east-1` |
| Frankfurt | Frankfurt | `eu-central-1` |
| Singapore | Singapore | `ap-southeast-1` |

**Render's region cannot be changed later** — the service has to be
recreated. Supabase's cannot either. Decide once, here.

Vercel has no region to choose: the dashboard is served from its edge
network everywhere.

---

## 3. Two hostnames on one domain

Pick two names under a domain you control:

| Name | Points at | Set up in |
| --- | --- | --- |
| `api.example.com` | Render | Step 4 |
| `app.example.com` | Vercel | Step 5 |

**They must share a registrable domain.** The API sets the session cookie
on its own host with `SameSite=Lax`, so the browser only sends it from a
dashboard on the same site. `api.example.com` + `app.example.com` works;
`airship-api.onrender.com` + `airship.vercel.app` does not — sign-in
appears to succeed and then lands back on the sign-in page.

Nothing to create yet. Just confirm now that you can add `CNAME` records
on that domain, because steps 4 and 5 both ask for one. If the domain is
on Cloudflare, also note that the records those steps create must be
**DNS only** (grey cloud), not proxied, or the platforms cannot issue
their certificates.

Names like `api.airship.example.com` and `app.airship.example.com` work
too, and keep the pilot out of the way of the marketing site on the apex.

---

## 4. Generate the credentials key

Do this now and treat it as the most important secret in the system:

```bash
openssl rand -base64 32
```

This becomes `CREDENTIALS_KEY`. It seals every customer's cloud credential
in the database with AES-GCM. The database holds only sealed bytes, so:

- **a database backup without this key cannot open a single credential**,
  and
- losing the key means every customer has to reconnect their cloud.

Store it in **two places**: your password manager now, and Render's
environment in step 4. Not in the repository, not in the same place as the
database password, not only in Render — a Render account you lose access to
takes the key with it.

The API refuses to start with the public development key from
`apps/api/.env.example`, so a copy-paste from the local setup fails loudly
rather than sealing real credentials with a published key.

---

## What it costs

At pilot size, with two API instances:

| | Monthly |
| --- | --- |
| Supabase Pro (includes the first project) | $25 |
| Supabase point-in-time recovery add-on | $100 (7-day) — see note |
| Render, 2 × Starter (512 MB) | $14 |
| Vercel Hobby / Pro | $0 / $20 |
| Domain | ~$1 |
| **Total, without PITR** | **~$40–60** |

The runner machines themselves are billed to each customer's own cloud
account, never to you.

**On PITR.** The [checklist](../checklist.md#data) asks for point-in-time
recovery, and on Supabase that add-on costs more than everything else
combined. For a pilot with one or two customers, Pro's **daily backups**
plus the fact that the whole database can be rebuilt from GitHub (every
installation, repository and runner is re-derived from webhooks and the
inventory scan) is a defensible trade — as long as you have written down
that you made it. Turn PITR on before the first customer who would be hurt
by losing a day of event history.

---

## Checkpoint

Before step 2, you should have:

- [ ] A GitHub organization you own.
- [ ] A Supabase account on Pro, and a region chosen.
- [ ] A Render account, signed in with GitHub, same region.
- [ ] A Vercel account, signed in with GitHub.
- [ ] A domain where you can add `CNAME` records.
- [ ] `CREDENTIALS_KEY` generated and in your password manager.
- [ ] An AWS account for step 6 (can wait).

Next: [step 2 — the GitHub App](2-github-app.md).
