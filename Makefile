# Shortcuts for Linux and macOS. Each target is a plain go, pnpm, docker or
# goreleaser command, so on Windows run those directly.

GO      ?= go
PNPM    ?= pnpm
PKG     := github.com/XMZO/JustEnoughCerts
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
LDFLAGS := -s -w -X $(PKG)/internal/kernel/buildinfo.version=$(VERSION)
IMAGE   ?= jec:dev

.PHONY: help web build test lint i18n-check i18n-fix gen new-module dev run image snapshot clean

help: ## list the targets
	@grep -hE '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-11s %s\n", $$1, $$2}'

web: ## build the web UI into web/dist, which the Go build embeds
	$(PNPM) -C web install --frozen-lockfile
	$(PNPM) -C web build

build: web ## build build/jec for this machine
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o build/jec ./cmd/jec

test: ## run the Go tests with the race detector, then the web UI's
	$(GO) test -race -count=1 ./...
	$(PNPM) -C web test

lint: ## run golangci-lint, then the web UI's type check and ESLint
	golangci-lint run
	$(PNPM) -C web typecheck
	$(PNPM) -C web lint

i18n-check: ## run the i18n gates
	$(GO) run ./tools/i18n check
	$(GO) run ./tools/i18nlint ./...

i18n-fix: ## put the locale files in canonical form and regenerate the keys
	$(GO) run ./tools/i18n fix

gen: ## regenerate the message keys, the lego catalog, the web UI's API types, its icons and fonts
	$(GO) generate ./i18n
	$(GO) run ./tools/icons
	$(GO) run ./tools/legocatalog
	$(GO) run ./tools/openapi
	$(PNPM) -C web gen:api
	$(PNPM) -C web fonts

new-module: ## scaffold a module: make new-module id=acme category=source
	$(GO) run ./tools/new-module -id '$(id)' -category '$(or $(category),feature)'

dev: ## run the web UI's dev server; it proxies /api to make run
	$(PNPM) -C web dev

run: ## run the server with ./data
	$(GO) run ./cmd/jec serve

image: ## build the container image from source
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

snapshot: web ## build the release artifacts locally without publishing
	goreleaser release --snapshot --clean --skip=sign,sbom

clean: ## remove build output
	rm -rf build dist
	find web/dist -mindepth 1 ! -name .gitkeep -delete
