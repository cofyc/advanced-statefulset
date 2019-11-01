GO  := go

# Enable GO111MODULE=on explicitly, disable it with GO111MODULE=off when necessary.
export GO111MODULE := on

ARCH ?= $(shell go env GOARCH)
OS ?= $(shell go env GOOS)

ALL_TARGETS := cmd/controller-manager
SRC_PREFIX := github.com/cofyc/advanced-statefulset

all: build
.PHONY: all

verify:
	./hack/verify-all.sh 
.PHONY: verify

build: $(ALL_TARGETS)
.PHONY: all

$(ALL_TARGETS):
	GOOS=$(OS) GOARCH=$(ARCH) CGO_ENABLED=0 $(GO) build -o output/bin/$(OS)/$(ARCH)/$@ $(SRC_PREFIX)/$@
.PHONY: $(ALL_TARGETS)

test:
	hack/make-rules/test.sh $(WHAT)
.PHONY: test

test-integration: vendor/k8s.io/kubernetes/pkg/generated/openapi/zz_generated.openapi.go
	hack/make-rules/test-integration.sh $(WHAT)
.PHONY: test-integration

e2e:
	hack/e2e.sh
.PHONY: e2e

vendor/k8s.io/kubernetes/pkg/generated/openapi/zz_generated.openapi.go:
	hack/generate-kube-openapi.sh
.PHONY: vendor/k8s.io/kubernetes/pkg/generated/openapi/zz_generated.openapi.go