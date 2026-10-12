# Host management and machine placement

Status: records the current implementation.

## Context and decision

CAPLV manages one VM per explicitly selected LibvirtHost. The machine supplies a
host reference and static network addresses. The host records SSH connection
information, credential references, capacity reservations, and firmware paths.
Omitting CPU or memory sizes uses available host capacity after reservations.

The controller invokes virsh over SSH. This keeps the manager a pure-Go binary
and avoids shipping native libvirt client libraries. An internal client
interface separates reconciliation from the transport.

## Alternatives and trade-offs

A direct libvirt RPC client could remove the remote command layer, but introduces
another connection and authentication path. SSH reuses host administration
infrastructure and also supports artifact transfer. It requires carefully
validated command arguments, host-key verification, and managed SSH credentials.

Packing multiple VMs on a host would improve utilization but needs resource
accounting, scheduling, and shared-host cleanup semantics. The current one-VM
rule keeps ownership and capacity handling simple at the cost of density.

## Consequences for templates

The absence of machine templates is a capability gap, not a guarantee that CAPI
higher-order resources work. Repeating a template containing one host and one
static IP conflicts with the placement model. Before promising deployments,
rolling upgrades, or ClusterClass, define host selection, IP allocation, and
replacement behavior, then add template CRDs and lifecycle tests.

## Session mode

Session mode uses an unprivileged per-user libvirt daemon. It reduces daemon
privilege requirements but needs user lingering and a configured bridge helper;
managed libvirt networks are not available through the same session driver.
System mode remains an alternative when host administration permits it.
