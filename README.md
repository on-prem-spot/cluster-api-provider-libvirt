# CAPLV — Cluster API Provider for LibVirt

CAPLV is a [Cluster API](https://cluster-api.sigs.k8s.io/) infrastructure
provider that provisions KVM virtual machines on libvirt hosts. It can be
used in three ways:

- **Workers for an existing cluster.** Create `LibvirtMachine` and
  `Machine` resources directly, with ignition or cloud-init bootstrap data,
  to add VMs as nodes.
- **With [5-Spot](https://github.com/finos/5-spot)**, which schedules
  OpenShift worker nodes on and off physical RHEL/KVM infrastructure based
  on time-of-day rules.
- **New kubeadm clusters.** The `topology` flavor and its `libvirt-kubeadm`
  ClusterClass build a complete workload cluster from cloud-init guests with
  the core CAPI kubeadm providers.

The API is `v1alpha1`; fields may still change before `v1beta2` (see
[ADR 0003](docs/adr/0003-api-version-promotion.md)).

The target hosts are machines with incumbent workloads — DR standby
systems or servers with predictable load schedules that have idle
capacity during off-peak periods. CAPLV is designed to be minimally
disruptive to these hosts: worker-node VMs are fully ephemeral and,
when backed by a tmpfs storage pool, won't touch persistent storage
on the device at all.

## How It Works

The 5-Spot flow, adding OpenShift workers to the cluster CAPLV runs on:

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

In this flow CAPLV runs on the same OpenShift cluster that the worker VMs
join. With the `topology` flavor it runs on a management cluster and builds
separate workload clusters, as any CAPI infrastructure provider does; see
[Cluster templates](docs/cluster-templates.md).

Each libvirt host runs exactly one CAPLV-managed VM — enforced by an
admission webhook that rejects a second machine targeting the same host.
Hundreds or thousands of hosts can come online simultaneously — the
controller runs up to 50 concurrent reconcilers by default
(`--max-concurrent-reconciles`), each operating against a different host
over its own SSH connection.

## Custom Resources

| CRD | Purpose |
|-----|---------|
| **LibvirtHost** | Reusable connection details for a libvirt hypervisor (URI, SSH key, OVMF paths, reserved resources). Discovers host capacity via `virsh nodeinfo`. Health-checked only while machines are active. |
| **LibvirtCluster** | CAPI contract resource — reports ready when the control plane endpoint is configured. No infrastructure provisioned. |
| **LibvirtMachine** | Core resource — a single KVM VM with static IP, UEFI firmware, and ignition or cloud-init bootstrap. VM size can be auto-derived from host capacity. One per host (webhook-enforced). |
| **LibvirtClusterIdentity** | Cluster-scoped SSH credential that LibvirtHosts in the namespaces it allows reference instead of their own Secret, so one key can be shared and rotated in one place. |
| **LibvirtMachineTemplate** / **LibvirtClusterTemplate** | Inert templates that MachineDeployments, control plane providers and ClusterClass stamp LibvirtMachines / LibvirtClusters from. |

## Documentation

| Guide | Contents |
| --- | --- |
| [Getting started](docs/getting-started.md) | Prerequisites, one-time cluster and host setup, creating a first worker directly or through 5-Spot |
| [Session mode](docs/session-mode.md) | Running VMs under an unprivileged per-user libvirt daemon |
| [Configuration](docs/configuration.md) | Ephemeral tmpfs storage, node labels, direct kernel boot, cluster-wide base images, private CAs, shared SSH credentials, `clusterctl move`, watch filters, feature gates, failure domains |
| [Cluster templates](docs/cluster-templates.md) | `clusterctl generate cluster` flavors and their variables |
| [Architecture](docs/architecture.md) | Design decisions, host security model, storage pools and host layout |
| [Troubleshooting](docs/troubleshooting.md) | MachineHealthCheck setup, failure modes and recovery, known limitations |
| [Development](docs/development.md) | Building, testing, code generation and project layout |
| [E2E suite](test/e2e-libvirt/README.md) | The Cluster API e2e suite run against real KVM in CI, and how to run it elsewhere |
| [API reference](docs/api-reference/v1alpha1.md) | Every field, default and validation rule, generated from `api/v1alpha1` |
| [Architecture decision records](docs/adr/) | Rationale for host management, readiness, API version promotion, credential identities, `clusterctl move` and host and address allocation |

## Compatibility

The following describes the current development tree. Dependency versions are
not a tested support range; release tags must be checked independently.

| CAPLV revision | Cluster API | Kubernetes | libvirt / QEMU |
| --- | --- | --- | --- |
| Development (`main`) | Requires a CAPI core that implements the v1beta2 contract (v1.11 or later). Built against CAPI v1.14.3 and its v1beta2 API (`sigs.k8s.io/cluster-api/api/core/v1beta2`) | Kubernetes Go libraries v0.36.3, controller-runtime v0.24.1; `make test` selects envtest 1.36 | Uses `virsh` over SSH; no numeric minimum version is currently enforced or certified |

`metadata.yaml` advertises the v1beta2 contract for the 0.1 and 0.2 release
series, and the CRDs carry only the `cluster.x-k8s.io/v1beta2` contract label.
Status fields such as `initialization.provisioned` and the list-shaped
`failureDomains` follow v1beta2, so CAPI releases that only implement the
v1beta1 contract (v1.10 and earlier) are not supported. See
[ADR 0003](docs/adr/0003-api-version-promotion.md) for the remaining gaps and
the promotion plan; host and IP allocation for scaling is the largest. This
tree's versions must not be read as verified compatibility for previously
released binaries.

### Tested in CI

Every pull request runs Cluster API's quick-start spec against real KVM
([E2E suite](test/e2e-libvirt/README.md)): clusterctl installs CAPI, the
kubeadm providers and CAPLV into a kind management cluster, and the
`topology` flavor builds, checks and deletes a workload cluster of one
control plane VM and one worker VM. That run covers:

| Component | Version |
| --- | --- |
| Cluster API and kubeadm providers | v1.14.3 |
| Management cluster | kind, Kubernetes v1.36.4 |
| Workload cluster | kubeadm, Kubernetes v1.36.5, on Ubuntu 24.04 and on CentOS Stream 9 cloud-init guests (one run each) |
| Host | Ubuntu 24.04, libvirt 10.0, QEMU 8.2, system mode |

The ignition bootstrap path, session mode, ephemeral storage and
RHCOS/OpenShift workers are not covered by that run.

Hosts need QEMU/KVM and libvirt tooling with the commands used by CAPLV, working
storage pools, and OVMF for UEFI guests. Session mode additionally needs the
bridge helper and user-session setup described in
[Session mode](docs/session-mode.md).
Test combinations other than the one above with your own host configuration
and guest OS before rollout.

## License

Copyright 2026 Anthony Green.

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.
