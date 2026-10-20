# Troubleshooting

What happens when hosts, VMs, the controller or the cluster fail, and how
to recover.

## MachineHealthCheck (recommended)

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
  checks:
    nodeStartupTimeoutSeconds: 600  # time for a new VM to boot and join the cluster
    unhealthyNodeConditions:
      - type: Ready
        status: "False"
        timeoutSeconds: 300  # node was Ready, now isn't — remediate after 5 min
      - type: Ready
        status: "Unknown"
        timeoutSeconds: 300  # node stopped reporting — remediate after 5 min
```

**Tuning `nodeStartupTimeoutSeconds`:** This is how long CAPI waits for a newly
provisioned VM to boot, run ignition, start kubelet, get its CSR approved,
and report `Ready`. Typical CAPLV startup is 2-4 minutes (VM provision
~30s, RHCOS boot ~60s, ignition + kubelet ~60s, CSR approval ~30s). The
600 seconds above give comfortable headroom. Raise it to 900 or more if your
hosts are slow, base images are large, or the network path to the API
server has high latency.

This is the safety net for the known limitation that CAPLV does not
monitor VM health after provisioning. Without it, dead VMs leave orphaned
Node objects in the cluster.

**Terminal provisioning failures** (a missing storage pool, a host without
room, an unsupported network type) show up as `Ready=False` on the
`LibvirtMachine`, which CAPI mirrors into the Machine's `InfrastructureReady`
condition, with the failure as the reason (see `api/v1alpha1/conditions.go`).
Do not remediate on them with `unhealthyMachineConditions`: a replacement
Machine names the same host and spec and fails the same way.

## Host failures

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| **Host dies or reboots** | VM vanishes. LibvirtHost controller marks host not-ready on the next health check (per-host `spec.healthCheckIntervalSeconds`, default 5 min). LibvirtMachine finalizer stalls cleanup (`CleanupStalled` condition). | Operator fixes host. Once reachable again, CAPLV retries cleanup automatically. If the VM no longer exists, cleanup completes (not-found errors are ignored). |
| **Host network becomes unreachable** | Same as host death from CAPLV's perspective. SSH connections fail, host goes not-ready on next health check. | Network restored, host re-verified on next check cycle. |
| **libvirtd crashes or restarts** | SSH works but virsh commands fail. Host Ping check (`virsh version`) catches this on the next cycle, marks host not-ready. Running VMs may or may not survive depending on libvirt config. | libvirtd restarts, next health check restores host to ready. |
| **Incumbent workload reclaims resources** | OOM killer terminates the VM process, or host admin runs `virsh destroy`. Domain goes to `shutoff` state. | CAPLV does not currently detect post-ready VM state changes (see Known Limitations). CAPI MachineHealthCheck can detect the node going `NotReady`. |

## VM failures

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| **VM killed externally** (`virsh destroy`) | Domain state changes to `shutoff`. CAPLV does not detect this after initial `ready=true`. | CAPI MachineHealthCheck detects the node disappearing and can trigger remediation. |
| **VM crashes** | Same as killed — domain state changes but CAPLV's status still shows `ready=true`. | Same as above. |
| **VM boots but never joins cluster** | Not CAPLV's responsibility. The node will sit in `NotReady` state. | CAPI MachineHealthCheck handles this (`nodeStartupTimeout`). The bootstrap provider and machine approver must be functional. |

## Controller failures

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| **Controller crashes mid-provisioning** | Partially created artifacts may exist on the host. | Automatic. Deterministic artifact naming allows the controller to discover existing artifacts and resume from where it left off. |
| **Controller crashes mid-deletion** | Finalizer remains on the resource, blocking garbage collection. | Automatic. On restart, the controller retries cleanup. Idempotent delete operations (not-found errors ignored) ensure safe retry. |
| **Leader election lost** | New leader picks up all pending reconciles. | Automatic. All operations are idempotent. |

## Control plane failures

Since CAPLV runs on the same cluster, control plane issues directly
affect CAPLV's ability to operate.

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| **OpenShift API fully down** | CAPLV cannot reconcile at all — it runs on this cluster. No new machines are created. Already-running VMs continue to serve workloads but cannot be managed (no drain, no deletion). | API recovers, CAPLV resumes reconciliation automatically. |
| **API reachable internally but not from worker endpoint** | Infrastructure stays ready and CAPLV can provision VMs, but new nodes cannot join until they can reach the endpoint. Already-running VMs may lose their API connection. | Restore endpoint reachability; node bootstrap and kubelet reconnect independently of infrastructure readiness. |
| **Cluster under resource pressure** | CAPLV pods may be evicted or throttled. Reconciliation slows. If 5-Spot activates a large schedule during a stressed period, hundreds of concurrent reconciles add API server and etcd load. | CAPLV pods restart via Deployment. Consider PodDisruptionBudget and resource requests to keep CAPLV running during pressure. |
| **etcd degradation** | Status patches and Machine updates slow down. All concurrent reconciles compete for etcd bandwidth. | etcd recovers, backlogged reconciles drain. |
| **CAPLV CSR approver not running** | VMs boot and request CSRs, but certificates are never approved. Nodes stay `NotReady`. CAPLV includes a built-in CSR approver that auto-approves CSRs from nodes matching a CAPI Machine backed by a LibvirtMachine. | Restart the CAPLV controller. Pending CSRs are approved and nodes join. |

## Storage failures (with ephemeralPool)

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| **Host runs out of RAM** | tmpfs can't allocate pages for CoW writes. VM disk I/O fails, VM likely crashes. | Operator intervention. Reduce `reservedResources` or increase host RAM. The crashed VM can be deleted and recreated by 5-Spot. |
| **Base image pool goes offline** | New VMs can't be provisioned (backing image unreachable). Existing CoW VMs may also fail if the backing chain is broken. | Operator restores the persistent storage pool. |

## Health check behavior

Host health checks only run while machines actively reference the host.
When 5-Spot deletes all machines on a host, health checks stop — no SSH
traffic to idle hosts. When a new machine is created, the host controller
wakes up and resumes checking. The interval is per-host via
`spec.healthCheckIntervalSeconds` (default 300, minimum 30).

## Known limitations

**No post-ready VM health monitoring.** Once a LibvirtMachine reaches
`ready=true`, CAPLV does not currently recheck the domain state. If the
VM dies after provisioning, `status.ready` remains `true` until the
resource is deleted. Deploy a `MachineHealthCheck` (see [above](#machinehealthcheck-recommended)) to
detect node-level failures and trigger remediation. Without it, dead VMs
leave orphaned `Node` objects and `Terminating` pods in OpenShift.
