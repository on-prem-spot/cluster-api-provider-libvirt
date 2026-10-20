# CAPLV e2e suite (real libvirt)

This suite runs Cluster API's provider test specs, from
`sigs.k8s.io/cluster-api/test`, against real KVM. A kind management cluster
gets CAPI, the kubeadm bootstrap and control plane providers, and CAPLV built
from this checkout, all installed by clusterctl. The specs then build
workload clusters out of libvirt VMs.

It is a separate Go module so that the CAPI test framework's dependencies
stay out of the provider's `go.mod`.

The older `test/e2e` suite is different: it only deploys the manager to kind
and checks that it starts. It needs no libvirt host.

## Specs

| Spec | What it checks |
| --- | --- |
| Quick start (`quick_start_test.go`) | Registers the three `LibvirtHost`s in `data/hosts.yaml`, then builds a cluster from the shipped `topology` flavor and the `libvirt-kubeadm` ClusterClass: one control plane VM and two worker VMs, each claiming a host by label and taking its address from it. Checks that the Cluster is Available and the Machines Ready, then deletes the cluster. CI runs it twice, with Ubuntu 24.04 and with CentOS Stream 9 guests. |

The suite installs kindnet as the CNI once the control plane is up, so the
shipped templates are used unmodified.

## The host

`scripts/setup-host.sh` turns the machine it runs on into the libvirt host.
It is written for GitHub's Ubuntu 24.04 runners, which expose `/dev/kvm`, and
it changes the system, so run it only on a throwaway machine. It:

- installs libvirt, QEMU, OVMF and virt-install;
- creates a `caplv` account with an SSH key, and limits sshd to its ed25519
  host key;
- defines the `caplv-e2e` NAT network (192.168.150.0/24) and opens it to
  forwarded traffic, so the kind cluster can reach the workload API server;
- builds the base image for `GUEST` (`ubuntu-24.04`, the default, or
  `centos-stream-9`): it boots the distribution's cloud image once, lets
  cloud-init install containerd and kubeadm, kubelet and kubectl at
  `KUBERNETES_VERSION` (`scripts/bake-ubuntu.sh` or `scripts/bake-el.sh`),
  then resets cloud-init. The Enterprise Linux image runs SELinux
  permissive, as kubeadm's installation guide does;
- prints the host variables the suite needs, and appends them to
  `$GITHUB_ENV` in CI.

The one physical host is registered as three `LibvirtHost`s (`e2e-cp`,
`e2e-worker-1` and `e2e-worker-2`) with the same URI, because a
`LibvirtHost` runs one VM. Their `reservedResources` leave each VM about
3 GiB of memory, and each has a `machineNetwork` with its VM's address.

## Running it

In CI, `.github/workflows/e2e-libvirt.yaml` runs the suite on every pull
request and push to main. To run it elsewhere, on a disposable Ubuntu 24.04
machine with KVM, Docker, kind and Go:

```bash
export WORK_DIR=$PWD/.e2e-libvirt
eval "$(test/e2e-libvirt/scripts/setup-host.sh | sed 's/^/export /')"
make test-e2e-libvirt CONTAINER_TOOL=docker
```

For CentOS Stream 9 guests, also export `GUEST=centos-stream-9` and
`BASE_IMAGE=e2e-centos-stream-9-kube-v1.36.5.qcow2` before both commands.

Results, logs and resource dumps go to `_artifacts/`. In CI, the guests'
serial consoles, captured by `scripts/capture-consoles.sh`, and the libvirt
logs are uploaded with them.

Settings live in `config/libvirt.yaml`, including the Kubernetes version,
which must match the base image's. Any variable there can be overridden by an
environment variable of the same name.
