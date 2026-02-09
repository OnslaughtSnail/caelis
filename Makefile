VERSION ?= $(shell cat VERSION 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/OnslaughtSnail/caelis/internal/version.Version=$(VERSION) \
	-X github.com/OnslaughtSnail/caelis/internal/version.Commit=$(COMMIT) \
	-X github.com/OnslaughtSnail/caelis/internal/version.Date=$(DATE)

.PHONY: build build-cli vet test eval-light eval-nightly eval-real-matrix release-dry-run

build:
	go build ./...

build-cli:
	mkdir -p ./.tmp/bin
	go build -ldflags "$(LDFLAGS)" -o ./.tmp/bin/caelis ./cmd/cli

vet:
	go vet ./...

test:
	go test ./...

eval-light:
	go run ./eval/cmd -suite light

eval-nightly:
	go run ./eval/cmd -suite nightly

eval-real-matrix:
	go run ./eval/cmd -suite light -models "deepseek-chat,gemini-2.5-flash" -stream-modes both -thinking-modes both -thinking-budget 1024

release-dry-run:
	goreleaser release --clean --snapshot
