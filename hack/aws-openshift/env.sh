# shellcheck shell=bash disable=SC2034 # the scripts that source this use the settings
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

# Settings shared by the hack/aws-openshift scripts. Source it; every value
# can be overridden from the environment.

export AWS_PROFILE=${AWS_PROFILE:-caplv}
export AWS_REGION=${AWS_REGION:-us-east-2}
export AWS_PAGER=""

CLUSTER_NAME=${CLUSTER_NAME:-caplv-sno}
# "latest" follows the newest OpenShift release on mirror.openshift.com.
OCP_VERSION=${OCP_VERSION:-latest}
PULL_SECRET=${PULL_SECRET:-$HOME/.openshift-pull-secret}
CAPLV_VERSION=${CAPLV_VERSION:-latest}

# The hosts need an instance type with nested virtualization (C8i/M8i/R8i
# at the time of writing). Host 0 runs the single-node OpenShift VM; the
# libvirt hosts, one per release in HOST_RHEL_MAJORS, run CAPLV's VMs.
SNO_HOST_INSTANCE_TYPE=${SNO_HOST_INSTANCE_TYPE:-m8i.4xlarge}
HOST_INSTANCE_TYPE=${HOST_INSTANCE_TYPE:-m8i.2xlarge}
HOST_RHEL_MAJORS=${HOST_RHEL_MAJORS:-"10 9"}
# The libvirt hosts' AMI family: RHEL, or RHEL_HA for the pay-as-you-go
# images with the High Availability Add-On (Pacemaker), which pgha.sh needs.
HOST_IMAGE_FAMILY=${HOST_IMAGE_FAMILY:-RHEL}
# The SNO VM's size, within the SNO host.
SNO_VCPUS=${SNO_VCPUS:-12}
SNO_MEMORY_MB=${SNO_MEMORY_MB:-49152}

VPC_CIDR=10.0.0.0/16
# Host N routes 10.200.N.0/24 to its VMs. The whole range is the cluster's
# machine network.
VM_NETWORK_PREFIX=${VM_NETWORK_PREFIX:-10.200}
VM_NETWORK_CIDR="${VM_NETWORK_PREFIX}.0.0/16"
SNO_IP="${VM_NETWORK_PREFIX}.0.10"
SNO_MAC=52:54:00:c4:00:10

STATE_DIR=${STATE_DIR:-$HOME/.cache/caplv-aws/$CLUSTER_NAME}
HOSTS_DIR="$STATE_DIR/hosts"
BIN_DIR=${BIN_DIR:-$HOME/.cache/caplv-aws/bin}
export PATH="$BIN_DIR:$PATH"
export KUBECONFIG="$STATE_DIR/install/auth/kubeconfig"

# Tag on everything these scripts create, so it can be found and removed.
TAG_KEY=caplv-e2e

if [[ -z ${BASE_DOMAIN:-} ]]; then
  # shellcheck disable=SC2016 # a JMESPath literal, not shell
  BASE_DOMAIN=$(aws route53 list-hosted-zones \
    --query 'HostedZones[?Config.PrivateZone==`false`].Name' --output text | tr '\t' '\n' | sed 's/\.$//')
  if [[ $(wc -w <<<"$BASE_DOMAIN") -ne 1 ]]; then
    echo "Set BASE_DOMAIN to one of the account's public Route53 zones: $BASE_DOMAIN" >&2
    exit 1
  fi
fi

CLUSTER_DOMAIN="$CLUSTER_NAME.$BASE_DOMAIN"

SSH_OPTS=(-i "$STATE_DIR/ssh/id_ed25519" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new
  -o UserKnownHostsFile="$HOSTS_DIR/known_hosts" -o ConnectTimeout=10)

log() { printf '\n==> %s\n' "$*"; }
