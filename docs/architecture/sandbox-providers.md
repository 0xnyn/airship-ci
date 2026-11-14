# Sandbox providers: assessment and design

**What this is.** Which sandbox providers Airship can launch runners on
besides Daytona, why each was brought over in the order it was, how they
work, and what it would take to add the rest. The shipped set is Daytona,
Runloop, E2B, Novita, Lelantos, Namespace, Tenki and Modal; the user-facing
pages are `apps/docs/sandboxes/`. The list comes from the
providers ComputeSDK tags as *benchmarked* (`docs/providers/*.md` in
`../reference/compute-sdk-ts`, the harness behind ComputeSDK Benchmarks).
ComputeSDK's own *Actions* product runs workflow YAML under `act` inside
these sandboxes; Airship's model is different (a real `actions/runner`
registered with a single-use JIT config, one sandbox per job), so a
provider fits Airship if it can do what [daytona.md](daytona.md) needs.

**Status — read this first.** Runloop and E2B (the first pass), Novita and
Lelantos (phase 1) and Namespace, Tenki and Modal (phase 2) are implemented
end to end. For Runloop and E2B:
provider, `airship.yaml` fields, connect endpoints (dashboard and developer
API), dashboard forms, migration. Each provider runs on every `make test`
against a fake of its API (`runloop/runlooptest`; `e2b/e2btest`, which also
fakes each sandbox's daemon, envd, and its Connect framing), and the fleet
and HTTP tests drive them through the whole control plane. **Neither has
run against the real service yet**; the request shapes come from E2B's
published OpenAPI spec and envd `.proto` (`spec/openapi.yml`,
`spec/envd/` in e2b-dev/E2B) and from Runloop's API reference and Python
client (`runloop_api_client/resources/devboxes`). The first live run should
check the items listed under each provider's "unverified" below.

---

## What a provider must do

A runner is one ephemeral sandbox per job. For that, a provider needs:

1. **An API Go can call** without a vendor runtime: REST/JSON, or a
   protocol small enough to speak by hand.
2. **Create with a lifetime of hours.** Airship's maximum runner lifetime
   is 8 hours; a provider capped at under one hour cannot run CI.
3. **Labels or metadata**, so `List` can be scoped to an installation and
   `Terminate` can refuse a sandbox Airship did not make.
4. **Start a long-lived process** (the boot script) that survives the API
   call that started it, as root or with sudo.
5. **Delete by ID**, and ideally a way for the sandbox to end itself when
   the runner exits. The second is not required: the control plane deletes
   a runner's machine when its job completes, and the sandbox's own TTL is
   the backstop.
6. **Outbound internet** (GitHub, the runner download) and preferably
   Docker inside the sandbox, which many workflows use.

## Assessment of the benchmarked providers

Scores are ease of bringing over on 1-10 from reading each provider's
ComputeSDK adapter, docs and, for the chosen two, the vendor's own spec.

| Provider | Score | Fit | Main blocker |
| --- | --- | --- | --- |
| Daytona | — | Shipped earlier | — |
| **Runloop** | 8 | REST; name + metadata; CPU/memory/disk; blueprints from Dockerfiles; keep-alive up to 48 h; **entrypoint exit shuts the devbox down** | List cannot filter by metadata (filtered client-side, live statuses only) |
| **E2B** | 7 | REST control plane with metadata filter and an `Idempotency-Key` on create; Firecracker VMs, Docker works; 24 h on Pro | Exec is envd's Connect-RPC on a per-sandbox host; size comes from a template; 1 h cap on Hobby |
| Superserve | 7 | REST, name + metadata, delete by ID | Max lifetime undocumented; size only via templates; `timeout` only auto-pauses |
| Archil | 6 | Any OCI image, vCPU/memory, `max_ttl_seconds` | Exec is WebSocket-only; no labels; delete needs stop first |
| Northflank | 6 | REST; the container command can be the boot script | A service restarts its container on exit; no Docker; no labels |
| CreateOS | 5 | VM shapes, egress allow-list | No published wire spec; catalog rootfs only; no labels |
| Blaxel | 5 | REST, labels, TTL | Idle standby can suspend a runner making only outbound calls; images need its sandbox-api entrypoint; no Docker |
| Tensorlake | 5 | REST, CPU/memory/disk | No labels; images pre-registered; lifetime and exec wire format unknown |
| HopX | 5 | REST control plane, root | In-VM agent protocol not visible; no labels |
| Modal | 5 | Labels, 24 h, mature | gRPC only (needs Modal's Go SDK); gVisor, no Docker |
| Lightning | 4 | Native detached exec, spot | Curated runtimes only; no labels; geo-blocked API |
| Vercel | 4 | REST, sudo | 45 min (about 5 h on Pro) lifetime cap; no custom image; no labels |
| Beam | 3 | Docker supported | Undocumented gRPC-behind-HTTP gateway; no list |
| Isorun | 3 | Any image, CPU/memory/TTL | Opaque protocol, 120 s per exec, no labels |
| Cloudflare | 3 | — | Customer must deploy a Worker; images fixed at deploy; no list; inactivity timeout; no Docker |
| Cloud Run | 2 | — | Customer must deploy a gateway; sandboxes pinned to one instance; idle reaper kills long jobs |
| CodeSandbox | 2 | Tags | Commands run over a custom WebSocket terminal protocol; templates from the dashboard only |
| Upstash Box | 2 | — | No image, labels, process API; undocumented HTTP API |

**Next candidates.** Superserve once its lifetime ceiling is confirmed
(the shape is the closest to Runloop's); Modal through its Go SDK for
teams that want its GPU capacity and can live without Docker; Archil with
a small WebSocket exec client. The rest wait on a vendor change (labels,
a longer lifetime, a public API).

## How the two are built

Both follow the Daytona provider ([daytona.md](daytona.md)): the control
plane creates the sandbox, writes the boot script into it and starts it;
the script is `bootscript.Sandbox`, the Daytona script with the "how the
sandbox ends" step made per-cloud. Neither cloud has object storage the
cache proxy speaks or regions: `cache: true` launches without the proxy
and records `cache.unavailable`, and every credential and runner is in the
one region `global`. Both are x64 only, Linux only, with no GPUs or spot.

### Runloop (`internal/cloud/runloop`)

- **Create.** `POST /v1/devboxes` with `name` (the runner name),
  `metadata` (`airship-installation`, `airship-runner-id`,
  `airship-repository`), `blueprint_name` or `blueprint_id` (from
  `machine_image`), and `launch_parameters`: a named
  `resource_size_request` or `CUSTOM_SIZE` with `custom_cpu_cores`,
  `custom_gb_memory`, `custom_disk_size`; `keep_alive_time_seconds` set
  to the runner's lifetime plus 15 minutes (48 h at most);
  `user_parameters` root.
- **Boot, and how it ends.** The devbox's `entrypoint` is a waiter with
  no secret in it: it waits up to 10 minutes for the boot script and a
  ready marker under `/tmp`, moves the script aside and `exec`s it. The
  control plane writes both with `write_file_contents` once the devbox is
  `running`. The script's exit is the entrypoint's, and Runloop shuts a
  devbox down when its entrypoint exits (`entrypoint_exit`), so this is
  EC2's shutdown-terminates; a devbox whose script never arrives shuts
  itself down when the waiter gives up. The JIT config is never in the
  entrypoint, because the devbox record can be read back from the API.
- **Idempotency.** Names are not unique and the list has no metadata
  filter, so every launch lists the live statuses (`queued` …
  `resuming`; never `shutdown` or `failure`, which would grow with
  history) and matches `airship-runner-id`. A create whose answer is lost
  is settled the same way, or reported ambiguous for the inventory scan.
- **List / Terminate.** List is the same per-status walk, paged with
  `starting_after`; `suspending` and `suspended` devboxes are reported
  stopped (a suspended devbox lost its processes). Terminate reads the
  devbox, refuses one without the installation's metadata, and
  `POST /v1/devboxes/{id}/shutdown`.
- **Verify.** `GET /v1/devboxes?limit=1`. Runloop names no account for a
  key, so the identity is the masked key.
- **Unverified until a live run:** that the entrypoint runs as the
  `user_parameters` user and before `running` is reported; that
  `write_file_contents` takes an absolute path; that `shutdown` of an
  ending devbox answers 4xx rather than 200.

### E2B (`internal/cloud/e2b`)

- **Create.** `POST /v2/sandboxes` with `templateID` (from
  `instance_type`), `timeout` (the runner's lifetime plus 15 minutes, 24 h
  at most), optional `cpuCount`, `autoPause: false` and the same metadata,
  and an `Idempotency-Key` header set to the runner ID, so a retried create
  replays the first sandbox. Every launch first looks the runner up with
  `GET /v2/sandboxes?metadata=airship-runner-id%3D<id>`, for when the key
  has expired.
- **Boot.** Through the sandbox's daemon at
  `https://49983-<id>.<domain>`, with the sandbox's `envdAccessToken` and
  Basic auth `root:` (envd's way of naming the user): `/health` until it
  answers; `process.Process/List` (Connect unary, JSON) to see whether a
  process tagged `airship-runner` already runs; `POST /files` to upload the
  script as root; `process.Process/Start` (Connect server stream, JSON
  envelopes framed by hand) with the tag, reading only the first event
  before hanging up. envd keeps a process after its client has gone.
- **How it ends.** Nothing inside an E2B sandbox can end it, so the
  script's stop step only logs. The control plane deletes the sandbox
  when the job completes, as it does on every cloud, and the inventory
  scan deletes one whose runner finished; the timeout is the backstop.
  `autoPause` is off, so a timed-out sandbox is gone, not paused.
- **List / Terminate.** `GET /v2/sandboxes?metadata=airship-installation%3D<n>&state=running,paused`,
  paged by `X-Next-Token`; paused sandboxes are reported stopped. Terminate
  reads `GET /sandboxes/{id}`, refuses another installation's, and
  `DELETE /sandboxes/{id}`.
- **Errors.** `sandbox_capacity_*` and `Should-Retry: true` are retryable;
  5xx, transport errors and `idempotency_outcome_unknown` /
  `idempotency_in_progress` are ambiguous (looked up first, then left to
  the scan).
- **Plan limits.** A Hobby team's sandboxes live one hour at most; E2B
  refuses a longer timeout, so a Hobby key fails every launch with a
  permanent error naming the limit. Lower `RUNNER_MAX_LIFETIME` or use Pro.
- **Unverified until a live run:** that a `Start` stream closed after its
  first event leaves the process running (E2B's SDKs rely on this for
  background commands); the `Authorization: Basic` user selection; the
  exact answer to a timeout over the plan's limit.

## Phase 1: Novita and Lelantos, as E2B flavours

Both speak E2B's protocol (Novita's SDK is a fork of E2B's; Lelantos wraps
E2B's own SDK), so `internal/cloud/e2b` serves them through a `Flavor`:
the cloud name, the hosted domain (`us-phx-1.sandbox.novita.ai`,
`lelantos.ai`), the key format (`lel_`/`e2b_` on Lelantos), the create path
and its quirks. Both create through the first API, `POST /sandboxes`, with
`secure: true` for an envd token; neither is sent E2B's `Idempotency-Key`,
so a repeated launch relies on the metadata lookup alone. Novita caps a
timeout at the plan's session length (1, 3 or 4 hours) unless the sandbox
carries `long_running: "true"` metadata, which Airship always sets (read
from the `novita-sandbox` SDK and Novita's long-running guide). Everything
else (envd, list by metadata, kill) is E2B's path.

**Unverified live:** that Lelantos serves `/v2/sandboxes` for the list (the
E2B SDK it wraps does), and Novita's ceiling on a long-running timeout.

## Phase 2: Namespace, Tenki and Modal

### Namespace (`internal/cloud/namespace`)

Connect-RPC unary calls are JSON POSTs, so the provider uses
`cloud/restapi` with `Connect-Protocol-Version: 1`; the field names come
from `namespace.cloud.compute.v1beta` as published in
`namespacelabs.dev/integrations`. `CreateInstance` sets the shape, labels
`airship-installation` and `airship-runner-id`, a `deadline` (lifetime plus
15 minutes; the workspace's policy may shorten it) and one container,
`runner`, whose entrypoint is `bootscript.Waiter` and whose workload type is
JOB, so the instance terminates when the container exits. `docker: true`
sets `docker_sock_path` to mount the VM's own dockerd. The script is written
with `CommandService.RunCommandSync` at the instance's
`commandServiceEndpoint`, in the command's environment. `ListInstances`
filters on the server by label (`labelFilter`, op EQUAL), paged by cursor.
Regions are the regional endpoints, `us` and `eu`.

**Unverified live:** whether the runner user can use the mounted Docker
socket without a group change; the exact create response field names (from
ComputeSDK's adapter, which reads `metadata.instanceId`).

### Tenki (`internal/cloud/tenki`)

Tenki's official Go SDK (`go.luxor.ai/sandbox`, gRPC/Connect plus a
per-sandbox data plane) sits behind a narrow `API` interface, faked by
`tenkitest` in tests. Sandboxes carry tags `airship-installation-<id>` and
`airship-runner-<runner ID>` (Tenki filters lists by tag) and the same values
as metadata; create waits until the sandbox runs; one exec writes the script
from its environment and starts it detached (`bootscript.StartDetachedCommand`,
which never rewrites a script already there). Nothing inside a Tenki sandbox
ends it, so the control plane closes it when the job completes and the max
duration is the backstop.

### Modal (`internal/cloud/modal`)

Modal's official Go SDK (`github.com/modal-labs/modal-client/go`, beta)
behind the same kind of interface, faked by `modaltest`. Every Sandbox lives
in the App `airship-runners`; its name, the runner's, is unique among the
App's running Sandboxes, so a repeated launch finds it with `FromName`.
Tags mark it; `Sandboxes.List` filters by tag. Its command is the waiter, so
it ends with the runner; the script is written by an exec. `docker: true`
selects the VM runtime, which Modal recommends for Docker; `gpu:` takes
Modal's GPU names. Timeout at most 24 hours.

**Unverified live:** that `Sandboxes.List` returns running Sandboxes only;
Docker on the VM runtime with Airship's static dockerd.

## Configuration

```yaml
runners:
  - name: devbox
    cloud: runloop
    instance_type: 4vcpu-16gb      # or x-small, small, medium, large, x-large, xx-large
    machine_image: ci-ubuntu       # a blueprint name or bpt_ ID; empty for Runloop's default
    labels: [airship-devbox]
    extra_config:
      docker: true
      disk: 32                      # GB, even, 2-64; shapes only
  - name: micro
    cloud: e2b
    instance_type: ci-runner:4vcpu  # a template, optionally with more vCPUs
    labels: [airship-micro]
    extra_config:
      docker: true
```

```yaml
  - {name: nov, cloud: novita, instance_type: "base:4vcpu", labels: [airship-nov]}
  - {name: eu, cloud: lelantos, instance_type: base, labels: [airship-eu]}
  - {name: ns, cloud: namespace, instance_type: 8vcpu-16gb, region: eu, labels: [airship-ns], extra_config: {docker: true}}
  - {name: vm, cloud: tenki, instance_type: 4vcpu-8gb, labels: [airship-vm], extra_config: {disk: 40}}
  - {name: gpu, cloud: modal, instance_type: 8vcpu-32gb, gpu: H100, labels: [airship-gpu]}
```

The full rules are in [../airship-yaml.md](../airship-yaml.md); the
user-facing pages, one per cloud, are in `apps/docs/sandboxes/`.
