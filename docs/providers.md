# Providers and how far they are tested

Every source, validation, target and notifier of JEC, and what it was
tested against; at the end, what the vendors ask for. The tests that need a server run in CI where the server
does; the others, live tests, run against throwaway containers when
their environment variables are set (see each module's `live_test.go`).

- **Real service**: tried with a real account of the service.
- **Real software**: the service's own software, run in a container.
- **Emulator**: software that speaks the service's API, such as moto for
  AWS or Pebble for an ACME CA.
- **Fake**: a stand-in of the API written from its documentation, or from
  a client that uses it; the requests JEC sends are checked, not what the
  service does with them.

Reports of what works with a real account, or does not, are welcome.

## Certificate sources

| Type | What | Tested against |
|---|---|---|
| `acme.account` | Any ACME CA, with External Account Binding | Emulator: Pebble, in CI |
| `namecheap.ssl_manager` | Namecheap SSL Manager (experimental) | Fake, after its client |
| `files.watch` | Certificates another client renews on this machine | The file system |
| `aliyun.fetch` | Certificates Alibaba Cloud's Certificate Management Service issued | Fake |
| `aliyun.apply` | Certificates applied for at Alibaba Cloud, its personal test ones by default | Fake |
| `tencentcloud.fetch` | Certificates Tencent Cloud's SSL Certificates issued | Fake, at the HTTP level through the SDK's client |
| `tencentcloud.free` | Tencent Cloud's free certificates, for domains in its DNSPod | Fake, at the HTTP level through the SDK's client |
| Upload | PEM, PFX/P12 | Unit tests |

## Domain validation

| Type | What | Tested against |
|---|---|---|
| `lego.*` | 221 DNS providers through lego | Fake, for the subprocess JEC runs them in; the providers are lego's |
| `lego.tencentcloud`, `lego.alidns`, `lego.cloudflare`, `lego.huaweicloud` adding any record | The TXT and CNAME records of vendors' validations, through JEC's own clients | Fake (Huawei Cloud's through its SDK) |
| `manual.dns` | DNS records added by hand | Fake resolver |
| `files.webroot` | A website directory on this machine | The file system |
| `ssh.webroot` | A website directory over SSH | An SSH server in the test |
| `wellknown.answer` | JEC answers /.well-known/ itself | Real HTTP |
| `esa.edge` | Alibaba Cloud ESA's edge, through Edge KV and an Edge Routine | Fake API; the routine's code runs in Node.js |
| `plugins.*` | Plugins | Real programs |

## Deploy targets

| Type | What | Tested against |
|---|---|---|
| `esa.site` | Alibaba Cloud ESA | Fake, with Alibaba Cloud's SDK |
| `aliyun.cdn`, `aliyun.dcdn` | Alibaba Cloud CDN and DCDN | Fake |
| `aliyun.cas` | Alibaba Cloud Certificate Management Service | Fake |
| `aliyun.oss` | Custom domains of Alibaba Cloud OSS buckets | Fake |
| `aliyun.clb` | Alibaba Cloud CLB load balancers | Fake |
| `aliyun.alb` | Alibaba Cloud ALB load balancers | Fake |
| `huaweicloud.cdn` | Huawei Cloud CDN | Fake |
| `cloudflare.custom` | Cloudflare custom certificates | Fake |
| `tencentcloud.ssl` | Tencent Cloud SSL Certificates, to CDN, EdgeOne, CLB and more | Fake |
| `qiniu.cdn` | Qiniu Cloud CDN | Fake |
| `upyun.cdn` | UPYUN CDN, through its console's interface (experimental) | Fake, after other tools' clients |
| `volcengine.cdn` | Volcengine CDN | Fake; the official SDK signs |
| `baiducloud.cdn` | Baidu AI Cloud CDN | Fake; signatures checked against the official SDK's |
| `aws.acm` | AWS Certificate Manager, for CloudFront and load balancers | Emulator: moto |
| `baota.site` | BaoTa (aaPanel) websites | Fake |
| `onepanel.certificate` | 1Panel certificates | Fake |
| `npm.certificate` | Nginx Proxy Manager | Real software: NPM 2.16 |
| `caddy.admin` | Caddy, through its admin API | Real software: Caddy 2.11 |
| `synology.dsm` | Synology DSM | Fake, after acme.sh's deploy hook |
| `proxmox.node` | Proxmox VE nodes | Fake |
| `kubernetes.secret` | Kubernetes TLS Secrets | Real software: kube-apiserver 1.36 with RBAC |
| `docker.container` | Docker containers | Real software: Docker 29 |
| `files.directory` | A directory on this machine, then a command | The file system |
| `ssh.server` | A server over SSH, then a command | An SSH server in the test |
| `webhook.deploy` | A webhook of yours | Real HTTP |
| `plugins.*` | Plugins | Real programs |

## Notifiers

| Type | What | Tested against |
|---|---|---|
| `email.smtp` | Email through SMTP | Real software: Mailpit's SMTP server |
| `ntfy.topic` | ntfy | Real software: ntfy |
| `gotify.app` | Gotify | Real software: Gotify |
| `telegram.bot` | Telegram | Fake |
| `webhook.notify` | A webhook of yours | Real HTTP |
| `dingtalk.robot`, `feishu.bot`, `wecom.robot` | DingTalk, Feishu (Lark) and WeCom group robots | Fake |
| `serverchan.sendkey` | ServerChan | Fake |
| `bark.device` | Bark | Fake |
| `discord.webhook` | Discord | Fake |
| `slack.webhook` | Slack, Mattermost, Rocket.Chat | Fake |
| `pushover.user` | Pushover | Fake |
| `hooks.script` | A script of yours, run by the built-in shell | Real scripts |
| `plugins.*` | Plugins | Real programs |

## What the vendors ask for

Some sources and targets work only on the vendor's terms. Where a CA
speaks ACME, with External Account Binding when it asks for an account,
prefer that, or upload its certificates by hand: the vendors' own
interfaces are optional modules.

- **Namecheap SSL Manager** (`namecheap.ssl_manager`, experimental): for
  certificates bought from Namecheap directly. Each is activated, or
  reissued, in Namecheap's panel with Server-side automation, which gives
  a one-time token valid for 24 hours. Single-domain certificates only,
  validated by a file on the website.
- **Namecheap's own API** (not in JEC yet): open to accounts with 20
  domains, a balance of $50, or $50 spent in the last two years, and only
  from IPv4 addresses on the account's allowlist.
- **Alibaba Cloud's personal test certificates** (`aliyun.apply`): DigiCert
  DV for three months, 20 a year, on the China site only; claim them in
  the console of the Certificate Management Service first. Other DV
  resource plans work the same way.
- **Tencent Cloud's free certificates** (`tencentcloud.free`): TrustAsia DV
  for three months, one domain each and no wildcards, for domains in the
  DNSPod of the same account, which validates them by itself; an account
  gets a limited number of them.
- **Alibaba Cloud ESA** (`esa.site`): the free plan holds two custom
  certificates a site, so JEC removes the one it replaces after deploying.
  Answers at the edge (`esa.edge`) need a plan with Functions and Pages,
  and Edge KV.
- **Cloudflare custom certificates** (`cloudflare.custom`): Business and
  Enterprise plans only.
- **UPYUN** (`upyun.cdn`, experimental): signs in to the console with the
  account's password, which an account with two-step verification cannot
  do.
- **Synology DSM** (`synology.dsm`): an administrator's account; with
  two-step verification, its secret, from which JEC makes the codes.
