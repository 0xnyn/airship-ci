# Using Supabase as the database

**What this is.** How to use a [Supabase](https://supabase.com/docs/guides/database/overview)
project as Airship's Postgres, in place of RDS, Cloud SQL or Azure's
flexible server. Airship uses only the database. Supabase Auth, Storage,
Realtime and the Data API stay unused, and the Data API is switched off.
Written for [render-vercel.md](render-vercel.md) but works with any compute
guide.

**Audience.** Whoever deploys Airship.

---

## Postgres 17 works

Hosted Supabase projects run Postgres 17 (17.11 when this was written).
Airship's schema uses `uuidv7()` for every primary key. Postgres 18 has it
built in. On 17, the first migration defines an equivalent in `public` (a
millisecond timestamp followed by random bits, as RFC 9562 describes), so
nothing extra is needed. CI runs the migrations and the API's tests on
both 17 and 18.

If Supabase later upgrades the project to 18, the function stays in place
and existing tables keep using it. That's harmless.

---

## The two rules

### 1. The session pooler, never the transaction pooler

Supabase offers three ways in:

| Connection | Host and port | IP | For Airship |
| --- | --- | --- | --- |
| Direct | `db.<ref>.supabase.co:5432` | IPv6 (IPv4 only with the paid IPv4 add-on) | Works, but not from Render, which has no IPv6 |
| **Shared pooler, session mode** | `aws-<n>-<region>.pooler.supabase.com:5432` | IPv4 | **Use this** |
| Shared pooler, transaction mode | `aws-<n>-<region>.pooler.supabase.com:6543` | IPv4 | **Never** |

The API elects one replica to run the reconciler, and one to run the
webhook redeliverer, with session-level advisory locks
(`pg_try_advisory_lock`, then `pg_advisory_unlock`). Transaction mode can
send the lock and the unlock to different server connections. The lock
then stays held where no replica can release it, and **the reconciler stops
running on every replica**: finished machines aren't terminated, idle
runners aren't reaped, lost webhooks aren't recovered. Session mode gives
each client its own server connection for as long as it's connected, the
same as a direct connection.

Migrations also run over the session pooler.

### 2. Nothing reachable through the Data API

Supabase serves tables in the `public` schema over HTTP (the Data API, also
called PostgREST). Airship's tables hold customers' sealed cloud
credentials and session tokens, and don't use row-level security. Airship
enforces access in its own API. So:

- the **Data API is switched off** (step 3), and
- Airship's tables belong to their **own role** (step 2). Supabase's
  default grants to `anon` and `authenticated` only cover tables the
  `postgres` role creates, so tables created by `airship` get no grants to
  them.

Either one alone closes the hole. Do both, so a later settings change can't
reopen it.

---

## Step 1 — Create the project

| Setting | Choose |
| --- | --- |
| Plan | **Pro** or above. Free projects pause when inactive and have no backups |
| Region | The same city as the API ([render-vercel.md, step 1](render-vercel.md#step-1--the-database)) |
| Database password | Generated. This is for the `postgres` role, which Airship won't use. Keep it in your password manager |
| Compute | The smallest is enough. The database holds rows per job and per runner, and events are pruned after 30 days |

Then check the version: `select version();` should say 17 or newer.

---

## Step 2 — A role for Airship

In the **SQL Editor**, as `postgres`:

```sql
create role airship with login password '<generate: openssl rand -hex 24>';
grant usage, create on schema public to airship;
```

Migrations run as `airship`, so it owns every table, index and type it
creates. Through the shared pooler, the user name gets the project ref
appended: `airship.<ref>`.

The connection string, from **Connect → Session pooler** with the user and
password swapped:

```
postgres://airship.<ref>:<password>@aws-<n>-<region>.pooler.supabase.com:5432/postgres?sslmode=require&pool_max_conns=4
```

- Copy the host from the Connect dialog. `<n>` is a pooler cluster number
  and can't be worked out from the region.
- `sslmode=require` encrypts the connection. `verify-full` also checks the
  certificate, but Supabase's is signed by its own root, which isn't in the
  API image's trust store. You would have to add the certificate from
  **Database Settings → SSL Configuration** as a file and point
  `sslrootcert=` at it. On Render, a Secret File does that: it's mounted at
  `/etc/secrets/<name>`.
- `pool_max_conns=4` caps each API instance at 4 connections (see step 4).
- Percent-encode the password if it contains `@`, `:`, `/`, `?` or `#`
  (the `hex` one above doesn't).

Check it from your machine:

```bash
psql 'postgres://airship.<ref>:<password>@aws-<n>-<region>.pooler.supabase.com:5432/postgres?sslmode=require' \
  -c 'select version(); select current_user;'
# PostgreSQL 17.x ...   airship
```

---

## Step 3 — Switch off the Data API

**Project Settings → Data API** (or **Integrations → Data API**): turn
**Enable Data API** off. With it off, none of the generated REST endpoints
answer, whatever the grants.

As a second guard, after the first migration has run:

```sql
-- Expect no rows: nothing of Airship's is granted to the API roles.
select table_name, grantee, privilege_type
from information_schema.role_table_grants
where table_schema = 'public' and grantee in ('anon', 'authenticated');
```

If rows come back (because the tables were created as `postgres`), revoke
them:

```sql
revoke all on all tables in schema public from anon, authenticated;
revoke all on all sequences in schema public from anon, authenticated;
```

Never put Supabase's `anon` or `service_role` keys anywhere in Airship. It
doesn't use them.

---

## Step 4 — Pool size

In session mode, every client connection holds one of the pooler's server
connections. The limit is the **Pool Size** under **Database Settings →
Connection pooling**, which is small on the smallest compute sizes.

Airship's usage, with `pool_max_conns=4`:

| | Connections |
| --- | --- |
| 2 API instances | 8 |
| A third instance during a rolling deploy | 4 |
| The migration, during a deploy | 1 |
| Supabase's dashboard and your own `psql` | a few |

Set the pool size to at least **20**. If it's too small, new connections
fail with `max clients reached`, and the API's `/healthz` turns `degraded`.

---

## Step 5 — Network and TLS

Under **Database Settings**:

- **Enforce SSL on incoming connections**: on.
- **Network Restrictions**: allow only the API's outbound addresses. On
  Render they're listed on the service's **Connect → Outbound** tab. Add
  your own address for `psql` when you need it. Render's ranges are shared
  by every Render service in the region, so this narrows who can try, but
  the password and TLS still do the work.

---

## Step 6 — Point the deploy guide at it

| Guide | Change |
| --- | --- |
| [render-vercel.md](render-vercel.md) | `DATABASE_URL` is the session-pooler string from step 2 |
| [aws.md](aws.md), [gcp.md](gcp.md), [azure.md](azure.md) | Skip the guide's database step and store the step 2 string as its database secret, as [planetscale.md, step 3](planetscale.md#step-3--point-the-deploy-guide-at-it) describes |
| [server.md](server.md) | Remove the `postgres` service and set `DATABASE_URL` for `api` and `migrate` to the step 2 string |

Migrations run the same way: `migrate up`, once per release, before the
rollout.

---

## Operations

| Item | How |
| --- | --- |
| Backups | Pro takes daily backups. Turn on the **Point-in-Time Recovery** add-on for production. Restore one into a new project once, as the [production checklist](README.md#production-checklist) asks. A restore is useless without the same `CREDENTIALS_KEY` |
| Rotating the password | `alter role airship password '...'`, update `DATABASE_URL` on Render, deploy. Instances using the old password keep their open connections until they restart |
| Unused features | Auth, Storage, Realtime and Edge Functions stay unused. Leave them empty |
| Upgrades | Supabase applies minor versions in maintenance. Major versions use its upgrade flow, which takes the database offline for a while. Plan it outside busy hours |

---

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| Migrations fail with `permission denied for schema public` | The `grant usage, create on schema public to airship` from step 2 is missing |
| Finished machines stay up until `RUNNER_MAX_LIFETIME`, idle runners are never reaped | `DATABASE_URL` uses port 6543 (transaction mode). Switch to 5432 and deploy. The stuck lock may hold until the pooler drops that connection; restarting the project clears it at once |
| `dial tcp [2a05:...]:5432: connect: network is unreachable` | The direct connection's IPv6 address, from a platform without IPv6. Use the session pooler |
| `FATAL: Tenant or user not found` | The user is `airship` instead of `airship.<ref>`, or the host is a different pooler cluster than the Connect dialog shows |
| `max clients reached` / `/healthz` says `degraded` under deploys | Pool size too small (step 4) |
