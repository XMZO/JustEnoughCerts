# Shortcuts for Linux and macOS. Each target is a plain go, docker or
# goreleaser command, so on Windows run those directly.

GO      ?= go
PKG     := github.com/XMZO/JustEnoughCerts
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
LDFLAGS := -s -w -X $(PKG)/internal/kernel/buildinfo.version=$(VERSION)
IMAGE   ?= jec:dev

.PHONY: help build test lint i18n-check i18n-fix run image snapshot clean

help: ## list the targets
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-10s %s\n", $$1, $$2}'

build: ## build build/jec for this machine
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o build/jec ./cmd/jec

test: ## run the tests with the race detector
	$(GO) test -race -count=1 ./...

lint: ## run golangci-lint
	golangci-lint run

i18n-check: ## run the i18n gates
	$(GO) run ./tools/i18n check
	$(GO) run ./tools/i18nlint ./...

i18n-fix: ## put the locale files in canonical form and regenerate the keys
	$(GO) run ./tools/i18n fix

run: ## run the server with ./data
	$(GO) run ./cmd/jec serve

image: ## build the container image from source
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

snapshot: ## build the release artifacts locally without publishing
	goreleaser release --snapshot --clean --skip=sign,sbom

clean: ## remove build output
	rm -rf build dist
