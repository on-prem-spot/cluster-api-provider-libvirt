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

# Prepares the machine it runs on as the libvirt host for the e2e suite and
# prints the host variables the suite reads (also appended to $GITHUB_ENV
# when set). Written for GitHub's Ubuntu 24.04 runners; uses sudo. It
# changes the system (packages, a user, sshd config, firewall rules), so run
# it only on a throwaway machine.
#
# Environment:
#   WORK_DIR            holds the SSH key and the built image, which CI caches
#   KUBERNETES_VERSION  baked into the base image; default from the e2e config
#   BASE_IMAGE          base image volume name; default from the e2e config
#
# The kind management cluster reaches the host over SSH at its primary
# address, and the workload VMs on the caplv-e2e libvirt network, which this
# script opens to forwarded traffic.

set -euo pipefail

: "${WORK_DIR:?}"

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
E2E_CONFIG="$SCRIPT_DIR/../config/libvirt.yaml"

# config_variable prints a top-level variable from the e2e config.
config_variable() {
  awk -v name="$1" '$1 == name ":" {print $2}' "$E2E_CONFIG"
}

KUBERNETES_VERSION=${KUBERNETES_VERSION:-$(config_variable KUBERNETES_VERSION)}
BASE_IMAGE=${BASE_IMAGE:-$(config_variable BASE_IMAGE)}
: "${KUBERNETES_VERSION:?}" "${BASE_IMAGE:?}"
SSH_USER=caplv
NETWORK_NAME=caplv-e2e
NETWORK_BRIDGE=virbr-e2e
NETWORK_PREFIX=192.168.150
# Memory each VM gets; each LibvirtHost reserves the rest of the host's.
VM_MEMORY_MB=4096
UBUNTU_IMAGE_URL=https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img

log() { echo "==> $*" >&2; }

emit() {
  echo "$1=$2"
  if [[ -n "${GITHUB_ENV:-}" ]]; then
    echo "$1=$2" >>"$GITHUB_ENV"
  fi
}

install_packages() {
  log "Installing libvirt, QEMU, OVMF and virt-install"
  sudo apt-get update -q
  sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -q \
    qemu-system-x86 qemu-utils libvirt-daemon-system libvirt-clients ovmf \
    virtinst cloud-image-utils openssh-server

  # GitHub's runners expose /dev/kvm but only to root and the kvm group.
  echo 'KERNEL=="kvm", GROUP="kvm", MODE="0666", OPTIONS+="static_node=kvm"' |
    sudo tee /etc/udev/rules.d/99-kvm4all.rules >/dev/null
  sudo udevadm control --reload-rules
  sudo udevadm trigger --name-match=kvm
  virsh -c qemu:///system version >&2 || sudo virsh version >&2
}

setup_ssh() {
  log "Creating the $SSH_USER service account and its SSH key"
  mkdir -p "$WORK_DIR"
  if [[ ! -f "$WORK_DIR/id_ed25519" ]]; then
    ssh-keygen -q -t ed25519 -N '' -C caplv-e2e -f "$WORK_DIR/id_ed25519"
  fi
  if ! id "$SSH_USER" >/dev/null 2>&1; then
    sudo useradd -m -s /bin/bash -G libvirt,kvm "$SSH_USER"
  fi
  sudo install -d -m 0700 -o "$SSH_USER" -g "$SSH_USER" "/home/$SSH_USER/.ssh"
  sudo install -m 0600 -o "$SSH_USER" -g "$SSH_USER" "$WORK_DIR/id_ed25519.pub" \
    "/home/$SSH_USER/.ssh/authorized_keys"

  # Offer only the ed25519 host key, so the fingerprint below is the one
  # CAPLV's SSH client is shown whatever algorithm it prefers.
  echo 'HostKey /etc/ssh/ssh_host_ed25519_key' |
    sudo tee /etc/ssh/sshd_config.d/10-caplv-e2e.conf >/dev/null
  sudo systemctl restart ssh.service
}

setup_network() {
  log "Defining the $NETWORK_NAME libvirt network"
  if ! sudo virsh net-info "$NETWORK_NAME" >/dev/null 2>&1; then
    local xml
    xml=$(mktemp)
    # Static machine addresses (.10, .11) sit outside the DHCP range that
    # cloud-init's first-boot fallback uses.
    cat >"$xml" <<EOF
<network>
  <name>$NETWORK_NAME</name>
  <forward mode='nat'/>
  <bridge name='$NETWORK_BRIDGE' stp='on' delay='0'/>
  <ip address='$NETWORK_PREFIX.1' netmask='255.255.255.0'>
    <dhcp>
      <range start='$NETWORK_PREFIX.100' end='$NETWORK_PREFIX.200'/>
    </dhcp>
  </ip>
</network>
EOF
    sudo virsh net-define "$xml"
    rm -f "$xml"
  fi
  sudo virsh net-start "$NETWORK_NAME" 2>/dev/null || true
  sudo virsh net-autostart "$NETWORK_NAME"

  # A NAT network rejects connections from outside. The management cluster
  # (in a kind container) needs to reach the workload API server, so accept
  # forwarded traffic to the network ahead of libvirt's and Docker's rules.
  sudo iptables -I LIBVIRT_FWI 1 -d "$NETWORK_PREFIX.0/24" -o "$NETWORK_BRIDGE" -j ACCEPT
  sudo iptables -I DOCKER-USER 1 -d "$NETWORK_PREFIX.0/24" -j ACCEPT 2>/dev/null || true
  sudo iptables -I DOCKER-USER 1 -s "$NETWORK_PREFIX.0/24" -j ACCEPT 2>/dev/null || true
}

setup_pool() {
  log "Preparing the default storage pool"
  if ! sudo virsh pool-info default >/dev/null 2>&1; then
    sudo virsh pool-define-as default dir --target /var/lib/libvirt/images
    sudo virsh pool-build default
  fi
  sudo virsh pool-start default 2>/dev/null || true
  sudo virsh pool-autostart default
}

build_base_image() {
  local image="$WORK_DIR/images/$BASE_IMAGE"
  if [[ -f "$image" ]]; then
    log "Using cached base image $image"
    return
  fi

  # Boot the cloud image once on the caplv-e2e network and let cloud-init
  # run bake-image.sh, then reset cloud-init and power off, so the disk
  # boots as a fresh instance next time. (virt-customize would avoid the
  # boot, but Ubuntu's libguestfs cannot give its appliance a network on
  # these runners.)
  log "Building $BASE_IMAGE with Kubernetes $KUBERNETES_VERSION"
  local version=${KUBERNETES_VERSION#v}
  local images=/var/lib/libvirt/images
  local tmp
  tmp=$(mktemp -d)
  curl -fsSL -o "$tmp/base.img" "$UBUNTU_IMAGE_URL"
  # Room for the packages; cloud-init grows the root filesystem to fit.
  qemu-img resize "$tmp/base.img" +3G
  sudo install -m 0644 "$tmp/base.img" "$images/caplv-e2e-bake.qcow2"

  cat >"$tmp/user-data" <<USERDATA
#cloud-config
write_files:
  - path: /root/bake-image.sh
    permissions: "0755"
    encoding: b64
    content: $(base64 -w0 "$SCRIPT_DIR/bake-image.sh")
runcmd:
  # Output reaches the serial console through cloud-final.service.
  - - sh
    - -c
    - >-
      K8S_MINOR=${version%.*} K8S_PACKAGE_VERSION=${version}-1.1 /root/bake-image.sh 2>&1
      && echo CAPLV-BAKE-OK || echo CAPLV-BAKE-FAILED
  - [sh, -c, "rm -f /root/bake-image.sh && cloud-init clean --logs --machine-id"]
power_state:
  mode: poweroff
  timeout: 30
USERDATA
  printf 'instance-id: caplv-e2e-bake\nlocal-hostname: caplv-e2e-bake\n' >"$tmp/meta-data"
  cloud-localds "$tmp/seed.iso" "$tmp/user-data" "$tmp/meta-data"
  sudo install -m 0644 "$tmp/seed.iso" "$images/caplv-e2e-bake-seed.iso"

  local console="$WORK_DIR/bake-console.log"
  sudo virt-install --name caplv-e2e-bake --memory 2048 --vcpus 2 --import \
    --osinfo ubuntu24.04 \
    --disk "path=$images/caplv-e2e-bake.qcow2,format=qcow2" \
    --disk "path=$images/caplv-e2e-bake-seed.iso,device=cdrom" \
    --network network="$NETWORK_NAME" \
    --graphics none --serial "file,path=$console" --noautoconsole

  local deadline=$((SECONDS + 1200))
  until [[ "$(sudo virsh domstate caplv-e2e-bake)" == "shut off" ]]; do
    if ((SECONDS > deadline)); then
      sudo virsh destroy caplv-e2e-bake || true
      break
    fi
    sleep 10
  done
  sudo virsh undefine caplv-e2e-bake
  sudo cat "$console" >&2 || true
  if ! sudo grep -q CAPLV-BAKE-OK "$console"; then
    echo "Building the base image failed; the console log is above" >&2
    exit 1
  fi

  mkdir -p "$WORK_DIR/images"
  sudo mv "$images/caplv-e2e-bake.qcow2" "$image"
  sudo chown "$(id -u):$(id -g)" "$image"
  sudo rm -f "$images/caplv-e2e-bake-seed.iso"
  rm -rf "$tmp"
}

install_base_image() {
  log "Uploading $BASE_IMAGE to the default pool"
  sudo install -m 0644 "$WORK_DIR/images/$BASE_IMAGE" "/var/lib/libvirt/images/$BASE_IMAGE"
  sudo virsh pool-refresh default
}

emit_variables() {
  local host_ip fingerprint total_mb cpus
  host_ip=$(ip -4 route get 1.1.1.1 | awk '{for (i = 1; i < NF; i++) if ($i == "src") print $(i + 1)}')
  fingerprint=$(ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub | awk '{print $2}')
  total_mb=$(($(sudo virsh nodeinfo | awk '/Memory size/ {print $3}') / 1024))
  cpus=$(nproc)

  log "Checking SSH and libvirt access as $SSH_USER@$host_ip"
  ssh -i "$WORK_DIR/id_ed25519" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    "$SSH_USER@$host_ip" virsh -c qemu:///system list --all >&2

  local uri="qemu+ssh://$SSH_USER@$host_ip/system"
  emit CONTROL_PLANE_LIBVIRT_HOST_URI "$uri"
  emit WORKER_LIBVIRT_HOST_URI "$uri"
  emit CONTROL_PLANE_LIBVIRT_HOST_SSH_FINGERPRINT "$fingerprint"
  emit WORKER_LIBVIRT_HOST_SSH_FINGERPRINT "$fingerprint"
  emit LIBVIRT_HOST_SSH_PRIVATE_KEY_PATH "$WORK_DIR/id_ed25519"
  emit LIBVIRT_HOST_RESERVED_MEMORY_MB "$((total_mb - VM_MEMORY_MB))"
  emit LIBVIRT_HOST_RESERVED_VCPUS "$((cpus > 2 ? cpus - 2 : 0))"
}

install_packages
setup_ssh
setup_network
setup_pool
build_base_image
install_base_image
emit_variables
