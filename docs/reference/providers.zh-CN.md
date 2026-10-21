# 服务商支持与测试情况

[English](providers.md)

本文列出 JEC 的全部证书来源、验证方式、部署目标与通知渠道及其测试方式，并说明各厂商的使用条件。依赖服务端的测试凡能在 CI 中运行的均在 CI 中运行；其余为现场测试，设置相应的环境变量后针对一次性容器运行（参见各模块的 `live_test.go`）。

测试方式分为以下四类：

| 测试方式 | 说明 |
|---|---|
| 真实服务 | 使用该服务的真实账号测试 |
| 真实软件 | 在容器中运行该服务自身的软件进行测试 |
| 模拟器 | 使用实现该服务接口的软件测试，例如模拟 AWS 的 moto、模拟 ACME CA 的 Pebble |
| 替身 | 依据接口文档或现有客户端编写的模拟服务端；仅校验 JEC 发出的请求，不反映服务端的实际处理 |

仅用替身测试的类型在界面上标有“未实测”：可能无法在真实服务上生效。欢迎反馈使用真实账号的测试结果。

## 证书来源

| 类型 | 说明 | 测试方式 |
|---|---|---|
| `acme.account` | 任意 ACME CA，支持 External Account Binding；续期信息（ARI）、吊销、证书配置与 IP 地址证书 | 模拟器：Pebble（CI）；ZeroSSL 自动领取 EAB 仅替身 |
| `namecheap.ssl_manager` | Namecheap SSL Manager（实验性） | 真实服务；替身，依据其官方客户端编写 |
| `privateca.ca` | 私有 CA：用自己的 CA 签发的证书 | 无外部服务；用 Go 的证书校验核对证书链 |
| `files.watch` | 本机其他客户端续期的证书 | 文件系统 |
| `aliyun.fetch` | 阿里云数字证书管理服务签发的证书 | 替身 |
| `aliyun.apply` | 向阿里云申请的证书（默认为个人测试证书） | 替身 |
| `tencentcloud.fetch` | 腾讯云 SSL 证书服务签发的证书 | 替身（经 SDK 客户端的 HTTP 层） |
| `tencentcloud.free` | 腾讯云免费证书（域名托管于同一账号的 DNSPod） | 替身（经 SDK 客户端的 HTTP 层） |
| 上传 | PEM、PFX/P12 | 单元测试 |

## 域名验证

| 类型 | 说明 | 测试方式 |
|---|---|---|
| `lego.*` | lego 支持的 221 家 DNS 服务商 | 替身，测试 JEC 运行服务商的子进程；服务商实现由 lego 提供 |
| `lego.tencentcloud`、`lego.alidns`、`lego.cloudflare`、`lego.huaweicloud`（任意记录、测试连接、区域预检） | 证书厂商验证所需的 TXT、CNAME 记录，列出账号的域名，核对账号是否管理某个域名，均经 JEC 自带的客户端 | 替身（华为云经其 SDK） |
| `manual.dns` | 手动添加的 DNS 记录 | 模拟的 DNS 查询 |
| `files.webroot` | 本机网站目录 | 文件系统 |
| `ssh.webroot` | 经 SSH 访问的网站目录 | 测试内置的 SSH 服务器 |
| `wellknown.answer` | 由 JEC 应答 `/.well-known/` 请求 | 真实 HTTP |
| `esa.edge` | 阿里云 ESA 边缘应答（边缘 KV 与边缘函数） | 替身接口；函数代码在 Node.js 中实际运行 |
| `cloudflare.edge` | Cloudflare 边缘应答（Workers KV 与 Worker） | 真实服务；替身接口 |
| `plugins.*` | 插件 | 真实程序 |

## 部署目标

| 类型 | 说明 | 测试方式 |
|---|---|---|
| `esa.site` | 阿里云 ESA | 真实服务；替身（使用阿里云 SDK） |
| `aliyun.cdn`、`aliyun.dcdn` | 阿里云 CDN 与全站加速 | 替身 |
| `aliyun.cas` | 阿里云数字证书管理服务 | 替身 |
| `aliyun.oss` | 阿里云 OSS 自定义域名 | 替身 |
| `aliyun.clb` | 阿里云传统型负载均衡（CLB） | 替身 |
| `aliyun.alb` | 阿里云应用型负载均衡（ALB） | 替身 |
| `huaweicloud.cdn` | 华为云 CDN | 替身 |
| `huaweicloud.elb` | 华为云 ELB：原地更新证书，可设为监听器的默认证书 | 替身 |
| `cloudflare.custom` | Cloudflare 自定义证书 | 替身 |
| `tencentcloud.ssl` | 腾讯云 SSL 证书服务，更新 CDN、EdgeOne、CLB 等资源 | 替身 |
| `tencentcloud.cdn` | 腾讯云 CDN，直接部署到加速域名 | 替身 |
| `tencentcloud.teo` | 腾讯云 EdgeOne，设为站点域名的证书 | 替身 |
| `qiniu.cdn` | 七牛云 CDN | 替身 |
| `upyun.cdn` | 又拍云 CDN，经控制台接口（实验性） | 替身，参照其他工具的客户端编写 |
| `volcengine.cdn` | 火山引擎 CDN | 替身；签名由官方 SDK 生成 |
| `baiducloud.cdn` | 百度智能云 CDN | 替身；签名已与官方 SDK 核对 |
| `aws.acm` | AWS Certificate Manager，供 CloudFront 与负载均衡使用 | 模拟器：moto |
| `aws.cloudfront` | AWS CloudFront 分发：导入 us-east-1 的 ACM 并设为分发的证书 | 替身 |
| `gcore.cdn` | Gcore CDN 资源 | 替身 |
| `baota.site` | 宝塔面板（aaPanel）网站 | 替身 |
| `onepanel.certificate` | 1Panel 网站证书 | 真实软件：1Panel v1、v2（社区容器镜像） |
| `onepanel.panel` | 1Panel 面板自己的 HTTPS 证书（随之启用 HTTPS） | 真实软件：1Panel v1、v2（社区容器镜像） |
| `safeline.certificate` | 雷池 WAF 证书，原地替换，用它的站点随之更新 | 真实软件：雷池 9.4 |
| `npm.certificate` | Nginx Proxy Manager | 真实软件：NPM 2.16 |
| `caddy.admin` | Caddy（经管理接口） | 真实软件：Caddy 2.11 |
| `apisix.ssl` | Apache APISIX 的 SSL 对象（经 Admin API） | 真实软件：APISIX 3.19（etcd） |
| `kong.certificate` | Kong Gateway 的证书及其 SNI（经 Admin API） | 真实软件：Kong 3.9（PostgreSQL） |
| `synology.dsm` | 群晖 DSM | 替身，参照 acme.sh 的部署脚本编写 |
| `proxmox.node` | Proxmox VE 节点 | 替身 |
| `portainer.ssl` | Portainer 自己的 HTTPS 证书 | 真实软件：Portainer CE（LTS 镜像） |
| `kubernetes.secret` | Kubernetes TLS Secret | 真实软件：kube-apiserver 1.36（启用 RBAC） |
| `docker.container` | Docker 容器 | 真实软件：Docker 29 |
| `files.directory` | 本机目录，可在写入后执行命令 | 文件系统 |
| `ssh.server` | 经 SSH 访问的服务器，可在写入后执行命令 | 测试内置的 SSH 服务器 |
| `webhook.deploy` | 自定义 Webhook | 真实 HTTP |
| `plugins.*` | 插件 | 真实程序 |

## 通知渠道

| 类型 | 说明 | 测试方式 |
|---|---|---|
| `email.smtp` | 电子邮件（SMTP） | 真实软件：Mailpit 的 SMTP 服务器 |
| `ntfy.topic` | ntfy | 真实软件：ntfy |
| `gotify.app` | Gotify | 真实软件：Gotify |
| `telegram.bot` | Telegram | 替身 |
| `webhook.notify` | 自定义 Webhook | 真实 HTTP |
| `dingtalk.robot`、`feishu.bot`、`wecom.robot` | 钉钉、飞书（Lark）、企业微信群机器人 | 替身 |
| `serverchan.sendkey` | Server酱 | 替身 |
| `bark.device` | Bark | 替身 |
| `discord.webhook` | Discord | 替身 |
| `slack.webhook` | Slack、Mattermost、Rocket.Chat | 替身 |
| `pushover.user` | Pushover | 替身 |
| `teams.workflow` | Microsoft Teams（Workflows） | 替身 |
| `googlechat.webhook` | Google Chat | 替身 |
| `matrix.room` | Matrix | 真实软件：Conduit |
| `pushplus.token` | PushPlus | 替身 |
| `onebot.qq` | QQ（OneBot v11） | 替身 |
| `hooks.script` | 自定义脚本，由内置 shell 运行 | 真实脚本 |
| `plugins.*` | 插件 | 真实程序 |

## 其他外部服务

以下功能不是服务商类型，但同样连接外部服务：

| 功能 | 连接的服务 | 测试方式 |
|---|---|---|
| [异地备份](../operations/backup.zh-CN.md#异地备份) | S3 兼容存储、WebDAV | 真实软件：SeaweedFS 的 S3 接口、rclone 的 WebDAV；AWS S3、Cloudflare R2、阿里云 OSS、腾讯云 COS、坚果云等未实测，界面上已注明 |
| [证书透明度](../usage/sites.zh-CN.md#证书透明度) | Cert Spotter | 真实服务（不带 API 密钥） |
| [域名注册](../usage/sites.zh-CN.md#域名注册) | IANA 与各注册局的 RDAP、WHOIS | 真实服务 |
| 吊销检查、补齐证书链、缺中间证书检查 | 证书中的 OCSP、CRL 与颁发者地址（AIA） | 真实服务：Let's Encrypt、Sectigo 等的证书，以及 badssl.com 的缺中间证书站点 |
| DNS 服务商的“试一下” | 各 DNS 服务商（经 lego） | 真实软件：BIND 的 RFC 2136 动态更新；其他服务商与签发时走同一路径 |
| [单点登录](../operations/security.zh-CN.md#单点登录) | OpenID Connect 身份提供方 | 真实软件：Dex |
| 添加证书时列出账号下的域名 | 腾讯云 DNSPod、阿里云 DNS、Cloudflare、华为云 DNS | 替身；界面上已注明未实测 |
| 自动检查更新 | GitHub 的发布列表 | 真实服务 |

## 厂商使用条件

部分证书来源与部署目标须满足厂商的条件方可使用。证书颁发机构支持 ACME（需要账号时提供 External Account Binding）的，建议优先使用 ACME，或手动上传其签发的证书；厂商专有接口均为可选模块。

- **Namecheap SSL Manager**（`namecheap.ssl_manager`，实验性）：仅适用于直接在 Namecheap 购买的证书。每张证书须在 Namecheap 控制台激活或重签时选择 Server-side automation，以获取有效期 24 小时的一次性令牌。仅支持单域名证书，签发的证书同时包含 `www`；Namecheap 通过网站文件验证主域名与 `www`。
- **Namecheap 官方 API**（暂无计划，Namecheap 的证书请使用 SSL Manager）：账号须满足以下条件之一：拥有 20 个域名、余额达到 50 美元、近两年消费满 50 美元；并且仅允许从账号白名单中的 IPv4 地址调用。
- **阿里云个人测试证书**（`aliyun.apply`）：DigiCert DV，有效期 3 个月，每年 20 张，仅限中国站；须先在数字证书管理服务控制台领取额度。其他 DV 资源包的用法相同。
- **腾讯云免费证书**（`tencentcloud.free`）：TrustAsia DV，有效期 3 个月，每张仅含一个域名且不支持通配符；域名须托管于同一账号的 DNSPod，由腾讯云自动完成验证；每个账号的申请数量有上限。
- **阿里云 ESA**（`esa.site`）：免费版每个站点最多 2 张自定义证书，因此 JEC 在部署新证书后删除被替换的证书。边缘应答（`esa.edge`）要求套餐包含“函数和 Pages”与“边缘 KV”。
- **Cloudflare 自定义证书**（`cloudflare.custom`）：仅限 Business 与 Enterprise 套餐。边缘应答（`cloudflare.edge`）适用于所有套餐，受 Workers 与 Workers KV 每日免费额度的限制。
- **又拍云**（`upyun.cdn`，实验性）：使用账号密码登录控制台，不支持已开启两步验证的账号。
- **群晖 DSM**（`synology.dsm`）：须使用管理员账号；已开启两步验证的账号须提供其密钥，由 JEC 生成验证码。
