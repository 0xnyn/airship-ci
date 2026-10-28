# Airship

Self-hosted GitHub Actions runners on your own AWS account, configured as
code. Connect your cloud once, activate a repository, commit an
`airship.yaml`, and write `runs-on: airship-<label>--${{ github.run_id }}`.
Every job gets a fresh EC2 instance that is destroyed when the job ends.

```yaml
# airship.yaml
runners:
  - name: build
    cloud: aws
    instance_type: [c7i.xlarge, m7i.xlarge]   # tried in order
    machine_image: ami-0123456789abcdef0
    region: us-east-1
    preemptible: [true, false]
    labels: [airship-build]
    extra_config:
      cache: true                              # S3-backed actions/cache
```

```yaml
# .github/workflows/ci.yml
jobs:
  test:
    runs-on: airship-build--${{ github.run_id }}
```

What is here:

- `airship.yaml` per repository: fallback lists for instance type, image,
  region and spot; `extra_config` for EBS volumes (`BlockDeviceMappings`),
  `env_vars`, `cache`, `provision_via: pull_from_api`, Windows
  (`run_as: interactive`, `runner_user`, `runner_path`); `gpu`.
- One-line runners: `airship-aws-c7g-xlarge-ubuntu2404-spot--${{ github.run_id }}`.
- `--suffix` labels for unique runners per job.
- Idle runners: `replicas`, `schedule` and `usage_based`, per repository
  or organization-wide.
- Organization runners in a `.airship` repository (`airship.global.yaml`) with
  `access.yaml` policies.
- Developer API compatible with [cirun-py](https://github.com/AktechLabs/cirun-py):
  `repo`, `cloud-connect`, `access-control`, `runner-provision`.
- Caching: `extra_config.cache: true` runs a proxy on the machine that serves
  `actions/cache`, `setup-*` and `docker/build-push-action` (`url_v2`) from an
  S3 bucket in your account.
- Dashboard: Repositories, Cloud, Runners, Jobs, API keys, Teams, Analytics,
  Events, Settings.

Only AWS is implemented. The cloud provider is an interface
(`apps/api/internal/cloud`) so another can be added. See
[docs/airship-yaml.md](docs/airship-yaml.md) for the file reference,
[docs/caching.md](docs/caching.md) for the cache, and
[docs/architecture.md](docs/architecture.md) for how it works.

Coming from [Cirun](https://docs.cirun.io)? The format is the same under
Airship's names: rename `.cirun.yml` to `airship.yaml`, `cirun-` labels to
`airship-`, and the `.cirun` organization repository to `.airship`
(`.cirun.global.yml` becomes `airship.global.yaml` and `.access.yml`
`access.yaml`). cirun-py works against the developer API unchanged.

## Layout

| Path | What it is |
| --- | --- |
| `apps/api` | Go: the control plane (`cmd/api`), migrations (`cmd/migrate`), the runner-side cache proxy (`cmd/cacheproxy`) |
| `apps/web` | Next.js 16 dashboard |
| `packages/client` | TypeScript client generated from the API's OpenAPI document |
| `packages/ui` | Shared shadcn/ui components |

The Go API is the source of truth for every type on the wire:

```
SQL ──sqlc──► Go structs ──Huma──► openapi.json ──openapi-typescript──► @airship/client ──► Next.js
```

`pnpm generate` rebuilds the chain; `pnpm dev` keeps it live. CI fails if a
generated file is stale.

## Running locally

The full guide, including a development GitHub App, tunnels, real cloud
machines and every test suite, is [docs/development.md](docs/development.md).
The short version:

You need Go 1.27+, Node 24+, pnpm 11 and Docker.

```bash
pnpm install
cp apps/api/.env.example apps/api/.env   # DEV_LOGIN=true and a CREDENTIALS_KEY are set here
pnpm db:up
pnpm db:migrate
pnpm dev
```

Open http://localhost:3000 and choose **Development sign-in**. It signs you in
as an admin of a demo organization, `acme`. API docs are at
http://localhost:8080/docs.

Jobs only arrive once a GitHub App is configured (below). Launches need real
AWS credentials under **Cloud**; the tests use a fake cloud, and the EC2
provider can be exercised against [moto](https://github.com/getmoto/moto),
or against a real account with one small instance that is terminated again:

```bash
uvx --from 'moto[server]' moto_server -p 5055 &
cd apps/api && AIRSHIP_AWS_ENDPOINT=http://localhost:5055 go test -run TestAgainstEmulator ./internal/cloud/aws

cd apps/api && AIRSHIP_LIVE_AWS_ACCESS_KEY_ID=AKIA... AIRSHIP_LIVE_AWS_SECRET_ACCESS_KEY=... \
  AIRSHIP_LIVE_AWS_REGION=eu-west-1 go test -v -run TestAgainstLiveAWS ./internal/cloud/aws
```

## Creating the GitHub App

Create an app at **GitHub → Settings → Developer settings → GitHub Apps**:

- **Callback URL**: `{PUBLIC_URL}/v1/auth/github/callback`
- **Webhook URL**: `{PUBLIC_URL}/v1/github/webhook`, with a random secret.
  For local development, expose port 8080 with a tunnel (e.g. `cloudflared
  tunnel --url localhost:8080`) and use that as `PUBLIC_URL`.
- **Repository permissions**: Administration read & write (to register and
  remove repository runners), Actions read (to look up jobs and runs),
  Contents read (to read `airship.yaml`; write if the access-control API
  should open pull requests on `.airship`), Pull requests write (same),
  Metadata read.
- **Organization permissions**: Members read (to tell admins from members,
  and for team rules in `access.yaml`), Self-hosted runners read & write
  (for idle runners defined in `airship.global.yaml`, which register on the
  organization in runner groups).
- **Subscribe to events**: Workflow job, Push (to refresh idle runners when
  `airship.yaml` changes), Repository (renames and visibility changes).

Then set `GITHUB_APP_SLUG`, `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`,
`GITHUB_WEBHOOK_SECRET` and `GITHUB_PRIVATE_KEY_FILE` (or
`GITHUB_PRIVATE_KEY`) in `apps/api/.env`.

## Connecting AWS

Under **Cloud**, paste an IAM user's access key. The user needs
`AmazonEC2FullAccess`; for `extra_config.cache` it also needs S3 bucket
management on `airship-cache-*` and `sts:GetFederationToken` (the exact
policy is in [docs/caching.md](docs/caching.md)). Credentials are verified
with STS, sealed with `CREDENTIALS_KEY` and never shown again. Runners
launch in the region a definition names, or the default region you pick
(`eu-west-1` unless you change it), into the region's default VPC with an
egress-only security group Airship creates.

## Scripts

| Command | Does |
| --- | --- |
| `pnpm dev` | API with hot reload, web app, live type generation |
| `pnpm generate` | sqlc → openapi.json → TypeScript client |
| `pnpm lint` / `pnpm format` | Ultracite (Biome) for TypeScript, golangci-lint for Go |
| `pnpm typecheck` / `pnpm test` / `pnpm build` | Across all packages |
| `pnpm check` | Everything CI runs |
| `pnpm db:up` / `db:down` | Start or stop local Postgres |
| `pnpm db:migrate` / `db:rollback` | Apply or undo migrations |

Go tests that use the database need `TEST_DATABASE_URL` (each test creates its
own database); without it they are skipped locally and fail in CI:

```bash
TEST_DATABASE_URL='postgres://airship:airship@localhost:5432/airship?sslmode=disable' pnpm test
```

## Deploying

Step-by-step guides for [AWS](docs/deploy/aws.md) (ECS Fargate and RDS),
[Google Cloud](docs/deploy/gcp.md) (Cloud Run and Cloud SQL),
[Azure](docs/deploy/azure.md) (Container Apps and PostgreSQL flexible
server) and [a single server](docs/deploy/server.md) (Docker Compose and
Caddy), with the production checklist they share, are in
[docs/deploy/](docs/deploy/README.md). Postgres 18 or newer is required.
In short:

The control plane is one container plus Postgres:

```bash
docker build -t airship-api apps/api
docker run --rm --entrypoint migrate -e DATABASE_URL=... airship-api up   # once per release
docker run -p 8080:8080 -e DATABASE_URL=... -e PUBLIC_URL=https://... -e CREDENTIALS_KEY=... airship-api
```

In production `PUBLIC_URL` must be https (runner machines fetch the cache
proxy and its credentials from it), `CREDENTIALS_KEY` must be a base64
32-byte key you keep (`openssl rand -base64 32`; losing it loses every
connected cloud), and the GitHub App must be configured. Several API
replicas can run at once: launchers claim runners with row locks, and the
reconciler elects a leader through a Postgres advisory lock. Serve the
dashboard (`apps/web`, `NEXT_PUBLIC_API_URL` set to the API) on the same site
as the API, e.g. `app.example.com` and `api.example.com`, so the session
cookie is sent. `NEXT_PUBLIC_API_URL` is inlined when `next build` runs, so
set it in the build environment; a production build with the `localhost`
default fails on purpose. To rotate `CREDENTIALS_KEY`, list the new key
first and the old one after a comma, deploy, reconnect each cloud, then
drop the old key.

The image embeds the cache proxy for `linux/amd64` and `linux/arm64`; a
`go build` of the API alone leaves it out, and cache-enabled runners then
run their jobs without acceleration.
