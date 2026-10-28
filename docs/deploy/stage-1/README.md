# Stage 1 — AWS

**What this is.** Where the control plane goes when managed hosting is no
longer the right answer: ECS Fargate behind an ALB, RDS for PostgreSQL,
Secrets Manager, all in one account you own. The guide is
[aws.md](aws.md).

**Audience.** Whoever will own the AWS account that hosts Airship.

**You are probably not here yet.** [Stage 0](../stage-0/) is a real
deployment — two replicas, managed backups, TLS — and it costs a fifth as
much to run and a tenth as much to operate. Move when one of the reasons
below is true, not before.

---

## When to move

| Reason | Why stage 0 can't do it |
| --- | --- |
| A customer requires the control plane in their own account or VPC | Render is multi-tenant hosting; there is no VPC to put it in |
| Private database networking is required | Supabase is reached over the public internet with TLS. Stage 1 keeps RDS in private subnets with a security group |
| A compliance regime names AWS, or needs a BAA/DPA you can only get there | |
| A customer allow-lists outbound IPs | Render's outbound ranges are shared by every Render service in the region |
| Committed AWS spend makes it cheaper | |
| You need more than two replicas, or autoscaling on queue depth | Render scales manually |

Cost and bus factor move the other way: stage 1 is roughly $250/month and a
day to build, plus a VPC, an ALB, ECR, ECS and RDS for someone to own.

---

## What carries over

| | Carries over | Notes |
| --- | --- | --- |
| The domain and both hostnames | **Yes** | Only the DNS records change, from CNAMEs to the ALB |
| The GitHub App | **Yes**, unchanged | Same slug, client ID, secret, webhook secret and private key. The callback and webhook URLs only change if the hostnames do |
| `CREDENTIALS_KEY` | **Yes — it must.** | Carry the same value into Secrets Manager. A new key makes every stored cloud credential unreadable and every customer reconnects |
| The database contents | **Yes**, by dump and restore (below) | Installations, repositories, runners, jobs, events, sessions and sealed credentials |
| Every other environment variable | **Yes**, same names and meanings ([env.md](../stage-0/env.md)) | Only where they are stored changes: Secrets Manager instead of Render's environment |
| Customers' cloud credentials | **Yes**, as sealed rows in the database | Nobody has to reconnect, as long as `CREDENTIALS_KEY` is the same |
| The images | Built the same way | Stage 1 builds and pushes them to ECR instead of letting the platforms build |

Nothing a customer did is thrown away, and no workflow file changes.

---

## Moving the database

Supabase → RDS, the short version. Full dump and restore, not a live
replication setup: the pilot's database is small, and a few minutes of
downtime is cheaper than streaming replication between two providers.

```bash
# 1. Stop the control plane so nothing writes. Render: scale to 0 instances.
#    Queued jobs are picked up again afterwards by the stale-job sweep,
#    and running jobs are unaffected — the runner talks to GitHub, not us.

# 2. Dump, as the owner.
pg_dump --no-owner --no-privileges --format=custom \
  'postgres://airship.<ref>:<pw>@aws-<n>-<region>.pooler.supabase.com:5432/postgres?sslmode=require' \
  -f airship.dump

# 3. Restore into the new RDS database (created by aws.md's step for it).
pg_restore --no-owner --no-privileges --dbname \
  'postgres://airship:<pw>@airship.<id>.<region>.rds.amazonaws.com:5432/airship?sslmode=require' \
  airship.dump

# 4. Check the schema is at the same version, as the same user it will run as.
cd apps/api && DATABASE_URL='<the RDS string>' go run ./cmd/migrate status
```

`--no-owner --no-privileges` is deliberate: the roles differ between the
two providers, and the migrations' ownership is re-established by whoever
runs `migrate` from then on.

Then, in order: bring up the ECS services with the same variables pointing
at RDS, verify `/healthz` on the ALB's own hostname, move the DNS records,
and only then delete the Render service. **DNS last** — it is the one step
that is awkward to reverse quickly.

Keep the Supabase project paused rather than deleted for a week, as a
rollback.

---

## Then

[aws.md](aws.md) is the full guide: VPC, ALB, ECR, ECS, RDS, Secrets
Manager, with the CLI commands for each. It assumes you already have the
domain, the GitHub App and `CREDENTIALS_KEY` from
[stage 0, steps 1–2](../stage-0/) — those are stage-independent, and
there is no reason to recreate them.

Work through [checklist.md](../checklist.md) again afterwards, including
the items marked **[stage 1]**, which only apply here.

Already on Google Cloud or Azure instead? The same shape for those is in
[alternatives/](../alternatives/).
