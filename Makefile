BINARY := uam
PKG := github.com/randomcodespace/unified-agent-manager
GOBIN ?= $(shell go env GOPATH)/bin

.PHONY: all build install run test lint tidy clean

all: build

build:
	go build -o bin/$(BINARY) ./cmd/uam

install:
	go install ./cmd/uam

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
