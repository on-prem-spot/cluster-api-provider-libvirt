# Cluster templates (clusterctl flavors)

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
| `direct-boot` | As default, plus `LibvirtCluster.spec.bootArtifacts`: VMs boot directly from a kernel and initramfs fetched over HTTPS, at every boot, with ignition delivered on a virtio-blk disk instead of fw_cfg. Not for RHCOS's live PXE artifacts, which boot a live system rather than the root disk (see [direct kernel boot](configuration.md#fast-first-boot-with-bootartifacts-direct-kernel-boot)). | `LIBVIRT_HOST_URI`, `BOOT_KERNEL_URL`, `BOOT_INITRAMFS_URL` |
| `session-mode` | As default, but the host is reached at `qemu+ssh://<user>@<host>/session` (the [unprivileged per-user daemon](session-mode.md)). `network.type` is fixed to `bridge`, the only type session mode supports. | `LIBVIRT_HOST_ADDRESS` (replaces `LIBVIRT_HOST_URI`) |
| `ephemeral` | As default, but `rootDisk.ephemeralPool: true` puts the root disk in a per-machine tmpfs pool that is destroyed with the VM (see [ephemeral storage](configuration.md#ephemeral-storage-with-tmpfs)). The base image stays in `BASE_IMAGE_POOL`. | `LIBVIRT_HOST_URI` |
| `topology` | ClusterClass-based cluster from the `libvirt-kubeadm` ClusterClass (`clusterclass-libvirt-kubeadm.yaml`, fetched by clusterctl automatically). Builds a new kubeadm workload cluster on cloud-init guests: one control plane VM and `WORKER_MACHINE_COUNT` worker VMs, each claiming a LibvirtHost by label. Register the hosts first. | `CLUSTER_NAME`, `NAMESPACE`, `KUBERNETES_VERSION`, `CONTROL_PLANE_ENDPOINT_HOST`, `BASE_IMAGE` (replaces the common set) |

Notes on the topology flavor:

- A ClusterClass has to name a control plane provider template and a
  bootstrap config template for every worker class. The default flavor's
  model, a pre-existing control plane plus hand-made bootstrap Secrets or
  capi-bootstrap-ignition (which has no template kind), cannot be written
  as a ClusterClass. The class therefore uses the core CAPI kubeadm
  providers, so `clusterctl init` needs `--control-plane kubeadm
  --bootstrap kubeadm`, which are the defaults.
- Machines claim hosts by label (see
  [Choosing hosts](configuration.md#choosing-hosts-hostselector)). Before
  creating a cluster, register LibvirtHosts in its namespace: label control
  plane hosts `caplv.infrastructure.cluster.x-k8s.io/role=control-plane` and
  worker hosts `caplv.infrastructure.cluster.x-k8s.io/role=worker` (the
  ClusterClass variables `controlPlaneHostSelector` and `workerHostSelector`
  change the labels), and give each a `spec.machineNetwork` with its VM's
  address. A host runs one VM, so `WORKER_MACHINE_COUNT` can be at most the
  number of worker hosts.
- `CONTROL_PLANE_ENDPOINT_HOST` is the control plane host's address. The
  flavor runs one control plane machine; a KubeadmControlPlane rollout
  surges onto another host with another address, so without a load balancer
  or virtual IP for the API server, recreate the cluster rather than
  rolling the control plane. Workers roll out delete-first (`maxSurge: 0`)
  and need no spare host.
- `BASE_IMAGE` must be a cloud-init qcow2 with kubeadm, kubelet and a
  container runtime for `KUBERNETES_VERSION`, such as an image-builder
  qemu EFI build. CAPLV gives each machine its static address through the
  NoCloud `network-config`, matched to the NIC by MAC address, and
  cloud-init applies it with the guest's own network stack. Ubuntu
  (netplan) and Enterprise Linux (NetworkManager) guests both work; the e2e
  suite tests Ubuntu 24.04 and CentOS Stream 9.
- The e2e suite in `test/e2e-libvirt` builds a cluster from this flavor on
  every CI run; `test/e2e-libvirt/data/hosts.yaml` and its host setup script
  are working examples of the hosts and base image this flavor accepts.
