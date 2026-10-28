# Deploying Airship on AWS

**What this is.** Airship's control plane on ECS Fargate, behind an
Application Load Balancer, with RDS for PostgreSQL and Secrets Manager,
from an empty account to a green job. Each step has the AWS CLI commands.
The same shape translates directly to Terraform, CDK or CloudFormation.

**Audience.** Whoever owns the AWS account that will host Airship. That can
be a different account from the ones runners launch in; it should be a
different account from your other production workloads.

**Time.** About an hour, most of it waiting for RDS and certificates.

Read [README.md](README.md) first: its "Before you start" section (domain,
credentials key, GitHub App) applies here unchanged.

---

## The result

```
                       Route 53: api.example.com, app.example.com
                                       │
 public subnets   ┌────────────────────▼────────────────────┐   NAT gateway ──► internet
 (2 AZs)          │  ALB  :443 (ACM certificate), :80 → 443 │       ▲
                  └───────┬───────────────────────┬─────────┘       │
                          │ host api.*            │ host app.*      │ outbound: GitHub,
 private subnets  ┌───────▼────────┐      ┌───────▼────────┐        │ customers' clouds
 (2 AZs)          │ ECS service    │      │ ECS service    │────────┘
                  │ airship-api ×2 │      │ airship-web ×2 │
                  └───────┬────────┘      └────────────────┘
                          │ 5432, TLS
                  ┌───────▼────────┐
                  │ RDS Postgres 18│  Multi-AZ, encrypted, 7-day PITR
                  └────────────────┘
       Secrets Manager: airship/* ──► injected as env vars by the task execution role
```

The tasks have **no task role**. Airship never uses the identity it runs
under to launch runners; it uses the credentials customers connect. The
only IAM in this guide is the execution role, which lets ECS pull images,
write logs and read the `airship/*` secrets.

---

## Step 0 — Set the variables

Every command below uses these. Run them in one shell.

```bash
export AWS_REGION=eu-west-1
export ACCOUNT=$(aws sts get-caller-identity --query Account --output text)
export API_DOMAIN=api.example.com
export WEB_DOMAIN=app.example.com
export VERSION=$(git rev-parse --short HEAD)
export REGISTRY=$ACCOUNT.dkr.ecr.$AWS_REGION.amazonaws.com
```

---

## Step 1 — The network

Create a VPC with public and private subnets in two Availability Zones and
a NAT gateway. The quickest way is the console: **VPC → Create VPC → VPC and
more**, with 2 AZs, 2 public subnets, 2 private subnets, and **NAT gateways:
In 1 AZ** (or 1 per AZ, which survives losing an AZ at twice the NAT cost).
Use an existing VPC instead if it already has this shape.

The API needs the NAT gateway: it calls `api.github.com` and every
customer's cloud API from the private subnets.

Record the IDs:

```bash
export VPC=vpc-...
export PUBLIC_SUBNETS="subnet-aaa subnet-bbb"
export PRIVATE_SUBNETS="subnet-ccc subnet-ddd"
```

Three security groups. The load balancer takes the internet, the tasks take
only the load balancer, and the database takes only the tasks:

```bash
ALB_SG=$(aws ec2 create-security-group --group-name airship-alb \
  --description "Airship load balancer" --vpc-id $VPC --query GroupId --output text)
APP_SG=$(aws ec2 create-security-group --group-name airship-app \
  --description "Airship API and dashboard tasks" --vpc-id $VPC --query GroupId --output text)
DB_SG=$(aws ec2 create-security-group --group-name airship-db \
  --description "Airship Postgres" --vpc-id $VPC --query GroupId --output text)

aws ec2 authorize-security-group-ingress --group-id $ALB_SG --protocol tcp --port 443 --cidr 0.0.0.0/0
aws ec2 authorize-security-group-ingress --group-id $ALB_SG --protocol tcp --port 80 --cidr 0.0.0.0/0
aws ec2 authorize-security-group-ingress --group-id $APP_SG --protocol tcp --port 8080 --source-group $ALB_SG
aws ec2 authorize-security-group-ingress --group-id $APP_SG --protocol tcp --port 3000 --source-group $ALB_SG
aws ec2 authorize-security-group-ingress --group-id $DB_SG --protocol tcp --port 5432 --source-group $APP_SG
```

Do not restrict port 443 on the load balancer to GitHub's webhook ranges.
Browsers and runner machines (for the cache) reach the same listener.
Webhooks are authenticated by their signature instead.

---

## Step 2 — The database

> **Using PlanetScale instead?** Skip this step and follow
> [planetscale.md](planetscale.md), which lists what else changes in this
> guide.

```bash
DB_PASSWORD=$(openssl rand -hex 24)

aws rds create-db-subnet-group --db-subnet-group-name airship \
  --db-subnet-group-description "Airship" --subnet-ids $PRIVATE_SUBNETS

aws rds create-db-instance \
  --db-instance-identifier airship \
  --engine postgres --engine-version 18 \
  --db-instance-class db.t4g.small \
  --allocated-storage 20 --storage-type gp3 --storage-encrypted \
  --db-name airship --master-username airship --master-user-password "$DB_PASSWORD" \
  --db-subnet-group-name airship --vpc-security-group-ids $DB_SG \
  --no-publicly-accessible --multi-az \
  --backup-retention-period 7 --deletion-protection \
  --auto-minor-version-upgrade

aws rds wait db-instance-available --db-instance-identifier airship   # 10–20 minutes
DB_HOST=$(aws rds describe-db-instances --db-instance-identifier airship \
  --query 'DBInstances[0].Endpoint.Address' --output text)
```

Use Postgres **18**. Airship also runs on 17, but there is no
reason to start a new database on it. `db.t4g.small` is
plenty for most installations; the database holds rows per job and per
runner, and events are pruned after 30 days. Drop `--multi-az` for a trial
to halve the cost.

RDS enforces TLS on Postgres 15 and newer (`rds.force_ssl=1`), so the
connection string uses `sslmode=require`.

---

## Step 3 — Secrets

Create the five secrets the API reads. Have the GitHub App's values from
[README.md, step 3](README.md#3-the-production-github-app) to hand, and the
private key at `./github-app.pem`.

```bash
secret() { aws secretsmanager create-secret --name "$1" --secret-string "$2" --query ARN --output text; }

DATABASE_URL_ARN=$(secret airship/database-url \
  "postgres://airship:$DB_PASSWORD@$DB_HOST:5432/airship?sslmode=require")
CREDENTIALS_KEY_ARN=$(secret airship/credentials-key "$(openssl rand -base64 32)")
GITHUB_CLIENT_SECRET_ARN=$(secret airship/github-client-secret "<client secret>")
GITHUB_WEBHOOK_SECRET_ARN=$(secret airship/github-webhook-secret "<webhook secret>")
GITHUB_PRIVATE_KEY_ARN=$(secret airship/github-private-key "$(cat github-app.pem)")
```

Now copy the credentials key somewhere outside this account:

```bash
aws secretsmanager get-secret-value --secret-id airship/credentials-key \
  --query SecretString --output text
```

The PEM is stored with real newlines; the API accepts that, and also a
single line with literal `\n`.

---

## Step 4 — Images

```bash
aws ecr create-repository --repository-name airship-api --image-scanning-configuration scanOnPush=true
aws ecr create-repository --repository-name airship-web --image-scanning-configuration scanOnPush=true
aws ecr get-login-password | docker login --username AWS --password-stdin $REGISTRY

docker build --platform linux/amd64 --build-arg VERSION=$VERSION \
  -t $REGISTRY/airship-api:$VERSION apps/api
docker build --platform linux/amd64 -f apps/web/Dockerfile \
  --build-arg NEXT_PUBLIC_API_URL=https://$API_DOMAIN \
  -t $REGISTRY/airship-web:$VERSION .
docker push $REGISTRY/airship-api:$VERSION
docker push $REGISTRY/airship-web:$VERSION
```

To run on Graviton instead, which is cheaper on Fargate, build with
`--platform linux/arm64` and set `"runtimePlatform": {"cpuArchitecture":
"ARM64", "operatingSystemFamily": "LINUX"}` in the task definitions below.

---

## Step 5 — The execution role

```bash
aws iam create-role --role-name airship-ecs-execution --assume-role-policy-document '{
  "Version": "2012-10-17",
  "Statement": [{"Effect": "Allow", "Principal": {"Service": "ecs-tasks.amazonaws.com"}, "Action": "sts:AssumeRole"}]
}'
aws iam attach-role-policy --role-name airship-ecs-execution \
  --policy-arn arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy
aws iam put-role-policy --role-name airship-ecs-execution --policy-name airship-secrets --policy-document "{
  \"Version\": \"2012-10-17\",
  \"Statement\": [{
    \"Effect\": \"Allow\",
    \"Action\": \"secretsmanager:GetSecretValue\",
    \"Resource\": \"arn:aws:secretsmanager:$AWS_REGION:$ACCOUNT:secret:airship/*\"
  }]
}"
export EXECUTION_ROLE=arn:aws:iam::$ACCOUNT:role/airship-ecs-execution
```

---

## Step 6 — Certificate and load balancer

Request one certificate for both names and validate it through DNS:

```bash
CERT=$(aws acm request-certificate --domain-name $API_DOMAIN \
  --subject-alternative-names $WEB_DOMAIN --validation-method DNS \
  --query CertificateArn --output text)
aws acm describe-certificate --certificate-arn $CERT \
  --query 'Certificate.DomainValidationOptions[].ResourceRecord'
# Create those CNAME records (Route 53 console: "Create records in Route 53"), then:
aws acm wait certificate-validated --certificate-arn $CERT
```

The load balancer, a target group per service, and host-based routing:

```bash
ALB=$(aws elbv2 create-load-balancer --name airship --type application \
  --subnets $PUBLIC_SUBNETS --security-groups $ALB_SG \
  --query 'LoadBalancers[0].LoadBalancerArn' --output text)

API_TG=$(aws elbv2 create-target-group --name airship-api --protocol HTTP --port 8080 \
  --vpc-id $VPC --target-type ip --health-check-path /healthz \
  --query 'TargetGroups[0].TargetGroupArn' --output text)
WEB_TG=$(aws elbv2 create-target-group --name airship-web --protocol HTTP --port 3000 \
  --vpc-id $VPC --target-type ip --health-check-path /sign-in \
  --query 'TargetGroups[0].TargetGroupArn' --output text)
for tg in $API_TG $WEB_TG; do
  aws elbv2 modify-target-group-attributes --target-group-arn $tg \
    --attributes Key=deregistration_delay.timeout_seconds,Value=30
done

HTTPS=$(aws elbv2 create-listener --load-balancer-arn $ALB --protocol HTTPS --port 443 \
  --certificates CertificateArn=$CERT --ssl-policy ELBSecurityPolicy-TLS13-1-2-2021-06 \
  --default-actions 'Type=fixed-response,FixedResponseConfig={StatusCode=404}' \
  --query 'Listeners[0].ListenerArn' --output text)
aws elbv2 create-rule --listener-arn $HTTPS --priority 10 \
  --conditions Field=host-header,Values=$API_DOMAIN --actions Type=forward,TargetGroupArn=$API_TG
aws elbv2 create-rule --listener-arn $HTTPS --priority 20 \
  --conditions Field=host-header,Values=$WEB_DOMAIN --actions Type=forward,TargetGroupArn=$WEB_TG
aws elbv2 create-listener --load-balancer-arn $ALB --protocol HTTP --port 80 \
  --default-actions 'Type=redirect,RedirectConfig={Protocol=HTTPS,Port=443,StatusCode=HTTP_301}'
```

Point both names at it. In Route 53, create an **A record, Alias** to the
load balancer for each of `$API_DOMAIN` and `$WEB_DOMAIN`. With another DNS
provider, create CNAMEs to:

```bash
aws elbv2 describe-load-balancers --load-balancer-arns $ALB --query 'LoadBalancers[0].DNSName' --output text
```

The ALB's 60-second idle timeout is fine. No request to the API takes
longer.

---

## Step 7 — Task definitions

```bash
aws ecs create-cluster --cluster-name airship
aws logs create-log-group --log-group-name /ecs/airship
aws logs put-retention-policy --log-group-name /ecs/airship --retention-in-days 30
```

The API. `stopTimeout` gives the 30-second drain room to finish:

```bash
cat > api-task.json <<EOF
{
  "family": "airship-api",
  "requiresCompatibilities": ["FARGATE"],
  "networkMode": "awsvpc",
  "cpu": "512",
  "memory": "1024",
  "executionRoleArn": "$EXECUTION_ROLE",
  "containerDefinitions": [{
    "name": "api",
    "image": "$REGISTRY/airship-api:$VERSION",
    "essential": true,
    "portMappings": [{"containerPort": 8080}],
    "stopTimeout": 45,
    "environment": [
      {"name": "PUBLIC_URL", "value": "https://$API_DOMAIN"},
      {"name": "WEB_URL", "value": "https://$WEB_DOMAIN"},
      {"name": "CORS_ORIGINS", "value": "https://$WEB_DOMAIN"},
      {"name": "GITHUB_APP_SLUG", "value": "<app slug>"},
      {"name": "GITHUB_CLIENT_ID", "value": "<client id>"},
      {"name": "LOG_LEVEL", "value": "info"}
    ],
    "secrets": [
      {"name": "DATABASE_URL", "valueFrom": "$DATABASE_URL_ARN"},
      {"name": "CREDENTIALS_KEY", "valueFrom": "$CREDENTIALS_KEY_ARN"},
      {"name": "GITHUB_CLIENT_SECRET", "valueFrom": "$GITHUB_CLIENT_SECRET_ARN"},
      {"name": "GITHUB_WEBHOOK_SECRET", "valueFrom": "$GITHUB_WEBHOOK_SECRET_ARN"},
      {"name": "GITHUB_PRIVATE_KEY", "valueFrom": "$GITHUB_PRIVATE_KEY_ARN"}
    ],
    "logConfiguration": {
      "logDriver": "awslogs",
      "options": {"awslogs-group": "/ecs/airship", "awslogs-region": "$AWS_REGION", "awslogs-stream-prefix": "api"}
    }
  }]
}
EOF
aws ecs register-task-definition --cli-input-json file://api-task.json
```

The migration task: the same image with the `migrate` entrypoint and only
the database secret:

```bash
cat > migrate-task.json <<EOF
{
  "family": "airship-migrate",
  "requiresCompatibilities": ["FARGATE"],
  "networkMode": "awsvpc",
  "cpu": "256",
  "memory": "512",
  "executionRoleArn": "$EXECUTION_ROLE",
  "containerDefinitions": [{
    "name": "migrate",
    "image": "$REGISTRY/airship-api:$VERSION",
    "essential": true,
    "entryPoint": ["/usr/local/bin/migrate"],
    "command": ["up"],
    "secrets": [{"name": "DATABASE_URL", "valueFrom": "$DATABASE_URL_ARN"}],
    "logConfiguration": {
      "logDriver": "awslogs",
      "options": {"awslogs-group": "/ecs/airship", "awslogs-region": "$AWS_REGION", "awslogs-stream-prefix": "migrate"}
    }
  }]
}
EOF
aws ecs register-task-definition --cli-input-json file://migrate-task.json
```

The dashboard needs no secrets; its one setting is baked into the image:

```bash
cat > web-task.json <<EOF
{
  "family": "airship-web",
  "requiresCompatibilities": ["FARGATE"],
  "networkMode": "awsvpc",
  "cpu": "256",
  "memory": "512",
  "executionRoleArn": "$EXECUTION_ROLE",
  "containerDefinitions": [{
    "name": "web",
    "image": "$REGISTRY/airship-web:$VERSION",
    "essential": true,
    "portMappings": [{"containerPort": 3000}],
    "logConfiguration": {
      "logDriver": "awslogs",
      "options": {"awslogs-group": "/ecs/airship", "awslogs-region": "$AWS_REGION", "awslogs-stream-prefix": "web"}
    }
  }]
}
EOF
aws ecs register-task-definition --cli-input-json file://web-task.json
```

These files contain secret ARNs but no secret values. They are safe to
keep, and worth turning into your infrastructure code.

---

## Step 8 — Migrate, then start the services

```bash
NETWORK="awsvpcConfiguration={subnets=[${PRIVATE_SUBNETS// /,}],securityGroups=[$APP_SG],assignPublicIp=DISABLED}"

migrate() {
  local task
  task=$(aws ecs run-task --cluster airship --launch-type FARGATE \
    --task-definition airship-migrate --network-configuration "$NETWORK" \
    --query 'tasks[0].taskArn' --output text)
  aws ecs wait tasks-stopped --cluster airship --tasks $task
  aws ecs describe-tasks --cluster airship --tasks $task \
    --query 'tasks[0].containers[0].exitCode' --output text   # 0 means migrated
}
migrate
```

The logs are in CloudWatch under `/ecs/airship`, stream prefix `migrate`.
They end with `goose: successfully migrated database to version: N`.

```bash
DEPLOY='deploymentCircuitBreaker={enable=true,rollback=true},minimumHealthyPercent=100,maximumPercent=200'

aws ecs create-service --cluster airship --service-name airship-api \
  --task-definition airship-api --desired-count 2 --launch-type FARGATE \
  --network-configuration "$NETWORK" \
  --load-balancers targetGroupArn=$API_TG,containerName=api,containerPort=8080 \
  --health-check-grace-period-seconds 30 --deployment-configuration "$DEPLOY"

aws ecs create-service --cluster airship --service-name airship-web \
  --task-definition airship-web --desired-count 2 --launch-type FARGATE \
  --network-configuration "$NETWORK" \
  --load-balancers targetGroupArn=$WEB_TG,containerName=web,containerPort=3000 \
  --health-check-grace-period-seconds 30 --deployment-configuration "$DEPLOY"

aws ecs wait services-stable --cluster airship --services airship-api airship-web
```

Two API tasks run the launcher on both and elect one reconciler through a
Postgres advisory lock. Do not attach target-tracking autoscaling that can
scale the API to zero; the background loops need a running task.

---

## Step 9 — Verify

```bash
curl -s https://$API_DOMAIN/healthz
# {"status":"ok","database":"ok","version":"<VERSION>"}
```

Then work through the **Verification** items in the
[production checklist](README.md#production-checklist): webhook deliveries
in the GitHub App, GitHub sign-in on `https://$WEB_DOMAIN`, and one real
job.

If a task will not start, the reason is in the service's events
(`aws ecs describe-services --cluster airship --services airship-api
--query 'services[0].events[:5]'`) or in the task's logs. The API exits
with a `load config:` message naming the variable when configuration is
wrong.

---

## Releasing

```bash
export VERSION=$(git rev-parse --short HEAD)
# build and push both images (step 4), then:
sed -i.bak "s#airship-api:[^\"]*#airship-api:$VERSION#" api-task.json migrate-task.json
sed -i.bak "s#airship-web:[^\"]*#airship-web:$VERSION#" web-task.json
for f in api migrate web; do aws ecs register-task-definition --cli-input-json file://$f-task.json >/dev/null; done

migrate   # from step 8; must print 0 before continuing
aws ecs update-service --cluster airship --service airship-api --task-definition airship-api
aws ecs update-service --cluster airship --service airship-web --task-definition airship-web
aws ecs wait services-stable --cluster airship --services airship-api airship-web
```

The circuit breaker rolls a service back to the previous task definition if
new tasks keep failing their health checks. Rolling back by hand is
`update-service` with the previous revision, `--task-definition
airship-api:<n>`.

---

## Hardening and operations

| Item | How on AWS |
| --- | --- |
| Backups | RDS automated backups (7 days above, up to 35) with point-in-time restore. Copy snapshots to a second region with `aws rds start-db-instance-automated-backups-replication` for regional disasters |
| Credentials key backup | Secrets Manager plus an offline copy. Consider replicating the secret to a second region (`aws secretsmanager replicate-secret-to-regions`) |
| Alarms | CloudWatch alarms on the `airship-api` target group's `UnHealthyHostCount > 0` and `HTTPCode_Target_5XX_Count`, on RDS `FreeStorageSpace` and `CPUUtilization`, and a metric filter on `/ecs/airship` for `"level":"ERROR"` |
| Log search | CloudWatch Logs Insights, e.g. `fields @timestamp, msg, err \| filter level = "ERROR" \| sort @timestamp desc` |
| WAF | Optional. If you add one, allow `POST /v1/github/webhook` bodies up to GitHub's 25 MB, and do not rate-limit the paths runner machines call (`/v1/runner/cache-grant`, `/v1/assets/cacheproxy/*`, `/api/runner-provision`) so tightly that a burst of runners starting at once is refused |
| Private ECR access | Add VPC endpoints for ECR, S3, Logs and Secrets Manager to keep image pulls off the NAT gateway. The API still needs the NAT gateway for GitHub and cloud APIs |
| Upgrading Postgres | Minor versions apply in the maintenance window. For a major version, use RDS Blue/Green deployments |
| Cost | Roughly: the NAT gateway and the ALB are fixed monthly costs, four small Fargate tasks, and a small Multi-AZ RDS instance. The runners themselves are billed to whichever accounts customers connect |

Day-to-day operation, alerts worth having and the incident runbook are in
[architecture/operations.md](../architecture/operations.md).

---

## Tearing it down

```bash
aws ecs update-service --cluster airship --service airship-api --desired-count 0
aws ecs update-service --cluster airship --service airship-web --desired-count 0
aws ecs delete-service --cluster airship --service airship-api --force
aws ecs delete-service --cluster airship --service airship-web --force
aws ecs delete-cluster --cluster airship
aws elbv2 delete-load-balancer --load-balancer-arn $ALB
aws rds modify-db-instance --db-instance-identifier airship --no-deletion-protection --apply-immediately
aws rds delete-db-instance --db-instance-identifier airship --final-db-snapshot-identifier airship-final
```

Runner machines already launched keep running until their job ends, and
every one powers itself off at `RUNNER_MAX_LIFETIME` at the latest, with or
without the control plane.
