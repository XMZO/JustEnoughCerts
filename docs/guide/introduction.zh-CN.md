# 介绍

[English](introduction.md)

Just Enough Certs（JEC）是一款自托管的 SSL/TLS 证书管理面板。它从各类来源获取证书，自动完成域名验证，将证书部署到 CDN、负载均衡、面板与服务器，并在到期前自动续期。

## 适用场景

- 证书分散在多家证书颁发机构与云平台，需要统一查看到期时间并集中续期。
- 网站托管于 CDN（如阿里云 ESA、腾讯云 EdgeOne、Cloudflare），每次续期后须手动上传证书。
- 证书有效期缩短（例如 200 天）后，人工更换的频率与出错风险随之上升。

## 工作流程

1. **获取**：从证书来源取得证书，例如由 ACME 证书颁发机构签发、经厂商接口签发或取回，或手动上传。
2. **验证**：通过验证方式证明域名控制权，即添加 DNS 记录或在网站上放置验证文件。
3. **部署**：将每个新版本部署到证书选定的全部部署目标，并按需删除被替换的旧证书。
4. **续期**：证书进入续期窗口后自动重新签发并部署；失败时自动重试并发出通知。
5. **巡检**：连接各域名核对线上证书，发现仍在使用旧证书时发出通知。

各环节涉及的对象及其关系，参见[核心概念](concepts.zh-CN.md)。

## 主要特性

- [证书来源](../usage/sources.zh-CN.md)：ACME（含 External Account Binding 与证书配置，可签短期证书与 IP 地址证书）、手动上传与多文件导入（自动补齐证书链）、本机 certbot 与 acme.sh、阿里云与腾讯云证书服务、私有 CA、Namecheap SSL Manager（实验性）。
- [域名验证](../usage/validation.zh-CN.md)：lego 支持的 221 家 DNS 服务商、任意记录、手动添加、网站目录、JEC 自行应答与阿里云 ESA、Cloudflare 边缘应答。
- [部署目标](../usage/targets.zh-CN.md)：31 种，覆盖阿里云、腾讯云、华为云、Cloudflare、AWS、Gcore 等云服务，宝塔面板、1Panel 等面板，以及本地目录、SSH 服务器、Docker 容器与 Kubernetes。
- [续期](../usage/renewal.zh-CN.md)：续期窗口与每日检查时间、ACME 续期信息（ARI）、失败自动重试、线上巡检与吊销检查。
- [到期监控](../usage/sites.zh-CN.md)：不由 JEC 管理的网站证书与域名注册到期。
- [通知](../usage/notifications.zh-CN.md)：14 种通知渠道，每个渠道可单独选择语言与事件，可每周发送概况；证书到期与续期的日历订阅；[Prometheus 指标与心跳监控](../operations/monitoring.zh-CN.md)。
- [账号与安全](../operations/security.zh-CN.md)：密钥加密存储、两步验证、单点登录、API 令牌与审计日志；已签发的证书可以[吊销](../usage/certificates.zh-CN.md#吊销证书)。
- 模块与[插件](../development/plugins.zh-CN.md)：内核之外的功能均为模块，可启用、停用或在编译时排除；插件可用任意语言编写。
- 轻量部署：单个静态二进制文件或小体积容器镜像，数据存储于 SQLite（也可使用 [PostgreSQL](../operations/postgresql.zh-CN.md)）；界面支持中文与英文，可作为渐进式 Web 应用（PWA）添加到手机主屏幕。

## 项目状态

JEC 仍处于早期开发阶段，尚未发布正式版。测试版为 0.1.0-alpha.x（镜像标签 `beta`）。阿里云 ESA 部署、Namecheap SSL Manager 与 Cloudflare 边缘应答已经过真实账号验证；仅用替身测试的类型在界面上标有“未实测”，可能无法生效；各服务商的测试情况见[服务商支持与测试情况](../reference/providers.zh-CN.md)。

JEC 以 MIT 许可证发布。项目名称与视觉风格致敬 Minecraft 模组 JEI。
