# 常见问题

[English](faq.md)

## 应该选择哪种证书来源？

只要证书颁发机构提供 ACME，就优先使用 ACME：Let's Encrypt 等免费 CA 的证书可全程自动签发与续期。付费证书优先使用 CA 提供的 ACME 服务（配合 External Account Binding），或 JEC 支持的厂商服务；都不适用时，再手动上传证书。参见[证书来源](../usage/sources.zh-CN.md)。

## 无法通过 `http://<服务器>:8080` 打开面板

`docker-compose.yml` 仅在 `127.0.0.1:8080` 上发布 JEC，只能从服务器本机访问。请经配置了 HTTPS 的反向代理或 SSH 隧道打开面板，参见[反向代理与 CDN](../operations/reverse-proxy.zh-CN.md)。

## 检查服务器

`jec doctor` 逐行检查数据目录、数据库、主密钥、面板自己的 HTTPS 证书（设置了时）、到 CA 的网络和服务器时钟，发现问题时以退出码 1 结束：

```sh
docker exec jec /jec doctor                                   # Docker
sudo -u jec /usr/local/bin/jec doctor -data-dir /var/lib/jec  # systemd 服务
```

`-ca` 指定要访问的 ACME 目录地址（默认为 Let's Encrypt）；`-offline` 跳过网络和时钟检查。

## 签发失败，如何查找原因？

打开证书，再打开最近一次签发：页面列出每个步骤及其日志，勾选“显示原始信息”可查看原始错误。常见原因包括：DNS 服务商的凭据没有添加记录的权限、所选 DNS 服务商并不负责该域名的解析、CAA 记录不允许该 CA，以及 CA 的频率限制。失败后 JEC 会自动重试，参见[失败与重试](../usage/renewal.zh-CN.md#失败与重试)。

## 验证记录一直不生效

JEC 向域名的权威 DNS 服务器查询记录是否生效。证书所选的 DNS 服务商必须是域名 NS 记录所指向的服务商。如服务器的域名解析结果与公网不同，可在 `JEC_DNS_RESOLVERS` 中指定公共解析服务器（参见 [DNS 解析服务器](../operations/configuration.zh-CN.md#dns-解析服务器)）。DNS 服务商没有 API 时，可用 CNAME 记录将 `_acme-challenge` 委派到受支持服务商上的域名（参见[域名验证](../usage/validation.zh-CN.md)）。

## CA 因 CAA 记录拒绝签发

域名 DNS 中的 CAA 记录指定允许为其签发证书的 CA。向 Let's Encrypt、ZeroSSL、Google Trust Services、SSL.com、Sectigo、DigiCert 等常见 CA 下单前，JEC 会先读取这些记录；不包括该 CA 时停止签发，并给出应添加的记录，如 `0 issue "letsencrypt.org"`。子域名没有 CAA 记录时，以上级域名的为准。添加证书时，选好 ACME 账号并填写域名后，页面就会提示 CAA 不允许的域名，无需等到签发失败。

## 部署后网站仍在使用旧证书

线上巡检会连接每个域名进行核对：新证书生效 30 分钟后网站仍使用旧证书时，JEC 会报告并发送通知（参见[线上巡检](../usage/renewal.zh-CN.md#线上巡检)）。请检查部署目标的设置（如要更新的站点或域名）及其最近一次部署日志；CDN 应用证书可能需要几分钟。在证书页面选择“重新部署”可再次部署。

## 服务器无法访问 CA 或境外服务

ACME 账号以及访问境外服务的服务商（如 Telegram、Cloudflare、AWS）在高级设置中各有独立的代理设置（参见[代理](../operations/configuration.zh-CN.md#代理)）。

## 忘记密码或丢失了验证器

在服务器上重置密码；此操作同时关闭两步验证、结束所有会话并删除全部 API 令牌：

```sh
docker exec -it jec /jec reset-password                               # Docker
sudo -u jec /usr/local/bin/jec reset-password -data-dir /var/lib/jec  # systemd 服务
```

## 如何将 JEC 迁移到其他服务器？

停止 JEC，将整个数据目录（含 `secret.key`）复制到新服务器，然后在新服务器上启动相同或更新版本的 JEC。也可以使用在设置页下载的备份，并同时提供 `secret.key`。参见[数据与备份](../operations/backup.zh-CN.md)。

## 主密钥丢失了

数据库由其他主密钥加密时，JEC 拒绝启动，因为其中的私钥与凭据无法解密。请从数据目录的备份中恢复 `secret.key`。如果无法恢复，只能使用新的数据目录，重新添加证书与服务商。

## 如何升级？

拉取新镜像并重新创建容器，或使用新的二进制文件再次执行 `jec service install`；数据库在启动时自动迁移。参见[升级](installation.zh-CN.md#升级)。
