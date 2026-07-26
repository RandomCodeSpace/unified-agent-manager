BINARY := uam
MODULE := github.com/RandomCodeSpace/unified-agent-manager
CMD := ./cmd/uam
GOBIN ?= $(shell go env GOPATH)/bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X $(MODULE)/internal/version.Override=$(VERSION)

.PHONY: all build install run test test-e2e cover lint tidy clean

all: build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o bin/$(BINARY) $(CMD)

install:
	mkdir -p $(GOBIN)
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o $(GOBIN)/$(BINARY) $(CMD)

run: build
	./bin/$(BINARY)

test:
	go test ./...

# End-to-end tests drive the built binary over real PTYs, so they need it built
# first and are skipped unless UAM_E2E_BIN points at it.
test-e2e: build
	UAM_E2E_BIN=$(CURDIR)/bin/$(BINARY) go test ./internal/session/ ./internal/app/ -run TestE2E -count=1 -v

cover:
	go test -coverprofile=coverage.out ./... >/dev/null
	go tool cover -func=coverage.out | tail -1
	@echo "per-package: go tool cover -func=coverage.out"

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

clean:
	rm -rf bin
