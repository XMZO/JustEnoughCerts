# 服务商和它们测到了哪一步

JEC 的每种证书来源、验证方式、部署目标和通知渠道，以及对着什么测过。需要服务端的测试，能在 CI 里跑的在 CI 里跑；其他的是“现场测试”，设置了对应的环境变量时对着一次性容器运行（见各模块的 `live_test.go`）。

- **真实服务**：用这个服务的真实账号试过。
- **真实软件**：这个服务自己的软件，跑在容器里。
- **模拟器**：能说这个服务的接口的软件，比如模拟 AWS 的 moto、模拟 ACME CA 的 Pebble。
- **替身**：照着接口文档、或者照着用这个接口的客户端写的假服务端；检查的是 JEC 发出的请求，不是服务那边会怎么处理。

欢迎反馈用真实账号试过哪些能用、哪些不行。

## 证书来源

| 类型 | 是什么 | 测过的对象 |
|---|---|---|
| `acme.account` | 任意 ACME CA，支持 External Account Binding | 模拟器：Pebble，在 CI 里 |
| `namecheap.ssl_manager` | Namecheap SSL Manager（实验） | 替身，照着它的客户端 |
| `files.watch` | 本机另一个客户端续期的证书 | 文件系统 |
| `aliyun.fetch` | 阿里云数字证书管理服务签发的证书 | 替身 |
| `aliyun.apply` | 向阿里云申请证书（默认个人测试证书） | 替身 |
| `tencentcloud.fetch` | 腾讯云 SSL 证书服务签发的证书 | 替身，HTTP 层经 SDK 的客户端 |
| `tencentcloud.free` | 腾讯云免费证书（域名在同一账号的 DNSPod） | 替身，HTTP 层经 SDK 的客户端 |
| 上传 | PEM、PFX/P12 | 单元测试 |

## 域名验证

| 类型 | 是什么 | 测过的对象 |
|---|---|---|
| `lego.*` | 经 lego 支持的 221 家 DNS 服务商 | 替身，测的是 JEC 运行它们的子进程；服务商的实现是 lego 的 |
| `lego.tencentcloud`、`lego.alidns`、`lego.cloudflare`、`lego.huaweicloud` 加任意记录 | 厂商验证要的 TXT、CNAME 记录，用 JEC 自己的客户端 | 替身（华为云经 SDK 发到替身） |
| `manual.dns` | 手动添加的 DNS 记录 | 假的 DNS 查询 |
| `files.webroot` | 本机的网站目录 | 文件系统 |
| `ssh.webroot` | 经 SSH 的网站目录 | 测试里的 SSH 服务器 |
| `wellknown.answer` | JEC 自己应答 /.well-known/ | 真实 HTTP |
| `esa.edge` | 阿里云 ESA 边缘应答，经边缘 KV 和边缘函数 | 替身接口；函数代码在 Node.js 里实际运行 |
| `plugins.*` | 插件 | 真实程序 |

## 部署目标

| 类型 | 是什么 | 测过的对象 |
|---|---|---|
| `esa.site` | 阿里云 ESA | 替身，用阿里云的 SDK |
| `aliyun.cdn`、`aliyun.dcdn` | 阿里云 CDN 和全站加速 | 替身 |
| `aliyun.cas` | 阿里云数字证书管理服务 | 替身 |
| `aliyun.oss` | 阿里云 OSS 存储空间的自定义域名 | 替身 |
| `aliyun.clb` | 阿里云传统型负载均衡（CLB） | 替身 |
| `aliyun.alb` | 阿里云应用型负载均衡（ALB） | 替身 |
| `huaweicloud.cdn` | 华为云 CDN | 替身 |
| `cloudflare.custom` | Cloudflare 自定义证书 | 替身 |
| `tencentcloud.ssl` | 腾讯云 SSL 证书，更新到 CDN、EdgeOne、CLB 等 | 替身 |
| `qiniu.cdn` | 七牛云 CDN | 替身 |
| `upyun.cdn` | 又拍云 CDN，经控制台的接口（实验） | 替身，照其他工具的客户端写 |
| `volcengine.cdn` | 火山引擎 CDN | 替身，签名用官方 SDK |
| `baiducloud.cdn` | 百度智能云 CDN | 替身，签名和官方 SDK 的核对过 |
| `aws.acm` | AWS Certificate Manager，供 CloudFront 和负载均衡使用 | 模拟器：moto |
| `baota.site` | 宝塔（aaPanel）网站 | 替身 |
| `onepanel.certificate` | 1Panel 证书 | 替身 |
| `npm.certificate` | Nginx Proxy Manager | 真实软件：NPM 2.16 |
| `caddy.admin` | 经管理接口装进 Caddy | 真实软件：Caddy 2.11 |
| `synology.dsm` | 群晖 DSM | 替身，照着 acme.sh 的部署脚本 |
| `proxmox.node` | Proxmox VE 节点 | 替身 |
| `kubernetes.secret` | Kubernetes 的 TLS Secret | 真实软件：kube-apiserver 1.36，带 RBAC |
| `docker.container` | Docker 容器 | 真实软件：Docker 29 |
| `files.directory` | 本机目录，再执行命令 | 文件系统 |
| `ssh.server` | 经 SSH 的服务器，再执行命令 | 测试里的 SSH 服务器 |
| `webhook.deploy` | 你自己的 Webhook | 真实 HTTP |
| `plugins.*` | 插件 | 真实程序 |

## 通知渠道

| 类型 | 是什么 | 测过的对象 |
|---|---|---|
| `email.smtp` | SMTP 邮件 | 真实软件：Mailpit 的 SMTP 服务器 |
| `ntfy.topic` | ntfy | 真实软件：ntfy |
| `gotify.app` | Gotify | 真实软件：Gotify |
| `telegram.bot` | Telegram | 替身 |
| `webhook.notify` | 你自己的 Webhook | 真实 HTTP |
| `dingtalk.robot`、`feishu.bot`、`wecom.robot` | 钉钉、飞书（Lark）、企业微信群机器人 | 替身 |
| `serverchan.sendkey` | Server酱 | 替身 |
| `bark.device` | Bark | 替身 |
| `discord.webhook` | Discord | 替身 |
| `slack.webhook` | Slack、Mattermost、Rocket.Chat | 替身 |
| `pushover.user` | Pushover | 替身 |
| `hooks.script` | 你自己的脚本，由内置的 shell 运行 | 真实脚本 |
| `plugins.*` | 插件 | 真实程序 |
