#!/usr/bin/env bash
# demo-create-worker.sh — Create a 5-Spot ScheduledMachine for demo purposes
#
# The schedule covers all days/hours so the worker activates immediately.
# Adjust hostRef, network, and rootDisk values for your environment.
#
# Usage:
#   ./demo-create-worker.sh              # Create and watch
#   ./demo-create-worker.sh --dry-run    # Print manifest only
#   ./demo-create-worker.sh --delete     # Remove the worker

set -euo pipefail

HOST="${DEMO_HOST:-laptop}"
WORKER_NAME="${DEMO_WORKER:-spot-worker-01}"
NAMESPACE="${DEMO_NAMESPACE:-default}"
BASE_IMAGE="${DEMO_BASE_IMAGE:-rhcos-qemu.x86_64.qcow2}"
STORAGE_POOL="${DEMO_STORAGE_POOL:-default}"
NETWORK_NAME="${DEMO_NETWORK:-default}"
NETWORK_TYPE="${DEMO_NETWORK_TYPE:-network}"
IP_ADDRESS="${DEMO_IP:-192.168.122.100/24}"
GATEWAY="${DEMO_GATEWAY:-192.168.122.1}"
VCPUS="${DEMO_VCPUS:-2}"
MEMORY_MB="${DEMO_MEMORY:-7168}"
DISK_SIZE="${DEMO_DISK_SIZE:-20Gi}"

if command -v oc &>/dev/null; then
    KUBECTL=oc
elif command -v kubectl &>/dev/null; then
    KUBECTL=kubectl
else
    echo "ERROR: Neither oc nor kubectl found" >&2
    exit 1
fi

MANIFEST=$(cat <<EOF
apiVersion: 5spot.finos.org/v1alpha1
kind: ScheduledMachine
metadata:
  name: ${WORKER_NAME}
  namespace: ${NAMESPACE}
spec:
  clusterName: spot

  schedule:
    enabled: true
    daysOfWeek:
      - mon-sun
    hoursOfDay:
      - "0-23"
    timezone: UTC

  infrastructureSpec:
    apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
    kind: LibvirtMachine
    spec:
      hostRef:
        name: ${HOST}
      bootstrapFormat: ignition
      domain:
        firmware: uefi
        machine: q35
        vcpus: ${VCPUS}
        memoryMB: ${MEMORY_MB}
      rootDisk:
        baseImage: ${BASE_IMAGE}
        storagePool: ${STORAGE_POOL}
        size: ${DISK_SIZE}
      network:
        name: ${NETWORK_NAME}
        type: ${NETWORK_TYPE}
        addresses:
          - ${IP_ADDRESS}
        gateway: ${GATEWAY}

  bootstrapSpec:
    apiVersion: bootstrap.cluster.x-k8s.io/v1alpha1
    kind: IgnitionConfig
    spec:
      secretRef:
        name: worker-ignition

  machineTemplate:
    labels:
      node-role.kubernetes.io/worker: ""

  priority: 50
  gracefulShutdownTimeout: 5m
  nodeDrainTimeout: 5m
  killSwitch: false
EOF
)

case "${1:-}" in
    --dry-run)
        echo "$MANIFEST"
        ;;
    --delete)
        echo "Deleting ScheduledMachine ${WORKER_NAME}..."
        $KUBECTL delete scheduledmachine "${WORKER_NAME}" -n "${NAMESPACE}" --ignore-not-found=true
        echo "Waiting for cleanup..."
        $KUBECTL wait --for=delete "scheduledmachine/${WORKER_NAME}" -n "${NAMESPACE}" --timeout=120s 2>/dev/null || true
        echo "Done."
        ;;
    *)
        echo "Creating ScheduledMachine ${WORKER_NAME} on host ${HOST}..."
        echo "$MANIFEST" | $KUBECTL apply -f -
        echo ""
        echo "Watching (Ctrl-C to stop)..."
        $KUBECTL get scheduledmachines -n "${NAMESPACE}" -w
        ;;
esac
