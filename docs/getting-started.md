# Getting started

This guide sets up CAPLV on an OpenShift cluster and brings up a first worker VM.
For how the pieces fit together, see [Architecture](architecture.md).

## Prerequisites

- An OpenShift cluster (CAPLV runs on the same cluster that workers join)
- CAPI installed on the cluster
- libvirt hosts reachable over SSH from the cluster network
- RHCOS qcow2 base images pre-staged in libvirt storage pools on each host
- OVMF firmware packages installed on libvirt hosts (for UEFI boot)

## Cluster setup

Before creating any ScheduledMachine resources, the OpenShift cluster
needs these one-time prerequisites:

**1. Install CAPI, 5-Spot, CAPLV, and
[capi-bootstrap-ignition](https://github.com/atgreen/capi-bootstrap-ignition)**
on the OpenShift cluster. The bootstrap provider bridges OpenShift's
ignition-based worker bootstrap with the CAPI bootstrap contract.

**2. Create a CAPI Cluster resource** pointing to itself:

```yaml
apiVersion: cluster.x-k8s.io/v1beta2
kind: Cluster
metadata:
  name: my-ocp-cluster
  namespace: default
spec:
  controlPlaneEndpoint:
    host: "api.my-ocp-cluster.example.com"
    port: 6443
  infrastructureRef:
    apiGroup: infrastructure.cluster.x-k8s.io
    kind: LibvirtCluster
    name: my-ocp-cluster
```

**3. Create a LibvirtCluster resource** (CAPLV reports ready once the endpoint
host and port are configured):

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtCluster
metadata:
  name: my-ocp-cluster
  namespace: default
spec:
  controlPlaneEndpoint:
    host: "api.my-ocp-cluster.example.com"
    port: 6443
```

**4. Register libvirt hosts** (one resource per physical host):

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtHost
metadata:
  name: rhel-host-01
spec:
  uri: "qemu+ssh://caplv@rhel-host-01.example.com/system"
  secretRef:
    name: rhel-host-01-ssh-key
  hostKeyFingerprint: "SHA256:abc123..."
  reservedResources:             # leave this much for the incumbent workload
    vcpus: 2                     # default: 2
    memoryMB: 4096               # default: 4096
  healthCheckIntervalSeconds: 300  # default: 300 (5 min), only while machines exist
```

The controller discovers total host capacity via `virsh nodeinfo` and
reports available resources (total minus reserved) in
`status.capacity.availableVCPUs` / `status.capacity.availableMemoryMB`.

The `secretRef` Secret lives in the host's namespace and holds the SSH private
key under `ssh-privatekey`:

```bash
kubectl create secret generic rhel-host-01-ssh-key \
  --from-file=ssh-privatekey=caplv_ed25519
```

If you will ever move this management cluster with `clusterctl move`, also
label the Secret `clusterctl.cluster.x-k8s.io/move=`; see
[Moving to another management cluster](configuration.md#moving-to-another-management-cluster-clusterctl-move).

To share one key across many hosts or namespaces and rotate it in one place,
reference a `LibvirtClusterIdentity` with `identityRef` instead; see
[Shared SSH credentials](configuration.md#shared-ssh-credentials-libvirtclusteridentity).

**5. Create a service account on each host** with minimal privileges:

```bash
# Create the caplv user with libvirt group membership.
useradd -r -s /bin/bash -G libvirt caplv
mkdir -p /home/caplv/.ssh
# Deploy the SSH public key matching the Kubernetes Secret.
cat > /home/caplv/.ssh/authorized_keys <<< "<public-key>"
chmod 700 /home/caplv/.ssh
chmod 600 /home/caplv/.ssh/authorized_keys
chown -R caplv:caplv /home/caplv/.ssh
```

Grant restricted sudo for tmpfs and file operations only:

```
# /etc/sudoers.d/caplv
caplv ALL=(root) NOPASSWD: /usr/bin/mkdir -p /run/caplv/*
caplv ALL=(root) NOPASSWD: /usr/bin/mount -t tmpfs tmpfs /run/caplv/*
caplv ALL=(root) NOPASSWD: /usr/bin/mount -t tmpfs -o size=* tmpfs /run/caplv/*
caplv ALL=(root) NOPASSWD: /usr/bin/umount /run/caplv/*
caplv ALL=(root) NOPASSWD: /usr/bin/rmdir /run/caplv/*
caplv ALL=(root) NOPASSWD: /usr/bin/tee /run/caplv/*
caplv ALL=(root) NOPASSWD: /usr/bin/rm -f /run/caplv/*
```

The `libvirt` group grants access to `virsh` commands against
`qemu:///system` without sudo. Only tmpfs mount/unmount and file writes
under `/run/caplv/` require elevated privileges.

The account name is arbitrary — the ansible playbook parameterizes it as
`caplv_user` (`-e caplv_user=<name>`), and the controller uses whatever
user appears in the `LibvirtHost` URI.

To run VMs under the service account's unprivileged per-user libvirt
daemon instead of the system daemon, see [Session mode](session-mode.md).

**6. Pre-stage RHCOS base images** on each libvirt host in the persistent
storage pool (e.g., `/var/lib/libvirt/images/rhcos.qcow2`). The RHCOS
version should match the OpenShift cluster version (e.g., use the 4.21
image for an OCP 4.21 cluster). Download from
`https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos/<version>/latest/`.

**7. Deploy a MachineHealthCheck** (recommended — see
[Troubleshooting](troubleshooting.md#machinehealthcheck-recommended)).

## Create a worker directly

First, fetch the worker ignition from the Machine Config Server on a
cluster node and create a bootstrap secret:

```bash
ssh core@<node-ip> "sudo curl -sk \
  -H 'Accept: application/vnd.coreos.ignition+json;version=3.2.0' \
  https://localhost:22623/config/worker" > worker-ignition.json

kubectl create secret generic worker-bootstrap -n default \
  --from-file=value=worker-ignition.json \
  --from-literal=format=ignition
```

Then create a Machine + LibvirtMachine:

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtMachine
metadata:
  name: worker01
  namespace: default
spec:
  hostRef:
    name: rhel-host-01
  domain:
    vcpus: 4
    memoryMB: 8192
  network:
    type: "bridge"
    name: "br0"
    addresses:
      - "192.168.1.50/24"
    gateway: "192.168.1.1"
    dns:
      nameservers:
        - "192.168.1.1"
  rootDisk:
    baseImage: "rhcos.qcow2"
    baseImagePool: "default"
    storagePool: "default"
    size: "100Gi"
  bootstrapFormat: "ignition"
---
apiVersion: cluster.x-k8s.io/v1beta2
kind: Machine
metadata:
  name: worker01
  namespace: default
  labels:
    cluster.x-k8s.io/cluster-name: my-ocp-cluster
spec:
  clusterName: my-ocp-cluster
  bootstrap:
    dataSecretName: worker-bootstrap
  infrastructureRef:
    apiGroup: infrastructure.cluster.x-k8s.io
    kind: LibvirtMachine
    name: worker01
```

CAPLV will automatically:
1. Inject hostname (`default-my-ocp-cluster-worker01`) and providerID
2. Provision the VM on the libvirt host
3. Auto-approve the kubelet CSR
4. The node joins the cluster as a worker

To delete, just `kubectl delete machine worker01` — CAPLV destroys the
VM, cleans up storage, and removes the Node object.

## Schedule a worker via 5-Spot

```yaml
apiVersion: 5spot.finos.org/v1alpha1
kind: ScheduledMachine
metadata:
  name: weekday-worker-01
spec:
  clusterName: my-ocp-cluster
  schedule:
    daysOfWeek: ["mon-fri"]
    hoursOfDay: ["9-17"]
    timezone: "America/Toronto"
    enabled: true
  infrastructureSpec:
    apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
    kind: LibvirtMachine
    spec:
      hostRef:
        name: rhel-host-01
      domain: {}                   # auto-sized from host capacity minus reserved
      rootDisk:
        size: "100Gi"
        storagePool: "vm-disks"
        baseImagePool: "default"
        baseImage: "rhcos.qcow2"
        ephemeralPool: true
      network:
        type: "bridge"
        name: "br0"
        addresses:
          - "192.168.1.50/24"
        gateway: "192.168.1.1"
        dns:
          nameservers:
            - "192.168.1.1"
      bootstrapFormat: "ignition"
```

## Using with 5-Spot ScheduledMachines

When using [5-Spot](https://github.com/finos/5-spot) to schedule workers,
install the [capi-bootstrap-ignition](https://github.com/atgreen/capi-bootstrap-ignition)
provider. This satisfies the CAPI bootstrap contract so 5-Spot's existing
`bootstrapSpec` flow works without modification:

```yaml
apiVersion: 5spot.finos.org/v1alpha1
kind: ScheduledMachine
metadata:
  name: spot-worker-01
spec:
  clusterName: my-ocp-cluster
  schedule:
    daysOfWeek: ["mon-fri"]
    hoursOfDay: ["9-17"]
    timezone: "America/New_York"
    enabled: true
  bootstrapSpec:
    apiVersion: bootstrap.cluster.x-k8s.io/v1alpha1
    kind: IgnitionConfig
    spec:
      secretRef:
        name: worker-ignition
  infrastructureSpec:
    apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
    kind: LibvirtMachine
    spec:
      hostRef:
        name: rhel-host-01
      # ... VM configuration
```

The same `worker-ignition` Secret serves all ScheduledMachines in the cluster.

## Next steps

- [Configuration](configuration.md) covers ephemeral storage, node labels,
  direct kernel boot and cluster-wide base images.
- [Cluster templates](cluster-templates.md) generates clusters with
  `clusterctl generate cluster`.
- The [API reference](api-reference/v1alpha1.md) lists every field.
