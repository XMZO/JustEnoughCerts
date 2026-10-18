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
> test releases (0.1.0-alpha.x, image tag `beta` in place of `latest` in
> `docker-compose.yml`) and the main branch do what the list below says
> works, but have not been tried end to end against a real Alibaba Cloud
> ESA site. See the roadmap in [PLAN.md](PLAN.md) (in Chinese).

## Features

Working on the main branch:

- Certificates from any ACME CA, with External Account Binding, or
  uploaded by hand, as PEM or as a PFX/P12 file, or taken in with their
  keys from where certbot or acme.sh renews them on the same machine, or
  from Alibaba Cloud's and Tencent Cloud's certificate services, which
  issued them; or applied for at Alibaba Cloud with a key of JEC's own,
  such as its free personal test certificates.
- Namecheap's paid certificates, such as PositiveSSL, with JEC acting as
  the client of Namecheap's SSL Manager. Experimental and off until you
  switch it on; not yet tried against a real account.
- Domain validation through DNS records at any of the 221 providers that
  [lego](https://github.com/go-acme/lego) supports, or added by hand at
  any other (the TXT and CNAME records a vendor such as Namecheap may ask
  for go in by themselves at Tencent Cloud DNSPod, Alibaba Cloud DNS,
  Cloudflare and Huawei Cloud DNS), or over HTTP: files in
  a website's directory, local or over SSH, answers from JEC itself once
  the website's proxy or CDN sends `/.well-known/` on to it, or, for
  websites on Alibaba Cloud ESA, answers at ESA's edge from Edge KV,
  through an Edge Routine JEC deploys, without the origin.
- Deployment of each new certificate to Alibaba Cloud ESA (removing the one
  it replaces), to Alibaba Cloud CDN and DCDN, its Certificate
  Management Service, custom domains of OSS buckets, and CLB and ALB
  load balancers, to Huawei Cloud's CDN, to
  Qiniu Cloud's, Volcengine's and Baidu AI Cloud's CDNs, to custom
  certificates of Cloudflare,
  through Tencent Cloud's SSL Certificates to its CDN, EdgeOne, CLB and
  more, and through AWS Certificate Manager to CloudFront and load
  balancers; to websites of BaoTa (aaPanel) and 1Panel, to Nginx Proxy
  Manager, Synology DSM and Proxmox VE nodes; into Caddy through its admin
  API; to local directories and to
  servers over SSH, each with a command afterwards, such as reloading
  nginx; into Docker containers, which are then signaled to reload, or
  restarted; into Kubernetes TLS Secrets; and to webhooks, as JSON or in
  a format of your own.
- Renewal once a certificate enters its renewal window, at the time of
  day set on the Settings page (02:00 by default), or at once with a week
  or less left, or earlier when the CA suggests so through ACME Renewal
  Information, as before a revocation; failures are retried with a
  growing delay.
- Notifications through Telegram, email (SMTP), webhooks, the group
  robots of DingTalk, Feishu (Lark) and WeCom, ServerChan, Bark, ntfy,
  Gotify, Discord, Slack (and Mattermost or Rocket.Chat) and Pushover,
  each in the language you choose for it; anything else through a
  webhook in a format of your own, or a script run by JEC's built-in
  shell.
- A history of every issuance, renewal and deployment, with its full log.
- Online checks: JEC connects to each domain as a browser does and tells
  you when one still serves the old certificate after a renewal, as when a
  CDN did not take the new one.
- Everything beyond the kernel is a module, switched on and off on the
  Modules page or left out at build time.
- Plugins, programs in any language, add deploy targets, notification
  channels and ways to validate domains without rebuilding JEC
  ([docs/plugins.md](docs/plugins.md)).
- The configuration as a YAML file: the providers, the certificates and
  the renewal defaults, to keep in Git or to take to another JEC. An
  import shows what it would add and change before it does, and deletes
  nothing.
- Sign-in with a password and, if you like, the codes of an authenticator
  app (two-factor authentication); API tokens for scripts; an audit log of
  what was done through the panel, by whom and from where; strict security
  headers.
- One small container image or one static binary, with SQLite.

Planned: the free certificates of Chinese cloud platforms as sources, and more CDNs and panels.

How far each provider is tested, against a real account, the real
software, an emulator or a fake: [docs/providers.md](docs/providers.md).

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
included, in `./data`: back up that directory, or download a backup of the
database on the Settings page, where Restore takes one back: JEC then
stops and Docker starts it again with it, the database before it kept in
`./data`. By hand, stop JEC and put the backup in `./data` as `jec.db`.
To run as the owner of an existing directory
instead, set `user:` in `docker-compose.yml`.

Private keys and credentials are encrypted in the database with the key in
`./data/secret.key`, which JEC creates on first start. Without that file,
nothing encrypted can be read again, so keep it with the database backups,
or keep the key elsewhere and pass it as `JEC_SECRET_KEY`.

To change the password, or turn on two-factor authentication with an
authenticator app, use the Settings page. Forgot the password, or lost the
app? Set a new password on the server; this also turns two-factor
authentication off and signs the account out everywhere:

```sh
docker exec -it jec /jec reset-password
```

### Behind a reverse proxy

Set `JEC_BASE_URL` to the URL people open, such as
`https://certs.example.com` or `https://example.com/certs/` for a sub-path.
With an `https` URL the session cookie is only sent over HTTPS.
Notifications link to the pages they are about only when it is set.
Set `JEC_TRUSTED_PROXIES` to the address of the proxy too, such as
`127.0.0.1` or the network of its containers, `172.16.0.0/12`: JEC then
takes the client from `X-Forwarded-For`, and locks out after failed
sign-ins the client rather than everyone behind the proxy.

### Without Docker

Each release has static binaries for Linux and macOS, on amd64 and arm64.
There are no Windows builds; use Docker or WSL there.

```sh
tar -xzf jec_<version>_linux_amd64.tar.gz
./jec serve -data-dir /var/lib/jec
```

### The API

Everything the panel does goes through its API, described at
`/api/v1/openapi.json`. For scripts and other programs, make an API token
in Settings → API tokens, read-only or read and write, and send it as a
bearer token:

```sh
curl -H "Authorization: Bearer jec_..." https://certs.example.com/api/v1/certs/certificates
```

No token changes the password, two-factor authentication or the tokens.

### The configuration as YAML

Settings → Data and backups → Configuration file exports the providers,
the certificates and the renewal defaults as YAML, and imports such a
file, to keep the configuration in Git or to take it to another JEC.
This is not `config.yaml`, which holds how the server runs. Certificates
name their providers by name:

```yaml
version: 1
providers:
  - name: Let's Encrypt
    type: acme.account
    config:
      ca: letsencrypt
      email: me@example.com
  - name: DNSPod
    type: lego.tencentcloud
    secrets:
      TENCENTCLOUD_SECRET_ID: AKID...
      TENCENTCLOUD_SECRET_KEY: ...
  - name: ESA
    type: esa.site
    config:
      region: cn-hangzhou
      access_key_id: LTAI...
      site: example.com
renewal:
  renew_before_days: 30
  check_time: "02:00"
  key_type: rsa-2048
certificates:
  - domains: [example.com, "*.example.com"]
    source: Let's Encrypt
    solver: DNSPod
    targets: [ESA]
```

An import adds what JEC lacks and changes what it has: providers found
by their kind and name, certificates by their domains. It deletes
nothing; secrets the file leaves out stay as they are (a provider it adds
needs its own), and a certificate keeps its source. JEC first lists what the file would change, and changes
nothing while an entry has an error. An export leaves the secrets out
unless you ask for them, with your password. The same goes through the
API: `GET /api/v1/config`, `POST /api/v1/config/check` to see what a file
would change, and `POST /api/v1/config/import`.

## Configuration

Each setting comes from, in increasing order of precedence: its default,
the config file, an environment variable and a command-line flag.

| Flag | Environment | `config.yaml` | Default |
|---|---|---|---|
| `-data-dir` | `JEC_DATA_DIR` | | `data` (`/data` in the image) |
| `-listen` | `JEC_LISTEN` | `listen` | `:8080` |
| `-base-url` | `JEC_BASE_URL` | `base_url` | the address the server sees |
| `-hsts` | `JEC_HSTS` | `hsts` | `false` |
| `-trusted-proxies` | `JEC_TRUSTED_PROXIES` | `trusted_proxies` | none |
| `-log-level` | `JEC_LOG_LEVEL` | `log.level` | `info` |
| `-log-format` | `JEC_LOG_FORMAT` | `log.format` | `text` (or `json`) |
| `-config` | `JEC_CONFIG` | | `<data-dir>/config.yaml`, if it exists |
| | `JEC_SECRET_KEY` | | `<data-dir>/secret.key` |

Set `JEC_HSTS` to `true` only when the panel is reached over HTTPS alone:
browsers then refuse plain HTTP to it for a year.

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
