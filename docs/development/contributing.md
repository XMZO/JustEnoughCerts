# Contributing

[中文](contributing.zh-CN.md)

## Requirements

Go 1.26 or later, Node.js 24 and pnpm 10 (`corepack enable`).

## Commands

```sh
make web         # build the web UI into web/dist, which the Go build embeds
make run         # run the server with ./data
make dev         # the web UI with hot reload on :5173, proxying /api to make run
make test        # Go tests with the race detector, then the web UI's tests
make lint        # golangci-lint, the type check and ESLint
make i18n-check  # the i18n checks
make gen         # after changing messages or the API: regenerate keys, types and fonts
make new-module id=acme category=source  # scaffold a module
```

Each target runs a single `go`, `pnpm` or `docker` command, which can be
run directly on Windows; `make help` lists all targets.

## Rules

Automated checks enforce the following rules:

- All UI text is defined in `i18n/locales/*.json`, one file per language,
  and referenced in code through generated constants. Run
  `go run ./tools/i18n fix` after editing a locale file; see the
  [Translation guide](translating.md).
- Code, comments and scripts are written in English.
- Modules communicate only through the kernel: a module never imports
  another module, and the kernel never imports a module.

## Documentation

- [PLAN.md](../../PLAN.md) describes the architecture, the module
  interfaces and the roadmap; [docs/design](../design) contains the design
  system and the page designs.
- The pages of this documentation site live in the `guide`, `usage`,
  `operations`, `reference` and `development` directories of `docs/`, each
  in English (`*.md`) and Chinese (`*.zh-CN.md`). `docs/site/pages.mjs`
  assembles them and VitePress builds the site; the home page and the
  navigation are defined in `docs/site`.
- To preview the site locally, run `pnpm -C docs/site install`, then
  `pnpm -C docs/site dev`, and open `http://localhost:5173/JustEnoughCerts/`.
  The docs workflow publishes the site whenever main changes.
