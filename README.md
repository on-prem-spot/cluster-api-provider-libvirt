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
| [Configuration](docs/configuration.md) | Ephemeral tmpfs storage, node labels, direct kernel boot, cluster-wide base images, private CAs, watch filters, feature gates, failure domains |
| [Cluster templates](docs/cluster-templates.md) | `clusterctl generate cluster` flavors and their variables |
| [Architecture](docs/architecture.md) | Design decisions, host security model, storage pools and host layout |
| [Troubleshooting](docs/troubleshooting.md) | MachineHealthCheck setup, failure modes and recovery, known limitations |
| [Development](docs/development.md) | Building, testing, code generation and project layout |
| [API reference](docs/api-reference/v1alpha1.md) | Every field, default and validation rule, generated from `api/v1alpha1` |
| [Architecture decision records](docs/adr/) | Rationale for host management, readiness and API version promotion |

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
v1beta1 contract (v1.10 and earlier) are not supported. That declaration
alone does not establish full contract compliance or ClusterClass support;
see [ADR 0003](docs/adr/0003-api-version-promotion.md) for the remaining gaps
and the promotion plan. Machine templates, host/IP allocation for scaling, and
end-to-end lifecycle coverage remain open work. This tree's versions must not
be read as verified compatibility for previously released binaries.

Hosts need QEMU/KVM and libvirt tooling with the commands used by CAPLV, working
storage pools, and OVMF for UEFI guests. Session mode additionally needs the
bridge helper and user-session setup described in
[Session mode](docs/session-mode.md).
Test the exact host configuration and guest OS before rollout; no complete
Kubernetes guest-version or libvirt-version matrix has been established yet.

## License

Copyright 2026 Anthony Green.

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.
