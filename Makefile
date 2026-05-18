BINARY := uam
PKG := github.com/RandomCodeSpace/unified-agent-manager
GOBIN ?= $(shell go env GOPATH)/bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X $(PKG)/internal/version.Override=$(VERSION)

.PHONY: all build install run test lint tidy clean

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

install:
	mkdir -p $(GOBIN)
	go build -ldflags "$(LDFLAGS)" -o $(GOBIN)/$(BINARY) .

run: build
	./bin/$(BINARY)

test:
	go test ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

clean:
	rm -rf bin
