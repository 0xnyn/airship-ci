# Step 3 — The database on Supabase

**What this is.** A [Supabase](https://supabase.com/docs/guides/database/overview)
project as Airship's Postgres, and the two settings that have to be right.
Airship uses only the database: Supabase Auth, Storage, Realtime and the
Data API stay unused, and the Data API is switched off.

**Time.** 20 minutes, of which about 5 is Supabase provisioning.

**Prerequisite.** Step 1: a Supabase account on Pro, and a region.

**Produces.** `DATABASE_URL`, and a database with Airship's schema in it.

> Running Airship locally against the same Postgres version, so pilot
> problems reproduce on your machine, is in [local.md](local.md).

---

## Postgres 17 works

Hosted Supabase projects run Postgres 17. Airship's schema uses `uuidv7()`
for every primary key. Postgres 18 has it built in. On 17, the first
migration defines an equivalent in `public` (a millisecond timestamp
followed by random bits, as RFC 9562 describes), so nothing extra is
needed. `make test-pg17` runs the API's tests against 17 locally, which is
the check to run before shipping anything that touches SQL.

If Supabase later upgrades the project to 18, the function stays in place
and existing tables keep using it. That is harmless.

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
runners aren't reaped, lost webhooks aren't recovered. Nothing errors —
the bill just climbs. Session mode gives each client its own server
connection for as long as it's connected, the same as a direct connection.

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

**New project**, in the organization you created in step 1:

| Setting | Choose |
| --- | --- |
| Name | `airship-pilot` |
| Plan | **Pro**. Free projects pause when inactive and have no backups |
| Region | The one from [1-accounts.md, "Pick one region"](1-accounts.md#2-pick-one-region-everywhere) — the same city as Render. **Cannot be changed later** |
| Database password | Generated. This is for the `postgres` role, which Airship won't use. Keep it in your password manager |
| Compute | The smallest is enough. The database holds rows per job and per runner, and events are pruned after 30 days |

Provisioning takes a few minutes. Then, in the **SQL Editor**:

```sql
select version();
-- PostgreSQL 17.x ...
```

---

## Step 2 — A role for Airship

In the **SQL Editor**, as `postgres`:

```sql
create role airship with login password '<generate: openssl rand -hex 24>';
grant usage, create on schema public to airship;
```

Generate that password with `openssl rand -hex 24` and keep it with the
rest. Migrations run as `airship`, so it owns every table, index and type
it creates.

Now build the connection string. Open **Connect** (top of the dashboard) →
**Session pooler**, and swap in the user and password:

```
postgres://airship.<ref>:<password>@aws-<n>-<region>.pooler.supabase.com:5432/postgres?sslmode=require&pool_max_conns=4
```

| Part | Where it comes from |
| --- | --- |
| `airship.<ref>` | Through the shared pooler the user name carries the project ref: the role from above, a dot, then your project's ref (the `<ref>` in the host Supabase shows). **`airship` alone fails** with `Tenant or user not found` |
| `<password>` | The `openssl rand -hex 24` above. Percent-encode it if it contains `@`, `:`, `/`, `?` or `#` — the `hex` one doesn't |
| the host | Copy it from the Connect dialog. `<n>` is a pooler cluster number and can't be worked out from the region |
| `:5432` | Session mode. **Never 6543** |
| `sslmode=require` | Encrypts the connection. `verify-full` also checks the certificate, but Supabase's is signed by its own root, which isn't in the API image's trust store — you would have to add the certificate from **Database Settings → SSL Configuration** as a file and point `sslrootcert=` at it. On Render, a Secret File does that: it's mounted at `/etc/secrets/<name>` |
| `pool_max_conns=4` | Caps each API instance at 4 connections (step 4) |

Check it from your machine:

```bash
psql 'postgres://airship.<ref>:<password>@aws-<n>-<region>.pooler.supabase.com:5432/postgres?sslmode=require' \
  -c 'select version(); select current_user;'
# PostgreSQL 17.x ...   airship
```

This string is `DATABASE_URL`. It is the single most sensitive value after
`CREDENTIALS_KEY`.

---

## Step 3 — Switch off the Data API

**Project Settings → Data API** (or **Integrations → Data API**): turn
**Enable Data API** off. With it off, none of the generated REST endpoints
answer, whatever the grants.

As a second guard, after the migrations in step 6 have run:

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
  Render they're listed on the service's **Connect → Outbound** tab, so
  come back here after [step 4](4-render.md). Add your own address for
  `psql` when you need it. Render's ranges are shared by every Render
  service in the region, so this narrows who can try, but the password and
  TLS still do the work.

Leave this open until step 4 exists, then close it.

---

## Step 6 — Apply the schema

Render runs the migrations on every deploy, so this is not strictly
necessary. Do it anyway: it proves the connection string works before
Render is in the picture, which turns a confusing failed deploy into an
obvious `psql` error.

From the repository root, with Go installed:

```bash
cd apps/api
DATABASE_URL='postgres://airship.<ref>:...@aws-<n>-<region>.pooler.supabase.com:5432/postgres?sslmode=require' \
  go run ./cmd/migrate up
# goose: successfully migrated database to version: N
```

A variable set on the command line wins over `apps/api/.env`, so this does
not touch your local database. Confirm the owner is right:

```bash
DATABASE_URL='...' go run ./cmd/migrate status
```

Or in the SQL Editor:

```sql
select tablename, tableowner from pg_tables
where schemaname = 'public' order by tablename;
-- every row: tableowner = airship
```

If `tableowner` says `postgres` anywhere, the migrations ran as the wrong
user: drop the schema (`drop schema public cascade; create schema public;`,
then re-grant as in step 2) and rerun with the `airship` string.

---

## Checkpoint

- [ ] `select version()` says 17 or newer.
- [ ] The `airship` role exists and owns every table in `public`.
- [ ] `DATABASE_URL` uses **port 5432** and the `airship.<ref>` user, and
      `psql` connects with it.
- [ ] Data API is off, and the grants query returns no rows.
- [ ] Pool size is at least 20, **Enforce SSL** is on.
- [ ] `migrate status` shows every migration applied.

Next: [step 4 — the API on Render](4-render.md).

---

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| Migrations fail with `permission denied for schema public` | The `grant usage, create on schema public to airship` from step 2 is missing |
| Finished machines stay up until `RUNNER_MAX_LIFETIME`, idle runners are never reaped | `DATABASE_URL` uses port 6543 (transaction mode). Switch to 5432 and deploy. The stuck lock may hold until the pooler drops that connection; restarting the project clears it at once |
| `dial tcp [2a05:...]:5432: connect: network is unreachable` | The direct connection's IPv6 address, from a platform without IPv6. Use the session pooler |
| `FATAL: Tenant or user not found` | The user is `airship` instead of `airship.<ref>`, or the host is a different pooler cluster than the Connect dialog shows |
| `max clients reached` / `/healthz` says `degraded` under deploys | Pool size too small (step 4) |
| `password authentication failed` with a password you know is right | An unencoded `@`, `:`, `/`, `?` or `#` in the password. Percent-encode it |

Backups, upgrades and rotating the database password are in
[operate.md](operate.md#the-database).

---

## Using Supabase from another stage

Nothing here is specific to Render. To use this database from
[stage 1](../stage-1/aws.md) or [another platform](../alternatives/),
skip that guide's database step and store the step 2 string as its database
secret, as
[planetscale.md, step 3](../alternatives/planetscale.md#step-3--point-the-deploy-guide-at-it)
describes for the same situation.
