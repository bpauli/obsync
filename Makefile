.DEFAULT_GOAL := build

.PHONY: build test test-integration fmt lint fmt-check

BIN_DIR := $(CURDIR)/bin
BIN := $(BIN_DIR)/obsync
CMD := ./cmd/obsync

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT := $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo "")
DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X obsync/internal/cmd.version=$(VERSION) -X obsync/internal/cmd.commit=$(COMMIT) -X obsync/internal/cmd.date=$(DATE)

build:
	@mkdir -p $(BIN_DIR)
	@go build -ldflags "$(LDFLAGS)" -o $(BIN) $(CMD)

test:
	@go test ./...

test-integration:
	@go test -tags integration -timeout 120s ./internal/integration/

fmt:
	@gofumpt -w . 2>/dev/null || gofmt -w .

lint:
	@golangci-lint run || true

fmt-check:
	@gofmt -l . | grep -q . && echo "Files need formatting" && exit 1 || true
