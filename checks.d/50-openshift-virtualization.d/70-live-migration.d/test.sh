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
DATA_SOURCE="${VIRT_VALIDATE_DATA_SOURCE:-}"
CLEANUP_POLICY="${VIRT_VALIDATE_CLEANUP_POLICY:-Always}"
VMNAME=""
MIGRATION_NAME=""

record_step() {
  [ -n "${VIRT_VALIDATE_STEPS_FILE:-}" ] || return 0
  jq -cn --arg name "$1" --arg status "$2" --arg state "${3:-}" --arg message "${4:-}" \
    '{name: $name, status: $status} +
     (if $state == "" and $message == "" then {} else {extra: ({state: $state, message: $message} | with_entries(select(.value != "")))} end)' \
    >> "$VIRT_VALIDATE_STEPS_FILE"
}

fail_migration() {
  record_step "Live migrate VM" failed "" "$2"
  fail_with "$1" "$2"
}

retain_artifacts() {
  [ "$CLEANUP_POLICY" = "Never" ] || { [ "$CLEANUP_POLICY" = "OnFailure" ] && [ "$1" -ne 0 ] && [ "$1" -ne 77 ]; }
}

cleanup() {
  status=$?
  if retain_artifacts "$status"; then
    [ -n "${VIRT_VALIDATE_ARTIFACTS_FILE:-}" ] && {
      printf 'retainedNamespace=%s\n' "$NS"
      [ -n "$VMNAME" ] && printf 'retainedVM=%s\n' "$VMNAME"
      [ -n "$MIGRATION_NAME" ] && printf 'retainedMigration=%s\n' "$MIGRATION_NAME"
    } >> "$VIRT_VALIDATE_ARTIFACTS_FILE"
  else
    [ -f migration.yaml ] && oc delete ${NS:+-n "$NS"} -f migration.yaml --ignore-not-found=true --force --grace-period=0 --wait=false >/dev/null 2>&1 || true
    [ -f vm.yaml ] && oc delete ${NS:+-n "$NS"} -f vm.yaml --ignore-not-found=true --force --grace-period=0 --wait=false >/dev/null 2>&1 || true
  fi
  return "$status"
}
trap cleanup EXIT

record_step "Live migrate VM" pending running
[ -n "$DATA_SOURCE" ] || fail_migration Setup "VIRT_VALIDATE_DATA_SOURCE is required"
oc auth can-i create virtualmachineinstancemigrations.kubevirt.io ${NS:+-n "$NS"} | grep -qx yes \
  || fail_migration Permissions "No permission to perform live migration"

virtctl create vm "--volume-import=type:ds,src:${DATA_SOURCE}" > vm.yaml \
  || fail_migration Setup "Failed to generate the migration VM manifest"
oc create --dry-run=client -o json -f vm.yaml | \
  jq --arg namespace "$NS" 'if $namespace != "" then .metadata.namespace = $namespace else . end' > vm.json \
  || fail_migration Setup "Failed to prepare the migration VM manifest"
mv vm.json vm.yaml
oc create ${NS:+-n "$NS"} -f vm.yaml \
  || fail_migration Setup "Failed to create test VM"

VMNAME=$(oc get ${NS:+-n "$NS"} -o jsonpath='{.metadata.name}' -f vm.yaml)
MIGRATION_NAME="${VMNAME}-migration"

oc wait ${NS:+-n "$NS"} --for=condition=Ready=true --timeout="${VM_READY_TIMEOUT:-2m}" -f vm.yaml \
|| {
  oc get ${NS:+-n "$NS"} -o yaml vm $VMNAME
  fail_migration Scheduling "Unable to schedule VM"
}
SOURCE_NODE=$(oc get ${NS:+-n "$NS"} vmi "$VMNAME" -o jsonpath='{.status.nodeName}')
[ -n "$SOURCE_NODE" ] || fail_migration Scheduling "VM has no source node"

tee migration.yaml <<EOF
apiVersion: kubevirt.io/v1
kind: VirtualMachineInstanceMigration
metadata:
  name: ${MIGRATION_NAME}
spec:
  vmiName: ${VMNAME}
status: {}
EOF
APPLY_OUT=$(oc apply ${NS:+-n "$NS"} -f migration.yaml 2>&1) || {
  if echo "$APPLY_OUT" | grep -q "DisksNotLiveMigratable"; then
    fail_migration Migration "VM disks are not live-migratable (PVCs must use ReadWriteMany access mode)"
  fi
  echo "$APPLY_OUT"
  fail_migration Migration "Failed to create migration: $APPLY_OUT"
}

oc wait ${NS:+-n "$NS"} --for=jsonpath='{.status.phase}'=Succeeded --timeout="${VM_READY_TIMEOUT:-2m}" -f migration.yaml \
|| {
  oc get ${NS:+-n "$NS"} -o yaml -f vm.yaml
  oc get ${NS:+-n "$NS"} -o yaml -f migration.yaml
  fail_migration Migration "VM failed to migrate"
}
TARGET_NODE=$(oc get ${NS:+-n "$NS"} vmi "$VMNAME" -o jsonpath='{.status.nodeName}')
[ -n "$TARGET_NODE" ] && [ "$TARGET_NODE" != "$SOURCE_NODE" ] \
  || fail_migration Migration "VM remained on ${SOURCE_NODE:-no node} after migration"
[ -n "${VIRT_VALIDATE_ARTIFACTS_FILE:-}" ] && {
  printf 'migrationSourceNode=%s\n' "$SOURCE_NODE"
  printf 'migrationTargetNode=%s\n' "$TARGET_NODE"
} >> "$VIRT_VALIDATE_ARTIFACTS_FILE"
record_step "Live migrate VM" passed
