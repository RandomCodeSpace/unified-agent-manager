BINARY := uam
MODULE := github.com/RandomCodeSpace/unified-agent-manager
CMD := ./cmd/uam
GOBIN ?= $(shell go env GOPATH)/bin
# Release tags sit on generated children of main (docs/releasing.md); the
# script names the newest release whose source is in this checkout.
VERSION ?= $(shell scripts/version 2>/dev/null || echo dev)
LDFLAGS := -X $(MODULE)/internal/version.Override=$(VERSION)

.PHONY: all build install run test test-race test-e2e cover lint tidy clean web check-web

all: build

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o bin/$(BINARY) $(CMD)

install: web
	mkdir -p $(GOBIN)
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o $(GOBIN)/$(BINARY) $(CMD)

# The web interface is a Vite build embedded into the binary from
# internal/web/dist. Source builds need Node.js; release tags include the bundle.
web:
	npm --prefix web ci
	npm --prefix web run build
	$(MAKE) check-web

# Tagged bundles must match the regenerated tree exactly, including extra files.
# Source checkouts intentionally leave the generated directory ignored.
check-web:
	@test -s internal/web/dist/index.html
	@set -eu; if test -n "$$(git ls-tree -r --name-only HEAD -- internal/web/dist)"; then \
		git diff --exit-code HEAD -- internal/web/dist; \
		extra="$$(git ls-files --others --ignored --exclude-standard -- internal/web/dist)"; \
		test -z "$$extra" || { printf 'Unexpected generated web assets:\n%s\n' "$$extra"; exit 1; }; \
	fi

run: build
	./bin/$(BINARY) web

test: web
	go test ./...

# Same command as the CI test-race job.
test-race: web
	go test -race -count=1 ./...

# Exercise the authenticated daemon lifecycle with the built binary. Test
# directories and tokens are disposable; no provider model calls are made.
test-e2e: build
	UAM_WEB_TEST_BIN=$(CURDIR)/bin/$(BINARY) go test ./internal/cli/ -run '^TestWebServiceOutlivesLauncherTerminal$$' -count=1 -v

cover: web
	go test -coverprofile=coverage.out ./... >/dev/null
	go tool cover -func=coverage.out | tail -1
	@echo "per-package: go tool cover -func=coverage.out"

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

clean:
	rm -rf bin
