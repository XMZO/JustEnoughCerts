# Just Enough Certs

*Just enough certs to keep your site green.*

[简体中文](README.zh-CN.md)

Just Enough Certs (JEC) is a self-hosted SSL/TLS certificate manager. It
gets certificates from wherever you buy or issue them, proves that you
control the domains, deploys the certificates to your CDNs and servers, and
renews them before they expire. Its name and look are a nod to the
Minecraft mod JEI.

> [!WARNING]
> JEC is in early development. The 0.0.x releases are the skeleton: the
> server, the setup wizard and signing in, in English and Chinese. The
> main branch does what the list below says works, but it is not released
> yet and has not been tried end to end against a real Alibaba Cloud ESA
> site. See the roadmap in [PLAN.md](PLAN.md) (in Chinese).

## Features

Working on the main branch:

- Certificates from any ACME CA, with External Account Binding, or
  uploaded by hand.
- Domain validation through DNS records at any of the 221 providers that
  [lego](https://github.com/go-acme/lego) supports, or through files in a
  website's directory, local or over SSH.
- Deployment of each new certificate to Alibaba Cloud ESA (removing the one
  it replaces), to local directories, to servers over SSH (with a command
  afterwards, such as reloading nginx) and to webhooks.
- Renewal once a certificate enters its renewal window; failures are
  retried with a growing delay.
- Notifications through Telegram, email (SMTP) and webhooks, each in the
  language you choose for it.
- A history of every issuance, renewal and deployment, with its full log.
- Everything beyond the kernel is a module that can be switched off at run
  time or left out at build time.
- One small container image or one static binary, with SQLite.

Planned: Namecheap's paid certificates through its SSL Manager, with JEC
acting as its client, manual validation, hooks, and more CDNs, panels and
notification channels.

## Quick start

With Docker Compose:

```sh
mkdir jec && cd jec
curl -fsSLO https://raw.githubusercontent.com/XMZO/JustEnoughCerts/main/deploy/docker-compose.yml
mkdir -p data && sudo chown 65532:65532 data
docker compose up -d
docker logs jec 2>&1 | grep setup_token
```

Open `http://<host>:8080`, enter the setup token from the log and create
your account. Until an account exists, each start of the server logs a new
token, so only someone who can read the log can claim the server.

The container runs as user 65532:65532 and keeps everything, the database
included, in `./data`: back up that directory. To run as the owner of an
existing directory instead, set `user:` in `docker-compose.yml`.

Private keys and credentials are encrypted in the database with the key in
`./data/secret.key`, which JEC creates on first start. Without that file,
nothing encrypted can be read again, so keep it with the database backups,
or keep the key elsewhere and pass it as `JEC_SECRET_KEY`.

Forgot the password? Set a new one; this also signs the account out
everywhere:

```sh
docker exec -it jec /jec reset-password
```

### Behind a reverse proxy

Set `JEC_BASE_URL` to the URL people open, such as
`https://certs.example.com` or `https://example.com/certs/` for a sub-path.
With an `https` URL the session cookie is only sent over HTTPS.
Notifications link to the pages they are about only when it is set.

### Without Docker

Each release has static binaries for Linux and macOS, on amd64 and arm64.
There are no Windows builds; use Docker or WSL there.

```sh
tar -xzf jec_<version>_linux_amd64.tar.gz
./jec serve -data-dir /var/lib/jec
```

## Configuration

Each setting comes from, in increasing order of precedence: its default,
the config file, an environment variable and a command-line flag.

| Flag | Environment | `config.yaml` | Default |
|---|---|---|---|
| `-data-dir` | `JEC_DATA_DIR` | | `data` (`/data` in the image) |
| `-listen` | `JEC_LISTEN` | `listen` | `:8080` |
| `-base-url` | `JEC_BASE_URL` | `base_url` | the address the server sees |
| `-log-level` | `JEC_LOG_LEVEL` | `log.level` | `info` |
| `-log-format` | `JEC_LOG_FORMAT` | `log.format` | `text` (or `json`) |
| `-config` | `JEC_CONFIG` | | `<data-dir>/config.yaml`, if it exists |
| | `JEC_SECRET_KEY` | | `<data-dir>/secret.key` |

`JEC_SECRET_KEY` is base64 of 32 random bytes, as from
`openssl rand -base64 32`. It can only come from the environment, and JEC
refuses to start when it is not the key the database was encrypted with.

`jec <command> -h` lists the flags of a command.

## Verifying a release

The checksums and the images are signed with
[cosign](https://github.com/sigstore/cosign), keyless, by the release
workflow:

```sh
cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/XMZO/JustEnoughCerts/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c checksums.txt

cosign verify ghcr.io/xmzo/justenoughcerts:<version> \
  --certificate-identity-regexp '^https://github\.com/XMZO/JustEnoughCerts/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Each archive comes with an SPDX SBOM, and each image with an SBOM and
build provenance attestations.

## Development

You need Go 1.26 or newer, Node.js 24 and pnpm 10 (`corepack enable`).

```sh
make web         # build the web UI into web/dist, which the Go build embeds
make run         # run the server with ./data
make dev         # the web UI with hot reload on :5173, proxying /api to make run
make test        # Go tests with the race detector, then the web UI's
make lint        # golangci-lint, the type check and ESLint
make i18n-check  # the i18n gates
make gen         # after changing messages or the API: regenerate keys and types
make new-module id=acme category=source  # scaffold a module
```

Each target is a plain `go`, `pnpm` or `docker` command, so on Windows run
those directly; `make help` lists them all.

Some rules the checks enforce:

- Every text the UI shows lives in `i18n/locales/*.json`, one file per
  language; code refers to it by generated constants. After changing a
  locale file, run `go run ./tools/i18n fix`.
- Code, comments and scripts are in English.
- Modules talk only through the kernel: a module never imports another
  module, and the kernel never imports a module.

[PLAN.md](PLAN.md) holds the architecture, the module interfaces and the
roadmap, and [docs/design](docs/design) the design system and the page
designs.

## License

[MIT](LICENSE)
