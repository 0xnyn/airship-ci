# Operating the pilot

**What this is.** The day-to-day once stage 0 is live: shipping a change,
rolling back, backups, rotating a secret, and what the failure modes look
like. The deep version — the background loops, GitHub rate limits, the
incident runbook, capacity planning — is in
[architecture/operations.md](../../architecture/operations.md).

---

## Shipping a change

Push to `main`, and both platforms deploy it. **Nothing checks the commit
first** — this repository has no CI — so run `make check` before you push
([development/6-making-changes.md](../development/6-making-changes.md#before-you-push)).

Then:

| | What happens | Where to watch |
| --- | --- | --- |
| Render | Builds the API image, runs `migrate up` as a pre-deploy step, then replaces the two instances one at a time, waiting for `/healthz` on each | Render → **Events** |
| Vercel | Builds and promotes the dashboard | Vercel → **Deployments** |

If the pre-deploy migration fails, the deploy stops and the previous
version keeps serving. That is the behaviour you want: a bad migration
never reaches a running API.

The two platforms deploy independently, so the dashboard is usually live a
few minutes before the new API. For most releases that is fine. **When a
dashboard change needs a new endpoint, ship the API change first and the
dashboard change in a later push.**

### Rolling back

| | How |
| --- | --- |
| API | Render → **Events → Rollback** on an earlier deploy |
| Dashboard | Vercel → **Deployments → Instant Rollback** |

Neither undoes a migration. Only run `migrate down` if the release notes
say the previous version cannot run against the new schema — from your
machine, with the production `DATABASE_URL`:

```bash
cd apps/api && DATABASE_URL='<the session pooler string>' go run ./cmd/migrate down
```

It reverts one migration at a time.

---

## The database

| Item | How |
| --- | --- |
| **Backups** | Pro takes daily backups. Turn on the **Point-in-Time Recovery** add-on before the first customer who would be hurt by losing a day ([1-accounts.md](1-accounts.md#what-it-costs) on the trade-off) |
| **A restore drill** | Restore a backup into a new project, point a local API at it with the **production** `CREDENTIALS_KEY`, and open the dashboard. A restore without that key cannot open a single cloud credential, which is the whole point of doing this once in advance |
| **Rotating the database password** | `alter role airship password '…'` in the SQL Editor, update `DATABASE_URL` on Render, deploy. Instances using the old password keep their open connections until they restart |
| **Upgrades** | Supabase applies minor versions during maintenance. Major versions use its upgrade flow, which takes the database offline for a while — plan it outside busy hours. Airship runs on 17 and 18 |
| **Unused features** | Auth, Storage, Realtime and Edge Functions stay unused and empty. The Data API stays off |
| **Growth** | Rows per job and per runner; events are pruned after 30 days. A pilot stays comfortably inside the smallest compute size |

---

## Rotating a secret

| Secret | Steps | Risk while rotating |
| --- | --- | --- |
| `CREDENTIALS_KEY` | Set it to `"<new>,<old>"`, deploy, have every customer reconnect their cloud (which re-seals with the new key), then set it to `"<new>"` and deploy | None if the order is kept. **Dropping the old key before customers reconnect makes their credentials unreadable** |
| `GITHUB_CLIENT_SECRET` | Generate a new one on the app, set it on Render, deploy, then delete the old one on GitHub | Signed-in sessions are unaffected; new sign-ins use whichever is set |
| `GITHUB_WEBHOOK_SECRET` | Change it on Render **and** on the app. Brief mismatch means rejected deliveries, which GitHub retries | A few minutes of `401`s; the stale-job sweep catches anything missed |
| `GITHUB_PRIVATE_KEY` | Generate a second key on the app, set it on Render, deploy, then delete the first | GitHub accepts both while both exist, so this one is clean |
| `DATABASE_URL` password | See the table above | |

Render reads its environment only at start, so every row above needs a
deploy or restart to take effect.

---

## Watching it

| | Where |
| --- | --- |
| API logs | Render → **Logs** for `airship-api`. Filter on `"level":"ERROR"`. Render can forward the stream to a syslog-compatible provider |
| Health | `curl https://api.example.com/healthz` → `{"status":"ok","database":"ok"}` |
| Webhook deliveries | GitHub App → **Advanced → Recent Deliveries**. Non-`200`s here are the single best early warning |
| What Airship thinks happened | Dashboard → **Events**: launches, failures, cache warnings, with the cloud's own error text |
| Deploy failures | Render's notifications, on by default |

The log lines worth alerting on are listed in
[operations.md, "Observability"](../../architecture/operations.md#observability).
At pilot size, the webhook delivery page and a weekly look at **Events**
catch most of it.

---

## When something is wrong

Ordered by how often it happens.

| Symptom | Cause | Fix |
| --- | --- | --- |
| **Instances keep running after jobs finish**; the AWS bill climbs | `DATABASE_URL` is on port 6543 (transaction mode), so the reconciler's advisory lock leaked and the reconciler stopped on every replica | Switch to 5432 and deploy. Restarting the Supabase project clears the stuck lock at once ([3-supabase.md](3-supabase.md#1-the-session-pooler-never-the-transaction-pooler)) |
| Jobs stay queued, nothing in **Events** | Webhooks are not arriving | GitHub App → Recent Deliveries. `401` = secret mismatch, `502`/timeout = API down or wrong URL, nothing at all = **Workflow job** not subscribed |
| Jobs stay queued, `capacity.reached` in **Events** | `max_runners` is below the queue | Raise **Most runners at once** under **Settings** |
| Jobs stay queued, an EC2 error in **Events** | The customer's quota or IAM | The message is the cloud's own; [aws-setup-console.md](../../architecture/aws-setup-console.md) covers the policy |
| Sign-in loops back to the sign-in page | Dashboard and API are not on the same registrable domain | Nothing to configure — the hostnames have to change ([1-accounts.md](1-accounts.md#3-two-hostnames-on-one-domain)) |
| `/healthz` says `"status":"degraded"`, `"database":"down"` | Supabase pool size too small, or a network restriction that no longer matches Render's outbound ranges | [3-supabase.md, steps 4–5](3-supabase.md#step-4--pool-size) |
| Everything is fine but jobs are slow to start | Normal: 1–3 minutes is instance boot plus runner registration, not Airship | |
| GitHub stops sending jobs to runners | `RUNNER_VERSION` is more than 30 days behind the latest [actions/runner release](https://github.com/actions/runner/releases) | Bump it on Render and deploy |

**The one that is silent** is the first row. Nothing errors, no alert
fires, and the only symptom is a cloud bill. If you check one thing a week,
check that **Runners** empties out after jobs finish.

---

## Tearing it down

Delete the Vercel project, then **Settings → Delete Web Service** for
`airship-api` on Render, then pause or delete the Supabase project.

Runner machines already launched keep running until their job ends, and
every one powers itself off at `RUNNER_MAX_LIFETIME` at the latest, with or
without a control plane. Nothing is left behind in a customer's account
that outlives that deadline.
