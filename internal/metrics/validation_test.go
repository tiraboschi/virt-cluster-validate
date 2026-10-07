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

package metrics

import (
	"testing"
	"time"

	validationv1alpha1 "github.com/openshift-cnv/virt-cluster-validate/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestValidationMetricsObserveTerminalAndFinalReport(t *testing.T) {
	now := metav1.Now()
	started := metav1.NewTime(now.Add(-30 * time.Second))
	completed := &validationv1alpha1.VirtualizationValidation{
		ObjectMeta: metav1.ObjectMeta{Name: "completed", Namespace: "test", CreationTimestamp: started},
		Spec:       validationv1alpha1.VirtualizationValidationSpec{Run: validationv1alpha1.ValidationRun{Profile: "basic-v1"}},
		Status: validationv1alpha1.VirtualizationValidationStatus{
			Phase:       validationv1alpha1.ValidationPhaseCompleted,
			Verdict:     validationv1alpha1.ValidationVerdictValid,
			StartedAt:   &started,
			CompletedAt: &now,
		},
	}
	running := &validationv1alpha1.VirtualizationValidation{
		ObjectMeta: metav1.ObjectMeta{Name: "running", Namespace: "test"},
		Spec:       validationv1alpha1.VirtualizationValidationSpec{Run: validationv1alpha1.ValidationRun{Profile: "basic-v1"}},
		Status:     validationv1alpha1.VirtualizationValidationStatus{Phase: validationv1alpha1.ValidationPhaseRunning},
	}
	scheme := runtime.NewScheme()
	if err := validationv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(completed, running).Build()
	registry := prometheus.NewRegistry()
	metrics, err := NewValidationMetrics(reader, registry)
	if err != nil {
		t.Fatal(err)
	}

	metrics.ObserveTerminal(completed, validationv1alpha1.ValidationPhaseRunning)
	metrics.ObserveTerminal(completed, validationv1alpha1.ValidationPhaseCompleted)
	metrics.ObserveQueue("basic-v1", "acquired", 12*time.Second)
	metrics.ObserveQueueTimeout("basic-v1")
	metrics.ObserveTerminalError("basic-v1", "InvalidCredentials")
	metrics.ObserveTerminalError("basic-v1", "untrusted reason")
	metrics.ObserveFinalReport("basic-v1", []validationv1alpha1.VirtualizationValidationExecution{
		{CheckID: "canary/workflow", Outcome: validationv1alpha1.ValidationOutcomePassed, Severity: validationv1alpha1.ValidationSeverityRequired},
		{CheckID: "unknown/123456", Outcome: validationv1alpha1.ValidationOutcomeFailed, Severity: validationv1alpha1.ValidationSeverityRequired},
	}, validationv1alpha1.ValidationReportReduction{Level: 2})

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if value := metricValue(families, "virt_validation_runs_total", map[string]string{"profile": "basic-v1", "verdict": "Valid"}); value != 1 {
		t.Fatalf("runs metric = %v, want 1", value)
	}
	if value := metricValue(families, "virt_validation_executions_total", map[string]string{"profile": "basic-v1", "check_id": "canary/workflow", "outcome": "Passed", "severity": "Required"}); value != 1 {
		t.Fatalf("canary execution metric = %v, want 1", value)
	}
	if value := metricValue(families, "virt_validation_executions_total", map[string]string{"profile": "basic-v1", "check_id": "unknown", "outcome": "Failed", "severity": "Required"}); value != 1 {
		t.Fatalf("unknown execution metric = %v, want 1", value)
	}
	if value := metricValue(families, "virt_validation_report_reductions_total", map[string]string{"profile": "basic-v1", "level": "2"}); value != 1 {
		t.Fatalf("report reduction metric = %v, want 1", value)
	}
	if value := histogramCount(families, "virt_validation_queue_duration_seconds", map[string]string{"profile": "basic-v1", "outcome": "acquired"}); value != 1 {
		t.Fatalf("queue duration metric count = %v, want 1", value)
	}
	if value := metricValue(families, "virt_validation_queue_timeouts_total", map[string]string{"profile": "basic-v1"}); value != 1 {
		t.Fatalf("queue timeout metric = %v, want 1", value)
	}
	if value := metricValue(families, "virt_validation_terminal_errors_total", map[string]string{"profile": "basic-v1", "reason": "InvalidCredentials"}); value != 1 {
		t.Fatalf("terminal error metric = %v, want 1", value)
	}
	if value := metricValue(families, "virt_validation_terminal_errors_total", map[string]string{"profile": "basic-v1", "reason": "unknown"}); value != 1 {
		t.Fatalf("unknown terminal error metric = %v, want 1", value)
	}
	if value := metricValue(families, "virt_validation_active_runs", map[string]string{"profile": "basic-v1"}); value != 1 {
		t.Fatalf("active validations metric = %v, want 1", value)
	}
	if value := metricValue(families, "virt_validation_active_runs", map[string]string{"profile": "basic-all-nodes-v1"}); value != 0 {
		t.Fatalf("inactive supported-profile metric = %v, want 0", value)
	}
}

func histogramCount(families []*dto.MetricFamily, name string, labels map[string]string) uint64 {
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			if labelsMatch(metric.Label, labels) && metric.Histogram != nil {
				return metric.Histogram.GetSampleCount()
			}
		}
	}
	return 0
}

func TestMetricLabelsAreBounded(t *testing.T) {
	if profile := metricProfile("not-a-profile-v999"); profile != "unknown" {
		t.Fatalf("metric profile = %q, want unknown", profile)
	}
	if checkID := metricCheckID("unknown/123456"); checkID != "unknown" {
		t.Fatalf("metric check ID = %q, want unknown", checkID)
	}
	if reason := terminalErrorReason("arbitrary controller error"); reason != "unknown" {
		t.Fatalf("terminal error reason = %q, want unknown", reason)
	}
}

func metricValue(families []*dto.MetricFamily, name string, labels map[string]string) float64 {
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			if labelsMatch(metric.Label, labels) {
				if metric.Gauge != nil {
					return metric.Gauge.GetValue()
				}
				if metric.Counter != nil {
					return metric.Counter.GetValue()
				}
			}
		}
	}
	return 0
}

func labelsMatch(actual []*dto.LabelPair, expected map[string]string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for _, label := range actual {
		if expected[label.GetName()] != label.GetValue() {
			return false
		}
	}
	return true
}
