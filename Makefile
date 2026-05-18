BINARY := uam
MODULE := github.com/RandomCodeSpace/unified-agent-manager
CMD := ./cmd/uam
GOBIN ?= $(shell go env GOPATH)/bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X $(MODULE)/internal/version.Override=$(VERSION)

.PHONY: all build install run test lint tidy clean

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(CMD)

install:
	mkdir -p $(GOBIN)
	go build -ldflags "$(LDFLAGS)" -o $(GOBIN)/$(BINARY) $(CMD)

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
