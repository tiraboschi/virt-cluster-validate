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
*   `-t, --timeout SPAN`: Max execution time per test (e.g. `2m`, `45s`, `180`. Default: `180`).
*   `-c, --concurrency N`: Number of tests to run in parallel (Default: Number of CPU cores).
*   `-f [N], --fail-fast [N]`: Stop execution after N failures (Default: 1).
*   `--url URL`: OpenShift API server URL. Must be used with `--token`.
*   `--token TOKEN`: Bearer token for `--url` (service account or user token).
*   `--ca-file PATH`: CA certificate file used to verify `--url` (use instead of `--insecure-skip-tls`).
*   `--insecure-skip-tls`: Skip TLS verification when using `--url` (typical for self-signed API certs).

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
*   `VIRT_VALIDATE_CA_FILE`: CA certificate file for verifying the remote API (same as `--ca-file`).
*   `VIRT_VALIDATE_INSECURE_SKIP_TLS`: Set to `true` to skip TLS verification for the remote API.

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
