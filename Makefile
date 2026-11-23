.PHONY: build test lint fmt docs-check

export PATH := $(shell go env GOPATH)/bin:$(PATH)

build:
	mkdir -p bin
	go build -o bin/inbai ./cmd/inbai
	go build -o bin/inbaid ./cmd/inbaid

test:
	go test ./...

fmt:
	gofmt -w .

lint:
	golangci-lint run ./...

docs-check:
	go run ./tools/docscheck
