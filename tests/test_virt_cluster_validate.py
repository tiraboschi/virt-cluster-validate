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

import os
import sys
import json
import re
import runpy
import time
import unittest
import subprocess
import tempfile
from pathlib import Path
from xml.etree import ElementTree as ET

# Path to the script we are testing
RUNNER_SCRIPT = Path(__file__).parent.parent / "virt-cluster-validate"
CONTROLLER_SOURCE = Path(__file__).parent.parent / "internal/controller/virtualizationvalidation_controller.go"

class TestVirtClusterValidate(unittest.TestCase):
    def setUp(self):
        # Create a temporary environment to act as our workspace
        self.test_dir = tempfile.TemporaryDirectory()
        self.workspace = Path(self.test_dir.name)
        
        # Create a mock checks.d directory
        self.checks_dir = self.workspace / "checks.d"
        self.checks_dir.mkdir()
        
        # We need to symlink the 'bin' directory so helper scripts work
        os.symlink(Path(__file__).parent.parent / "bin", self.workspace / "bin")

    def tearDown(self):
        self.test_dir.cleanup()

    def _create_test(self, name, content):
        """Helper to create a test script in the mock checks.d directory."""
        test_dir = self.checks_dir / name
        test_dir.mkdir(parents=True, exist_ok=True)
        test_file = test_dir / "test.sh"
        test_file.write_text(content)
        # Make the test executable
        test_file.chmod(0o755)
        return test_file

    def _create_prerequisite(self, content):
        """Helper to create a prerequisite script."""
        prerequisite = self.checks_dir / "prerequisite.sh"
        prerequisite.write_text(content)
        prerequisite.chmod(0o755)
        return prerequisite

    def test_runner_ctrf_output(self):
        """Test that the runner correctly identifies passes and failures and outputs CTRF."""

        self._create_test("10-pass.d", "#!/bin/bash\necho 'I passed!'\nexit 0")
        self._create_test("20-fail.d", "#!/bin/bash\necho 'I failed!'\nexit 1")

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf"],
            cwd=self.workspace,
            capture_output=True,
            text=True
        )

        self.assertEqual(res.returncode, 1)

        output = json.loads(res.stdout)
        summary = output["results"]["summary"]
        tests = output["results"]["tests"]

        self.assertEqual(summary["passed"], 1)
        self.assertEqual(summary["failed"], 1)
        self.assertEqual(summary["tests"], 2)
        self.assertEqual(len(tests), 2)

        passed_test = next(t for t in tests if "10-pass" in t["name"])
        self.assertEqual(passed_test["testId"], passed_test["name"])
        self.assertEqual(passed_test["executionId"], passed_test["name"])
        self.assertEqual(passed_test["status"], "passed")

        failed_test = next(t for t in tests if "20-fail" in t["name"])
        self.assertEqual(failed_test["status"], "failed")
        self.assertIn("trace", failed_test)
        self.assertTrue("I failed!" in failed_test["trace"])

    def test_profiles_have_stable_check_ids(self):
        runner = runpy.run_path(str(RUNNER_SCRIPT))
        controller = CONTROLLER_SOURCE.read_text()
        table = re.search(r"var stableCheckIDs = map\[string\]string\{(.*?)\n\}", controller, re.DOTALL)
        self.assertIsNotNone(table, "stableCheckIDs table not found")
        stable_ids = dict(re.findall(r'^\s*"([^"]+)":\s*"([^"]+)",$', table.group(1), re.MULTILINE))

        for profile, checks in runner["PROFILES"].items():
            for check in checks["cluster_checks"]:
                test_id = runner["execution_task"](check, profile=profile)["test_id"]
                self.assertIn(test_id, stable_ids, f"{profile} emits unmapped CTRF testId {test_id!r} from {check!r}")
            for check in checks["node_checks"]:
                test_id = runner["execution_task"](check, node_name="probe", profile=profile)["test_id"]
                self.assertIn(test_id, stable_ids, f"{profile} emits unmapped CTRF testId {test_id!r} from {check!r}")

    def test_publisher_advisory_ids_match_controller_severity(self):
        previous = {key: os.environ.get(key) for key in ("VIRT_VALIDATE_RESULT_CONFIGMAP", "KUBERNETES_SERVICE_HOST")}
        os.environ["VIRT_VALIDATE_RESULT_CONFIGMAP"] = "result"
        os.environ["KUBERNETES_SERVICE_HOST"] = "kubernetes"
        try:
            publisher = runpy.run_path(str(RUNNER_SCRIPT.parent / "bin/publish-ctrf"))
        finally:
            for key, value in previous.items():
                if value is None:
                    os.environ.pop(key, None)
                else:
                    os.environ[key] = value

        controller = CONTROLLER_SOURCE.read_text()
        stable = re.search(r"var stableCheckIDs = map\[string\]string\{(.*?)\n\}", controller, re.DOTALL)
        severities = re.search(r"var checkSeverities = map\[string\]validationv1alpha1.ValidationSeverity\{(.*?)\n\}", controller, re.DOTALL)
        self.assertIsNotNone(stable)
        self.assertIsNotNone(severities)
        stable_ids = dict(re.findall(r'^\s*"([^"]+)":\s*"([^"]+)",$', stable.group(1), re.MULTILINE))
        advisory_checks = set(re.findall(r'^\s*"([^"]+)":\s*validationv1alpha1.ValidationSeverityAdvisory,?$', severities.group(1), re.MULTILINE))
        expected = {test_id for test_id, check_id in stable_ids.items() if check_id in advisory_checks}
        self.assertEqual(publisher["ADVISORY_TEST_IDS"], expected)

    def test_report_reduction_uses_the_minimum_sufficient_rung(self):
        previous = {key: os.environ.get(key) for key in ("VIRT_VALIDATE_RESULT_CONFIGMAP", "KUBERNETES_SERVICE_HOST")}
        os.environ["VIRT_VALIDATE_RESULT_CONFIGMAP"] = "result"
        os.environ["KUBERNETES_SERVICE_HOST"] = "kubernetes"
        try:
            publisher = runpy.run_path(str(RUNNER_SCRIPT.parent / "bin/publish-ctrf"))
        finally:
            for key, value in previous.items():
                if value is None:
                    os.environ.pop(key, None)
                else:
                    os.environ[key] = value

        report = {
            "reportFormat": "CTRF",
            "specVersion": "0.0.0",
            "results": {
                "summary": {"tests": 2, "passed": 1, "failed": 1, "pending": 0, "skipped": 0, "other": 0},
                "tests": [
                    {"testId": "pass", "executionId": "pass", "name": "pass", "status": "passed"},
                    {"testId": "fail", "executionId": "fail", "name": "fail", "status": "failed", "trace": "x" * 1000},
                ],
            },
        }
        reduced = publisher["reduce_report"](report, budget=700)
        descriptor = reduced["results"]["extra"]["validation.kubevirt.io/reduction"]
        self.assertEqual(descriptor["level"], 1)
        self.assertNotIn("trace", reduced["results"]["tests"][1])

    def test_report_reduction_collapses_pending_before_outcomes(self):
        previous = {key: os.environ.get(key) for key in ("VIRT_VALIDATE_RESULT_CONFIGMAP", "KUBERNETES_SERVICE_HOST")}
        os.environ["VIRT_VALIDATE_RESULT_CONFIGMAP"] = "result"
        os.environ["KUBERNETES_SERVICE_HOST"] = "kubernetes"
        try:
            publisher = runpy.run_path(str(RUNNER_SCRIPT.parent / "bin/publish-ctrf"))
        finally:
            for key, value in previous.items():
                if value is None:
                    os.environ.pop(key, None)
                else:
                    os.environ[key] = value

        tests = [{"testId": f"node/{index}", "executionId": f"node/{index}", "name": f"node/{index}", "status": "pending"} for index in range(100)]
        report = {"reportFormat": "CTRF", "specVersion": "0.0.0", "results": {"summary": {"tests": 100, "passed": 0, "failed": 0, "pending": 100, "skipped": 0, "other": 0}, "tests": tests}}
        reduced = publisher["reduce_report"](report, budget=400)
        descriptor = reduced["results"]["extra"]["validation.kubevirt.io/reduction"]
        self.assertEqual(descriptor["level"], 2)
        self.assertEqual(descriptor["pendingCollapsed"], 100)
        self.assertEqual(reduced["results"]["tests"], [])

    def test_runner_ctrf_output_ignores_prerequisite_success_stdout(self):
        """Test that prerequisite success output does not pollute CTRF stdout."""
        self._create_prerequisite("#!/bin/bash\npass_with info 'Prerequisite passed'\nexit 0")
        self._create_test("10-pass.d", "#!/bin/bash\necho 'I passed!'\nexit 0")

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf"],
            cwd=self.workspace,
            capture_output=True,
            text=True
        )

        self.assertEqual(res.returncode, 0)
        output = json.loads(res.stdout)
        self.assertEqual(output["results"]["summary"]["passed"], 1)
        self.assertEqual(res.stderr, "")

    def test_runner_ctrf_reports_retained_artifacts(self):
        self._create_test(
            "10-retained.d",
            "#!/bin/bash\n"
            "printf 'retainedNamespace=vcv-validation\\nretainedVM=canary-vm\\n' > \"$VIRT_VALIDATE_ARTIFACTS_FILE\"\n"
            "exit 1\n",
        )

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf"],
            cwd=self.workspace,
            capture_output=True,
            text=True,
        )

        self.assertEqual(res.returncode, 1)
        test = json.loads(res.stdout)["results"]["tests"][0]
        self.assertEqual(test["parameters"], {"retainedNamespace": "vcv-validation", "retainedVM": "canary-vm"})

    def test_runner_ctrf_marks_warnings_structurally(self):
        self._create_test("10-warning.d", "#!/bin/bash\npass_with warn 'quota may affect validation'\n")

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf"],
            cwd=self.workspace,
            capture_output=True,
            text=True,
        )

        self.assertEqual(res.returncode, 0, res.stderr)
        test = json.loads(res.stdout)["results"]["tests"][0]
        self.assertEqual(test["parameters"]["validation.kubevirt.io/advisory-warning"], "true")

    def test_runner_ctrf_reports_workflow_steps(self):
        self._create_test(
            "10-steps.d",
            "#!/bin/bash\n"
            "printf '%s\\n' '{\"name\":\"Start VM\",\"status\":\"pending\",\"extra\":{\"state\":\"running\"}}' > \"$VIRT_VALIDATE_STEPS_FILE\"\n"
            "printf '%s\\n' '{\"name\":\"Start VM\",\"status\":\"passed\"}' >> \"$VIRT_VALIDATE_STEPS_FILE\"\n"
            "printf '%s\\n' '{\"name\":\"Snapshot\",\"status\":\"pending\",\"extra\":{\"state\":\"queued\"}}' >> \"$VIRT_VALIDATE_STEPS_FILE\"\n",
        )

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf"],
            cwd=self.workspace,
            capture_output=True,
            text=True,
        )

        self.assertEqual(res.returncode, 0, res.stderr)
        steps = json.loads(res.stdout)["results"]["tests"][0]["steps"]
        self.assertEqual(steps, [{"name": "Start VM", "status": "passed"}, {"name": "Snapshot", "status": "pending", "extra": {"state": "queued"}}])

    def test_runner_publishes_running_steps_before_check_completes(self):
        self._create_test(
            "10-progress.d",
            "#!/bin/bash\n"
            "printf '%s\\n' '{\"name\":\"Snapshot\",\"status\":\"pending\",\"extra\":{\"state\":\"running\"}}' > \"$VIRT_VALIDATE_STEPS_FILE\"\n"
            "sleep 3\n",
        )
        report_path = self.workspace / "ctrf.json"
        process = subprocess.Popen(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf", "--report-file", str(report_path)],
            cwd=self.workspace,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        report = None
        deadline = time.time() + 2.5
        while time.time() < deadline:
            if report_path.exists():
                candidate = json.loads(report_path.read_text())
                steps = candidate["results"]["tests"][0].get("steps", [])
                if steps:
                    report = candidate
                    break
            time.sleep(0.1)
        stdout, stderr = process.communicate()
        self.assertEqual(process.returncode, 0, stderr or stdout)
        self.assertIsNotNone(report, "running step was not published before check completion")
        self.assertEqual(report["results"]["tests"][0]["steps"], [{"name": "Snapshot", "status": "pending", "extra": {"state": "running"}}])

    def test_basic_profile_runs_cluster_workflow_with_three_steps(self):
        self._create_test(
            "50-openshift-virtualization.d/90-canary-workflow.d",
            "#!/bin/bash\n"
            "test \"$VIRT_VALIDATE_PROFILE\" = basic-v1\n"
            "test -z \"${VIRT_VALIDATE_NODE:-}\"\n"
            "printf '%s\\n' '{\"name\":\"Start VM\",\"status\":\"passed\"}' > \"$VIRT_VALIDATE_STEPS_FILE\"\n"
            "printf '%s\\n' '{\"name\":\"Snapshot\",\"status\":\"passed\"}' >> \"$VIRT_VALIDATE_STEPS_FILE\"\n"
            "printf '%s\\n' '{\"name\":\"Live migrate VM\",\"status\":\"passed\"}' >> \"$VIRT_VALIDATE_STEPS_FILE\"\n",
        )

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf", "--profile", "basic-v1"],
            cwd=self.workspace,
            capture_output=True,
            text=True,
        )

        self.assertEqual(res.returncode, 0, res.stderr)
        test = json.loads(res.stdout)["results"]["tests"][0]
        self.assertEqual(test["executionId"], "cluster")
        self.assertEqual(test["testId"], "basic-v1/canary-workflow")
        self.assertEqual([step["name"] for step in test["steps"]], ["Start VM", "Snapshot", "Live migrate VM"])

    def test_basic_profile_publishes_queued_workflow_steps(self):
        self._create_test(
            "50-openshift-virtualization.d/90-canary-workflow.d",
            "#!/bin/bash\n"
            "sleep 3\n",
        )
        report_path = self.workspace / "ctrf.json"
        process = subprocess.Popen(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf", "--profile", "basic-v1", "--report-file", str(report_path)],
            cwd=self.workspace,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        try:
            deadline = time.time() + 2.5
            while time.time() < deadline and not report_path.exists():
                time.sleep(0.1)
            report = json.loads(report_path.read_text())
        finally:
            stdout, stderr = process.communicate()
        self.assertEqual(process.returncode, 0, stderr or stdout)
        self.assertEqual(
            report["results"]["tests"][0]["steps"],
            [
                {"name": "Start VM", "status": "pending", "extra": {"state": "queued"}},
                {"name": "Snapshot", "status": "pending", "extra": {"state": "queued"}},
                {"name": "Live migrate VM", "status": "pending", "extra": {"state": "queued"}},
            ],
        )

    def test_timeout_marks_running_step_failed_and_future_steps_skipped(self):
        self._create_test(
            "50-openshift-virtualization.d/90-canary-workflow.d",
            "#!/bin/bash\n"
            "printf '%s\\n' '{\"name\":\"Start VM\",\"status\":\"passed\"}' > \"$VIRT_VALIDATE_STEPS_FILE\"\n"
            "printf '%s\\n' '{\"name\":\"Snapshot\",\"status\":\"pending\",\"extra\":{\"state\":\"running\"}}' >> \"$VIRT_VALIDATE_STEPS_FILE\"\n"
            "printf '%s\\n' '{\"name\":\"Live migrate VM\",\"status\":\"pending\",\"extra\":{\"state\":\"queued\"}}' >> \"$VIRT_VALIDATE_STEPS_FILE\"\n"
            "sleep 3\n",
        )
        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf", "--profile", "basic-v1", "-t", "1"],
            cwd=self.workspace,
            capture_output=True,
            text=True,
        )

        self.assertEqual(res.returncode, 1, res.stderr)
        test = json.loads(res.stdout)["results"]["tests"][0]
        steps = test["steps"]
        self.assertEqual(steps[0]["status"], "passed")
        self.assertEqual(steps[1], {"name": "Snapshot", "status": "failed", "extra": {"state": "timedOut"}})
        self.assertEqual(steps[2], {"name": "Live migrate VM", "status": "skipped", "extra": {"state": "skipped"}})
        self.assertEqual(test["extra"]["validation.kubevirt.io/reason"], "SnapshotTimedOut")
        self.assertEqual(test["extra"]["validation.kubevirt.io/outcome"], "TimedOut")

    def test_all_nodes_profile_creates_one_ctrf_execution_per_eligible_worker(self):
        self._create_test(
            "10-openshift.d/00-login.d",
            "#!/bin/bash\n"
            "test -z \"${VIRT_VALIDATE_NODE:-}\"\n",
        )
        self._create_test(
            "50-openshift-virtualization.d/90-canary-workflow.d",
            "#!/bin/bash\n"
            "case \"${VIRT_VALIDATE_PROFILE}:${VIRT_VALIDATE_NODE}:${VIRT_VALIDATE_NODE_HOSTNAME}\" in\n"
            "  basic-all-nodes-v1:worker-a:host-a|basic-all-nodes-v1:worker-b:host-b) echo \"$VIRT_VALIDATE_NODE\" >> \"$VIRT_VALIDATE_CACHE_DIR/nodes\"; exit 0 ;;\n"
            "  *) exit 1 ;;\n"
            "esac\n",
        )
        fake_bin = self.workspace / "fake-bin"
        fake_bin.mkdir()
        fake_oc = fake_bin / "oc"
        fake_oc.write_text(
            "#!/bin/bash\n"
            "cat <<'EOF'\n"
            '{"items":['
            '{"metadata":{"name":"worker-a","labels":{"kubernetes.io/hostname":"host-a"}},"status":{"conditions":[{"type":"Ready","status":"True"}]},"spec":{}},'
            '{"metadata":{"name":"worker-b","labels":{"kubernetes.io/hostname":"host-b"}},"status":{"conditions":[{"type":"Ready","status":"True"}]},"spec":{}},'
            '{"metadata":{"name":"cordoned","labels":{"kubernetes.io/hostname":"host-c"}},"status":{"conditions":[{"type":"Ready","status":"True"}]},"spec":{"unschedulable":true}}'
            ']}\nEOF\n'
        )
        fake_oc.chmod(0o755)
        run_env = os.environ.copy()
        run_env["PATH"] = f"{fake_bin}{os.pathsep}{run_env['PATH']}"
        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf", "--profile", "basic-all-nodes-v1"],
            cwd=self.workspace,
            env=run_env,
            capture_output=True,
            text=True,
        )
        self.assertEqual(res.returncode, 0, res.stderr)
        tests = json.loads(res.stdout)["results"]["tests"]
        cluster_tests = [test for test in tests if test["testId"] != "basic-all-nodes-v1/canary-workflow"]
        node_tests = [test for test in tests if test["testId"] == "basic-all-nodes-v1/canary-workflow"]
        self.assertEqual(len(cluster_tests), 1)
        self.assertEqual({test["executionId"] for test in cluster_tests}, {"10-openshift.d/00-login.d/test.sh"})
        self.assertEqual(len(node_tests), 2)
        self.assertEqual({test["executionId"] for test in node_tests}, {"node/worker-a", "node/worker-b"})
        self.assertEqual({test["parameters"]["requestedNodeHostname"] for test in node_tests}, {"host-a", "host-b"})
        self.assertTrue(all([step["name"] for step in test["steps"]] == ["Start VM", "Snapshot", "Live migrate VM"] for test in node_tests))

    def test_runner_ctrf_prerequisite_failure_goes_to_stderr(self):
        """Test that prerequisite failures are reported on stderr in CTRF mode."""
        self._create_prerequisite("#!/bin/bash\nfail_with 'Prerequisite failed'\n")
        self._create_test("10-pass.d", "#!/bin/bash\necho 'I passed!'\nexit 0")

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf"],
            cwd=self.workspace,
            capture_output=True,
            text=True
        )

        self.assertEqual(res.returncode, 2)
        self.assertEqual(res.stdout, "")
        self.assertIn("PREREQUISITE FAILURE", res.stderr)
        self.assertIn("Prerequisite failed", res.stderr)

    def test_runner_junit_output(self):
        """Test that the runner emits JUnit XML with pass/fail cases."""
        self._create_test("10-pass.d", "#!/bin/bash\npass_with info 'All good'\nexit 0")
        self._create_test("20-fail.d", "#!/bin/bash\nfail_with 'Something broke'\n")

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "junit"],
            cwd=self.workspace,
            capture_output=True,
            text=True
        )

        self.assertEqual(res.returncode, 1)

        root = ET.fromstring(res.stdout)
        testsuite = root.find("testsuite")
        self.assertIsNotNone(testsuite)
        self.assertEqual(testsuite.attrib["tests"], "2")
        self.assertEqual(testsuite.attrib["failures"], "1")
        self.assertEqual(testsuite.attrib["skipped"], "0")

        testcases = {case.attrib["name"]: case for case in testsuite.findall("testcase")}
        self.assertIn("10-pass.d/test.sh", testcases)
        self.assertIn("20-fail.d/test.sh", testcases)

        self.assertIsNone(testcases["10-pass.d/test.sh"].find("failure"))
        self.assertIsNone(testcases["10-pass.d/test.sh"].find("system-out"))
        failure = testcases["20-fail.d/test.sh"].find("failure")
        self.assertIsNotNone(failure)
        self.assertIn("Something broke", failure.attrib["message"])
        self.assertIsNotNone(testcases["20-fail.d/test.sh"].find("system-out"))

    def test_runner_junit_verbose_includes_system_out_for_passes(self):
        """Test that -v enables system-out for passing tests in JUnit mode."""
        self._create_test("10-pass.d", "#!/bin/bash\npass_with info 'All good'\nexit 0")

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "junit", "-v"],
            cwd=self.workspace,
            capture_output=True,
            text=True
        )

        self.assertEqual(res.returncode, 0)

        root = ET.fromstring(res.stdout)
        testsuite = root.find("testsuite")
        self.assertIsNotNone(testsuite)
        testcase = testsuite.find("testcase")
        self.assertIsNotNone(testcase)
        system_out = testcase.find("system-out")
        self.assertIsNotNone(system_out)
        self.assertIn("All good", system_out.text)

    def test_runner_junit_output_ignores_prerequisite_success_stdout(self):
        """Test that prerequisite success output does not pollute JUnit stdout."""
        self._create_prerequisite("#!/bin/bash\npass_with info 'Prerequisite passed'\nexit 0")
        self._create_test("10-pass.d", "#!/bin/bash\necho 'I passed!'\nexit 0")

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "junit"],
            cwd=self.workspace,
            capture_output=True,
            text=True
        )

        self.assertEqual(res.returncode, 0)
        root = ET.fromstring(res.stdout)
        testsuite = root.find("testsuite")
        self.assertIsNotNone(testsuite)
        self.assertEqual(testsuite.attrib["tests"], "1")
        self.assertEqual(res.stderr, "")

    def test_runner_junit_no_tests_still_emits_xml(self):
        """Test that JUnit mode stays machine-readable when no tests are selected."""
        self._create_test("10-pass.d", "#!/bin/bash\nexit 0")

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "junit", "--include", "does-not-match"],
            cwd=self.workspace,
            capture_output=True,
            text=True
        )

        self.assertEqual(res.returncode, 0)
        root = ET.fromstring(res.stdout)
        testsuite = root.find("testsuite")
        self.assertIsNotNone(testsuite)
        self.assertEqual(testsuite.attrib["tests"], "0")
        self.assertEqual(testsuite.attrib["failures"], "0")
        self.assertEqual(testsuite.attrib["skipped"], "0")
        self.assertEqual(res.stderr, "")

    def test_runner_junit_prerequisite_failure_goes_to_stderr(self):
        """Test that prerequisite failures are reported on stderr in JUnit mode."""
        self._create_prerequisite("#!/bin/bash\nfail_with 'Prerequisite failed'\n")
        self._create_test("10-pass.d", "#!/bin/bash\necho 'I passed!'\nexit 0")

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "junit"],
            cwd=self.workspace,
            capture_output=True,
            text=True
        )

        self.assertEqual(res.returncode, 2)
        self.assertEqual(res.stdout, "")
        self.assertIn("PREREQUISITE FAILURE", res.stderr)
        self.assertIn("Prerequisite failed", res.stderr)

    def test_runner_junit_file_written_alongside_ctrf(self):
        """Test that --junit-file writes JUnit XML while stdout gets CTRF JSON."""
        self._create_test("10-pass.d", "#!/bin/bash\npass_with info 'All good'\nexit 0")
        self._create_test("20-fail.d", "#!/bin/bash\nfail_with 'Something broke'\n")

        junit_path = os.path.join(self.workspace, "junit-out.xml")
        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf", "--junit-file", junit_path],
            cwd=self.workspace,
            capture_output=True,
            text=True
        )

        self.assertEqual(res.returncode, 1)

        ctrf = json.loads(res.stdout)
        self.assertEqual(ctrf["results"]["summary"]["tests"], 2)
        self.assertEqual(ctrf["results"]["summary"]["failed"], 1)

        self.assertTrue(os.path.exists(junit_path))
        root = ET.parse(junit_path).getroot()
        testsuite = root.find("testsuite")
        self.assertIsNotNone(testsuite)
        self.assertEqual(testsuite.attrib["tests"], "2")
        self.assertEqual(testsuite.attrib["failures"], "1")

    def test_runner_junit_file_no_tests(self):
        """Test that --junit-file writes valid empty XML when no tests match."""
        self._create_test("10-pass.d", "#!/bin/bash\nexit 0")

        junit_path = os.path.join(self.workspace, "junit-out.xml")
        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf", "--junit-file", junit_path, "--include", "does-not-match"],
            cwd=self.workspace,
            capture_output=True,
            text=True
        )

        self.assertEqual(res.returncode, 0)
        self.assertTrue(os.path.exists(junit_path))
        root = ET.parse(junit_path).getroot()
        testsuite = root.find("testsuite")
        self.assertIsNotNone(testsuite)
        self.assertEqual(testsuite.attrib["tests"], "0")
        self.assertEqual(testsuite.attrib["failures"], "0")

    def test_runner_fail_fast(self):
        """Test that the runner correctly stops after N failures when --fail-fast is used."""

        # In a ThreadPoolExecutor with N workers, Python proactively schedules N tasks.
        # If max_workers is 1, it will queue up the first task, but it STILL pre-fetches
        # the next task and submits it to the underlying work queue. So `cancel()` can only
        # safely abort tasks that haven't been popped off the queue yet.
        #
        # By creating 4 tests and limiting workers to 1, we guarantee at least some of
        # the trailing tests are trapped in the pending queue and can be cancelled.
        self._create_test("10-fail.d", "#!/bin/bash\necho 'fail'\nexit 1")
        self._create_test("20-long.d", "#!/bin/bash\nsleep 1\nexit 0")
        self._create_test("30-long.d", "#!/bin/bash\nsleep 1\nexit 0")
        self._create_test("40-long.d", "#!/bin/bash\nsleep 1\nexit 0")

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf", "-f", "1", "-c", "1"],
            cwd=self.workspace,
            capture_output=True,
            text=True
        )

        self.assertEqual(res.returncode, 1)
        output = json.loads(res.stdout)
        summary = output["results"]["summary"]

        self.assertGreater(summary["skipped"], 0, f"Summary was: {summary}")

        executed_names = [t["name"] for t in output["results"]["tests"] if t["status"] != "skipped"]
        self.assertTrue(any("10-fail" in n for n in executed_names))

    def test_runner_timeout(self):
        """Test that the runner correctly enforces timeouts and kills hung processes."""
        self._create_test("10-timeout.d", "#!/bin/bash\necho 'start'\nsleep 10\necho 'done'\nexit 0")

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf", "-t", "2"],
            cwd=self.workspace,
            capture_output=True,
            text=True
        )

        self.assertEqual(res.returncode, 1)
        output = json.loads(res.stdout)
        tests = output["results"]["tests"]

        self.assertEqual(len(tests), 1)
        self.assertEqual(tests[0]["status"], "failed")
        self.assertIn("TIMEOUT", tests[0].get("trace", ""))
        self.assertIn("start", tests[0].get("trace", ""))
        self.assertNotIn("done", tests[0].get("trace", ""))

    def test_runner_mock_mode(self):
        """Test that the runner successfully simulates tests without executing them when --mock is used."""
        self._create_test("10-syntax-error.d", "!!!this is not bash!!!")

        mock_env = os.environ.copy()
        mock_env["VIRT_VALIDATE_MOCK"] = "1"
        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf", "-t", "10"],
            cwd=self.workspace,
            env=mock_env,
            capture_output=True,
            text=True
        )

        output = json.loads(res.stdout)
        tests = output["results"]["tests"]

        self.assertEqual(len(tests), 1)
        self.assertIn(tests[0]["status"], ("passed", "failed"))

    def test_ctrf_schema(self):
        """Test that -o ctrf produces a fully valid CTRF JSON report with all required fields."""
        self._create_test("10-pass.d", "#!/bin/bash\npass_with INFO 'All good'\nexit 0")
        self._create_test("20-fail.d", "#!/bin/bash\nfail_with 'Something broke'")

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf"],
            cwd=self.workspace,
            capture_output=True,
            text=True
        )

        self.assertEqual(res.returncode, 1)
        output = json.loads(res.stdout)

        self.assertIn("results", output)
        self.assertEqual(output["reportFormat"], "CTRF")
        self.assertEqual(output["specVersion"], "0.0.0")
        results = output["results"]
        self.assertIn("tool", results)
        self.assertIn("summary", results)
        self.assertIn("tests", results)

        self.assertEqual(results["tool"]["name"], "virt-cluster-validate")

        summary = results["summary"]
        self.assertEqual(summary["tests"], 2)
        self.assertEqual(summary["passed"], 1)
        self.assertEqual(summary["failed"], 1)
        self.assertEqual(summary["pending"], 0)
        self.assertEqual(summary["skipped"], 0)
        self.assertEqual(summary["other"], 0)
        self.assertIsInstance(summary["start"], int)
        self.assertIsInstance(summary["stop"], int)
        self.assertGreaterEqual(summary["stop"], summary["start"])

        tests = results["tests"]
        self.assertEqual(len(tests), 2)

        passed_test = next(t for t in tests if "10-pass" in t["name"])
        self.assertEqual(passed_test["status"], "passed")
        self.assertIsInstance(passed_test["duration"], int)
        self.assertIn("message", passed_test)

        failed_test = next(t for t in tests if "20-fail" in t["name"])
        self.assertEqual(failed_test["status"], "failed")
        self.assertIn("trace", failed_test)

    def test_report_file_publishes_pending_then_final_ctrf_snapshots(self):
        """Test that --report-file is atomic CTRF and exposes pending checks while running."""
        self._create_test("10-slow-pass.d", "#!/bin/bash\nsleep 1\nexit 0")
        self._create_test("20-slow-pass.d", "#!/bin/bash\nsleep 3\nexit 0")
        report_file = self.workspace / "report.json"

        process = subprocess.Popen(
            [sys.executable, str(RUNNER_SCRIPT), "--report-file", str(report_file), "-c", "1"],
            cwd=self.workspace, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        )
        try:
            deadline = time.time() + 5
            pending_report = None
            partial_report = None
            while time.time() < deadline:
                if report_file.exists():
                    report = json.loads(report_file.read_text())
                    summary = report["results"]["summary"]
                    if summary["pending"] == 2:
                        pending_report = report
                    if summary["passed"] == 1 and summary["pending"] == 1:
                        partial_report = report
                        break
                time.sleep(0.05)
            self.assertIsNotNone(pending_report)
            self.assertEqual(pending_report["reportFormat"], "CTRF")
            self.assertEqual(pending_report["specVersion"], "0.0.0")
            self.assertIn("stop", pending_report["results"]["summary"])
            self.assertEqual(pending_report["results"]["summary"]["tests"], 2)
            self.assertEqual(pending_report["results"]["summary"]["pending"], 2)
            self.assertIsNotNone(partial_report)
        finally:
            stdout, stderr = process.communicate(timeout=10)

        self.assertEqual(process.returncode, 0, stderr + stdout)
        final_report = json.loads(report_file.read_text())
        self.assertEqual(final_report["results"]["summary"]["pending"], 0)
        self.assertEqual(final_report["results"]["summary"]["passed"], 2)
        self.assertIn("stop", final_report["results"]["summary"])

    def test_include_filter(self):
        """Test that --include filters tests by substring match."""
        self._create_test("10-nodes.d", "#!/bin/bash\nexit 0")
        self._create_test("20-network.d", "#!/bin/bash\nexit 0")
        self._create_test("30-basic.d", "#!/bin/bash\nexit 0")

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf", "--include", "nodes,basic"],
            cwd=self.workspace,
            capture_output=True,
            text=True
        )

        self.assertEqual(res.returncode, 0)
        output = json.loads(res.stdout)

        names = [t["name"] for t in output["results"]["tests"]]
        self.assertEqual(len(names), 2)
        self.assertTrue(any("nodes" in n for n in names))
        self.assertTrue(any("basic" in n for n in names))
        self.assertFalse(any("network" in n for n in names))

    def test_exclude_filter(self):
        """Test that --exclude skips tests by substring match."""
        self._create_test("10-nodes.d", "#!/bin/bash\nexit 0")
        self._create_test("20-network.d", "#!/bin/bash\nexit 0")
        self._create_test("30-basic.d", "#!/bin/bash\nexit 0")

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf", "--exclude", "network"],
            cwd=self.workspace,
            capture_output=True,
            text=True
        )

        self.assertEqual(res.returncode, 0)
        output = json.loads(res.stdout)

        names = [t["name"] for t in output["results"]["tests"]]
        self.assertEqual(len(names), 2)
        self.assertTrue(any("nodes" in n for n in names))
        self.assertTrue(any("basic" in n for n in names))
        self.assertFalse(any("network" in n for n in names))

    def test_log_dir(self):
        """Test that --log-dir writes per-check log files."""
        self._create_test("10-pass.d", "#!/bin/bash\necho 'hello from test'\nexit 0")

        log_dir = self.workspace / "test-logs"

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf", "--log-dir", str(log_dir)],
            cwd=self.workspace,
            capture_output=True,
            text=True
        )

        self.assertEqual(res.returncode, 0)
        self.assertTrue(log_dir.exists())

        log_files = list(log_dir.iterdir())
        self.assertEqual(len(log_files), 1)
        log_content = log_files[0].read_text()
        self.assertIn("hello from test", log_content)

    def test_url_and_token_writes_kubeconfig(self):
        """Test that --url and --token write KUBECONFIG for checks."""
        self._create_prerequisite("#!/bin/bash\nexit 0")
        self._create_test(
            "10-kubeconfig.d",
            "#!/bin/bash\n"
            "python3 -c \"import json,os; c=json.load(open(os.environ['KUBECONFIG'])); "
            "assert c['clusters'][0]['cluster']['server']=='https://api.cluster.example.com:6443'; "
            "assert c['users'][0]['user']['token']=='test-token'; "
            "assert c['clusters'][0]['cluster'].get('insecure-skip-tls-verify') is True\"\n"
        )

        res = subprocess.run(
            [
                sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf",
                "--url", "https://api.cluster.example.com:6443",
                "--token", "test-token",
                "--insecure-skip-tls",
            ],
            cwd=self.workspace,
            capture_output=True,
            text=True,
        )

        self.assertEqual(res.returncode, 0, res.stderr + res.stdout)
        output = json.loads(res.stdout)
        self.assertEqual(output["results"]["summary"]["passed"], 1)

    def test_ca_file_writes_kubeconfig_ca_data(self):
        """Test that --ca-file embeds the CA certificate in the generated kubeconfig."""
        self._create_prerequisite("#!/bin/bash\nexit 0")
        ca_file = self.workspace / "ca.crt"
        ca_file.write_text("test CA certificate\n")
        self._create_test(
            "10-kubeconfig.d",
            "#!/bin/bash\n"
            "python3 -c \"import base64,json,os; c=json.load(open(os.environ['KUBECONFIG'])); "
            "assert base64.b64decode(c['clusters'][0]['cluster']['certificate-authority-data']) == b'test CA certificate\\\\n'; "
            "assert 'insecure-skip-tls-verify' not in c['clusters'][0]['cluster']\"\n"
        )

        res = subprocess.run(
            [
                sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf",
                "--url", "https://api.cluster.example.com:6443",
                "--token", "test-token", "--ca-file", str(ca_file),
            ], cwd=self.workspace, capture_output=True, text=True,
        )

        self.assertEqual(res.returncode, 0, res.stderr + res.stdout)
        self.assertEqual(json.loads(res.stdout)["results"]["summary"]["passed"], 1)

    def test_url_without_token_errors(self):
        """Test that --url without --token is rejected."""
        self._create_test("10-pass.d", "#!/bin/bash\nexit 0")

        run_env = os.environ.copy()
        run_env.pop("VIRT_VALIDATE_URL", None)
        run_env.pop("VIRT_VALIDATE_TOKEN", None)

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "--url", "https://api.cluster.example.com:6443"],
            cwd=self.workspace,
            env=run_env,
            capture_output=True,
            text=True,
        )

        self.assertEqual(res.returncode, 2)
        self.assertIn("--url and --token must be provided together", res.stderr)

    def test_token_without_url_errors(self):
        """Test that --token without --url is rejected."""
        self._create_test("10-pass.d", "#!/bin/bash\nexit 0")

        run_env = os.environ.copy()
        run_env.pop("VIRT_VALIDATE_URL", None)
        run_env.pop("VIRT_VALIDATE_TOKEN", None)

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "--token", "test-token"],
            cwd=self.workspace,
            env=run_env,
            capture_output=True,
            text=True,
        )

        self.assertEqual(res.returncode, 2)
        self.assertIn("--url and --token must be provided together", res.stderr)

    def test_provider_auth_from_env(self):
        """Test that VIRT_VALIDATE_URL and VIRT_VALIDATE_TOKEN set KUBECONFIG."""
        self._create_prerequisite("#!/bin/bash\nexit 0")
        self._create_test(
            "10-kubeconfig.d",
            "#!/bin/bash\n"
            "python3 -c \"import json,os; c=json.load(open(os.environ['KUBECONFIG'])); "
            "assert c['clusters'][0]['cluster']['server']=='https://api.env.example.com:6443'; "
            "assert c['users'][0]['user']['token']=='env-token'; "
            "assert 'VIRT_VALIDATE_TOKEN' not in os.environ\"\n"
        )

        run_env = os.environ.copy()
        run_env["VIRT_VALIDATE_URL"] = "https://api.env.example.com:6443"
        run_env["VIRT_VALIDATE_TOKEN"] = "env-token"

        res = subprocess.run(
            [sys.executable, str(RUNNER_SCRIPT), "-o", "ctrf"],
            cwd=self.workspace,
            env=run_env,
            capture_output=True,
            text=True,
        )

        self.assertEqual(res.returncode, 0, res.stderr + res.stdout)
        output = json.loads(res.stdout)
        self.assertEqual(output["results"]["summary"]["passed"], 1)

if __name__ == "__main__":
    unittest.main()
