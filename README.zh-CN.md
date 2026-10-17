# Just Enough Certs

*Just enough certs to keep your site green.*

[English](README.md)

Just Enough Certs（JEC）是自托管的 SSL/TLS 证书管理面板：从你购买或签发证书的地方获取证书，自动完成域名验证，部署到 CDN 和服务器，到期前自动续期。名字和视觉致敬 Minecraft 的 JEI 模组。

> [!WARNING]
> JEC 还在早期开发中。0.0.x 版本只是骨架：服务端、初始化向导和登录，支持中文和英文。证书相关的功能在接下来的里程碑里加入，路线图见 [PLAN.md](PLAN.md)。

## 计划中的功能

- 从任意 ACME CA（支持 External Account Binding）和 Namecheap 等厂商 API 获取证书。
- 域名验证：在 [lego](https://github.com/go-acme/lego) 支持的任意 DNS 服务商添加记录，或者 HTTP 验证、手动验证。
- 部署到阿里云 ESA 等 CDN、通过 SSH 部署到服务器、写到本地文件，部署前后可以跑钩子。
- 到期前自动续期，并发送通知。
- 内核之外的一切都是模块，可以在运行时停用，也可以在编译时去掉。
- 一个很小的容器镜像，或者一个静态二进制文件，数据存在 SQLite 里。

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

容器以 65532:65532 用户运行，数据库等所有数据都在 `./data`，备份这个目录就够了。想以已有目录的属主运行，在 `docker-compose.yml` 里设置 `user:`。

忘了密码可以重新设置，同时会让这个账号在所有地方退出登录：

```sh
docker exec -it jec /jec reset-password
```

### 放在反向代理后面

把 `JEC_BASE_URL` 设为用户实际打开的地址，例如 `https://certs.example.com`，或者子路径 `https://example.com/certs/`。地址是 `https` 时，会话 Cookie 只通过 HTTPS 发送。

### 不用 Docker

每个版本都有 Linux 和 macOS 的静态二进制文件，分 amd64 和 arm64。不提供 Windows 版本，Windows 上请用 Docker 或 WSL。

```sh
tar -xzf jec_<版本>_linux_amd64.tar.gz
./jec serve -data-dir /var/lib/jec
```

## 配置

每项设置的来源，优先级从低到高：默认值、配置文件、环境变量、命令行参数。

| 参数 | 环境变量 | `config.yaml` | 默认值 |
|---|---|---|---|
| `-data-dir` | `JEC_DATA_DIR` | | `data`（镜像里是 `/data`） |
| `-listen` | `JEC_LISTEN` | `listen` | `:8080` |
| `-base-url` | `JEC_BASE_URL` | `base_url` | 服务端看到的地址 |
| `-log-level` | `JEC_LOG_LEVEL` | `log.level` | `info` |
| `-log-format` | `JEC_LOG_FORMAT` | `log.format` | `text`（或 `json`） |
| `-config` | `JEC_CONFIG` | | `<data-dir>/config.yaml`（存在时） |

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
