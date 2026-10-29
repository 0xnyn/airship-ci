# Setting up Airship on Microsoft Azure — in the portal

**What this is.** The path from an empty Azure subscription to a green CI
job on your own Azure virtual machines, entirely through the Azure portal
and the Airship dashboard. **No terminal is needed from start to finish.**

**Prefer a terminal?** [azure-setup-cli.md](azure-setup-cli.md) is the same
setup as `az` commands. Pick one guide and stay in it — they do not
interleave.

**Audience.** Whoever owns the Azure subscription and its Microsoft Entra
tenant, and whoever owns the GitHub organization — often three different
people in a large company, so each step says which hat it needs. **No prior
Azure experience is assumed.**

**Time.** 30–45 minutes, plus any wait on a quota increase (Step 5), an app
registration (Step 2) or a policy exemption (Step 0). All three can depend
on someone other than you — read ahead and ask early.

**Status.** Read the status note at the top of [azure.md](azure.md) before
you plan around this. The Azure provider is thoroughly tested against a
faithful fake of Resource Manager, and the Linux boot script it sends has
run a real GitHub job, but no request has yet been accepted by Azure itself.

---

## Orientation — if you have never operated Azure

Airship's job is to start a fresh machine for each CI job and delete it when
the job ends. Nothing is deployed into your subscription, there is no agent
and no bootstrap script. You hand Airship four values and it calls Resource
Manager.

| The thing | In one sentence | Why Airship needs it | Step |
| --- | --- | --- | --- |
| **A tenant** | Your Microsoft Entra ID directory — the organization that owns identities. One company, usually one tenant | Airship signs in here before it can call anything | [0](#step-0--check-your-policies-azure-admin) |
| **A subscription** | The billing and isolation boundary inside the tenant. Resources live in exactly one | Machines launch here and you pay for them directly | [below](#getting-a-subscription-if-you-need-one) |
| **Resource providers, registered** | Azure switches each service off per subscription until first use | Without `Microsoft.Compute` and `Microsoft.Network`, every launch fails | [1](#step-1--register-the-resource-providers-azure-admin) |
| **An app registration with a client secret** | A robot identity in the tenant (its in-subscription half is called a *service principal*); the secret is its password | How Airship proves it may create and delete machines | [2](#step-2--register-the-application-entra-admin) |
| **A role assignment on the subscription** | RBAC: who may do what, where. An app registration starts with no access to anything | Creating a VM is not permitted by default | [2](#step-2--register-the-application-entra-admin) / [3](#step-3--least-privilege-optional-azure-admin) |
| **Quotas** | Per-subscription, per-region caps on how much you can run | The ceiling on concurrent runners — almost always the real limit, not Airship | [5](#step-5--check-the-quotas-that-will-bite-azure-admin) |

Vocabulary that appears constantly below:

- A **resource group** is a named folder for resources within a subscription.
  Airship makes one called `airship-runners` on first launch and keeps its
  networks there.
- A **VM size** like `Standard_D8s_v5` names a machine's shape. The number is
  the vCPU count; `s` means premium-storage-capable, `a` is AMD, `p` is Arm.
- A **VNet** is a private network in one region, divided into **subnets**. A
  **network security group** (NSG) is a firewall. Airship creates a
  per-region network and an NSG with **no inbound rules** — runners dial out
  and are never dialled into.

### Four things that catch newcomers out

1. **Tenant ≠ subscription.** The tenant holds identities; the subscription
   holds resources and the bill. You need an ID from each, and they look
   identical (both GUIDs), so label them when you copy them.
2. **Region spelling.** The portal shows *East US*; the API wants `eastus`.
   Airship takes the API spelling, case and all.
3. **The client secret is shown once**, and the portal shows two columns —
   you want **Value**, not **Secret ID**. Copying the wrong one is the single
   most common failure in Step 7.
4. **The secret expires.** Unlike an AWS access key, it has an end date, at
   most two years out. Put it in a calendar; see Step 4.

### What this will cost

You pay Microsoft directly, by the second, for machines that only exist
while a job runs. Airship adds nothing that idles. Approximate
pay-as-you-go prices in `eastus` — the portal's size picker shows a live
estimate, and other regions run 10–30% higher:

| Size | vCPU / RAM | Roughly | A 10-minute job |
| --- | --- | --- | --- |
| `Standard_B2s` | 2 / 4 GiB | $0.04/hr | under a cent |
| `Standard_D4s_v5` | 4 / 16 GiB | $0.19/hr | about 3¢ |
| `Standard_D8s_v5` | 8 / 32 GiB | $0.38/hr | about 6¢ |
| `Standard_D16s_v5` | 16 / 64 GiB | $0.77/hr | about 13¢ |

Disks bill per gigabyte-**month**, so a 128 GiB premium disk that lives
fifteen minutes costs a fraction of a cent. `preemptible: true` (Azure Spot)
takes a large discount in exchange for Azure being allowed to reclaim the
machine. **Windows runners carry a Windows Server licence charge on top of
the compute price** — often close to doubling it on smaller sizes — and boot
minutes slower, so keep Windows definitions separate and sized
deliberately. A new account usually has **$200 of credit** for the first
30 days.

The two numbers that bound your bill are `max_runners` (Airship's ceiling on
concurrent machines) and your vCPU quotas (Azure's). Add a budget alert as a
third, independent net: **Cost Management** → **Budgets** → **Add**, monthly,
at an amount that would surprise you.

---

## Before you start

| You need | Notes |
| --- | --- |
| An Azure subscription, ideally dedicated to CI | Same argument as on AWS: the role assignment becomes uninteresting and the bill becomes legible |
| Permission to register an application in Microsoft Entra ID and create a client secret | Often restricted in larger tenants. See [Step 0](#step-0--check-your-policies-azure-admin) |
| Owner or User Access Administrator on the subscription | To assign the application its role. Contributor is **not** enough — granting access is a separate power from using it, which surprises people |
| Admin on the GitHub organization | To install the app and activate repositories. If that is not you, Steps 6–8 need the person it is |
| A region | `eastus` by default. Spelled as the API spells it, not as the portal shows it |
| A concurrency number | Becomes `max_runners`. Start at 10 |
| A control plane | Airship-hosted, or your own ([operations.md](operations.md)) |

### Getting a subscription, if you need one

**New to Azure entirely:** sign up at
[azure.microsoft.com](https://azure.microsoft.com/free/) with a card and a
phone number. You get a subscription called *Azure subscription 1*; rename
it to something honest like `acme-ci` under **Subscriptions** → the
subscription → **Rename**.

**Already on Azure:** **Subscriptions** → **Add** creates another one
against the same billing account. A dedicated CI subscription is worth it —
runners execute whatever is in your repositories, and the separation makes
both the role assignment and the bill trivial to reason about.

---

## Step 0 — Check your policies *(Azure admin)*

**Azure Policy** is a rule engine that can deny resource creation across a
subscription, set by whoever administers your Azure estate. On a personal
subscription you have none of these and can skip to Step 1. In a company
tenant, read this now rather than discovering it in Step 7. Four things
commonly block this setup:

| Policy | Effect on Airship | What to do |
| --- | --- | --- |
| Users cannot register applications, or client secrets are restricted by an app management policy | **Hard blocker.** Airship authenticates with an application's client secret and nothing else — no certificate, no workload identity federation, no managed identity | Have an Entra admin create the application and secret, or grant an exception for this one application |
| Azure Policy denying public IP addresses on network interfaces | Blocks the default path, which gives each runner a public address | Use `enable_public_ip: false` on every definition. Airship then creates a NAT gateway per region |
| Azure Policy requiring tags, or a naming convention, on resource groups or resources | Airship's shared resources carry only `airship-managed: true`; machines carry the `airship-*` tags in [azure.md](azure.md#what-airship-creates-in-your-subscription). A deny policy fails the launch with the policy's name | Exempt the `airship-runners` resource group, or use a "modify" policy that adds your tags rather than a deny |
| Azure Policy limiting allowed locations or VM sizes | Launches outside the list fail as permanent errors | Make sure the regions and sizes in your `airship.yaml` are on the list |

Search → **Policy** → **Assignments**, scoped to your subscription.
**Compliance** on the same blade shows what is already being enforced. An
empty list means nothing is in your way.

---

## Step 1 — Register the resource providers *(Azure admin)*

A **resource provider** is a service namespace that Azure keeps switched off
per subscription until something uses it. Airship needs two.

**Subscriptions** → your subscription → **Resource providers** → search
`Microsoft.Compute` → **Register**; repeat for `Microsoft.Network`.
Registration takes a minute or two and the status column tells you when it
is done.

A fresh subscription may have neither registered. The SDK registers them on
first use when the application's role allows it (Contributor does), so this
step is required only with the least-privilege role in Step 3, and harmless
otherwise.

---

## Step 2 — Register the application *(Entra admin)*

This is the identity Airship uses. Three things happen together: an **app
registration** (the identity, in the tenant), a **service principal** (its
representation inside the subscription) and a **role assignment** (what it
may do).

1. Search → **Microsoft Entra ID** → **App registrations** → **New
   registration**. Name `airship-runners`; leave the account type as
   *Accounts in this organizational directory only*; no redirect URI
   (Airship never signs a human in through it). **Register**.
2. The **Overview** blade now shows **Application (client) ID** and
   **Directory (tenant) ID** — two of your four values.
3. **Certificates & secrets** → **Client secrets** → **New client secret**.
   Describe it (`airship`), choose an expiry, **Add**. Copy the **Value**
   column immediately — **not** the *Secret ID*, which is a different string
   and will be rejected in Step 7. It is masked as soon as you leave the
   page.
4. Search → **Subscriptions** → your subscription → **Access control
   (IAM)** → **Add** → **Add role assignment** → role **Contributor** →
   **Members** → *User, group, or service principal* → select
   `airship-runners` → **Review + assign**.

Contributor on the subscription is the simple choice, and in a dedicated CI
subscription it is a reasonable end state. Skip to Step 4, or replace it
with the narrower role in Step 3.

**Check it worked:** the subscription's **Access control (IAM)** → **Role
assignments** must list `airship-runners`. An empty result here is the cause
of the most misleading error in this guide: a subscription the application
cannot see reads as *not found*, and Step 7 reports it as bad credentials.

If the role assignment step was refused, you have Contributor but not Owner
or User Access Administrator — see [Before you start](#before-you-start).

> The step notes say *(Entra admin)* and *(Azure admin)* because in a larger
> company these are two different people: one can create the identity, the
> other can grant it access. The handoff between them is the client ID.

---

## Step 3 — Least privilege *(optional, Azure admin)*

Skip this on a dedicated CI subscription; Contributor is fine there. It
earns its keep in a shared subscription.

The role has to be assigned at the **subscription**, not at the resource
group: the inventory scan lists the subscription's VMs (the only list that
carries their power state), the marketplace image lookups are
subscription-level reads, and the `airship-runners` resource group is
created on first use.

In the portal: **Subscriptions** → your subscription → **Access control
(IAM)** → **Add** → **Add custom role** → the **JSON** tab → paste the
document below (replacing the subscription GUID in `AssignableScopes`) →
**Review + create**. Then assign it as in Step 2, and remove the Contributor
assignment.

```json
{
  "Name": "Airship runners",
  "Description": "Launch, list and delete Airship's GitHub Actions runner VMs",
  "AssignableScopes": ["/subscriptions/00000000-0000-0000-0000-000000000000"],
  "Actions": [
    "Microsoft.Resources/subscriptions/read",
    "Microsoft.Resources/subscriptions/locations/read",
    "Microsoft.Resources/subscriptions/resourceGroups/read",
    "Microsoft.Resources/subscriptions/resourceGroups/write",

    "Microsoft.Compute/virtualMachines/read",
    "Microsoft.Compute/virtualMachines/write",
    "Microsoft.Compute/virtualMachines/delete",
    "Microsoft.Compute/virtualMachines/instanceView/read",
    "Microsoft.Compute/virtualMachines/runCommands/read",
    "Microsoft.Compute/virtualMachines/runCommands/write",
    "Microsoft.Compute/disks/read",
    "Microsoft.Compute/disks/write",
    "Microsoft.Compute/disks/delete",
    "Microsoft.Compute/locations/operations/read",
    "Microsoft.Compute/locations/publishers/artifacttypes/offers/skus/versions/read",
    "Microsoft.Compute/images/read",
    "Microsoft.Compute/galleries/read",
    "Microsoft.Compute/galleries/images/read",
    "Microsoft.Compute/galleries/images/versions/read",

    "Microsoft.Network/virtualNetworks/read",
    "Microsoft.Network/virtualNetworks/write",
    "Microsoft.Network/virtualNetworks/subnets/read",
    "Microsoft.Network/virtualNetworks/subnets/write",
    "Microsoft.Network/virtualNetworks/subnets/join/action",
    "Microsoft.Network/networkSecurityGroups/read",
    "Microsoft.Network/networkSecurityGroups/write",
    "Microsoft.Network/networkSecurityGroups/join/action",
    "Microsoft.Network/networkInterfaces/read",
    "Microsoft.Network/networkInterfaces/write",
    "Microsoft.Network/networkInterfaces/delete",
    "Microsoft.Network/networkInterfaces/join/action",
    "Microsoft.Network/publicIPAddresses/read",
    "Microsoft.Network/publicIPAddresses/write",
    "Microsoft.Network/publicIPAddresses/delete",
    "Microsoft.Network/publicIPAddresses/join/action",
    "Microsoft.Network/natGateways/*",
    "Microsoft.Network/locations/operations/read",
    "Microsoft.Network/locations/operationResults/read",

    "Microsoft.MarketplaceOrdering/offertypes/publishers/offers/plans/agreements/read"
  ],
  "NotActions": [],
  "DataActions": [],
  "NotDataActions": []
}
```

The map from those actions to what Airship does:

| Actions | Needed for |
| --- | --- |
| `subscriptions/read`, `subscriptions/locations/read` | The credential check at connect time, and again after a failed launch or list |
| `resourceGroups/read`, `/write` | Creating `airship-runners` on first launch |
| `virtualMachines/*`, `virtualMachines/instanceView/read`, `disks/*` | The launch, idempotency lookup, inventory list (with power states), teardown; the OS disk is created and deleted with the VM |
| `virtualMachines/runCommands/*` | Booting Windows and specialized images |
| `locations/publishers/.../versions/read`, `images/read`, `galleries/...` | Resolving `machine_image` |
| `virtualNetworks/*`, `subnets/*`, `networkSecurityGroups/*` | The per-region network, on first launch in a region |
| `networkInterfaces/*`, `publicIPAddresses/*` | Each machine's interface and address, and the sweep of orphans |
| `natGateways/*` | `enable_public_ip: false`. A wildcard because Microsoft's published operation list names only `natGateways/join/action`, not the read and write actions the gateway's creation needs |
| `locations/operations/read`, `operationResults/read` | Polling long-running operations |
| `MarketplaceOrdering/.../agreements/read` | Launching third-party marketplace images that carry a purchase plan |

Every action name above except the NAT gateway wildcard was checked
verbatim against Microsoft's published operation lists (the Compute,
Networking, Management and governance, and General pages of the Azure RBAC
permissions reference) on 2026-10-28, and each maps to a call in the
provider's code. The role as a whole has not yet been exercised against
Azure, so it may still be missing an action a call needs. If a launch
fails with `AuthorizationFailed`, Azure's message names the missing action,
and the Events page shows that message: add the action and relaunch. A
gallery or managed image in another resource group of the subscription needs
only the read actions above, which a subscription-scoped role already
covers.

---

## Step 4 — Collect the four values *(Entra admin)*

Airship needs exactly four strings. Three are GUIDs that look alike, so
label them as you collect them.

| Value | Where it comes from |
| --- | --- |
| **Subscription ID** | **Subscriptions** → your subscription → Overview |
| **Tenant ID** | The app's **Overview** blade, *Directory (tenant) ID* |
| **Client ID** | The app's **Overview** blade, *Application (client) ID* |
| **Client secret** | The **Value** column from **Certificates & secrets**. Cannot be read back |

Lost the secret? **Certificates & secrets** → **New client secret**. The
existing one keeps working until it expires or you delete it, so you can
roll over without an outage.

The client secret is the credential, so treat it like a password: it goes
into Airship and nowhere else — not a repository, not a Dockerfile, not
Slack. Airship validates the four values' shapes (three GUIDs and a secret
without whitespace), verifies them against Azure, seals them with
AES-256-GCM and opens them only for the duration of a single call. They are
never returned by any endpoint and never placed on a runner machine.

**The secret expires.** Entra client secrets have an end date (at most two
years). Put it in your calendar: an expired secret fails every launch (the
Events page shows Entra's refusal and the Cloud page shows **Credentials
rejected**) until you reconnect with a new one (Step 7, any time — a
new secret for the same subscription is always accepted). This is the one
piece of recurring maintenance Azure adds over AWS.

---

## Step 5 — Check the quotas that will bite *(Azure admin)*

A **quota** is a per-subscription, per-region cap Azure applies by default.
They exist to stop a runaway script costing you a fortune, and they are the
limit on concurrent runners far more often than Airship is. **Read this step
before you need it:** an increase is a request Azure approves, usually
within minutes to a few hours.

Azure counts vCPUs **twice** — once regionally and once per size family —
and on a new subscription the per-family number is often the lower one. That
double accounting is Azure's particular trap: raising "Total Regional vCPUs"
and still failing is almost always an unraised family quota.

### Do the arithmetic

*(vCPUs per machine) × (max concurrent runners) = the quota you need,
regionally **and** in the family you are using.*

A `Standard_D8s_v5` is 8 vCPUs in the *Standard DSv5 Family*, so 10
concurrent runners need **80** in both places. The number in the size name
is its vCPU count.

### Look at what you have

Search → **Quotas** → **Compute**, then filter by subscription and region.
The list shows usage against limit per family, and the regional total.

| Quota | Why it matters |
| --- | --- |
| Total Regional vCPUs | The real ceiling on concurrent runners, per region |
| Per-family vCPUs (`Standard DSv5 Family vCPUs`, …) | Often lower than the regional figure on a new subscription; each size family counts separately |
| Total Regional Low-priority vCPUs | For `preemptible: true` (Azure's spot) |
| GPU families (`Standard NCSv3 Family vCPUs`, …) | Usually 0 until you request an increase |
| Public IP Addresses — Standard | One per runner by default. `enable_public_ip: false` removes this ceiling |

### Ask for more

**Quotas** → **Compute** → find the row → the pencil / **Request increase**
→ enter the new limit → **Submit**. Small increases are often granted
immediately by the automated system; larger ones open a support request,
where "self-hosted CI runners for our GitHub organization" is the whole
justification.

Raise the regional total *and* every family you plan to use. GPU families
start at zero and are the slowest to be approved, so file those days ahead.
A quota is a ceiling, not a reservation — you are never billed for headroom.

### Then set `max_runners` below it

So the ceiling shows up as a dashboard message rather than a stream of
`QuotaExceeded` launches, and the eleventh job queues instead of failing.

### Third-party images

If any definition names a marketplace image with a purchase plan, accept its
terms once for the subscription — the portal prompts on the image's page in
the Marketplace. Canonical's Ubuntu images, the default, need no terms.

---

## Step 6 — Install the GitHub App *(GitHub org admin)*

Identical to the AWS path — the app's permissions are about GitHub, not
about your cloud. See
[aws-setup-console.md, Step 4](aws-setup-console.md#step-4--install-the-github-app-github-org-admin)
for the permission table, and for the **Subscribe to events** setting
(**Workflow job**, **Push**, **Repository**) that silently breaks everything
if it is empty. Installing the app activates nothing by itself; Step 8 is
the switch that matters.

---

## Step 7 — Connect Azure *(GitHub org admin)*

**Cloud → Microsoft Azure** in the dashboard, enter the subscription ID,
tenant ID, client ID and client secret from Step 4, and choose the default
region (API spelling — `eastus`, not *East US*).

On submit, Airship signs in to Entra with the secret, reads the subscription
(an application that cannot see it gets a 404, reported as bad
credentials), refuses a disabled or deleted subscription, and checks the
default region is one the subscription offers. It then **shows the
subscription ID and the application's client ID** — read them, and confirm
they are the subscription and identity you meant.

If it is rejected, work down this list in order; it covers nearly every
case:

1. *Must be a GUID* — two values swapped, or a display name pasted instead
   of an ID (Step 4's table).
2. *Entra did not accept the client secret* — you copied the **Secret ID**
   instead of the **Value**, or the secret has expired (Step 4).
3. *Bad credentials* with everything looking right — the role assignment is
   missing, so the subscription reads as not found (Step 2's check).
4. *Region not available* — portal spelling. Use the API form, which the
   subscription's own region list shows.

Nothing is stored on a failure, so fix and retry.

One credential per cloud per installation — you can have AWS, Google Cloud,
Azure and Daytona connected at the same time, and each runner definition
names its own `cloud:`. Replacing the secret within the same subscription is
always allowed; swapping it for another subscription's application, or
disconnecting, is refused while runners launched with it are alive, because
without it their machines could not be deleted.

---

## Step 8 — Activate repositories and commit a config

**Repositories →** toggle on. Public repositories need the separate
public-access choice, and an `airship.yaml` in a repository nobody activated
launches nothing. Start with a private repository and a Linux definition for
your first job — Windows adds minutes and variables you do not want while
proving the basics.

```yaml
runners:
  - name: build
    cloud: azure
    instance_type: [Standard_D8s_v5, Standard_D8as_v5]
    machine_image: ubuntu2204
    region: [eastus, westus3]
    labels: [airship-build]
    extra_config:
      storageProfile:
        osDisk:
          diskSizeGB: 128
          managedDisk: {storageAccountType: Premium_LRS}
      enable_public_ip: false        # egress via the NAT gateway Airship makes

  - name: arm
    cloud: azure
    instance_type: Standard_D4pls_v5  # "p": Arm, so the arm64 Ubuntu SKU
    labels: [airship-arm]

  - name: windows
    cloud: azure
    instance_type: Standard_D4s_v5
    machine_image: MicrosoftWindowsServer:WindowsServer:2022-datacenter-azure-edition:latest
    labels: [airship-windows]
```

```yaml
jobs:
  test:
    runs-on: airship-build--${{ github.run_id }}
```

The `--${{ github.run_id }}` suffix makes the runner register under a label
unique to that workflow run, so GitHub can only hand it a job from that run.
Use it everywhere except with idle pools, which exist before a run does.
Only labels starting with `airship-` are looked up at all, so jobs on
`ubuntu-latest` are untouched and you can migrate one workflow at a time.

Choosing the size, if you have no idea: start at `Standard_D8s_v5` for a
typical test suite, `Standard_B2s` for something small like a linter, and
change it once you have seen a real job's `nproc` and timings. The `D`
families are general-purpose, `F` is compute-optimised, `E` is
memory-optimised, `B` is burstable and cheapest. The OS disk **defaults to
75 GiB** on Linux, or the image's own size on Windows. Full reference:
[the configuration reference](../airship-yaml.md).

Three Azure-specific notes:

- **Regions and sizes are spelled as the API spells them** (`eastus`,
  `Standard_D8s_v5`), case and all.
- **The public address is on by default**, because Airship's subnet is
  private (as Azure makes new networks' subnets by default for API versions
  released after March 31, 2026), so a machine needs its own address or a
  NAT to reach GitHub. Set `enable_public_ip: false` for private runners
  behind a NAT.
- **Windows machines take a few minutes longer** to create and then start
  their boot script through a Run Command. That is expected; the launch
  keeps its lease for up to 15 minutes.

**Before you commit,** the dashboard's **Repositories** page has a config
checker that validates a file and shows the machines it would plan.

---

## Step 9 — Verify

Push, then watch in this order. The first job is slower than the rest — a
cold machine downloads the runner and your toolchain, which is what a custom
image removes.

| Where | What you should see |
| --- | --- |
| Airship → **Jobs** | The job queued within a second or two, with its planned machine |
| Airship → **Runners** | `pending → launching → running → busy`, instance ID shown as the VM's name |
| **Resource groups** → `airship-runners` | One VM, tagged, running, no identity — alongside the shared VNet and NSG |
| GitHub → the job log | Labels `self-hosted`, `x64`, `azure`, `cpu`, `airship-build--…` |
| The same resource group after the job | The VM gone, with its disk, interface and address |
| Airship → **Events** | Every launch failure and degradation, with Azure's own message |

When a machine boots but never registers, read the VM → **Help** → **Boot
diagnostics** → **Serial log**.

**The first-week test, Azure edition:** cancel a queued job and a running
job, and confirm no VM survives either — then stop a runner VM by hand from
the portal and confirm the next inventory pass (within ~15 seconds plus a
2-minute grace) deletes it. A stopped VM runs no job and, unless
deallocated, is still billed, which is why it is treated as gone.

---

## Going to production

| Step | Why |
| --- | --- |
| Set `max_runners` under your regional and per-family vCPU quotas | The one number that bounds total spend |
| Bake a custom image | Boot the runner tarball and your toolchain from a managed image or a compute gallery image; `machine_image` takes either one's ID |
| `enable_public_ip: false` as the default | One NAT per region with one stable outbound address, no per-machine public IPs, no Standard public IP ceiling, and nothing addressable from the internet |
| Add rules to `airship-runners-<region>` if you want egress control | Airship adds none of its own, so the security group is your lever, and it applies to every future runner in the region |
| Calendar the client secret's expiry | An expired secret stops every launch until you reconnect |
| Idle pools where latency matters | `idle: {replicas: 2}` or `schedule:` windows; they count toward `max_runners`. Especially worth it for Windows, whose cold start is minutes |
| A subscription per installation, if more than one GitHub organization uses Airship | The resource group, networks and NAT are shared by every installation connected to the subscription; machines are separated by tag alone |
| Watch the bill by tag | Cost Management → Cost analysis, grouped by the `airship-repository` tag |
| Remember: no cache acceleration on Azure | `cache: true` launches without it and records a warning. Use GitHub's own cache meanwhile. See [azure.md](azure.md#caching) |
| Run `TestAgainstLiveAzure` once | It is the cheapest way to turn "tested against a faithful fake" into "accepted by Azure" |

---

## Troubleshooting

Start at Airship's **Events** page for every one of these: it records every
launch failure and every degradation with Azure's own message quoted. These
causes are the same whichever guide you followed.

### Reading an Azure error

Azure error codes are CamelCase and fairly literal, in four kinds:

- **`AuthorizationFailed`** — a missing RBAC action. The message names it;
  add it to the role (Step 3).
- **`QuotaExceeded`, `OperationNotAllowed`, `PublicIPCountLimitReached`** —
  a quota. Yours to raise (Step 5). Remember the per-family one.
- **`AllocationFailed`** — Azure has run out of that size in that region
  right now. Nothing is misconfigured; Airship retries and falls back.
- **`RequestDisallowedByPolicy`, `MissingSubscriptionRegistration`** —
  governance: Azure Policy (Step 0) or an unregistered provider (Step 1).

### Table

| Symptom | Cause | Fix |
| --- | --- | --- |
| Job stays queued and **no job events appear at all** | GitHub is sending no webhooks — most often an app subscribed to no events | The app's **Permissions & events** → **Subscribe to events** needs **Workflow job**, **Push** and **Repository**. Then **Advanced** → **Recent Deliveries**. Connecting a cloud and activating repositories keep working without webhooks, which is why it *looks* healthy |
| Connect fails: *… must be a … (a GUID)* | A value pasted into the wrong field, or a display name instead of an ID | Use the IDs from Step 4 |
| Connect fails: *Microsoft Entra did not accept the client secret* | Wrong or expired secret, or the secret's ID instead of its value | Make a new secret (Step 4), reconnect |
| Connect fails with bad credentials, the secret is right | The application has no role on the subscription, so the subscription reads as not found | Step 2 or Step 3's role assignment, at the subscription scope |
| Connect fails: *region … is not available to subscription …* | A region the subscription does not offer, or a portal spelling (`East US`) | Use the API spelling |
| Launch fails: `AuthorizationFailed` | The custom role lacks an action, named in the message | Add it to the role (Step 3) |
| Launch fails: `MissingSubscriptionRegistration` | `Microsoft.Compute` or `Microsoft.Network` not registered, and the role cannot register it | Step 1 |
| Launch fails: `RequestDisallowedByPolicy` | An Azure Policy denies the resource | Step 0 |
| Launch fails: `QuotaExceeded` / `OperationNotAllowed` naming a quota | A regional or per-family vCPU quota | Raise it (Step 5); lower `max_runners` meanwhile. Treated as retryable |
| Launch fails: `AllocationFailed` | That region is out of that size | Expected and handled as retryable. If it persists, add sizes to `instance_type` or a second `region` |
| Launch fails: `PublicIPCountLimitReached` | The Standard public IP quota | `enable_public_ip: false`, or raise the quota |
| Launch fails: `SkuNotAvailable` | That size is not offered in that region | Pick another size or region |
| Launch fails: marketplace terms | A third-party image's terms not accepted | Accept them on the image's Marketplace page (the error carries the hint) |
| Launch fails: *image … is deprecated by its publisher* | The marketplace image was deprecated | Pick the publisher's newer SKU |
| Launch fails: *virtual network airship-runners-… was not created by Airship* | A network of that name exists without Airship's tag | Rename it. Airship refuses to put runners on a network nobody meant for them |
| Launch fails: *a virtual machine named … is not this runner's* | A VM with the runner's name and another runner's tag | Should not happen; delete the stray VM. Airship never deletes a VM it cannot prove is the runner's |
| Launch fails: *start the boot script on …* | The Run Command could not start (Windows or a specialized image) | The VM is deleted and the job retried. Repeated failures point at the image's VM agent |
| VM exists, runner never registers | No egress: a policy or a security group rule blocking outbound, or a private runner whose NAT is missing | Check the subnet's NAT gateway and any outbound rules you added, then the VM's boot diagnostics serial log |
| VM sits stopped | It shut itself down after its job; Azure does not delete on shutdown | Expected. The next inventory pass deletes it. If it lingers, the credential or the scan is failing — check Events |
| Runner finished `launch_unconfirmed` | The creation's answer was lost, or it was still in progress at the deadline | Handled: the job is retried, and the next attempt or the scan adopts the VM |
| `cache: true` does nothing | Expected on Azure | Events records `cache.unavailable`. [azure.md](azure.md#caching) |
| You cannot find a resource you just made | The portal is filtered to another subscription | The subscription filter sits in the portal's top bar, separately from any blade's own filters |

---

## Turning it off

As on AWS: deactivate a repository to stop it; uninstall the GitHub App to
stop everything (every runner is finished with reason `deleted` and its VM
removed). Disconnecting the credential is refused while runners launched
with it are alive.

Left behind by design, to delete by hand if you are done — and only once no
other installation uses the subscription, since the resource group is
shared. Confirm `airship-runners` holds no VMs, then delete the whole
resource group, which takes the networks, the NAT and its address with it.
Then delete the app registration (**Entra ID** → **App registrations**) and
the custom role if you made one.

Do run this rather than leaving it: a **NAT gateway and its public address
bill while they exist**, whether or not a runner is using them, which is the
one piece of Airship's Azure footprint that costs money at idle. If the
subscription was dedicated to CI, cancelling the subscription is the
cleanest end state.

---

## Glossary

| Term | What it means here |
| --- | --- |
| **App registration** | The identity Airship authenticates as, defined in Entra. Its subscription-side half is the **service principal** |
| **Azure Policy** | A rule engine that can deny or modify resource creation across a subscription. See Step 0 |
| **Client ID / secret** | The app registration's username and password for the API. The secret is shown once and expires |
| **Contributor** | The built-in role that can create and delete resources but not grant access. What Step 2 assigns |
| **Entra ID** | Microsoft's identity service, formerly Azure AD. Holds tenants, users and app registrations |
| **Managed disk** | A VM's disk. `Premium_LRS` is fast SSD, `StandardSSD_LRS` cheaper. Billed per GB-month |
| **NAT gateway** | What gives a machine with no public IP a route out. Airship creates one per region for `enable_public_ip: false`, and it bills while it exists |
| **NSG** | Network security group — a firewall on a subnet or interface. Airship's has no inbound rules |
| **RBAC** | Role-based access control: a principal, a role and a scope. Airship's role goes on the **subscription** |
| **Region / location** | `eastus`, `westeurope`. The API spelling, not the portal's display name |
| **Resource group** | A folder for resources inside a subscription. Airship makes `airship-runners` |
| **Resource provider** | A service namespace (`Microsoft.Compute`) that must be registered per subscription |
| **Spot / low-priority** | Much cheaper, reclaimable by Azure at short notice (`preemptible: true`) |
| **Subscription** | The billing and isolation boundary. Where machines and the bill live |
| **Tenant** | Your Entra directory — the organization that owns identities. Not the same as a subscription |
| **VM size** | A machine's shape, e.g. `Standard_D8s_v5`: 8 vCPU, `s` premium-storage-capable, `v5` generation |
| **VNet / subnet** | Your private network in a region, and a slice of it. Airship makes one per region |
