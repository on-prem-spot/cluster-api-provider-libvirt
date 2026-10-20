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

# Runs inside an Ubuntu 24.04 cloud image on its first boot, from cloud-init
# (see setup-host.sh), and turns it into a guest the libvirt-kubeadm
# ClusterClass accepts: containerd plus kubeadm, kubelet and kubectl, with the kernel
# settings kubeadm checks for.
#
# Environment:
#   K8S_MINOR            e.g. 1.36, selects the pkgs.k8s.io repository
#   K8S_PACKAGE_VERSION  e.g. 1.36.5-1.1

set -euxo pipefail

: "${K8S_MINOR:?}" "${K8S_PACKAGE_VERSION:?}"
export DEBIAN_FRONTEND=noninteractive
# unattended-upgrades also runs on first boot; wait for its dpkg lock.
echo 'DPkg::Lock::Timeout "600";' >/etc/apt/apt.conf.d/99caplv-lock-timeout

apt-get update -q
apt-get install -y -q ca-certificates curl gpg
install -d -m 0755 /etc/apt/keyrings

# containerd from Docker's repository, for a current release independent of
# the one frozen in the Ubuntu archive.
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
echo "deb [signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu noble stable" \
  >/etc/apt/sources.list.d/docker.list
curl -fsSL "https://pkgs.k8s.io/core:/stable:/v${K8S_MINOR}/deb/Release.key" |
  gpg --dearmor -o /etc/apt/keyrings/kubernetes.gpg
echo "deb [signed-by=/etc/apt/keyrings/kubernetes.gpg] https://pkgs.k8s.io/core:/stable:/v${K8S_MINOR}/deb/ /" \
  >/etc/apt/sources.list.d/kubernetes.list

apt-get update -q
apt-get install -y -q containerd.io \
  "kubelet=${K8S_PACKAGE_VERSION}" "kubeadm=${K8S_PACKAGE_VERSION}" "kubectl=${K8S_PACKAGE_VERSION}"
apt-mark hold containerd.io kubelet kubeadm kubectl

# Docker's package disables the CRI plugin; start from the full default and
# use the systemd cgroup driver, as kubeadm configures the kubelet to.
containerd config default >/etc/containerd/config.toml
sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml
grep -q 'SystemdCgroup = true' /etc/containerd/config.toml

cat >/etc/modules-load.d/kubernetes.conf <<EOF
overlay
br_netfilter
EOF
cat >/etc/sysctl.d/99-kubernetes.conf <<EOF
net.bridge.bridge-nf-call-iptables = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.ipv4.ip_forward = 1
EOF

systemctl enable containerd kubelet
apt-get clean

# Each VM must generate its own machine ID at first boot: the DHCP client
# identifier derives from it, and two VMs sharing one would be handed the same
# lease before switching to their static addresses.
truncate -s 0 /etc/machine-id
