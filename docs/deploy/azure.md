# Deploying Airship on Azure

**What this is.** Airship's control plane on Azure Container Apps, with
Azure Database for PostgreSQL flexible server on a private network, Key
Vault for secrets, and managed certificates for the two custom domains. It
goes from an empty subscription to a green job, with the `az` commands for
each step.

**Audience.** Whoever owns the Azure subscription that will host Airship.
That can be a different subscription from the ones runners launch in.

**Time.** About an hour, most of it waiting for the database and the
Container Apps environment.

Read [README.md](README.md) first: its "Before you start" section (domain,
credentials key, GitHub App) applies here unchanged.

---

## The result

```
              DNS: api.example.com, app.example.com (CNAME + asuid TXT)
                                      │
 ┌──── VNet 10.20.0.0/16 ─────────────▼───────────────────────────────────────┐
 │  subnet apps (delegated to Container Apps)                                 │
 │   ┌──────────────── Container Apps environment ───────────────────────┐    │
 │   │  airship-api  ×2, ingress :8080, managed cert ──► GitHub, clouds  │    │
 │   │  airship-dash ×1+, ingress :3000, managed cert                    │    │
 │   │  airship-migrate (manual job)                                     │    │
 │   └──────────────────────────────┬────────────────────────────────────┘    │
 │  subnet db (delegated)           │ 5432, TLS, private DNS                  │
 │   ┌──────────────────────────────▼───────┐                                 │
 │   │ PostgreSQL flexible server 18        │  7-day PITR                     │
 │   └──────────────────────────────────────┘                                 │
 └────────────────────────────────────────────────────────────────────────────┘
   Key Vault ──► secrets, read with a user-assigned managed identity
   Container Registry ──► images, pulled with the same identity
```

The managed identity has two roles: pull images and read secrets. Airship
never uses the identity it runs under to launch runners; it uses the
credentials customers connect.

**Name the resource group carefully.** When a customer connects Azure,
Airship puts their runner VMs in a resource group called `airship-runners`
in their subscription. If you also run runners in this subscription, keep
the control plane in a different group (`airship-control-plane` below), so
the two never mix.

---

## Step 0 — Set the variables

Container Registry, Key Vault and PostgreSQL names are global, so they get a
suffix.

```bash
export LOCATION=westeurope
export RG=airship-control-plane
export SUFFIX=$(openssl rand -hex 3)
export ACR=airship$SUFFIX           # letters and digits only
export VAULT=airship-kv-$SUFFIX
export DB=airship-db-$SUFFIX
export API_DOMAIN=api.example.com
export DASH_DOMAIN=app.example.com
export VERSION=$(git rev-parse --short HEAD)

az extension add --name containerapp --upgrade
az provider register --namespace Microsoft.App
az provider register --namespace Microsoft.OperationalInsights
az provider register --namespace Microsoft.DBforPostgreSQL
az group create --name $RG --location $LOCATION
```

---

## Step 1 — The network

```bash
az network vnet create -g $RG -n airship-vnet --address-prefixes 10.20.0.0/16
az network vnet subnet create -g $RG --vnet-name airship-vnet -n apps \
  --address-prefixes 10.20.0.0/23 --delegations Microsoft.App/environments
az network vnet subnet create -g $RG --vnet-name airship-vnet -n db \
  --address-prefixes 10.20.2.0/24 --delegations Microsoft.DBforPostgreSQL/flexibleServers
export APPS_SUBNET=$(az network vnet subnet show -g $RG --vnet-name airship-vnet -n apps --query id -o tsv)
```

The database gets no public endpoint at all. Only the Container Apps
environment, in the same VNet, can reach it.

---

## Step 2 — The database

> **Using PlanetScale instead?** Skip this step and follow
> [planetscale.md](planetscale.md), which lists what else changes in this
> guide.

```bash
DB_PASSWORD=$(openssl rand -hex 24)

az postgres flexible-server create -g $RG -n $DB -l $LOCATION \
  --version 18 --tier Burstable --sku-name Standard_B2s \
  --storage-size 32 --backup-retention 7 \
  --admin-user airship --admin-password "$DB_PASSWORD" \
  --vnet airship-vnet --subnet db \
  --database-name airship --yes          # 5–15 minutes

export DB_HOST=$(az postgres flexible-server show -g $RG -n $DB --query fullyQualifiedDomainName -o tsv)
```

Use Postgres **18**. Airship also runs on 17, but there is no
reason to start a new database on it. With `--vnet` and
`--subnet`, the CLI also creates a private DNS zone linked to the VNet, so
`$DB_HOST` resolves to a private address from inside it.

The flexible server requires TLS by default (`require_secure_transport`),
so the connection string uses `sslmode=require`. For zone-redundant high
availability, use the General Purpose tier
(`--tier GeneralPurpose --sku-name Standard_D2ds_v5 --high-availability ZoneRedundant`);
Burstable does not offer it.

---

## Step 3 — Identity, registry and Key Vault

```bash
az identity create -g $RG -n airship-api
export IDENTITY=$(az identity show -g $RG -n airship-api --query id -o tsv)
PRINCIPAL=$(az identity show -g $RG -n airship-api --query principalId -o tsv)

az acr create -g $RG -n $ACR --sku Basic
az role assignment create --assignee-object-id $PRINCIPAL --assignee-principal-type ServicePrincipal \
  --role AcrPull --scope $(az acr show -n $ACR --query id -o tsv)

az keyvault create -g $RG -n $VAULT -l $LOCATION --enable-rbac-authorization true
VAULT_ID=$(az keyvault show -n $VAULT --query id -o tsv)
az role assignment create --assignee-object-id $PRINCIPAL --assignee-principal-type ServicePrincipal \
  --role "Key Vault Secrets User" --scope $VAULT_ID
# you, to write the secrets:
az role assignment create --assignee $(az ad signed-in-user show --query id -o tsv) \
  --role "Key Vault Secrets Officer" --scope $VAULT_ID
```

Role assignments can take a few minutes to take effect. If the next step
returns `Forbidden`, wait and retry.

Have the GitHub App's values from
[README.md, step 3](README.md#3-the-production-github-app) to hand, and the
private key at `./github-app.pem`:

```bash
az keyvault secret set --vault-name $VAULT -n database-url \
  --value "postgres://airship:$DB_PASSWORD@$DB_HOST:5432/airship?sslmode=require" -o none
az keyvault secret set --vault-name $VAULT -n credentials-key --value "$(openssl rand -base64 32)" -o none
az keyvault secret set --vault-name $VAULT -n github-client-secret --value "<client secret>" -o none
az keyvault secret set --vault-name $VAULT -n github-webhook-secret --value "<webhook secret>" -o none
az keyvault secret set --vault-name $VAULT -n github-private-key --file github-app.pem -o none
```

Copy the credentials key somewhere outside this subscription:

```bash
az keyvault secret show --vault-name $VAULT -n credentials-key --query value -o tsv
```

---

## Step 4 — Images

```bash
az acr login -n $ACR
export REGISTRY=$ACR.azurecr.io

docker build --platform linux/amd64 --build-arg VERSION=$VERSION \
  -t $REGISTRY/airship-api:$VERSION apps/api
docker build --platform linux/amd64 -f apps/dash/Dockerfile \
  --build-arg NEXT_PUBLIC_API_URL=https://$API_DOMAIN \
  -t $REGISTRY/airship-dash:$VERSION .
docker push $REGISTRY/airship-api:$VERSION
docker push $REGISTRY/airship-dash:$VERSION
```

Without Docker locally, `az acr build` builds in the registry instead and
takes the same `--build-arg` flags.

---

## Step 5 — The Container Apps environment

```bash
az containerapp env create -g $RG -n airship -l $LOCATION \
  --infrastructure-subnet-resource-id $APPS_SUBNET
```

This also creates a Log Analytics workspace for the apps' logs. Outbound
internet access, which the API needs for GitHub and customers' clouds, works
without further setup.

Key Vault references used below take this shape. Container Apps resolves
them with the identity when a replica starts:

```bash
kv() { echo "$1=keyvaultref:https://$VAULT.vault.azure.net/secrets/$1,identityref:$IDENTITY"; }
```

---

## Step 6 — Migrate

```bash
az containerapp job create -g $RG -n airship-migrate --environment airship \
  --trigger-type Manual --replica-timeout 600 --replica-retry-limit 0 \
  --parallelism 1 --replica-completion-count 1 \
  --image $REGISTRY/airship-api:$VERSION \
  --registry-server $REGISTRY --registry-identity $IDENTITY --mi-user-assigned $IDENTITY \
  --command /usr/local/bin/migrate --args up \
  --secrets "$(kv database-url)" \
  --env-vars DATABASE_URL=secretref:database-url

migrate() {
  local run status
  run=$(az containerapp job start -g $RG -n airship-migrate --query name -o tsv)
  while :; do
    status=$(az containerapp job execution show -g $RG -n airship-migrate \
      --job-execution-name $run --query properties.status -o tsv)
    case $status in Succeeded) echo migrated; return 0 ;; Failed|Stopped) echo "migration $status"; return 1 ;; esac
    sleep 5
  done
}
migrate
```

The logs are in the Log Analytics workspace (`ContainerAppConsoleLogs_CL`,
filtered on the job's name). They end with `goose: successfully migrated
database to version: N`.

---

## Step 7 — The apps

```bash
az containerapp create -g $RG -n airship-api --environment airship \
  --image $REGISTRY/airship-api:$VERSION \
  --registry-server $REGISTRY --registry-identity $IDENTITY --user-assigned $IDENTITY \
  --ingress external --target-port 8080 \
  --cpu 0.5 --memory 1Gi --min-replicas 2 --max-replicas 4 \
  --secrets "$(kv database-url)" "$(kv credentials-key)" "$(kv github-client-secret)" \
            "$(kv github-webhook-secret)" "$(kv github-private-key)" \
  --env-vars \
    PUBLIC_URL=https://$API_DOMAIN DASH_URL=https://$DASH_DOMAIN CORS_ORIGINS=https://$DASH_DOMAIN \
    GITHUB_APP_SLUG=<app slug> GITHUB_CLIENT_ID=<client id> LOG_LEVEL=info \
    DATABASE_URL=secretref:database-url CREDENTIALS_KEY=secretref:credentials-key \
    GITHUB_CLIENT_SECRET=secretref:github-client-secret \
    GITHUB_WEBHOOK_SECRET=secretref:github-webhook-secret \
    GITHUB_PRIVATE_KEY=secretref:github-private-key

az containerapp create -g $RG -n airship-dash --environment airship \
  --image $REGISTRY/airship-dash:$VERSION \
  --registry-server $REGISTRY --registry-identity $IDENTITY --user-assigned $IDENTITY \
  --ingress external --target-port 3000 \
  --cpu 0.5 --memory 1Gi --min-replicas 1 --max-replicas 2
```

`--min-replicas 2` on the API is what keeps the launcher and reconciler
running. Never set it to 0. Container Apps does not throttle CPU between
requests, so nothing else is needed.

Add an HTTP health probe on `/healthz`, so a replica whose database
connection is broken stops receiving traffic:

```bash
az containerapp show -g $RG -n airship-api -o yaml > api.yaml
# under properties.template.containers[0], add:
#   probes:
#     - type: Readiness
#       httpGet: {path: /healthz, port: 8080}
#       periodSeconds: 10
#     - type: Liveness
#       httpGet: {path: /healthz, port: 8080}
#       periodSeconds: 30
#       failureThreshold: 3
az containerapp update -g $RG -n airship-api --yaml api.yaml
```

---

## Step 8 — Domains and certificates

Each hostname needs a CNAME to the app's default hostname, and a TXT record
proving the domain is yours:

```bash
az containerapp show -g $RG -n airship-api --query properties.configuration.ingress.fqdn -o tsv
az containerapp show -g $RG -n airship-dash --query properties.configuration.ingress.fqdn -o tsv
az containerapp show -g $RG -n airship-api --query properties.customDomainVerificationId -o tsv
```

| Record | Type | Value |
| --- | --- | --- |
| `api.example.com` | CNAME | the `airship-api` FQDN |
| `asuid.api.example.com` | TXT | the verification ID |
| `app.example.com` | CNAME | the `airship-dash` FQDN |
| `asuid.app.example.com` | TXT | the verification ID |

Once they resolve, add the hostnames and bind free managed certificates:

```bash
az containerapp hostname add -g $RG -n airship-api --hostname $API_DOMAIN
az containerapp hostname bind -g $RG -n airship-api --hostname $API_DOMAIN \
  --environment airship --validation-method CNAME
az containerapp hostname add -g $RG -n airship-dash --hostname $DASH_DOMAIN
az containerapp hostname bind -g $RG -n airship-dash --hostname $DASH_DOMAIN \
  --environment airship --validation-method CNAME
```

Issuing a certificate can take several minutes. Container Apps redirects
HTTP to HTTPS by default. The `*.azurecontainerapps.io` names cannot stand
in for the custom domains, because the session cookie would not be sent
between them.

---

## Step 9 — Verify

```bash
curl -s https://$API_DOMAIN/healthz
# {"status":"ok","database":"ok","version":"<VERSION>"}
```

Then work through the **Verification** items in the
[production checklist](README.md#production-checklist): webhook deliveries
in the GitHub App, GitHub sign-in on `https://$DASH_DOMAIN`, and one real
job.

If a revision does not start, `az containerapp revision list -g $RG -n
airship-api -o table` shows its state, and `az containerapp logs show -g $RG
-n airship-api --tail 50` shows the API's `load config:` message naming the
variable when configuration is wrong. A replica stuck on a Key Vault
reference usually means the identity's role assignment has not taken
effect yet.

---

## Releasing

```bash
export VERSION=$(git rev-parse --short HEAD)
# build and push both images (step 4), then:
az containerapp job update -g $RG -n airship-migrate --image $REGISTRY/airship-api:$VERSION
migrate   # from step 6; must print "migrated" before continuing
az containerapp update -g $RG -n airship-api --image $REGISTRY/airship-api:$VERSION
az containerapp update -g $RG -n airship-dash --image $REGISTRY/airship-dash:$VERSION
```

In the default single-revision mode, the new revision takes all traffic
once its replicas are ready, and the old one is deactivated. To roll back,
update the image back to the previous tag.

---

## Hardening and operations

| Item | How on Azure |
| --- | --- |
| Backups | Automated with point-in-time restore, 7 days above, up to 35 (`--backup-retention`). Add `--geo-redundant-backup Enabled` at creation for regional disasters |
| Credentials key backup | Key Vault soft-delete and purge protection (`az keyvault update -n $VAULT --enable-purge-protection true`), plus an offline copy |
| Rotating a secret | `az keyvault secret set` with a new value, then restart the API's revision (`az containerapp revision restart`). Replicas read secrets when they start |
| Alerts | Azure Monitor on the API's `Replicas` metric below 1 and its 5xx `Requests`, the database's CPU and storage, and a log alert on `ContainerAppConsoleLogs_CL` for `"level":"ERROR"` |
| Static egress IP | Attach a NAT gateway with a reserved public IP to the `apps` subnet, if a GitHub Enterprise Server or a customer allow-lists by IP |
| Drain time | Container Apps waits 30 seconds after `SIGTERM` by default, the same as the API's drain. Raise `terminationGracePeriodSeconds` in the YAML to give it margin |
| Upgrading Postgres | Minor versions apply in the maintenance window; major versions upgrade in place (`az postgres flexible-server upgrade --version`) |

Day-to-day operation, alerts worth having and the incident runbook are in
[architecture/operations.md](../architecture/operations.md).

---

## Tearing it down

```bash
az group delete --name $RG
```

That removes everything in this guide, including the database (Key Vault
stays soft-deleted for its retention period). Runner machines already
launched keep running until their job ends, and every one powers itself off
at `RUNNER_MAX_LIFETIME` at the latest, with or without the control plane.
