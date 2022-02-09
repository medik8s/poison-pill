# SHELL defines bash so all the inline scripts here will work as expected.
SHELL := /bin/bash

# Override this when building images for dev only!
IMAGE_REGISTRY ?= quay.io/medik8s

# Current Operator version

# VERSION defines the project version for the bundle. 
# Update this value when you upgrade the version of your project.
# To re-generate a bundle for another specific version without changing the standard setup, you can:
# - use the VERSION as arg of the bundle target (e.g make bundle VERSION=0.0.2)
# - use environment variables to overwrite this value (e.g export VERSION=0.0.2)
export VERSION ?= $(shell git describe --match="v*"| sed 's/v//')

# use stable channel
CHANNELS = stable

# CHANNELS define the bundle channels used in the bundle. 
# Add a new line here if you would like to change its default config. (E.g CHANNELS = "preview,fast,stable")
# To re-generate a bundle for other specific channels without changing the standard setup, you can:
# - use the CHANNELS as arg of the bundle target (e.g make bundle CHANNELS=preview,fast,stable)
# - use environment variables to overwrite this value (e.g export CHANNELS="preview,fast,stable")
ifneq ($(origin CHANNELS), undefined)
BUNDLE_CHANNELS := --channels=$(CHANNELS)
endif

# DEFAULT_CHANNEL defines the default channel used in the bundle. 
# Add a new line here if you would like to change its default config. (E.g DEFAULT_CHANNEL = "stable")
# To re-generate a bundle for any other default channel without changing the default setup, you can:
# - use the DEFAULT_CHANNEL as arg of the bundle target (e.g make bundle DEFAULT_CHANNEL=stable)
# - use environment variables to overwrite this value (e.g export DEFAULT_CHANNEL="stable")
ifneq ($(origin DEFAULT_CHANNEL), undefined)
BUNDLE_DEFAULT_CHANNEL := --default-channel=$(DEFAULT_CHANNEL)
endif
BUNDLE_METADATA_OPTS ?= $(BUNDLE_CHANNELS) $(BUNDLE_DEFAULT_CHANNEL)

OPERATOR_NAME ?= poison-pill

# For example, running 'make bundle-build bundle-push index-build index-push' will build and push both
# medik8s/poison-pill-operator-bundle:$VERSION and medik8s/poison-pill-operator-index:$VERSION.
IMAGE_TAG_BASE ?= $(IMAGE_REGISTRY)/$(OPERATOR_NAME)

# INDEX_IMG defines the image:tag used for the index.
# You can use it as an arg. (E.g make bundle-build INDEX_IMG=<some-registry>/<project-name-index>:<tag>)
INDEX_IMG ?= $(IMAGE_TAG_BASE)-operator-index:$(VERSION)

# BUNDLE_IMG defines the image:tag used for the bundle. 
# You can use it as an arg. (E.g make bundle-build BUNDLE_IMG=<some-registry>/<project-name-bundle>:<tag>)
BUNDLE_IMG ?= $(IMAGE_TAG_BASE)-operator-bundle:$(VERSION)

# Image URL to use all building/pushing image targets
IMG ?= $(IMAGE_TAG_BASE)-operator:$(VERSION)

# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:trivialVersions=true,preserveUnknownFields=false"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Use kubectl, fallback to oc
KUBECTL = kubectl
ifeq (,$(shell which kubectl))
KUBECTL=oc
endif
##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=config/crd/bases

generate: controller-gen protoc ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."
	PATH=$(PATH):$(shell pwd)/bin/proto/bin && $(PROTOC) --go_out=. --go-grpc_out=. pkg/peerhealth/peerhealth.proto

fmt: ## Run go fmt against code.
	go fmt ./...

vet: ## Run go vet against code.
	go vet ./...

verify-no-changes: ## verify there are no un-staged changes
	./hack/verify-diff.sh

fetch-mutation: ## fetch mutation package.
	GO111MODULE=off go get -t -v github.com/mshitrit/go-mutesting/...

ENVTEST_ASSETS_DIR=$(shell pwd)/testbin
test: manifests generate fmt vet ## Run tests.
	mkdir -p ${ENVTEST_ASSETS_DIR}
	test -f ${ENVTEST_ASSETS_DIR}/setup-envtest.sh || curl -sSLo ${ENVTEST_ASSETS_DIR}/setup-envtest.sh https://raw.githubusercontent.com/kubernetes-sigs/controller-runtime/v0.7.2/hack/setup-envtest.sh
	source ${ENVTEST_ASSETS_DIR}/setup-envtest.sh; fetch_envtest_tools $(ENVTEST_ASSETS_DIR); setup_envtest_env $(ENVTEST_ASSETS_DIR); go test ./api/... ./controllers/... ./pkg/... -test.v -coverprofile cover.out

test-mutation: verify-no-changes fetch-mutation ## Run mutation tests in manual mode.
	echo -e "## Verifying diff ## \n##Mutations tests actually changes the code while running - this is a safeguard in order to be able to easily revert mutation tests changes (in case mutation tests have not completed properly)##"
	./hack/test-mutation.sh

test-mutation-ci: fetch-mutation ## Run mutation tests as part of auto build process.
	./hack/test-mutation.sh

##@ Build

build: generate fmt vet ## Build manager binary.
	go build -o bin/manager main.go

run: manifests generate fmt vet ## Run a controller from your host.
	go run ./main.go

docker-build: test ## Build docker image with the manager.
	podman build -t ${IMG} .

docker-push: ## Push docker image with the manager.
	podman push ${IMG}

##@ Time fixes

.PHONY: csv-date
csv-date: ## Set createdAt date in the CSV.
	sed -r -i "s|createdAt: .*|createdAt: `date '+%Y-%m-%d %T'`|;"  ./config/manifests/bases/$(OPERATOR_NAME).clusterserviceversion.yaml

# Some fixes in the bundle
.PHONY: bundle-fixes
bundle-fixes: ## update container image
	sed -r -i "s|containerImage: .*|containerImage: $(IMG)|;" ./bundle/manifests/$(OPERATOR_NAME).clusterserviceversion.yaml

.PHONY: bundle-date
bundle-date: ## Set createdAt date in the bundle's CSV. Do not commit changes!
	sed -r -i "s|createdAt: .*|createdAt: `date '+%Y-%m-%d %T'`|;" ./bundle/manifests/$(OPERATOR_NAME).clusterserviceversion.yaml

.PHONY: bundle-date-reset
bundle-date-reset: ## Reset createdAt date to empty value
	sed -r -i "s|createdAt: .*|createdAt: \"\"|;" ./bundle/manifests/$(OPERATOR_NAME).clusterserviceversion.yaml

##@ Download Locally

CONTROLLER_GEN = $(shell pwd)/bin/controller-gen
controller-gen: ## Download controller-gen locally if necessary.
	$(call go-get-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@v0.4.1)

KUSTOMIZE = $(shell pwd)/bin/kustomize
kustomize: ## Download kustomize locally if necessary.
	$(call go-get-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v3@v3.8.7)

# go-get-tool will 'go get' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-get-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
BIN_DIR=$$(dirname $(1)) ;\
mkdir -p $$BIN_DIR ;\
echo "Downloading $(2)" ;\
GOBIN=$$BIN_DIR go get $(2) ;\
rm -rf $$TMP_DIR ;\
}
endef

.PHONY: operator-sdk
OPERATOR_SDK = ./bin/operator-sdk
operator-sdk: ## Download operator-sdk locally if necessary.
ifeq (,$(wildcard $(OPERATOR_SDK)))
	@{ \
	set -e ;\
	mkdir -p $(dir $(OPERATOR_SDK)) ;\
	OS=linux && ARCH=amd64 && \
	curl -sSLo $(OPERATOR_SDK) https://github.com/operator-framework/operator-sdk/releases/download/v1.12.0/operator-sdk_$${OS}_$${ARCH} ;\
	chmod +x $(OPERATOR_SDK) ;\
	}
endif

PROTOC_GEN_GO = $(shell pwd)/bin/proto/bin/protoc-gen-go
protoc-gen-go: ## Download protoc-gen-go locally if necessary.
	$(call go-get-tool,$(PROTOC_GEN_GO),google.golang.org/protobuf/cmd/protoc-gen-go@v1.26)

PROTOC_GEN_GO_GRPC = $(shell pwd)/bin/proto/bin/protoc-gen-go-prpc
protoc-gen-go-grpc: ## Download protoc-gen-go-grpc locally if necessary.
	$(call go-get-tool,$(PROTOC_GEN_GO_GRPC),google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.1.0)

##@ Deployment

install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) apply -f -

uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) delete -f -

deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | envsubst | $(KUBECTL) apply -f -

undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete -f -

all: ## Build and push all the three images to quay (docker, bundle, and index).
	docker-build docker-push bundle bundle-build bundle-push index-build index-push

.PHONY: bundle 
bundle: manifests kustomize operator-sdk ## Generate bundle manifests and metadata, then validate generated files.
	$(OPERATOR_SDK) generate --verbose kustomize manifests -q
	cd config/manager && $(KUSTOMIZE) edit set image controller=$(IMG)
	$(KUSTOMIZE) build config/manifests | envsubst | $(OPERATOR_SDK) generate --verbose bundle -q --overwrite --version $(VERSION) $(BUNDLE_METADATA_OPTS)
	$(OPERATOR_SDK) bundle validate ./bundle
	$(MAKE) csv-date bundle-date

.PHONY: bundle-build 
bundle-build: ## Build the bundle image.
	podman build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

.PHONY: bundle-push
bundle-push: ## Push the bundle image
	podman push ${BUNDLE_IMG}

# Build an index image by adding bundle images to an empty catalog using the operator package manager tool, 'opm'.
# This recipe invokes 'opm' in 'semver' bundle add mode. For more information on add modes, see:
# https://github.com/operator-framework/community-operators/blob/7f1438c/docs/packaging-operator.md#updating-your-existing-operator
.PHONY: index-build
index-build: opm ## Build an index image.
	$(OPM) index add --container-tool podman --mode semver --tag $(INDEX_IMG) --bundles $(BUNDLE_IMG)

# Push the index image.
.PHONY: index-push
index-push: ## Push an index image.
	podman push $(INDEX_IMG)
	
.PHONY: protoc 
PROTOC = $(shell pwd)/bin/proto/bin/protoc
protoc: protoc-gen-go protoc-gen-go-grpc ## Download protoc (protocol buffers tool needed for gRPC)
	test -f ${PROTOC} || (cd $(shell pwd)/bin/proto && curl -sSLo protoc.zip https://github.com/protocolbuffers/protobuf/releases/download/v3.16.0/protoc-3.16.0-linux-x86_64.zip && unzip protoc.zip && rm protoc.zip)

.PHONY: e2e-test
e2e-test: ## Run end to end tests.
	# KUBECONFIG must be set to the cluster, and PP needs to be deployed already
    # count arg makes the test ignoring cached test results
	go test ./e2e -ginkgo.v -ginkgo.progress -test.v -timeout 60m -count=1