# Deploying Airship on Google Cloud

**What this is.** Airship's control plane on Cloud Run, with Cloud SQL for
PostgreSQL, Secret Manager, and a global external Application Load Balancer
for the two custom domains. It goes from an empty project to a green job,
with the `gcloud` commands for each step.

**Audience.** Whoever owns the Google Cloud project that will host Airship.
That can be a different project from the ones runners launch in.

**Time.** About an hour, most of it waiting for Cloud SQL and the managed
certificate.

Read [README.md](README.md) first: its "Before you start" section (domain,
credentials key, GitHub App) applies here unchanged.

---

## The result

```
                 DNS: api.example.com, app.example.com ──► one global IP
                                       │
              ┌────────────────────────▼──────────────────────────┐
              │ external Application Load Balancer, managed cert  │
              │   host api.*  ──► serverless NEG ──► airship-api  │
              │   host app.*  ──► serverless NEG ──► airship-dash │
              └────────────────────────┬──────────────────────────┘
                                       │
   ┌───────────────────────────────────▼─────────────┐   ┌────────────────────┐
   │ Cloud Run airship-api, min 2 instances,         │   │ Cloud Run          │
   │ CPU always allocated ──► GitHub, clouds         │   │ airship-dash       │
   └───────────────┬─────────────────────────────────┘   └────────────────────┘
                   │ Unix socket via the built-in Cloud SQL connection
          ┌────────▼──────────┐
          │ Cloud SQL Postgres│  regional (HA), PITR
          │ 18                │
          └───────────────────┘
   Secret Manager: airship-* ──► env vars, read by the airship-api service account
```

The service account the API runs as has exactly two roles: connect to
Cloud SQL and read its secrets. Airship never uses the identity it runs
under to launch runners; it uses the credentials customers connect.

---

## Two Cloud Run settings that are not optional

1. **CPU always allocated** (`--no-cpu-throttling`, instance-based billing).
   With the default request-based billing, Cloud Run throttles CPU to almost
   nothing between requests. The launcher and reconciler are background
   loops, so launches stall and finished machines are not cleaned up
   until their own deadline.
2. **Minimum instances of at least 1** (`--min-instances=2` below). At zero
   instances there is nothing to run the loops at all.

Cloud Run gives a container 10 seconds after `SIGTERM`, less than the API's
30-second drain. That is acceptable: API requests are short, and a launch
interrupted mid-call is covered by its 5-minute lease and picked up by
another instance.

---

## Step 0 — Set the variables

```bash
export PROJECT=my-airship-project
export REGION=europe-west1
export API_DOMAIN=api.example.com
export DASH_DOMAIN=app.example.com
export VERSION=$(git rev-parse --short HEAD)
export REGISTRY=$REGION-docker.pkg.dev/$PROJECT/airship

gcloud config set project $PROJECT
gcloud services enable run.googleapis.com sqladmin.googleapis.com \
  secretmanager.googleapis.com artifactregistry.googleapis.com compute.googleapis.com
```

---

## Step 1 — The service account

```bash
gcloud iam service-accounts create airship-api --display-name "Airship API"
export SA=airship-api@$PROJECT.iam.gserviceaccount.com

gcloud projects add-iam-policy-binding $PROJECT \
  --member serviceAccount:$SA --role roles/cloudsql.client
```

Secret access is granted per secret in step 3, not project-wide.

---

## Step 2 — The database

> **Using PlanetScale instead?** Skip this step and follow
> [planetscale.md](planetscale.md), which lists what else changes in this
> guide.

```bash
DB_PASSWORD=$(openssl rand -hex 24)

gcloud sql instances create airship \
  --database-version=POSTGRES_18 --edition=ENTERPRISE \
  --tier=db-custom-1-3840 --region=$REGION \
  --availability-type=REGIONAL \
  --storage-type=SSD --storage-size=10 --storage-auto-increase \
  --backup-start-time=03:00 --enable-point-in-time-recovery \
  --deletion-protection        # 5–15 minutes

gcloud sql databases create airship --instance=airship
gcloud sql users create airship --instance=airship --password="$DB_PASSWORD"
export CONNECTION=$(gcloud sql instances describe airship --format='value(connectionName)')
```

Use Postgres **18**. Airship also runs on 17, but there is no
reason to start a new database on it. One vCPU is plenty
for most installations. For a trial, use `--availability-type=ZONAL` to
halve the cost.

Cloud Run reaches the instance through its built-in Cloud SQL connection, a
Unix socket at `/cloudsql/$CONNECTION`. The connection is encrypted and
authorized by the service account's IAM role, so the database needs no
public authorized networks, and the connection string has no `sslmode`:

```
postgres://airship:<password>@/airship?host=/cloudsql/<project>:<region>:airship
```

---

## Step 3 — Secrets

Have the GitHub App's values from
[README.md, step 3](README.md#3-the-production-github-app) to hand, and the
private key at `./github-app.pem`.

```bash
secret() {
  printf '%s' "$2" | gcloud secrets create "$1" --replication-policy=automatic --data-file=-
  gcloud secrets add-iam-policy-binding "$1" \
    --member serviceAccount:$SA --role roles/secretmanager.secretAccessor >/dev/null
}

secret airship-database-url "postgres://airship:$DB_PASSWORD@/airship?host=/cloudsql/$CONNECTION"
secret airship-credentials-key "$(openssl rand -base64 32)"
secret airship-github-client-secret "<client secret>"
secret airship-github-webhook-secret "<webhook secret>"
secret airship-github-private-key "$(cat github-app.pem)"
```

Copy the credentials key somewhere outside this project:

```bash
gcloud secrets versions access latest --secret=airship-credentials-key
```

---

## Step 4 — Images

```bash
gcloud artifacts repositories create airship --repository-format=docker --location=$REGION
gcloud auth configure-docker $REGION-docker.pkg.dev

docker build --platform linux/amd64 --build-arg VERSION=$VERSION \
  -t $REGISTRY/api:$VERSION apps/api
docker build --platform linux/amd64 -f apps/dash/Dockerfile \
  --build-arg NEXT_PUBLIC_API_URL=https://$API_DOMAIN \
  -t $REGISTRY/dash:$VERSION .
docker push $REGISTRY/api:$VERSION
docker push $REGISTRY/dash:$VERSION
```

---

## Step 5 — Migrate

Migrations run as a Cloud Run job: the API image with the `migrate`
entrypoint.

```bash
gcloud run jobs create airship-migrate \
  --image=$REGISTRY/api:$VERSION --region=$REGION \
  --service-account=$SA --set-cloudsql-instances=$CONNECTION \
  --set-secrets=DATABASE_URL=airship-database-url:latest \
  --command=/usr/local/bin/migrate --args=up \
  --max-retries=0 --task-timeout=10m

gcloud run jobs execute airship-migrate --region=$REGION --wait
```

`--wait` returns non-zero if the migration failed. The logs, under the job
in the console or `gcloud logging read`, end with `goose: successfully
migrated database to version: N`.

---

## Step 6 — The services

```bash
gcloud run deploy airship-api \
  --image=$REGISTRY/api:$VERSION --region=$REGION \
  --service-account=$SA --add-cloudsql-instances=$CONNECTION \
  --port=8080 --cpu=1 --memory=512Mi \
  --no-cpu-throttling --min-instances=2 --max-instances=4 \
  --ingress=internal-and-cloud-load-balancing --allow-unauthenticated \
  --set-env-vars="PUBLIC_URL=https://$API_DOMAIN,DASH_URL=https://$DASH_DOMAIN,CORS_ORIGINS=https://$DASH_DOMAIN,GITHUB_APP_SLUG=<app slug>,GITHUB_CLIENT_ID=<client id>,LOG_LEVEL=info" \
  --set-secrets="DATABASE_URL=airship-database-url:latest,CREDENTIALS_KEY=airship-credentials-key:latest,GITHUB_CLIENT_SECRET=airship-github-client-secret:latest,GITHUB_WEBHOOK_SECRET=airship-github-webhook-secret:latest,GITHUB_PRIVATE_KEY=airship-github-private-key:latest"

gcloud run deploy airship-dash \
  --image=$REGISTRY/dash:$VERSION --region=$REGION \
  --port=3000 --cpu=1 --memory=512Mi --min-instances=1 \
  --ingress=internal-and-cloud-load-balancing --allow-unauthenticated
```

- `--allow-unauthenticated` lets the load balancer, GitHub and browsers
  reach the services. Airship does its own authentication (sessions,
  webhook signatures, API tokens).
- `--ingress=internal-and-cloud-load-balancing` closes the `*.run.app`
  URLs, so the only way in is the load balancer. The `run.app` URLs would
  not work anyway: each is its own site, so the session cookie is never
  sent between them.
- The dashboard can scale to zero (`--min-instances=0`) if a cold start on
  the first visit is acceptable. The API cannot.

---

## Step 7 — Load balancer and domains

```bash
for svc in api dash; do
  gcloud compute network-endpoint-groups create airship-$svc-neg --region=$REGION \
    --network-endpoint-type=serverless --cloud-run-service=airship-$svc
  gcloud compute backend-services create airship-$svc --global \
    --load-balancing-scheme=EXTERNAL_MANAGED
  gcloud compute backend-services add-backend airship-$svc --global \
    --network-endpoint-group=airship-$svc-neg --network-endpoint-group-region=$REGION
done

gcloud compute url-maps create airship --default-service=airship-dash
gcloud compute url-maps add-path-matcher airship --path-matcher-name=api \
  --default-service=airship-api --new-hosts=$API_DOMAIN

gcloud compute ssl-certificates create airship --global --domains=$API_DOMAIN,$DASH_DOMAIN
gcloud compute target-https-proxies create airship --url-map=airship --ssl-certificates=airship
gcloud compute addresses create airship --global --ip-version=IPV4
gcloud compute forwarding-rules create airship-https --global \
  --load-balancing-scheme=EXTERNAL_MANAGED --address=airship \
  --target-https-proxy=airship --ports=443

gcloud compute addresses describe airship --global --format='value(address)'
```

Create an **A record** for both `$API_DOMAIN` and `$DASH_DOMAIN` pointing at
that address. The managed certificate is issued once both names resolve to
it, which can take up to an hour. Watch it with:

```bash
gcloud compute ssl-certificates describe airship --global --format='value(managed.status, managed.domainStatus)'
```

To redirect plain HTTP to HTTPS, add a second forwarding rule on port 80 to
a target HTTP proxy whose URL map only redirects:

```bash
cat > redirect.yaml <<'EOF'
name: airship-redirect
defaultUrlRedirect:
  httpsRedirect: true
  redirectResponseCode: MOVED_PERMANENTLY_DEFAULT
EOF
gcloud compute url-maps import airship-redirect --global --source=redirect.yaml
gcloud compute target-http-proxies create airship-redirect --url-map=airship-redirect
gcloud compute forwarding-rules create airship-http --global \
  --load-balancing-scheme=EXTERNAL_MANAGED --address=airship \
  --target-http-proxy=airship-redirect --ports=80
```

**Why not Cloud Run domain mappings?** They are simpler, but they are a
preview feature that is only available in some regions. The load balancer
is the supported path. It also gives you one IP, Cloud Armor if you want
it, and closed `run.app` URLs.

---

## Step 8 — Verify

```bash
curl -s https://$API_DOMAIN/healthz
# {"status":"ok","database":"ok","version":"<VERSION>"}
```

Then work through the **Verification** items in the
[production checklist](README.md#production-checklist): webhook deliveries
in the GitHub App, GitHub sign-in on `https://$DASH_DOMAIN`, and one real
job.

If a revision does not become ready, `gcloud run services describe
airship-api --region=$REGION` shows why, and the logs show the API's
`load config:` message naming the variable when configuration is wrong.

---

## Releasing

```bash
export VERSION=$(git rev-parse --short HEAD)
# build and push both images (step 4), then:
gcloud run jobs update airship-migrate --region=$REGION --image=$REGISTRY/api:$VERSION
gcloud run jobs execute airship-migrate --region=$REGION --wait
gcloud run deploy airship-api --region=$REGION --image=$REGISTRY/api:$VERSION
gcloud run deploy airship-dash --region=$REGION --image=$REGISTRY/dash:$VERSION
```

`gcloud run deploy` with only `--image` keeps every other setting from the
previous revision. Cloud Run shifts traffic once the new revision passes its
startup probe. To roll back, send traffic to the previous revision:

```bash
gcloud run revisions list --service=airship-api --region=$REGION
gcloud run services update-traffic airship-api --region=$REGION --to-revisions=<revision>=100
```

---

## Hardening and operations

| Item | How on Google Cloud |
| --- | --- |
| Backups | Automated daily backups (above) plus point-in-time recovery, 7 days by default. Raise with `--retained-backups-count` and `--retained-transaction-log-days` |
| Credentials key backup | Secret Manager keeps old versions; keep an offline copy as well |
| Rotating a secret | `gcloud secrets versions add <name> --data-file=-`, then redeploy `airship-api`. Services read `:latest` when an instance starts, not continuously |
| Alerts | Cloud Monitoring on the load balancer's 5xx rate for the `airship-api` backend, Cloud Run instance count for `airship-api` below 1, Cloud SQL CPU and disk, and a log-based metric on `jsonPayload.level="ERROR"` |
| Log search | Logs Explorer: `resource.type="cloud_run_revision" resource.labels.service_name="airship-api" jsonPayload.level="ERROR"` |
| Static egress IP | Cloud Run egress comes from shared Google addresses. If a GitHub Enterprise Server or a customer allow-lists by IP, add Direct VPC egress (`--network`, `--subnet`, `--vpc-egress=all-traffic`) and a Cloud NAT with a reserved address |
| Cloud Armor | Optional. Attach a policy to `airship-api`, but do not block GitHub's webhooks or rate-limit the paths runner machines call (`/v1/runner/cache-grant`, `/v1/assets/cacheproxy/*`, `/api/runner-provision`) too tightly |
| Upgrading Postgres | Minor versions apply in the maintenance window (`--maintenance-window-day`, `--maintenance-window-hour`); major versions upgrade in place with `gcloud sql instances patch --database-version` |

Day-to-day operation, alerts worth having and the incident runbook are in
[architecture/operations.md](../architecture/operations.md).

---

## Tearing it down

```bash
gcloud compute forwarding-rules delete airship-https airship-http --global
gcloud run services delete airship-api --region=$REGION
gcloud run services delete airship-dash --region=$REGION
gcloud run jobs delete airship-migrate --region=$REGION
gcloud sql instances patch airship --no-deletion-protection
gcloud sql instances delete airship
```

Runner machines already launched keep running until their job ends, and
every one powers itself off at `RUNNER_MAX_LIFETIME` at the latest, with or
without the control plane.
