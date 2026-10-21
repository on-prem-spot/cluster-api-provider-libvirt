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

# capi.sh installs cert-manager, Cluster API and the released CAPLV on the
# OpenShift cluster with clusterctl, as docs/getting-started.md describes.

set -euo pipefail
cd "$(dirname "$0")"
# shellcheck source=env.sh
source ./env.sh

if [[ $CAPLV_VERSION == latest ]]; then
  release_path=latest/download
else
  release_path=download/$CAPLV_VERSION
fi
cat >"$STATE_DIR/clusterctl.yaml" <<EOF
providers:
  - name: libvirt
    type: InfrastructureProvider
    url: https://github.com/on-prem-spot/cluster-api-provider-libvirt/releases/$release_path/infrastructure-components.yaml
EOF

if ! oc get deployment -n caplv-system caplv-controller-manager >/dev/null 2>&1; then
  log "Installing cert-manager, Cluster API and CAPLV with clusterctl"
  clusterctl init --config "$STATE_DIR/clusterctl.yaml" --infrastructure libvirt
fi

# The upstream CAPI controllers run as UID 65532, outside the UID range
# OpenShift's restricted-v2 SCC assigns, so their pods are refused until
# their service accounts may use nonroot-v2. CAPLV leaves its UID to the
# SCC and needs no grant.
for sa in capi-system/capi-manager \
  capi-kubeadm-bootstrap-system/capi-kubeadm-bootstrap-manager \
  capi-kubeadm-control-plane-system/capi-kubeadm-control-plane-manager; do
  oc adm policy add-scc-to-user nonroot-v2 -z "${sa#*/}" -n "${sa%/*}"
  # A ReplicaSet refused for long enough stops retrying; restart it.
  if [[ $(oc get deployment -n "${sa%/*}" -o jsonpath='{.items[0].status.availableReplicas}') != 1 ]]; then
    oc rollout restart deployment -n "${sa%/*}"
  fi
done
for ns in capi-system capi-kubeadm-bootstrap-system capi-kubeadm-control-plane-system caplv-system; do
  oc rollout status deployment -n "$ns" --timeout 5m
done

log "Provider pods"
oc get pods -A -l 'cluster.x-k8s.io/provider' -o wide
oc get providers.clusterctl.cluster.x-k8s.io -A
