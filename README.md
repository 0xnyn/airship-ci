# Airship

Managed GitHub Actions runners that run in your own cloud account.

Point a workflow at a profile (`runs-on: airship-4cpu`) and Airship starts a
fresh machine for the job in your AWS account (or on your Docker host),
registers it with GitHub for exactly one job, and destroys it afterwards. We
run the control plane: webhooks, scheduling, the GitHub integration and the
dashboard. You run the data plane: a small agent that only makes outbound
HTTPS calls. Your code, secrets and build output never leave your account.

See [docs/architecture.md](docs/architecture.md) for how it works, the design
decisions and the known limitations.

## Layout

| Path | What it is |
| --- | --- |
| `apps/api` | Go: the control plane (`cmd/api`), the data plane agent (`cmd/agent`), migrations (`cmd/migrate`) |
| `apps/web` | Next.js 16 dashboard |
| `packages/client` | TypeScript client generated from the API's OpenAPI document |
| `packages/ui` | Shared shadcn/ui components |
| `deploy/aws` | CloudFormation template that runs the agent in a customer's AWS account |

The Go API is the source of truth for every type on the wire:

```
SQL ──sqlc──► Go structs ──Huma──► openapi.json ──openapi-typescript──► @airship/client ──► Next.js
```

`pnpm generate` rebuilds the chain; `pnpm dev` keeps it live. CI fails if a
generated file is stale.

## Running locally

You need Go 1.27+, Node 24+, pnpm 11 and Docker.

```bash
pnpm install
cp apps/api/.env.example apps/api/.env   # DEV_LOGIN=true is set here
pnpm db:up
pnpm db:migrate
pnpm dev
```

Open http://localhost:3000 and choose **Development sign-in**. It signs you in
as an admin of a demo organization, `acme`. From there you can create a data
plane, connect an agent and create profiles. API docs are at
http://localhost:8080/docs.

To connect a local agent that runs jobs as Docker containers:

```bash
cd apps/api
AIRSHIP_URL=http://localhost:8080 AIRSHIP_TOKEN=asd_... AIRSHIP_PROVIDER=docker \
  AIRSHIP_DOCKER_SOCKET=$HOME/.docker/run/docker.sock go run ./cmd/agent
```

Jobs only arrive once a GitHub App is configured (below); without one,
everything except GitHub sign-in, webhooks and launches works.

## Creating the GitHub App

Create an app at **GitHub → Settings → Developer settings → GitHub Apps**:

- **Callback URL**: `{PUBLIC_URL}/v1/auth/github/callback`
- **Webhook URL**: `{PUBLIC_URL}/v1/github/webhook`, with a random secret.
  For local development, expose port 8080 with a tunnel (e.g. `cloudflared
  tunnel --url localhost:8080`) and use that as `PUBLIC_URL`.
- **Repository permissions**: Administration read & write (to create and
  remove repository runners), Actions read (to look up jobs), Metadata read.
- **Organization permissions**: Members read (to tell admins from members).
- **Subscribe to events**: Workflow job.

Then set `GITHUB_APP_SLUG`, `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`,
`GITHUB_WEBHOOK_SECRET` and `GITHUB_PRIVATE_KEY_FILE` (or
`GITHUB_PRIVATE_KEY`) in `apps/api/.env`.

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

The control plane is one container plus Postgres:

```bash
docker build -t airship-api apps/api
docker run --rm --entrypoint migrate -e DATABASE_URL=... airship-api up   # once per release
docker run -p 8080:8080 -e DATABASE_URL=... -e PUBLIC_URL=https://... airship-api
```

In production `PUBLIC_URL` must be https and the GitHub App must be
configured. Several API replicas can run at once: the reconciler elects a
leader through a Postgres advisory lock. Serve the dashboard (`apps/web`,
`NEXT_PUBLIC_API_URL` set to the API) on the same site as the API, e.g.
`app.example.com` and `api.example.com`, so the session cookie is sent.

The agent image is the `agent` target of the same Dockerfile
(`docker build --target agent`). CI publishes both images to GHCR from
`main` and print each image's digest in the run summary. Customers deploy
the agent with `deploy/aws/data-plane.yaml`; the dashboard shows the exact
command when a data plane is created. Pin `AgentImage` by digest in
production. Runners may only use AMIs owned by Canonical or the customer's
account unless `ImageOwners` says otherwise.

Without an AWS account, CI checks the AWS side against emulators:

```bash
# cfn-lint, deploy the template into moto, simulate the agent's IAM policy
pnpm --filter @airship/deploy-aws test:policy
# the EC2 provider's SDK calls against a moto server
uvx --from 'moto[server]' moto_server -p 5055 &
cd apps/api && AIRSHIP_AWS_ENDPOINT=http://localhost:5055 go test -run TestAgainstEmulator ./internal/agent/aws
```

Both need [uv](https://docs.astral.sh/uv/).
