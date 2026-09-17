.PHONY: build test vet lint generate check-generated check

GOLANGCI_LINT_VERSION := v2.8.0

build:
	go build -o terraform-provider-asc .

# TF_ACC=1 is required: without it every resource.Test under internal/tests
# silently t.Skip()s while still reporting PASS, so a green run would prove
# nothing about the acceptance tier.
test:
	TF_ACC=1 go test -count=1 ./...

vet:
	go vet ./...

# CGO_ENABLED=0 applies to building the linter, not to the code under test.
# golangci-lint needs no cgo, and linking it against the host toolchain fails on
# a macOS nix shell whose clang wrapper cannot resolve -lresolv. Disabling cgo
# for this one build makes the gate runnable everywhere without changing a
# single analyzer.
lint:
	CGO_ENABLED=0 go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

generate:
	go generate ./...

check-generated:
	./scripts/check-generated.sh

check: test vet lint check-generated
