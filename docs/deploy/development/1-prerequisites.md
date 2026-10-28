# Step 1 — Prerequisites

**What this is.** The four tools Airship needs on your machine, how to
install them on macOS, Linux and Windows, and how to check you have them.

**Time.** 10 minutes, or none if you already develop in Go and Node.

---

## The four tools

| Tool | Version | What needs it |
| --- | --- | --- |
| **Go** | 1.27+ | The API, the migrations, the tests |
| **Node** | 24+ | The dashboard, the marketing site, the build system |
| **pnpm** | 11.22 | The package manager. npm and yarn will not work: this is a pnpm workspace |
| **Docker** | any recent | Postgres. Nothing else needs it in development |

Go's version is a floor, not a ceiling: `apps/api/go.mod` names
`toolchain go1.27.3`, and an older Go downloads that toolchain itself as
long as `GOTOOLCHAIN=auto`, which is the default. Node's floor is in
`.node-version` (24) and `engines` in `package.json`.

### macOS

```bash
brew install go node docker
corepack enable            # gives you the pnpm version package.json pins
```

Docker Desktop, [OrbStack](https://orbstack.dev) and
[Colima](https://github.com/abiosoft/colima) all work; OrbStack is the
lightest. Start it before `make setup`.

### Linux

```bash
# Go: use the tarball from go.dev/dl, not the distro package, which is often old.
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.27.3.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin

# Node 24 via nvm, fnm or your distro's nodesource repository.
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash
nvm install 24

corepack enable
# Docker Engine: https://docs.docker.com/engine/install/
```

Add yourself to the `docker` group (`sudo usermod -aG docker $USER`, then
log out and in) so `make db-up` does not need `sudo`.

### Windows

Use **WSL2** with Ubuntu and follow the Linux instructions inside it, with
Docker Desktop's WSL2 backend. The repository works natively on Windows
too, but `make` and the shell snippets in these guides assume a POSIX
shell.

The only thing that genuinely needs native Windows is PowerShell's parser,
for one test of the Windows runner boot script. That test skips itself when
`pwsh` is not on `PATH` ([7-tests.md](7-tests.md)).

---

## Check them

```bash
make doctor
```

```
  go       go version go1.27.3 darwin/arm64
  node     v24.x.x
  pnpm     11.22.0
  docker   Docker version 28.x.x
  optional: uvx (moto, AWS emulator), cloudflared or ngrok (webhook tunnel), supabase (local Postgres 17)
All required tools found.
```

It fails if a tool is missing, if Node is older than 24, or if Docker is
installed but its daemon is not running. Fix what it names and run it
again.

---

## What is *not* installed globally

This matters, because it is the usual source of "it works on my machine".

| | How it is run instead |
| --- | --- |
| **sqlc**, **goose**, **golangci-lint**, **air** | Pinned in `apps/api/tools/go.mod` and run as `go tool -modfile=tools/go.mod <tool>`. The Makefile does this for you |
| **Biome / Ultracite** (formatting and linting) | A dev dependency, run with `pnpm exec` |
| **Postgres** | A Docker container (`make db-up`). You do not need a local Postgres, or even a local `psql` — `make db-psql` goes through the container |
| **The Supabase CLI** | Optional, and `pnpm dlx supabase` is the fallback ([stage-0/local.md](../stage-0/local.md)) |
| **moto** (the AWS emulator) | `uvx`, on demand, only for one opt-in test |

So: never `go install` or `npm i -g` anything for this repository. If a
command seems to be missing, it is almost certainly meant to be run through
`make` or `pnpm`.

---

## Optional, for later steps

| Tool | Needed by | Install |
| --- | --- | --- |
| [`cloudflared`](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/) or [`ngrok`](https://ngrok.com) | [Step 4](4-github.md): a public https URL for webhooks | `brew install cloudflared` |
| [`uv`](https://docs.astral.sh/uv/) | The AWS emulator test | `brew install uv` |
| `aws`, `gcloud`, `az` CLIs | [Step 5](5-real-machines.md), to create a credential | — |
| `psql` | Convenience only | `brew install libpq` |

---

## Checkpoint

- [ ] `make doctor` prints **All required tools found.**

Next: [step 2 — the first run](2-first-run.md).
