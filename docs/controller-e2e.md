# Controller end-to-end development

These helpers exercise the hub-side controller against a remote OpenShift
target. They are developer tooling: they require cluster-admin-equivalent
access on the target to install the deliberately narrow validation identity.

Build images and make them pullable by the hub cluster:

```bash
make image-validator VALIDATOR_IMAGE=quay.io/$USER/virt-cluster-validate:dev
make image-controller CONTROLLER_IMAGE=quay.io/$USER/virt-validation-controller:dev
```

Deploy the controller to the MTV namespace on the hub. The deployment manifest
is intentionally packaged for `openshift-mtv`; the helper refuses another
namespace instead of silently creating a partial installation.

```bash
make deploy-controller \
  CONTROLLER_IMAGE=quay.io/$USER/virt-validation-controller:dev \
  VALIDATOR_IMAGE=quay.io/$USER/virt-cluster-validate:dev
```

Bootstrap the target identity. For a remote target, `TARGET_KUBECONFIG` points
at that cluster while the active `oc` context remains the hub. Omitting it uses
the active hub context as a loopback target, which is the intended OpenShift CI
topology.

```bash
export TARGET_KUBECONFIG=/path/to/target.kubeconfig
export TARGET_NAMESPACE=vcv-validation
make bootstrap-target
```

For the loopback case, leave `TARGET_KUBECONFIG` unset:

```bash
unset TARGET_KUBECONFIG
export TARGET_NAMESPACE=vcv-validation
make bootstrap-target
```

Start a validation and collect its CR, validator Job logs, and CTRF ConfigMap.
The helper obtains a short-lived token for the target ServiceAccount and derives
the target API URL and CA from `TARGET_KUBECONFIG` when set, or from the active
hub context in loopback mode. Set `TARGET_TOKEN`, `TARGET_URL`, or
`TARGET_CA_FILE` to override those inputs explicitly.

```bash
make run-controller-e2e
```

The command succeeds only when the validation reaches `Completed` with `Valid`
or `ValidWithWarnings`. It leaves the validation and credential Secret in place
for debugging. Remove them explicitly when they are no longer needed.
