# Providers and how far they are tested

Every source, validation, target and notifier of JEC, and what it was
tested against. The tests that need a server run in CI where the server
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
| `volcengine.cdn` | Volcengine CDN | Fake; the official SDK signs |
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
