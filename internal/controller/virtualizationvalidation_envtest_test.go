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

//go:build envtest

package controller

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	validationv1alpha1 "github.com/openshift-cnv/virt-cluster-validate/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestVirtualizationValidationCRD(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("set KUBEBUILDER_ASSETS or run make test-envtest")
	}

	environment := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")}}
	configuration, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := environment.Stop(); err != nil {
			t.Error(err)
		}
	}()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := validationv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient, err := client.New(configuration, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := apiClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "virt-validation-system"}}); err != nil {
		t.Fatal(err)
	}
	for _, sample := range []string{
		filepath.Join("..", "..", "config", "samples", "validation_v1alpha1_virtualizationvalidation.yaml"),
		filepath.Join("..", "..", "config", "samples", "validation_v1alpha1_virtualizationvalidation_all_nodes.yaml"),
	} {
		applySample(t, ctx, apiClient, sample)
	}

	validation := &validationv1alpha1.VirtualizationValidation{}
	key := client.ObjectKey{Namespace: "virt-validation-system", Name: "target-canary"}
	if err := apiClient.Get(ctx, key, validation); err != nil {
		t.Fatal(err)
	}
	systemTrust := validation.DeepCopy()
	systemTrust.Name = "target-system-trust"
	systemTrust.ResourceVersion = ""
	systemTrust.UID = ""
	systemTrust.Spec.Target.CredentialsSecret.CAKey = ""
	if err := apiClient.Create(ctx, systemTrust); err != nil {
		t.Fatal(err)
	}
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(systemTrust), systemTrust); err != nil {
		t.Fatal(err)
	}
	if systemTrust.Spec.Target.CredentialsSecret.CAKey != "" {
		t.Fatalf("explicit empty caKey must preserve system trust, got %q", systemTrust.Spec.Target.CredentialsSecret.CAKey)
	}
	defaults := validation.DeepCopy()
	defaults.Name = "target-defaults"
	defaults.ResourceVersion = ""
	defaults.UID = ""
	defaults.Spec.Run.ID = ""
	defaults.Spec.Run.QueueTimeoutSeconds = 0
	defaults.Spec.Run.Cancel = false
	if err := apiClient.Create(ctx, defaults); err != nil {
		t.Fatalf("creating request without optional run fields: %v", err)
	}
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(defaults), defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.Spec.Run.ID != "initial" || defaults.Spec.Run.QueueTimeoutSeconds != 0 {
		t.Fatalf("run defaults were not applied: %#v", defaults.Spec.Run)
	}
	defaults.Spec.Run.Cancel = true
	if err := apiClient.Update(ctx, defaults); err != nil {
		t.Fatalf("cancelling request with defaulted run fields must be allowed: %v", err)
	}
	defaults.Spec.Run.Cancel = false
	defaults.Spec.Run.ID = "retry-1"
	if err := apiClient.Update(ctx, defaults); err != nil {
		t.Fatalf("changing run.id must permit resetting cancellation: %v", err)
	}
	validation.Spec.Run.Cancel = true
	if err := apiClient.Update(ctx, validation); err != nil {
		t.Fatalf("setting run.cancel must be allowed: %v", err)
	}
	validation.Spec.Run.Profile = "different-v1"
	if err := apiClient.Update(ctx, validation); err == nil || !apierrors.IsInvalid(err) {
		t.Fatalf("changing profile must be rejected by CRD validation, got %v", err)
	}
}

func applySample(t *testing.T, ctx context.Context, apiClient client.Client, path string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := kyaml.NewYAMLOrJSONDecoder(file, 4096)
	for {
		object := &unstructured.Unstructured{}
		if err := decoder.Decode(object); err == io.EOF {
			return
		} else if err != nil {
			t.Fatal(err)
		}
		if object.IsList() || object.GetKind() == "" {
			continue
		}
		if err := apiClient.Create(ctx, object); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("apply %s %s: %v", object.GetKind(), object.GetName(), err)
		}
	}
}
