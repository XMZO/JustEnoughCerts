# 配置参考

[English](configuration.md)

服务器的运行设置可通过命令行参数、环境变量或配置文件 `config.yaml` 指定，优先级由低到高依次为：默认值、配置文件、环境变量、命令行参数。证书、服务商等业务设置在界面中管理，不属于本页范围。

## 设置项

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

`jec <命令> -h` 列出命令的全部参数。配置文件示例：

```yaml
listen: ":8080"
base_url: https://certs.example.com
trusted_proxies: 172.16.0.0/12
dns_resolvers: 223.5.5.5,119.29.29.29
log:
  level: info
  format: json
```

## 反向代理

Caddy 与 nginx 的完整配置（含前置 CDN 的情形）见[反向代理与 CDN](reverse-proxy.zh-CN.md)。

- `JEC_BASE_URL`：设为用户访问的地址，例如 `https://certs.example.com`，或子路径 `https://example.com/certs/`。使用 `https` 地址时，会话 Cookie 仅经 HTTPS 发送；设置后，通知中会附带对应页面的链接。
- `JEC_TRUSTED_PROXIES`：设为反向代理的地址或 CIDR 网段，多个以逗号分隔，例如 `127.0.0.1`，或容器网段 `172.16.0.0/12`（`docker-compose.yml` 的默认设置）。来自这些地址的请求，JEC 从 `X-Forwarded-For` 读取客户端地址；未设置时，代理之后的全部用户共用代理的地址，一人连续输错密码将导致所有人被锁定。
- `JEC_HSTS`：仅当面板始终经 HTTPS 访问时设为 `true`；启用后，浏览器在一年内拒绝以 HTTP 访问该地址。

## DNS 解析服务器

`JEC_DNS_RESOLVERS` 指定 DNS 递归解析服务器，例如 `223.5.5.5,119.29.29.29`，可带端口。查询验证记录时，JEC 先用它们确定记录所在的域，再直接查询该域的权威 DNS 服务器。仅在本机的解析结果有误时（例如内外网解析不一致）需要设置；地址无效时 JEC 拒绝启动。

## 主密钥

私钥与凭据在数据库中以 AES-256-GCM 加密存储。主密钥默认为数据目录中的 `secret.key`，首次启动时生成。也可通过 `JEC_SECRET_KEY` 传入：32 个随机字节的 base64 编码，可用 `openssl rand -base64 32` 生成，仅可通过环境变量传入。主密钥与加密数据库时所用的密钥不一致时，JEC 拒绝启动。主密钥的备份要求见[数据与备份](backup.zh-CN.md)。

## 时区

续期的每日检查时间、通知中的时间与界面上的时间均以面板的时区为准。时区在设置页的“外观”中选择，默认跟随服务器（`TZ` 环境变量；`docker-compose.yml` 默认设置 `Asia/Shanghai`）。

## 代理

JEC 的通用 HTTP 客户端（大多数通知渠道与部署目标使用）不读取环境变量中的代理（`HTTP_PROXY`、`HTTPS_PROXY`），以免国内服务也经代理访问。连接境外服务的服务商类型可在高级设置中单独设置代理，支持 `http://`、`https://` 与 `socks5://`。未单独设置代理的 ACME 账号、lego 的 DNS 服务商以及“检查更新”使用环境变量中的代理。
