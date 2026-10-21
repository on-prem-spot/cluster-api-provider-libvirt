#!/usr/bin/env bash
# Copyright 2026 Anthony Green.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# workers.sh follows docs/getting-started.md on the OpenShift cluster: a
# Cluster, LibvirtCluster and LibvirtExternalControlPlane for the cluster
# itself, a LibvirtHost per host from infra.sh, and one RHCOS worker VM per
# host, which joins the cluster.
# The LibvirtCluster distributes the RHCOS image matching the cluster's
# release to the hosts. With --delete it deletes the workers again and checks
# that CAPLV removed their Nodes.

set -euo pipefail
cd "$(dirname "$0")"
# shellcheck source-path=SCRIPTDIR
source ./env.sh

NS=caplv-workers
# Which of the hosts' SSH keys to pin. CAPLV v0.3.0 only verifies the key its
# SSH client prefers, ECDSA on a stock sshd; later releases find any of them.
HOST_KEY_TYPE=${HOST_KEY_TYPE:-ed25519}
# The worker bootstrap data: "full", the whole config from the machine config
# server, as the getting started guide does, or "pointer", the Machine API's
# worker-user-data, which has Ignition fetch it from the initramfs.
BOOTSTRAP=${BOOTSTRAP:-full}
# DIRECT_BOOT=1 boots the workers with LibvirtCluster.spec.bootArtifacts:
# the release's RHCOS kernel and initramfs, and Ignition from a virtio disk.
DIRECT_BOOT=${DIRECT_BOOT:-0}

if [[ ${1:-} == --delete ]]; then
  log "Deleting the worker Machines"
  oc delete machines.cluster.x-k8s.io -n "$NS" --all --wait --timeout 20m
  log "Nodes after deletion"
  oc get nodes -o wide
  exit 0
fi

stream=$(openshift-install coreos print-stream-json)
rhcos=$(jq -c '.architectures.x86_64.artifacts.qemu | {release, url: .formats["qcow2.gz"].disk.location, sha256: .formats["qcow2.gz"].disk["uncompressed-sha256"]}' <<<"$stream")
pxe=$(jq -c '.architectures.x86_64.artifacts.metal.formats.pxe' <<<"$stream")
boot_artifacts=""
if [[ $DIRECT_BOOT == 1 ]]; then
  boot_artifacts="  bootArtifacts:
    hostPath: /var/lib/caplv/boot
    kernelArgs: ignition.platform.id=metal ignition.config.url=oem:/dev/disk/by-id/virtio-ignition console=ttyS0
    source:
      type: HTTPS
      https:
        kernelURL: $(jq -r .kernel.location <<<"$pxe")
        kernelSHA256: $(jq -r .kernel.sha256 <<<"$pxe")
        initramfsURL: $(jq -r .initramfs.location <<<"$pxe")
        initramfsSHA256: $(jq -r .initramfs.sha256 <<<"$pxe")"
fi
api_host="api.$CLUSTER_DOMAIN"

oc create namespace "$NS" --dry-run=client -o yaml | oc apply -f -
oc create secret generic libvirt-host-ssh-key -n "$NS" \
  --from-file=ssh-privatekey="$HOSTS_DIR/caplv-key" --dry-run=client -o yaml | oc apply -f -
if [[ $BOOTSTRAP == pointer ]]; then
  # Ignition fetches the rest from the initramfs, so the initramfs needs the
  # static address: CAPLV puts it on the kernel command line with direct
  # boot. Without direct boot, on this routed network without DHCP, the
  # fetch cannot work.
  oc get secret -n openshift-machine-api worker-user-data -o jsonpath='{.data.userData}' |
    base64 -d >"$STATE_DIR/worker.ign"
else
  # The full worker config from the machine config server, as the getting
  # started guide does. Pods may not reach the machine config server, so
  # fetch it through the SNO host, next to the cluster.
  read -r _ sno_host _ < <(head -1 "$HOSTS_DIR/hosts")
  ssh -n "${SSH_OPTS[@]}" "ec2-user@$sno_host" \
    curl -sfk --max-time 60 -H "'Accept: application/vnd.coreos.ignition+json;version=3.2.0'" \
    "https://api-int.$CLUSTER_DOMAIN:22623/config/worker" >"$STATE_DIR/worker.ign"
fi
oc create secret generic worker-bootstrap -n "$NS" \
  --from-file=value="$STATE_DIR/worker.ign" \
  --from-literal=format=ignition --dry-run=client -o yaml | oc apply --server-side --force-conflicts -f -

{
  cat <<EOF
apiVersion: cluster.x-k8s.io/v1beta2
kind: Cluster
metadata:
  name: $CLUSTER_NAME
  namespace: $NS
spec:
  controlPlaneEndpoint:
    host: $api_host
    port: 6443
  infrastructureRef:
    apiGroup: infrastructure.cluster.x-k8s.io
    kind: LibvirtCluster
    name: $CLUSTER_NAME
  # OpenShift's own control plane. CAPLV reports it to CAPI and maintains
  # the $CLUSTER_NAME-kubeconfig Secret CAPI reaches the cluster with.
  controlPlaneRef:
    apiGroup: infrastructure.cluster.x-k8s.io
    kind: LibvirtExternalControlPlane
    name: $CLUSTER_NAME
---
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtExternalControlPlane
metadata:
  name: $CLUSTER_NAME
  namespace: $NS
spec: {}
---
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtCluster
metadata:
  name: $CLUSTER_NAME
  namespace: $NS
spec:
  controlPlaneEndpoint:
    host: $api_host
    port: 6443
  baseImage:
    pool: default
    volumeName: rhcos-$(jq -r .release <<<"$rhcos").qcow2
    source:
      type: HTTPS
      https:
        url: $(jq -r .url <<<"$rhcos")
        sha256: $(jq -r .sha256 <<<"$rhcos")
$boot_artifacts
EOF
  while read -r name _ private_ip subnet; do
    fingerprint=$(awk -v n="$name" -v t="$HOST_KEY_TYPE" '$1==n && $2==t {print $3}' "$HOSTS_DIR/fingerprints")
    short=${name##*-libvirt-}
    cat <<EOF
---
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtHost
metadata:
  name: libvirt-$short
  namespace: $NS
spec:
  uri: qemu+ssh://caplv@$private_ip/system
  secretRef:
    name: libvirt-host-ssh-key
  hostKeyFingerprint: "$fingerprint"
---
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtMachine
metadata:
  name: worker-$short
  namespace: $NS
spec:
  hostRef:
    name: libvirt-$short
  domain: {}
  rootDisk:
    baseImage: rhcos-$(jq -r .release <<<"$rhcos").qcow2
    storagePool: default
    size: 100Gi
  network:
    type: network
    name: caplv
    addresses:
      - $subnet.10/24
    gateway: $subnet.1
    dns:
      nameservers:
        - $subnet.1
  bootstrapFormat: ignition
---
apiVersion: cluster.x-k8s.io/v1beta2
kind: Machine
metadata:
  name: worker-$short
  namespace: $NS
  labels:
    cluster.x-k8s.io/cluster-name: $CLUSTER_NAME
spec:
  clusterName: $CLUSTER_NAME
  bootstrap:
    dataSecretName: worker-bootstrap
  infrastructureRef:
    apiGroup: infrastructure.cluster.x-k8s.io
    kind: LibvirtMachine
    name: worker-$short
EOF
  done < <(tail -n +2 "$HOSTS_DIR/hosts")
} >"$STATE_DIR/workers.yaml"

log "Creating the Cluster, LibvirtCluster, LibvirtHosts and workers"
oc apply -f "$STATE_DIR/workers.yaml"

log "Waiting for the workers' Nodes to be Ready (first boot downloads and stages RHCOS)"
oc wait machines.cluster.x-k8s.io -n "$NS" --all --for=jsonpath='{.status.phase}'=Running --timeout 45m
oc wait nodes -l node-role.kubernetes.io/worker --for=condition=Ready --timeout 20m
oc get machines.cluster.x-k8s.io,libvirtmachines,libvirthosts -n "$NS" -o wide
oc get nodes -o wide
