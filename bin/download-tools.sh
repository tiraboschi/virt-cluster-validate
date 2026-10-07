#!/bin/bash
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
#

set -euo pipefail

# Redirect all output to both stdout and stderr for debugging
exec 2>&1

TOOL_DOWNLOAD_TIMEOUT_SECONDS="${VIRT_VALIDATE_TOOL_DOWNLOAD_TIMEOUT_SECONDS:-300}"

# Download oc and virtctl from the target cluster at runtime. Prefer explicit
# target endpoints supplied by the validation request. The ingress-domain
# fallback is retained for conventional OpenShift deployments only.
OC_URL="${VIRT_VALIDATE_OC_URL:-}"
VIRTCTL_URL="${VIRT_VALIDATE_VIRTCTL_URL:-}"
CLUSTER_DOMAIN="${VIRT_VALIDATE_CLUSTER_DOMAIN:-${CLUSTER_DOMAIN:-}}"
if [ -z "$CLUSTER_DOMAIN" ] && { [ -z "$OC_URL" ] || { [ -z "$VIRTCTL_URL" ] && [ "${VIRT_VALIDATE_SKIP_VIRTCTL:-}" != "true" ]; }; } && [ -n "${VIRT_VALIDATE_URL:-}" ]; then
    TARGET_API_HOST="${VIRT_VALIDATE_URL#https://}"
    TARGET_API_HOST="${TARGET_API_HOST%%/*}"
    TARGET_API_HOST="${TARGET_API_HOST%%:*}"
    case "$TARGET_API_HOST" in
        api.*)
            CLUSTER_DOMAIN="${TARGET_API_HOST#api.}"
            ;;
        *)
            echo "ERROR: Cannot derive the target ingress domain from ${VIRT_VALIDATE_URL}; set target.toolDownloads.clusterDomain or explicit ocURL and virtctlURL." >&2
            exit 1
            ;;
    esac
fi
if [ -z "$CLUSTER_DOMAIN" ] && { [ -z "$OC_URL" ] || { [ -z "$VIRTCTL_URL" ] && [ "${VIRT_VALIDATE_SKIP_VIRTCTL:-}" != "true" ]; }; } && [ -z "${VIRT_VALIDATE_URL:-}" ]; then
    # Get cluster domain from OAuth issuer (publicly accessible endpoint)
    OAUTH_ISSUER=$(curl --connect-timeout 30 --max-time "$TOOL_DOWNLOAD_TIMEOUT_SECONDS" --fail --silent --show-error --cacert /var/run/secrets/kubernetes.io/serviceaccount/ca.crt https://${KUBERNETES_SERVICE_HOST}:${KUBERNETES_SERVICE_PORT}/.well-known/oauth-authorization-server \
        | jq -r '.issuer // empty')

    if [ -z "$OAUTH_ISSUER" ]; then
        echo "ERROR: Cannot query OAuth well-known endpoint." >&2
        exit 1
    fi

    # Extract domain from https://oauth-openshift.apps.DOMAIN
    CLUSTER_DOMAIN=$(echo "$OAUTH_ISSUER" | sed 's|https://oauth-openshift.apps.\(.*\)|\1|')

    if [ -z "$CLUSTER_DOMAIN" ]; then
        echo "ERROR: Cannot extract cluster domain from OAuth issuer: $OAUTH_ISSUER" >&2
        exit 1
    fi
fi

if [ -z "$OC_URL" ]; then
    [ -n "$CLUSTER_DOMAIN" ] || { echo "ERROR: target.toolDownloads.ocURL or target.toolDownloads.clusterDomain is required." >&2; exit 1; }
    OC_URL="https://downloads-openshift-console.apps.${CLUSTER_DOMAIN}/amd64/linux/oc.rhel9.tar"
fi

# Download and install oc.
# TODO: Replace this dynamically discovered archive with a pinned, verified CLI
# artifact in the controller image before this mode is promoted beyond a PoC.
# Tracking issue: https://github.com/openshift-cnv/virt-cluster-validate/issues/35
# TODO: Replace --insecure by trusting the target ingress CA, or remove this
# runtime download by baking a pinned, verified CLI into the validator image.
echo "Downloading oc from ${OC_URL}" >&2
curl --connect-timeout 30 --max-time "$TOOL_DOWNLOAD_TIMEOUT_SECONDS" --insecure --fail --silent --show-error --location "$OC_URL" | tar -xf - -C /usr/local/bin/
mv /usr/local/bin/oc.rhel9 /usr/local/bin/oc
chmod +x /usr/local/bin/oc

# Configure the downloaded client for the remote target. The validation runner
# creates its own kubeconfig later, but this keeps standalone use of this
# bootstrap script pointed at the target as well.
if [ -n "${VIRT_VALIDATE_URL:-}" ] && [ -n "${VIRT_VALIDATE_TOKEN:-}" ]; then
    TOOL_KUBECONFIG="${VIRT_VALIDATE_TOOL_KUBECONFIG:-/tmp/virt-validation/tools-kubeconfig}"
    mkdir -p "$(dirname "$TOOL_KUBECONFIG")"
    if [ "${VIRT_VALIDATE_INSECURE_SKIP_TLS:-}" = "true" ]; then
        oc config --kubeconfig="$TOOL_KUBECONFIG" set-cluster target --server="$VIRT_VALIDATE_URL" --insecure-skip-tls-verify=true
    else
        if [ -z "${VIRT_VALIDATE_CA_FILE:-}" ]; then
            echo "ERROR: VIRT_VALIDATE_CA_FILE is required for a TLS-verified target connection." >&2
            exit 1
        fi
        oc config --kubeconfig="$TOOL_KUBECONFIG" set-cluster target --server="$VIRT_VALIDATE_URL" --certificate-authority="$VIRT_VALIDATE_CA_FILE" --embed-certs=true
    fi
    oc config --kubeconfig="$TOOL_KUBECONFIG" set-credentials target-validator --token="$VIRT_VALIDATE_TOKEN"
    oc config --kubeconfig="$TOOL_KUBECONFIG" set-context target-validator --cluster=target --user=target-validator
    oc config --kubeconfig="$TOOL_KUBECONFIG" use-context target-validator
    export KUBECONFIG="$TOOL_KUBECONFIG"
fi

if [ "${VIRT_VALIDATE_SKIP_VIRTCTL:-}" = "true" ]; then
    echo "Skipping virtctl download" >&2
    exit 0
fi

# Download and install virtctl from the target's conventional generated Route
# only when the request did not supply an explicit endpoint. Avoid discovering
# routes through the target API: that would require cluster-wide Route access.
if [ -z "$VIRTCTL_URL" ]; then
    [ -n "$CLUSTER_DOMAIN" ] || { echo "ERROR: target.toolDownloads.virtctlURL or target.toolDownloads.clusterDomain is required." >&2; exit 1; }
    VIRTCTL_URL="https://hyperconverged-cluster-cli-download-openshift-cnv.apps.${CLUSTER_DOMAIN}/amd64/linux/virtctl.tar.gz"
fi
echo "Downloading virtctl from ${VIRTCTL_URL}..." >&2
if ! curl --connect-timeout 30 --max-time "$TOOL_DOWNLOAD_TIMEOUT_SECONDS" --insecure --fail --silent --show-error --location "$VIRTCTL_URL" | tar -xzf - -C /usr/local/bin/; then
    echo "ERROR: Failed to download virtctl from ${VIRTCTL_URL}" >&2
    exit 1
fi
chmod +x /usr/local/bin/virtctl

echo "Tools downloaded successfully" >&2
