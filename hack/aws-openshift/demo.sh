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

# demo.sh deploys the front end of the PostgreSQL HA demo (demo/) on the
# OpenShift cluster, after pgha.sh: the Red Hat build of Kueue with a queue
# for the borrowed workers, the internal image registry, and the narrated
# web app, built in the cluster. It prints the app's URL.

set -euo pipefail
cd "$(dirname "$0")"
# shellcheck source-path=SCRIPTDIR
source ./env.sh

NS=caplv-demo
SECRETS="$STATE_DIR/pgha"
PG_VIP=${PG_VIP:-10.0.1.250}

log "Red Hat build of Kueue"
oc apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: openshift-kueue-operator
---
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: kueue-operator
  namespace: openshift-kueue-operator
spec: {}
---
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: kueue-operator
  namespace: openshift-kueue-operator
spec:
  channel: stable-v1.4
  name: kueue-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
EOF
until oc get crd kueues.kueue.openshift.io >/dev/null 2>&1; do sleep 5; done
oc apply -f - >/dev/null <<EOF
apiVersion: kueue.openshift.io/v1
kind: Kueue
metadata:
  name: cluster
spec:
  managementState: Managed
  config:
    integrations:
      frameworks: [BatchJob]
EOF
until oc get crd clusterqueues.kueue.x-k8s.io >/dev/null 2>&1 &&
  oc get deployment -n openshift-kueue-operator kueue-controller-manager >/dev/null 2>&1; do
  sleep 5
done
oc rollout status -n openshift-kueue-operator deployment/kueue-controller-manager --timeout 5m

log "Namespace $NS and its queue"
oc apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: $NS
  labels:
    kueue.openshift.io/managed: "true"
EOF
# Kueue's webhooks answer a moment after its deployment reports available.
until oc apply -f - >/dev/null 2>&1 <<EOF
apiVersion: kueue.x-k8s.io/v1beta1
kind: ResourceFlavor
metadata:
  name: scavenged
spec:
  # The workers CAPLV runs on the PostgreSQL standbys (pgha.sh's nodeLabels).
  nodeLabels:
    caplv.demo/capacity: scavenged
  # The workers are tainted (pgha.sh) so that only Kueue's admitted pods,
  # which this toleration is added to, run on capacity that can vanish.
  nodeTaints:
    - {key: caplv.demo/scavenged, value: "true", effect: NoSchedule}
  tolerations:
    - {key: caplv.demo/scavenged, operator: Equal, value: "true", effect: NoSchedule}
---
apiVersion: kueue.x-k8s.io/v1beta1
kind: ClusterQueue
metadata:
  name: embed
spec:
  namespaceSelector: {}
  resourceGroups:
    - coveredResources: [cpu, memory]
      flavors:
        - name: scavenged
          resources:
            # Two borrowed workers of 6 vCPUs (5.5 allocatable) and about
            # 22 GB each: six pods of 1.5 CPUs, three to a worker.
            - name: cpu
              nominalQuota: 9
            - name: memory
              nominalQuota: 40Gi
---
apiVersion: kueue.x-k8s.io/v1beta1
kind: LocalQueue
metadata:
  name: embed
  namespace: $NS
spec:
  clusterQueue: embed
EOF
do sleep 5; done

log "Internal image registry"
oc patch configs.imageregistry.operator.openshift.io cluster --type merge \
  -p '{"spec":{"managementState":"Managed","storage":{"emptyDir":{}}}}' >/dev/null
until oc get deployment -n openshift-image-registry image-registry >/dev/null 2>&1; do sleep 5; done
oc rollout status -n openshift-image-registry deployment/image-registry --timeout 5m

log "AWS credentials for the kill and recovery buttons"
APP_USER=$CLUSTER_NAME-demo-app
if ! aws iam get-user --user-name "$APP_USER" >/dev/null 2>&1; then
  aws iam create-user --user-name "$APP_USER" --tags "Key=$TAG_KEY,Value=$CLUSTER_NAME" >/dev/null
fi
aws iam put-user-policy --user-name "$APP_USER" --policy-name demo --policy-document "{
  \"Version\": \"2012-10-17\",
  \"Statement\": [
    {\"Effect\": \"Allow\", \"Action\": \"ec2:DescribeInstances\", \"Resource\": \"*\"},
    {\"Effect\": \"Allow\", \"Action\": [\"ec2:StopInstances\", \"ec2:StartInstances\"], \"Resource\": \"*\",
      \"Condition\": {\"StringEquals\": {\"ec2:ResourceTag/$TAG_KEY\": \"$CLUSTER_NAME\"}}}
  ]}"
if ! oc get secret aws -n "$NS" >/dev/null 2>&1; then
  for key in $(aws iam list-access-keys --user-name "$APP_USER" --query 'AccessKeyMetadata[].AccessKeyId' --output text); do
    aws iam delete-access-key --user-name "$APP_USER" --access-key-id "$key"
  done
  read -r key_id key_secret < <(aws iam create-access-key --user-name "$APP_USER" \
    --query 'AccessKey.[AccessKeyId,SecretAccessKey]' --output text)
  oc create secret generic aws -n "$NS" --from-literal=AWS_ACCESS_KEY_ID="$key_id" \
    --from-literal=AWS_SECRET_ACCESS_KEY="$key_secret" >/dev/null
fi

log "Secrets, permissions and the build"
oc create secret generic postgres -n "$NS" --from-literal=password="$(cat "$SECRETS/demo")" \
  --dry-run=client -o yaml | oc apply -f - >/dev/null
oc create secret generic ssh -n "$NS" --from-file=id_ed25519="$STATE_DIR/ssh/id_ed25519" \
  --dry-run=client -o yaml | oc apply -f - >/dev/null
oc apply -f - >/dev/null <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: caplv-demo
  namespace: $NS
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: caplv-demo
rules:
  - apiGroups: [""]
    resources: [nodes]
    verbs: [get, list]
  - apiGroups: [kueue.x-k8s.io]
    resources: [clusterqueues]
    verbs: [get, list]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: caplv-demo
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: caplv-demo}
subjects: [{kind: ServiceAccount, name: caplv-demo, namespace: $NS}]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: caplv-demo
  namespace: caplv-workers
rules:
  - apiGroups: [infrastructure.cluster.x-k8s.io]
    resources: [libvirthosts]
    verbs: [get, list, patch]
  - apiGroups: [infrastructure.cluster.x-k8s.io]
    resources: [libvirtmachines]
    verbs: [get, list]
  - apiGroups: [cluster.x-k8s.io]
    resources: [machines, machinedeployments]
    verbs: [get, list]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: caplv-demo
  namespace: caplv-workers
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: caplv-demo}
subjects: [{kind: ServiceAccount, name: caplv-demo, namespace: $NS}]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: caplv-demo
  namespace: $NS
rules:
  - apiGroups: [batch]
    resources: [jobs]
    verbs: [get, list, create, delete]
  - apiGroups: [""]
    resources: [pods]
    verbs: [get, list]
  - apiGroups: [""]
    resources: [configmaps]
    verbs: [get, create, update]
  - apiGroups: [kueue.x-k8s.io]
    resources: [workloads]
    verbs: [get, list]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: caplv-demo
  namespace: $NS
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: caplv-demo}
subjects: [{kind: ServiceAccount, name: caplv-demo, namespace: $NS}]
EOF
oc get bc caplv-demo -n "$NS" >/dev/null 2>&1 ||
  oc new-build --strategy docker --binary --name caplv-demo -n "$NS" >/dev/null
oc start-build caplv-demo -n "$NS" --from-dir demo --follow --wait | tail -5

log "The app"
pg_hosts=$(tail -n +2 "$HOSTS_DIR/hosts" | awk '{n=$1; sub(/.*-libvirt-/, "libvirt-", n); printf "%s%s=%s", sep, n, $3; sep=","}')
console=$(oc whoami --show-console)
oc apply -f - >/dev/null <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: caplv-demo
  namespace: $NS
spec:
  replicas: 1
  selector:
    matchLabels: {app: caplv-demo}
  template:
    metadata:
      labels: {app: caplv-demo}
      annotations:
        image.openshift.io/triggers: '[{"from":{"kind":"ImageStreamTag","name":"caplv-demo:latest"},"fieldPath":"spec.template.spec.containers[0].image"}]'
    spec:
      serviceAccountName: caplv-demo
      # On the control plane node: the borrowed workers come and go.
      nodeSelector:
        node-role.kubernetes.io/control-plane: ""
      containers:
        - name: app
          image: image-registry.openshift-image-registry.svc:5000/$NS/caplv-demo:latest
          ports: [{containerPort: 8080}]
          env:
            - {name: PGHOST, value: "$PG_VIP"}
            - {name: PG_HOSTS, value: "$pg_hosts"}
            - {name: CONSOLE_URL, value: "$console"}
            - {name: AWS_REGION, value: "$AWS_REGION"}
            - {name: AWS_TAG, value: "$TAG_KEY=$CLUSTER_NAME"}
            - {name: IMAGE, value: "image-registry.openshift-image-registry.svc:5000/$NS/caplv-demo:latest"}
            - {name: PGPASSWORD, valueFrom: {secretKeyRef: {name: postgres, key: password}}}
          envFrom:
            - secretRef: {name: aws}
          volumeMounts:
            - {name: ssh, mountPath: /etc/caplv-demo/ssh, readOnly: true}
          resources:
            requests: {cpu: 500m, memory: 1Gi}
            limits: {memory: 2Gi}
          readinessProbe:
            httpGet: {path: /api/config, port: 8080}
      volumes:
        - name: ssh
          secret: {secretName: ssh, defaultMode: 0400}
---
apiVersion: v1
kind: Service
metadata:
  name: caplv-demo
  namespace: $NS
spec:
  selector: {app: caplv-demo}
  ports: [{port: 8080, targetPort: 8080}]
---
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: caplv-demo
  namespace: $NS
spec:
  to: {kind: Service, name: caplv-demo}
  tls: {termination: edge, insecureEdgeTerminationPolicy: Redirect}
EOF
oc rollout restart deployment/caplv-demo -n "$NS" >/dev/null
oc rollout status deployment/caplv-demo -n "$NS" --timeout 5m
log "Demo: https://$(oc get route caplv-demo -n "$NS" -o jsonpath='{.spec.host}')"
