#!/usr/bin/bash
#
# Copyright (C) 2024-2026 Red Hat, Inc.
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
VM_OPERATION_TIMEOUT="${VM_READY_TIMEOUT:-2m}"
CLEANUP_POLICY="${VIRT_VALIDATE_CLEANUP_POLICY:-Always}"
DATA_SOURCE="${VIRT_VALIDATE_DATA_SOURCE:-}"
DATA_SOURCE_NAMESPACE="openshift-virtualization-os-images"
VMNAME=""
SNAPSHOT_NAME=""
RESTORE_NAME=""

retain_artifacts() {
  [ "$CLEANUP_POLICY" = "Never" ] || { [ "$CLEANUP_POLICY" = "OnFailure" ] && [ "$1" -ne 0 ] && [ "$1" -ne 77 ]; }
}

cleanup() {
  status=$?
  if retain_artifacts "$status"; then
    [ -n "${VIRT_VALIDATE_ARTIFACTS_FILE:-}" ] && {
      printf 'retainedNamespace=%s\n' "$NS"
      [ -n "$VMNAME" ] && printf 'retainedVM=%s\n' "$VMNAME"
      [ -n "$SNAPSHOT_NAME" ] && printf 'retainedSnapshot=%s\n' "$SNAPSHOT_NAME"
      [ -n "$RESTORE_NAME" ] && printf 'retainedRestore=%s\n' "$RESTORE_NAME"
    } >> "$VIRT_VALIDATE_ARTIFACTS_FILE"
  else
    [ -f restore.yaml ] && oc delete ${NS:+-n "$NS"} -f restore.yaml --ignore-not-found=true --force --grace-period=0 --wait=false >/dev/null 2>&1 || true
    [ -f snap.yaml ] && oc delete ${NS:+-n "$NS"} -f snap.yaml --ignore-not-found=true --force --grace-period=0 --wait=false >/dev/null 2>&1 || true
    [ -f vm.yaml ] && oc delete ${NS:+-n "$NS"} -f vm.yaml --ignore-not-found=true --force --grace-period=0 --wait=false >/dev/null 2>&1 || true
  fi
  return "$status"
}
trap cleanup EXIT

VSC_COUNT=$(oc get volumesnapshotclass -o json 2>/dev/null | jq '.items | length' 2>/dev/null || echo 0)
if [ "$VSC_COUNT" -eq 0 ]; then
  fail_with VolumeSnapshotClass "No VolumeSnapshotClass found, snapshot operations will not work"
fi

VSC_DELETE_COUNT=$(oc get volumesnapshotclass -o json 2>/dev/null | jq '[.items[] | select(.deletionPolicy == "Delete")] | length' 2>/dev/null || echo 0)
if [ "$VSC_DELETE_COUNT" -eq 0 ]; then
  skip_with "All VolumeSnapshotClass objects have deletionPolicy=Retain (or unset). Skipping to avoid leaving retained backend snapshots behind."
fi

if [ -z "$DATA_SOURCE" ]; then
  for source in rhel10 rhel9; do
    if oc get datasource -n "$DATA_SOURCE_NAMESPACE" "$source" >/dev/null 2>&1; then
      DATA_SOURCE="$DATA_SOURCE_NAMESPACE/$source"
      break
    fi
  done
fi
[ -n "$DATA_SOURCE" ] || fail_with Setup "Neither rhel10 nor rhel9 DataSource was found in ${DATA_SOURCE_NAMESPACE}"
virtctl create vm "--volume-import=type:ds,src:${DATA_SOURCE}" > vm.yaml \
  || fail_with Setup "Failed to generate the snapshot VM manifest"
oc create ${NS:+-n "$NS"} -f vm.yaml \
  || fail_with Setup "Failed to create test VM"

VMNAME=$(oc get ${NS:+-n "$NS"} -o jsonpath='{.metadata.name}' -f vm.yaml)
SNAPSHOT_NAME="snap-${VMNAME}"

oc wait ${NS:+-n "$NS"} --for=condition=Ready=true --timeout="$VM_OPERATION_TIMEOUT" -f vm.yaml \
  || fail_with Setup "VM did not become Ready"
virtctl stop ${NS:+-n "$NS"} "$VMNAME" \
  || fail_with Setup "Failed to stop VM"

tee snap.yaml <<EOF
apiVersion: snapshot.kubevirt.io/v1alpha1
kind: VirtualMachineSnapshot
metadata:
  name: ${SNAPSHOT_NAME}
spec:
  source:
    apiGroup: kubevirt.io
    kind: VirtualMachine
    name: ${VMNAME}
EOF
oc apply ${NS:+-n "$NS"} -f snap.yaml \
  || fail_with Snapshot "Failed to apply snapshot resource"

oc wait ${NS:+-n "$NS"} -f snap.yaml --for condition=Ready --timeout="$VM_OPERATION_TIMEOUT" \
|| {
  oc get ${NS:+-n "$NS"} -o yaml -f snap.yaml
  fail_with Snapshot "Failed to create snapshot with default storageclass"
}

RESTORE_NAME="restore-${VMNAME}"
tee restore.yaml <<EOF
apiVersion: snapshot.kubevirt.io/v1alpha1
kind: VirtualMachineRestore
metadata:
  name: ${RESTORE_NAME}
spec:
  target:
    apiGroup: kubevirt.io
    kind: VirtualMachine
    name: ${VMNAME}
  virtualMachineSnapshotName: ${SNAPSHOT_NAME}
EOF
oc apply ${NS:+-n "$NS"} -f restore.yaml \
  || fail_with Restore "Failed to apply restore resource"
oc wait ${NS:+-n "$NS"} -f restore.yaml --for condition=Ready --timeout="$VM_OPERATION_TIMEOUT" \
|| {
  oc get ${NS:+-n "$NS"} -o yaml -f restore.yaml
  fail_with Restore "Failed to restore snapshots"
}
