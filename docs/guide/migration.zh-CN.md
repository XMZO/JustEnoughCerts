# 从 certbot 或 acme.sh 迁移

[English](migration.md)

JEC 可以一次性接替 certbot 或 acme.sh，也可以分步进行：先读取它们续期的证书，由 JEC 负责部署和到期监控，准备好后再由 JEC 自己签发。

## 选择方式

| 方式 | 适用情形 |
|---|---|
| 原客户端继续续期，JEC 读取其文件 | 原有续期运行正常，希望先用上更多部署目标、通知、线上检查和统一的总览 |
| 由 JEC 签发 | 希望由 JEC 续期，并停用原客户端 |

## 保留原客户端

1. 让 JEC 能读取文件。私钥属于 root，只有 root 可读。与其让 JEC 以 root 运行，不如把文件的属组改为 JEC 的组并加上组读权限（Docker 镜像为组 65532，systemd 服务为 `jec`）。certbot 续期生成的新私钥会沿用旧私钥的属组和组读权限（以 certbot 5 实测）：

   ```sh
   sudo chgrp -R 65532 /etc/letsencrypt/live /etc/letsencrypt/archive
   sudo chmod -R g+rX /etc/letsencrypt/live /etc/letsencrypt/archive
   ```

   acme.sh 续期时沿用域名的私钥：对其主目录（如 `/root/.acme.sh`）做同样设置，并用 `sudo chmod o+x /root` 允许 JEC 进入 `/root`。在 Docker 中还须在 `docker-compose.yml` 里启用该目录的挂载，参见[可选挂载](installation.zh-CN.md#可选挂载)。
2. 在“证书来源”中添加“本机证书目录”，目录填 `/etc/letsencrypt` 或 acme.sh 的主目录。
3. 以该来源添加证书：页面列出目录中的证书供选择，在“多张证书”中点“全部加入”可一次全部添加；按需选择部署目标。
4. 原客户端续期后，JEC 在下一次查看时取入新证书并部署。

## 由 JEC 签发

1. 添加同一 CA 的 ACME 账号（新账号即可），以及原客户端使用的验证方式：相同的 DNS 服务商和 API 密钥，或其 webroot 对应的网站目录。
2. 以相同的域名添加证书。JEC 立即签发一张新证书，旧证书在到期前仍然有效。已经以“本机证书目录”添加的证书，可改为在其“续期设置”中把证书来源换成该 ACME 账号：签发记录和部署目标保持不变，下一次续期起由 JEC 签发。
3. 部署到服务器读取证书的位置：使用“本地目录”部署目标，填写服务器的路径和重载命令。JEC 默认的文件名与 certbot 相同，写入 `/etc/letsencrypt/live/<域名>` 时原有路径无需修改。
4. JEC 部署完成后，停用原客户端的续期：

   ```sh
   sudo systemctl disable --now certbot.timer   # 软件包安装的 certbot
   acme.sh --uninstall-cronjob                   # acme.sh
   ```

   另请检查 `/etc/cron.d` 或 crontab 中是否还有 certbot 的任务。

## 对应关系

| 原客户端 | JEC 中 |
|---|---|
| certbot 的 deploy hook | “本地目录”部署目标的写入后执行命令，带 certbot 的 `RENEWED_LINEAGE`、`RENEWED_DOMAINS` 环境变量 |
| `acme.sh --install-cert ... --reloadcmd` | “本地目录”部署目标，填写文件名和命令 |
| certbot 的 DNS 插件（如 certbot-dns-cloudflare） | 同名的 DNS 服务商，填写其 API 密钥 |
| acme.sh 的 DNS API（如 `dns_dp`、`dns_ali`、`dns_cf`） | 腾讯云 DNSPod（使用腾讯云 API 密钥）、阿里云 DNS、Cloudflare |
| `--webroot` | 网站目录验证，填写相同目录 |

各项设置参见[域名验证](../usage/validation.zh-CN.md)与[部署目标](../usage/targets.zh-CN.md)。
