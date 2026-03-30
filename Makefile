GIT_TAG ?= $(shell git describe --tags --exact-match 2>/dev/null || true)
VERSION ?= $(if $(strip $(GIT_TAG)),$(strip $(GIT_TAG)),$(shell cat VERSION 2>/dev/null || echo dev))
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GOFILES := $(shell if command -v rg >/dev/null 2>&1; then rg --files -g '*.go'; else find . -type f -name '*.go' | sed 's|^\./||' | LC_ALL=C sort; fi)
LDFLAGS := -s -w \
	-X github.com/OnslaughtSnail/caelis/internal/version.Version=$(VERSION) \
	-X github.com/OnslaughtSnail/caelis/internal/version.Commit=$(COMMIT) \
	-X github.com/OnslaughtSnail/caelis/internal/version.Date=$(DATE)

.PHONY: build build-cli fmt fmt-check install lint quality test vet eval-light eval-nightly eval-real-matrix release-dry-run

fmt:
	gofmt -w $(GOFILES)

fmt-check:
	@test -z "$$(gofmt -l $(GOFILES))"

build:
	go build ./...

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/cli

build-cli:
	mkdir -p ./.tmp/bin
	go build -ldflags "$(LDFLAGS)" -o ./.tmp/bin/caelis ./cmd/cli

vet:
	go vet ./...

lint:
	golangci-lint run ./...

quality: fmt-check lint test vet build

test:
ifeq ($(CI),true)
	bash ./scripts/go_test_serial.sh
else
	go test ./...
endif

eval-light:
	go run ./eval/cmd -suite light

eval-nightly:
	go run ./eval/cmd -suite nightly

eval-real-matrix:
	go run ./eval/cmd -suite light -models "deepseek-chat,gemini-3.1-flash-lite-preview" -stream-modes both -thinking-modes both -thinking-budget 1024

release-dry-run:
	goreleaser release --clean --snapshot
