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

# The Enterprise Linux counterpart of bake-ubuntu.sh: runs inside a CentOS
# Stream (or other RHEL-compatible) cloud image on its first boot, from
# cloud-init (see setup-host.sh), and installs containerd plus kubeadm,
# kubelet and kubectl, with the kernel settings kubeadm checks for.
#
# Environment:
#   K8S_MINOR            e.g. 1.36, selects the pkgs.k8s.io repository
#   K8S_PACKAGE_VERSION  e.g. 1.36.5

set -euxo pipefail

: "${K8S_MINOR:?}" "${K8S_PACKAGE_VERSION:?}"

# kubeadm's installation guide runs SELinux permissive on Enterprise Linux.
setenforce 0 || true
sed -i 's/^SELINUX=enforcing$/SELINUX=permissive/' /etc/selinux/config

dnf -y install dnf-plugins-core
# containerd from Docker's repository; Enterprise Linux does not ship it.
dnf config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo
cat >/etc/yum.repos.d/kubernetes.repo <<EOF
[kubernetes]
name=Kubernetes
baseurl=https://pkgs.k8s.io/core:/stable:/v${K8S_MINOR}/rpm/
enabled=1
gpgcheck=1
gpgkey=https://pkgs.k8s.io/core:/stable:/v${K8S_MINOR}/rpm/repodata/repomd.xml.key
exclude=kubelet kubeadm kubectl cri-tools kubernetes-cni
EOF

dnf -y install --disableexcludes=kubernetes containerd.io \
  "kubelet-${K8S_PACKAGE_VERSION}" "kubeadm-${K8S_PACKAGE_VERSION}" "kubectl-${K8S_PACKAGE_VERSION}"

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
dnf clean all

# Each VM must generate its own machine ID at first boot; see bake-ubuntu.sh.
truncate -s 0 /etc/machine-id
