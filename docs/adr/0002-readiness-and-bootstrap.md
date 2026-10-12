# Infrastructure readiness and bootstrap approval

Status: records current behavior, including the event-driven readiness change.

## Readiness

CAPLV does not provision a control plane endpoint. A configured host and valid
port are sufficient to mark cluster infrastructure ready. Cluster and resource
changes trigger reconciliation. API reachability belongs to control plane and
node health checks.

The earlier TCP health gate blocked infrastructure readiness until the API was
already running. That can deadlock controllers which wait for infrastructure
before creating the control plane. Removing polling also removes periodic TCP
traffic, but operators must monitor endpoint reachability separately.

## Bootstrap approval

The built-in CSR controller supports the existing deployment model where CAPLV
runs on the cluster its VMs join. It checks CSR identity and the matching CAPI
Machine / LibvirtMachine before approving supported kubelet requests. See
[`csrapprover_controller.go`](../../internal/controller/csrapprover_controller.go)
for the authorization checks and supported request forms.

A separate approver is an alternative for installations with their own approval
policy. Automatic approval reduces bootstrap operations, but makes the
controller's identity checks and permissions security-sensitive. Treat changes
to those checks as authorization changes and retain negative test cases.

## API evolution

Conditions use Kubernetes `metav1.Condition`. CAPI v1.9's transitional accessors
allow its condition utilities to update that representation. Switching back to
legacy CAPI condition structs solely to type condition constants would change
the serialized API and is not part of this direction.

Future API promotion needs explicit compatibility tests, a real second version
and conversions, and a verified CAPI contract. A single-version conversion
webhook with no conversion to perform does not establish compatibility.
