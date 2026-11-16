# External control plane

Status: accepted. Fixes #61.

## Context

On OpenShift, CAPLV adds workers to the cluster it runs on. That cluster's
control plane is OpenShift's own. It is not made of Cluster API Machines, and
no control plane provider manages it. The Cluster in the getting started
guide therefore had no `spec.controlPlaneRef`. In that case Cluster API
considers the control plane initialized once a control plane Machine has a
Node, which never happens, so the Cluster reports
`ControlPlaneInitialized=False` for good. An end-to-end run on OpenShift
4.22 with CAPI v1.14 showed what follows from that:

- Machines' `NodeHealthy` and `NodeReady` stay `Unknown`, so `Ready` stays
  `Unknown` and `Available` stays `False`, although the Nodes are Ready.
- MachineHealthChecks evaluate nothing: Cluster API skips every
  non-control-plane Machine while the control plane is uninitialized. The
  guide recommended one.
- The Cluster's `Available` and `ControlPlaneAvailable` stay `Unknown`.
- Cluster API does not delete a worker's Node: with no control plane Machines
  it refuses to remove what it takes for the last control plane member.
  CAPLV deleted the Node itself.
- Nothing created the `<cluster>-kubeconfig` Secret Cluster API reaches the
  cluster with. Without it, Machines never got a `nodeRef`. Users had to make
  one, and the obvious way was the installer's admin kubeconfig.

The Cluster API control plane contract covers this. A control plane object
reports `status.initialization.controlPlaneInitialized` and an `Available`
condition, which Cluster API mirrors into the Cluster.
`status.externalManagedControlPlane: true` tells Cluster API not to look for
control plane Machines. Control plane providers are also the ones that
create and maintain `<cluster>-kubeconfig`. Managed Kubernetes providers
(EKS in CAPA, AKS, GKE) use this for control planes that Cluster API does not
build either.

## Decision

CAPLV adds a `LibvirtExternalControlPlane` kind, which a Cluster names in
`spec.controlPlaneRef`. Its controller:

- **Reports the control plane.** Once the Cluster has a control plane
  endpoint (from its LibvirtCluster, as before) and the kubeconfig below is
  in place, it sets `controlPlaneInitialized`, `externalManagedControlPlane`,
  an `Available` condition, and the cluster's Kubernetes version. The
  controller runs in the cluster it reports on, so reaching it is proof
  enough that the control plane accepts requests. `controlPlaneInitialized`
  is never set back to false, as the contract requires.
- **Maintains `<cluster>-kubeconfig`** in the contract's shape (type
  `cluster.x-k8s.io/secret`, the cluster-name label, the `value` key):
  - It creates a ServiceAccount, `<cluster>-capi-workload`, in the Cluster's
    namespace, and a ClusterRoleBinding of that ServiceAccount to
    `caplv-capi-workload-access`.
  - That role holds only what Cluster API does through the kubeconfig: the
    health probe of `/`, and finding, labelling, cordoning, draining and
    deleting Nodes, with the Pod, Namespace, DaemonSet, PersistentVolume and
    VolumeAttachment reads draining needs.
  - The kubeconfig carries a token for that ServiceAccount, from the
    TokenRequest API, lasting 24 hours and refreshed once half of that has
    passed, following the contract's advice on short-lived credentials.
  - The server and CA are the ones the manager itself uses, since Cluster API
    runs in the same cluster.
- **Leaves a user's kubeconfig alone.** A `<cluster>-kubeconfig` Secret that
  CAPLV did not create (it is not controlled by the control plane object) is
  used as it is. That is the way to point Cluster API elsewhere.
- **Cleans up.** The ServiceAccount and Secret are owned by the control plane
  object and garbage collected with it. The ClusterRoleBinding, being
  cluster-scoped, is removed by a finalizer.

CAPLV's manager may create ClusterRoleBindings but binds only this one role:
its `bind` permission names `caplv-capi-workload-access` and nothing else.
So it cannot hand out permissions it was not given.

The default, direct-boot, session-mode and ephemeral cluster templates, and
the getting started guide, add the object and the reference. The topology
flavor builds its own control plane with KubeadmControlPlane and is
unchanged.

## Alternatives

- **Document the limits and leave it.** This was the interim state. Machines
  run, but health checking, remediation and the Cluster's own status never
  work, and every user hand-makes a kubeconfig, usually an admin one.
- **Put the admin kubeconfig in the Secret.** It works, but it hands Cluster
  API cluster-admin through a Secret that does not expire.
- **A long-lived ServiceAccount token Secret.** It is simpler than
  TokenRequest, but it never expires, which the contract advises against.
- **Report the control plane from the LibvirtCluster.** The infrastructure
  cluster contract has no field for it. The Cluster derives
  `ControlPlaneInitialized` only from a control plane object or from control
  plane Machines.

## Consequences

- The manager gains RBAC: create, update and delete ClusterRoleBindings; bind
  `caplv-capi-workload-access`; create ServiceAccounts, their tokens and
  Secrets. Cluster API's aggregated role gains the new kind.
- A cluster moved with `clusterctl move` to another management cluster would
  get a kubeconfig for the new management cluster, which is not the
  OpenShift cluster its workers join. The kind assumes CAPLV runs in the
  cluster it adds workers to, as the getting started guide does. To manage
  that cluster from elsewhere, provide `<cluster>-kubeconfig` yourself.
- With `externalManagedControlPlane`, Cluster API deletes a worker's Node when
  its Machine is deleted, and CAPLV's own Node deletion (which checks the
  provider ID) usually finds it gone.
- ClusterClass support would need a `LibvirtExternalControlPlaneTemplate`. No
  ClusterClass uses this kind yet.
