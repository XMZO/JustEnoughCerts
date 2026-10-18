# Just Enough Certs

*Just enough certs to keep your site green.*

[English](README.md)

Just Enough Certs（JEC）是自托管的 SSL/TLS 证书管理面板：从你购买或签发证书的地方获取证书，自动完成域名验证，部署到 CDN 和服务器，到期前自动续期。名字和视觉致敬 Minecraft 的 JEI 模组。

> [!WARNING]
> JEC 还在早期开发中。已发布的 0.0.x 版本只是骨架：服务端、初始化向导和登录，支持中文和英文。测试版（0.1.0-alpha.x，把 `docker-compose.yml` 里镜像的 `latest` 换成 `beta`）和 main 分支已经能做到下面列出的功能，但还没有对真实的阿里云 ESA 站点完整跑通一次。路线图见 [PLAN.md](PLAN.md)。

## 功能

main 分支上已经可用：

- 从任意 ACME CA（支持 External Account Binding）获取证书，或者手动上传（PEM，或者 PFX/P12 文件），或者连同私钥取进 certbot、acme.sh 在本机续期的证书，以及阿里云、腾讯云的证书服务签发的证书；也能用 JEC 自己的私钥向阿里云申请证书（比如免费的个人测试证书）。
- 由 JEC 扮演 Namecheap SSL Manager 的客户端，自动取得在 Namecheap 买的付费证书（如 PositiveSSL）。实验性功能，默认关闭；还没有用真实账号试过。
- 域名验证：在 [lego](https://github.com/go-acme/lego) 支持的 221 家 DNS 服务商添加记录（其他服务商可以手动添加；Namecheap 等厂商要的任意 TXT、CNAME 记录，能在腾讯云 DNSPod、阿里云 DNS、Cloudflare、华为云 DNS 自动添加），或者走 HTTP：把文件放进网站目录（本机或经 SSH），或者让网站的反向代理或 CDN 把 `/.well-known/` 转给 JEC，由 JEC 自己应答；网站在阿里云 ESA 上时，还可以由 JEC 部署的边缘函数从边缘 KV 直接应答，不经过源站。
- 每张新证书自动部署到阿里云 ESA（并删掉被替换的旧证书）、阿里云 CDN 和全站加速（DCDN）、阿里云数字证书管理服务、OSS 存储空间的自定义域名、传统型负载均衡（CLB）和应用型负载均衡（ALB）、华为云 CDN、七牛云 CDN、Cloudflare 自定义证书、经腾讯云 SSL 证书服务更新到腾讯云 CDN、EdgeOne、CLB 等资源，经 AWS Certificate Manager 更新到 CloudFront 和负载均衡，宝塔面板和 1Panel 的网站、Nginx Proxy Manager、群晖 DSM、Proxmox VE 节点，本地目录和 SSH 服务器（写完都可以执行一条命令，比如重载 nginx）、Docker 容器（放好后发信号让它重载，或者重启它）、Kubernetes 的 TLS Secret 和 Webhook（发 JEC 的 JSON，或者按你写的格式）。
- 进入续期窗口后，在设置页设定的时间自动续期（默认每天 02:00），只剩一周或更少时立即续期；CA 经 ACME 续期信息（ARI）建议提前续期时（比如要吊销证书之前）提前续期；失败时按逐渐拉长的间隔重试。
- 通过 Telegram、邮件（SMTP）、Webhook，钉钉、飞书（Lark）、企业微信的群机器人，以及 Server酱、Bark、ntfy、Gotify、Discord、Slack（Mattermost、Rocket.Chat 也行）、Pushover 发通知，每个渠道可以选自己的语言；别的系统可以用自定义格式的 Webhook 对接，或者用你自己的脚本，由 JEC 内置的 shell 运行。
- 签发记录：每一次签发、续期和部署，都带完整日志。
- 线上巡检：像浏览器一样连到每个域名，续期后如果线上还是旧证书（比如 CDN 没换上新的），会通知你。
- 内核之外的一切都是模块，可以在“模块”页开关，也可以在编译时去掉。
- 用任何语言写的插件可以加上部署目标、通知渠道和域名验证方式，不用重新编译 JEC（[docs/plugins.zh-CN.md](docs/plugins.zh-CN.md)）。
- 配置可以导出为 YAML 文件（服务商、证书和续期默认值），放进 Git，或者导入另一个 JEC；导入前先列出要新增和修改的内容，不会删除任何东西。
- 用密码登录，也可以开启两步验证，再输入验证器 App 里的验证码；给脚本用的 API 令牌；审计日志记下通过面板做过的事，是谁、从哪里做的；严格的安全响应头。
- 一个很小的容器镜像，或者一个静态二进制文件，数据存在 SQLite 里。

计划中：国内云平台的免费证书作为来源，以及更多 CDN 和面板。

每个服务商测到了哪一步（真实账号、真实软件、模拟器还是替身）：[docs/providers.zh-CN.md](docs/providers.zh-CN.md)。

## 快速上手

用 Docker Compose：

```sh
mkdir jec && cd jec
curl -fsSLO https://raw.githubusercontent.com/XMZO/JustEnoughCerts/main/deploy/docker-compose.yml
mkdir -p data && sudo chown 65532:65532 data
docker compose up -d
docker logs jec 2>&1 | grep setup_token
```

打开 `http://<主机>:8080`，填入日志里的初始化令牌，创建账号。在账号创建之前，服务每次启动都会在日志里打印一个新令牌，所以只有能看到日志的人才能接管这台服务。

容器以 65532:65532 用户运行，数据库等所有数据都在 `./data`，备份这个目录就够了，也可以在设置页下载数据库的备份。在设置页点“恢复”就能把备份换回来：JEC 会停下，由 Docker 重新启动后用上备份，原来的数据库改名留在 `./data`。也可以手动恢复：停止 JEC，把备份文件放进 `./data`、改名为 `jec.db`。想以已有目录的属主运行，在 `docker-compose.yml` 里设置 `user:`。

私钥和凭据在数据库里是加密存放的，密钥在 `./data/secret.key`，第一次启动时自动生成。没有这个文件，加密的内容就再也读不出来，所以要和数据库一起备份；也可以把密钥放在别处，通过 `JEC_SECRET_KEY` 传入。

改密码、用验证器 App 开启两步验证都在设置页。忘了密码或者 App 丢了，可以在服务器上重新设置密码，同时会关闭两步验证、让这个账号在所有地方退出登录：

```sh
docker exec -it jec /jec reset-password
```

### 放在反向代理后面

把 `JEC_BASE_URL` 设为用户实际打开的地址，例如 `https://certs.example.com`，或者子路径 `https://example.com/certs/`。地址是 `https` 时，会话 Cookie 只通过 HTTPS 发送。设置了它，通知里才会带上对应页面的链接。同时把 `JEC_TRUSTED_PROXIES` 设为代理的地址，比如 `127.0.0.1`，或者代理容器所在的网段 `172.16.0.0/12`：JEC 就会从 `X-Forwarded-For` 取客户端地址，登录失败锁定的是那个客户端，而不是代理后面的所有人。

### 不用 Docker

每个版本都有 Linux 和 macOS 的静态二进制文件，分 amd64 和 arm64。不提供 Windows 版本，Windows 上请用 Docker 或 WSL。

```sh
tar -xzf jec_<版本>_linux_amd64.tar.gz
./jec serve -data-dir /var/lib/jec
```

### 接口

面板做的一切都经过它的接口，说明在 `/api/v1/openapi.json`。给脚本和其他程序用时，在 设置 → API 令牌 里创建一个令牌（只读或读写），作为 Bearer 令牌发送：

```sh
curl -H "Authorization: Bearer jec_..." https://certs.example.com/api/v1/certs/certificates
```

任何令牌都不能修改密码、两步验证和令牌。

### 用 YAML 导出和导入配置

设置 → 数据和备份 → 配置文件，可以把服务商、证书和续期默认值导出为 YAML，也可以导入这样的文件，用来把配置放进 Git，或者搬到另一个 JEC。它不是 `config.yaml`（那是服务器自己的运行设置）。证书按名称引用服务商：

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

导入会新增 JEC 没有的、修改已有的：服务商按种类和名称对应，证书按域名对应。导入不删除任何东西；文件里没有的密钥保持不变（新增的服务商要在文件里写上自己的密钥），证书的来源也不会变。JEC 会先列出文件要做的改动，任何一项有错误就什么都不改。导出默认不含密钥，要包含需要输入密码。接口也一样：`GET /api/v1/config`，`POST /api/v1/config/check` 查看文件会改什么，`POST /api/v1/config/import` 导入。

## 配置

每项设置的来源，优先级从低到高：默认值、配置文件、环境变量、命令行参数。

| 参数 | 环境变量 | `config.yaml` | 默认值 |
|---|---|---|---|
| `-data-dir` | `JEC_DATA_DIR` | | `data`（镜像里是 `/data`） |
| `-listen` | `JEC_LISTEN` | `listen` | `:8080` |
| `-base-url` | `JEC_BASE_URL` | `base_url` | 服务端看到的地址 |
| `-hsts` | `JEC_HSTS` | `hsts` | `false` |
| `-trusted-proxies` | `JEC_TRUSTED_PROXIES` | `trusted_proxies` | 无 |
| `-log-level` | `JEC_LOG_LEVEL` | `log.level` | `info` |
| `-log-format` | `JEC_LOG_FORMAT` | `log.format` | `text`（或 `json`） |
| `-config` | `JEC_CONFIG` | | `<data-dir>/config.yaml`（存在时） |
| | `JEC_SECRET_KEY` | | `<data-dir>/secret.key` |

只有面板始终经 HTTPS 访问时才把 `JEC_HSTS` 设为 `true`：设置后浏览器一年内都不肯再用 HTTP 打开它。

`JEC_SECRET_KEY` 是 32 个随机字节的 base64，可以用 `openssl rand -base64 32` 生成。它只能通过环境变量传入；如果和加密数据库时用的密钥不一致，JEC 会拒绝启动。

`jec <命令> -h` 列出一个命令的全部参数。

## 校验发布文件

校验和文件和镜像由发布流水线用 [cosign](https://github.com/sigstore/cosign) 无密钥签名：

```sh
cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/XMZO/JustEnoughCerts/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c checksums.txt

cosign verify ghcr.io/xmzo/justenoughcerts:<版本> \
  --certificate-identity-regexp '^https://github\.com/XMZO/JustEnoughCerts/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

每个压缩包都附带 SPDX 格式的 SBOM，每个镜像都附带 SBOM 和构建来源证明。

## 开发

需要 Go 1.26 或更新版本、Node.js 24 和 pnpm 10（`corepack enable`）。

```sh
make web         # 把前端构建到 web/dist，Go 编译时会嵌进程序
make run         # 用 ./data 运行服务
make dev         # 前端热更新开发服务器（:5173），/api 转发给 make run
make test        # Go 测试（带竞态检测），然后是前端测试
make lint        # golangci-lint、类型检查和 ESLint
make i18n-check  # i18n 门禁
make gen         # 改了界面文字或 API 之后：重新生成 key 常量和类型
make new-module id=acme category=source  # 生成一个新模块
```

每个目标都只是一条 `go`、`pnpm` 或 `docker` 命令，Windows 上直接运行这些命令即可；`make help` 列出全部目标。

几条由检查强制执行的规则：

- 界面上的每一段文字都只写在 `i18n/locales/*.json` 里，每种语言一个文件；代码通过生成的常量引用。改了语言文件之后运行 `go run ./tools/i18n fix`。
- 代码、注释和脚本都用英文。
- 模块之间只通过内核通信：模块不能 import 其他模块，内核也不能 import 模块。

[PLAN.md](PLAN.md) 里是架构、模块接口和路线图，[docs/design](docs/design) 里是设计系统和页面设计稿。

## 许可证

[MIT](LICENSE)
