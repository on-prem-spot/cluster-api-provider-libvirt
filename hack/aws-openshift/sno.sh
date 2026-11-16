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

# sno.sh installs single-node OpenShift, platform none, as a VM on the SNO
# host from infra.sh, with the agent-based installer and a static address.
# The installer runs on the SNO host, next to the VM. The host forwards the
# API and the console (ports 6443, 443, 80) to the VM, so they can be used
# from here through the public DNS records. With --render-only it stops
# after writing install-config.yaml and agent-config.yaml, for review.

set -euo pipefail
cd "$(dirname "$0")"
# shellcheck source-path=SCRIPTDIR
source ./env.sh

MIRROR=https://mirror.openshift.com/pub/openshift-v4/clients/ocp
OCP_RESOLVED_VERSION=$(curl -sfL "$MIRROR/$OCP_VERSION/release.txt" | awk '/^Name:/{print $2}')
[[ -n $OCP_RESOLVED_VERSION ]] || { echo "Could not resolve OpenShift version $OCP_VERSION" >&2; exit 1; }

install_tools() {
  local capi_version dl
  mkdir -p "$BIN_DIR"
  if [[ ! -x $BIN_DIR/openshift-install ]] ||
    ! openshift-install version | grep -qx "openshift-install $OCP_RESOLVED_VERSION"; then
    log "Installing openshift-install and oc $OCP_RESOLVED_VERSION"
    dl=$(mktemp -d "$HOME/.cache/caplv-aws/dl.XXXXXX")
    (
      cd "$dl"
      curl -sfLO "$MIRROR/$OCP_RESOLVED_VERSION/sha256sum.txt"
      for f in openshift-install-linux.tar.gz openshift-client-linux.tar.gz; do
        curl -sfLO "$MIRROR/$OCP_RESOLVED_VERSION/$f"
        grep " $f\$" sha256sum.txt | sha256sum -c --quiet -
        tar -C "$BIN_DIR" -xzf "$f" --exclude README.md
      done
    )
    rm -rf "$dl"
  fi
  capi_version=$(awk '$1=="sigs.k8s.io/cluster-api"{print $2}' ../../go.mod)
  if [[ ! -x $BIN_DIR/clusterctl ]] || ! clusterctl version -o short | grep -qx "$capi_version"; then
    log "Installing clusterctl $capi_version"
    curl -sfL -o "$BIN_DIR/clusterctl" \
      "https://github.com/kubernetes-sigs/cluster-api/releases/download/$capi_version/clusterctl-linux-amd64"
    chmod +x "$BIN_DIR/clusterctl"
  fi
}

render_configs() {
  local dir=$STATE_DIR/agent
  mkdir -p "$dir"
  # One machine network for the SNO VM and every CAPLV VM, on all hosts.
  cat >"$dir/install-config.yaml" <<EOF
apiVersion: v1
baseDomain: $BASE_DOMAIN
metadata:
  name: $CLUSTER_NAME
controlPlane:
  name: master
  replicas: 1
compute:
  - name: worker
    replicas: 0
networking:
  networkType: OVNKubernetes
  machineNetwork:
    - cidr: $VM_NETWORK_CIDR
  clusterNetwork:
    - cidr: 10.128.0.0/14
      hostPrefix: 23
  serviceNetwork:
    - 172.30.0.0/16
platform:
  none: {}
pullSecret: '$(jq -c . "$PULL_SECRET")'
sshKey: '$(cat "$STATE_DIR/ssh/id_ed25519.pub")'
EOF
  cat >"$dir/agent-config.yaml" <<EOF
apiVersion: v1beta1
kind: AgentConfig
metadata:
  name: $CLUSTER_NAME
rendezvousIP: $SNO_IP
hosts:
  - hostname: $CLUSTER_NAME
    role: master
    rootDeviceHints:
      deviceName: /dev/vda
    interfaces:
      - name: enp1s0
        macAddress: $SNO_MAC
    networkConfig:
      interfaces:
        - name: enp1s0
          type: ethernet
          state: up
          mac-address: $SNO_MAC
          ipv4:
            enabled: true
            dhcp: false
            address:
              - ip: $SNO_IP
                prefix-length: 24
      dns-resolver:
        config:
          server:
            - ${SNO_IP%.*}.1
      routes:
        config:
          - destination: 0.0.0.0/0
            next-hop-address: ${SNO_IP%.*}.1
            next-hop-interface: enp1s0
            table-id: 254
EOF
  chmod 600 "$dir"/*.yaml
}

install_tools
render_configs
if [[ ${1:-} == --render-only ]]; then
  log "Rendered $STATE_DIR/agent for OpenShift $OCP_RESOLVED_VERSION (pull secret redacted)"
  sed "s/^pullSecret: .*/pullSecret: <redacted>/" "$STATE_DIR/agent/install-config.yaml"
  cat "$STATE_DIR/agent/agent-config.yaml"
  exit 0
fi

read -r _ SNO_HOST _ < <(head -1 "$HOSTS_DIR/hosts")
# shellcheck disable=SC2029 # the commands are meant for the remote shell
remote() { ssh "${SSH_OPTS[@]}" "ec2-user@$SNO_HOST" "$@"; }

log "Creating the agent ISO on the SNO host"
# Start over unless the ISO was made; a failed run leaves installer state.
remote 'test -f /var/lib/libvirt/images/agent.x86_64.iso || rm -rf agent; mkdir -p -m 700 agent'
# The installer consumes its inputs, so give it copies.
scp -q "${SSH_OPTS[@]}" "$STATE_DIR/agent/install-config.yaml" "$STATE_DIR/agent/agent-config.yaml" \
  "ec2-user@$SNO_HOST:agent/"
remote bash -s -- "$OCP_RESOLVED_VERSION" "$MIRROR" <<'REMOTE'
set -euo pipefail
version=$1 mirror=$2
sudo dnf -y -q install nmstate virt-install >/dev/null
# The agent installer runs oc to extract the base ISO from the release.
mkdir -p bin
if [[ ! -x bin/openshift-install ]] || ! bin/openshift-install version | grep -qx "bin/openshift-install $version"; then
  curl -sfL "$mirror/$version/openshift-install-linux.tar.gz" | tar -C bin -xz openshift-install
  curl -sfL "$mirror/$version/openshift-client-linux.tar.gz" | tar -C bin -xz oc
fi
if [[ ! -f /var/lib/libvirt/images/agent.x86_64.iso ]]; then
  PATH=$PWD/bin:$PATH openshift-install agent create image --dir agent --log-level warn
  sudo mv agent/agent.x86_64.iso /var/lib/libvirt/images/
fi
REMOTE

log "Starting the SNO VM ($SNO_VCPUS vCPUs, $SNO_MEMORY_MB MB)"
remote bash -s -- "$CLUSTER_NAME" "$SNO_VCPUS" "$SNO_MEMORY_MB" "$SNO_MAC" "$SNO_IP" <<'REMOTE'
set -euo pipefail
name=$1 vcpus=$2 memory=$3 mac=$4 ip=$5
if ! sudo virsh dominfo "$name" >/dev/null 2>&1; then
  # The empty disk boots first, so the ISO runs once; after installing,
  # the VM boots from disk. --import rather than --cdrom: a --cdrom install
  # is destroyed, not restarted, when the installer reboots it.
  # q35, so the NIC is enp1s0 as agent-config.yaml names it (and as CAPLV's
  # VMs have it); the installed system matches its network config by name.
  sudo virt-install --name "$name" --vcpus "$vcpus" --memory "$memory" --cpu host-passthrough --machine q35 \
    --disk size=120,bus=virtio --disk /var/lib/libvirt/images/agent.x86_64.iso,device=cdrom \
    --import --boot hd,cdrom \
    --network "network=caplv,mac=$mac,model=virtio" --osinfo detect=on,require=off \
    --graphics none --console pty --noautoconsole --autostart >/dev/null
fi
# Forward the API and the console to the VM.
sudo tee /etc/caplv-sno-forward.nft >/dev/null <<EOF
table ip caplv_sno_forward
delete table ip caplv_sno_forward
table ip caplv_sno_forward {
  chain prerouting {
    type nat hook prerouting priority dstnat; policy accept;
    fib daddr type local tcp dport { 80, 443, 6443 } dnat to $ip
  }
}
EOF
sudo tee /etc/systemd/system/caplv-sno-forward.service >/dev/null <<EOF
[Unit]
Description=Forward the OpenShift API and console to the SNO VM
After=network-online.target libvirtd.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/nft -f /etc/caplv-sno-forward.nft
ExecStop=/usr/sbin/nft delete table ip caplv_sno_forward
[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable --now caplv-sno-forward.service
sudo systemctl restart caplv-sno-forward.service
REMOTE

mkdir -p "$STATE_DIR/install/auth"
scp -q "${SSH_OPTS[@]}" "ec2-user@$SNO_HOST:agent/auth/*" "$STATE_DIR/install/auth/"
chmod 600 "$STATE_DIR/install/auth/"*

log "Installing OpenShift $OCP_RESOLVED_VERSION (about 45 minutes)"
remote 'bin/openshift-install agent wait-for install-complete --dir agent --log-level info'
oc get clusterversion
