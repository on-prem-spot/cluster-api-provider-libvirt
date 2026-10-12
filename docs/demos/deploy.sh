#!/usr/bin/env bash
# deploy.sh — Deploy the full CAPLV stack (5-Spot + capi-bootstrap-ignition + CAPLV)
#
# Usage:
#   ./deploy.sh              # Deploy everything
#   ./deploy.sh status       # Show status of all components
#   ./deploy.sh teardown     # Remove 5-Spot and CAPLV (leaves CAPI/cert-manager)
#   ./deploy.sh build-push   # Build and push CAPLV image, then deploy
#
# Environment variables:
#   CAPLV_IMG           CAPLV controller image (default: ghcr.io/atgreen/caplv:latest)
#   FIVESPOT_IMG        5-Spot controller image (default: ghcr.io/atgreen/5-spot:latest)
#   KUBECTL             kubectl/oc binary (default: auto-detect)
#   SKIP_PREREQS        Set to 1 to skip prerequisite checks
#   DRY_RUN             Set to 1 to print manifests without applying

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FIVESPOT_DIR="${FIVESPOT_DIR:-$(cd "$SCRIPT_DIR/../5-spot" 2>/dev/null && pwd || echo "")}"

CAPLV_IMG="${CAPLV_IMG:-ghcr.io/atgreen/caplv:latest}"
FIVESPOT_IMG="${FIVESPOT_IMG:-ghcr.io/atgreen/5-spot:latest}"
SKIP_PREREQS="${SKIP_PREREQS:-0}"
DRY_RUN="${DRY_RUN:-0}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info()  { echo -e "${BLUE}[INFO]${NC}  $*"; }
ok()    { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
err()   { echo -e "${RED}[ERROR]${NC} $*"; }
die()   { err "$@"; exit 1; }

# ---------- detect kubectl/oc ----------
detect_kubectl() {
    if [[ -n "${KUBECTL:-}" ]]; then
        return
    fi
    if command -v oc &>/dev/null; then
        KUBECTL=oc
    elif command -v kubectl &>/dev/null; then
        KUBECTL=kubectl
    else
        die "Neither oc nor kubectl found in PATH"
    fi
}

apply() {
    if [[ "$DRY_RUN" == "1" ]]; then
        echo "--- DRY RUN: $KUBECTL apply -f $* ---"
        cat "$@"
        echo "---"
    else
        $KUBECTL apply -f "$@"
    fi
}

apply_stdin() {
    if [[ "$DRY_RUN" == "1" ]]; then
        echo "--- DRY RUN: $KUBECTL apply -f - ---"
        cat
        echo "---"
    else
        $KUBECTL apply -f -
    fi
}

# ---------- prerequisite checks ----------
check_prereqs() {
    if [[ "$SKIP_PREREQS" == "1" ]]; then
        warn "Skipping prerequisite checks"
        return
    fi

    info "Checking prerequisites..."

    # Cluster connectivity
    $KUBECTL cluster-info &>/dev/null || die "Cannot connect to cluster. Check your kubeconfig."
    ok "Cluster reachable"

    # cert-manager
    if $KUBECTL get ns cert-manager &>/dev/null; then
        local cm_ready
        cm_ready=$($KUBECTL get pods -n cert-manager -l app.kubernetes.io/instance=cert-manager \
            --no-headers 2>/dev/null | grep -c Running || true)
        if [[ "$cm_ready" -ge 1 ]]; then
            ok "cert-manager running ($cm_ready pods)"
        else
            die "cert-manager namespace exists but no Running pods found"
        fi
    else
        die "cert-manager not installed. Install it first:\n  kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml"
    fi

    # Cluster API
    if $KUBECTL get ns capi-system &>/dev/null; then
        local capi_ready
        capi_ready=$($KUBECTL get pods -n capi-system --no-headers 2>/dev/null | grep -c Running || true)
        if [[ "$capi_ready" -ge 1 ]]; then
            ok "Cluster API running ($capi_ready pods)"
        else
            die "capi-system namespace exists but no Running pods found"
        fi
    else
        die "Cluster API not installed. Install it with clusterctl:\n  clusterctl init"
    fi

    # capi-bootstrap-ignition
    if $KUBECTL get ns capi-bootstrap-ignition-system &>/dev/null; then
        local ignition_ready
        ignition_ready=$($KUBECTL get pods -n capi-bootstrap-ignition-system --no-headers 2>/dev/null | grep -c Running || true)
        if [[ "$ignition_ready" -ge 1 ]]; then
            ok "capi-bootstrap-ignition running ($ignition_ready pods)"
        else
            warn "capi-bootstrap-ignition namespace exists but no Running pods — may still be starting"
        fi
    else
        die "capi-bootstrap-ignition not installed. See: https://github.com/atgreen/capi-bootstrap-ignition"
    fi
}

# ---------- deploy 5-Spot ----------

# DNS-1035 fix: Service, PDB, and NetworkPolicy names must start with a letter.
# The upstream 5-spot manifests use "5spot-controller" which requires the
# RelaxedServiceNameValidation feature gate. We detect whether the cluster
# supports it and patch if needed.
detect_dns1035_fixup() {
    if [[ -n "${FIVESPOT_DNS_FIXUP:-}" ]]; then
        return
    fi
    # Try creating a dry-run Service with a digit-prefixed name
    if $KUBECTL create service clusterip 5test-probe --tcp=80:80 --dry-run=server -o name &>/dev/null; then
        FIVESPOT_DNS_FIXUP=0
        info "  Cluster accepts digit-prefixed Service names (RelaxedServiceNameValidation enabled)"
    else
        FIVESPOT_DNS_FIXUP=1
        info "  Cluster requires DNS-1035 names — renaming 5spot-* → fivespot-* in Service/PDB/NetworkPolicy"
    fi
}

# Apply a 5-spot manifest, optionally fixing the image and DNS-1035 names
apply_fivespot() {
    local file=$1
    local content
    content=$(cat "$file")

    # Always patch image
    content=$(echo "$content" | sed "s|ghcr.io/RBC/5-spot:latest|${FIVESPOT_IMG}|g")

    # Fix DNS-1035 names if needed
    if [[ "$FIVESPOT_DNS_FIXUP" == "1" ]]; then
        content=$(echo "$content" | sed 's/\bname: 5spot-/name: fivespot-/g')
    fi

    echo "$content" | apply_stdin
}

deploy_fivespot() {
    info "Deploying 5-Spot..."

    if [[ -z "$FIVESPOT_DIR" || ! -d "$FIVESPOT_DIR" ]]; then
        die "5-spot repo not found. Set FIVESPOT_DIR to the path of your 5-spot clone.\n  Expected: $SCRIPT_DIR/../5-spot"
    fi

    detect_dns1035_fixup

    # CRDs first
    info "  Applying ScheduledMachine CRD..."
    apply "$FIVESPOT_DIR/deploy/crds/scheduledmachine.yaml"

    # Namespace
    info "  Creating namespace..."
    apply "$FIVESPOT_DIR/deploy/deployment/namespace.yaml"

    # RBAC
    info "  Applying RBAC..."
    apply_fivespot "$FIVESPOT_DIR/deploy/deployment/rbac/serviceaccount.yaml"
    apply_fivespot "$FIVESPOT_DIR/deploy/deployment/rbac/clusterrole.yaml"
    apply_fivespot "$FIVESPOT_DIR/deploy/deployment/rbac/clusterrolebinding.yaml"

    # ConfigMap, Service, NetworkPolicy, PDB
    info "  Applying configuration..."
    apply_fivespot "$FIVESPOT_DIR/deploy/deployment/configmap.yaml"
    apply_fivespot "$FIVESPOT_DIR/deploy/deployment/service.yaml"
    apply_fivespot "$FIVESPOT_DIR/deploy/deployment/networkpolicy.yaml"
    apply_fivespot "$FIVESPOT_DIR/deploy/deployment/pdb.yaml"

    # Deployment
    info "  Deploying controller (image: $FIVESPOT_IMG)..."
    apply_fivespot "$FIVESPOT_DIR/deploy/deployment/deployment.yaml"

    # Admission policies
    info "  Applying admission policies..."
    apply "$FIVESPOT_DIR/deploy/admission/validatingadmissionpolicy.yaml"
    apply "$FIVESPOT_DIR/deploy/admission/validatingadmissionpolicybinding.yaml"

    # Monitoring (optional — won't fail if CRD missing)
    if $KUBECTL api-resources 2>/dev/null | grep -q servicemonitors; then
        info "  Applying ServiceMonitor..."
        apply_fivespot "$FIVESPOT_DIR/deploy/monitoring/servicemonitor.yaml"
    else
        warn "  ServiceMonitor CRD not found, skipping monitoring setup"
    fi

    ok "5-Spot deployed"
}

# ---------- deploy CAPLV ----------
deploy_caplv() {
    info "Deploying CAPLV..."

    # Use the Makefile's deploy target which handles kustomize properly
    info "  Building and applying kustomize manifests (image: $CAPLV_IMG)..."
    if [[ "$DRY_RUN" == "1" ]]; then
        cd "$SCRIPT_DIR/config/manager" && \
            "$(make -s -C "$SCRIPT_DIR" kustomize 2>/dev/null || echo kustomize)" \
            edit set image "controller=${CAPLV_IMG}" 2>/dev/null
        echo "--- DRY RUN: kustomize build config/default ---"
        cd "$SCRIPT_DIR" && make kustomize 2>/dev/null
        "$(find "$SCRIPT_DIR/bin" -name kustomize -type f 2>/dev/null | head -1 || echo kustomize)" \
            build config/default
        echo "---"
    else
        make -C "$SCRIPT_DIR" deploy IMG="$CAPLV_IMG"
    fi

    ok "CAPLV deployed"
}

# ---------- wait for rollout ----------
wait_for_deployment() {
    local ns=$1 name=$2 timeout=${3:-120}

    if [[ "$DRY_RUN" == "1" ]]; then
        info "  DRY RUN: would wait for $ns/$name"
        return
    fi

    info "  Waiting for $ns/$name to be ready (timeout: ${timeout}s)..."
    if $KUBECTL rollout status deployment/"$name" -n "$ns" --timeout="${timeout}s" 2>/dev/null; then
        ok "  $name is ready"
    else
        warn "  $name not ready after ${timeout}s — check: $KUBECTL logs -n $ns deployment/$name"
    fi
}

# ---------- status ----------
cmd_status() {
    detect_kubectl
    echo ""
    info "=== Stack Status ==="
    echo ""

    for ns_name in \
        "cert-manager:cert-manager" \
        "capi-system:Cluster API" \
        "capi-bootstrap-ignition-system:capi-bootstrap-ignition" \
        "5spot-system:5-Spot" \
        "caplv-system:CAPLV"; do

        ns="${ns_name%%:*}"
        label="${ns_name##*:}"

        if $KUBECTL get ns "$ns" &>/dev/null; then
            running=$($KUBECTL get pods -n "$ns" --no-headers 2>/dev/null | grep -c Running || true)
            total=$($KUBECTL get pods -n "$ns" --no-headers 2>/dev/null | wc -l || true)
            if [[ "$running" -eq "$total" && "$total" -gt 0 ]]; then
                ok "$label: $running/$total pods running"
            elif [[ "$total" -gt 0 ]]; then
                warn "$label: $running/$total pods running"
            else
                warn "$label: namespace exists, no pods"
            fi
        else
            echo -e "  ${RED}✗${NC}  $label: not installed"
        fi
    done

    echo ""
    info "=== Pods ==="
    echo ""
    for ns in 5spot-system caplv-system; do
        if $KUBECTL get ns "$ns" &>/dev/null; then
            $KUBECTL get pods -n "$ns" -o wide 2>/dev/null || true
            echo ""
        fi
    done
}

# ---------- teardown ----------
cmd_teardown() {
    detect_kubectl

    warn "This will remove 5-Spot and CAPLV (cert-manager, CAPI, and capi-bootstrap-ignition are left intact)"
    read -rp "Continue? [y/N] " confirm
    [[ "$confirm" =~ ^[Yy]$ ]] || { info "Aborted."; exit 0; }

    info "Removing CAPLV..."
    make -C "$SCRIPT_DIR" undeploy ignore-not-found=true 2>/dev/null || true
    ok "CAPLV removed"

    info "Removing 5-Spot..."
    if [[ -n "$FIVESPOT_DIR" && -d "$FIVESPOT_DIR" ]]; then
        $KUBECTL delete -f "$FIVESPOT_DIR/deploy/admission/" --ignore-not-found=true 2>/dev/null || true
        $KUBECTL delete -f "$FIVESPOT_DIR/deploy/monitoring/" --ignore-not-found=true 2>/dev/null || true
        $KUBECTL delete -f "$FIVESPOT_DIR/deploy/deployment/deployment.yaml" --ignore-not-found=true 2>/dev/null || true
        $KUBECTL delete -f "$FIVESPOT_DIR/deploy/deployment/pdb.yaml" --ignore-not-found=true 2>/dev/null || true
        $KUBECTL delete -f "$FIVESPOT_DIR/deploy/deployment/networkpolicy.yaml" --ignore-not-found=true 2>/dev/null || true
        $KUBECTL delete -f "$FIVESPOT_DIR/deploy/deployment/service.yaml" --ignore-not-found=true 2>/dev/null || true
        $KUBECTL delete -f "$FIVESPOT_DIR/deploy/deployment/configmap.yaml" --ignore-not-found=true 2>/dev/null || true
        $KUBECTL delete -f "$FIVESPOT_DIR/deploy/deployment/rbac/" --ignore-not-found=true 2>/dev/null || true
        $KUBECTL delete -f "$FIVESPOT_DIR/deploy/deployment/namespace.yaml" --ignore-not-found=true 2>/dev/null || true
        $KUBECTL delete -f "$FIVESPOT_DIR/deploy/crds/scheduledmachine.yaml" --ignore-not-found=true 2>/dev/null || true
    else
        warn "5-spot repo not found, cleaning up by namespace..."
        $KUBECTL delete ns 5spot-system --ignore-not-found=true 2>/dev/null || true
        $KUBECTL delete crd scheduledmachines.5spot.finos.org --ignore-not-found=true 2>/dev/null || true
        $KUBECTL delete clusterrole 5spot-controller --ignore-not-found=true 2>/dev/null || true
        $KUBECTL delete clusterrolebinding 5spot-controller --ignore-not-found=true 2>/dev/null || true
        $KUBECTL delete validatingadmissionpolicy scheduledmachine-validation --ignore-not-found=true 2>/dev/null || true
        $KUBECTL delete validatingadmissionpolicybinding scheduledmachine-validation-binding --ignore-not-found=true 2>/dev/null || true
    fi
    ok "5-Spot removed"
}

# ---------- build-push ----------
cmd_build_push() {
    detect_kubectl

    info "Building CAPLV image: $CAPLV_IMG"
    make -C "$SCRIPT_DIR" podman-build IMG="$CAPLV_IMG"

    info "Pushing CAPLV image: $CAPLV_IMG"
    make -C "$SCRIPT_DIR" podman-push IMG="$CAPLV_IMG"

    info "Deploying with new image..."
    cmd_deploy
}

# ---------- deploy (main) ----------
cmd_deploy() {
    detect_kubectl
    echo ""
    info "=== Deploying CAPLV Stack ==="
    echo ""

    check_prereqs
    echo ""

    deploy_fivespot
    echo ""

    deploy_caplv
    echo ""

    # Wait for rollouts
    info "Verifying deployments..."
    wait_for_deployment 5spot-system 5spot-controller
    wait_for_deployment caplv-system caplv-controller-manager
    echo ""

    ok "=== Stack deployment complete ==="
    echo ""
    cmd_status
}

# ---------- main ----------
case "${1:-deploy}" in
    deploy)      cmd_deploy ;;
    status)      cmd_status ;;
    teardown)    cmd_teardown ;;
    build-push)  cmd_build_push ;;
    -h|--help)
        echo "Usage: $0 [deploy|status|teardown|build-push]"
        echo ""
        echo "Commands:"
        echo "  deploy      Deploy the full stack (default)"
        echo "  status      Show status of all components"
        echo "  teardown    Remove 5-Spot and CAPLV"
        echo "  build-push  Build, push CAPLV image, then deploy"
        echo ""
        echo "Environment:"
        echo "  CAPLV_IMG      CAPLV image (default: ghcr.io/atgreen/caplv:latest)"
        echo "  FIVESPOT_IMG   5-Spot image (default: ghcr.io/atgreen/5-spot:latest)"
        echo "  FIVESPOT_DIR   Path to 5-spot repo (default: ../5-spot)"
        echo "  KUBECTL        kubectl/oc binary (default: auto-detect)"
        echo "  SKIP_PREREQS   Set to 1 to skip prerequisite checks"
        echo "  DRY_RUN        Set to 1 to print manifests without applying"
        ;;
    *)
        die "Unknown command: $1 (try --help)"
        ;;
esac
