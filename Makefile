# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.24.2
LOCALBIN ?= $(shell pwd)/testbin

test:
	mkdir -p $(LOCALBIN)
	KUBEBUILDER_ASSETS="$(shell go run sigs.k8s.io/controller-runtime/tools/setup-envtest@latest use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test -coverprofile cover.out ./...