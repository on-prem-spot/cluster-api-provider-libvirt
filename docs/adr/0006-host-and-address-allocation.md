# Host and address allocation

Status: accepted for phase 1 (host selection and host-provided addresses),
which is implemented; phase 2 (IP pools) remains proposed. Decides the host
and address item of ADR 0003 step 3.

## Context

Every `LibvirtMachine` names its host (`spec.hostRef`) and its static
addresses (`spec.network.addresses`), and a host runs one VM. A template
copies the same host and address into every machine it stamps out, so:

- a `MachineDeployment` or a `KubeadmControlPlane` cannot run more than one
  replica;
- rollouts that surge (every `KubeadmControlPlane` rollout, and any
  `MachineDeployment` with `maxSurge` above 0) collide on the host and stall;
- the `libvirt-kubeadm` ClusterClass is limited to one control plane machine
  and one worker.

Two official providers solved the same problems. CAPM3 (Metal3) consumes
bare-metal hosts one-to-one: a machine names a host selector, the controller
claims a free matching host by setting the host's `consumerRef`, and the claim
is the record of which machine owns which host. CAPV takes addresses from IP
pools through the CAPI IPAM contract (`addressesFromPools`, creating an
`IPAddressClaim` per address).

CAPLV's model sits between the two. Like Metal3 hosts, libvirt hosts are a
fixed inventory consumed one VM at a time. Unlike a vSphere VM, a CAPLV VM's
network is usually tied to its host: each host has its own bridge, often on
its own subnet, so an address is only valid on the hosts that can reach it.

CAPI already reports each `LibvirtHost` as a failure domain, and
`KubeadmControlPlane` spreads control plane machines across failure domains by
setting `Machine.spec.failureDomain`. A `MachineDeployment` gives all its
machines the same failure domain, so worker placement has to come from CAPLV.

## Decision

### Hosts: claim a free host from a selector

- `LibvirtMachine.spec.hostRef` becomes optional, and a new
  `spec.hostSelector` (a `metav1.LabelSelector` over `LibvirtHost`s in the
  machine's namespace) is added. Exactly one of them must be set. `hostRef`
  pins a machine to a host, as today; `hostSelector: {}` selects every host.
- A host is claimed by setting `LibvirtHost.spec.consumerRef` to the machine,
  with an optimistic-concurrency update, so two machines racing for one host
  cannot both win; the loser picks again. The claim replaces the webhook's
  namespace scan as what enforces one VM per host. A pinned machine claims
  its host the same way.
- A host is eligible when it matches the selector, is `Ready`, has no
  consumer, is not being deleted, and has the capacity the machine asks for.
  When `Machine.spec.failureDomain` is set, only that host is eligible, so
  `KubeadmControlPlane`'s spreading carries over unchanged. Among eligible
  hosts the controller takes the first by name, so placement is predictable.
- The claim is the source of truth. The machine also reports its host in
  `status.hostRef`, which the controller can rebuild by finding the host whose
  `consumerRef` names it, so the record survives `clusterctl move` (both
  objects move) under the move contract's rule that status be rebuildable.
- The claim is released after the domain and its artifacts are deleted,
  so a host is never reused while the old VM might still exist.
- With no eligible host, the machine reports `Ready=False` with reason
  `NoHostAvailable` and retries; it does not fail terminally, since a host may
  be freed or added.

### Addresses: from the host, or from an IP pool

`spec.network.addresses` stays and becomes optional. When it is empty, the
address comes from one of:

1. **The host.** A new `LibvirtHost.spec.machineNetwork` gives the network
   settings for the VM that host runs: addresses, gateway, DNS, and the bridge
   or libvirt network to attach to. Since a host runs one VM, the VM's address
   can be a property of the host, which keeps the address valid for the host's
   network by construction. This is the default for host-selected machines.
2. **An IP pool** (phase 2). `spec.network.addressesFromPools` names pools
   through the CAPI IPAM contract (`ipam.cluster.x-k8s.io/v1beta2`): CAPLV
   creates an `IPAddressClaim` per pool, owned by the machine, waits for the
   `IPAddress`, and uses its address, prefix and gateway. The claims are
   garbage-collected with the machine, releasing the addresses. This suits
   hosts that share one network, and it is how CAPV does it.

Settings on the machine take precedence over the host's, field by field, so
a template can set, say, DNS for every machine while each host supplies its
own address.

Where an address came from, CAPLV records it in `status.addresses`, which the
CSR approver then uses in place of `spec.network.addresses`.

### Rollout

- Phase 1: `hostSelector` and `consumerRef` claiming, and
  `LibvirtHost.spec.machineNetwork`. Templates and the ClusterClass take a
  selector instead of a host and address, the replica limit is lifted, and
  surge rollouts work whenever a spare host exists. The e2e suite registers
  three hosts and runs two workers from one MachineDeployment.
- Phase 2: `addressesFromPools`, with the in-cluster IPAM provider in the
  e2e suite.

Both are additive on `v1alpha1`: existing machines with `hostRef` and
explicit addresses behave exactly as now. They become part of `v1beta2`.

## Alternatives

- **Let CAPI choose through failure domains alone.** It covers the control
  plane, but a `MachineDeployment` puts every worker in one failure domain.
- **Record the choice in `LibvirtMachine.spec.hostRef`**, written by the
  controller. A claim on the host is still needed to stop two machines taking
  one host, so this would keep two records, and the controller would be
  editing a field users and templates also write.
- **Addresses from IP pools only.** A pool does not know which hosts can
  reach which addresses, so with hosts on different subnets it would hand out
  addresses that do not work on the host chosen. Pools stay, as the option for
  hosts on one network.
- **Several VMs per host.** Out of scope; ADR 0001 keeps one VM per host for
  simple ownership, capacity and cleanup.

## Consequences

- `LibvirtHost` gains `spec.consumerRef` and `spec.machineNetwork`. The
  manager needs `update` on `libvirthosts`, which it already has.
- A host can be freed by deleting its machine; an operator who removes a
  `consumerRef` by hand is reassigning a host that may still run a VM.
- Grouped failure domains (a rack or site holding several hosts) would let
  `KubeadmControlPlane` spread across racks rather than hosts. They can follow
  as a host label without changing this design.
- The `topology` flavor's `controlPlaneMachine` and `workerMachine` variables
  are replaced by selectors, which is a breaking change to the ClusterClass
  but not to the API. The flavor no longer creates `LibvirtHost`s: hosts are
  inventory shared by clusters and are registered beforehand.
- With the host chosen at runtime, the kubelet's `--provider-id` cannot be
  written into a template. CAPLV adds it to the NoCloud meta-data as
  `provider_id`, which bootstrap data reads as
  `{{ ds.meta_data.provider_id }}`.
- KubeadmControlPlane may hand a control plane machine any failure domain
  marked control plane eligible, and the machine then takes only that host.
  With hosts split by role, a worker host offered to the control plane would
  leave the machine waiting, so `LibvirtCluster.spec.controlPlaneHostSelector`
  limits eligibility to the control plane hosts. The e2e suite found this:
  the first runs passed only because KubeadmControlPlane happened to choose
  the control plane host.
- Claims left by a force-deleted machine are not cleared automatically. A
  cleanup that found no machine named by a claim would also fire during
  `clusterctl move`, when a host can reach the target before its machine,
  and the machine would then claim a second host while its VM still runs on
  the first.
