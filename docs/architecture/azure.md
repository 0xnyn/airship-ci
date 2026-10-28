# Airship on Microsoft Azure

**What this is.** Everything Airship does inside an Azure subscription:
the Resource Manager calls, the resources it creates, and the places where
Azure's model forced different decisions from EC2's and Compute Engine's.

**Audience.** Cloud and platform engineers approving the application's role
assignment. For the hands-on path, read [azure-setup.md](azure-setup.md)
next.

**Status — read this first.** The Azure provider is driven through
Microsoft's real `azure-sdk-for-go` clients (`armcompute` v8, `armnetwork`
v8, `armresources`, `armsubscriptions`) against a hand-written fake of
Resource Manager, built from Microsoft's reference: case-insensitive paths,
long-running operations through `Azure-AsyncOperation`, creations that fail
after they were accepted and leave a `Failed` VM, dropped answers that the
SDK's retries then repeat, hung and failing operation polls, resources that
cascade on delete by their `deleteOption`, marketplace versions, managed and
gallery images, networks, addresses, interfaces, NAT gateways and run
commands. The tests cover verification, the Linux, Windows, spot and disk
request shapes, idempotent relaunch, every error class before and after
acceptance with the leftovers cleaned up, lost answers settled by lookup, a
creation still in progress reported ambiguous and adopted later, the Run
Command started once and never twice, NAT creation, ownership checks on
delete, listing by region, installation and power state, and the orphan
sweep.

The machine's side has run for real: the exact `customData` script the
provider builds for a Linux launch (6,288 bytes, against Azure's 65,535
limit) ran unmodified in an `ubuntu:22.04` container on 2026-10-27, with a
JIT config carrying Airship's labels, took a real `workflow_dispatch` job,
passed it, and reached its shutdown.

**No Azure subscription has been available to run against.** No request
body has been accepted by Azure itself. Treat the Azure provider as
unexercised against the real API, however thorough the fake. The five
things to watch on a first real launch are listed at the end of this
document. An opt-in test (`TestAgainstLiveAzure`) verifies a service
principal, launches one `Standard_B2s`, checks idempotent relaunch, listing
with power state, the ownership refusal, deletion with the interface and
address, and an empty sweep. It costs one machine's minutes and is the
fastest way to close this gap.

---

## The picture

```
┌──────────────────────── your Azure subscription ──────────────────────────┐
│                                                                           │
│  resource group  airship-runners   (created on first launch)              │
│                                                                           │
│  region eastus                                                            │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ virtual network  airship-runners-eastus   10.0.0.0/16               │   │
│  │   subnet runners   default outbound access off                      │   │
│  │   security group airship-runners-eastus   no rules of Airship's     │   │
│  │                                                                     │   │
│  │  ┌──────────────────────────────────┐   tags airship-installation   │   │
│  │  │ VM  airship-<runner>             │        airship-runner-id      │   │
│  │  │   one job, one VM                │        airship-repository     │   │
│  │  │   no managed identity            │        airship-created        │   │
│  │  │   <name>-osdisk  ┐               │                               │   │
│  │  │   <name>-nic     ├ deleted with  │                               │   │
│  │  │   <name>-ip      ┘ the VM        │                               │   │
│  │  └────────────────┬─────────────────┘                               │   │
│  │                   │ egress                                          │   │
│  │   default         ├── its own static public address ──► internet    │   │
│  │                   │                                                 │   │
│  │   enable_public_ip: false                                           │   │
│  │                   └──► NAT gateway  airship-nat-eastus              │   │
│  │                        (+ address airship-nat-eastus-ip; one per    │   │
│  │                         region, shared by every runner there)       │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                           │
│  No cache bucket on Azure — see "Caching" below.                          │
└───────────────────────────────────────────────────────────────────────────┘
          ▲
          │ virtualMachines PUT / GET / list(statusOnly) / DELETE (force)
          │ runCommands PUT / GET · publicIPAddresses · networkInterfaces
          │ virtualNetworks · subnets · networkSecurityGroups · natGateways
          │ resourceGroups · VM images · images · galleries · subscriptions
          └──────────────── Airship control plane ──────────────────────────
                    holds one sealed Entra application secret
```

---

## Why Resource Manager is not EC2

Six differences shaped the provider. They explain decisions that look odd
next to the AWS path.

| Azure | Consequence in Airship |
| --- | --- |
| **A machine is several resources.** An address, an interface, the VM and its OS disk are separate Resource Manager resources | Each is created by name with a PUT, which is an idempotent upsert, so a retried launch re-finds every part an earlier attempt made. The interface, address and disk carry `deleteOption: Delete` and go with the VM. A launch that died between making the interface and making the VM leaves parts behind, which a sweep removes (see "Keeping the subscription clean") |
| **Machines are named, not numbered** | The runner's name *is* the VM's name, and the VM is looked up by name before anything is created. A VM with that name and this runner's tag is the launch; one with another runner's tag is a permanent refusal. There is no idempotency token on Azure, so the name is the whole of the guarantee |
| **Creating a VM is a long-running operation**, and a failure after acceptance (`AllocationFailed`, quota, marketplace terms) leaves a VM in the `Failed` state | The provider waits for the operation (up to 10 minutes) before calling a launch successful. A `Failed` VM is deleted, which takes its interface, address and disk with it, before the launch fails as retryable or permanent by the error code |
| **A VM that shuts itself down is not deleted**, and stays allocated and billed until deallocated or deleted | The inventory scan treats a VM whose power state is stopped or deallocated as gone and deletes it. Airship does not give the machine an identity that could delete itself |
| **Windows images do not run `customData`**, and specialized images take no OS profile at all | Linux takes the boot script as `customData` (cloud-init runs it once). Windows and specialized images get it through a managed Run Command, `airship-boot`, started once the VM is `Succeeded` |
| **Azure is retiring default outbound access.** Subnets of virtual networks created with API versions released after March 31, 2026 are private by default, and Airship makes its own subnet private explicitly (`defaultOutboundAccess: false`) | A machine with neither an address nor a NAT never reaches GitHub. Airship gives each machine a public address by default and creates a NAT gateway for `enable_public_ip: false`. This is a deliberate change from Cirun's default (see "Networking") |

Spot machines behave like EC2's: `priority: Spot` with `evictionPolicy:
Delete`, so an evicted machine is deleted rather than left deallocated, and
`maxPrice: -1` (up to the on-demand price, never evicted for price).

---

## What Airship creates in your subscription

| Resource | Name | Created when | Destroyed |
| --- | --- | --- | --- |
| Resource group | `airship-runners` | First launch in the subscription. Its own location is the first region launched in; resources of every region go in it | Never. Delete by hand if you stop using Airship |
| Network security group | `airship-runners-<region>` | First launch in a region | Never — later runners share it |
| Virtual network + subnet | `airship-runners-<region>`, subnet `runners`, `10.0.0.0/16` | First launch in a region | Never |
| NAT gateway + its address | `airship-nat-<region>`, `airship-nat-<region>-ip` (Standard, static) | First `enable_public_ip: false` launch in a region; attached to that region's subnet | Never |
| Public address | `<runner>-ip` (Standard, static IPv4) | One per machine, unless `enable_public_ip: false` | With the VM |
| Network interface | `<runner>-nic` | One per machine | With the VM |
| Virtual machine | the runner's name | One per job (or warm pool slot) | When the job ends |
| OS disk | `<runner>-osdisk` | With the VM | With the VM |
| Run Command | `airship-boot` | On Windows and specialized images only | With the VM |

No managed identities, no role assignments, no key vaults, no storage
accounts, no scale sets, no images, no extensions, nothing else.

The shared pieces (resource group, networks, security groups, NAT) carry
only `airship-managed: true`. They are **not** per installation: two
installations that connect the same subscription share them. Every
per-machine resource carries:

| Tag | Value |
| --- | --- |
| `airship-installation` | your GitHub installation ID — **the tenancy key**: listing filters on it, deleting refuses a VM without it, the sweep only touches resources carrying it |
| `airship-runner-id` | the runner's UUID |
| `airship-repository` | `owner/name` |
| `airship-created` | when the resource was made, so the sweep can tell an orphan from a launch in flight |

The security group `airship-runners-<region>` is the hook for your network
policy. Airship adds no rules of its own (Azure's defaults deny inbound
traffic from the internet and allow all outbound), and a rule you add there
applies to every runner in the region, present and future. Airship reads an
existing network with its name back before using it: one without the
`airship-managed` tag, or without a `runners` subnet, is refused with a
message naming it, rather than used for runners nobody meant to put there.

---

## Regions

```yaml
region: eastus                     # one region
region: [eastus, westus3]          # a fallback list, as on AWS
```

Regions are spelled as the API spells them (`eastus`, `uksouth`,
`germanywestcentral`), not as the portal shows them (`East US`). The default
is the connected credential's region, `eastus` unless you chose another at
connect time; it is checked against the subscription's region list when you
connect.

Airship does not choose availability zones. A single VM with no zone is
placed by Azure (Cirun documents no zone field for Azure either). A region out of capacity for
a size fails the attempt with `AllocationFailed`, which is retryable; the
job then moves through its fallback chain as on any cloud.

---

## Images

| `machine_image` | Resolves to |
| --- | --- |
| *(omitted)* or `ubuntu2204` | `Canonical:0001-com-ubuntu-server-jammy:22_04-lts-gen2:latest` (`22_04-lts-arm64` on Arm sizes) |
| `ubuntu2404` | `Canonical:ubuntu-24_04-lts:server:latest` (`server-arm64` on Arm sizes) |
| `publisher:offer:sku:version` | a marketplace image; `version` is `latest` or `X.Y.Z` |
| `{publisher, offer, sku, version}` | the same as a mapping, as Cirun's YAML spells it; `version` defaults to `latest` |
| `/subscriptions/S/resourceGroups/G/providers/Microsoft.Compute/images/N` | a managed image of the connected subscription |
| `.../galleries/G/images/N` | a compute gallery image; Azure launches its newest version |
| `.../galleries/G/images/N/versions/X.Y.Z` | exactly that version |
| a list | each entry is one fallback attempt |

Resolution reads the image's properties and is cached an hour. For a
marketplace image with `latest`, the newest version is read for its
properties and the `latest` reference is still what is sent, so Azure picks
the version at launch. What the image says decides:

- **Architecture.** An image whose architecture is known and differs from
  the size's is refused before the launch.
- **OS.** A Windows image gets the PowerShell boot script through a Run
  Command. A specialized image (managed or gallery) boots with its own
  accounts and host name, so it gets no OS profile and its script also
  goes through a Run Command.
- **Purchase plan.** A third-party image's plan is repeated on the VM, as
  Azure requires. Its marketplace terms must be accepted for the
  subscription once (`az vm image terms accept --urn ...`); the error says
  so when they are not.
- **Deprecation.** A marketplace image its publisher deprecated is refused.
- **Trusted Launch.** Requested only for images that require it (Windows 11)
  and for Windows images that support it, with the vTPM on and **Secure Boot
  off**: the GPU driver path loads unsigned kernel modules. Linux images
  stay on the standard security type, which every size takes.

A managed or gallery image in **another** subscription is refused by its ID.

---

## Disks

```yaml
extra_config:
  storageProfile:
    osDisk:
      diskSizeGB: 128
      managedDisk:
        storageAccountType: Premium_LRS
```

| | Default | Notes |
| --- | --- | --- |
| `diskSizeGB` | 75 GB on Linux (Cirun's root volume default); the image's own size on Windows (Microsoft documents marketplace Windows images as usually 127 GiB) | 30–4095. Azure does not shrink a disk below its image, which is why the image's size is the Windows default |
| `storageAccountType` | the image's default kind | `Standard_LRS`, `StandardSSD_LRS`, `Premium_LRS`, `StandardSSD_ZRS`, `Premium_ZRS`. Premium SSD v2 and Ultra Disk are refused as config errors: Microsoft documents both as unusable for an OS disk. Premium SSD works only on sizes Microsoft lists as premium-storage compatible (each size's page says); on another size Azure refuses the launch, as a permanent error |

The OS disk is the only disk. It uses read-write host caching and is deleted
with the VM. Managed disks are encrypted at rest by Azure with
platform-managed keys.

---

## Networking

**Default path.** One interface on the region's `runners` subnet, with a
Standard static public address of its own. Runners reach GitHub directly.
The subnet's security group has no rules of Airship's, so Azure's defaults
apply: no inbound traffic from the internet, all outbound allowed. A runner
needs no inbound traffic at all.

**Private runners.** `enable_public_ip: false` launches with no public
address and routes egress through a NAT gateway Airship creates on first
use: `airship-nat-<region>`, Standard, with a static address of its own and
a 10-minute TCP idle timeout (raised from Azure's 4-minute default for long
quiet connections to GitHub). It is attached to the region's subnet, which
is allowed while machines are using it. Once a region has the NAT, every
machine on that subnet egresses through it, including ones with their own
address: Microsoft's NAT gateway reference says a gateway on a subnet
"becomes the subnet's default next hop type for all outbound traffic
directed to the internet".

**Why the default differs from Cirun's.** Cirun launches Azure machines
without a public address unless `enable_public_ip: true`, and its docs say
outbound traffic then "goes through Azure's default outbound". Microsoft is
retiring that path: subnets of virtual networks created with API versions
released after March 31, 2026 are private by default and have no default
outbound access, and Airship's subnet is private by its own setting either
way. Without an address or a NAT, such a machine cannot reach GitHub. Airship keeps
`enable_public_ip` with the same meaning and flips the default to the one
that works; `false` gets you Cirun's private shape plus the NAT it needs.

**Your own network.** Not offered on Azure today: Cirun documents no field
for it, and runners go on Airship's own per-region network. The networks
use `10.0.0.0/16` and are not peered with anything, so overlap with your
other address spaces does not matter. If you need runners inside an existing
virtual network, say so — it is a contained addition, not a redesign.

Egress a runner needs is the same list as on AWS — GitHub,
`githubusercontent.com`, the distro's package mirrors, and whatever your
jobs reach. See the table in [aws.md](aws.md#networking). On Azure, the NAT
gateway gives every private runner in a region one stable outbound address,
which is the address to allow in a firewall elsewhere.

---

## Sizes, spot and GPUs

`instance_type` is an Azure VM size as the API spells it
(`Standard_D4s_v5`, `Standard_D2pls_v5`, `Standard_NC6s_v3`), or a list.

| Case | What Airship sends |
| --- | --- |
| A size whose feature letters include `p` (`Standard_D2pls_v5`, `Standard_E4ps_v5`) | Arm64: the Arm Ubuntu SKU for the aliases, and the `arm64` runner |
| `preemptible: true` | `priority: Spot`, `evictionPolicy: Delete`, `maxPrice: -1` |
| A size of the `NC`, `ND`, `NV` or `NG` families | The `gpu` label, and on Ubuntu images without a driver, the NVIDIA driver installed at boot |

On Azure the size decides the GPU; `gpu:` is a flag that asks for the
label and the driver, not a separate accelerator to attach.

**Accounts.** Azure requires an administrator account at creation. Linux
machines get an `airship` account with an SSH public key whose private half
is generated once per control-plane process, held in memory and never
written anywhere; password authentication is off. Windows machines get the
same account name with a random 24-character password that is discarded.
Nobody can log in to a runner, Airship included. Windows machines also get
automatic updates off, so an update cannot restart a machine mid-job, and a
host name that fits Windows' 15 characters (`air<hex>`).

**No managed identity.** No identity is assigned to a runner, so nothing in
a job can obtain an Azure token from the instance metadata service. The
application secret Airship holds is never on a machine. Cirun documents no
identity field for Azure, and Airship offers none.

---

## Caching

`cache: true` on an Azure runner launches **without** the cache proxy and
records a `cache.unavailable` warning on the Events page. The job runs
normally, just without acceleration.

This matches Cirun, whose caching documentation says auto mode is
"Currently Linux runners on AWS only". The proxy speaks S3; Azure Blob
Storage would be a backend of its own behind the provider interface's
`cloud.Cacher` seam, an addition rather than parity. Until then, Azure
runners can use `actions/cache` against GitHub's own cache.

---

## Keeping the subscription clean

Same loop as AWS, with Azure-specific behaviours:

| It sees | It does |
| --- | --- |
| A VM tagged for the installation with no live runner row | Deletes it |
| A runner row whose VM is no longer listed (after a 2-minute grace) | Marks the runner `lost`; retries its job if still queued |
| A VM whose power state is stopped or deallocated | Treats it as gone and deletes it, with its disk, interface and address. **This is how a machine that shut itself down is reclaimed on Azure**: until then it is still allocated and billed |
| A VM in `Deleting` | Lists it as stopping, and waits |
| A `Failed` VM that was never allocated (no power state at all) | Treats it as gone and deletes it |
| A runner whose lease expired or launch was unconfirmed, but whose VM exists | Revives it, after GitHub confirms the registration still exists |
| An interface or address tagged for the installation, in the region, attached to nothing and older than 15 minutes | Deletes it (the sweep, below) |
| A list that fails on any page | Nothing. The whole list fails; no VM is presumed gone |

**Listing uses the subscription-wide `virtualMachines` list with
`statusOnly=true`**, the only list that carries each VM's power state, and
filters it to the `airship-runners` resource group, the region and the
installation tag. This is why the application's role must reach the
subscription, not just the resource group.

**Deleting** reads the VM first and refuses one whose
`airship-installation` tag is not the installation's — a name recorded
wrongly can never destroy something else. The delete uses
`forceDeletion` and is not waited for; the next scan lists the VM as
stopping until it is gone.

**The sweep.** A launch that made the interface and address and then died
before creating the VM (a crash, a refusal) leaves both behind; the VM's own
deletion never sees them. After each inventory pass over a scope, the
provider deletes the installation's interfaces that no VM uses and addresses
that nothing is attached to, once they are older than 15 minutes (longer
than any launch in flight could take). It looks at most every five minutes
per scope, interfaces first, since an address attached to an interface
cannot be deleted before it. The shared resources are never swept.

**Every call is bounded.** 30 seconds for a single request, 3 minutes for a
network resource's creation, 10 minutes for a VM's (Linux is usually ready
in one or two, Windows in three to five), 5 minutes for the Run Command to
start (it waits for the VM agent), 2 minutes for one inventory list with all
its pages. The SDK's own retries are kept to two, with short delays, so a
deadline is spent on the call rather than on waiting to retry it.

**Slow launches keep their lease.** Creating a Windows machine and starting
its Run Command can take longer than the 5-minute launch lease. The launcher
renews the lease every half lease while the call runs, up to 15 minutes, and
cuts the call off 30 seconds before the last lease it knows of expires. If a
renewal is refused (the runner was finished meanwhile), the call is stopped
at once, so no machine is made for a runner nobody tracks.

---

## How Azure errors are classified

| Class | Examples | Behaviour |
| --- | --- | --- |
| **Retryable** | `AllocationFailed`, `ZonalAllocationFailed`, `OverconstrainedAllocationRequest`, `QuotaExceeded` (or `OperationNotAllowed` naming a quota), `PublicIPCountLimitReached`, `TooManyRequests`, `AnotherOperationInProgress`, `InternalExecutionError`, `OSProvisioningTimedOut`, `VMAgentStatusCommunicationError`, a 429 or 5xx | The chain starts over, up to the attempt cap |
| **Ambiguous** | A transport error, a 429 or 5xx with no verdict after the request was sent, a failed or timed-out operation poll, a VM still `Creating` when the launch runs out of time | Look the VM up by name; if it exists and is this runner's, that *is* the launch (a ready VM is booted, one still creating is waited for, a `Failed` one is deleted and retried). Otherwise `launch_unconfirmed`, and the next attempt or the scan adopts the VM |
| **Permanent** | An image that does not exist, a size the region does not offer, a disk kind the size cannot take, marketplace terms not accepted, a VM named for this runner but tagged for another | Next fallback; stop once every attempt has been tried |
| **Credential** | Microsoft Entra refuses the client secret; `AuthorizationFailed`; `InvalidAuthenticationToken`; a 401, 403 or (at connect time) a 404 on the subscription | Refused at connect time, so such a credential is never stored. At launch (a secret expired or a role removed since) it is permanent: the job moves to its next fallback and then fails, with Azure's message on the Events page. Airship then checks the credential again (at most every five minutes) and, if the cloud refuses it, the Cloud page shows **Credentials rejected** with the cloud's message and Events records `cloud.credentials_rejected`; reconnecting, or the cloud accepting it again, clears it. The check reads the subscription, so a role removed from it counts as rejected too; a role that still reads the subscription but lacks a launch action does not, and only the launch errors show it |

Errors are shown with Azure's own message and nothing else: the SDK's error
text carries the whole request and response, which is not repeated into the
Events page. A marketplace refusal adds the command that fixes it.

A creation **refused before it was accepted** deletes the interface and
address made for it; a creation **accepted and then failed** deletes the
`Failed` VM, which takes the interface, address and disk with it. A Run
Command that cannot be started deletes the VM rather than leave a machine
that will never register to idle out.

---

## Quotas to check before you scale

| Quota | Why it bites |
| --- | --- |
| **Total Regional vCPUs** | The real limit on concurrent runners. A `Standard_D8s_v5` is 8 vCPUs |
| **Per-family vCPUs** (`Standard DSv5 Family vCPUs`, …) | A second, per-family ceiling; often lower than the regional one on a new subscription |
| **Total Regional Spot vCPUs** (shown as low-priority) | For `preemptible: true` |
| GPU families (`Standard NCSv3 Family vCPUs`, …) | Usually 0 until you ask |
| **Public IP addresses — Standard** per region | One per runner by default. `enable_public_ip: false` removes this ceiling (the NAT uses one address) |
| Network interfaces per region | One per runner; the default limit is high |
| **Microsoft.Compute and Microsoft.Network registered** | Not a quota, but a fresh subscription may not have them. The SDK registers them on first use if the role allows it; see [azure-setup.md](azure-setup.md) |
| **Marketplace terms** | Not a quota, but a third-party image fails every launch until they are accepted once per subscription |

```bash
az vm list-usage --location eastus -o table
az network list-usages --location eastus -o table
```

---

## What to watch on the first real launch

Because no request body has yet been accepted by Azure, these five are the
specific things to verify on day one — each is a shape the fake accepts by
construction:

1. **`customData` size.** The Linux script was measured at 6,288 bytes raw
   and 8,384 encoded, well under the 65,535 limit, and a larger one is
   refused before the call. Worth an eyeball with many `env_vars`.
2. **Trusted Launch and size combinations.** Trusted Launch is requested for
   Windows images that support it; a size that cannot take it would refuse
   the VM, which surfaces as a permanent error naming the size.
3. **The NAT gateway attachment.** Creating the gateway and its address,
   then updating the subnet that may already have machines on it.
4. **The Run Command on a Windows image.** Asynchronous execution, started
   once the VM is `Succeeded`, and the PowerShell script it carries.
5. **Power state in `statusOnly=true` lists.** Microsoft documents the
   instance view's `PowerState/*` status there; the reclaim of a
   self-stopped machine depends on it.

Run `TestAgainstLiveAzure` with a throwaway subscription
(`AIRSHIP_LIVE_AZURE_{SUBSCRIPTION_ID,TENANT_ID,CLIENT_ID,CLIENT_SECRET}`)
and the first and fifth close in minutes (it launches one Linux machine with
a public address; the NAT and the Windows Run Command need a launch of their
own with `enable_public_ip: false` and a Windows image). It leaves the
resource group and network behind. Until then, this document describes a provider that is
thoroughly tested against a faithful fake and not yet against Azure.
