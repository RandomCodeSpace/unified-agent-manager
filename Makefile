BINARY := uam
MODULE := github.com/RandomCodeSpace/unified-agent-manager
CMD := ./cmd/uam
GOBIN ?= $(shell go env GOPATH)/bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X $(MODULE)/internal/version.Override=$(VERSION)

.PHONY: all build install run test test-e2e test-e2e-real cover lint tidy clean test-e2e-dashboard web

all: build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o bin/$(BINARY) $(CMD)

install:
	mkdir -p $(GOBIN)
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o $(GOBIN)/$(BINARY) $(CMD)

# The web interface is a Vite build embedded into the binary from
# internal/web/dist. Node.js is needed only to rebuild it, never to run uam.
web:
	npm --prefix web ci
	npm --prefix web run build

run: build
	./bin/$(BINARY)

test:
	go test ./...

# End-to-end tests drive the built binary over real PTYs, so they need it built
# first and are skipped unless UAM_E2E_BIN points at it.
test-e2e: build
	UAM_E2E_BIN=$(CURDIR)/bin/$(BINARY) go test ./internal/session/ ./internal/app/ -run TestE2E -count=1 -v

# Real-provider end-to-end tests launch the installed opencode, copilot and
# codex CLIs and make model calls on the operator's accounts. Opt in with
# UAM_E2E_REAL_PROVIDERS (comma-separated); provider state is isolated per run.
test-e2e-real: build
	UAM_E2E_BIN=$(CURDIR)/bin/$(BINARY) UAM_E2E_REAL_PROVIDERS=$${UAM_E2E_REAL_PROVIDERS:-opencode,copilot,codex} go test ./internal/e2e/ -run TestRealProvider -count=1 -v -timeout 30m

# Dashboard pointer checks on a real PTY (and over ssh to localhost when a
# key-based login is available); needs no provider account.
test-e2e-dashboard: build
	UAM_E2E_BIN=$(CURDIR)/bin/uam go test ./internal/e2e/ -run TestE2EDashboard -count=1 -v -timeout 10m

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
