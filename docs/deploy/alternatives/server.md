# Deploying Airship on one server

**What this is.** Airship's whole control plane (Postgres, the API, the
dashboard and Caddy for TLS) on one Linux machine with Docker Compose,
from [`deploy/compose`](../../../deploy/compose). Any provider works: an EC2
instance, a Compute Engine VM, an Azure VM, a Hetzner box, a machine in
your own rack.

**Audience.** Small teams, trials, and anyone who wants the control plane
on hardware they already run.

**Time.** 20 minutes.

Do [stage 0's steps 1 and 2](../stage-0/) first: the domain and two
hostnames, `CREDENTIALS_KEY`, and the production GitHub App are the same
however Airship is hosted. Where each environment variable's value comes
from is in [stage-0/env.md](../stage-0/env.md).

**What you give up** compared with the managed guides: one machine is one
point of failure, and the database backups are yours to run. When the
server is down, no new runners launch. Jobs already running finish, and
every machine still powers itself off at its own deadline. For many teams
that is an acceptable trade.

---

## Step 1 — The server

| Need | Notes |
| --- | --- |
| Linux, 2 vCPU, 2 GB RAM, 20 GB disk | Plenty. Building the dashboard image is the heaviest moment |
| Docker Engine with the Compose plugin | [docs.docker.com/engine/install](https://docs.docker.com/engine/install/) |
| A public IP | GitHub and runner machines must reach it on 443 |
| Inbound 80 and 443 open | 80 for Let's Encrypt's HTTP challenge and the redirect. Close everything else except SSH |
| Two DNS records | `api.example.com` and `app.example.com`, A records to the server's IP, **before** the first start |

---

## Step 2 — Configure

```bash
git clone <this repository> airship && cd airship/deploy/compose
cp .env.example .env
chmod 600 .env
```

Fill in `.env`:

| Variable | Value |
| --- | --- |
| `API_DOMAIN`, `DASH_DOMAIN` | The two names from step 1 |
| `ACME_EMAIL` | Where Let's Encrypt sends expiry warnings |
| `POSTGRES_PASSWORD` | `openssl rand -hex 24` |
| `CREDENTIALS_KEY` | `openssl rand -base64 32`. **Copy it off this server now**, to a password manager. A backup without it cannot open any credential |
| `GITHUB_APP_SLUG`, `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`, `GITHUB_WEBHOOK_SECRET` | From the GitHub App |
| `GITHUB_PRIVATE_KEY_PATH` | Leave as `./github-app.pem` and copy the app's key there with `chmod 600` |

Compose refuses to start if a required value is missing, and names it.

---

> **Using PlanetScale instead of the bundled Postgres?** See
> [planetscale.md, step 3](planetscale.md#step-3--point-the-deploy-guide-at-it)
> for the `compose.yaml` change before starting.

## Step 3 — Start

```bash
docker compose up -d --build
docker compose ps
```

What happens in order:

1. `postgres` starts and reports healthy.
2. `migrate` applies the schema and exits with code 0.
3. `api` and `dash` start. `api` waits for `migrate` to succeed.
4. `caddy` gets certificates for both names from Let's Encrypt and starts
   proxying.

```bash
curl -s https://api.example.com/healthz
# {"status":"ok","database":"ok","version":"local"}
```

Then work through the **Verification** items in the
[production checklist](../checklist.md).

If Caddy cannot get a certificate, `docker compose logs caddy` says why.
It is almost always DNS that does not point at this server yet, or port 80
closed by a firewall.

---

## Backups

Postgres lives in the `airship_postgres` Docker volume. Dump it daily, and
copy the dumps off the server:

```bash
# /etc/cron.daily/airship-backup
#!/bin/sh
set -e
cd /path/to/airship/deploy/compose
docker compose exec -T postgres pg_dump -U airship -Fc airship > /var/backups/airship-$(date +%F).dump
find /var/backups -name 'airship-*.dump' -mtime +14 -delete
# then copy to object storage, e.g. aws s3 cp / gcloud storage cp / rclone
```

To restore into a fresh stack:

```bash
docker compose up -d postgres
docker compose exec -T postgres pg_restore -U airship -d airship --clean --if-exists < airship-2026-10-28.dump
docker compose up -d
```

Restore with the same `CREDENTIALS_KEY`, or every customer has to
reconnect their cloud.

---

## Releasing

```bash
cd airship && git pull
cd deploy/compose
AIRSHIP_VERSION=$(git rev-parse --short HEAD) docker compose up -d --build
```

Compose rebuilds both images, runs `migrate` again (it applies only what is
new), and recreates `api` and `dash`. There is a few seconds' gap while the
API restarts. GitHub webhooks that fail in that gap are redelivered by the
API once it is back.

Put `AIRSHIP_VERSION` in `.env` instead to keep the tag between runs, and
to roll back: set it to a tag still in `docker image ls` and run `docker
compose up -d` without `--build`.

---

## Hardening

| Item | How |
| --- | --- |
| Host firewall | Allow 22 (ideally from your network only), 80 and 443. Postgres is not published to the host at all |
| Updates | Unattended security updates for the OS. Rebuild now and then to pick up new base images (`docker compose build --pull`) |
| Disk | Docker images accumulate across releases: `docker image prune` after a release |
| Logs | `docker compose logs -f api`. The API logs JSON. Set Docker's `json-file` driver with `max-size` in `/etc/docker/daemon.json` so logs do not fill the disk |
| Monitoring | An external uptime check on `https://api.example.com/healthz`, alerting when `status` is not `ok` |
| Growing out of it | Move Postgres to a managed database first: point `DATABASE_URL` at it by editing `compose.yaml`, and remove the `postgres` service. Then run a second server with the same `.env` behind a load balancer. Several API replicas are supported |

Day-to-day operation, alerts worth having and the incident runbook are in
[architecture/operations.md](../../architecture/operations.md).
