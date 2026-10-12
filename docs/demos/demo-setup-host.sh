#!/usr/bin/env bash
# demo-setup-host.sh — Create the LibvirtHost, Cluster, and LibvirtCluster CRs
#
# This sets up the cluster-level resources needed before creating workers.
# Run this once after deploying the stack with deploy.sh.
#
# Usage:
#   ./demo-setup-host.sh              # Create all resources
#   ./demo-setup-host.sh --dry-run    # Print manifests only
#   ./demo-setup-host.sh --delete     # Remove all resources

set -euo pipefail

HOST_NAME="${DEMO_HOST:-laptop}"
NAMESPACE="${DEMO_NAMESPACE:-default}"
CLUSTER_NAME="${DEMO_CLUSTER:-spot}"
CONTROL_PLANE_HOST="${DEMO_CP_HOST:-api.spot.labdroid.net}"
CONTROL_PLANE_PORT="${DEMO_CP_PORT:-6443}"
LIBVIRT_URI="${DEMO_LIBVIRT_URI:-qemu+ssh://caplv@192.168.122.1/system}"
SSH_KEY_SECRET="${DEMO_SSH_SECRET:-${HOST_NAME}-ssh-key}"
SSH_KEY_FILE="${DEMO_SSH_KEY:-$HOME/.ssh/id_ed25519}"
FIRMWARE_PATH="${DEMO_FIRMWARE:-/usr/share/OVMF/OVMF_CODE.fd}"
NVRAM_PATH="${DEMO_NVRAM:-/usr/share/OVMF/OVMF_VARS.fd}"
HOST_KEY_FP="${DEMO_HOST_KEY_FP:-}"

if command -v oc &>/dev/null; then
    KUBECTL=oc
elif command -v kubectl &>/dev/null; then
    KUBECTL=kubectl
else
    echo "ERROR: Neither oc nor kubectl found" >&2
    exit 1
fi

# Auto-detect host key fingerprint if not set and host is reachable
if [[ -z "$HOST_KEY_FP" ]]; then
    # Extract host from URI (e.g., qemu+ssh://user@host/system -> host)
    HOST_IP=$(echo "$LIBVIRT_URI" | sed -n 's|.*@\([^/]*\)/.*|\1|p')
    if [[ -n "$HOST_IP" ]]; then
        HOST_KEY_FP=$(ssh-keyscan -t ed25519 "$HOST_IP" 2>/dev/null \
            | ssh-keygen -lf - 2>/dev/null \
            | awk '{print $2}' | head -1) || true
    fi
fi

HOST_KEY_LINE=""
if [[ -n "$HOST_KEY_FP" ]]; then
    HOST_KEY_LINE="  hostKeyFingerprint: ${HOST_KEY_FP}"
fi

MANIFESTS=$(cat <<EOF
---
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: ${CLUSTER_NAME}
  namespace: ${NAMESPACE}
spec:
  controlPlaneEndpoint:
    host: ${CONTROL_PLANE_HOST}
    port: ${CONTROL_PLANE_PORT}
  infrastructureRef:
    apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
    kind: LibvirtCluster
    name: ${CLUSTER_NAME}
---
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtCluster
metadata:
  name: ${CLUSTER_NAME}
  namespace: ${NAMESPACE}
spec:
  controlPlaneEndpoint:
    host: ${CONTROL_PLANE_HOST}
    port: ${CONTROL_PLANE_PORT}
---
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtHost
metadata:
  name: ${HOST_NAME}
  namespace: ${NAMESPACE}
spec:
  uri: ${LIBVIRT_URI}
  secretRef:
    name: ${SSH_KEY_SECRET}
${HOST_KEY_LINE}
  firmwarePath: ${FIRMWARE_PATH}
  nvramTemplatePath: ${NVRAM_PATH}
  healthCheckIntervalSeconds: 300
EOF
)

create_ssh_secret() {
    if $KUBECTL get secret "${SSH_KEY_SECRET}" -n "${NAMESPACE}" &>/dev/null; then
        echo "Secret ${SSH_KEY_SECRET} already exists, skipping."
        return
    fi

    if [[ ! -f "$SSH_KEY_FILE" ]]; then
        echo "ERROR: SSH key file not found: ${SSH_KEY_FILE}" >&2
        echo "Set DEMO_SSH_KEY to the path of the private key for the libvirt host." >&2
        exit 1
    fi

    echo "Creating SSH key secret ${SSH_KEY_SECRET}..."
    $KUBECTL create secret generic "${SSH_KEY_SECRET}" \
        --from-file=value="${SSH_KEY_FILE}" \
        -n "${NAMESPACE}"
}

create_worker_ignition() {
    if $KUBECTL get secret worker-ignition -n "${NAMESPACE}" &>/dev/null; then
        echo "Secret worker-ignition already exists, skipping."
        return
    fi

    # Try to fetch worker ignition from the cluster's MCS
    echo ""
    echo "=== Worker Ignition Setup ==="
    echo "The worker-ignition secret contains the Ignition config that new"
    echo "worker VMs use to join the cluster."
    echo ""
    echo "To create it, SSH into your SNO node and run:"
    echo ""
    echo "  sudo curl -sk \\"
    echo "    -H 'Accept: application/vnd.coreos.ignition+json;version=3.2.0' \\"
    echo "    https://localhost:22623/config/worker > worker-ignition.json"
    echo ""
    echo "Then from this machine:"
    echo ""
    echo "  ${KUBECTL} create secret generic worker-ignition \\"
    echo "    --from-file=value=worker-ignition.json -n ${NAMESPACE}"
    echo ""
}

case "${1:-}" in
    --dry-run)
        echo "$MANIFESTS"
        echo ""
        echo "# SSH key secret (would be created from ${SSH_KEY_FILE}):"
        echo "#   ${KUBECTL} create secret generic ${SSH_KEY_SECRET} --from-file=value=${SSH_KEY_FILE} -n ${NAMESPACE}"
        ;;
    --delete)
        echo "Deleting demo host resources..."
        echo "$MANIFESTS" | $KUBECTL delete -f - --ignore-not-found=true
        $KUBECTL delete secret "${SSH_KEY_SECRET}" -n "${NAMESPACE}" --ignore-not-found=true
        echo "Done."
        echo ""
        echo "NOTE: worker-ignition secret was NOT deleted. Remove manually if needed:"
        echo "  ${KUBECTL} delete secret worker-ignition -n ${NAMESPACE}"
        ;;
    *)
        echo "Creating cluster resources..."
        create_ssh_secret
        echo ""
        echo "Applying Cluster, LibvirtCluster, and LibvirtHost..."
        echo "$MANIFESTS" | $KUBECTL apply -f -
        echo ""
        create_worker_ignition
        echo "Done. Verify with:"
        echo "  ${KUBECTL} get cluster,libvirtcluster,libvirthost -n ${NAMESPACE}"
        ;;
esac
