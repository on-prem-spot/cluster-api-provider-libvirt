# clusterctl move

Status: accepted. Completes the `clusterctl move` item left open by ADR 0004.

## Context

`clusterctl move` copies the Cluster API objects describing workload clusters
from one management cluster to another, most often from a temporary kind
cluster into a permanent one. It moves nothing on the hypervisors: the VMs
keep running and the controllers on the target take over managing them.

Move copies an object only if it is linked to a `Cluster` by owner references
or its CRD (or the object itself) carries a move label. CAPI already makes
each `Cluster` own its `LibvirtCluster` and each `Machine` own its
`LibvirtMachine`, and the provider honours `Cluster.spec.paused` and keeps
nothing in `status` it cannot rebuild, so those moved correctly. Nothing
linked the rest:

- `LibvirtHost`s are inventory shared by every cluster in a namespace, so no
  `Cluster` owns them. Left behind, moved `LibvirtMachine`s name hosts the
  target does not have and cannot be reconciled or deleted.
- `secretRef` Secrets belong to hosts, not clusters.
- `LibvirtClusterIdentity` objects are cluster-scoped, and their Secrets are in
  the controller's namespace.

Supporting move is optional for a provider, and most CAPLV installations are
expected to run in one long-lived management cluster. It is supported because
the cost is small.

## Decision

- The `LibvirtHost` CRD carries `clusterctl.cluster.x-k8s.io/move`, so every
  host in the moved namespace moves.
- The `LibvirtClusterIdentity` CRD carries
  `clusterctl.cluster.x-k8s.io/move-hierarchy`, so every identity moves with
  the objects it owns. Because identities are cluster-scoped, clusterctl
  treats them and what they own as global: it copies them, keeps an existing
  copy on the target, and does not delete them from the source, where other
  clusters may still use them.
- The controller makes each identity an owner of its Secret when it first
  resolves the identity. The reference is not a controller reference, so one
  Secret can serve several identities. This needs `patch` on Secrets, granted
  by a Role in the controller's namespace only.
- Users label `secretRef` Secrets `clusterctl.cluster.x-k8s.io/move` themselves.

`test/move` runs clusterctl's own move code between two envtest API servers,
with the CRDs from the kustomize build, and checks where each object ends up.

### Alternatives

- **Hosts own their `secretRef` Secrets.** Move would follow the reference,
  but garbage collection would also delete a user's Secret once the last
  host using it was deleted.
- **The controller labels `secretRef` Secrets.** Those Secrets are in any
  namespace, so this needs cluster-wide Secret write access, a lot of
  privilege for an optional feature. A forgotten label is easy to recover
  from: the host on the target reports the missing Secret, and copying it
  across fixes it.
- **An identity controller** that sets the owner reference, as CAPV has. The
  host and machine controllers already resolve identities, so a separate
  controller would add a reconciler and nothing else.

## Consequences

- Deleting an identity now garbage-collects its Secret, as in CAPV.
- The owner reference is set when a controller first connects to a host with
  the identity. An identity no host has used yet, at the time of a move, is
  copied without its Secret; the Secret has to be copied by hand.
- With `--identity-namespace` set to a namespace other than the controller's
  own, the Role does not cover it: the owner reference is not set, which is
  logged, and identity Secrets there must be labelled by hand like
  `secretRef` Secrets.
- `test/move` adds clusterctl's library and its dependencies to `go.mod`.
  They are test-only and not linked into the manager.
