# Other platforms

**What this is.** Guides for platforms that are not on Airship's own path
(which is [stage 0](../stage-0/) now, [stage 1](../stage-1/) later). They
are kept current because customers ask for them: a team already on Google
Cloud or Azure, or one that has to self-host.

Each one is complete and meets the same
[production checklist](../checklist.md).

| Platform | Guide | API runs on | Database | Good for |
| --- | --- | --- | --- | --- |
| Google Cloud | [gcp.md](gcp.md) | Cloud Run | Cloud SQL for PostgreSQL | The least to operate of the three clouds |
| Azure | [azure.md](azure.md) | Container Apps | Azure Database for PostgreSQL flexible server | Teams already on Azure |
| One server | [server.md](server.md) | Docker Compose with Caddy | Postgres in a container | Small teams, trials, on-premises, air-gapped-ish |
| — | [planetscale.md](planetscale.md) | — | PlanetScale Postgres | A managed database for any of the guides above, in place of the cloud's own |

Two rules carry across all of them, because they come from how Airship
works rather than from any platform:

1. **The API must be a long-running process with at least one replica
   always running.** Scale-to-zero, or CPU allocated only during requests,
   stops the launcher and the reconciler.
2. **Connect to Postgres with a session, not a transaction pooler.** The
   reconciler is elected with a session-level advisory lock. On PlanetScale
   that is port 5432, never PgBouncer's 6432; on Supabase, 5432 and never
   6543.

[Supabase](../stage-0/3-supabase.md) works with any of these too — skip the
guide's database step and use its connection string.

The environment variables are the same everywhere, and where each value
comes from is in [stage-0/env.md](../stage-0/env.md).
