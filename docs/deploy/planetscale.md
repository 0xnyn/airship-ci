# Using PlanetScale Postgres as the database

**What this is.** How to use [PlanetScale Postgres](https://planetscale.com/docs/postgres)
in place of RDS, Cloud SQL or Azure's flexible server. Use it with any of
the compute guides ([AWS](aws.md), [Google Cloud](gcp.md), [Azure](azure.md),
[one server](server.md)): set up the database here, then follow the guide
for the API and dashboard, skipping its database step.

**Audience.** Whoever deploys Airship.

---

## The one rule: port 5432, never 6432

PlanetScale gives every database two ports with the same credentials:

| Port | What | For Airship |
| --- | --- | --- |
| **5432** | Direct connection to Postgres | **Use this** |
| 6432 | PgBouncer in **transaction** mode | Do not use |

The API elects one replica to run the reconciler, and one to run the
webhook redeliverer, with **session-level** advisory locks
(`pg_try_advisory_lock`, then `pg_advisory_unlock` when the pass ends).
Transaction pooling only keeps a client on one server connection for the
length of a transaction. The lock and the unlock can therefore land on
different server connections. The unlock then does nothing, the lock stays
held by a pooled connection no replica controls, and **the reconciler stops
running on every replica**. Finished machines are no longer terminated,
idle runners are no longer reaped, and lost webhooks are no longer
recovered.

The API already pools its own connections (pgx, a few per replica), so
PgBouncer gains it nothing. Migrations run DDL, which PlanetScale also
recommends doing over the direct port.

---

## Step 1 — Create the database

In the PlanetScale dashboard, **New database → Postgres**:

| Setting | Choose |
| --- | --- |
| Version | **18** (the default). Airship requires 18 or newer: the schema uses `uuidv7()` |
| Cloud and region | **The same cloud and region as the API.** Every launch and reconcile pass makes several round trips, so cross-region latency slows everything down, and cross-cloud traffic is billed as egress |
| Cluster size | The smallest production size is enough. The database holds rows per job and per runner, and events are pruned after 30 days |

Where to put it, per compute guide:

| API on | PlanetScale region |
| --- | --- |
| AWS (ECS) | The AWS region the cluster runs in, e.g. `eu-west-1` |
| Google Cloud (Cloud Run) | The same GCP region, if PlanetScale offers it; otherwise the nearest GCP region |
| Azure (Container Apps) | PlanetScale does not run on Azure. Pick the AWS or GCP region physically closest to your Azure region, and expect a few milliseconds more per query plus public egress charges. If that matters, the flexible server in [azure.md](azure.md) is the better fit |
| One server | Whichever region is closest to the server |

PlanetScale does not upgrade between major Postgres versions in place. Going
from 18 to 19 later means a new database and an online migration, so start
on the latest.

---

## Step 2 — Create a role for Airship

Under the database's **Connect** page, create a role (for example
`airship`) and copy its password. It is shown once. The role needs to
create tables, indexes and types in the database, because migrations run
as it. If the first migration fails with `permission denied`, give the
role more privileges on the Connect page, or grant them over SQL.

Build the connection string for the **direct port**:

```
postgres://airship:<password>@<host>:5432/postgres?sslmode=verify-full
```

- `<host>` is the hostname on the Connect page.
- The database name is `postgres` unless you created another
  (`CREATE DATABASE airship;` over the same connection).
- `sslmode=verify-full` checks PlanetScale's certificate against the system
  trust store. The API image carries the standard CA bundle, so no
  `sslrootcert` is needed. PlanetScale only accepts TLS connections.
- Percent-encode the password if it contains `@`, `:`, `/`, `?` or `#`.

Check it from your machine before deploying anything:

```bash
psql 'postgres://airship:<password>@<host>:5432/postgres?sslmode=verify-full' -c 'select version()'
# PostgreSQL 18.x ...
```

---

## Step 3 — Point the deploy guide at it

Follow your compute guide with these changes:

| Guide | Skip | Change |
| --- | --- | --- |
| [aws.md](aws.md) | Step 2 (RDS) and the `airship-db` security group | Store the PlanetScale URL as `airship/database-url` in step 3. The NAT gateway already gives the tasks the outbound access they need. For private traffic, see AWS PrivateLink below |
| [gcp.md](gcp.md) | Step 2 (Cloud SQL), the `roles/cloudsql.client` binding | Store the PlanetScale URL as `airship-database-url`. Drop `--add-cloudsql-instances` and `--set-cloudsql-instances` from the service and the job. Drop `sqladmin.googleapis.com` from the APIs |
| [azure.md](azure.md) | Step 2 (flexible server), the `db` subnet | Store the PlanetScale URL as `database-url` in Key Vault. Container Apps reaches it over the internet |
| [server.md](server.md) | The `postgres` service | In `compose.yaml`, set `DATABASE_URL` for `api` and `migrate` to the PlanetScale URL (better: `DATABASE_URL: ${DATABASE_URL}` and put it in `.env`), and remove `postgres` and the `depends_on` entries that name it |

Everything else is unchanged. Migrations run the same way (`migrate up`,
once per release, before the rollout), and `/healthz` reports the database
as `ok` once the API can reach it.

---

## Private connectivity (optional)

By default the API connects over the public internet with TLS, which is
what PlanetScale recommends for most deployments. To keep database traffic
inside your cloud's network:

| Cloud | Option |
| --- | --- |
| AWS | [AWS PrivateLink](https://planetscale.com/changelog/privatelink-postgres): an interface endpoint in the VPC from [aws.md](aws.md), in the private subnets. Then use the private hostname PlanetScale gives you in `DATABASE_URL` |
| Google Cloud | [Private Service Connect](https://planetscale.com/docs/postgres/connecting/private-connections/gcp-private-service-connect): an endpoint in a VPC. Cloud Run then needs Direct VPC egress (`--network`, `--subnet`) to reach it |
| Azure | None. Traffic crosses the internet, with TLS |

Which plans include private connections, and what they cost, changes over
time. Check PlanetScale's current pricing before you rely on it.

---

## Operations

| Item | How |
| --- | --- |
| Connections | Each API replica opens at most `max(4, CPU count)` connections, so two replicas use about 10. That fits any cluster size's limit. To cap it lower, add `&pool_max_conns=4` to `DATABASE_URL` |
| Backups | PlanetScale takes them automatically. Check the schedule and retention for your cluster size in the dashboard, and restore one into a new branch or database once, as the [production checklist](README.md#production-checklist) asks. A restore is useless without the same `CREDENTIALS_KEY` |
| Replicas | PlanetScale's read replicas are not used. Airship sends every query to the primary |
| Rotating the password | Create a second role, update the `DATABASE_URL` secret, roll out the API, then delete the old role |

---

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| Migrations fail with `function uuidv7() does not exist` | The database is on Postgres 17. Create a new one on 18 |
| Jobs launch, but finished machines stay up until `RUNNER_MAX_LIFETIME`, and idle runners are never reaped | `DATABASE_URL` uses port 6432. Switch to 5432 and roll out the API. The stuck lock lives on a pooled server connection until PgBouncer closes it, so the reconciler may still not run until then. Restarting the database clears it at once |
| `/healthz` says `degraded` | The API cannot reach PlanetScale within 2 seconds. Check outbound access (NAT gateway on AWS) and the password |
| `x509: certificate signed by unknown authority` | The image has no CA bundle, which happens if you replaced the base image. Use the stock `apps/api/Dockerfile` |
