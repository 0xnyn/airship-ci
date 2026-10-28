# Production checklist

**What this is.** What every Airship deployment has to pass before real
jobs run on it, whichever platform it is on. Each guide shows how to meet
each item.

Items marked **[stage 1]** do not apply to a stage-0 pilot, where the
platform owns that concern. Everything else applies from the first
customer.

---

## Configuration

- [ ] `PUBLIC_URL` and `DASH_URL` are the https custom domains, and
      `CORS_ORIGINS` is exactly `DASH_URL`.
- [ ] `CREDENTIALS_KEY`, `DATABASE_URL`, `GITHUB_CLIENT_SECRET`,
      `GITHUB_WEBHOOK_SECRET` and the private key are stored as secrets
      (Render's encrypted environment on stage 0, Secrets Manager on
      stage 1), not in a file in the repository.
- [ ] `CREDENTIALS_KEY` is backed up **outside** the platform and outside
      the database — your password manager. A database backup without this
      key cannot open a single credential.
- [ ] `DEV_LOGIN` and every `*_ENDPOINT` variable are unset. The API
      refuses to start otherwise for `DEV_LOGIN`, but the endpoints only
      send launches to the wrong place.
- [ ] The dashboard was built with the production `NEXT_PUBLIC_API_URL`.

Where each value comes from: [stage-0/env.md](stage-0/env.md).

## Runtime

- [ ] **At least one API replica is always running, with CPU always
      allocated.** The launcher and reconciler are background loops, not
      requests. Scale-to-zero, or CPU that is only allocated during
      requests, stops launches and leaves finished machines running until
      their own deadline. Two replicas is the recommendation.
- [ ] The load balancer's health check is `GET /healthz` on the API.
- [ ] The platform waits at least 30 seconds after `SIGTERM` before killing
      an API container, so requests in flight can finish.
- [ ] Migrations run as a separate step before each rollout, never from
      every replica at startup.

## Network

- [ ] GitHub can reach `https://api.example.com/v1/github/webhook` from the
      internet.
- [ ] The API can reach the internet: `api.github.com`, and each cloud's
      API endpoints (the list is in
      [operations.md, "Networking requirements"](../architecture/operations.md#networking-requirements)).
      **[stage 1]** In a private subnet, that means a NAT gateway.
- [ ] Runner machines can reach `PUBLIC_URL` over 443. This only matters for
      `cache: true`, but without it caching is silently off.
- [ ] Postgres is reachable only from the API, with TLS. On stage 0 that is
      Supabase's **Enforce SSL** plus network restrictions
      ([3-supabase.md](stage-0/3-supabase.md#step-5--network-and-tls));
      **[stage 1]** a security group that allows only the API's.

## Data

- [ ] Postgres is version 17 or newer (18 where the platform offers it).
- [ ] Automated backups with point-in-time recovery are on. On Supabase
      that is the Pro plan plus the PITR add-on.
- [ ] You have restored a backup once, into a scratch database, and opened
      the dashboard against it with the production `CREDENTIALS_KEY`.

## Verification

- [ ] `curl https://api.example.com/healthz` returns `"status":"ok"` and
      the version you deployed.
- [ ] GitHub App → **Advanced → Recent Deliveries** shows `200`s.
- [ ] **Sign in with GitHub** on `https://app.example.com` lands on the
      dashboard, not back on the sign-in page.
- [ ] Install the app on a test organization, connect a cloud, activate a
      repository and run a job with
      `runs-on: airship-aws-t3-medium-ubuntu2404--${{ github.run_id }}` (or
      the equivalent for another cloud). The job goes green and the machine
      disappears from **Runners** within a few minutes.

Step 6 of the pilot runbook walks the last three
([6-first-job.md](stage-0/6-first-job.md)).

## Ongoing

- [ ] Someone owns bumping `RUNNER_VERSION`. GitHub stops sending jobs to
      runners more than 30 days behind the latest
      [actions/runner release](https://github.com/actions/runner/releases).
- [ ] Alerts on the log lines listed in
      [operations.md, "Observability"](../architecture/operations.md#observability).
- [ ] Someone knows how to roll back, and has the incident runbook in
      [operations.md](../architecture/operations.md#incident-runbook) to
      hand.
