# Configuration

Optional features and manager settings.

## Custom resources

| CRD | Purpose |
|-----|---------|
| **LibvirtHost** | Reusable connection details for a libvirt hypervisor (URI, SSH key, OVMF paths, reserved resources). Discovers host capacity via `virsh nodeinfo`. Health-checked only while machines are active. |
| **LibvirtCluster** | CAPI contract resource — reports ready when the control plane endpoint is configured. No infrastructure provisioned. |
| **LibvirtMachine** | Core resource — a single KVM VM with static IP, UEFI firmware, and ignition or cloud-init bootstrap. VM size can be auto-derived from host capacity. One per host (webhook-enforced). |
| **LibvirtClusterIdentity** | Cluster-scoped SSH credential that LibvirtHosts in the namespaces it allows reference instead of their own Secret, so one key can be shared and rotated in one place. |
| **LibvirtMachineTemplate** / **LibvirtClusterTemplate** | Inert templates that MachineDeployments, control plane providers and ClusterClass stamp LibvirtMachines / LibvirtClusters from. |

The [API reference](api-reference/v1alpha1.md) lists every field, default
and validation rule. It is generated from `api/v1alpha1` with `make api-docs`.

## Ephemeral storage with tmpfs

VMs are ephemeral — created and destroyed on demand by 5-Spot schedules.
To avoid touching persistent storage on hosts with incumbent workloads,
set `ephemeralPool: true`:

```yaml
rootDisk:
  storagePool: "vm-disks"        # ignored in this mode — CAPLV provisions its own tmpfs pool
  baseImagePool: "default"       # persistent pool with pre-staged base image
  baseImage: "rhcos.qcow2"
  ephemeralPool: true
  ephemeralPoolSize: "80%"       # optional cap on tmpfs RAM (default: kernel's 50%)
```

**No host setup required.** CAPLV creates a per-machine tmpfs mount at
`/run/caplv/<namespace>-<cluster>-<machine>` and a libvirt storage pool
named `<namespace>-<cluster>-<machine>-pool` when the VM is provisioned,
and tears both down when the VM is deleted. All per-VM artifacts (the
root disk and any bootstrap ISO) land in this generated pool — the
`storagePool` field is not used as the disk target in this mode (see
[Storage Pools](architecture.md#storage-pools)). RAM is only consumed while the VM
exists. The host's persistent storage is never touched.

`ephemeralPoolSize` accepts tmpfs `size=` syntax: a percentage of physical
RAM (`"80%"`) or an absolute size (`"16G"`). When unset, the kernel's
default applies (50% of physical RAM per mount).

## Node labels and annotations

`LibvirtMachine.spec.nodeLabels` and `LibvirtMachine.spec.nodeAnnotations`
apply arbitrary labels and annotations to the Kubernetes `Node` object
that backs the VM, once kubelet has registered it with the cluster:

```yaml
spec:
  # ...existing fields...
  nodeLabels:
    node-role.kubernetes.io/app: ""
    k8s.ovn.org/egress-assignable: ""
    dynatrace: "true"
    aqua: "true"
  nodeAnnotations:
    example.com/owner: "platform-team"
```

Unlike the existing CAPI `Machine.spec.nodeLabels` field (and `kubelet
--node-labels` / `K0sWorkerConfig.spec.args` / ignition kubelet drop-ins),
this is **not subject to the NodeRestriction admission allow-list**.
NodeRestriction polices labels self-set by kubelet (`system:nodes:<name>`)
and CAPI mirrors the same allow-list on its own `nodeLabels` field. CAPLV
instead patches the `Node` from the controller's own identity after
kubelet registration, so arbitrary keys like `dynatrace` or
`k8s.ovn.org/egress-assignable` are accepted.

**Ownership.** CAPLV owns only the keys it has applied. It tracks them
via two annotations it writes onto the `Node` itself:

- `infrastructure.cluster.x-k8s.io/libvirt-managed-labels`
- `infrastructure.cluster.x-k8s.io/libvirt-managed-annotations`

On each reconcile, keys that have disappeared from spec are removed from
the `Node`; admin-applied keys CAPLV never set are left untouched. The
result of the patch is visible on
`status.conditions[?(@.type=="NodeLabelled")]`:

| Status | Reason          | Meaning                                                |
| ------ | --------------- | ------------------------------------------------------ |
| True   | `NodeLabelled`  | All declared keys are present on the Node              |
| False  | `NodeNotJoined` | Waiting for kubelet to register the Node (retried 15s) |

The condition is set only when at least one of `nodeLabels` /
`nodeAnnotations` is non-empty; clearing both removes the condition. Node
labelling does **not** block `status.ready` — infrastructure is reported
ready as soon as the domain is running, and the patch is applied as a
follow-on step.

When using 5-Spot, put the fields inside `infrastructureSpec.spec` and
they pass through to the underlying `LibvirtMachine`:

```yaml
apiVersion: 5spot.finos.org/v1alpha1
kind: ScheduledMachine
spec:
  # ...
  infrastructureSpec:
    apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
    kind: LibvirtMachine
    spec:
      # ...VM config...
      nodeLabels:
        k8s.ovn.org/egress-assignable: ""
        dynatrace: "true"
```

## Fast first boot with `bootArtifacts` (direct kernel boot)

The default first-boot path delivers ignition via QEMU `fw_cfg`. That works
on every OS image but has a known kernel issue: `qemu_fw_cfg` does O(n²)
offset reads, which adds tens of seconds of wall-clock time for multi-MB
ignition payloads (see [kernel bug #218394](https://bugzilla.kernel.org/show_bug.cgi?id=218394)).
Setting `LibvirtCluster.spec.bootArtifacts` switches first-boot ignition
delivery to libvirt direct-kernel-boot plus a virtio-blk ignition disk:

- The controller fetches kernel + initramfs from a configured source.
- Blobs are content-addressed on the libvirt host under `<hostPath>/<sha256>/`
  and reused across machines and reconciles.
- The domain XML is rendered with `<kernel>` / `<initrd>` / `<cmdline>` plus
  a virtio-blk disk with `serial=ignition` carrying the rendered config.

Things to know before using it:

- **The kernel boots the machine every time.** The domain always boots the
  configured kernel and initramfs, not the root disk's own. The RHCOS
  *live* (PXE) kernel and initramfs boot a live system from RAM: they need
  `coreos.live.rootfs_url` and do not use the root disk. They are therefore
  not suitable for OpenShift workers, which the machine config daemon
  updates on disk and reboots. For OpenShift, keep the Ignition payload
  small instead: see [Create a worker directly](getting-started.md#create-a-worker-directly).
- **`hostPath` must be `/var/lib/caplv` or a directory under it.** The host
  helper (`caplv-hostctl`) writes only there and under the `/run/caplv`
  tmpfs, and a VM needs its kernel at every boot. Hosts set up before the
  helper allowed `/var/lib/caplv` need `deploy/ansible/setup-host.yaml` run
  again.
- **Static addresses reach the initramfs.** For an Ignition machine with a
  static address, CAPLV appends `rd.neednet=1 ip=<address>::<gateway>:<netmask>:<hostname>:enp1s0:none nameserver=<dns>`
  to `kernelArgs`, unless `kernelArgs` already sets `ip=`. That lets
  Ignition fetch a pointer config's target on a network without DHCP.
- **The manager stages artifacts on disk.** It streams them into
  `--boot-artifacts-cache-dir`, an emptyDir in the shipped manifests,
  before copying them to each host.

Three transports are supported: **HTTPS**, **OCI** (single artifact with two
layers), and **S3** (any S3-compatible store).

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtCluster
metadata:
  name: prod
spec:
  controlPlaneEndpoint:
    host: 10.0.0.10
    port: 6443
  bootArtifacts:
    hostPath: /var/lib/caplv/boot      # cache dir on each libvirt host
    kernelArgs: |-
      ignition.platform.id=metal
      ignition.config.url=oem:/dev/disk/by-id/virtio-ignition
      console=ttyS0
    source:
      type: HTTPS                       # one of: HTTPS, OCI, S3
      https:
        kernelURL:       https://artifacts.example.com/rhcos/vmlinuz
        initramfsURL:    https://artifacts.example.com/rhcos/initramfs.img
        kernelSHA256:    <hex>          # optional integrity check
        initramfsSHA256: <hex>
```

**OCI source.** Push a single `oras`-style artifact with the kernel and
initramfs as two layers, identified by the
`org.opencontainers.image.title` annotation:

```bash
oras push ghcr.io/example/boot:v1 \
    vmlinuz:application/octet-stream \
    initramfs.img:application/octet-stream
```

```yaml
source:
  type: OCI
  oci:
    reference: ghcr.io/example/boot:v1
    # Optional layer-title overrides (defaults shown):
    # kernelLayerTitle:    vmlinuz
    # initramfsLayerTitle: initramfs.img
    # plainHTTP:            false       # for in-cluster mirrors
    # insecureSkipTLSVerify: false      # dev/self-signed only
    credentialsSecretRef:                # optional for private registries
      name: ghcr-pull-secret
    kernelSHA256:    <hex>               # optional
    initramfsSHA256: <hex>
```

The credentials secret can be either a `kubernetes.io/dockerconfigjson`
Secret (preferred) or a Secret with plain `username` / `password` keys. It
is read from the LibvirtCluster's namespace unless
`credentialsSecretRef.namespace` is set.

**S3 source.** Works with AWS S3, MinIO, and Ceph RGW:

```yaml
source:
  type: S3
  s3:
    endpoint:     s3.amazonaws.com       # host[:port]
    region:       us-east-1              # required by AWS, optional elsewhere
    bucket:       my-boot-artifacts
    kernelKey:    rhcos/vmlinuz
    initramfsKey: rhcos/initramfs.img
    # usePathStyle:           true       # required by MinIO/Ceph
    # insecure:               false      # plain HTTP endpoint
    # insecureSkipTLSVerify:  false
    credentialsSecretRef:                # optional for private buckets
      name: s3-read-creds
    kernelSHA256:    <hex>               # optional
    initramfsSHA256: <hex>
```

The S3 credentials Secret recognizes either:
- `accessKeyID` / `secretAccessKey` / `sessionToken` (preferred), or
- `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_SESSION_TOKEN`.

Leaving `credentialsSecretRef` unset means anonymous reads.

**Transport gzip.** Artifacts that arrive gzip-wrapped (detected by the
`1f 8b` magic bytes — no naming convention required) are transparently
decompressed before the sha256 is computed. So a `.gz`-wrapped kernel in
Artifactory, an OCI layer pushed with `application/gzip`, and a raw
`vmlinuz` all produce the same digest and the same on-host cache path.
The `*SHA256` fields you configure describe the *decompressed* payload —
i.e. what the kernel actually boots.

**Caching and integrity.** Optional `*SHA256` fields are verified after
download (and decompression, if applicable). Bytes are cached in-memory
per controller process (keyed by the source spec digest), and on the
libvirt host they live under `<hostPath>/<sha256>/{vmlinuz,initramfs.img}`
so multiple machines reuse the same staged files.

## Cluster-wide base image via URL (`baseImage`)

Setting `LibvirtCluster.spec.baseImage` makes the controller responsible for
distributing the cluster's root-disk qcow2 to every libvirt host. Hosts no
longer need the qcow2 pre-staged via Ansible — they just need libvirt and a
storage pool. The controller fetches the qcow2 once into its local cache
and SCPs it onto each host the first time a machine targeting that host is
scheduled; subsequent machines on the same host reuse the staged volume.

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtCluster
metadata:
  name: prod
spec:
  controlPlaneEndpoint:
    host: 10.0.0.10
    port: 6443
  baseImage:
    pool: default                       # libvirt storage pool on each host
    volumeName: rhcos-4.18.qcow2        # the libvirt volume name the controller registers
    source:
      type: HTTPS                        # one of HTTPS, OCI, S3
      https:
        url: https://artifactory.example.com/rhcos/rhcos-4.18.qcow2.gz
        sha256: <hex>                    # optional but strongly recommended for mutable URLs
        credentialsSecretRef:
          name: artifactory-creds        # optional
        # insecureSkipTLSVerify: false   # dev/self-signed only; prefer SSL_CERT_FILE (see below)
```

`LibvirtMachine.spec.rootDisk.baseImage` continues to refer to the staged
volume by name (here `rhcos-4.18.qcow2`), so existing machine manifests work
unchanged once you point them at the cluster-managed volume name.

**Transports.** `HTTPS` is the typical mirror case; `OCI` reads a
single-blob artifact (`oras push <ref> rhcos.qcow2:application/octet-stream`)
with optional `blobTitle` disambiguation; `S3` reads a single object from
any S3-compatible store. All three accept a `credentialsSecretRef` with the
same secret shapes used by `bootArtifacts`: `kubernetes.io/dockerconfigjson`
or `username`/`password` for OCI/HTTPS basic auth, and
`accessKeyID`/`secretAccessKey`/`sessionToken` (or the `AWS_*` env-var
spellings) for S3.

**Transparent gzip.** Same magic-byte sniff used by `bootArtifacts`:
`.qcow2.gz` mirrors are decompressed in-stream before the digest is
computed. The `sha256` field describes the *decompressed* qcow2.

**HTTPS trust (private CAs).** The `HTTPS` transport verifies the server
certificate against the controller container's trust store. When the mirror
is served by an internal Artifactory (or any endpoint) fronted by a private
or corporate CA, the fetch fails with `x509: certificate signed by unknown
authority`. Rather than rebuild the controller image, mount the CA bundle
and point Go's `SSL_CERT_FILE` at it — the controller's HTTPS client honors
it automatically:

```bash
# CA certs are not secret, so a ConfigMap is fine.
oc -n <controller-namespace> create configmap caplv-ca \
  --from-file=ca-bundle.crt=/path/to/ca-bundle.pem
```

Add the mount and env var to the manager Deployment (works with the shipped
`readOnlyRootFilesystem: true` security context, since the bundle is a
read-only volume rather than a write into `/etc/pki`):

```yaml
        env:
        - name: SSL_CERT_FILE
          value: /etc/pki/caplv/ca-bundle.crt
        volumeMounts:
        - name: caplv-ca
          mountPath: /etc/pki/caplv
          readOnly: true
      volumes:
      - name: caplv-ca
        configMap:
          name: caplv-ca
```

`SSL_CERT_FILE` *replaces* the system bundle rather than adding to it. If the
controller also fetches from public-CA endpoints, concatenate your CA with
the system bundle into the ConfigMap (`cat ca.pem /etc/pki/tls/certs/ca-bundle.crt`),
or use `SSL_CERT_DIR` to point at a directory of trusted certs instead. On
OpenShift you can also have the cluster populate the bundle for you by
creating an empty ConfigMap labeled
`config.openshift.io/inject-trusted-cabundle: "true"` and mounting it the
same way — useful when the CA is already in the cluster's proxy/additional
trust bundle.

As a last resort for development or self-signed endpoints, the `HTTPS` source
also accepts `insecureSkipTLSVerify: true` (matching the `OCI` and `S3`
transports), which disables certificate verification entirely. Prefer the
`SSL_CERT_FILE` approach above for anything production — it keeps verification
on.

**Cache.** The controller stores fetched payloads under
`--base-image-cache-dir` (default `/var/cache/caplv/baseimages`), mounted as
an `emptyDir` in the shipped manager Deployment. Files are content-addressed
by sha256; concurrent fetches for the same artifact are coalesced via
`singleflight`, and concurrent uploads of the same artifact to the same
host are coalesced per-`(host, sha256)`. A controller restart re-downloads
on first use after the restart.

**Status.** The first machine on a fresh host blocks while the qcow2 is
SCP'd over and registered with libvirt — for a ~1 GB image this is minutes,
not seconds. Watch
`LibvirtMachine.status.conditions[?(@.type=="BaseImageStaged")]`:

| Status | Reason             | Meaning                                                 |
| ------ | ------------------ | ------------------------------------------------------- |
| False  | `BaseImageStaging` | qcow2 is being uploaded to this host                    |
| True   | `BaseImageStaged`  | Volume present in `spec.baseImage.pool`, ready to clone |

Subsequent machines on the same host hit the volume-already-present fast
path and don't surface the condition at all.

## Shared SSH credentials (`LibvirtClusterIdentity`)

By default each `LibvirtHost` names a Secret in its own namespace with
`spec.secretRef`. When many hosts, possibly in several namespaces, use the same
service-account key, a cluster-scoped `LibvirtClusterIdentity` holds it once:

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtClusterIdentity
metadata:
  name: caplv-fleet
spec:
  secretName: caplv-fleet-ssh   # Secret in the controller's namespace
  allowedNamespaces:
    list: [team-a]
    selector:
      matchLabels:
        caplv.example.com/hosts: "true"
---
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtHost
metadata:
  name: rhel-host-01
  namespace: team-a
spec:
  uri: "qemu+ssh://caplv@rhel-host-01.example.com/system"
  identityRef:
    name: caplv-fleet           # instead of secretRef
  hostKeyFingerprint: "SHA256:abc123..."
```

Create the Secret in the namespace the controller runs in (`caplv-system` for
the shipped manifests), with the private key under `ssh-privatekey`:

```bash
kubectl -n caplv-system create secret generic caplv-fleet-ssh \
  --from-file=ssh-privatekey=caplv_ed25519
```

- **Exactly one** of `secretRef` and `identityRef` must be set on a host.
- **`allowedNamespaces`** decides which namespaces' hosts may use the identity.
  A namespace is allowed if it is in `list` or its labels match `selector`.
  `allowedNamespaces: {}` allows every namespace; leaving it unset allows
  none. Anyone who can create a `LibvirtHost` in an allowed namespace can
  connect, as the identity's account, to any host that trusts its key, so
  scope it as tightly as the key's reach.
- **The Secret stays in the controller's namespace.** Host authors use the
  key without being able to read it, which `secretRef` cannot offer: its
  Secret has to sit next to the host.
- **Rotation.** The controller reads the Secret on every connection. Replace
  the key in the Secret, or point `secretName` at a new Secret, and every host
  using the identity picks it up on its next reconcile. Hosts referencing an
  identity are rechecked whenever the identity changes.
- **Failures show on the host.** A missing identity, a namespace the identity
  does not allow, or a missing Secret marks the host `Ready=False` with reason
  `ConnectionFailed` and a message naming the cause. The admission webhook also
  warns about the first two when the host is created or updated.

The controller finds its namespace from the `POD_NAMESPACE` environment
variable, which the shipped Deployment sets; override it with
`--identity-namespace`. Each identity is made an owner of its Secret the
first time a host connects with it, so `clusterctl move` carries the Secret
along and deleting the identity deletes the Secret; see
[Moving to another management cluster](#moving-to-another-management-cluster-clusterctl-move).

## Moving to another management cluster (`clusterctl move`)

`clusterctl move` copies the objects describing workload clusters from one
management cluster to another, for example from a temporary kind cluster into
a permanent one. Nothing on the hypervisors changes: the VMs keep running and
the controllers on the target take over. CAPLV supports it as follows.

| Object | Moved | Removed from the source |
| --- | --- | --- |
| `LibvirtCluster`, `LibvirtMachine` | With their `Cluster` | Yes |
| `LibvirtHost` | All hosts in the moved namespace | Yes |
| `secretRef` Secrets | Only if labelled (below) | Yes |
| `LibvirtClusterIdentity` and its Secret | Always | No: other clusters may still use them |

`secretRef` Secrets are not linked to anything move follows, so label each one:

```bash
kubectl label secret rhel-host-01-ssh-key clusterctl.cluster.x-k8s.io/move=
```

A Secret left behind shows on the target as a host with `Ready=False` and a
message naming the missing Secret; copying it across fixes the host.

Identity Secrets need no label, with two exceptions: an identity no host has
connected with yet, whose Secret is not yet owned by it, and identities
whose Secrets are in a namespace other than the manager's own (set with
`--identity-namespace`), which the manager cannot write to. Label those
Secrets as above. The design is recorded in
[ADR 0005](adr/0005-clusterctl-move.md).

## Running multiple provider instances (`--watch-filter`)

Several CAPLV managers can share one management cluster, each owning a
subset of objects. Start each manager with `--watch-filter=<value>` and
label every object it should own — `Cluster`, `Machine`, `LibvirtCluster`,
`LibvirtMachine` and `LibvirtHost` — with
`cluster.x-k8s.io/watch-filter: <value>`. A manager ignores objects whose
label does not match, and its CSR approver only approves CSRs for nodes
backed by a matching `LibvirtMachine`. Without the flag, a manager
reconciles everything.

## Feature gates

New, experimental behaviour ships behind a feature gate, enabled per
manager with `--feature-gates=Name=true[,Other=false]`. Gates are declared in
`feature/feature.go`. There are none yet; features that shipped before the
framework existed, such as session mode and ephemeral storage, stay ungated.

## Choosing hosts (`hostSelector`)

A `LibvirtMachine` either pins a host with `spec.hostRef` or lets CAPLV
choose one with `spec.hostSelector`, a label selector over the
`LibvirtHost`s in its namespace; exactly one must be set. Selectors are what
let a `MachineDeployment`, a `KubeadmControlPlane` or a ClusterClass run more
than one machine from one template.

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtHost
metadata:
  name: rack1-host07
  labels:
    caplv.infrastructure.cluster.x-k8s.io/role: worker
spec:
  uri: qemu+ssh://caplv@rack1-host07.example.com/system
  secretRef:
    name: caplv-ssh
  hostKeyFingerprint: SHA256:...
  # The network for the one VM this host runs.
  machineNetwork:
    type: bridge
    name: br0
    addresses: [10.20.0.107/24]
    gateway: 10.20.0.1
    dns:
      nameservers: [10.20.0.2]
---
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtMachineTemplate
metadata:
  name: workers
spec:
  template:
    spec:
      hostSelector:
        matchLabels:
          caplv.infrastructure.cluster.x-k8s.io/role: worker
      domain: {}
      rootDisk:
        baseImage: ubuntu-2404-kube-v1.36.5.qcow2
        storagePool: default
      bootstrapFormat: cloud-init
```

How a host is chosen:

- **Eligible hosts** match the selector, are `Ready`, are not being deleted,
  have the vCPUs and memory the machine asks for, and have no
  `spec.consumerRef`. When CAPI sets the Machine's failure domain, as
  `KubeadmControlPlane` does to spread its machines, only that host is
  eligible. The first eligible host by name is taken.
- **The claim** is the host's `spec.consumerRef`, set to the machine with an
  update that fails if the host changed, so two machines cannot take the same
  host. Pinned machines claim their host the same way. The machine reports its
  host in `status.hostRef` and the `Host` column of `kubectl get libvirtm`;
  `kubectl get libvirthosts` shows each host's consumer.
- **No free host** leaves the machine waiting with `HostAssigned=False`,
  reason `NoHostAvailable`, rechecked every 30 seconds. It is not a failure:
  adding or freeing a host lets it continue.
- **Release** happens after the machine's VM and its artifacts are deleted,
  so a host is never handed on while the old VM could still exist. If a
  machine is force-deleted (its finalizer removed by hand), its claim stays;
  once you have checked the VM is gone, clear it with
  `kubectl patch libvirthost <name> --type merge -p '{"spec":{"consumerRef":null}}'`.

**Addresses from the host.** A host runs one VM, so the VM's network can be
part of the host: `spec.machineNetwork` gives its type, bridge or network
name, addresses, gateway and DNS. Each field the machine leaves unset in
`spec.network` comes from there, so templates can omit `network` entirely,
or set, say, DNS for every machine while each host supplies its own address.
A machine whose network and host together give no type, name or address
waits with `InfrastructureReady=False`, reason `NetworkIncomplete`.

IP address pools (the CAPI IPAM contract) are planned as a second source of
addresses; see [ADR 0006](adr/0006-host-and-address-allocation.md).

## Cordoning a host (`unschedulable`)

A LibvirtHost can be cordoned while its capacity belongs to something else,
the way `kubectl cordon` marks a Node:

```bash
kubectl patch libvirthost rhel-host-01 --type merge -p '{"spec":{"unschedulable":true}}'
```

While `spec.unschedulable` is true:

- **No new machines.** No machine is given the host unless it already holds
  it. A host-selected machine claims another host, and a machine pinned to
  it with `hostRef` waits with `HostAssigned=False, reason HostCordoned`.
- **No control plane placement.** The LibvirtCluster leaves the host out of
  its failure domains, so KubeadmControlPlane does not place a machine there.
- **No VM is created or started.** A VM already running keeps running. A
  VM that is stopped, or stopped later (`virsh destroy`), stays stopped:
  its LibvirtMachine reports `InfrastructureReady=False, reason
  HostCordoned` rather than CAPLV starting it again.

This makes it possible to take a host's resources back within seconds,
without waiting for Cluster API. First cordon the host, then destroy its VM
on the host itself, then delete the machine's Machine (or let a
MachineHealthCheck do it), so that a MachineDeployment replaces it on
another host. `docs/demos/postgres-ha.md` does this from a Pacemaker
resource when a PostgreSQL standby is promoted on the host.

Set `spec.unschedulable` back to false to uncordon. CAPLV retries machines
waiting for the host within 30 seconds.

## Failure domains

Each `LibvirtHost` is a failure domain. `LibvirtCluster.status.failureDomains`
lists every host in the cluster's namespace (except hosts being deleted),
all marked control-plane eligible, and CAPI copies the list to
`Cluster.status.failureDomains`. Each `LibvirtMachine` reports its host in
`status.failureDomain`, which CAPI copies to `Machine.status.failureDomain`.
Both fields use the CAPI v1beta2 contract shapes. A machine with a
`hostSelector` honours the failure domain CAPI chooses for it (see
[Choosing hosts](#choosing-hosts-hostselector)); a pinned `hostRef` does
not consult it.

Every host is control plane eligible unless
`LibvirtCluster.spec.controlPlaneHostSelector` is set, in which case only
matching hosts are. Set it to the selector your control plane machines use
when hosts are split by role: `KubeadmControlPlane` assigns each of its
machines an eligible failure domain, and a host-selected machine takes only
that host, so a worker host offered to the control plane would leave the
machine waiting. The `libvirt-kubeadm` ClusterClass sets it.
