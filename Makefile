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

CONTROLLER_PACKAGES := ./api/... ./cmd/... ./internal/...
CONTROLLER_SOURCES := $(shell find api cmd internal -name '*.go')
CONTROLLER_GEN_VERSION := v0.16.5
CONTROLLER_GEN := go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)
SETUP_ENVTEST ?= setup-envtest
ENVTEST_KUBERNETES_VERSION ?= 1.37.x

.PHONY: build-controller check-controller fmt-controller generate generate-check lint-controller test-controller test-envtest image-validator image-controller deploy-controller bootstrap-target run-controller-e2e

CONTAINER_ENGINE ?= podman
VALIDATOR_IMAGE ?= quay.io/openshift-cnv/virt-cluster-validate:dev
CONTROLLER_IMAGE ?= quay.io/openshift-cnv/virt-validation-controller:dev

generate:
	$(CONTROLLER_GEN) object:headerFile=hack/boilerplate.go.txt paths=./api/...
	$(CONTROLLER_GEN) crd:crdVersions=v1 output:crd:dir=config/crd/bases paths=./api/...
	$(CONTROLLER_GEN) rbac:roleName=virt-validation-controller output:rbac:dir=config/rbac paths=./internal/controller

generate-check:
	@before="$$(git status --porcelain -- api/v1alpha1 config/crd/bases config/rbac/role.yaml)"; \
	$(MAKE) generate; \
	after="$$(git status --porcelain -- api/v1alpha1 config/crd/bases config/rbac/role.yaml)"; \
	test "$$before" = "$$after" || { echo "Generated API artifacts are stale; run 'make generate'."; git diff -- api/v1alpha1 config/crd/bases config/rbac/role.yaml; exit 1; }

build-controller:
	go build -o bin/validation-controller ./cmd/validation-controller

image-validator:
	$(CONTAINER_ENGINE) build -t $(VALIDATOR_IMAGE) -f Containerfile .

image-controller:
	$(CONTAINER_ENGINE) build -t $(CONTROLLER_IMAGE) -f build/validation-controller/Containerfile .

deploy-controller:
	CONTROLLER_IMAGE=$(CONTROLLER_IMAGE) VALIDATOR_IMAGE=$(VALIDATOR_IMAGE) ./hack/deploy-controller.sh

bootstrap-target:
	./hack/bootstrap-validation-target.sh

run-controller-e2e:
	./hack/run-controller-e2e.sh

test-controller:
	go test $(CONTROLLER_PACKAGES)

# envtest is intentionally module-mode because it is a test-only dependency.
test-envtest:
	KUBEBUILDER_ASSETS="$$($(SETUP_ENVTEST) use -p path $(ENVTEST_KUBERNETES_VERSION))" go test -mod=mod -tags=envtest ./internal/controller -run TestVirtualizationValidationCRD

fmt-controller:
	gofmt -w $(CONTROLLER_SOURCES)

lint-controller:
	@unformatted="$$(gofmt -l $(CONTROLLER_SOURCES))"; \
	test -z "$$unformatted" || { echo "Run 'make fmt-controller':"; echo "$$unformatted"; exit 1; }
	go vet $(CONTROLLER_PACKAGES)

check-controller: lint-controller test-controller build-controller generate-check
