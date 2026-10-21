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

# refresh-ips.sh updates the hosts' public addresses in the state directory
# (hosts, inventory, known_hosts) after instances were stopped and started,
# which gives them new ones: the PostgreSQL HA demo does that on every kill
# and recovery. Hosts are matched by their private address, which stays.

set -euo pipefail
cd "$(dirname "$0")"
# shellcheck source-path=SCRIPTDIR
source ./env.sh

declare -A public_by_private
while read -r private public; do
  public_by_private[$private]=$public
done < <(aws ec2 describe-instances \
  --filters "Name=tag:$TAG_KEY,Values=$CLUSTER_NAME" "Name=instance-state-name,Values=running" \
  --query 'Reservations[].Instances[].[PrivateIpAddress,PublicIpAddress]' --output text)

: >"$HOSTS_DIR/hosts.new"
while read -r name public private subnet; do
  new=${public_by_private[$private]:-$public}
  if [[ $new != "$public" ]]; then
    log "$name: $public -> $new"
    sed -i "s/^$public /$new /" "$HOSTS_DIR/known_hosts"
    sed -i "s/ansible_host=$public /ansible_host=$new /" "$HOSTS_DIR/inventory"
  fi
  echo "$name $new $private $subnet" >>"$HOSTS_DIR/hosts.new"
done <"$HOSTS_DIR/hosts"
mv "$HOSTS_DIR/hosts.new" "$HOSTS_DIR/hosts"
cat "$HOSTS_DIR/hosts"
