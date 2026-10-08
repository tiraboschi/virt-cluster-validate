#!/usr/bin/env bash
#
# Copyright (C) 2026 Red Hat, Inc.
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

set -euo pipefail

target_namespace="${TARGET_NAMESPACE:-vcv-validation}"
data_source_namespace="${TARGET_DATA_SOURCE_NAMESPACE:-openshift-virtualization-os-images}"
service_account="${TARGET_SERVICE_ACCOUNT:-virt-validation-target}"
target_oc=(oc)
if [[ -n "${TARGET_KUBECONFIG:-}" ]]; then
  target_oc+=(--kubeconfig="$TARGET_KUBECONFIG")
fi

"${target_oc[@]}" create namespace "$target_namespace" --dry-run=client -o yaml | "${target_oc[@]}" apply -f -
"${target_oc[@]}" apply -f - <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ${service_account}
  namespace: ${target_namespace}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: virt-validation-target
  namespace: ${target_namespace}
rules:
- apiGroups: [""]
  resources: ["pods", "persistentvolumeclaims"]
  verbs: ["create", "delete", "get", "list", "patch", "watch"]
- apiGroups: ["cdi.kubevirt.io"]
  resources: ["datavolumes"]
  verbs: ["create", "delete", "get", "list", "patch", "watch"]
- apiGroups: ["kubevirt.io"]
  resources: ["virtualmachines", "virtualmachineinstances", "virtualmachineinstancemigrations"]
  verbs: ["create", "delete", "get", "list", "patch", "watch"]
- apiGroups: ["snapshot.kubevirt.io"]
  resources: ["virtualmachinesnapshots"]
  verbs: ["create", "delete", "get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: virt-validation-target
  namespace: ${target_namespace}
subjects:
- kind: ServiceAccount
  name: ${service_account}
  namespace: ${target_namespace}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: virt-validation-target
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: virt-validation-datasource-reader
  namespace: ${data_source_namespace}
rules:
- apiGroups: ["cdi.kubevirt.io"]
  resources: ["datasources"]
  verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: virt-validation-datasource-reader
  namespace: ${data_source_namespace}
subjects:
- kind: ServiceAccount
  name: ${service_account}
  namespace: ${target_namespace}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: virt-validation-datasource-reader
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: virt-validation-target-reader
rules:
- apiGroups: [""]
  resources: ["nodes"]
  verbs: ["get", "list"]
- apiGroups: ["snapshot.storage.k8s.io"]
  resources: ["volumesnapshotclasses"]
  verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: virt-validation-target-reader-${target_namespace}
subjects:
- kind: ServiceAccount
  name: ${service_account}
  namespace: ${target_namespace}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: virt-validation-target-reader
EOF
