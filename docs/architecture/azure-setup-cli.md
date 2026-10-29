# Setting up Airship on Microsoft Azure — from the CLI

**What this is.** The same setup as
[azure-setup-console.md](azure-setup-console.md), as `az` commands. Terse on
purpose: it assumes you know your way around a shell and an Azure
subscription.

**New to Azure?** Use [azure-setup-console.md](azure-setup-console.md)
instead. It covers the same ground with clicks, explains tenants versus
subscriptions, and names the traps. Pick one guide and stay in it — they do
not interleave.

**Two steps have no CLI**, because they are web-only: installing the GitHub
App (Step 6) and creating the API token Steps 7 and 8 authenticate with.

**Status.** The Azure provider is thoroughly tested against a faithful fake
of Resource Manager, and its Linux boot script has run a real GitHub job,
but no request has yet been accepted by Azure itself. Read the status note
at the top of [azure.md](azure.md). `TestAgainstLiveAzure` closes most of
that gap for the price of one `Standard_B2s`'s minutes.

---

## Prerequisites

```bash
# macOS; elsewhere see https://learn.microsoft.com/cli/azure/install-azure-cli
brew install azure-cli

az login
az account show      # prints both the subscription ID and the tenant ID
```

```bash
export SUBSCRIPTION=00000000-0000-0000-0000-000000000000
export REGION=eastus
az account set --subscription "$SUBSCRIPTION"
```

Three things worth knowing before you start:

- **Tenant ≠ subscription.** The tenant holds identities, the subscription
  holds resources and the bill. You need an ID from each and they are both
  GUIDs, so label them as you collect them.
- **Regions are spelled as the API spells them** — `eastus`, not *East US*.
  `az account list-locations -o table` is the authoritative list.
- **You need Owner or User Access Administrator** on the subscription to
  create the role assignment in Step 2. Contributor is not enough: granting
  access is a separate power from using it.

---

## Step 0 — Policies

Four things commonly block this setup, and the first is a hard stop:

| Policy | Effect |
| --- | --- |
| Cannot register applications, or an app management policy restricts client secrets | **Hard blocker.** Airship authenticates with a client secret and nothing else — no certificate, no workload identity federation, no managed identity |
| Policy denying public IPs on NICs | Use `enable_public_ip: false` everywhere; Airship then creates a NAT gateway per region |
| Policy requiring tags or naming conventions | Airship's shared resources carry only `airship-managed: true`; machines carry the `airship-*` tags in [azure.md](azure.md#what-airship-creates-in-your-subscription). Exempt the `airship-runners` group, or use a *modify* policy rather than a deny |
| Policy limiting locations or VM sizes | Launches outside the list fail as permanent errors |

```bash
az policy assignment list --scope "/subscriptions/$SUBSCRIPTION" -o table
```

Empty output means nothing is in your way.

---

## Step 1 — Resource providers

```bash
az provider register --namespace Microsoft.Compute
az provider register --namespace Microsoft.Network

az provider show --namespace Microsoft.Compute --query registrationState -o tsv   # Registered
```

A fresh subscription may have neither. The SDK registers them on first use
when the role allows it (Contributor does), so this is required only with the
least-privilege role in Step 3, and harmless otherwise.

---

## Step 2 — The application, its secret and its role

One command registers the application, creates its service principal and
assigns Contributor on the subscription:

```bash
az ad sp create-for-rbac --name airship-runners --role Contributor \
  --scopes "/subscriptions/$SUBSCRIPTION"
```

**Copy the output now** — `password` is never shown again:

| Printed | Airship calls it |
| --- | --- |
| `appId` | Client ID |
| `password` | Client secret |
| `tenant` | Tenant ID |
| (your subscription) | Subscription ID |

Contributor on the subscription is the simple choice and a reasonable end
state in a dedicated CI subscription. Skip to Step 4, or narrow it in Step 3.

```bash
export CLIENT_ID=$(az ad sp list --display-name airship-runners --query "[0].appId" -o tsv)
export TENANT_ID=$(az account show --query tenantId -o tsv)

az role assignment list --assignee "$CLIENT_ID" --all -o table
```

An empty role-assignment list is the cause of the most misleading error
here: a subscription the application cannot see reads as *not found*, and
Step 7 reports it as bad credentials.

---

## Step 3 — Least privilege *(optional)*

Skip on a dedicated CI subscription. The role must be assigned at the
**subscription**, not a resource group: the inventory scan lists the
subscription's VMs (the only list carrying power state), marketplace image
lookups are subscription-level reads, and `airship-runners` is created on
first use.

```bash
cat > airship-role.json <<JSON
{
  "Name": "Airship runners",
  "Description": "Launch, list and delete Airship's GitHub Actions runner VMs",
  "AssignableScopes": ["/subscriptions/$SUBSCRIPTION"],
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
JSON

az role definition create --role-definition airship-role.json
az role assignment create --assignee "$CLIENT_ID" --role "Airship runners" \
  --scope "/subscriptions/$SUBSCRIPTION"

# and drop the broad one Step 2 created
az role assignment delete --assignee "$CLIENT_ID" --role Contributor \
  --scope "/subscriptions/$SUBSCRIPTION"
```

What those actions are for: the connect check and region list
(`subscriptions/read`, `locations/read`); creating `airship-runners`
(`resourceGroups/*`); the launch, idempotency lookup, inventory list with
power states and teardown (`virtualMachines/*`, `instanceView/read`,
`disks/*`); Windows and specialized images (`runCommands/*`); resolving
`machine_image` (`publishers/.../versions/read`, `images/read`,
`galleries/...`); the per-region network (`virtualNetworks/*`, `subnets/*`,
`networkSecurityGroups/*`); each machine's interface and address plus the
orphan sweep (`networkInterfaces/*`, `publicIPAddresses/*`);
`enable_public_ip: false` (`natGateways/*`, a wildcard because Microsoft's
published list names only `join/action`, not the read and write the
gateway's creation needs); long-running operation polling; and marketplace
images with a purchase plan.

Every action above except the NAT wildcard was checked verbatim against
Microsoft's published operation lists on 2026-10-28. The role as a whole has
not yet been exercised against Azure, so it may still be missing one. A
launch that fails with `AuthorizationFailed` names the missing action in the
message, and Events shows it: add it and relaunch.

---

## Step 4 — The four values

```bash
echo "subscription : $SUBSCRIPTION"
echo "tenant       : $TENANT_ID"
echo "client       : $CLIENT_ID"
# client secret: the "password" Step 2 printed; it cannot be read back.
```

Make a new secret without an outage — `--append` keeps the existing one
working:

```bash
az ad app credential reset --id "$CLIENT_ID" --append --years 1
```

Omit `--append` and the old secret is revoked immediately.

**The secret expires** (at most two years). Calendar it: an expired secret
fails every launch — Events shows Entra's refusal, the Cloud page shows
**Credentials rejected** — until you reconnect (Step 7, any time). This is
the one piece of recurring maintenance Azure adds over AWS.

---

## Step 5 — Quotas

Azure counts vCPUs **twice**, regionally and per size family, and on a new
subscription the per-family number is often the lower one. Raising "Total
Regional vCPUs" and still failing is almost always an unraised family quota.

*(vCPUs per machine) × (max concurrent runners)*, in both places. A
`Standard_D8s_v5` is 8 vCPUs in the *Standard DSv5 Family*.

```bash
az vm list-usage --location "$REGION" -o table        # regional + per family
az network list-usages --location "$REGION" -o table  # public IPs
```

| Quota | Why it matters |
| --- | --- |
| Total Regional vCPUs | The real ceiling on concurrent runners, per region |
| Per-family vCPUs (`Standard DSv5 Family vCPUs`, …) | Often lower than the regional figure; each family counts separately |
| Total Regional Low-priority vCPUs | For `preemptible: true` (Azure's spot) |
| GPU families (`Standard NCSv3 Family vCPUs`, …) | Usually 0 until you request an increase |
| Public IP Addresses — Standard | One per runner by default; `enable_public_ip: false` removes this ceiling |

Requesting an increase needs the `quota` extension
(`az extension add --name quota`), and the portal is the path most people
should use:

```bash
az quota list \
  --scope "/subscriptions/$SUBSCRIPTION/providers/Microsoft.Compute/locations/$REGION" \
  -o table

az quota update --resource-name standardDSv5Family \
  --scope "/subscriptions/$SUBSCRIPTION/providers/Microsoft.Compute/locations/$REGION" \
  --limit-object value=160 type=LimitValue
```

Raise the regional total *and* every family you plan to use. GPU families
start at zero and are slowest to approve. Then set `max_runners` below the
quota, so the ceiling is a dashboard message rather than a stream of
`QuotaExceeded` launches.

**Third-party images** with a purchase plan need their terms accepted once
per subscription. Canonical's Ubuntu images, the default, need none:

```bash
az vm image terms accept --urn publisher:offer:plan:latest
```

---

## Step 6 — Install the GitHub App *(no CLI — web only)*

See
[aws-setup-console.md, Step 4](aws-setup-console.md#step-4--install-the-github-app-github-org-admin)
for the permission table — the app's permissions are about GitHub, not your
cloud.

**If you run your own control plane,** the app's **Permissions & events** →
**Subscribe to events** must include **Workflow job**, **Push** and
**Repository**. An app subscribed to no events looks healthy — you can
connect a cloud and activate repositories — and jobs never appear.

---

## Step 7 — Connect Azure

Create an API token in the dashboard first, under **API keys** *(web only)*.

```bash
export AIRSHIP_API=https://api.example.com
export AIRSHIP_TOKEN=asa_...
export CLIENT_SECRET='...'          # the "password" from Step 2

curl -fsS -X POST "$AIRSHIP_API/api/v1/cloud-connect" \
  -H "Authorization: Bearer $AIRSHIP_TOKEN" \
  -H 'Content-Type: application/json' \
  -d "$(printf '{"cloud":"azure","credentials":{"subscription_id":"%s","tenant_id":"%s","client_id":"%s","client_secret":"%s","region":"%s"}}' \
        "$SUBSCRIPTION" "$TENANT_ID" "$CLIENT_ID" "$CLIENT_SECRET" "$REGION")"
```

`region` is optional and sets the default used when a definition names none.
Check what is connected:

```bash
curl -fsS "$AIRSHIP_API/api/v1/cloud-connect" -H "Authorization: Bearer $AIRSHIP_TOKEN"
```

On submit Airship signs in to Entra with the secret, reads the subscription
(an application that cannot see it gets a 404, reported as bad credentials),
refuses a disabled or deleted subscription, checks the default region is one
the subscription offers, and **echoes back the subscription ID and the
client ID**. Read them and confirm they are what you meant.

Rejected? Work down this list; it covers nearly every case:

1. *Must be a GUID* — two values swapped, or a display name instead of an ID.
2. *Entra did not accept the client secret* — expired, or you used the
   secret's **ID** rather than its **value**.
3. *Bad credentials* with everything looking right — the role assignment is
   missing (Step 2's check).
4. *Region not available* — portal spelling; use
   `az account list-locations -o table`.

One credential per cloud per installation. Replacing the secret within the
same subscription is always allowed; swapping it for another subscription's
application, or disconnecting, is refused while runners launched with it are
alive.

---

## Step 8 — Activate repositories

```bash
curl -fsS "$AIRSHIP_API/v1/api/repositories" \
  -H "Authorization: Bearer $AIRSHIP_TOKEN"

curl -fsS -X PUT "$AIRSHIP_API/v1/api/repositories/api/activation" \
  -H "Authorization: Bearer $AIRSHIP_TOKEN" \
  -H 'Content-Type: application/json' -d '{}'
```

The path takes the **repository name alone**, not `owner/repo`, matched
case-insensitively within the installation. A public repository needs
`-d '{"allowPublic": true}'`, because its fork pull requests run untrusted
code on your machines. `DELETE` the same path to deactivate.

An `airship.yaml` in a repository nobody activated launches nothing, which
is why this is a separate step.

---

## Step 9 — The two files

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

- **Regions and sizes use the API's spelling**, case and all.
- **A public address is on by default**, because Airship's subnet is private
  (as Azure makes new networks' subnets by default for API versions released
  after March 31, 2026), so a machine needs its own address or a NAT to
  reach GitHub.
- **Windows takes minutes longer** to create and then start its boot script
  through a Run Command. The launch keeps its lease for up to 15 minutes,
  and Windows carries a licence charge on top of compute.
- The OS disk **defaults to 75 GiB** on Linux, the image's own size on
  Windows.
- The `--${{ github.run_id }}` suffix registers the runner under a label
  unique to that run. Use it everywhere except idle pools.

Full reference: [the configuration reference](../airship-yaml.md).

---

## Step 10 — Verify

```bash
# everything Airship has running, with what it is for
az vm list -g airship-runners -d \
  --query "[].{name:name, location:location, power:powerState, runner:tags.\"airship-runner-id\"}" -o table

# the shared pieces
az network vnet list -g airship-runners -o table
az network nat gateway list -g airship-runners -o table

# expect no inbound rules at all
az network nsg list -g airship-runners --query "[].securityRules" -o json

# a machine that booted but never registered
az vm boot-diagnostics get-boot-log -g airship-runners -n NAME
```

After the job the VM is gone, with its disk, interface and address.
Alongside: **Jobs** shows it queued within a second or two; **Runners** shows
`pending → launching → running → busy`; the job log shows labels
`self-hosted`, `x64`, `azure`, `cpu`, `airship-build--…`.

**The first-week test, Azure edition:** cancel a queued job and a running
job and confirm no VM survives either, then stop a runner VM by hand and
confirm the next inventory pass (~15 seconds plus a 2-minute grace) deletes
it. A stopped VM runs no job and, unless deallocated, still bills.

---

## Troubleshooting

Identical causes whichever guide you followed, so they live in one place:
[azure-setup-console.md § Troubleshooting](azure-setup-console.md#troubleshooting).
It sorts Azure's error codes by what they mean — `AuthorizationFailed`,
`QuotaExceeded`, `AllocationFailed`, `RequestDisallowedByPolicy` — and covers
the empty webhook subscription and the network-ownership refusals.

Start at **Events** for all of them.

---

## Turning it off

```bash
curl -fsS -X DELETE "$AIRSHIP_API/v1/api/repositories/api/activation" \
  -H "Authorization: Bearer $AIRSHIP_TOKEN"
```

Uninstalling the GitHub App stops everything: every runner is finished with
reason `deleted` and its VM removed. Disconnecting the credential is refused
while runners launched with it are alive.

Then, by hand — and only once no other installation uses the subscription,
since the resource group is shared. A **NAT gateway and its public address
bill while they exist**, so do clear this up:

```bash
az vm list -g airship-runners -o table                   # should be empty
az group delete -n airship-runners                       # networks, NAT, its address
az ad sp delete --id "$CLIENT_ID"
az ad app delete --id "$CLIENT_ID"
az role definition delete --name "Airship runners"       # if you made it
```

If the subscription was dedicated to CI, cancelling it is the cleanest end
state.

---

## Appendix — the whole Azure side in one go

```bash
#!/usr/bin/env bash
set -euo pipefail
SUBSCRIPTION=00000000-0000-0000-0000-000000000000
REGION=eastus

az account set --subscription "$SUBSCRIPTION"
az account show          # confirm the subscription and tenant before changing anything

az provider register --namespace Microsoft.Compute
az provider register --namespace Microsoft.Network

# Prints appId (client ID), password (client secret) and tenant.
az ad sp create-for-rbac --name airship-runners --role Contributor \
  --scopes "/subscriptions/$SUBSCRIPTION"

az vm list-usage --location "$REGION" -o table
az network list-usages --location "$REGION" -o table
```

Then: install the app (Step 6), connect the four values (Step 7), activate a
repository (Step 8), commit the two files from Step 9.
