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

# down.sh removes everything infra.sh created, which takes the SNO VM and
# the CAPLV VMs with it: the instances, the DNS records and private zone,
# and the VPC. It then lists anything still carrying the caplv-e2e tag.

set -euo pipefail
cd "$(dirname "$0")"
# shellcheck source-path=SCRIPTDIR
source ./env.sh

tagged() {
  aws ec2 "describe-$1" --filters "Name=tag:$TAG_KEY,Values=$CLUSTER_NAME" --query "$2" --output text
}

instances=$(aws ec2 describe-instances --filters "Name=tag:$TAG_KEY,Values=$CLUSTER_NAME" \
  "Name=instance-state-name,Values=pending,running,stopping,stopped" \
  --query 'Reservations[].Instances[].InstanceId' --output text)
if [[ -n $instances ]]; then
  log "Terminating $instances"
  # shellcheck disable=SC2086 # one argument per instance
  aws ec2 terminate-instances --instance-ids $instances >/dev/null
  # shellcheck disable=SC2086
  aws ec2 wait instance-terminated --instance-ids $instances
fi

log "Deleting DNS records"
# delete_records ZONE: removes the zone's A records under the cluster domain.
delete_records() {
  local changes
  changes=$(aws route53 list-resource-record-sets --hosted-zone-id "$1" --output json |
    jq -c --arg d "$CLUSTER_DOMAIN." '[.ResourceRecordSets[] | select(.Type=="A" and (.Name|endswith($d)))
      | {Action:"DELETE", ResourceRecordSet:.}]')
  [[ $changes == "[]" ]] ||
    aws route53 change-resource-record-sets --hosted-zone-id "$1" \
      --change-batch "{\"Changes\":$changes}" >/dev/null
}
public_zone=$(aws route53 list-hosted-zones-by-name --dns-name "$BASE_DOMAIN." \
  --query "HostedZones[?Name=='$BASE_DOMAIN.' && Config.PrivateZone==\`false\`].Id | [0]" --output text)
[[ $public_zone == None ]] || delete_records "$public_zone"
for zone in $(aws route53 list-hosted-zones --query \
  "HostedZones[?Name=='$CLUSTER_DOMAIN.' && Config.PrivateZone==\`true\` && Config.Comment=='$TAG_KEY=$CLUSTER_NAME'].Id" \
  --output text); do
  delete_records "$zone"
  aws route53 delete-hosted-zone --id "$zone" >/dev/null
done

log "Deleting the VPC"
for sg in $(tagged security-groups 'SecurityGroups[].GroupId'); do
  aws ec2 delete-security-group --group-id "$sg"
done
for rt in $(tagged route-tables 'RouteTables[].RouteTableId'); do
  for assoc in $(aws ec2 describe-route-tables --route-table-ids "$rt" \
    --query 'RouteTables[0].Associations[?!Main].RouteTableAssociationId' --output text); do
    aws ec2 disassociate-route-table --association-id "$assoc"
  done
  aws ec2 delete-route-table --route-table-id "$rt"
done
for subnet in $(tagged subnets 'Subnets[].SubnetId'); do
  aws ec2 delete-subnet --subnet-id "$subnet"
done
for vpc in $(tagged vpcs 'Vpcs[].VpcId'); do
  for igw in $(aws ec2 describe-internet-gateways --filters "Name=attachment.vpc-id,Values=$vpc" \
    --query 'InternetGateways[].InternetGatewayId' --output text); do
    aws ec2 detach-internet-gateway --internet-gateway-id "$igw" --vpc-id "$vpc"
    aws ec2 delete-internet-gateway --internet-gateway-id "$igw"
  done
  aws ec2 delete-vpc --vpc-id "$vpc"
done
aws ec2 delete-key-pair --key-name "$CLUSTER_NAME" >/dev/null 2>&1 || true
rm -rf "$STATE_DIR/install" "$STATE_DIR/agent" "$HOSTS_DIR"

log "Resources still tagged $TAG_KEY=$CLUSTER_NAME (expect none)"
aws resourcegroupstaggingapi get-resources --tag-filters "Key=$TAG_KEY,Values=$CLUSTER_NAME" \
  --query 'ResourceTagMappingList[].ResourceARN' --output text
