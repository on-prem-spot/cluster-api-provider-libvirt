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

# infra.sh creates the AWS side: a VPC with one public subnet, and RHEL EC2
# instances with nested virtualization. Host 0 runs the single-node
# OpenShift VM (sno.sh); hosts 1.. are CAPLV's libvirt hosts, one per release
# in HOST_RHEL_MAJORS. Host N routes 10.200.N.0/24 to its VMs and the VPC
# routes that range to it, so the cluster and the VMs reach each other
# directly. DNS: a private zone for the cluster inside the VPC, and public
# api/*.apps records, open only to this machine's address, for the console.
# The hosts are prepared with CAPLV's own deploy/ansible/setup-host.yaml.

set -euo pipefail
cd "$(dirname "$0")"
# shellcheck source-path=SCRIPTDIR
source ./env.sh

mkdir -p "$HOSTS_DIR" "$STATE_DIR/ssh"
[[ -f $STATE_DIR/ssh/id_ed25519 ]] ||
  ssh-keygen -q -t ed25519 -N "" -C "$CLUSTER_NAME" -f "$STATE_DIR/ssh/id_ed25519"

tags() {
  printf 'ResourceType=%s,Tags=[{Key=Name,Value=%s},{Key=%s,Value=%s}]' "$1" "$2" "$TAG_KEY" "$CLUSTER_NAME"
}
# tagged prints the ID of the resource of a kind (an aws ec2 describe-*
# command and the query of its ID) with this environment's Name tag.
tagged() {
  aws ec2 "describe-$1" --filters "Name=tag:$TAG_KEY,Values=$CLUSTER_NAME" "Name=tag:Name,Values=$2" \
    --query "$3" --output text
}

log "VPC"
VPC_ID=$(tagged vpcs "$CLUSTER_NAME" 'Vpcs[0].VpcId')
if [[ $VPC_ID == None ]]; then
  VPC_ID=$(aws ec2 create-vpc --cidr-block "$VPC_CIDR" --tag-specifications "$(tags vpc "$CLUSTER_NAME")" \
    --query Vpc.VpcId --output text)
  aws ec2 wait vpc-available --vpc-ids "$VPC_ID"
  aws ec2 modify-vpc-attribute --vpc-id "$VPC_ID" --enable-dns-hostnames
fi
IGW_ID=$(tagged internet-gateways "$CLUSTER_NAME" 'InternetGateways[0].InternetGatewayId')
if [[ $IGW_ID == None ]]; then
  IGW_ID=$(aws ec2 create-internet-gateway --tag-specifications "$(tags internet-gateway "$CLUSTER_NAME")" \
    --query InternetGateway.InternetGatewayId --output text)
  aws ec2 attach-internet-gateway --internet-gateway-id "$IGW_ID" --vpc-id "$VPC_ID"
fi
SUBNET_ID=$(tagged subnets "$CLUSTER_NAME" 'Subnets[0].SubnetId')
if [[ $SUBNET_ID == None ]]; then
  az=$(aws ec2 describe-availability-zones --query 'AvailabilityZones[0].ZoneName' --output text)
  SUBNET_ID=$(aws ec2 create-subnet --vpc-id "$VPC_ID" --cidr-block "${VPC_CIDR%.0.0/16}.1.0/24" \
    --availability-zone "$az" --tag-specifications "$(tags subnet "$CLUSTER_NAME")" \
    --query Subnet.SubnetId --output text)
  aws ec2 modify-subnet-attribute --subnet-id "$SUBNET_ID" --map-public-ip-on-launch
fi
RT_ID=$(tagged route-tables "$CLUSTER_NAME" 'RouteTables[0].RouteTableId')
if [[ $RT_ID == None ]]; then
  RT_ID=$(aws ec2 create-route-table --vpc-id "$VPC_ID" --tag-specifications "$(tags route-table "$CLUSTER_NAME")" \
    --query RouteTable.RouteTableId --output text)
  aws ec2 create-route --route-table-id "$RT_ID" --destination-cidr-block 0.0.0.0/0 --gateway-id "$IGW_ID" >/dev/null
  aws ec2 associate-route-table --route-table-id "$RT_ID" --subnet-id "$SUBNET_ID" >/dev/null
fi

SG_ID=$(tagged security-groups "$CLUSTER_NAME" 'SecurityGroups[0].GroupId')
if [[ $SG_ID == None ]]; then
  SG_ID=$(aws ec2 create-security-group --vpc-id "$VPC_ID" --group-name "$CLUSTER_NAME" \
    --description "CAPLV OpenShift test hosts" --tag-specifications "$(tags security-group "$CLUSTER_NAME")" \
    --query GroupId --output text)
  aws ec2 authorize-security-group-ingress --group-id "$SG_ID" --ip-permissions \
    "IpProtocol=-1,IpRanges=[{CidrIp=$VPC_CIDR,Description=VPC},{CidrIp=$VM_NETWORK_CIDR,Description=VMs}]" >/dev/null
fi
# SSH for setup, and the API and console (forwarded to the SNO VM), only
# from here.
MY_IP=$(curl -sf https://checkip.amazonaws.com)
for port in 22 80 443 6443; do
  aws ec2 authorize-security-group-ingress --group-id "$SG_ID" --ip-permissions \
    "IpProtocol=tcp,FromPort=$port,ToPort=$port,IpRanges=[{CidrIp=$MY_IP/32,Description=operator}]" \
    >/dev/null 2>&1 || true
done

KEY_NAME=$CLUSTER_NAME
aws ec2 describe-key-pairs --key-names "$KEY_NAME" >/dev/null 2>&1 ||
  aws ec2 import-key-pair --key-name "$KEY_NAME" --public-key-material \
    "fileb://$STATE_DIR/ssh/id_ed25519.pub" --tag-specifications "$(tags key-pair "$KEY_NAME")" >/dev/null

newest_rhel_ami() {
  local name
  name=$(aws ec2 describe-images --owners 309956199498 \
    --filters "Name=name,Values=RHEL-$1.*_HVM-*-x86_64-*-Hourly2-GP3" Name=state,Values=available \
    --query 'Images[].Name' --output text | tr '\t' '\n' | sort -V | tail -1)
  aws ec2 describe-images --owners 309956199498 --filters "Name=name,Values=$name" \
    --query 'Images[0].ImageId' --output text
}

# create_host NAME INDEX TYPE RHEL_MAJOR DISK_GB creates (or finds) a host
# and routes its VM subnet to it, and appends it to the hosts file.
create_host() {
  local name=$1 n=$2 type=$3 major=$4 disk=$5 instance eni private_ip public_ip
  instance=$(aws ec2 describe-instances --filters "Name=tag:$TAG_KEY,Values=$CLUSTER_NAME" \
    "Name=tag:Name,Values=$name" "Name=instance-state-name,Values=pending,running" \
    --query 'Reservations[0].Instances[0].InstanceId' --output text)
  if [[ $instance == None ]]; then
    local ami
    ami=$(newest_rhel_ami "$major")
    log "Creating $name ($type, RHEL $major, $ami)"
    instance=$(aws ec2 run-instances --image-id "$ami" --instance-type "$type" \
      --cpu-options NestedVirtualization=enabled --key-name "$KEY_NAME" \
      --network-interfaces "DeviceIndex=0,SubnetId=$SUBNET_ID,Groups=$SG_ID,AssociatePublicIpAddress=true" \
      --block-device-mappings "DeviceName=/dev/sda1,Ebs={VolumeSize=$disk,VolumeType=gp3,DeleteOnTermination=true}" \
      --metadata-options HttpTokens=required \
      --tag-specifications "$(tags instance "$name")" "$(tags volume "$name")" \
      --query 'Instances[0].InstanceId' --output text)
  fi
  aws ec2 wait instance-running --instance-ids "$instance"
  read -r eni private_ip public_ip < <(aws ec2 describe-instances --instance-ids "$instance" \
    --query 'Reservations[0].Instances[0].[NetworkInterfaces[0].NetworkInterfaceId,PrivateIpAddress,PublicIpAddress]' \
    --output text)
  # The host forwards its VMs' traffic, so the VPC must deliver packets for
  # addresses other than the host's own, and route the VMs' range to it.
  aws ec2 modify-network-interface-attribute --network-interface-id "$eni" --no-source-dest-check
  aws ec2 replace-route --route-table-id "$RT_ID" --destination-cidr-block "$VM_NETWORK_PREFIX.$n.0/24" \
    --network-interface-id "$eni" 2>/dev/null ||
    aws ec2 create-route --route-table-id "$RT_ID" --destination-cidr-block "$VM_NETWORK_PREFIX.$n.0/24" \
      --network-interface-id "$eni" >/dev/null
  echo "$name $public_ip $private_ip $VM_NETWORK_PREFIX.$n" >>"$HOSTS_DIR/hosts"
  log "$name: $instance, $private_ip (public $public_ip), VMs on $VM_NETWORK_PREFIX.$n.0/24"
}

: >"$HOSTS_DIR/hosts"
create_host "$CLUSTER_NAME-sno-host" 0 "$SNO_HOST_INSTANCE_TYPE" 10 250
n=0
for major in $HOST_RHEL_MAJORS; do
  n=$((n + 1))
  create_host "$CLUSTER_NAME-libvirt-$n" "$n" "$HOST_INSTANCE_TYPE" "$major" 150
done
read -r _ SNO_HOST_PUBLIC_IP _ < <(head -1 "$HOSTS_DIR/hosts")

log "DNS for $CLUSTER_DOMAIN"
PRIVATE_ZONE=$(aws route53 list-hosted-zones-by-name --dns-name "$CLUSTER_DOMAIN." \
  --query "HostedZones[?Name=='$CLUSTER_DOMAIN.' && Config.PrivateZone].Id | [0]" --output text)
if [[ $PRIVATE_ZONE == None ]]; then
  PRIVATE_ZONE=$(aws route53 create-hosted-zone --name "$CLUSTER_DOMAIN" \
    --vpc "VPCRegion=$AWS_REGION,VPCId=$VPC_ID" --caller-reference "$CLUSTER_NAME-$(date +%s)" \
    --hosted-zone-config "Comment=$TAG_KEY=$CLUSTER_NAME,PrivateZone=true" \
    --query HostedZone.Id --output text)
fi
PUBLIC_ZONE=$(aws route53 list-hosted-zones-by-name --dns-name "$BASE_DOMAIN." \
  --query "HostedZones[?Name=='$BASE_DOMAIN.' && Config.PrivateZone==\`false\`].Id | [0]" --output text)
# upsert_a ZONE NAME ADDRESS
upsert_a() {
  aws route53 change-resource-record-sets --hosted-zone-id "$1" --change-batch \
    "{\"Changes\":[{\"Action\":\"UPSERT\",\"ResourceRecordSet\":{\"Name\":\"$2\",\"Type\":\"A\",\"TTL\":60,\"ResourceRecords\":[{\"Value\":\"$3\"}]}}]}" \
    >/dev/null
}
for name in api api-int '*.apps'; do
  upsert_a "$PRIVATE_ZONE" "$name.$CLUSTER_DOMAIN" "$SNO_IP"
done
for name in api '*.apps'; do
  upsert_a "$PUBLIC_ZONE" "$name.$CLUSTER_DOMAIN" "$SNO_HOST_PUBLIC_IP"
done

# Ansible inventory: every host is prepared with libvirt; the libvirt group
# is what CAPLV manages.
{
  echo "[sno]"
  head -1 "$HOSTS_DIR/hosts" | awk '{print $1" ansible_host="$2" vm_subnet="$4}'
  echo "[libvirt]"
  tail -n +2 "$HOSTS_DIR/hosts" | awk '{print $1" ansible_host="$2" vm_subnet="$4}'
} >"$HOSTS_DIR/inventory"

while read -r _ public_ip _; do
  until ssh -n "${SSH_OPTS[@]}" "ec2-user@$public_ip" true 2>/dev/null; do sleep 5; done
done <"$HOSTS_DIR/hosts"

log "Preparing the hosts with deploy/ansible/setup-host.yaml"
cp -n ~/.cache/caplv-aws/caplv-key "$HOSTS_DIR/caplv-key" 2>/dev/null || true
cp -n ~/.cache/caplv-aws/caplv-key.pub "$HOSTS_DIR/caplv-key.pub" 2>/dev/null || true
ANSIBLE_HOST_KEY_CHECKING=False ansible-playbook -i "$HOSTS_DIR/inventory" -u ec2-user \
  --private-key "$STATE_DIR/ssh/id_ed25519" -e ssh_key_path="$HOSTS_DIR/caplv-key" \
  ../../deploy/ansible/setup-host.yaml

log "Defining the routed libvirt network on each host"
ANSIBLE_HOST_KEY_CHECKING=False ansible-playbook -i "$HOSTS_DIR/inventory" -u ec2-user \
  --private-key "$STATE_DIR/ssh/id_ed25519" -e "vpc_cidr=$VPC_CIDR vm_network_cidr=$VM_NETWORK_CIDR sno_ip=$SNO_IP sno_mac=$SNO_MAC" \
  routed-network.yaml

# The libvirt hosts' SSH host key fingerprints, one line per host and key
# type, for LibvirtHost.spec.hostKeyFingerprint.
: >"$HOSTS_DIR/fingerprints"
tail -n +2 "$HOSTS_DIR/hosts" | while read -r name public_ip _; do
  for type in ed25519 ecdsa rsa; do
    fp=$(ssh -n "${SSH_OPTS[@]}" "ec2-user@$public_ip" \
      ssh-keygen -lf "/etc/ssh/ssh_host_${type}_key.pub" | awk '{print $2}')
    echo "$name $type $fp" >>"$HOSTS_DIR/fingerprints"
  done
done
cat "$HOSTS_DIR/hosts"
