.PHONY: build test test-race vet lint check-format check-modules vulncheck generate check-generated check release-snapshot install-check

GOLANGCI_LINT_VERSION := v2.13.2
GOVULNCHECK_VERSION := v1.8.0
ACTIONLINT_VERSION := v1.7.12

build:
	go build -o terraform-provider-asc .

# Use the same CLI for acceptance tests and migration fixtures.
test:
	TF_ACC=1 TF_ACC_TERRAFORM_PATH="$${TF_ACC_TERRAFORM_PATH:-$$(command -v terraform)}" go test -count=1 ./...

test-race:
	TF_ACC=1 TF_ACC_TERRAFORM_PATH="$${TF_ACC_TERRAFORM_PATH:-$$(command -v terraform)}" go test -race -count=1 ./...

vet:
	go vet ./...

# Building the linter without cgo avoids the macOS Nix linker dependency.
lint:
	CGO_ENABLED=0 go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...
	go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

check-format:
	CGO_ENABLED=0 go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) fmt --diff

check-modules:
	go mod tidy -diff
	go mod verify

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

generate:
	go generate ./...

check-generated:
	./scripts/check-generated.sh

check: test vet lint check-format check-modules check-generated vulncheck

# Creates a temporary signing key and never publishes artifacts.
release-snapshot:
	./scripts/release-snapshot-check.sh

# Requires the artifacts produced by release-snapshot.
install-check:
	./scripts/install-check.sh
