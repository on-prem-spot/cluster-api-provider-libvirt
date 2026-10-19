# CAPLV — *EXPERIMENTAL* Cluster API Provider for LibVirt

CAPLV is an *EXPERIMENTAL* [Cluster API](https://cluster-api.sigs.k8s.io/)
infrastructure provider that provisions KVM virtual machines on libvirt
hosts. It works standalone as a plain CAPI infrastructure provider —
create `LibvirtMachine` and `Machine` resources directly to bring up
workers — and pairs naturally with
[5-Spot](https://github.com/finos/5-spot), which schedules OpenShift
worker nodes on and off physical RHEL/KVM infrastructure based on
time-of-day rules.

The target hosts are machines with incumbent workloads — DR standby
systems or servers with predictable load schedules that have idle
capacity during off-peak periods. CAPLV is designed to be minimally
disruptive to these hosts: worker-node VMs are fully ephemeral and,
when backed by a tmpfs storage pool, won't touch persistent storage
on the device at all.

## How It Works

```
5-Spot: schedule becomes active
  → Creates IgnitionConfig + LibvirtMachine + CAPI Machine
    → capi-bootstrap-ignition copies ignition data, reports ready
      → CAPLV injects hostname + providerID into ignition config
        → Connects to libvirt host over SSH
          → Clones RHCOS base image, writes ignition config
            → Defines and starts KVM domain
              → VM boots, kubelet starts, CSR auto-approved
                → Node joins this OpenShift cluster as worker

5-Spot: schedule becomes inactive
  → Deletes CAPI Machine
    → CAPI drains pods
      → CAPLV destroys domain, cleans up all artifacts, deletes Node object
```

CAPLV runs on the same OpenShift cluster that the worker VMs join.

Each libvirt host runs exactly one CAPLV-managed VM — enforced by an
admission webhook that rejects a second machine targeting the same host.
Hundreds or thousands of hosts can come online simultaneously — the
controller runs up to 50 concurrent reconcilers by default
(`--max-concurrent-reconciles`), each operating against a different host
over its own SSH connection.

## Cluster Setup

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
    apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
    kind: LibvirtCluster
    name: my-ocp-cluster
    namespace: default
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

### Session mode (unprivileged libvirt)

By default VMs are managed by the root-owned system libvirt daemon
(`/system` in the URI). Hosts that must not run a privileged VM manager
can instead use the service account's per-user daemon by ending the URI
with `/session`:

```yaml
spec:
  uri: "qemu+ssh://caplv@rhel-host-01.example.com/session"
```

In session mode QEMU runs as the service account itself. Because an
unprivileged process cannot create tap devices, bridge attachment goes
through QEMU's setuid `qemu-bridge-helper`, which only attaches to
bridges whitelisted in `/etc/qemu/bridge.conf`. The ansible playbook
configures all of this:

```bash
ansible-playbook -i <host>, -u root deploy/ansible/setup-host.yaml \
  -e libvirt_mode=session \
  -e '{"session_allowed_bridges": ["br0"]}'
```

which additionally: adds the service account to the `kvm` group (direct
`/dev/kvm` access), enables `loginctl` lingering (so VMs survive SSH
disconnect and the user daemon can autostart), makes `qemu-bridge-helper`
setuid, whitelists the bridges, and creates the storage pool under
`~caplv/.local/share/libvirt/images` in the user session.

The `LibvirtHost` health probe verifies session hosts end to end: on top
of the usual connectivity and KVM checks (which run against the session
daemon), it confirms `qemu-bridge-helper` is setuid (or has
`cap_net_admin`) and that lingering is enabled, marking the host
`Ready=false` with reason `SessionModeMisconfigured` otherwise.

On session hosts, UEFI machines get no explicit NVRAM path in their
domain XML: the system daemon's `/var/lib/libvirt/qemu/nvram/` is
root-owned and unwritable for the per-user daemon, so libvirt instead
allocates each machine's NVRAM file in its own directory
(`~caplv/.config/libvirt/qemu/nvram/`), initialized from the host's
`spec.nvramTemplatePath` and removed at undefine. Note this lives in the
service account's home — if that home is on NFS, see the caveat below.

Session mode limitations:

- Machines must use `network.type: bridge`. Libvirt-managed NAT networks
  (`network.type: network`) require the system daemon. The machine
  controller rejects this combination up front (terminal
  `NetworkTypeUnsupported` condition).
- `qemu-bridge-helper` does not support multi-queue virtio-net.
- On SELinux-enforcing hosts, sVirt isolation between guests is reduced
  (session guests run unconfined as the service account). If a guest
  image fails to open, check file contexts under the session pool path.
- The session daemon keeps its state — domain definitions, logs, and the
  per-machine UEFI NVRAM files QEMU holds open for write — under the
  service account's `~/.config` and `~/.cache`. If the account's home is
  on NFS, QEMU's image locking needs working byte-range locks (fine on
  NFSv4; NFSv3 needs a functioning lock manager) and an NFS outage can
  I/O-error running guests. Prefer a local home for the service account,
  or relocate the state by setting `XDG_CONFIG_HOME`/`XDG_CACHE_HOME` to
  a local path in the account's systemd user environment
  (`~/.config/environment.d/` is itself read from `$XDG_CONFIG_HOME`, so
  set it via `systemctl --user set-environment` under lingering, or a
  PAM env entry).

**6. Pre-stage RHCOS base images** on each libvirt host in the persistent
storage pool (e.g., `/var/lib/libvirt/images/rhcos.qcow2`). The RHCOS
version should match the OpenShift cluster version (e.g., use the 4.21
image for an OCP 4.21 cluster). Download from
`https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos/<version>/latest/`.

**7. Deploy a MachineHealthCheck** (recommended — see below).

## Custom Resources

| CRD | Purpose |
|-----|---------|
| **LibvirtHost** | Reusable connection details for a libvirt hypervisor (URI, SSH key, OVMF paths, reserved resources). Discovers host capacity via `virsh nodeinfo`. Health-checked only while machines are active. |
| **LibvirtCluster** | CAPI contract resource — reports ready when the control plane endpoint is configured. No infrastructure provisioned. |
| **LibvirtMachine** | Core resource — a single KVM VM with static IP, UEFI firmware, and ignition or cloud-init bootstrap. VM size can be auto-derived from host capacity. One per host (webhook-enforced). |
| **LibvirtMachineTemplate** / **LibvirtClusterTemplate** | Inert templates that MachineDeployments, control plane providers and ClusterClass stamp LibvirtMachines / LibvirtClusters from. |

The [API reference](docs/api-reference/v1alpha1.md) lists every field, default
and validation rule. It is generated from `api/v1alpha1` with `make api-docs`.

## Example

### Schedule a worker via 5-Spot

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

### Create a worker directly

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

### Using with 5-Spot ScheduledMachines

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

### Ephemeral storage with tmpfs

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
[Storage Pools](#storage-pools)). RAM is only consumed while the VM
exists. The host's persistent storage is never touched.

`ephemeralPoolSize` accepts tmpfs `size=` syntax: a percentage of physical
RAM (`"80%"`) or an absolute size (`"16G"`). When unset, the kernel's
default applies (50% of physical RAM per mount).

### Node labels and annotations

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

### Fast first boot with `bootArtifacts` (direct kernel boot)

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

### Cluster-wide base image via URL (`baseImage`)

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

### MachineHealthCheck (recommended)

When the designed deletion path is followed (5-Spot → CAPI → CAPLV),
CAPI drains pods and deletes the `Node` object from OpenShift before the
VM is destroyed. Everything cleans up automatically.

However, if a VM dies unexpectedly (host crash, OOM kill, admin
`virsh destroy`), the `Node` object lingers in `NotReady` state
indefinitely. A CAPI `MachineHealthCheck` detects this and triggers
remediation — deleting the orphaned Machine and its stale Node.

```yaml
apiVersion: cluster.x-k8s.io/v1beta2
kind: MachineHealthCheck
metadata:
  name: caplv-worker-health
  namespace: default
spec:
  clusterName: my-ocp-cluster
  selector:
    matchLabels:
      node-role.kubernetes.io/worker: ""
  unhealthyConditions:
    - type: Ready
      status: "False"
      timeout: 5m           # node was Ready, now isn't — remediate after 5 min
    - type: Ready
      status: "Unknown"
      timeout: 5m           # node stopped reporting — remediate after 5 min
  nodeStartupTimeout: 10m   # time for a new VM to boot and join the cluster
```

**Tuning `nodeStartupTimeout`:** This is how long CAPI waits for a newly
provisioned VM to boot, run ignition, start kubelet, get its CSR approved,
and report `Ready`. Typical CAPLV startup is 2-4 minutes (VM provision
~30s, RHCOS boot ~60s, ignition + kubelet ~60s, CSR approval ~30s). The
10-minute default gives comfortable headroom. Increase to 15m+ if your
hosts are slow, base images are large, or the network path to the API
server has high latency.

This is the safety net for the known limitation that CAPLV does not
monitor VM health after provisioning. Without it, dead VMs leave orphaned
Node objects in the cluster.

### Running multiple provider instances (`--watch-filter`)

Several CAPLV managers can share one management cluster, each owning a
subset of objects. Start each manager with `--watch-filter=<value>` and
label every object it should own — `Cluster`, `Machine`, `LibvirtCluster`,
`LibvirtMachine` and `LibvirtHost` — with
`cluster.x-k8s.io/watch-filter: <value>`. A manager ignores objects whose
label does not match, and its CSR approver only approves CSRs for nodes
backed by a matching `LibvirtMachine`. Without the flag, a manager
reconciles everything.

### Feature gates

New, experimental behaviour ships behind a feature gate, enabled per
manager with `--feature-gates=Name=true[,Other=false]`. Gates are declared in
`feature/feature.go`. There are none yet; features that shipped before the
framework existed, such as session mode and ephemeral storage, stay ungated.

### Failure domains

Each `LibvirtHost` is a failure domain. `LibvirtCluster.status.failureDomains`
lists every host in the cluster's namespace (except hosts being deleted),
all marked control-plane eligible, and CAPI copies the list to
`Cluster.status.failureDomains`. Each `LibvirtMachine` reports its host in
`status.failureDomain`, which CAPI copies to `Machine.status.failureDomain`.
Both fields use the CAPI v1beta2 contract shapes. Placement is still chosen
by `spec.hostRef`. CAPLV does not yet assign hosts from a failure domain
chosen by CAPI.

## Cluster Templates (clusterctl flavors)

`templates/` holds the cluster templates published with each release, for
`clusterctl generate cluster`:

```bash
clusterctl generate cluster my-cluster --infrastructure libvirt \
  --flavor ephemeral > my-cluster.yaml   # omit --flavor for the default
clusterctl generate cluster my-cluster --infrastructure libvirt \
  --flavor ephemeral --list-variables    # show what a flavor needs
```

The flavors reuse the same variable names. The non-topology flavors all need
`CLUSTER_NAME`, `NAMESPACE`, `CONTROL_PLANE_ENDPOINT_HOST`,
`LIBVIRT_HOST_NAME`, `LIBVIRT_HOST_SSH_SECRET`,
`LIBVIRT_HOST_SSH_FINGERPRINT`, `BOOTSTRAP_SECRET_NAME`, `BASE_IMAGE`,
`CONTROL_PLANE_IP`, `NETWORK_GATEWAY` and `NETWORK_DNS`. The table lists
what each flavor needs on top of that. Each template's header comment
documents every variable, including the optional ones with defaults.

| Flavor | Purpose | Required variables beyond the common set |
| --- | --- | --- |
| *(default)* `cluster-template.yaml` | One explicitly placed Machine + LibvirtMachine on a system-mode host. Duplicate the LibvirtHost / Machine / LibvirtMachine blocks per extra node. | `LIBVIRT_HOST_URI` |
| `direct-boot` | As default, plus `LibvirtCluster.spec.bootArtifacts`: first boot uses direct kernel boot with the kernel and initramfs fetched over HTTPS, and ignition delivered on a virtio-blk disk instead of fw_cfg (see [direct kernel boot](#fast-first-boot-with-bootartifacts-direct-kernel-boot)). | `LIBVIRT_HOST_URI`, `BOOT_KERNEL_URL`, `BOOT_INITRAMFS_URL` |
| `session-mode` | As default, but the host is reached at `qemu+ssh://<user>@<host>/session` (the [unprivileged per-user daemon](#session-mode-unprivileged-libvirt)). `network.type` is fixed to `bridge`, the only type session mode supports. | `LIBVIRT_HOST_ADDRESS` (replaces `LIBVIRT_HOST_URI`) |
| `ephemeral` | As default, but `rootDisk.ephemeralPool: true` puts the root disk in a per-machine tmpfs pool that is destroyed with the VM (see [ephemeral storage](#ephemeral-storage-with-tmpfs)). The base image stays in `BASE_IMAGE_POOL`. | `LIBVIRT_HOST_URI` |
| `topology` | ClusterClass-based cluster from the `libvirt-kubeadm` ClusterClass (`clusterclass-libvirt-kubeadm.yaml`, fetched by clusterctl automatically). Builds a new kubeadm workload cluster on cloud-init guests: one control plane VM and one worker VM, each on its own LibvirtHost. | `CLUSTER_NAME`, `NAMESPACE`, `KUBERNETES_VERSION`, `CONTROL_PLANE_ENDPOINT_HOST`, `CONTROL_PLANE_LIBVIRT_HOST`, `CONTROL_PLANE_LIBVIRT_HOST_URI`, `CONTROL_PLANE_LIBVIRT_HOST_SSH_FINGERPRINT`, `WORKER_LIBVIRT_HOST`, `WORKER_LIBVIRT_HOST_URI`, `WORKER_LIBVIRT_HOST_SSH_FINGERPRINT`, `LIBVIRT_HOST_SSH_SECRET`, `CONTROL_PLANE_IP`, `WORKER_IP`, `NETWORK_GATEWAY`, `NETWORK_DNS`, `BASE_IMAGE` (replaces the common set) |

Notes on the topology flavor:

- A ClusterClass has to name a control plane provider template and a
  bootstrap config template for every worker class. The default flavor's
  model, a pre-existing control plane plus hand-made bootstrap Secrets or
  capi-bootstrap-ignition (which has no template kind), cannot be written
  as a ClusterClass. The class therefore uses the core CAPI kubeadm
  providers, so `clusterctl init` needs `--control-plane kubeadm
  --bootstrap kubeadm`, which are the defaults.
- CAPLV does not allocate hosts or IP addresses. Each role gets one
  LibvirtHost and one static address through ClusterClass variables, and
  the control plane and worker replica counts must stay at 1. Workers roll
  out delete-first (`maxSurge: 0`). A KubeadmControlPlane rollout always
  surges, so a control plane upgrade collides on the host and stalls.
- `BASE_IMAGE` must be a cloud-init qcow2 with kubeadm, kubelet and a
  container runtime for `KUBERNETES_VERSION`, such as an image-builder
  qemu EFI build. Its NIC must be named `enp1s0` and it must use netplan,
  because the class writes the static address as a netplan file. CAPLV only
  injects network configuration into ignition guests.

## Key Design Decisions

Rationale and trade-offs are recorded in [host management](docs/adr/0001-host-management.md)
and [readiness and bootstrap](docs/adr/0002-readiness-and-bootstrap.md).

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

## Host Security Model

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
daemon at all, see [session mode](#session-mode-unprivileged-libvirt),
which runs both the daemon and QEMU as the service account and confines
the remaining privilege to the setuid `qemu-bridge-helper` plus the same
`/run/caplv/` sudo rules.

## Storage Pools

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

## Host Storage Layout (with ephemeralPool: true)

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

## Failure Modes

### Host failures

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| **Host dies or reboots** | VM vanishes. LibvirtHost controller marks host not-ready on the next health check (per-host `spec.healthCheckIntervalSeconds`, default 5 min). LibvirtMachine finalizer stalls cleanup (`CleanupStalled` condition). | Operator fixes host. Once reachable again, CAPLV retries cleanup automatically. If the VM no longer exists, cleanup completes (not-found errors are ignored). |
| **Host network becomes unreachable** | Same as host death from CAPLV's perspective. SSH connections fail, host goes not-ready on next health check. | Network restored, host re-verified on next check cycle. |
| **libvirtd crashes or restarts** | SSH works but virsh commands fail. Host Ping check (`virsh version`) catches this on the next cycle, marks host not-ready. Running VMs may or may not survive depending on libvirt config. | libvirtd restarts, next health check restores host to ready. |
| **Incumbent workload reclaims resources** | OOM killer terminates the VM process, or host admin runs `virsh destroy`. Domain goes to `shutoff` state. | CAPLV does not currently detect post-ready VM state changes (see Known Limitations). CAPI MachineHealthCheck can detect the node going `NotReady`. |

### VM failures

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| **VM killed externally** (`virsh destroy`) | Domain state changes to `shutoff`. CAPLV does not detect this after initial `ready=true`. | CAPI MachineHealthCheck detects the node disappearing and can trigger remediation. |
| **VM crashes** | Same as killed — domain state changes but CAPLV's status still shows `ready=true`. | Same as above. |
| **VM boots but never joins cluster** | Not CAPLV's responsibility. The node will sit in `NotReady` state. | CAPI MachineHealthCheck handles this (`nodeStartupTimeout`). The bootstrap provider and machine approver must be functional. |

### Controller failures

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| **Controller crashes mid-provisioning** | Partially created artifacts may exist on the host. | Automatic. Deterministic artifact naming allows the controller to discover existing artifacts and resume from where it left off. |
| **Controller crashes mid-deletion** | Finalizer remains on the resource, blocking garbage collection. | Automatic. On restart, the controller retries cleanup. Idempotent delete operations (not-found errors ignored) ensure safe retry. |
| **Leader election lost** | New leader picks up all pending reconciles. | Automatic. All operations are idempotent. |

### Control plane failures

Since CAPLV runs on the same cluster, control plane issues directly
affect CAPLV's ability to operate.

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| **OpenShift API fully down** | CAPLV cannot reconcile at all — it runs on this cluster. No new machines are created. Already-running VMs continue to serve workloads but cannot be managed (no drain, no deletion). | API recovers, CAPLV resumes reconciliation automatically. |
| **API reachable internally but not from worker endpoint** | Infrastructure stays ready and CAPLV can provision VMs, but new nodes cannot join until they can reach the endpoint. Already-running VMs may lose their API connection. | Restore endpoint reachability; node bootstrap and kubelet reconnect independently of infrastructure readiness. |
| **Cluster under resource pressure** | CAPLV pods may be evicted or throttled. Reconciliation slows. If 5-Spot activates a large schedule during a stressed period, hundreds of concurrent reconciles add API server and etcd load. | CAPLV pods restart via Deployment. Consider PodDisruptionBudget and resource requests to keep CAPLV running during pressure. |
| **etcd degradation** | Status patches and Machine updates slow down. All concurrent reconciles compete for etcd bandwidth. | etcd recovers, backlogged reconciles drain. |
| **CAPLV CSR approver not running** | VMs boot and request CSRs, but certificates are never approved. Nodes stay `NotReady`. CAPLV includes a built-in CSR approver that auto-approves CSRs from nodes matching a CAPI Machine backed by a LibvirtMachine. | Restart the CAPLV controller. Pending CSRs are approved and nodes join. |

### Storage failures (with ephemeralPool)

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| **Host runs out of RAM** | tmpfs can't allocate pages for CoW writes. VM disk I/O fails, VM likely crashes. | Operator intervention. Reduce `reservedResources` or increase host RAM. The crashed VM can be deleted and recreated by 5-Spot. |
| **Base image pool goes offline** | New VMs can't be provisioned (backing image unreachable). Existing CoW VMs may also fail if the backing chain is broken. | Operator restores the persistent storage pool. |

### Health check behavior

Host health checks only run while machines actively reference the host.
When 5-Spot deletes all machines on a host, health checks stop — no SSH
traffic to idle hosts. When a new machine is created, the host controller
wakes up and resumes checking. The interval is per-host via
`spec.healthCheckIntervalSeconds` (default 300, minimum 30).

### Known Limitations

**No post-ready VM health monitoring.** Once a LibvirtMachine reaches
`ready=true`, CAPLV does not currently recheck the domain state. If the
VM dies after provisioning, `status.ready` remains `true` until the
resource is deleted. Deploy a `MachineHealthCheck` (see example above) to
detect node-level failures and trigger remediation. Without it, dead VMs
leave orphaned `Node` objects and `Terminating` pods in OpenShift.

## Prerequisites

- An OpenShift cluster (CAPLV runs on the same cluster that workers join)
- CAPI installed on the cluster
- libvirt hosts reachable over SSH from the cluster network
- RHCOS qcow2 base images pre-staged in libvirt storage pools on each host
- OVMF firmware packages installed on libvirt hosts (for UEFI boot)

## Compatibility

The following describes the current development tree. Dependency versions are
not a tested support range; release tags must be checked independently.

| CAPLV revision | Cluster API | Kubernetes | libvirt / QEMU |
| --- | --- | --- | --- |
| Development (`main`) | Requires a CAPI core that implements the v1beta2 contract (v1.11 or later). Built against the CAPI v1.9.11 Go module; imports the v1beta1 API and transitional metav1 condition utilities | Kubernetes Go libraries v0.35.0; `make test` selects envtest 1.35 | Uses `virsh` over SSH; no numeric minimum version is currently enforced or certified |

`metadata.yaml` advertises the v1beta2 contract for the 0.1 and 0.2 release
series, and the CRDs carry only the `cluster.x-k8s.io/v1beta2` contract label.
Status fields such as `initialization.provisioned` and the list-shaped
`failureDomains` follow v1beta2, so CAPI releases that only implement the
v1beta1 contract (v1.10 and earlier) are not supported. That declaration
alone does not establish full contract compliance or ClusterClass support;
see [ADR 0003](docs/adr/0003-api-version-promotion.md) for the remaining gaps
and the promotion plan. Machine templates, host/IP allocation for scaling, and
end-to-end lifecycle coverage remain open work. This tree's versions must not
be read as verified compatibility for previously released binaries.

Hosts need QEMU/KVM and libvirt tooling with the commands used by CAPLV, working
storage pools, and OVMF for UEFI guests. Session mode additionally needs the
bridge helper and user-session setup described above. Test the exact host
configuration and guest OS before rollout; no complete Kubernetes guest-version
or libvirt-version matrix has been established yet.

## Development

```bash
# Build
make build

# Run tests
make test

# Generate CRDs and deepcopy
make manifests generate

# Regenerate the API reference (docs/api-reference/v1alpha1.md) after editing api/
make api-docs

# Build container image (pure Go, no CGo)
make podman-build IMG=ghcr.io/atgreen/caplv:latest

# Deploy to cluster
make deploy IMG=ghcr.io/atgreen/caplv:latest
```

## Project Structure

```
api/v1alpha1/          CRD type definitions (LibvirtHost, LibvirtCluster, LibvirtMachine)
internal/
  controller/          Reconcilers for all three CRDs + CSR approver (50 concurrent machine workers)
  ignition/            Ignition config manipulation (hostname and providerID injection)
  libvirt/             Client interface, virsh-over-SSH, domain XML generation
  iso/                 Cloud-init NoCloud ISO creation (pure Go, go-diskfs)
  ssh/                 SSH client with host key verification
  scope/               MachineScope — gathers reconciliation context, artifact naming
  webhook/             Admission webhook (immutable spec, required static IP, one VM per host)
config/                Kubernetes manifests (CRDs, RBAC, deployment, webhook)
```

## License

Copyright 2026 Anthony Green.

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.
