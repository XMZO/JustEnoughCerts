# Providers and test coverage

[中文](providers.zh-CN.md)

This page lists every certificate source, validation method, deploy target
and notification channel of JEC with the way it is tested, followed by the
requirements of the vendors. Tests that need a server run in CI wherever
the server can; the others are live tests, which run against throwaway
containers when their environment variables are set (see each module's
`live_test.go`).

The tests fall into four kinds:

| Kind | Meaning |
|---|---|
| Real service | Tested with a real account of the service |
| Real software | Tested against the service's own software, run in a container |
| Emulator | Tested against software that implements the service's API, such as moto for AWS or Pebble for an ACME CA |
| Fake | A stand-in for the API, written from its documentation or from an existing client; it checks the requests JEC sends, not how the service handles them |

Types tested only with a fake are tagged "Untested" in the web UI: they
may not work with the real service. Reports of results with real
accounts are welcome.

## Certificate sources

| Type | Description | Tested with |
|---|---|---|
| `acme.account` | Any ACME CA, with External Account Binding; renewal information (ARI), revocation, profiles and certificates of IP addresses | Emulator: Pebble, in CI; ZeroSSL's EAB fetched by itself only with a fake |
| `namecheap.ssl_manager` | Namecheap SSL Manager (experimental) | Real service; fake, written after its official client |
| `privateca.ca` | Private CA: certificates signed with a CA of your own | No outside service; chains checked by Go's verifier |
| `files.watch` | Certificates another client renews on this machine | The file system |
| `aliyun.fetch` | Certificates issued by Alibaba Cloud Certificate Management Service | Fake |
| `aliyun.apply` | Certificates applied for at Alibaba Cloud, personal test certificates by default | Fake |
| `tencentcloud.fetch` | Certificates issued by Tencent Cloud SSL Certificates | Fake, at the HTTP level of the SDK's client |
| `tencentcloud.free` | Tencent Cloud free certificates, for domains in the same account's DNSPod | Fake, at the HTTP level of the SDK's client |
| Upload | PEM, PFX/P12 | Unit tests |

## Domain validation

| Type | Description | Tested with |
|---|---|---|
| `lego.*` | The 221 DNS providers of lego | Fake, for the subprocess JEC runs them in; the providers themselves are lego's |
| `lego.tencentcloud`, `lego.alidns`, `lego.cloudflare`, `lego.huaweicloud` (any record, test, zone check) | The TXT and CNAME records of vendors' validations, the list of the account's domains, and whether the account holds a domain, all through JEC's own clients | Fake (Huawei Cloud's through its SDK) |
| `manual.dns` | DNS records added manually | Fake resolver |
| `files.webroot` | A website directory on this machine | The file system |
| `ssh.webroot` | A website directory over SSH | An SSH server in the test |
| `wellknown.answer` | JEC answering `/.well-known/` requests itself | Real HTTP |
| `esa.edge` | Answers at Alibaba Cloud ESA's edge, through Edge KV and an Edge Routine | Fake API; the routine's code runs in Node.js |
| `cloudflare.edge` | Answers at Cloudflare's edge, through Workers KV and a Worker | Real service; fake API |
| `plugins.*` | Plugins | Real programs |

## Deploy targets

| Type | Description | Tested with |
|---|---|---|
| `esa.site` | Alibaba Cloud ESA | Real service; fake, with Alibaba Cloud's SDK |
| `aliyun.cdn`, `aliyun.dcdn` | Alibaba Cloud CDN and DCDN | Fake |
| `aliyun.cas` | Alibaba Cloud Certificate Management Service | Fake |
| `aliyun.oss` | Custom domains of Alibaba Cloud OSS buckets | Fake |
| `aliyun.clb` | Alibaba Cloud CLB load balancers | Fake |
| `aliyun.alb` | Alibaba Cloud ALB load balancers | Fake |
| `huaweicloud.cdn` | Huawei Cloud CDN | Fake |
| `huaweicloud.elb` | Huawei Cloud ELB certificates, updated in place, and their listeners | Fake |
| `cloudflare.custom` | Cloudflare custom certificates | Fake |
| `tencentcloud.ssl` | Tencent Cloud SSL Certificates, updating CDN, EdgeOne, CLB and other resources | Fake |
| `tencentcloud.cdn` | Tencent Cloud CDN, deployed straight to its domains | Fake |
| `tencentcloud.teo` | Tencent Cloud EdgeOne, as the certificate of a zone's domains | Fake |
| `qiniu.cdn` | Qiniu Cloud CDN | Fake |
| `upyun.cdn` | UPYUN CDN, through its console's interface (experimental) | Fake, written after other tools' clients |
| `volcengine.cdn` | Volcengine CDN | Fake; signatures made by the official SDK |
| `baiducloud.cdn` | Baidu AI Cloud CDN | Fake; signatures checked against the official SDK's |
| `aws.acm` | AWS Certificate Manager, for CloudFront and load balancers | Emulator: moto |
| `aws.cloudfront` | AWS CloudFront distributions, through ACM in us-east-1 | Fake |
| `gcore.cdn` | Gcore CDN resources | Fake |
| `cpanel.account` | cPanel websites of an account, through UAPI | Fake |
| `baota.site` | BaoTa (aaPanel) websites | Real software: BaoTa 11.0, its official LNMP image |
| `onepanel.certificate` | 1Panel certificates of websites | Real software: 1Panel v1 and v2, in a community container image |
| `onepanel.panel` | 1Panel's own HTTPS certificate, turning its HTTPS on | Real software: 1Panel v1 and v2, in a community container image |
| `safeline.certificate` | SafeLine WAF certificates, replaced in place for their sites | Real software: SafeLine 9.4 |
| `npm.certificate` | Nginx Proxy Manager | Real software: NPM 2.16 |
| `caddy.admin` | Caddy, through its admin API | Real software: Caddy 2.11 |
| `apisix.ssl` | Apache APISIX SSL objects, through its Admin API | Real software: APISIX 3.19 with etcd |
| `kong.certificate` | Kong Gateway certificates and their SNIs, through its Admin API | Real software: Kong 3.9 with PostgreSQL |
| `synology.dsm` | Synology DSM | Fake, written after acme.sh's deploy hook |
| `proxmox.node` | Proxmox VE nodes | Fake |
| `portainer.ssl` | Portainer's own HTTPS certificate | Real software: Portainer CE, LTS image |
| `kubernetes.secret` | Kubernetes TLS Secrets | Real software: kube-apiserver 1.36 with RBAC |
| `docker.container` | Docker containers | Real software: Docker 29 |
| `files.directory` | A directory on this machine, optionally followed by a command | The file system |
| `ssh.server` | A server over SSH, optionally followed by a command | An SSH server in the test |
| `webhook.deploy` | A custom webhook | Real HTTP |
| `plugins.*` | Plugins | Real programs |

## Notification channels

| Type | Description | Tested with |
|---|---|---|
| `email.smtp` | Email through SMTP | Real software: Mailpit's SMTP server |
| `ntfy.topic` | ntfy | Real software: ntfy |
| `gotify.app` | Gotify | Real software: Gotify |
| `telegram.bot` | Telegram | Fake |
| `webhook.notify` | A custom webhook | Real HTTP |
| `dingtalk.robot`, `feishu.bot`, `wecom.robot` | DingTalk, Feishu (Lark) and WeCom group robots | Fake |
| `serverchan.sendkey` | ServerChan | Fake |
| `bark.device` | Bark | Fake |
| `discord.webhook` | Discord | Fake |
| `slack.webhook` | Slack, Mattermost, Rocket.Chat | Fake |
| `pushover.user` | Pushover | Fake |
| `teams.workflow` | Microsoft Teams (Workflows) | Fake |
| `googlechat.webhook` | Google Chat | Fake |
| `matrix.room` | Matrix | Real software: Conduit |
| `pushplus.token` | PushPlus | Fake |
| `onebot.qq` | QQ (OneBot v11) | Fake |
| `hooks.script` | A custom script, run by the built-in shell | Real scripts |
| `plugins.*` | Plugins | Real programs |

## Other outside services

These features are no provider types, but reach outside services too:

| Feature | Service | Tested with |
|---|---|---|
| [Offsite backups](../operations/backup.md#offsite-backups) | S3 compatible storage, WebDAV | Real software: the S3 interface of SeaweedFS, the WebDAV server of rclone; AWS S3, Cloudflare R2, Alibaba Cloud OSS, Tencent Cloud COS, Jianguoyun and others untested, as the UI says |
| [Certificate transparency](../usage/sites.md#certificate-transparency) | Cert Spotter | Real service, without an API key |
| [Domain registrations](../usage/sites.md#domain-registrations) | RDAP and WHOIS of IANA and the registries | Real services |
| Revocation checks, completing chains, missing intermediates | OCSP, CRLs and issuer addresses (AIA) in certificates | Real services: certificates of Let's Encrypt, Sectigo and others, and badssl.com's site without intermediates |
| Try on DNS providers | Each DNS provider, through lego | Real software: RFC 2136 dynamic updates of BIND; other providers take the path of an issuance |
| [Single sign-on](../operations/security.md#single-sign-on) | OpenID Connect providers | Real software: Dex |
| The account's domains offered when adding a certificate | Tencent Cloud DNSPod, Alibaba Cloud DNS, Cloudflare, Huawei Cloud DNS | Stand-ins; marked untested in the UI |
| Checking for updates by itself | GitHub's list of releases | Real service |

## Vendor requirements

The permissions each cloud's keys need are listed in
[Access keys and least privilege](permissions.md).

Some sources and targets are available only on the vendor's terms. Where a
CA supports ACME, with External Account Binding when it requires an
account, prefer ACME or upload its certificates manually: the vendors' own
interfaces are optional modules.

- **Namecheap SSL Manager** (`namecheap.ssl_manager`, experimental): for
  certificates bought directly from Namecheap. Each certificate must be
  activated or reissued in the Namecheap dashboard with Server-side
  automation, which provides a one-time token valid for 24 hours.
  Single-domain certificates only, which also cover `www`; Namecheap
  validates the domain and `www` by files on the website.
- **Namecheap's own API** (not planned; use SSL Manager for Namecheap's
  certificates): available to accounts
  with 20 domains, a balance of $50 or $50 spent in the last two years,
  and only from IPv4 addresses on the account's allowlist.
- **Alibaba Cloud personal test certificates** (`aliyun.apply`): DigiCert
  DV, valid for three months, 20 a year, on the China site only; the quota
  must first be claimed in the console of Certificate Management Service.
  Other DV resource plans work the same way.
- **Tencent Cloud free certificates** (`tencentcloud.free`): TrustAsia DV,
  valid for three months, one domain each and no wildcards; the domain
  must be hosted in the DNSPod of the same account, and Tencent Cloud
  completes the validation itself; each account has a quota.
- **Alibaba Cloud ESA** (`esa.site`): the free plan holds at most two
  custom certificates per site, so JEC removes the replaced certificate
  after deploying a new one. Edge answers (`esa.edge`) require a plan with
  Functions and Pages, and Edge KV.
- **Cloudflare custom certificates** (`cloudflare.custom`): Business and
  Enterprise plans only. Edge answers (`cloudflare.edge`) work on every
  plan, within the daily quotas of Workers and Workers KV.
- **UPYUN** (`upyun.cdn`, experimental): signs in to the console with the
  account's password, so accounts with two-step verification are not
  supported.
- **Synology DSM** (`synology.dsm`): requires an administrator account;
  with two-step verification enabled, its secret is required, from which
  JEC generates the codes.
