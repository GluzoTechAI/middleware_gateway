.PHONY: fmt vet test test-race check verify build run devstack tidy docker-up docker-down

BINARY  ?= bin/server
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

fmt:
	gofmt -l -w .

vet:
	go vet ./...

test:
	go test ./...

test-race:
	go test -race ./...

check: fmt vet test

# The non-mutating gate. `check` depends on `fmt`, which is `gofmt -l -w .` and
# rewrites the tree, so `check` can never fail on formatting and dirties the
# diff you are about to review. Use `verify` before committing and `check` to
# fix things up.
verify:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needs to run on:"; gofmt -l .; exit 1; }
	go vet ./...
	go test ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(BINARY) ./cmd/server

run:
	go run ./cmd/server

# The whole stack on a machine with no Docker, no PostgreSQL and no Redis:
# embedded infrastructure, stub vendors, one order driven end to end.
devstack:
	go run ./cmd/devstack

tidy:
	go mod tidy

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down
