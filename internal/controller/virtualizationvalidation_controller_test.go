// Copyright (C) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	validationv1alpha1 "github.com/openshift-cnv/virt-cluster-validate/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestTrustedProfile(t *testing.T) {
	workload := validationv1alpha1.ValidationWorkload{Namespace: "validation", DataSource: validationv1alpha1.WorkloadDataSourceReference{Namespace: "images", Name: "rhel10"}}
	profile, err := trustedProfile("basic-v1", workload)
	if err != nil || profile != "basic-v1" {
		t.Fatalf("unexpected profile: %q, %v", profile, err)
	}
	if _, err := trustedProfile("unknown-v1", workload); err == nil {
		t.Fatal("expected unsupported profile to fail")
	}
	if profile, err := trustedProfile(basicAllNodesProfile, workload); err != nil || profile != basicAllNodesProfile {
		t.Fatalf("unexpected all-nodes profile: %q, %v", profile, err)
	}
	if _, err := trustedProfile("basic-v1", validationv1alpha1.ValidationWorkload{}); err == nil {
		t.Fatal("expected missing workload inputs to fail")
	}
}

func TestValidReport(t *testing.T) {
	report := ctrfReport{ReportFormat: "CTRF", SpecVersion: "0.0.0"}
	report.Results.Summary.Tests = 2
	report.Results.Summary.Passed = 1
	report.Results.Summary.Pending = 1
	report.Results.Tests = []ctrfTest{{Name: "one", Status: "passed"}, {Name: "two", Status: "pending"}}
	if !validReport(report) {
		t.Fatal("expected valid progressive CTRF report")
	}
	report.Results.Tests[1].Status = "unknown"
	if validReport(report) {
		t.Fatal("expected invalid test status to fail")
	}
}

func TestValidReportAcceptsConsistentReduction(t *testing.T) {
	report := ctrfReport{ReportFormat: "CTRF", SpecVersion: "0.0.0"}
	report.Results.Summary.Tests = 2
	report.Results.Summary.Passed = 2
	report.Results.Extra = map[string]json.RawMessage{
		"validation.kubevirt.io/reduction": json.RawMessage(`{"level":4,"totalExecutions":2,"pendingCollapsed":0,"passedCollapsed":2,"requiredFailuresOmitted":0,"advisoryFailuresOmitted":0}`),
	}
	if !validReport(report) {
		t.Fatal("expected reduced report to be valid")
	}
	report.Results.Extra["validation.kubevirt.io/reduction"] = json.RawMessage(`{"level":4,"totalExecutions":1,"pendingCollapsed":0,"passedCollapsed":2,"requiredFailuresOmitted":0,"advisoryFailuresOmitted":0}`)
	if validReport(report) {
		t.Fatal("expected inconsistent reduced report to be invalid")
	}
}

func TestObserveReportProjectsCTRFExecutions(t *testing.T) {
	scheme := controllerScheme(t)
	validation := validation("target", "one")
	report := ctrfReport{ReportFormat: "CTRF", SpecVersion: "0.0.0", ReportID: "report-1", RunID: "run-1", GeneratedBy: "virt-cluster-validate"}
	report.Results.Summary.Tests = 2
	report.Results.Summary.Passed = 2
	report.Results.Tests = []ctrfTest{
		{TestID: "basic-v1/canary-vm", ExecutionID: "node/worker-a", Name: "canary VM", Status: "passed", Parameters: map[string]any{"node": "worker-a"}},
		{TestID: "basic-v1/canary-vm", ExecutionID: "node/worker-b", Name: "canary VM", Status: "passed", Parameters: map[string]any{"node": "worker-b", "replicas": float64(1)}, Retries: 1, Flaky: true, Steps: []ctrfStep{{Name: "Start VM", Status: "passed"}, {Name: "Snapshot", Status: "passed", Extra: map[string]any{"message": "complete"}}}},
	}
	reportData, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	result := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "result", Namespace: validation.Namespace, ResourceVersion: "1"}, Data: map[string]string{reportKey: string(reportData)}}
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(validation).WithObjects(validation).Build()
	reconciler := &VirtualizationValidationReconciler{Client: client}
	changed, err := reconciler.observeReport(context.Background(), validation, result)
	if err != nil || !changed {
		t.Fatalf("changed=%t err=%v", changed, err)
	}
	if validation.Status.Report == nil || validation.Status.Report.Digest == "" {
		t.Fatalf("unexpected report status: %#v", validation.Status.Report)
	}
	if len(validation.Status.Executions) != 2 || validation.Status.Executions[1].ExecutionID != "node/worker-b" || validation.Status.Executions[1].Parameters["node"] != "worker-b" || !validation.Status.Executions[1].ParametersTruncated || validation.Status.Executions[1].RetryCount != 1 || !validation.Status.Executions[1].Flaky || len(validation.Status.Executions[1].Steps) != 2 || validation.Status.Executions[1].Steps[1].Extra["message"] != "complete" {
		t.Fatalf("unexpected execution projection: %#v", validation.Status.Executions)
	}
	if validation.Status.Executions[0].CheckID != "canary/workflow" || validation.Status.Executions[0].Subject == nil || validation.Status.Executions[0].Subject.Name != "worker-a" || validation.Status.Verdict != validationv1alpha1.ValidationVerdictValid {
		t.Fatalf("unexpected public execution identity or verdict: %#v", validation.Status)
	}
}

func TestAggregateVerdict(t *testing.T) {
	requiredFailure := validationv1alpha1.VirtualizationValidationExecution{Severity: validationv1alpha1.ValidationSeverityRequired, Outcome: validationv1alpha1.ValidationOutcomeFailed}
	failed := validationv1alpha1.VirtualizationValidationSummary{Total: 1, Failed: 1}
	if verdict := aggregateVerdict(failed, []validationv1alpha1.VirtualizationValidationExecution{requiredFailure}, validationv1alpha1.ValidationReportReduction{}, true); verdict != validationv1alpha1.ValidationVerdictInvalid {
		t.Fatalf("required failure verdict = %q, want Invalid", verdict)
	}
	requiredTimeout := requiredFailure
	requiredTimeout.Outcome = validationv1alpha1.ValidationOutcomeTimedOut
	if verdict := aggregateVerdict(failed, []validationv1alpha1.VirtualizationValidationExecution{requiredTimeout}, validationv1alpha1.ValidationReportReduction{}, true); verdict != validationv1alpha1.ValidationVerdictInconclusive {
		t.Fatalf("timeout verdict = %q, want Inconclusive", verdict)
	}
	advisoryFailure := requiredFailure
	advisoryFailure.Severity = validationv1alpha1.ValidationSeverityAdvisory
	if verdict := aggregateVerdict(failed, []validationv1alpha1.VirtualizationValidationExecution{advisoryFailure}, validationv1alpha1.ValidationReportReduction{}, true); verdict != validationv1alpha1.ValidationVerdictValidWithWarnings {
		t.Fatalf("advisory failure verdict = %q, want ValidWithWarnings", verdict)
	}
	advisoryWarning := validationv1alpha1.VirtualizationValidationExecution{Severity: validationv1alpha1.ValidationSeverityAdvisory, Outcome: validationv1alpha1.ValidationOutcomePassed, Reason: "AdvisoryWarning"}
	passed := validationv1alpha1.VirtualizationValidationSummary{Total: 1, Passed: 1}
	if verdict := aggregateVerdict(passed, []validationv1alpha1.VirtualizationValidationExecution{advisoryWarning}, validationv1alpha1.ValidationReportReduction{}, true); verdict != validationv1alpha1.ValidationVerdictValidWithWarnings {
		t.Fatalf("advisory warning verdict = %q, want ValidWithWarnings", verdict)
	}
	if verdict := aggregateVerdict(failed, nil, validationv1alpha1.ValidationReportReduction{RequiredFailuresOmitted: 1}, true); verdict != validationv1alpha1.ValidationVerdictInvalid {
		t.Fatalf("omitted required failure verdict = %q, want Invalid", verdict)
	}
}

func TestStableCheckIDsMatchRunnerOutput(t *testing.T) {
	runnerTestIDs := map[string]string{
		"checks.d/10-openshift.d/00-login.d/test.sh":                       "openshift/login",
		"checks.d/50-openshift-virtualization.d/00-installation.d/test.sh": "virtualization/installation",
		"checks.d/50-openshift-virtualization.d/10-quota.d/test.sh":        "virtualization/quota",
	}
	for sourcePath, expectedCheckID := range runnerTestIDs {
		testID := strings.TrimPrefix(sourcePath, "checks.d/")
		checkID := stableCheckID(testID)
		if checkID != expectedCheckID {
			t.Fatalf("runner test ID %q resolved to %q, want %q", testID, checkID, expectedCheckID)
		}
		if _, found := checkSeverities[checkID]; !found {
			t.Fatalf("runner test ID %q resolved to check ID %q without an explicit severity", testID, checkID)
		}
	}
	for testID, expectedCheckID := range map[string]string{
		"basic-v1/canary-workflow":           "canary/workflow",
		"basic-all-nodes-v1/canary-workflow": "canary/workflow",
		"basic-v1/canary-vm":                 "canary/workflow",
	} {
		checkID := stableCheckID(testID)
		if checkID != expectedCheckID {
			t.Fatalf("runner test ID %q resolved to %q, want %q", testID, checkID, expectedCheckID)
		}
		if _, found := checkSeverities[checkID]; !found {
			t.Fatalf("runner test ID %q resolved to check ID %q without an explicit severity", testID, checkID)
		}
	}
}

func TestCTRFStepReasonPreservesFailureContext(t *testing.T) {
	step := ctrfStep{Name: "Snapshot", Status: "failed", Extra: map[string]any{"state": "timedOut"}}
	if reason := ctrfStepReason(step); reason != "SnapshotTimedOut" {
		t.Fatalf("snapshot timeout reason = %q, want SnapshotTimedOut", reason)
	}
}

func TestCTRFTestOutcomeHonorsRunnerOutcomeHint(t *testing.T) {
	test := ctrfTest{Status: "failed", Extra: map[string]any{"validation.kubevirt.io/outcome": "TimedOut"}}
	if outcome := ctrfTestOutcome(test, nil); outcome != validationv1alpha1.ValidationOutcomeTimedOut {
		t.Fatalf("stepless runner timeout outcome = %q, want TimedOut", outcome)
	}
}

func TestObserveReportBoundsSchemaStrings(t *testing.T) {
	scheme := controllerScheme(t)
	validation := validation("target", "one")
	report := ctrfReport{ReportFormat: "CTRF", SpecVersion: "0.0.0"}
	report.Results.Summary.Tests = 1
	report.Results.Summary.Passed = 1
	report.Results.Tests = []ctrfTest{{
		TestID: "basic-v1/canary-workflow", ExecutionID: "node/" + strings.Repeat("n", 200), Name: strings.Repeat("x", 300), Status: "passed",
		Parameters: map[string]any{strings.Repeat("k", 200): strings.Repeat("v", 300)},
		Steps:      []ctrfStep{{Name: strings.Repeat("s", 300), Status: "passed"}},
	}}
	reportData, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	result := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "result", Namespace: validation.Namespace, ResourceVersion: "1"}, Data: map[string]string{reportKey: string(reportData)}}
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(validation).WithObjects(validation).Build()
	if _, err := (&VirtualizationValidationReconciler{Client: client}).observeReport(context.Background(), validation, result); err != nil {
		t.Fatal(err)
	}
	execution := validation.Status.Executions[0]
	if len(execution.ExecutionID) > maxExecutionID || len(execution.Name) > maxExecutionName || len(execution.Steps[0].Name) > maxStepName || !execution.ParametersTruncated {
		t.Fatalf("schema-bounded strings were not projected safely: %#v", execution)
	}
}

func TestObserveReportProjectsStructuredAdvisoryWarning(t *testing.T) {
	scheme := controllerScheme(t)
	validation := validation("target", "one")
	report := ctrfReport{ReportFormat: "CTRF", SpecVersion: "0.0.0"}
	report.Results.Summary.Tests = 1
	report.Results.Summary.Passed = 1
	report.Results.Tests = []ctrfTest{{TestID: "50-openshift-virtualization.d/10-quota.d/test.sh", Name: "quota", Status: "passed", Message: "unstructured text", Parameters: map[string]any{"validation.kubevirt.io/advisory-warning": "true"}}}
	reportData, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	result := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "result", Namespace: validation.Namespace, ResourceVersion: "1"}, Data: map[string]string{reportKey: string(reportData)}}
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(validation).WithObjects(validation).Build()
	if _, err := (&VirtualizationValidationReconciler{Client: client}).observeReport(context.Background(), validation, result); err != nil {
		t.Fatal(err)
	}
	if validation.Status.Executions[0].Reason != "AdvisoryWarning" || validation.Status.Verdict != validationv1alpha1.ValidationVerdictValidWithWarnings {
		t.Fatalf("structured advisory warning was not projected: %#v", validation.Status)
	}
}

func TestFitStatusProjectionBoundsTotalSize(t *testing.T) {
	status := validationv1alpha1.VirtualizationValidationStatus{Summary: validationv1alpha1.VirtualizationValidationSummary{Total: maxResults, Passed: maxResults}}
	for range maxResults {
		steps := make([]validationv1alpha1.ValidationStep, maxSteps)
		for index := range steps {
			steps[index] = validationv1alpha1.ValidationStep{Name: "step", Outcome: validationv1alpha1.ValidationOutcomePassed, Extra: map[string]string{"detail": strings.Repeat("x", maxParameterValue)}}
		}
		status.Executions = append(status.Executions, validationv1alpha1.VirtualizationValidationExecution{Name: "passing", CheckID: "test/passing", Outcome: validationv1alpha1.ValidationOutcomePassed, Severity: validationv1alpha1.ValidationSeverityRequired, Message: strings.Repeat("x", maxMessage), Steps: steps})
	}
	reduceStatusProjection(&status)
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > maxStatusBytes || status.Reduction.PassedCollapsed != maxResults-1 || len(status.Executions) != 1 {
		t.Fatalf("status projection was not reduced: bytes=%d executions=%d reduction=%#v", len(data), len(status.Executions), status.Reduction)
	}
}

func TestStatusReductionAccountsForEveryOutcome(t *testing.T) {
	for _, outcome := range []validationv1alpha1.ValidationOutcome{
		validationv1alpha1.ValidationOutcomePending,
		validationv1alpha1.ValidationOutcomeRunning,
		validationv1alpha1.ValidationOutcomePassed,
		validationv1alpha1.ValidationOutcomeFailed,
		validationv1alpha1.ValidationOutcomeTimedOut,
		validationv1alpha1.ValidationOutcomeError,
		validationv1alpha1.ValidationOutcomeSkipped,
		validationv1alpha1.ValidationOutcomeCancelled,
	} {
		status := validationv1alpha1.VirtualizationValidationStatus{Summary: validationv1alpha1.VirtualizationValidationSummary{Total: 200}}
		switch outcome {
		case validationv1alpha1.ValidationOutcomePassed:
			status.Summary.Passed = 200
		case validationv1alpha1.ValidationOutcomePending, validationv1alpha1.ValidationOutcomeRunning:
			status.Summary.Pending = 200
		case validationv1alpha1.ValidationOutcomeSkipped:
			status.Summary.Skipped = 200
		case validationv1alpha1.ValidationOutcomeError:
			status.Summary.Other = 200
		default:
			status.Summary.Failed = 200
		}
		for range 200 {
			status.Executions = append(status.Executions, validationv1alpha1.VirtualizationValidationExecution{CheckID: "check", Reason: string(outcome), Outcome: outcome, Severity: validationv1alpha1.ValidationSeverityRequired})
		}
		reduceStatusProjection(&status)
		omitted := status.Reduction.PendingCollapsed + status.Reduction.PassedCollapsed + status.Reduction.RequiredFailuresOmitted + status.Reduction.AdvisoryFailuresOmitted + status.Reduction.RequiredInconclusiveOmitted + status.Reduction.AdvisoryInconclusiveOmitted
		if status.Reduction.TotalExecutions != 200 || (outcome != validationv1alpha1.ValidationOutcomePending && outcome != validationv1alpha1.ValidationOutcomeRunning && len(status.Executions) == 0) || int32(len(status.Executions))+omitted != 200 {
			t.Fatalf("outcome %q was not reduced and accounted for: executions=%d reduction=%#v", outcome, len(status.Executions), status.Reduction)
		}
	}
}

func TestSeverityPolicyFailsClosed(t *testing.T) {
	for sourceID, checkID := range stableCheckIDs {
		if _, found := checkSeverities[checkID]; !found {
			t.Fatalf("stable check %q from %q has no explicit severity", checkID, sourceID)
		}
	}
	if severityForCheck("canary/workflow") != validationv1alpha1.ValidationSeverityRequired || severityForCheck("virtualization/quota") != validationv1alpha1.ValidationSeverityAdvisory || severityForCheck("unknown/check") != validationv1alpha1.ValidationSeverityRequired {
		t.Fatal("unexpected check severity policy")
	}
}

func TestInputHashIgnoresCredentialContent(t *testing.T) {
	validation := &validationv1alpha1.VirtualizationValidation{Spec: validationv1alpha1.VirtualizationValidationSpec{
		Target:   validationv1alpha1.ValidationTarget{URL: "https://API.example.com:6443/"},
		Workload: validationv1alpha1.ValidationWorkload{Namespace: "validation", DataSource: validationv1alpha1.WorkloadDataSourceReference{Namespace: "images", Name: "rhel10"}},
		Run:      validationv1alpha1.ValidationRun{Profile: "basic-v1"},
	}}
	first := inputHash(validation, "basic")
	validation.Spec.Run.ID = "retry-1"
	if first == inputHash(validation, "basic") {
		t.Fatal("run.id must change the run hash")
	}
}

func TestInputHashChangesWithTimeouts(t *testing.T) {
	validation := &validationv1alpha1.VirtualizationValidation{Spec: validationv1alpha1.VirtualizationValidationSpec{
		Target:   validationv1alpha1.ValidationTarget{URL: "https://api.example.com:6443"},
		Workload: validationv1alpha1.ValidationWorkload{Namespace: "validation", DataSource: validationv1alpha1.WorkloadDataSourceReference{Namespace: "images", Name: "rhel10"}},
		Run:      validationv1alpha1.ValidationRun{Profile: "basic-v1"},
	}}
	defaultTimeouts := inputHash(validation, "basic")
	validation.Spec.Run.SuiteTimeoutSeconds = 900
	validation.Spec.Run.ExecutionTimeoutSeconds = 180
	if defaultTimeouts != inputHash(validation, "basic") {
		t.Fatal("the default timeouts and their explicit values must identify the same run")
	}
	validation.Spec.Run.ExecutionTimeoutSeconds = 181
	if defaultTimeouts == inputHash(validation, "basic") {
		t.Fatal("execution timeout must change the run hash")
	}
	validation.Spec.Run.ExecutionTimeoutSeconds = 180
	validation.Spec.Run.SuiteTimeoutSeconds = 901
	if defaultTimeouts == inputHash(validation, "basic") {
		t.Fatal("suite timeout must change the run hash")
	}
}

func TestInputHashChangesWithMaxConcurrentExecutions(t *testing.T) {
	validation := validation("target", "one")
	first := inputHash(validation, "basic")
	validation.Spec.Run.MaxConcurrentExecutions = 2
	if first == inputHash(validation, "basic") {
		t.Fatal("maxConcurrentExecutions must change the run hash")
	}
}

func TestValidSpec(t *testing.T) {
	validation := &validationv1alpha1.VirtualizationValidation{Spec: validationv1alpha1.VirtualizationValidationSpec{
		Target:   validationv1alpha1.ValidationTarget{URL: "https://api.example.com:6443", CredentialsSecret: validationv1alpha1.CredentialSecretReference{Name: "credentials"}},
		Workload: validationv1alpha1.ValidationWorkload{Namespace: "validation", DataSource: validationv1alpha1.WorkloadDataSourceReference{Namespace: "images", Name: "rhel10"}},
		Run:      validationv1alpha1.ValidationRun{Profile: "basic-v1"},
	}}
	if err := validSpec(validation); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	validation.Spec.Target.URL = "http://api.example.com"
	if err := validSpec(validation); err == nil {
		t.Fatal("non-HTTPS URL accepted")
	}
	validation.Spec.Target.URL = "https://api.example.com"
	validation.Spec.Run.SuiteTimeoutSeconds = 300
	validation.Spec.Run.ExecutionTimeoutSeconds = 301
	if err := validSpec(validation); err == nil {
		t.Fatal("execution timeout exceeding suite timeout accepted")
	}
	validation.Spec.Run.SuiteTimeoutSeconds = 0
	validation.Spec.Run.ExecutionTimeoutSeconds = 0
	validation.Spec.Run.CleanupPolicy = "Sometimes"
	if err := validSpec(validation); err == nil {
		t.Fatal("invalid cleanup policy accepted")
	}
	validation.Spec.Run.CleanupPolicy = ""
	validation.Spec.Run.MaxConcurrentExecutions = 65
	if err := validSpec(validation); err == nil {
		t.Fatal("out-of-range maxConcurrentExecutions accepted")
	}
}

func TestJobUsesProfileAndCredentialKeys(t *testing.T) {
	validation := validation("target", "one")
	validation.Spec.Target.CredentialsSecret = validationv1alpha1.CredentialSecretReference{Name: "credentials", TokenKey: "access-token", CAKey: "target-ca"}
	job := (&VirtualizationValidationReconciler{ValidatorImage: "validator"}).job(validation, "job", "result", "basic-v1")
	container := job.Spec.Template.Spec.Containers[0]
	if value := environmentValue(container.Env, "VIRT_VALIDATE_PROFILE"); value != "basic-v1" {
		t.Fatalf("unexpected profile: %q", value)
	}
	if value := environmentValue(container.Env, "VIRT_VALIDATE_DATA_SOURCE"); value != "images/rhel10" {
		t.Fatalf("unexpected data source: %q", value)
	}
	validation.Spec.Run.SuiteTimeoutSeconds = 1200
	validation.Spec.Run.ExecutionTimeoutSeconds = 300
	validation.Spec.Run.CleanupPolicy = "OnFailure"
	job = (&VirtualizationValidationReconciler{ValidatorImage: "validator"}).job(validation, "job", "result", "basic-v1")
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != 1200 {
		t.Fatalf("unexpected suite deadline: %v", job.Spec.ActiveDeadlineSeconds)
	}
	if value := environmentValue(job.Spec.Template.Spec.Containers[0].Env, "VIRT_VALIDATE_EXECUTION_TIMEOUT_SECONDS"); value != "300" {
		t.Fatalf("unexpected execution timeout: %q", value)
	}
	if value := environmentValue(job.Spec.Template.Spec.Containers[0].Env, "VIRT_VALIDATE_CLEANUP_POLICY"); value != "OnFailure" {
		t.Fatalf("unexpected cleanup policy: %q", value)
	}
	validation.Spec.Run.MaxConcurrentExecutions = 2
	job = (&VirtualizationValidationReconciler{ValidatorImage: "validator"}).job(validation, "job", "result", "basic-v1")
	if value := environmentValue(job.Spec.Template.Spec.Containers[0].Env, "VIRT_VALIDATE_CONCURRENCY"); value != "2" {
		t.Fatalf("unexpected concurrency: %q", value)
	}
	validation.Spec.Run.Profile = basicAllNodesProfile
	job = (&VirtualizationValidationReconciler{ValidatorImage: "validator"}).job(validation, "job", "result", basicAllNodesProfile)
	if value := environmentValue(job.Spec.Template.Spec.Containers[0].Env, "VIRT_VALIDATE_PROFILE"); value != basicAllNodesProfile {
		t.Fatalf("unexpected all-nodes profile: %q", value)
	}
	if key := environmentSecretKey(container.Env, "VIRT_VALIDATE_TOKEN"); key != "access-token" {
		t.Fatalf("unexpected token key: %q", key)
	}
	if job.Spec.Template.Spec.Volumes[2].Secret.Items[0].Key != "target-ca" {
		t.Fatal("custom CA key was not mounted")
	}
	if !reflect.DeepEqual(job.Labels, job.Spec.Template.Labels) {
		t.Fatalf("validator Pod labels do not match Job labels: job=%#v pod=%#v", job.Labels, job.Spec.Template.Labels)
	}
}

func TestJobDoesNotMountCAWhenTLSIsInsecure(t *testing.T) {
	validation := validation("target", "one")
	validation.Spec.Target.InsecureSkipTLS = true
	job := (&VirtualizationValidationReconciler{ValidatorImage: "validator"}).job(validation, "job", "result", "basic")
	for _, volume := range job.Spec.Template.Spec.Volumes {
		if volume.Name == "credentials" {
			t.Fatal("insecure target unexpectedly mounted a CA Secret")
		}
	}
}

func TestJobUsesSystemTrustWhenCAKeyIsEmpty(t *testing.T) {
	validation := validation("target", "one")
	validation.Spec.Target.CredentialsSecret = validationv1alpha1.CredentialSecretReference{Name: "credentials", TokenKey: tokenKey, CAKey: ""}
	job := (&VirtualizationValidationReconciler{ValidatorImage: "validator"}).job(validation, "job", "result", "basic")
	if environmentValue(job.Spec.Template.Spec.Containers[0].Env, "VIRT_VALIDATE_CA_FILE") != "" {
		t.Fatal("system-trust target unexpectedly configured a CA file")
	}
	for _, volume := range job.Spec.Template.Spec.Volumes {
		if volume.Name == "credentials" {
			t.Fatal("system-trust target unexpectedly mounted a CA Secret")
		}
	}
}

func environmentValue(env []corev1.EnvVar, name string) string {
	for _, item := range env {
		if item.Name == name {
			return item.Value
		}
	}
	return ""
}

func environmentSecretKey(env []corev1.EnvVar, name string) string {
	for _, item := range env {
		if item.Name == name && item.ValueFrom != nil && item.ValueFrom.SecretKeyRef != nil {
			return item.ValueFrom.SecretKeyRef.Key
		}
	}
	return ""
}

func TestEnsureResourcesRejectsForeignObject(t *testing.T) {
	scheme := controllerScheme(t)
	validation := validation("target", "one")
	validation.Status.ObservedInputHash = "current-run"
	foreign := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: runName(validation, "vr"), Namespace: validation.Namespace, Labels: map[string]string{runLabel: "foreign-run"}}}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(foreign).Build()
	reconciler := &VirtualizationValidationReconciler{Client: fakeClient, Scheme: scheme}
	if err := reconciler.ensureResources(context.Background(), validation, runName(validation, "vv"), runName(validation, "vr")); err == nil {
		t.Fatal("expected a foreign result ConfigMap to be rejected")
	}
}

func TestEnsureResourcesCreatesOwnedResources(t *testing.T) {
	scheme := controllerScheme(t)
	validation := validation("target", "one")
	validation.Status.ObservedInputHash = "current-run"
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := &VirtualizationValidationReconciler{Client: fakeClient, Scheme: scheme}
	if err := reconciler.ensureResources(context.Background(), validation, runName(validation, "vv"), runName(validation, "vr")); err != nil {
		t.Fatal(err)
	}
	result := &corev1.ConfigMap{}
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: validation.Namespace, Name: runName(validation, "vr")}, result); err != nil {
		t.Fatal(err)
	}
	if !metav1.IsControlledBy(result, validation) || result.Labels[runLabel] != runHashLabel(validation.Status.ObservedInputHash) {
		t.Fatal("result ConfigMap is not owned and labelled by the validation run")
	}
	policy := &networkingv1.NetworkPolicy{}
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: validation.Namespace, Name: runName(validation, "vnp")}, policy); err != nil {
		t.Fatal(err)
	}
	if !metav1.IsControlledBy(policy, validation) || !reflect.DeepEqual(policy.Spec.PodSelector.MatchLabels, labelsFor(validation)) || len(policy.Spec.Egress) != 2 {
		t.Fatalf("unexpected validation NetworkPolicy: %#v", policy)
	}
}

func TestValidationNetworkPolicyAllowsDeclaredTargetPort(t *testing.T) {
	validation := validation("target", "one")
	validation.Spec.Target.URL = "https://api.example.com:8443"
	policy := (&VirtualizationValidationReconciler{}).validationNetworkPolicy(validation)
	if !hasNetworkPolicyPort(policy.Spec.Egress[1].Ports, 8443) {
		t.Fatalf("target API port is absent from egress policy: %#v", policy.Spec.Egress[1].Ports)
	}
	if !hasNetworkPolicyPort(policy.Spec.Egress[1].Ports, 443) || !hasNetworkPolicyPort(policy.Spec.Egress[1].Ports, 6443) {
		t.Fatalf("standard API and tool-download ports are absent from egress policy: %#v", policy.Spec.Egress[1].Ports)
	}
}

func TestValidationNetworkPolicyAllowsOpenShiftDNSServiceAndBackendPorts(t *testing.T) {
	validation := validation("target", "one")
	policy := (&VirtualizationValidationReconciler{}).validationNetworkPolicy(validation)
	dnsPorts := policy.Spec.Egress[0].Ports
	for _, protocol := range []corev1.Protocol{corev1.ProtocolUDP, corev1.ProtocolTCP} {
		for _, port := range []int{53, 5353} {
			if !hasNetworkPolicyPortForProtocol(dnsPorts, protocol, port) {
				t.Fatalf("DNS egress is missing %s/%d: %#v", protocol, port, dnsPorts)
			}
		}
	}
}

func TestValidSpecRejectsInvalidTargetPort(t *testing.T) {
	validation := validation("target", "one")
	validation.Spec.Target.URL = "https://api.example.com:65536"
	if err := validSpec(validation); err == nil {
		t.Fatal("out-of-range target port accepted")
	}
}

func hasNetworkPolicyPort(ports []networkingv1.NetworkPolicyPort, wanted int) bool {
	for _, port := range ports {
		if port.Port != nil && port.Port.IntValue() == wanted {
			return true
		}
	}
	return false
}

func hasNetworkPolicyPortForProtocol(ports []networkingv1.NetworkPolicyPort, protocol corev1.Protocol, wanted int) bool {
	for _, port := range ports {
		if port.Protocol != nil && *port.Protocol == protocol && port.Port != nil && port.Port.IntValue() == wanted {
			return true
		}
	}
	return false
}

func TestRunHashLabelTruncatesSHA256Values(t *testing.T) {
	hash := "4213da9aa43c3deef1de7c3302023b64035bb0890b63dad562a5a9cccb6db514"
	label := runHashLabel(hash)
	if len(label) != 63 {
		t.Fatalf("label has length %d, want 63", len(label))
	}
	if label != hash[:63] {
		t.Fatalf("unexpected label value: %q", label)
	}
}

func TestReconcileRequeuesAfterInitializingRunState(t *testing.T) {
	scheme := controllerScheme(t)
	validation := validation("target", "one")
	validation.Finalizers = []string{finalizer}
	validation.Spec.Target.CredentialsSecret.Name = "credentials"
	credentials := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: validation.Namespace, ResourceVersion: "1"},
		Data:       map[string][]byte{tokenKey: []byte("token"), caKey: []byte("ca")},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(validation).WithObjects(validation, credentials).Build()
	reconciler := &VirtualizationValidationReconciler{Client: fakeClient, Scheme: scheme, ValidatorImage: "validator"}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(validation)})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue {
		t.Fatal("initializing a run must explicitly requeue the validation")
	}
}

func TestCancelDeletesJobAndMarksValidationCancelled(t *testing.T) {
	scheme := controllerScheme(t)
	validation := validation("target", "one")
	validation.Finalizers = []string{finalizer}
	validation.Spec.Run.Cancel = true
	validation.Status.JobName = "running-job"
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: validation.Status.JobName, Namespace: validation.Namespace}}
	report := ctrfReport{ReportFormat: "CTRF", SpecVersion: "0.0.0"}
	report.Results.Summary.Tests = 2
	report.Results.Summary.Passed = 1
	report.Results.Summary.Pending = 1
	report.Results.Tests = []ctrfTest{
		{Name: "completed", Status: "passed"},
		{Name: "running", Status: "pending", Steps: []ctrfStep{{Name: "Snapshot", Status: "pending", Extra: map[string]any{"state": "running"}}}},
	}
	reportData, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	result := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: runName(validation, "vr"), Namespace: validation.Namespace}, Data: map[string]string{reportKey: string(reportData)}}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(validation).WithObjects(validation, job, result).Build()
	reconciler := &VirtualizationValidationReconciler{Client: fakeClient, Scheme: scheme}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(validation)}); err != nil {
		t.Fatal(err)
	}
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(job), &batchv1.Job{}); err == nil {
		t.Fatal("cancelled validation Job still exists")
	}
	updated := &validationv1alpha1.VirtualizationValidation{}
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(validation), updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Phase != "Cancelled" || updated.Status.CompletedAt == nil {
		t.Fatalf("unexpected cancellation status: %#v", updated.Status)
	}
	if updated.Status.Verdict != validationv1alpha1.ValidationVerdictInconclusive || updated.Status.Summary.Pending != 0 || updated.Status.Summary.Skipped != 1 || updated.Status.Executions[1].Outcome != validationv1alpha1.ValidationOutcomeCancelled || updated.Status.Executions[1].Steps[0].Outcome != validationv1alpha1.ValidationOutcomeCancelled || updated.Status.Executions[1].Steps[0].Extra["state"] != "cancelled" {
		t.Fatalf("cancellation did not finalize report: %#v", updated.Status)
	}
}

func TestAwaitFinalReportFailsAfterGracePeriod(t *testing.T) {
	scheme := controllerScheme(t)
	validation := validation("target", "one")
	validation.Status.ObservedInputHash = "current-run"
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(validation).WithObjects(validation).Build()
	reconciler := &VirtualizationValidationReconciler{Client: fakeClient}
	completed := metav1.NewTime(time.Now().Add(-reportGrace - time.Second))
	job := &batchv1.Job{Status: batchv1.JobStatus{CompletionTime: &completed}}
	result, err := reconciler.awaitFinalReport(context.Background(), validation, job)
	if err != nil || !result.IsZero() {
		t.Fatalf("unexpected result=%#v err=%v", result, err)
	}
	updated := &validationv1alpha1.VirtualizationValidation{}
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(validation), updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Phase != validationv1alpha1.ValidationPhaseError || updated.Status.Verdict != validationv1alpha1.ValidationVerdictInconclusive || updated.Status.Conditions[0].Reason != "IncompleteReport" {
		t.Fatalf("unexpected terminal status: %#v", updated.Status)
	}
}

func TestDeleteLeaseIfUnused(t *testing.T) {
	scheme := controllerScheme(t)
	validation := validation("target", "one")
	lease := &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: leaseName(validation), Namespace: validation.Namespace}}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(validation, lease).Build()
	reconciler := &VirtualizationValidationReconciler{Client: fakeClient}
	if err := reconciler.deleteLeaseIfUnused(context.Background(), validation); err != nil {
		t.Fatal(err)
	}
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(lease), &coordinationv1.Lease{}); !apierrors.IsNotFound(err) {
		t.Fatalf("unused lease was not removed: %v", err)
	}

	other := validation.DeepCopy()
	other.Name = "other"
	other.UID = "two"
	lease = &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: leaseName(validation), Namespace: validation.Namespace}}
	fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(validation, other, lease).Build()
	reconciler = &VirtualizationValidationReconciler{Client: fakeClient}
	if err := reconciler.deleteLeaseIfUnused(context.Background(), validation); err != nil {
		t.Fatal(err)
	}
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(lease), &coordinationv1.Lease{}); err != nil {
		t.Fatalf("shared lease was removed: %v", err)
	}
}

func controllerScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{validationv1alpha1.AddToScheme, corev1.AddToScheme, batchv1.AddToScheme, coordinationv1.AddToScheme, networkingv1.AddToScheme, rbacv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return scheme
}

func TestLeaseExclusivityAndExpiry(t *testing.T) {
	scheme := controllerScheme(t)
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := &VirtualizationValidationReconciler{Client: client}
	first := validation("first", "one")
	held, err := reconciler.acquireLease(context.Background(), first)
	if err != nil || !held {
		t.Fatalf("first holder: held=%t err=%v", held, err)
	}
	second := validation("second", "two")
	held, err = reconciler.acquireLease(context.Background(), second)
	if err != nil || held {
		t.Fatalf("second holder: held=%t err=%v", held, err)
	}
	lease := &coordinationv1.Lease{}
	key := types.NamespacedName{Namespace: first.Namespace, Name: leaseName(first)}
	if err := client.Get(context.Background(), key, lease); err != nil {
		t.Fatal(err)
	}
	expired := metav1.MicroTime{Time: time.Now().Add(-3 * time.Minute)}
	lease.Spec.RenewTime = &expired
	if err := client.Update(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	held, err = reconciler.acquireLease(context.Background(), second)
	if err != nil || !held {
		t.Fatalf("expired holder takeover: held=%t err=%v", held, err)
	}
}

func validation(name, uid string) *validationv1alpha1.VirtualizationValidation {
	return &validationv1alpha1.VirtualizationValidation{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "hub", UID: types.UID(uid)}, Spec: validationv1alpha1.VirtualizationValidationSpec{Target: validationv1alpha1.ValidationTarget{URL: "https://api.target.example.com:6443"}, Workload: validationv1alpha1.ValidationWorkload{Namespace: "validation", DataSource: validationv1alpha1.WorkloadDataSourceReference{Namespace: "images", Name: "rhel10"}}, Run: validationv1alpha1.ValidationRun{Profile: "basic-v1"}}}
}
