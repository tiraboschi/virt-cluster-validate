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

namespace="${NAMESPACE:-openshift-mtv}"
: "${CONTROLLER_IMAGE:?set CONTROLLER_IMAGE to a pullable controller image}"
: "${VALIDATOR_IMAGE:?set VALIDATOR_IMAGE to a pullable validator image}"

if [[ "$namespace" != "openshift-mtv" ]]; then
  echo "config/manager/manager.yaml is packaged for openshift-mtv; got NAMESPACE=$namespace" >&2
  exit 2
fi

oc get namespace "$namespace" >/dev/null
oc apply -f config/crd/bases
oc apply -f config/rbac/role.yaml
oc apply -f config/manager/manager.yaml

patch="$(jq -cn --arg controller "$CONTROLLER_IMAGE" --arg validator "$VALIDATOR_IMAGE" '
  {spec: {template: {spec: {containers: [{
    name: "manager",
    image: $controller,
    args: [
      "--validator-image=" + $validator,
      "--leader-elect=true",
      "--leader-election-namespace=openshift-mtv"
    ]
  }]}}}}')"
oc -n "$namespace" patch deployment virt-validation-controller --type=strategic -p "$patch"
oc -n "$namespace" rollout status deployment/virt-validation-controller --timeout="${ROLLOUT_TIMEOUT:-5m}"
