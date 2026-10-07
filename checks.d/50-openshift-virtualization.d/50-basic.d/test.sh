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
DATA_SOURCE_NAMESPACE="openshift-virtualization-os-images"
MANIFEST="$(mktemp)"
VMNAME=""
DVNAMES=""
CLEANUP_POLICY="${VIRT_VALIDATE_CLEANUP_POLICY:-Always}"

retain_artifacts() {
  [ "$CLEANUP_POLICY" = "Never" ] || { [ "$CLEANUP_POLICY" = "OnFailure" ] && [ "$1" -ne 0 ] && [ "$1" -ne 77 ]; }
}

cleanup() {
  status=$?
  if retain_artifacts "$status"; then
    [ -n "${VIRT_VALIDATE_ARTIFACTS_FILE:-}" ] && {
      printf 'retainedNamespace=%s\n' "$NS"
      [ -n "$VMNAME" ] && printf 'retainedVM=%s\n' "$VMNAME"
      [ -n "$DVNAMES" ] && printf 'retainedDataVolumes=%s\nretainedPVCs=%s\n' "${DVNAMES% }" "${DVNAMES% }"
    } >> "$VIRT_VALIDATE_ARTIFACTS_FILE"
  else
    if [ -n "$VMNAME" ]; then
      oc delete ${NS:+-n "$NS"} vm,vmi "$VMNAME" --ignore-not-found=true --force --grace-period=0 --wait=true --timeout=60s >/dev/null 2>&1 || true
    fi
    for dv in $DVNAMES; do
      oc delete ${NS:+-n "$NS"} datavolume,pvc "$dv" --ignore-not-found=true --force --grace-period=0 --wait=false >/dev/null 2>&1 || true
    done
  fi
  rm -f "$MANIFEST"
  return "$status"
}
trap cleanup EXIT

DATA_SOURCE="${VIRT_VALIDATE_DATA_SOURCE:-}"
if [ -z "$DATA_SOURCE" ]; then
  for source in rhel10 rhel9; do
    if oc get datasource -n "$DATA_SOURCE_NAMESPACE" "$source" >/dev/null 2>&1; then
      DATA_SOURCE="$DATA_SOURCE_NAMESPACE/$source"
      break
    fi
  done
fi

[ -n "$DATA_SOURCE" ] \
  || fail_with Setup "Neither rhel10 nor rhel9 DataSource was found in ${DATA_SOURCE_NAMESPACE}"

virtctl create vm "--volume-import=type:ds,src:${DATA_SOURCE}" > "$MANIFEST" \
  || fail_with Setup "Failed to generate the test VM manifest"
oc create --dry-run=client -o json -f "$MANIFEST" | \
  jq --arg namespace "$NS" --arg hostname "${VIRT_VALIDATE_NODE_HOSTNAME:-}" '
    if $namespace != "" then .metadata.namespace = $namespace else . end |
    if $hostname != "" then .spec.template.spec.nodeSelector["kubernetes.io/hostname"] = $hostname else . end
  ' > "${MANIFEST}.json" \
  || fail_with Setup "Failed to prepare the canary VM manifest"
mv "${MANIFEST}.json" "$MANIFEST"
oc create ${NS:+-n "$NS"} -f "$MANIFEST" \
  || fail_with Setup "Failed to create test VM"

VMNAME=$(oc get ${NS:+-n "$NS"} -o jsonpath='{.metadata.name}' -f "$MANIFEST")
DVNAMES=$(oc get ${NS:+-n "$NS"} vm "$VMNAME" -o jsonpath='{range .spec.dataVolumeTemplates[*]}{.metadata.name}{" "}{end}')

oc wait ${NS:+-n "$NS"} --for=condition=Ready=true --timeout="${VM_READY_TIMEOUT:-2m}" -f "$MANIFEST" \
|| {
  oc get ${NS:+-n "$NS"} -o yaml vm "$VMNAME"
  fail_with Scheduling "Unable to schedule VMs"
}

if [ -n "${VIRT_VALIDATE_NODE:-}" ]; then
  ACTUAL_NODE=$(oc get ${NS:+-n "$NS"} vmi "$VMNAME" -o jsonpath='{.status.nodeName}')
  [ "$ACTUAL_NODE" = "$VIRT_VALIDATE_NODE" ] \
    || fail_with Scheduling "VM landed on ${ACTUAL_NODE:-no node}, expected ${VIRT_VALIDATE_NODE}"
fi
