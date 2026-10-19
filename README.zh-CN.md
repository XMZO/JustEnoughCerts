# Just Enough Certs

*Just enough certs to keep your site green.*

[English](README.md)

在线文档：<https://xmzo.github.io/JustEnoughCerts/zh-CN/>

Just Enough Certs（JEC）是一款自托管的 SSL/TLS 证书管理面板，负责从各类来源获取证书、自动完成域名验证、将证书部署到 CDN 与服务器，并在到期前自动续期。项目名称与视觉风格致敬 Minecraft 模组 JEI。

> [!WARNING]
> JEC 仍处于早期开发阶段，尚未发布正式版。测试版为 0.1.0-alpha.x（镜像标签 `beta`），已实现下文所列功能，但阿里云 ESA 部署、ESA 边缘应答与 Namecheap SSL Manager 尚未经真实账号完整验证。路线图见 [PLAN.md](PLAN.md)。

## 功能

### 证书来源

- 任意 ACME 证书颁发机构，支持 External Account Binding（EAB）。
- 手动上传 PEM 或 PFX/PKCS #12 文件。
- 只读导入本机 certbot、acme.sh 续期的证书及私钥。
- 取回阿里云数字证书管理服务、腾讯云 SSL 证书服务签发的证书。
- 以 JEC 生成的私钥向阿里云申请证书（如免费的个人测试证书）；为托管于 DNSPod 的域名申请腾讯云免费证书。
- 作为 Namecheap SSL Manager 的客户端，自动获取在 Namecheap 购买的付费证书（如 PositiveSSL）。该功能为实验性功能，默认关闭，尚未经真实账号验证。

### 域名验证

- DNS 验证：支持 [lego](https://github.com/go-acme/lego) 所支持的 221 家 DNS 服务商，其他服务商可手动添加记录。证书厂商（如 Namecheap）要求的任意 TXT、CNAME 记录可在腾讯云 DNSPod、阿里云 DNS、Cloudflare 与华为云 DNS 自动添加。ACME 支持 `_acme-challenge` 的 CNAME 委派。
- HTTP 验证：将验证文件写入网站目录（本机或经 SSH）；或由网站的反向代理、CDN 将 `/.well-known/` 请求转发至 JEC，由 JEC 直接应答；阿里云 ESA 站点还可由 JEC 部署的边缘函数从边缘 KV 应答，无需回源。

### 部署目标

| 类别 | 目标 |
|---|---|
| 阿里云 | ESA（部署后删除被替换的证书）、CDN、全站加速（DCDN）、数字证书管理服务、OSS 自定义域名、传统型负载均衡（CLB）、应用型负载均衡（ALB） |
| 其他云服务 | 腾讯云 SSL 证书服务（更新 CDN、EdgeOne、CLB 等资源）、华为云 CDN、七牛云 CDN、火山引擎 CDN、百度智能云 CDN、又拍云 CDN（实验性）、Cloudflare 自定义证书、AWS Certificate Manager（供 CloudFront 与负载均衡使用） |
| 面板与服务器软件 | 宝塔面板（aaPanel）、1Panel、Nginx Proxy Manager、群晖 DSM、Proxmox VE、Caddy（经管理接口） |
| 文件与容器 | 本地目录、SSH 服务器（均可在写入后执行命令，如重载 nginx）、Docker 容器（写入后发送信号或重启）、Kubernetes TLS Secret |
| 自定义 | Webhook（JEC 的 JSON 格式或自定义模板）、插件 |

写入文件的目标可自定义文件名、权限与属主，并可额外生成合并文件（私钥加完整证书链，供 HAProxy 使用）与 PKCS #12 文件。

### 续期

- 证书进入续期窗口后，于每日检查时间开始续期（默认 02:00，时区可设置）；剩余 7 天及以内时立即续期。
- 支持 ACME 续期信息（ARI，RFC 9773），按证书颁发机构的建议提前续期，例如证书即将被吊销时。
- 签发与部署失败后按指数退避自动重试，重试次数可设置。

### 通知

支持 Telegram、电子邮件（SMTP）、Webhook、钉钉、飞书（Lark）、企业微信、Server酱、Bark、ntfy、Gotify、Discord、Slack（兼容 Mattermost、Rocket.Chat）与 Pushover，每个渠道可单独设置消息语言。其他系统可通过自定义格式的 Webhook 或脚本（由内置 shell 执行）对接。连接境外服务的渠道可单独设置代理。

### 运维与安全

- 签发记录：保存每次签发、续期与部署的完整日志。
- 线上巡检：模拟浏览器连接各域名；续期后若线上仍为旧证书（例如 CDN 未生效），即发出通知。
- 凭据：云平台密钥与 SSH 私钥集中保存，供多个证书来源、验证方式与部署目标共用；“凭据”页显示各凭据的使用者与最近使用时间。
- 配置导入导出：以 YAML 文件导出凭据、服务商、证书、续期默认值与时区，便于纳入 Git 管理或迁移至另一实例；导入前预览全部变更，且不删除任何已有配置。
- 登录与权限：密码登录，可启用 TOTP 两步验证，或经 OpenID Connect 身份提供方（如 Authelia、Authentik、Keycloak、Google）单点登录；提供只读或读写的 API 令牌；审计日志记录经面板执行的操作、操作账号与来源地址；默认发送严格的安全响应头。
- 渐进式网页应用（PWA）：可添加到手机桌面。

### 架构

- 内核以外的功能均以模块实现，可在“模块”页启用或停用，也可在编译时排除。
- 插件可用任意语言编写，用于新增证书来源、部署目标、通知渠道与验证方式，无需重新编译 JEC，参见 [docs/plugins.zh-CN.md](docs/plugins.zh-CN.md)。
- 以单个静态二进制文件或小体积容器镜像发布，数据存储于 SQLite。

计划中：更多厂商的证书接口，例如 Namecheap 官方 API。

各服务商的测试情况（真实账号、真实软件、模拟器或替身）与厂商使用条件，参见 [docs/providers.zh-CN.md](docs/providers.zh-CN.md)；使用真实账号验证 ESA 部署、ESA 边缘应答与 Namecheap SSL Manager 的步骤，参见 [docs/trying.zh-CN.md](docs/trying.zh-CN.md)。

## 安装

### Docker Compose

```sh
mkdir jec && cd jec
curl -fsSLO https://raw.githubusercontent.com/XMZO/JustEnoughCerts/main/deploy/docker-compose.yml
mkdir -p data && sudo chown 65532:65532 data
docker compose up -d
docker logs jec 2>&1 | grep setup_token
```

在浏览器中打开 `http://<主机>:8080`，填写日志中的初始化令牌并创建管理员账号。账号创建之前，服务每次启动都会在日志中生成新的令牌，因此只有能够读取日志的人才能完成初始化。

### 二进制文件

每个版本提供 Linux 与 macOS 的静态二进制文件（amd64 与 arm64）。不提供 Windows 版本，Windows 用户请使用 Docker 或 WSL。

```sh
tar -xzf jec_<版本>_linux_amd64.tar.gz
./jec serve -data-dir /var/lib/jec
```

## 使用

### 数据与备份

- 容器以 65532:65532 用户运行，全部数据（含数据库）存放在 `./data`。如需以现有目录的属主运行，请在 `docker-compose.yml` 中设置 `user:`。
- 备份 `./data` 目录，或在设置页下载数据库备份。
- 恢复：在设置页选择“恢复”并上传备份后，JEC 停止运行，由 Docker 重新启动并载入备份，原数据库重命名后保留在 `./data`。也可手动恢复：停止 JEC，将备份文件放入 `./data` 并重命名为 `jec.db`。
- 私钥与凭据在数据库中加密存储，主密钥为首次启动时生成的 `./data/secret.key`。缺少该文件将无法解密已保存的数据，因此须与数据库一同备份；也可将主密钥另行保管，通过 `JEC_SECRET_KEY` 传入。

### 账号

密码与两步验证在设置页管理。如遗失密码或验证器，可在服务器上重置密码；该操作同时关闭两步验证，并注销该账号的全部会话：

```sh
docker exec -it jec /jec reset-password
```

### 反向代理

- `JEC_BASE_URL`：设为用户访问的地址，例如 `https://certs.example.com`，或子路径 `https://example.com/certs/`。使用 `https` 地址时，会话 Cookie 仅经 HTTPS 发送；设置后，通知中会附带对应页面的链接。
- `JEC_TRUSTED_PROXIES`：设为反向代理的地址或网段，例如 `127.0.0.1` 或容器网段 `172.16.0.0/12`。JEC 据此从 `X-Forwarded-For` 读取客户端地址，登录失败时锁定的是该客户端，而非代理后的全部用户。

### API

面板的全部功能均通过 API 提供，接口说明位于 `/api/v1/openapi.json`。供脚本或其他程序使用时，在“设置 → API 令牌”中创建令牌（只读或读写），以 Bearer 令牌方式发送：

```sh
curl -H "Authorization: Bearer jec_..." https://certs.example.com/api/v1/certs/certificates
```

API 令牌不能修改密码、两步验证与令牌本身。

### 以 YAML 导出和导入配置

在“设置 → 数据和备份 → 配置文件”中，可将凭据、服务商、证书、续期默认值与时区导出为 YAML 文件，或导入此类文件，用于将配置纳入 Git 管理或迁移至另一实例。该文件与服务器的运行配置 `config.yaml` 无关。证书按名称引用服务商：

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
time_zone: Asia/Shanghai
renewal:
  renew_before_days: 30
  check_time: "02:00"
  key_type: rsa-2048
  retries: 7
certificates:
  - domains: [example.com, "*.example.com"]
    source: Let's Encrypt
    solver: DNSPod
    targets: [ESA]
```

导入规则：

- 新增 JEC 中不存在的条目，更新已存在的条目：服务商按种类与名称匹配，证书按域名匹配。
- 不删除任何条目。文件中未写明的密钥保持不变（新增的服务商须在文件中提供密钥），已有证书的来源不会变更。
- 导入前列出全部变更；任一条目有错误时，不做任何修改。
- 导出默认不含密钥；导出密钥须输入密码。

对应接口：`GET /api/v1/config`（导出）、`POST /api/v1/config/check`（预览变更）、`POST /api/v1/config/import`（导入）。

## 配置参考

各项设置的优先级由低到高依次为：默认值、配置文件、环境变量、命令行参数。

| 参数 | 环境变量 | `config.yaml` | 默认值 |
|---|---|---|---|
| `-data-dir` | `JEC_DATA_DIR` | | `data`（镜像中为 `/data`） |
| `-listen` | `JEC_LISTEN` | `listen` | `:8080` |
| `-base-url` | `JEC_BASE_URL` | `base_url` | 服务端看到的地址 |
| `-hsts` | `JEC_HSTS` | `hsts` | `false` |
| `-trusted-proxies` | `JEC_TRUSTED_PROXIES` | `trusted_proxies` | 无 |
| `-dns-resolvers` | `JEC_DNS_RESOLVERS` | `dns_resolvers` | 系统设置 |
| `-log-level` | `JEC_LOG_LEVEL` | `log.level` | `info` |
| `-log-format` | `JEC_LOG_FORMAT` | `log.format` | `text`（或 `json`） |
| `-config` | `JEC_CONFIG` | | `<data-dir>/config.yaml`（如存在） |
| | `JEC_SECRET_KEY` | | `<data-dir>/secret.key` |

- `JEC_HSTS`：仅当面板始终经 HTTPS 访问时设为 `true`；启用后，浏览器在一年内拒绝以 HTTP 访问该地址。
- `JEC_DNS_RESOLVERS`：DNS 递归解析服务器列表，例如 `223.5.5.5,119.29.29.29`，用于在查询权威 DNS 之前确定验证记录所在的域。仅在本机解析结果有误时（例如内外网解析不一致）需要设置。
- `JEC_SECRET_KEY`：32 个随机字节的 base64 编码，可用 `openssl rand -base64 32` 生成。仅可通过环境变量传入；与加密数据库时所用的密钥不一致时，JEC 拒绝启动。

`jec <命令> -h` 可列出命令的全部参数。

## 校验发布文件

校验和文件与容器镜像由发布流水线使用 [cosign](https://github.com/sigstore/cosign) 进行无密钥签名，校验方法如下：

```sh
cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/XMZO/JustEnoughCerts/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c checksums.txt

cosign verify ghcr.io/xmzo/justenoughcerts:<版本> \
  --certificate-identity-regexp '^https://github\.com/XMZO/JustEnoughCerts/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

每个压缩包附带 SPDX 格式的 SBOM，每个镜像附带 SBOM 与构建来源证明。

## 开发

环境要求：Go 1.26 或更高版本、Node.js 24、pnpm 10（`corepack enable`）。

```sh
make web         # 构建前端至 web/dist，Go 编译时将其嵌入程序
make run         # 以 ./data 为数据目录运行服务
make dev         # 前端热更新开发服务器（:5173），/api 转发至 make run
make test        # Go 测试（含竞态检测）与前端测试
make lint        # golangci-lint、类型检查与 ESLint
make i18n-check  # i18n 检查
make gen         # 修改界面文字或 API 后，重新生成 key 常量与类型
make new-module id=acme category=source  # 生成新模块
```

各目标均为单条 `go`、`pnpm` 或 `docker` 命令，Windows 上可直接运行对应命令；`make help` 列出全部目标。

以下规则由自动检查强制执行：

- 界面文字仅写在 `i18n/locales/*.json`（每种语言一个文件），代码通过生成的常量引用；修改语言文件后须运行 `go run ./tools/i18n fix`。
- 代码、注释与脚本使用英文。
- 模块之间仅通过内核通信：模块不得引用其他模块，内核不得引用模块。

架构、模块接口与路线图见 [PLAN.md](PLAN.md)，设计系统与页面设计稿见 [docs/design](docs/design)。翻译 JEC 请参见 [docs/translating.zh-CN.md](docs/translating.zh-CN.md)。

文档站由本 README 与 `docs/` 中的指南组成，提供中英文两种语言：`docs/site/pages.mjs` 负责组织页面，VitePress 负责构建。本地预览：执行 `pnpm -C docs/site install` 后运行 `pnpm -C docs/site dev`，然后访问 `http://localhost:5173/JustEnoughCerts/`。main 分支更新后，docs 工作流自动发布文档站。

## 许可证

[MIT](LICENSE)
