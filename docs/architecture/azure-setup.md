# Setting up Airship on Microsoft Azure

**What this is.** The path from an empty Azure subscription to a green CI
job on your own Azure virtual machines, including the policies that are
most likely to stand in the way.

**Audience.** Whoever owns the Azure subscription and its Microsoft Entra
tenant, and whoever owns the GitHub organization.

**Status.** Read the status note at the top of [azure.md](azure.md) before
you plan around this. The Azure provider is thoroughly tested against a
faithful fake of Resource Manager, and the Linux boot script it sends has
run a real GitHub job, but no request has yet been accepted by Azure itself.
The first launch in a real subscription is the thing to watch, and
`TestAgainstLiveAzure` closes most of that gap for the price of one
`Standard_B2s`'s minutes.

---

## Before you start

| You need | Notes |
| --- | --- |
| An Azure subscription, ideally dedicated to CI | Same argument as on AWS: the role assignment becomes uninteresting and the bill becomes legible |
| Permission to register an application in Microsoft Entra ID and create a client secret | Often restricted in larger tenants. See Step 0 |
| Owner or User Access Administrator on the subscription | To assign the application its role |
| Admin on the GitHub organization | To install the app and activate repositories |
| A region | `eastus` by default. Spelled as the API spells it, not as the portal shows it |
| A concurrency number | Becomes `max_runners`. Start at 10 |
| A control plane | Airship-hosted, or your own ([operations.md](operations.md)) |
| The Azure CLI (`az`) | The commands below use it; the portal works too, as Cirun's own guide shows |

---

## Step 0 — Check your policies *(Azure admin)*

Four things commonly block this setup. Find out now rather than in Step 7.

| Policy | Effect on Airship | What to do |
| --- | --- | --- |
| Users cannot register applications, or client secrets are restricted by an app management policy | **Hard blocker.** Airship authenticates with an application's client secret and nothing else — no certificate, no workload identity federation, no managed identity | Have an Entra admin create the application and secret, or grant an exception for this one application |
| Azure Policy denying public IP addresses on network interfaces | Blocks the default path, which gives each runner a public address | Use `enable_public_ip: false` on every definition. Airship then creates a NAT gateway per region |
| Azure Policy requiring tags, or a naming convention, on resource groups or resources | Airship's shared resources carry only `airship-managed: true`; machines carry the `airship-*` tags in [azure.md](azure.md#what-airship-creates-in-your-subscription). A deny policy fails the launch with the policy's name | Exempt the `airship-runners` resource group, or use a "modify" policy that adds your tags rather than a deny |
| Azure Policy limiting allowed locations or VM sizes | Launches outside the list fail as permanent errors | Make sure the regions and sizes in your `airship.yaml` are on the list |

```bash
az policy assignment list --scope "/subscriptions/$SUBSCRIPTION" -o table
```

---

## Step 1 — Register the resource providers *(Azure admin)*

```bash
SUBSCRIPTION=00000000-0000-0000-0000-000000000000
az account set --subscription "$SUBSCRIPTION"
az provider register --namespace Microsoft.Compute
az provider register --namespace Microsoft.Network
```

A fresh subscription may have neither registered. The SDK registers them on
first use when the application's role allows it (Contributor does), so this
step is required only with the least-privilege role in Step 3, and harmless
otherwise.

---

## Step 2 — Register the application *(Entra admin)*

```bash
az ad sp create-for-rbac --name airship-runners --role Contributor \
  --scopes "/subscriptions/$SUBSCRIPTION"
```

This registers an application, creates its service principal, assigns it
Contributor on the subscription, and prints the four values Airship needs:

| Printed | Airship calls it |
| --- | --- |
| `appId` | Client ID |
| `password` | Client secret |
| `tenant` | Tenant ID |
| (your subscription) | Subscription ID |

Contributor on the subscription is what Cirun asks for, and in a dedicated
CI subscription it is a reasonable end state. Skip to Step 4, or replace it
with the narrower role in Step 3.

In the portal, the same is **Microsoft Entra ID → App registrations → New
registration**, then **Certificates & secrets → New client secret**, then
**Subscriptions → Access control (IAM) → Add role assignment →
Contributor**.

---

## Step 3 — Least privilege *(optional, Azure admin)*

The role has to be assigned at the **subscription**, not at the resource
group: the inventory scan lists the subscription's VMs (the only list that
carries their power state), the marketplace image lookups are
subscription-level reads, and the `airship-runners` resource group is
created on first use.

A custom role covering exactly the calls Airship makes:

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

```bash
az role definition create --role-definition airship-role.json
az role assignment create --assignee "$CLIENT_ID" --role "Airship runners" \
  --scope "/subscriptions/$SUBSCRIPTION"
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

```bash
TENANT_ID=$(az account show --query tenantId -o tsv)
CLIENT_ID=$(az ad sp list --display-name airship-runners --query "[0].appId" -o tsv)
# CLIENT_SECRET is the "password" Step 2 printed; it cannot be read back.
# To make a new one:
#   az ad app credential reset --id "$CLIENT_ID" --append --years 1
```

The client secret is the credential. Airship validates the four values'
shapes (three GUIDs and a secret without whitespace), verifies them against
Azure, seals them with AES-256-GCM and opens them only for the duration of a
single call. They are never returned by any endpoint and never placed on a
runner machine.

**The secret expires.** Entra client secrets have an end date (at most two
years). Put it in your calendar: an expired secret fails every launch (the
Events page shows Entra's refusal and the Cloud page shows **Credentials
rejected**) until you reconnect with a new one (Step 7, any time — a
new secret for the same subscription is always accepted).

---

## Step 5 — Check the quotas that will bite *(Azure admin)*

```bash
az vm list-usage --location eastus -o table
az network list-usages --location eastus -o table
```

| Quota | Why it matters |
| --- | --- |
| Total Regional vCPUs | The real ceiling on concurrent runners, per region |
| Per-family vCPUs (`Standard DSv5 Family vCPUs`, …) | Often lower than the regional figure on a new subscription; each size family counts separately |
| Total Regional Low-priority vCPUs | For `preemptible: true` (Azure's spot) |
| GPU families (`Standard NCSv3 Family vCPUs`, …) | Usually 0 until you request an increase |
| Public IP Addresses — Standard | One per runner by default. `enable_public_ip: false` removes this ceiling |

Arithmetic as on AWS: *(vCPUs per machine) × (max concurrent runners)*. A
`Standard_D8s_v5` is 8 vCPUs. Then set `max_runners` below what the quota
allows, so the ceiling shows up as a dashboard message rather than a stream
of `QuotaExceeded` launches.

**Third-party images.** If any definition names a marketplace image with a
purchase plan, accept its terms once for the subscription:

```bash
az vm image terms accept --urn publisher:offer:plan:latest
```

Canonical's Ubuntu images, the default, need no terms.

---

## Step 6 — Install the GitHub App *(GitHub org admin)*

Identical to the AWS path — the app's permissions are about GitHub, not
about your cloud. See [aws-setup.md](aws-setup.md#step-4--install-the-github-app-github-org-admin)
for the permission table and why each one is needed.

---

## Step 7 — Connect Azure *(GitHub org admin)*

**Dashboard:** **Cloud → Microsoft Azure**, enter the subscription ID,
tenant ID, client ID and client secret, and choose the default region.

**Or the developer API:**

```bash
export CIRUN_API_ENDPOINT=https://api.example.com/api/v1
export CIRUN_API_KEY=asa_...
cirun cloud connect azure --subscription-id "$SUBSCRIPTION" \
  --tenant-id "$TENANT_ID" --client-id "$CLIENT_ID" --client-secret "$CLIENT_SECRET"
```

cirun-py sends no region, so that path uses `eastus`; to choose another,
call the route directly with `"region"` among the credentials:

```bash
curl -X POST "$CIRUN_API_ENDPOINT/cloud-connect" \
  -H "Authorization: Bearer $CIRUN_API_KEY" -H "Content-Type: application/json" \
  -d '{"cloud": "azure", "credentials": {"subscription_id": "...", "tenant_id": "...",
       "client_id": "...", "client_secret": "...", "region": "westeurope"}}'
```

On submit, Airship signs in to Entra with the secret, reads the subscription
(an application that cannot see it gets a 404, reported as bad
credentials), refuses a disabled or deleted subscription, and checks the
default region is one the subscription offers. It then shows the
subscription ID and the application's client ID so you can confirm it is the
identity you meant.

One credential per cloud per installation — you can have AWS, Google Cloud,
Azure and Daytona connected at the same time, and each runner definition
names its own `cloud:`. Replacing the secret within the same subscription is
always allowed; swapping it for another subscription's application, or
disconnecting, is refused while runners launched with it are alive, because
without it their machines could not be deleted.

---

## Step 8 — Activate repositories and commit a config

Activation works exactly as on AWS: **Repositories →** toggle on, public
repositories need the separate public-access choice, and an `airship.yaml` in
a repository nobody activated launches nothing.

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

Three Azure-specific notes:

- **Regions and sizes are spelled as the API spells them** (`eastus`,
  `Standard_D8s_v5`), case and all.
- **The public address is on by default**, unlike Cirun's Azure default,
  because Airship's subnet is private (as Azure makes new networks'
  subnets by default for API versions released after March 31, 2026), so
  a machine needs its own address or a NAT to reach GitHub. Set
  `enable_public_ip: false` for private runners behind a NAT.
- **Windows machines take a few minutes longer** to create and then start
  their boot script through a Run Command. That is expected; the launch
  keeps its lease for up to 15 minutes.

---

## Step 9 — Verify

| Where | What you should see |
| --- | --- |
| Airship → **Jobs** | The job queued within a second or two, with its planned machine |
| Airship → **Runners** | `pending → launching → running → busy`, instance ID shown as the VM's name |
| `az vm list -g airship-runners -d -o table` | One VM, tagged, running, no identity |
| GitHub → the job log | Labels `self-hosted`, `x64`, `azure`, `cpu`, `airship-build--…` |
| `az vm list -g airship-runners` after the job | Gone, with its disk, interface and address |
| Airship → **Events** | Every launch failure and degradation, with Azure's own message |

```bash
# Everything Airship has running, with what it is for
az vm list -g airship-runners -d \
  --query "[].{name:name, location:location, power:powerState, runner:tags.\"airship-runner-id\"}" -o table

# The shared pieces
az network vnet list -g airship-runners -o table
az network nat gateway list -g airship-runners -o table
```

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
| Remember: no cache acceleration on Azure | `cache: true` launches without it and records a warning. Use GitHub's own cache meanwhile. See [azure.md](azure.md#caching) |
| Run `TestAgainstLiveAzure` once | It is the cheapest way to turn "tested against a faithful fake" into "accepted by Azure" |

---

## Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| Connect fails: *… must be a … (a GUID)* | A value pasted into the wrong field, or a display name instead of an ID | Use the IDs from Step 4 |
| Connect fails: *Microsoft Entra did not accept the client secret* | Wrong or expired secret, or the secret's ID instead of its value | `az ad app credential reset --id "$CLIENT_ID" --append`, reconnect |
| Connect fails with bad credentials, the secret is right | The application has no role on the subscription, so the subscription reads as not found | Step 2 or Step 3's role assignment, at the subscription scope |
| Connect fails: *region … is not available to subscription …* | A region the subscription does not offer, or a portal spelling (`East US`) | Use the API spelling: `az account list-locations -o table` |
| Launch fails: `AuthorizationFailed` | The custom role lacks an action, named in the message | Add it to the role (Step 3) |
| Launch fails: `MissingSubscriptionRegistration` | `Microsoft.Compute` or `Microsoft.Network` not registered, and the role cannot register it | Step 1 |
| Launch fails: `RequestDisallowedByPolicy` | An Azure Policy denies the resource | Step 0 |
| Launch fails: `QuotaExceeded` / `OperationNotAllowed` naming a quota | A regional or per-family vCPU quota | Raise it; lower `max_runners` meanwhile. Treated as retryable |
| Launch fails: `AllocationFailed` | That region is out of that size | Expected and handled as retryable. If it persists, add sizes to `instance_type` or a second `region` |
| Launch fails: `PublicIPCountLimitReached` | The Standard public IP quota | `enable_public_ip: false`, or raise the quota |
| Launch fails: marketplace terms | A third-party image's terms not accepted | `az vm image terms accept --urn …` (the error carries the hint) |
| Launch fails: *image … is deprecated by its publisher* | The marketplace image was deprecated | Pick the publisher's newer SKU |
| Launch fails: *virtual network airship-runners-… was not created by Airship* | A network of that name exists without Airship's tag | Rename it. Airship refuses to put runners on a network nobody meant for them |
| Launch fails: *a virtual machine named … is not this runner's* | A VM with the runner's name and another runner's tag | Should not happen; delete the stray VM. Airship never deletes a VM it cannot prove is the runner's |
| Launch fails: *start the boot script on …* | The Run Command could not start (Windows or a specialized image) | The VM is deleted and the job retried. Repeated failures point at the image's VM agent |
| VM exists, runner never registers | No egress: a policy or a security group rule blocking outbound, or a private runner whose NAT is missing | Check the subnet's NAT gateway and any outbound rules you added |
| VM sits stopped | It shut itself down after its job; Azure does not delete on shutdown | Expected. The next inventory pass deletes it. If it lingers, the credential or the scan is failing — check Events |
| Runner finished `launch_unconfirmed` | The creation's answer was lost, or it was still in progress at the deadline | Handled: the job is retried, and the next attempt or the scan adopts the VM |
| `cache: true` does nothing | Expected on Azure | Events records `cache.unavailable`. [azure.md](azure.md#caching) |

---

## Turning it off

As on AWS: deactivate a repository to stop it; uninstall the GitHub App to
stop everything (every runner is finished with reason `deleted` and its VM
removed). Disconnecting the credential is refused while runners launched
with it are alive.

Left behind by design, to delete by hand if you are done — and only once no
other installation uses the subscription, since the resource group is
shared:

```bash
az vm list -g airship-runners -o table                 # should be empty
az group delete -n airship-runners                       # networks, NAT, its address
az ad sp delete --id "$CLIENT_ID"
az ad app delete --id "$CLIENT_ID"
az role definition delete --name "Airship runners"       # if you made it
```

---

## Appendix — the whole Azure side in one go

```bash
#!/usr/bin/env bash
set -euo pipefail
SUBSCRIPTION=00000000-0000-0000-0000-000000000000
REGION=eastus

az account set --subscription "$SUBSCRIPTION"
az provider register --namespace Microsoft.Compute
az provider register --namespace Microsoft.Network

# Prints appId (client ID), password (client secret) and tenant.
az ad sp create-for-rbac --name airship-runners --role Contributor \
  --scopes "/subscriptions/$SUBSCRIPTION"

az vm list-usage --location "$REGION" -o table
az network list-usages --location "$REGION" -o table
```

Then: install the app, connect the four values (Cloud → Microsoft Azure),
activate a repository, commit the config from Step 8.
