# virt-cluster-validate

Validates an OpenShift cluster's virtualization readiness.

## Objectives

*   **Fast**: Execution time boxed to <3 minutes.
*   **Simple**: Direct feedback with human-readable and JSON outputs.
*   **Safe**: Runs unprivileged with `cluster-reader` permissions.
*   **Isolated**: Tests run in temporary sandboxes to prevent source tree pollution.
*   **Extensible**: Add new checks via `test*.sh` in `checks.d/`.

## Prerequisites

*   `oc` and `virtctl` binaries in your `PATH`.
*   Cluster access via `oc login`, or `--url` plus `--token` (same as `kubectl mtv create provider --type openshift`).
*   Python 3.x (to run the validator).
*   [Claude Code](https://claude.ai/code) or [Gemini CLI](https://github.com/google/gemini-cli) (Optional, for AI-assisted development).

## Usage

    # Login to the cluster
    oc login ...

    # Or authenticate with API URL + bearer token (no prior oc login)
    ./virt-cluster-validate --url https://api.cluster.example.com:6443 \
      --token "$TOKEN" --ca-file /path/to/ca.crt

    # Basic run (Human readable, summary only)
    ./virt-cluster-validate

    # Show details of failed and warned checks
    ./virt-cluster-validate -v

    # Show details of all checks (including passed)
    ./virt-cluster-validate -vv

    # Run only specific checks (substring match)
    ./virt-cluster-validate --include nodes,basic

    # Skip specific checks
    ./virt-cluster-validate --exclude high-performance,rebalance

    # CTRF output (For CI/CD integration)
    ./virt-cluster-validate -o ctrf

    # Atomically update a CTRF report file as checks complete
    ./virt-cluster-validate --report-file /tmp/virt-validation-report.json

    # Fail fast (Stop after 1 failure)
    ./virt-cluster-validate -f

    # Write per-check logs to a directory
    ./virt-cluster-validate --log-dir /tmp/check-logs

### CLI Options

*   `-o {human,ctrf,junit}`: Output format (Default: `human`). `ctrf` produces a [CTRF](https://ctrf.io) JSON report on stdout for CI/CD integration; `junit` produces JUnit XML on stdout. In both machine-readable modes, prerequisite failures are written to stderr.
*   `-v, --verbose`: Show test details. Use `-v` for failed/warned checks only, `-vv` for all checks.
*   `-s, --select PATH`: Run only a specific test script.
*   `--include PATTERNS`: Comma-separated substrings; only run tests whose path contains at least one pattern (e.g. `--include nodes,basic`).
*   `--exclude PATTERNS`: Comma-separated substrings; skip tests whose path contains any pattern (e.g. `--exclude high-performance,rebalance`).
*   `--log-dir DIR`: Write per-check log files to the given directory.
*   `--report-file PATH`: Write complete, atomic CTRF snapshots to a file. The initial snapshot marks selected checks `pending`; snapshots are updated as checks finish.
*   `-t, --timeout SPAN`: Max execution time per test (e.g. `2m`, `45s`, `180`. Default: `180`).
*   `-c, --concurrency N`: Number of tests to run in parallel (Default: Number of CPU cores).
*   `-f [N], --fail-fast [N]`: Stop execution after N failures (Default: 1).
*   `--url URL`: OpenShift API server URL. Must be used with `--token`.
*   `--token TOKEN`: Bearer token for `--url` (service account or user token).
*   `--ca-file PATH`: CA certificate file used to verify `--url`; it is mutually exclusive with `--insecure-skip-tls`.
*   `--insecure-skip-tls`: Skip TLS verification when using `--url` (typical for self-signed API certs); it is mutually exclusive with `--ca-file`.

`--url`, `--token`, and `--ca-file` can also be set via `VIRT_VALIDATE_URL`, `VIRT_VALIDATE_TOKEN`, and `VIRT_VALIDATE_CA_FILE`. Set `VIRT_VALIDATE_INSECURE_SKIP_TLS=true` to skip TLS verification.

## Disconnected Environments (Container)

If you are running in a restricted environment or don't have Python/`oc` installed on your bastion, you can build and run the tool as a container.

1.  **Build the Image:**
    ```bash
    podman build -t myregistry.internal/virt-cluster-validate:latest -f Containerfile .
    ```
2.  **Push to your Mirror:**
    ```bash
    podman push myregistry.internal/virt-cluster-validate:latest
    ```
3.  **Run as a Job:**
    You can deploy this as a Kubernetes `Job` within your cluster. The container already includes the `oc` and `virtctl` binaries.

4.  **Run locally with Podman:**
    To test the container locally, mount your `KUBECONFIG`:
    ```bash
    podman run --rm \
      -v ${KUBECONFIG:-$HOME/.kube/config}:/opt/app-root/src/.kube/config:z \
      -e KUBECONFIG=/opt/app-root/src/.kube/config \
      myregistry.internal/virt-cluster-validate:latest
    ```

## Must-Gather Integration

The container image includes a must-gather entry point, allowing you to run the validation checks via `oc adm must-gather`. This is the easiest way to run the tool — no local prerequisites needed.

    # Run all checks
    oc adm must-gather --image=<image> -- /usr/bin/gather

    # Run only specific checks (substring match on test paths)
    oc adm must-gather --image=<image> -- CHECKS=nodes,basic /usr/bin/gather

    # Skip specific checks
    oc adm must-gather --image=<image> -- SKIP_CHECKS=high-performance,rebalance /usr/bin/gather

    # Custom timeout and concurrency
    oc adm must-gather --image=<image> -- TIMEOUT=5m CONCURRENCY=2 /usr/bin/gather

### Environment Variables

*   `CHECKS`: Comma-separated substrings to select which checks to run (maps to `--include`).
*   `SKIP_CHECKS`: Comma-separated substrings to skip certain checks (maps to `--exclude`).
*   `TIMEOUT`: Per-check timeout (e.g. `5m`, `300`. Default: `180`).
*   `CONCURRENCY`: Number of parallel checks (Default: CPU count).
*   `VIRT_VALIDATE_URL` / `VIRT_VALIDATE_TOKEN`: Remote cluster API URL and bearer token (same as `--url` / `--token`).
*   `VIRT_VALIDATE_CA_FILE`: CA certificate file for verifying the remote API (same as `--ca-file`); it cannot be combined with `VIRT_VALIDATE_INSECURE_SKIP_TLS`.
*   `VIRT_VALIDATE_INSECURE_SKIP_TLS`: Set to `true` to skip TLS verification for the remote API; it cannot be combined with `VIRT_VALIDATE_CA_FILE`.
*   `VIRT_VALIDATE_CLUSTER_DOMAIN`: Optional target ingress domain used only for conventional OpenShift tool-download Routes.
*   `VIRT_VALIDATE_OC_URL`: Optional HTTPS archive URL for a target-compatible `oc` client.
*   `VIRT_VALIDATE_VIRTCTL_URL`: Optional HTTPS archive URL for target-compatible `virtctl`. When explicit URLs are omitted, controller mode uses conventional console and `hyperconverged-cluster-cli-download-openshift-cnv` Routes without granting the target token cluster-wide Route discovery access.
*   `VIRT_VALIDATE_NAMESPACE`: Namespace in which workload checks create their short-lived resources.
*   `VIRT_VALIDATE_DATA_SOURCE`: Optional `namespace/name` override for the CDI DataSource used by the basic VM workload check. When unset, the check tries the `rhel10` and then `rhel9` aliases.
*   `VM_READY_TIMEOUT`: Maximum time to wait for the validation VM to become ready (default: `2m`). Controller mode derives it from `spec.run.executionTimeoutSeconds`.
*   `VIRT_VALIDATE_TOOL_DOWNLOAD_TIMEOUT_SECONDS`: Maximum duration of each `oc` or `virtctl` download (default: `300`).

### Output

The must-gather archive will contain:

    must-gather.local.<id>/<image-hash>/virt-cluster-validate/
    ├── ctrf-results.json    # CTRF-formatted test results
    ├── runner.log           # Runner stderr/diagnostics
    └── logs/                # Per-check execution logs
        ├── 10-openshift.d_00-login.d_test.sh.log
        ├── 10-openshift.d_10-nodes.d_test.sh.log
        └── ...

The `ctrf-results.json` file follows the [CTRF (Common Test Report Format)](https://ctrf.io) specification and can be consumed by any CTRF-compatible tooling.

## Prerequisites

The validator includes a global prerequisite check at `checks.d/prerequisite.sh`:

1. It runs **before** any tests execute
2. If it fails, the entire test suite is aborted with exit code 2
3. In `human` mode, the prerequisite output is always displayed (regardless of `-v` flags)
4. In `ctrf` and `junit` modes, successful prerequisite messages are suppressed on stdout so the machine-readable report stays clean

By default, it verifies cluster connectivity (`oc whoami`). You can customize this file to add additional checks like required operator installations, minimum cluster versions, or permissions validation.

## Controller mode (PoC)

The repository also contains a Kubernetes controller for the
`validation.kubevirt.io/v1alpha1` `VirtualizationValidation` API. It runs the
validator **on the hub cluster** and authenticates from that Job to the target
cluster API. The target is never asked to run the controller or validator.

The controller is intentionally Forklift-agnostic: it does not import, read,
or watch Forklift APIs. An integrator such as Forklift resolves Provider or
Plan data into the complete validation request and consumes its status.

### Artifacts and lifecycle

For each `VirtualizationValidation`, the controller creates and owns:

* A validator `Job` in the same namespace as the CR.
* A result `ConfigMap` containing atomic CTRF snapshots in `data.ctrf.json`.
  It is annotated with the `CTRF`/`v1` report contract.
* A dedicated ServiceAccount, Role, and RoleBinding. The Job can only get,
  patch, and update that one result ConfigMap.
* A per-run NetworkPolicy selecting only the validator Job Pods. It permits
  DNS, TCP 443/6443, and the explicit target API port.
* A target-scoped `Lease` that prevents parallel validations from creating
  canaries against the same target API. A queued request has a bounded
  `spec.run.queueTimeoutSeconds` (the suite timeout by default).

The validator reports progressively as checks finish. Every ConfigMap update
has a new Kubernetes `resourceVersion`; the controller stores the version it
has consumed in `status.observedReportResourceVersion`, alongside a bounded
projection of the CTRF summary and check results. The ConfigMap is the full,
durable report artifact. It is capped below the Kubernetes object-size limit:
if required, the publisher drops only unbounded CTRF diagnostic `trace` fields
and records `status.artifact.truncated: true`. The validator Job is retained
for seven days after completion so its Pod logs remain available for diagnosis.

The referenced target credential Secret is deliberately **not** owned by the
validation CR. Its lifecycle remains with the caller that created it, such as
Forklift.

Integrators may add ordinary metadata labels to correlate a validation with
their own objects. The recommended generic marker is
`validation.kubevirt.io/consumer=<component>` (for example, `forklift`); an
integration-specific immutable UID label, such as
`forklift.konveyor.io/provider-uid`, can carry the source object identity.
These labels are correlation metadata only and are not part of the VCV API
contract.

### Build the images and controller

The validator and controller are separate images:

```bash
# Validator runner image.
podman build -t quay.io/example/virt-cluster-validate:dev -f Containerfile .

# Controller image.
podman build -t quay.io/example/virt-validation-controller:dev \
  -f build/validation-controller/Containerfile .
```

Local controller checks are available through Make:

```bash
make fmt-controller       # format Go sources
make lint-controller      # enforce gofmt and run go vet
make test-controller      # run Go tests
make test-envtest         # validate CRD defaults and CEL against an API server
make build-controller     # build bin/validation-controller
make generate             # regenerate deepcopy and CRD artifacts
make check-controller     # lint, test, build, and verify generated artifacts
```

### Install the controller on the hub

The PoC manifests install the CRD, controller permissions, and controller
Deployment into MTV's existing `openshift-mtv` namespace. They deliberately do
not create or label that shared namespace; MTV packaging owns it and enables
platform monitoring with `openshift.io/cluster-monitoring: "true"`. Replace
both placeholder digest references with immutable images available to your hub
cluster:

```bash
oc apply -f config/crd/bases/validation.kubevirt.io_virtualizationvalidations.yaml
oc apply -f config/rbac/role.yaml
oc apply -f config/manager/manager.yaml

oc -n openshift-mtv set image deployment/virt-validation-controller \
  manager=quay.io/example/virt-validation-controller:dev
oc -n openshift-mtv patch deployment/virt-validation-controller \
  --type=strategic --patch '
spec:
  template:
    spec:
      containers:
        - name: manager
          args:
            - --validator-image=quay.io/example/virt-cluster-validate:dev
            - --leader-elect=true
            - --leader-election-namespace=openshift-mtv
'
oc -n openshift-mtv rollout status deployment/virt-validation-controller
```

The controller follows MTV's topology: `VirtualizationValidation`, its input
Secret, and all per-run artifacts remain namespaced, while the shared
controller watches validations across namespaces. The shipped `ClusterRole`
therefore has cluster-wide access to validation resources and their same-
namespace Job, ConfigMap, Secret, ServiceAccount, Role, RoleBinding,
NetworkPolicy, and Lease dependencies. It does not read Forklift `Provider` or
`Plan` objects. Its
cache is restricted to controller-labeled Jobs and ConfigMaps; credentials are
read directly by name rather than watched or retained in the cache. Leader
election is enabled by default, so it is safe to scale the Deployment only
after all replicas use the same `--leader-election-id` and namespace.

The manager exposes mTLS-protected controller-runtime metrics on port 8443
using an OpenShift service-serving certificate. The ServiceMonitor uses the
platform `tls-client-certificate-auth` scrape class; only the OpenShift
Prometheus client certificate is accepted. Health and readiness probes remain
on port 8081. Reconciliation is limited to four concurrent requests to prevent
a large set of queued validations from overwhelming the hub API.

In addition to controller-runtime reconciliation and workqueue metrics, VCV
exports `virt_validation_runs_total`, `virt_validation_duration_seconds`,
`virt_validation_queue_duration_seconds`, `virt_validation_active_runs`,
`virt_validation_queue_timeouts_total`, `virt_validation_terminal_errors_total`,
`virt_validation_executions_total`, and `virt_validation_report_reductions_total`.
Labels are limited to the supported profile, stable check ID, outcome, severity,
verdict, bounded controller error reason, and reduction level; the metrics never expose target URLs, names,
execution IDs, or node names.

The manifests apply default-deny NetworkPolicies only to the validation
controller Pods in `openshift-mtv`, then allow their DNS, Kubernetes API access
on 443/6443, and mTLS metrics ingress on 8443 from `openshift-monitoring`.
They do not alter connectivity for other MTV Pods. Each validation additionally
creates an owned per-run NetworkPolicy in its own namespace. It selects only
that run's validator Pod and allows DNS plus TCP 443/6443, as well as the
explicit port in `spec.target.url`, for the hub API, target API, and target
HTTPS tool-download route. It is removed with the validation.

Kubernetes NetworkPolicy has no portable FQDN target selector, and the API
Service can be DNATed to a node endpoint. Consequently the HTTPS policy cannot
soundly distinguish a target API from the hub API by pod selector or name. The
validation CR still accepts only an HTTPS target URL; clusters that require
tighter egress control must permit the target endpoint (or proxy) through their
own egress firewall or policy implementation.

The validation namespace must be able to reach the target API endpoint on port
6443 and its HTTPS tool-download route. If an egress firewall or proxy is in
use, allow the validator Job's egress accordingly. `HTTP_PROXY`,
`HTTPS_PROXY`, and `NO_PROXY` set on the controller Deployment are propagated
to its validator Jobs.

For this PoC, controller Jobs use the same tool bootstrap as the existing
must-gather image. The controller mounts a writable tools-only `emptyDir` at
`/usr/local/bin`; the root filesystem remains read-only. The `basic-v1`
profile verifies target authentication, CNV installation, the validation
namespace quota, then starts, snapshots, and live-migrates one canary VM. It
uses both `oc` and `virtctl`.
Tool downloads are dynamically discovered and temporarily skip TLS
verification; this is a tracked PoC limitation before promotion to production.
For clusters with a custom API hostname or disabled tool-download Routes, set
`spec.target.toolDownloads.ocURL` and `virtctlURL`; alternatively set
`clusterDomain` when only the API hostname is non-conventional.

`basic-all-nodes-v1` performs the same platform checks and starts, snapshots,
then live-migrates one canary VM on every target worker node that is Ready and
not cordoned. It uses the node's `kubernetes.io/hostname` label for VM
placement, then verifies the VMI landed on that node. Before migration, it
removes the VM node selector and verifies that change propagated to the VMI.
HCO-managed OpenShift Virtualization targets reconcile `LiveUpdate`; if that
propagation is unavailable, the per-node migration fails with an actionable
diagnostic. A per-node scheduling or migration failure is a failed validation
execution, not a skipped node.

### Create a target credential and validation request

The CR refers to a same-namespace Secret with a bearer `token` and, unless TLS
verification is disabled, a `ca.crt` key. The token belongs to the **target**
cluster and needs the permissions required by the selected checks. For the
`basic-v1` profile, this includes verifying the CNV installation, reading the
golden-image DataSource, checking quotas, and creating, reading, and deleting
the canary VM, VMI, DataVolume, and PVC in the validation namespace.
`basic-v1` additionally creates snapshot and migration resources.
`basic-all-nodes-v1` additionally creates snapshot and migration resources,
and lists target worker Nodes.

For a quick development exercise against the current cluster, create the
credential from your current `oc` session. Do not use this pattern for shared
or production clusters; create a minimally privileged target ServiceAccount
there instead.

```bash
validation_namespace=vcv-demo
oc create namespace "${validation_namespace}"

oc whoami -t > /tmp/vcv-target-token
oc get configmap kube-root-ca.crt -n "${validation_namespace}" \
  -o jsonpath='{.data.ca\.crt}' > /tmp/vcv-target-ca.crt
oc create secret generic target-cluster-credentials -n "${validation_namespace}" \
  --from-file=token=/tmp/vcv-target-token \
  --from-file=ca.crt=/tmp/vcv-target-ca.crt

api_server=$(oc whoami --show-server)
cat <<EOF | oc apply -f -
apiVersion: validation.kubevirt.io/v1alpha1
kind: VirtualizationValidation
metadata:
  name: target-canary
  namespace: ${validation_namespace}
spec:
  target:
    url: ${api_server}
    credentialsSecret:
      name: target-cluster-credentials
      tokenKey: token
      caKey: ca.crt
    identity:
      id: development-cluster
      displayName: Development cluster
    # Required only when conventional target download Routes cannot be used.
    # toolDownloads:
    #   ocURL: https://tools.example.invalid/oc.rhel9.tar
    #   virtctlURL: https://tools.example.invalid/virtctl.tar.gz
  workload:
    namespace: ${validation_namespace}
    dataSource:
      apiGroup: cdi.kubevirt.io
      kind: DataSource
      namespace: openshift-virtualization-os-images
      name: rhel10 # or rhel9 if that is the available golden image
  run:
    profile: basic-v1
    suiteTimeoutSeconds: 900
    executionTimeoutSeconds: 300
EOF
```

For a remote target, the validation CR and its Secret live on the **hub**,
while the canary workload and its target ServiceAccount live on the **target**.
The target endpoint must be reachable from hub validator Pods; no hub
credential is copied to the target cluster.

First, run the following on the **target cluster**. It creates a dedicated
validation namespace and a minimally scoped ServiceAccount for the `basic-v1`
profile, then writes the target connection material to a local directory.

```bash
target_kubeconfig=/path/to/target.kubeconfig
target_workload_namespace=vcv-validation
target_credentials_dir=/tmp/vcv-target-credentials

KUBECONFIG="${target_kubeconfig}" oc create namespace "${target_workload_namespace}" \
  --dry-run=client -o yaml | KUBECONFIG="${target_kubeconfig}" oc apply -f -
KUBECONFIG="${target_kubeconfig}" oc -n "${target_workload_namespace}" \
  create serviceaccount target-validator

cat <<EOF | KUBECONFIG="${target_kubeconfig}" oc apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: target-validator
  namespace: ${target_workload_namespace}
rules:
  - apiGroups: [""]
    resources: [persistentvolumeclaims]
    verbs: [get, list, watch, create, delete]
  - apiGroups: [""]
    resources: [resourcequotas]
    verbs: [get, list]
  - apiGroups: [cdi.kubevirt.io]
    resources: [datavolumes]
    verbs: [get, list, watch, create, delete]
  - apiGroups: [kubevirt.io]
    resources: [virtualmachines, virtualmachineinstances, virtualmachineinstancemigrations]
    verbs: [get, list, watch, create, delete]
  - apiGroups: [snapshot.kubevirt.io]
    resources: [virtualmachinesnapshots]
    verbs: [get, list, watch, create, delete]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: target-validator
  namespace: ${target_workload_namespace}
subjects:
  - kind: ServiceAccount
    name: target-validator
    namespace: ${target_workload_namespace}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: target-validator
EOF

# Required by both profiles for VolumeSnapshotClass discovery; basic-all-nodes-v1
# additionally needs Node discovery. The VM and all workflow resources remain
# constrained to the validation namespace.
cat <<EOF | KUBECONFIG="${target_kubeconfig}" oc apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: target-validator-worker-reader
rules:
  - apiGroups: [snapshot.storage.k8s.io]
    resources: [volumesnapshotclasses]
    verbs: [get, list]
  - apiGroups: [""]
    resources: [nodes]
    verbs: [list]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: target-validator-worker-reader
subjects:
  - kind: ServiceAccount
    name: target-validator
    namespace: ${target_workload_namespace}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: target-validator-worker-reader
EOF

# Permit the platform checks to verify the CNV namespace and its named
# KubeVirt installation without granting broader cluster read access.
cat <<EOF | KUBECONFIG="${target_kubeconfig}" oc apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: target-validator-cnv-namespace-reader
rules:
  - apiGroups: [""]
    resources: [namespaces]
    resourceNames: [openshift-cnv]
    verbs: [get]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: target-validator-cnv-namespace-reader
subjects:
  - kind: ServiceAccount
    name: target-validator
    namespace: ${target_workload_namespace}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: target-validator-cnv-namespace-reader
---
EOF

# Permit the canary to read the golden-image DataSource.
cat <<EOF | KUBECONFIG="${target_kubeconfig}" oc apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: target-validator-datasource-reader
  namespace: openshift-virtualization-os-images
rules:
  - apiGroups: [cdi.kubevirt.io]
    resources: [datasources]
    verbs: [get]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: target-validator-datasource-reader
  namespace: openshift-virtualization-os-images
subjects:
  - kind: ServiceAccount
    name: target-validator
    namespace: ${target_workload_namespace}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: target-validator-datasource-reader
EOF

mkdir -p "${target_credentials_dir}"
KUBECONFIG="${target_kubeconfig}" oc -n "${target_workload_namespace}" \
  create token target-validator --duration=24h > "${target_credentials_dir}/token"
KUBECONFIG="${target_kubeconfig}" oc whoami --show-server > "${target_credentials_dir}/api-server"
KUBECONFIG="${target_kubeconfig}" oc -n "${target_workload_namespace}" \
  get configmap kube-root-ca.crt -o jsonpath='{.data.ca\.crt}' > "${target_credentials_dir}/ca.crt"
chmod 600 "${target_credentials_dir}/token"
```

Transfer `token`, `ca.crt`, and `api-server` to the administrator workstation
through your approved secure channel. Do not paste the token into a terminal
history, chat, or Git repository. Then run the following against the **hub
cluster**, in a namespace shared by the validation CR and its credential
Secret. The controller watches validation CRs cluster-wide; this walkthrough
uses MTV's `openshift-mtv` namespace:

If the target API certificate chains to the validator image's system trust
store, omit both the CA file and `credentialsSecret.caKey` from the validation
request. This is preferable to disabling TLS verification.

```bash
export KUBECONFIG=/path/to/hub.kubeconfig
validation_namespace=openshift-mtv
target_credentials_dir=/tmp/vcv-target-credentials

oc -n "${validation_namespace}" create secret \
  generic target-cluster-credentials \
  --from-file=token="${target_credentials_dir}/token" \
  --from-file=ca.crt="${target_credentials_dir}/ca.crt"

target_api_server=$(cat "${target_credentials_dir}/api-server")
cat <<EOF | oc apply -f -
apiVersion: validation.kubevirt.io/v1alpha1
kind: VirtualizationValidation
metadata:
  name: target-canary
  namespace: ${validation_namespace}
spec:
  target:
    url: ${target_api_server}
    credentialsSecret:
      name: target-cluster-credentials
      tokenKey: token
      caKey: ca.crt
    identity:
      displayName: remote-target
  workload:
    namespace: vcv-validation
    dataSource:
      apiGroup: cdi.kubevirt.io
      kind: DataSource
      namespace: openshift-virtualization-os-images
      name: rhel10
  run:
    profile: basic-v1
    suiteTimeoutSeconds: 900
    executionTimeoutSeconds: 300
EOF
```

### Watch results and collect artifacts

`status.phase` is the controller lifecycle: `Pending`, `Running`, `Completed`,
`Cancelled`, or `Error`. `status.verdict` is independent from that lifecycle:
`Valid`, `ValidWithWarnings`, `Invalid`, or `Inconclusive` (with `Unknown`
while a run is in progress). A timeout, cancellation, missing report, or
controller failure is `Inconclusive`, not an `Invalid` cluster.

The report ConfigMap is updated during the run, so it can be inspected before
completion:

```bash
oc get virtualizationvalidation target-canary -n "${validation_namespace}" -w

result_configmap=$(oc get virtualizationvalidation target-canary \
  -n "${validation_namespace}" -o jsonpath='{.status.artifact.name}')
oc get configmap "${result_configmap}" -n "${validation_namespace}" \
  -o jsonpath='{.data.ctrf\.json}' > /tmp/ctrf.json
python3 -m json.tool /tmp/ctrf.json
```

Compare `status.observedReportResourceVersion` with the result ConfigMap's
`metadata.resourceVersion` to confirm that the controller has consumed the
latest snapshot. The Job and Pod remain available until the Job's seven-day TTL
expires, which allows normal Kubernetes log collection:

```bash
oc get virtualizationvalidation target-canary -n "${validation_namespace}" \
  -o jsonpath='{.status.observedReportResourceVersion}{"\n"}'
oc get configmap "${result_configmap}" -n "${validation_namespace}" \
  -o jsonpath='{.metadata.resourceVersion}{"\n"}'

oc get job,pod,configmap -n "${validation_namespace}" \
  -l validation.kubevirt.io/validation=target-canary
```

Delete the CR to remove the controller-owned Job, report ConfigMap, and
per-run RBAC objects. Job termination is forwarded to running checks so their
target-side cleanup traps can remove canary resources; the canary VM is also
labeled with `validation.kubevirt.io/validation-uid` for manual recovery.
Delete the caller-owned credential Secret separately:

```bash
oc delete virtualizationvalidation target-canary -n "${validation_namespace}"
oc delete secret target-cluster-credentials -n "${validation_namespace}"
```

`config/samples/validation_v1alpha1_virtualizationvalidation.yaml` contains a
static `basic-v1` example. `config/samples/validation_v1alpha1_virtualizationvalidation_all_nodes.yaml`
contains the corresponding `basic-all-nodes-v1` example; it sets
`maxConcurrentExecutions: 1` so that the per-worker canary runs serially.

The trusted profile catalog includes `basic-v1` and `basic-all-nodes-v1`.
Both verify target authentication, CNV installation, validation-namespace
quota, and a canary workflow. `basic-v1` starts, snapshots, and live-migrates
one VM; `basic-all-nodes-v1` does the same for one hard-pinned VM on every
Ready, non-cordoned worker, then removes its node selector before migration.
HCO-managed OpenShift Virtualization targets reconcile `LiveUpdate`; the
profile detects a missing propagation from the VMI itself. Both require a
dedicated `workload.namespace` and a typed CDI DataSource reference. A
`VirtualizationValidation` is one immutable run. Set a new `spec.run.id` to
request a deliberate rerun using the same object; Secret rotation, a controller
image upgrade, or a deleted Job never silently starts a new run. Consumers should use the `Valid`,
`Progressing`, and `Completed` conditions together with `phase` and `verdict`;
`status.artifact` (including its API version, key, and truncation state) locates
the complete CTRF report. `status.report` identifies the observed CTRF
snapshot, while `status.executions` is a bounded projection intended for
consumers and UIs: `checkId` groups a logical check using a stable VCV-defined
identifier and `executionId` identifies each concrete
execution (for example, a canary VM on a particular node). Per-execution
`subject` identifies the target object or scope without parsing IDs. String
parameters distinguish requested placement (`requestedNodeName`,
`requestedNodeHostname`) from the observed VMI placement (`observedNodeName`). Workflow execution
steps are projected as `status.executions[].steps`, including a bounded string
`extra` map; full CTRF details, including arbitrary parameter values and retry
history, remain in the report artifact. `status.executionsTruncated`,
`status.executions[].parametersTruncated`, and
`status.executions[].stepsTruncated` make projection bounds explicit.
Each execution also reports `startedAt`, `completedAt`, and
`durationMilliseconds` when supplied by the executor, so concurrent work can
be placed on a timeline.

`spec.run.maxConcurrentExecutions` is an optional limit (1-64) on concurrent
profile execution units. When omitted, the executor uses its own default. It
is intended for profiles such as future per-node validation and is not a
generic pass-through for runner flags.

### Cancel a running validation

Set `spec.run.cancel` to `true` to stop a running validation without deleting
its CR or CTRF artifact:

```bash
validation_namespace=openshift-mtv
oc -n "${validation_namespace}" patch virtualizationvalidation target-canary \
  --type=merge -p '{"spec":{"run":{"cancel":true}}}'
```

Cancellation is one-way. The controller deletes the active validator Job,
releases the target lease, and records the terminal `Cancelled` phase. It does
not delete target resources retained by the selected `cleanupPolicy`; create a
new `VirtualizationValidation` to run the validation again.

`Error` is terminal too. Correct an invalid request or unavailable credential
by fixing the request and assigning a new `spec.run.id`, or by creating a new
`VirtualizationValidation`.

`spec.run.queueTimeoutSeconds` bounds time spent in `Pending` while another
validation holds the target Lease. It defaults to the suite timeout. A queue
timeout enters terminal `Error` with the `QueueTimeout` reason; it never starts
a Job later if the Lease becomes available.

Before uninstalling the controller, delete or cancel all validations and wait
for their finalizers to clear. If the controller has already been removed and
a CR is stuck in deletion, an administrator may remove only
`validation.kubevirt.io/finalizer` after confirming that no validation Job is
still running. This is a recovery procedure, not the normal uninstall path.

`spec.run.suiteTimeoutSeconds` limits the whole validator Job (default 900,
maximum 86400),
while `spec.run.executionTimeoutSeconds` limits one check execution (default
180). The execution timeout cannot exceed the suite timeout. For an all-node
run, set the suite timeout for the complete node set and the execution timeout
for a single VM import and boot.

`spec.run.cleanupPolicy` controls target workload cleanup: `Always` (default)
deletes all validation VM resources, `OnFailure` retains resources from failed
executions, and `Never` retains all of them. Retained resource names and their
target namespace are included in the execution's CTRF parameters and projected
into `status.executions[].parameters`. Controller-owned Job, report ConfigMap,
and RBAC resources keep their normal lifecycle.

## Development & Testing

### Unit Tests

    python3 -m unittest discover -s tests

### AI-Assisted Development

This project includes developer skills for both [Claude Code](https://claude.ai/code) and [Gemini CLI](https://github.com/google/gemini-cli) to ensure architectural consistency.

**Claude Code**:
The `.claude/skills/` directory contains project-specific skills that are automatically loaded when working in this repository.

**Gemini CLI**:
To enable the Gemini skill in your workspace:

    gemini skills install .gemini/skills/virt-cluster-validate-developer-skill/ --scope workspace

See [CONTRIBUTING.md](CONTRIBUTING.md) for how to write new checks.
