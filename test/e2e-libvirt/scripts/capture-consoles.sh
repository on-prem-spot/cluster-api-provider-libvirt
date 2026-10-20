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

# Copies the serial console of every running libvirt domain to
# <dir>/console-<domain>.log until killed, so the guests' boot and cloud-init
# output survives the VMs being deleted. Run in the background on the host.
#
# Usage: capture-consoles.sh <dir>

set -uo pipefail

dir=${1:?usage: capture-consoles.sh <dir>}
mkdir -p "$dir"
declare -A capturing

while true; do
  for domain in $(sudo virsh list --name 2>/dev/null); do
    pty=$(sudo virsh ttyconsole "$domain" 2>/dev/null) || continue
    key="$domain:$pty"
    if [[ -z "${capturing[$key]:-}" ]]; then
      # Only the pty needs root; the log stays owned by the invoking user.
      # shellcheck disable=SC2024
      sudo sh -c "exec cat '$pty'" >>"$dir/console-$domain.log" 2>/dev/null &
      capturing[$key]=1
    fi
  done
  sleep 5
done
