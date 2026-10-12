# CAPLV Demo Setup

This demo uses three Kubernetes operators working together to
schedule ephemeral libvirt VMs as CAPI worker nodes.

## Components

### CAPLV (Cluster API Provider Libvirt)
- **LibvirtHost** CR: points to a libvirt hypervisor over SSH
- **LibvirtMachine** CR: defines a VM (vCPUs, memory, disk, network)
- The controller provisions/destroys VMs on the referenced host

### 5-Spot (Scheduled Machine Controller)
- **ScheduledMachine** CR: wraps a CAPI Machine with a time-based schedule
- Creates infrastructure + bootstrap + Machine resources when in-schedule
- Drains and deletes them when out-of-schedule
- Supports day-of-week, hour-of-day, timezone, priority, and kill switch

### capi-bootstrap-ignition
- **IgnitionConfig** CR: references a Secret containing Ignition/Butane config
- Generates bootstrap data consumed by CAPLV to configure the VM on first boot

## Prerequisites

- An OpenShift/SNO cluster (the management cluster)
- `oc` or `kubectl` configured to talk to it
- `clusterctl` installed
- libvirt running on the hypervisor host (can be the same machine)
- A `caplv` user on the hypervisor with SSH key access and libvirt permissions
- RHCOS base image in the libvirt default storage pool (`rhcos-qemu.x86_64.qcow2`)

## Step-by-Step Runbook

### 1. Install cluster prerequisites

```bash
# cert-manager
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml
kubectl wait --for=condition=Available -n cert-manager deployment/cert-manager-webhook --timeout=120s

# Cluster API
clusterctl init

# capi-bootstrap-ignition (from sibling repo)
cd ../capi-bootstrap-ignition
make deploy
cd ../CAPLV
```

### 2. Deploy the CAPLV stack (5-Spot + CAPLV)

```bash
./deploy.sh
```

This installs both 5-Spot and CAPLV controllers, verifies rollouts,
and shows status. Use `./deploy.sh status` to check later.

### 3. Set up the libvirt host and cluster resources

```bash
./demo-setup-host.sh
```

This creates:
- **Cluster** (`spot`) — the CAPI cluster object
- **LibvirtCluster** (`spot`) — infrastructure reference for the cluster
- **LibvirtHost** (`laptop`) — points to the libvirt hypervisor
- **SSH key secret** — from `~/.ssh/id_ed25519` (override with `DEMO_SSH_KEY`)

Configure via environment variables:

| Variable | Default | Description |
|---|---|---|
| `DEMO_HOST` | `laptop` | LibvirtHost CR name |
| `DEMO_CLUSTER` | `spot` | Cluster name |
| `DEMO_LIBVIRT_URI` | `qemu+ssh://caplv@192.168.122.1/system` | Libvirt connection URI |
| `DEMO_SSH_KEY` | `~/.ssh/id_ed25519` | SSH private key for the hypervisor |
| `DEMO_CP_HOST` | `api.spot.labdroid.net` | Control plane API hostname |
| `DEMO_FIRMWARE` | `/usr/share/OVMF/OVMF_CODE.fd` | OVMF firmware path on hypervisor |
| `DEMO_NVRAM` | `/usr/share/OVMF/OVMF_VARS.fd` | NVRAM template path on hypervisor |

> **Note on the `Cluster` status.** After this step the `Cluster` will report
> `ControlPlaneInitialized=False` with the message "Waiting for the first
> control plane machine to have status.nodeRef set". This is **expected** and
> not blocking. In this demo the actual Kubernetes control plane is the
> existing SNO cluster (externally managed); CAPLV only schedules workers,
> so no CAPI control-plane Machine is ever created. CAPI v1.9 does not gate
> worker provisioning on this condition. The signal that matters is
> `InfrastructureReady` on the `Cluster` (driven by the `LibvirtCluster`
> controller TCP-dialing `controlPlaneEndpoint`):
>
> ```bash
> kubectl get cluster ${DEMO_CLUSTER:-spot} \
>   -o jsonpath='{.status.infrastructureReady}{"\n"}'
> # should print: true
> ```
>
> If that returns empty/false, check that `DEMO_CP_HOST` resolves and
> port 6443 is reachable from the management cluster.

### 4. Create the worker-ignition secret

This secret is created once and shared by all workers. It contains the
standard OpenShift worker Ignition config (kubelet certs, join config, etc.)
fetched from the cluster's Machine Config Server.

SSH into your SNO node and fetch it:

```bash
ssh core@<sno-ip> "sudo curl -sk \
  -H 'Accept: application/vnd.coreos.ignition+json;version=3.2.0' \
  https://localhost:22623/config/worker" > worker-ignition.json

kubectl create secret generic worker-ignition \
  --from-file=value=worker-ignition.json
```

### 5. Create a scheduled worker (the demo!)

```bash
./demo-create-worker.sh
```

This creates a **ScheduledMachine** with an always-on schedule (mon-sun, 0-23).
5-Spot picks it up immediately and creates the LibvirtMachine, IgnitionConfig,
and CAPI Machine. CAPLV provisions the VM, and it joins the cluster as a worker.

Configure via environment variables:

| Variable | Default | Description |
|---|---|---|
| `DEMO_HOST` | `laptop` | LibvirtHost to provision on |
| `DEMO_WORKER` | `spot-worker-01` | Worker name |
| `DEMO_MEMORY` | `5120` | VM memory in MB |
| `DEMO_VCPUS` | `2` | VM vCPUs |
| `DEMO_IP` | `192.168.122.100/24` | Worker VM IP address |
| `DEMO_BASE_IMAGE` | `rhcos-qemu.x86_64.qcow2` | Base disk image |

### 6. Watch it work

```bash
# Watch the ScheduledMachine status
kubectl get scheduledmachines -w

# See all resources
kubectl get cluster,libvirtcluster,libvirthost,libvirtmachine,machine,ignitionconfig,scheduledmachine

# Check the node joined
kubectl get nodes

# Stack status
./deploy.sh status
```

### 7. Tear down the worker

```bash
./demo-create-worker.sh --delete
```

### 8. Full teardown

```bash
# Remove host/cluster resources
./demo-setup-host.sh --delete

# Remove 5-Spot and CAPLV controllers
./deploy.sh teardown
```

## Flow

1. User creates a **ScheduledMachine** in the `default` namespace
2. 5-Spot evaluates the schedule and, when active, creates a
   **LibvirtMachine**, **IgnitionConfig**, and CAPI **Machine**
3. CAPLV provisions the VM on the target **LibvirtHost**
4. The VM boots with Ignition config and joins the cluster as a worker
5. When the schedule window closes, 5-Spot drains and deletes the machine
