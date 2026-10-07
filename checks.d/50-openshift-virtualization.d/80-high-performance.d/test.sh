#!/usr/bin/bash
#
# Copyright (C) 2025-2026 Red Hat, Inc.
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
#

NS="${VIRT_VALIDATE_NAMESPACE:-}"
VM_OPERATION_TIMEOUT="${VM_READY_TIMEOUT:-45s}"
CLEANUP_POLICY="${VIRT_VALIDATE_CLEANUP_POLICY:-Always}"
DATA_SOURCE="${VIRT_VALIDATE_DATA_SOURCE:-}"
DATA_SOURCE_NAMESPACE="openshift-virtualization-os-images"
VMNAME=""

retain_artifacts() {
  [ "$CLEANUP_POLICY" = "Never" ] || { [ "$CLEANUP_POLICY" = "OnFailure" ] && [ "$1" -ne 0 ] && [ "$1" -ne 77 ]; }
}

cleanup() {
  status=$?
  if retain_artifacts "$status"; then
    [ -n "${VIRT_VALIDATE_ARTIFACTS_FILE:-}" ] && {
      printf 'retainedNamespace=%s\n' "$NS"
      [ -n "$VMNAME" ] && printf 'retainedVM=%s\n' "$VMNAME"
    } >> "$VIRT_VALIDATE_ARTIFACTS_FILE"
  else
    [ -f vm.yaml ] && oc delete ${NS:+-n "$NS"} -f vm.yaml --ignore-not-found=true --force --grace-period=0 --wait=false >/dev/null 2>&1 || true
  fi
  return "$status"
}
trap cleanup EXIT

if [ -z "$DATA_SOURCE" ]; then
  for source in rhel10 rhel9; do
    if oc get datasource -n "$DATA_SOURCE_NAMESPACE" "$source" >/dev/null 2>&1; then
      DATA_SOURCE="$DATA_SOURCE_NAMESPACE/$source"
      break
    fi
  done
fi
[ -n "$DATA_SOURCE" ] || fail_with Setup "Neither rhel10 nor rhel9 DataSource was found in ${DATA_SOURCE_NAMESPACE}"
virtctl create vm --instancetype cx1.medium "--volume-import=type:ds,src:${DATA_SOURCE}" > vm.yaml \
  || fail_with Setup "Failed to generate the high-performance VM manifest"
oc create ${NS:+-n "$NS"} -f vm.yaml \
  || fail_with Setup "Failed to create high-performance test VM"
VMNAME=$(oc get ${NS:+-n "$NS"} -o jsonpath='{.metadata.name}' -f vm.yaml)

oc wait ${NS:+-n "$NS"} --for=condition=Ready=true --timeout="$VM_OPERATION_TIMEOUT" -f vm.yaml \
|| {
  oc get ${NS:+-n "$NS"} -o yaml -f vm.yaml
  fail_with Scheduling "Unable to schedule high-performance VMs. Is the CPU manager enabled?"
}
