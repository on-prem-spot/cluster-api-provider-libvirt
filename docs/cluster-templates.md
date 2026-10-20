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
| `direct-boot` | As default, plus `LibvirtCluster.spec.bootArtifacts`: first boot uses direct kernel boot with the kernel and initramfs fetched over HTTPS, and ignition delivered on a virtio-blk disk instead of fw_cfg (see [direct kernel boot](configuration.md#fast-first-boot-with-bootartifacts-direct-kernel-boot)). | `LIBVIRT_HOST_URI`, `BOOT_KERNEL_URL`, `BOOT_INITRAMFS_URL` |
| `session-mode` | As default, but the host is reached at `qemu+ssh://<user>@<host>/session` (the [unprivileged per-user daemon](session-mode.md)). `network.type` is fixed to `bridge`, the only type session mode supports. | `LIBVIRT_HOST_ADDRESS` (replaces `LIBVIRT_HOST_URI`) |
| `ephemeral` | As default, but `rootDisk.ephemeralPool: true` puts the root disk in a per-machine tmpfs pool that is destroyed with the VM (see [ephemeral storage](configuration.md#ephemeral-storage-with-tmpfs)). The base image stays in `BASE_IMAGE_POOL`. | `LIBVIRT_HOST_URI` |
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
- Optional `LIBVIRT_HOST_FIRMWARE_PATH` and `LIBVIRT_HOST_NVRAM_TEMPLATE_PATH`
  set the OVMF files on both hosts. They default to the Fedora/RHEL paths;
  Debian and Ubuntu hosts need `/usr/share/OVMF/OVMF_CODE_4M.fd` and
  `/usr/share/OVMF/OVMF_VARS_4M.fd`. `LIBVIRT_HOST_RESERVED_VCPUS` and
  `LIBVIRT_HOST_RESERVED_MEMORY_MB` (default 2 and 4096) set what each host
  keeps back; the class sizes each VM to the rest.
- The e2e suite in `test/e2e-libvirt` builds a cluster from this flavor on
  every CI run; its host setup script is a working example of a host and
  base image this flavor accepts.
