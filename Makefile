.PHONY: build test fmt

build:
	go build -o terraform-provider-asc

test:
	TF_ACC=1 go test ./...

fmt:
	gofmt -w .
