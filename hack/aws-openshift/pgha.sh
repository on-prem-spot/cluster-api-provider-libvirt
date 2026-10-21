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

# pgha.sh turns the libvirt hosts into a PostgreSQL cluster managed by
# Pacemaker (RHEL High Availability), whose standby nodes lend their idle
# capacity to OpenShift as CAPLV workers. See docs/demos/postgres-ha.md.
#
#   - Pacemaker over the three hosts, fenced with fence_aws.
#   - PostgreSQL 16 synchronous streaming replication, managed by
#     ocf:heartbeat:pgsql (a commit waits for one standby, which is the one
#     promoted, so a failover loses no committed write):
#     one primary, two standbys, and a virtual IP (awsvip + IPaddr2) that
#     follows the primary.
#   - ocf:caplv:evict (pgha/evict), placed with the primary and started before
#     it is promoted: it hard-kills the host's VMs and cordons its LibvirtHost,
#     so a promoted standby gets its machine back at once.
#   - A two-replica MachineDeployment of RHCOS workers, which CAPLV places on
#     the hosts that are not primary.
#
# The hosts must be RHEL HA images: HOST_IMAGE_FAMILY=RHEL_HA
# HOST_RHEL_MAJORS="10 10 10" ./infra.sh.

set -euo pipefail
cd "$(dirname "$0")"
# shellcheck source-path=SCRIPTDIR
source ./env.sh

NS=caplv-workers
PG_VIP=${PG_VIP:-10.0.1.250}
PG_CLUSTER=caplv-pg
SECRETS="$STATE_DIR/pgha"
install -d -m 700 "$SECRETS"
for secret in hacluster replicator demo; do
  [[ -f $SECRETS/$secret ]] || openssl rand -hex 16 >"$SECRETS/$secret"
done

mapfile -t HOSTS < <(tail -n +2 "$HOSTS_DIR/hosts")
[[ ${#HOSTS[@]} -eq 3 ]] || { echo "pgha.sh needs three libvirt hosts, found ${#HOSTS[@]}" >&2; exit 1; }
NODES=() PUBLIC=() PRIVATE=() SUBNETS=()
for line in "${HOSTS[@]}"; do
  read -r name public private subnet <<<"$line"
  NODES+=("libvirt-${name##*-libvirt-}") PUBLIC+=("$public") PRIVATE+=("$private") SUBNETS+=("$subnet")
done

# on N COMMAND runs COMMAND as root on host N (0-based).
on() {
  local n=$1
  shift
  # ssh hands the remote shell one string, so quote each argument for it.
  # shellcheck disable=SC2029
  ssh -n "${SSH_OPTS[@]}" "ec2-user@${PUBLIC[$n]}" "sudo $(printf '%q ' "$@")"
}
# on_script N runs a bash script from stdin as root on host N.
on_script() {
  # shellcheck disable=SC2029 # expanded here on purpose: quoted for the remote shell
  ssh "${SSH_OPTS[@]}" "ec2-user@${PUBLIC[$1]}" "sudo bash -s -- $(printf '%q ' "${@:2}")"
}
instance_id() {
  aws ec2 describe-instances --filters "Name=tag:$TAG_KEY,Values=$CLUSTER_NAME" \
    "Name=private-ip-address,Values=$1" "Name=instance-state-name,Values=running" \
    --query 'Reservations[0].Instances[0].InstanceId' --output text
}

log "IAM role for fence_aws and awsvip"
ROLE=$CLUSTER_NAME-pacemaker
if ! aws iam get-role --role-name "$ROLE" >/dev/null 2>&1; then
  aws iam create-role --role-name "$ROLE" --tags "Key=$TAG_KEY,Value=$CLUSTER_NAME" \
    --assume-role-policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}' >/dev/null
  aws iam create-instance-profile --instance-profile-name "$ROLE" --tags "Key=$TAG_KEY,Value=$CLUSTER_NAME" >/dev/null
  aws iam add-role-to-instance-profile --instance-profile-name "$ROLE" --role-name "$ROLE"
  sleep 15 # IAM is eventually consistent
fi
# Fencing may stop and start only this environment's instances.
aws iam put-role-policy --role-name "$ROLE" --policy-name pacemaker --policy-document "{
  \"Version\": \"2012-10-17\",
  \"Statement\": [
    {\"Effect\": \"Allow\", \"Action\": [\"ec2:DescribeInstances\", \"ec2:DescribeInstanceAttribute\",
      \"ec2:DescribeNetworkInterfaces\", \"ec2:DescribeTags\"], \"Resource\": \"*\"},
    {\"Effect\": \"Allow\", \"Action\": [\"ec2:StartInstances\", \"ec2:StopInstances\", \"ec2:RebootInstances\"],
      \"Resource\": \"*\", \"Condition\": {\"StringEquals\": {\"ec2:ResourceTag/$TAG_KEY\": \"$CLUSTER_NAME\"}}},
    {\"Effect\": \"Allow\", \"Action\": [\"ec2:AssignPrivateIpAddresses\", \"ec2:UnassignPrivateIpAddresses\"],
      \"Resource\": \"*\"}
  ]}"
HOST_MAP=""
for n in 0 1 2; do
  id=$(instance_id "${PRIVATE[$n]}")
  HOST_MAP+="${NODES[$n]}:$id;"
  if [[ $(aws ec2 describe-iam-instance-profile-associations --filters "Name=instance-id,Values=$id" \
    --query 'length(IamInstanceProfileAssociations)' --output text) == 0 ]]; then
    aws ec2 associate-iam-instance-profile --instance-id "$id" --iam-instance-profile "Name=$ROLE" >/dev/null
  fi
done

log "A kubeconfig for the eviction agent"
oc apply -f - >/dev/null <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: pg-ha-evictor
  namespace: $NS
---
apiVersion: v1
kind: Secret
metadata:
  name: pg-ha-evictor-token
  namespace: $NS
  annotations:
    kubernetes.io/service-account.name: pg-ha-evictor
type: kubernetes.io/service-account-token
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: pg-ha-evictor
  namespace: $NS
rules:
  - apiGroups: [infrastructure.cluster.x-k8s.io]
    resources: [libvirthosts]
    verbs: [get, patch]
  - apiGroups: [infrastructure.cluster.x-k8s.io]
    resources: [libvirtmachines]
    verbs: [get, list]
  - apiGroups: [cluster.x-k8s.io]
    resources: [machines]
    verbs: [get, delete]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: pg-ha-evictor
  namespace: $NS
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: pg-ha-evictor
subjects:
  - kind: ServiceAccount
    name: pg-ha-evictor
    namespace: $NS
EOF
until token=$(oc get secret pg-ha-evictor-token -n "$NS" -o jsonpath='{.data.token}' | base64 -d) && [[ -n $token ]]; do
  sleep 2
done
ca=$(oc config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')
cat >"$SECRETS/evict.kubeconfig" <<EOF
apiVersion: v1
kind: Config
clusters:
  - name: $CLUSTER_NAME
    cluster:
      server: https://api.$CLUSTER_DOMAIN:6443
      certificate-authority-data: $ca
users:
  - name: pg-ha-evictor
    user:
      token: $token
contexts:
  - name: pg-ha-evictor
    context: {cluster: $CLUSTER_NAME, user: pg-ha-evictor, namespace: $NS}
current-context: pg-ha-evictor
EOF
chmod 600 "$SECRETS/evict.kubeconfig"

log "Packages, names and the eviction agent on each host"
OCP_RESOLVED_VERSION=$(oc version -o json | jq -r .openshiftVersion)
hosts_file=""
for n in 0 1 2; do hosts_file+="${PRIVATE[$n]} ${NODES[$n]}"$'\n'; done
for n in 0 1 2; do
  on_script "$n" "${NODES[$n]}" "$(cat "$SECRETS/hacluster")" "$OCP_RESOLVED_VERSION" "$hosts_file" <<'REMOTE'
set -euo pipefail
node=$1 hacluster_password=$2 ocp_version=$3 hosts_file=$4
hostnamectl set-hostname "$node"
sed -i '/# caplv-pg$/d' /etc/hosts
while read -r ip name; do [[ -n $ip ]] && echo "$ip $name # caplv-pg"; done <<<"$hosts_file" >>/etc/hosts
dnf -y -q install pcs pacemaker fence-agents-aws resource-agents-cloud postgresql-server pgvector awscli2 jq >/dev/null
if ! command -v oc >/dev/null; then
  curl -sfL "https://mirror.openshift.com/pub/openshift-v4/clients/ocp/$ocp_version/openshift-client-linux.tar.gz" |
    tar -C /usr/local/bin -xz oc
fi
echo "hacluster:$hacluster_password" | chpasswd
systemctl enable --now pcsd >/dev/null 2>&1
install -d -m 0755 /usr/lib/ocf/resource.d/caplv /etc/caplv
REMOTE
  scp -q "${SSH_OPTS[@]}" pgha/evict "$SECRETS/evict.kubeconfig" "ec2-user@${PUBLIC[$n]}:"
  on "$n" install -m 0755 /home/ec2-user/evict /usr/lib/ocf/resource.d/caplv/evict
  on "$n" install -m 0600 /home/ec2-user/evict.kubeconfig /etc/caplv/evict.kubeconfig
  on "$n" rm -f /home/ec2-user/evict /home/ec2-user/evict.kubeconfig
done

# bootstrap_postgres creates the first primary and clones the standbys from
# it, with systemd; once Pacemaker runs PostgreSQL, systemd must not touch it.
bootstrap_postgres() {
log "PostgreSQL: ${NODES[0]} as the first primary, the others cloned from it"
on_script 0 "$(cat "$SECRETS/replicator")" "$(cat "$SECRETS/demo")" "$VPC_CIDR" "$VM_NETWORK_CIDR" <<'REMOTE'
set -euo pipefail
replicator=$1 demo=$2 vpc=$3 vms=$4
data=/var/lib/pgsql/data
if [[ ! -f $data/PG_VERSION ]]; then
  postgresql-setup --initdb >/dev/null
  cat >>"$data/postgresql.conf" <<EOF
# caplv-pg
listen_addresses = '*'
wal_level = replica
max_wal_senders = 10
wal_keep_size = 1GB
hot_standby = on
wal_log_hints = on
restart_after_crash = off
EOF
  cat >>"$data/pg_hba.conf" <<EOF
host replication replicator $vpc scram-sha-256
host all all $vpc scram-sha-256
host all all $vms scram-sha-256
EOF
  systemctl start postgresql
  runuser -u postgres -- psql -q -c "CREATE ROLE replicator WITH REPLICATION LOGIN PASSWORD '$replicator'"
  runuser -u postgres -- psql -q -c "CREATE ROLE demo WITH LOGIN PASSWORD '$demo'"
  runuser -u postgres -- psql -q -c "CREATE DATABASE demo OWNER demo"
  systemctl stop postgresql
fi
REMOTE
for n in 0 1 2; do
  on_script "$n" "$(cat "$SECRETS/replicator")" "${PRIVATE[0]}" "$n" <<'REMOTE'
set -euo pipefail
replicator=$1 primary=$2 n=$3
systemctl disable --now postgresql >/dev/null 2>&1 || true
install -d -o postgres -g postgres -m 0700 /var/lib/pgsql/tmp
echo "*:*:*:replicator:$replicator" >/var/lib/pgsql/.pgpass
chown postgres:postgres /var/lib/pgsql/.pgpass
chmod 600 /var/lib/pgsql/.pgpass
if [[ $n != 0 && ! -f /var/lib/pgsql/data/PG_VERSION ]]; then
  # The first primary serves the base backup.
  rm -rf /var/lib/pgsql/data
  runuser -u postgres -- pg_basebackup -h "$primary" -U replicator -D /var/lib/pgsql/data -X stream -c fast >/dev/null
fi
REMOTE
  # The clones need the primary up while they copy it.
  if [[ $n == 0 ]]; then on 0 systemctl start postgresql; fi
done
on 0 systemctl stop postgresql
}
if ! on 0 pcs status >/dev/null 2>&1; then
  bootstrap_postgres
fi

log "Pacemaker cluster $PG_CLUSTER"
auth_args=() setup_args=()
for n in 0 1 2; do
  auth_args+=("${NODES[$n]}" "addr=${PRIVATE[$n]}")
  setup_args+=("${NODES[$n]}" "addr=${PRIVATE[$n]}")
done
on 0 pcs host auth "${auth_args[@]}" -u hacluster -p "$(cat "$SECRETS/hacluster")" >/dev/null
if ! on 0 pcs status >/dev/null 2>&1; then
  on 0 pcs cluster setup "$PG_CLUSTER" "${setup_args[@]}" --start --enable --wait=120 >/dev/null
fi
until on 0 pcs status nodes 2>/dev/null | grep -q "Online: ${NODES[0]} ${NODES[1]} ${NODES[2]}"; do sleep 5; done

nic=$(on 0 ip -o route get 1.1.1.1 | awk '{for (i = 1; i < NF; i++) if ($i == "dev") print $(i + 1)}')
on_script 0 "$AWS_REGION" "$HOST_MAP" "$PG_VIP" "$nic" "${NODES[*]}" <<'REMOTE'
set -euo pipefail
region=$1 host_map=$2 vip=$3 nic=$4 nodes=$5
exists() { pcs resource config "$1" >/dev/null 2>&1 || pcs stonith config "$1" >/dev/null 2>&1; }

# Build the configuration offline and push it in one go.
pcs cluster cib /root/caplv-pg.xml
cib() { pcs -f /root/caplv-pg.xml "$@"; }

# A fenced node is powered off and stays off, so a failed primary waits to
# be re-seeded (see docs/demos/postgres-ha.md) instead of rejoining ahead of
# the new one.
exists fence-aws || cib stonith create fence-aws fence_aws region="$region" pcmk_host_map="$host_map" \
  pcmk_reboot_action=off power_timeout=240 pcmk_reboot_timeout=480 pcmk_reboot_retries=4 op monitor interval=60s
cib property set stonith-enabled=true

exists pgsql || cib resource create pgsql ocf:heartbeat:pgsql \
  pgctl=/usr/bin/pg_ctl psql=/usr/bin/psql pgdata=/var/lib/pgsql/data \
  rep_mode=sync node_list="$nodes" master_ip="$vip" repuser=replicator restart_on_promote=true \
  primary_conninfo_opt="keepalives_idle=60 keepalives_interval=5 keepalives_count=5" \
  op start timeout=60s op stop timeout=60s op promote timeout=60s op demote timeout=60s \
  op notify timeout=60s op monitor interval=4s timeout=60s op monitor interval=3s role=Promoted timeout=60s \
  promotable
# The agent needs notifications; pcs 0.12 takes clone meta attributes here.
cib resource meta pgsql-clone promoted-max=1 promoted-node-max=1 clone-max=3 clone-node-max=1 notify=true

# The virtual IP follows the primary: awsvip moves the secondary private
# address between ENIs, IPaddr2 configures it.
exists pg-vip-aws || cib resource create pg-vip-aws ocf:heartbeat:awsvip secondary_private_ip="$vip" \
  auth_type=role region="$region" --group pg-vip
exists pg-vip-ip || cib resource create pg-vip-ip ocf:heartbeat:IPaddr2 ip="$vip" cidr_netmask=24 nic="$nic" \
  --group pg-vip
cib constraint colocation add pg-vip with Promoted pgsql-clone score=INFINITY 2>/dev/null || true
cib constraint order promote pgsql-clone then start pg-vip symmetrical=false kind=Mandatory 2>/dev/null || true
cib constraint order demote pgsql-clone then stop pg-vip symmetrical=false kind=Optional 2>/dev/null || true

# CAPLV gives way: wherever the primary will run, the eviction agent runs
# first, killing and cordoning that host's VMs before the promotion.
exists caplv-evict || cib resource create caplv-evict ocf:caplv:evict op monitor interval=30s
cib constraint colocation add caplv-evict with Promoted pgsql-clone score=INFINITY 2>/dev/null || true
cib constraint order start caplv-evict then promote pgsql-clone kind=Mandatory 2>/dev/null || true

pcs cluster cib-push /root/caplv-pg.xml --config
REMOTE
log "Waiting for a PostgreSQL primary"
until on 0 crm_mon -1 2>/dev/null | grep -q 'Promoted:'; do sleep 5; done
on 0 crm_mon -1 | sed -n '/Full List of Resources/,$p'

log "Machine taints in Cluster API"
# MachineDeployment taints need CAPI's MachineTaintPropagation feature gate.
args=$(oc get deploy capi-controller-manager -n capi-system -o json | jq -c '.spec.template.spec.containers[0].args')
gate=$(jq 'map(startswith("--feature-gates=")) | index(true)' <<<"$args")
value=$(jq -r ".[$gate]" <<<"$args")
if [[ $value != *MachineTaintPropagation=true* ]]; then
  value=${value/MachineTaintPropagation=false/MachineTaintPropagation=true}
  [[ $value == *MachineTaintPropagation=true* ]] || value+=",MachineTaintPropagation=true"
  oc patch deploy capi-controller-manager -n capi-system --type json \
    -p "[{\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/args/$gate\",\"value\":\"$value\"}]" >/dev/null
  oc rollout status deploy/capi-controller-manager -n capi-system --timeout 5m >/dev/null
fi

log "CAPLV hosts and a two-replica MachineDeployment"
labels="caplv.infrastructure.cluster.x-k8s.io/role: worker"
{
  for n in 0 1 2; do
    fp=$(awk -v n="${HOSTS[$n]%% *}" '$1==n && $2=="ed25519" {print $3}' "$HOSTS_DIR/fingerprints")
    cat <<EOF
---
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtHost
metadata:
  name: ${NODES[$n]}
  namespace: $NS
  labels:
    $labels
spec:
  uri: qemu+ssh://caplv@${PRIVATE[$n]}/system
  secretRef:
    name: libvirt-host-ssh-key
  hostKeyFingerprint: "$fp"
  # Leave PostgreSQL its share of the host.
  reservedResources:
    vcpus: 2
    memoryMB: 8192
  machineNetwork:
    type: network
    name: caplv
    addresses: ["${SUBNETS[$n]}.10/24"]
    gateway: ${SUBNETS[$n]}.1
    dns:
      nameservers: ["${SUBNETS[$n]}.1"]
EOF
  done
} | oc apply -f -
# The eviction agent cordons the primary's LibvirtHost when it starts, which
# it did before the hosts existed: start it again, so the MachineDeployment
# below never places a worker on the primary.
on 0 pcs resource restart caplv-evict >/dev/null
until [[ $(oc get libvirthosts -n "$NS" -o jsonpath='{.items[?(@.spec.unschedulable==true)].metadata.name}') ]]; do
  sleep 2
done
{
  base_image=$(oc get libvirtcluster "$CLUSTER_NAME" -n "$NS" -o jsonpath='{.spec.baseImage.volumeName}')
  cat <<EOF
---
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtMachineTemplate
metadata:
  name: pg-ha-workers-v2
  namespace: $NS
spec:
  template:
    spec:
      hostSelector:
        matchLabels:
          $labels
      # Kueue's ResourceFlavor for borrowed capacity selects this label.
      nodeLabels:
        caplv.demo/capacity: scavenged
      domain: {}
      rootDisk:
        baseImage: $base_image
        storagePool: default
        size: 100Gi
      bootstrapFormat: ignition
---
apiVersion: cluster.x-k8s.io/v1beta2
kind: MachineDeployment
metadata:
  name: pg-ha-workers
  namespace: $NS
spec:
  clusterName: $CLUSTER_NAME
  replicas: 2
  selector:
    matchLabels:
      caplv-demo: pg-ha-workers
  template:
    metadata:
      labels:
        caplv-demo: pg-ha-workers
    spec:
      clusterName: $CLUSTER_NAME
      # Only Kueue's batch pods tolerate this (demo.sh), so nothing else
      # lands on capacity that disappears when a standby is promoted.
      taints:
        - {key: caplv.demo/scavenged, value: "true", effect: NoSchedule, propagation: Always}
      bootstrap:
        dataSecretName: worker-bootstrap
      infrastructureRef:
        apiGroup: infrastructure.cluster.x-k8s.io
        kind: LibvirtMachineTemplate
        name: pg-ha-workers-v2
  # A lost worker is replaced before anything else; there is no spare host
  # to surge onto.
  rollout:
    strategy:
      type: RollingUpdate
      rollingUpdate:
        maxSurge: 0
        maxUnavailable: 1
EOF
} | oc apply -f -
log "Done. Database clients connect to $PG_VIP:5432 (user demo, password in $SECRETS/demo)"
