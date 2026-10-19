# Architecture

The design decisions behind CAPLV, its host security model and how it uses
storage. The [README](../README.md#how-it-works) shows the provisioning flow.

## Key design decisions

Rationale and trade-offs are recorded in [host management](adr/0001-host-management.md)
and [readiness and bootstrap](adr/0002-readiness-and-bootstrap.md).

- **virsh-over-SSH** — pure Go binary (`CGO_ENABLED=0`), distroless container.
  No `libvirt-dev`, no CGo. The `Client` interface allows swapping to native
  libvirt bindings later.
- **One VM per host** — enforced by admission webhook. Each LibvirtHost runs
  at most one CAPLV-managed VM. No contention, no resource accounting complexity.
- **Auto-sizing** — omit `vcpus` and `memoryMB` and CAPLV sizes the VM from
  the host's available capacity (total minus `reservedResources`). Hosts with
  varying specs just work.
- **Static IPs required** — every VM must declare its network identity upfront.
  No DHCP. This matches 5-Spot's model of predetermined machine configurations.
- **Infrastructure readiness** — LibvirtCluster is ready when its externally
  managed control plane endpoint has a host and valid port. API server health
  is separate from infrastructure readiness; reconciliation responds to object
  changes instead of polling the endpoint.
- **Parallel at scale** — 50 concurrent reconcilers by default. Each host runs
  one VM, so there are no contention issues across parallel provisions.
- **Ephemeral storage** — `ephemeralPool: true` creates a per-machine tmpfs
  mount and libvirt pool on demand, destroys both on cleanup. No host setup
  required, no persistent storage touched, RAM reclaimed when the VM goes away.
- **Metadata injection** — CAPLV injects the machine hostname and providerID
  into ignition configs before writing them to the host. The hostname is set
  via `/etc/hostname` (using the domain name `<namespace>-<cluster>-<machine>`)
  and the providerID is set via a kubelet systemd drop-in. For ignition
  (OpenShift/RHCOS), the config is delivered via QEMU `fw_cfg` — the standard
  libvirt method, no ISO needed. For cloud-init, a NoCloud ISO is created.
- **Built-in CSR approver** — CAPLV includes a CSR approver controller that
  auto-approves kubelet CSRs (both bootstrap and serving) for nodes whose
  hostname matches a CAPI Machine backed by a LibvirtMachine. No external
  machine-approver integration required.
- **Deterministic artifact naming** — domain names, disk volumes, and ISOs are
  named `<namespace>-<cluster>-<machine>`. Artifacts can be rediscovered after a
  controller crash without relying on status writes.
- **Immutable spec** — all spec fields are frozen after creation. To change VM
  configuration, delete and recreate. Enforced by admission webhook.
- **Finalizer safety** — the finalizer is never removed until cleanup is confirmed
  on the libvirt host. If the host is unreachable, the resource stays and a
  `CleanupStalled` condition is surfaced for operator intervention.

## Host security model

CAPLV connects to each libvirt host over SSH using a dedicated `caplv`
service account. The account is designed with minimal privileges:

| Operation | Privilege | Mechanism |
|-----------|-----------|-----------|
| `virsh define/start/destroy/undefine` | libvirt group | Group membership grants `qemu:///system` access — no sudo |
| `virsh vol-create-as/vol-upload/vol-delete` | libvirt group | Same — storage pool operations via libvirt API |
| `virsh nodeinfo/dominfo/version` | libvirt group | Read-only host and domain queries |
| `mkdir -p /run/caplv/*` | root | `sudo` — create tmpfs mount point |
| `mount -t tmpfs tmpfs /run/caplv/*` | root | `sudo` — mount ephemeral storage |
| `mount -t tmpfs -o size=* tmpfs /run/caplv/*` | root | `sudo` — mount with size cap (`ephemeralPoolSize`) |
| `umount /run/caplv/*` | root | `sudo` — unmount on cleanup |
| `rmdir /run/caplv/*` | root | `sudo` — remove mount point |
| `tee /run/caplv/*` | root | `sudo` — write ignition config |
| `rm -f /run/caplv/*` | root | `sudo` — delete ignition config |
| `cat > /tmp/caplv-*` | caplv | No sudo — `/tmp/` is world-writable (temp files for virsh define/vol-upload) |

All sudo rules are restricted to paths under `/run/caplv/`. The service
account cannot escalate beyond these specific commands.

Note that even in the default `/system` mode the VMs themselves do not run
as root: the system libvirt daemon launches QEMU as the unprivileged
`qemu` user. For hosts that must not run a root-owned VM management
daemon at all, see [session mode](session-mode.md),
which runs both the daemon and QEMU as the service account and confines
the remaining privilege to the setuid `qemu-bridge-helper` plus the same
`/run/caplv/` sudo rules.

## Storage pools

Storage pools appear in five places across CAPLV's CRDs. All but one are
**operator-provided**: they must already exist on the libvirt host before
a machine is scheduled, and preflight verifies each one — a missing pool
fails the machine terminally with `BaseImagePoolNotFound` /
`StoragePoolNotFound` instead of retrying. The only pool CAPLV creates
itself is the per-machine ephemeral pool.

| Pool | Who creates it | What it holds |
|------|----------------|---------------|
| `LibvirtCluster.spec.baseImage.pool` | Operator | Staging target for the cluster-wide base image: the controller uploads the fetched qcow2 here as `baseImage.volumeName`. Only referenced when `spec.baseImage` is set. |
| `LibvirtMachine.spec.rootDisk.baseImagePool` | Operator | The read-only base image (`rootDisk.baseImage`) that root disks are cloned from (`full-clone`) or backed by (`copy-on-write`). Defaults to `rootDisk.storagePool` when unset. |
| `LibvirtMachine.spec.rootDisk.storagePool` | Operator | The VM's root disk volume, plus the NoCloud ISO for cloud-init guests. **Not used as the root-disk target when `ephemeralPool: true`** — see below. |
| `LibvirtMachine.spec.additionalDisks[].storagePool` | Operator | The corresponding additional data disk volume. |
| `<namespace>-<cluster>-<machine>-pool` | **CAPLV** (only with `ephemeralPool: true`) | Everything `rootDisk.storagePool` would otherwise hold — the root-disk overlay and any bootstrap ISO — on a per-machine tmpfs mounted at `/run/caplv/<namespace>-<cluster>-<machine>`. |

The ephemeral pool is created when the machine is provisioned and torn
down — pool, tmpfs mount, and directory — when it is deleted. Its
generated name is surfaced on the machine at
`status.artifacts.ephemeralPoolName`, so a pool named after one of your
machines on a host is CAPLV's, not something an operator needs to manage.
If a libvirt daemon restart leaves the pool defined but inactive, the
controller restarts it on the next reconcile — or, when the tmpfs backing
is gone, destroys and recreates it. Because the pool is generated per
machine, `rootDisk.storagePool` is never used as the root-disk target in
ephemeral mode. One caveat: `baseImagePool` still *defaults* to the
`storagePool` value when unset, so in ephemeral mode either set
`baseImagePool` explicitly to the persistent pool holding the base image
(recommended), or make sure `storagePool` names that pool.

## Host storage layout (with ephemeralPool: true)

```
/var/lib/libvirt/images/                    (persistent pool "default")
  └── rhcos.qcow2                     ← pre-staged by operator (read-only)

/run/caplv/ns-cluster-worker01/             (tmpfs, CAPLV creates/destroys)
  ├── ns-cluster-worker01-root.qcow2       ← CoW overlay (in RAM)
  └── ignition.json                        ← ignition config (delivered via fw_cfg)

/var/lib/libvirt/qemu/nvram/
  └── ns-cluster-worker01_VARS.fd           ← UEFI NVRAM (libvirt manages)
```

The tmpfs mount, libvirt pool, and ignition file are created when the VM
is provisioned and destroyed when the VM is deleted. No permanent host
storage impact. For cloud-init guests, a NoCloud ISO replaces the
ignition file.
