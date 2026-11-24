.PHONY: build test lint fmt docs-check

export PATH := $(shell go env GOPATH)/bin:$(PATH)

# make 3.81 (macOS) looks recipe commands up on its own PATH, so lint resolves
# golangci-lint itself: PATH first, then GOPATH/bin.
GOBIN_DIR ?= $(shell go env GOPATH)/bin

build:
	mkdir -p bin
	go build -o bin/inbai ./cmd/inbai
	go build -o bin/inbaid ./cmd/inbaid

test:
	go test ./...

fmt:
	gofmt -w .

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	elif [ -x "$(GOBIN_DIR)/golangci-lint" ]; then \
		"$(GOBIN_DIR)/golangci-lint" run ./...; \
	else \
		echo "golangci-lint not found on PATH or in $(GOBIN_DIR)." >&2; \
		echo "Install it: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0" >&2; \
		exit 1; \
	fi

docs-check:
	go run ./tools/reference -check
	go run ./tools/docscheck
