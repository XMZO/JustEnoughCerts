# 快速开始

[English](getting-started.md)

本节使用 Docker Compose 部署 JEC，并签发、部署第一张证书。其他安装方式见[安装与升级](installation.zh-CN.md)。

## 前提条件

- 一台安装了 Docker 与 Docker Compose 的 Linux 主机。
- 一个托管于受支持 DNS 服务商（如腾讯云 DNSPod、阿里云 DNS、Cloudflare）的域名，以及该服务商的 API 密钥。

> [!NOTE]
> 正式版发布前，`docker-compose.yml` 使用最新测试版，即镜像标签 `beta`。

## 1. 启动 JEC

```sh
mkdir jec && cd jec
curl -fsSLO https://raw.githubusercontent.com/XMZO/JustEnoughCerts/main/deploy/docker-compose.yml
mkdir -p data && sudo chown 65532:65532 data
docker compose up -d
docker logs jec 2>&1 | grep setup_token
```

最后一条命令输出日志中的初始化令牌（`setup_token=` 之后的部分）。

## 2. 创建管理员账号

JEC 仅监听服务器本机的 `127.0.0.1:8080`。按[反向代理与 CDN](../operations/reverse-proxy.zh-CN.md)配置 HTTPS 访问后，打开面板地址；或者先试用时，在自己的电脑上保持以下 SSH 隧道运行，然后打开 `http://localhost:8080`：

```sh
ssh -N -L 8080:127.0.0.1:8080 <用户>@<主机>
```

填写初始化令牌、用户名与密码，然后选择“创建账号”。账号创建之前，服务每次启动都会生成新的令牌，因此只有能够读取日志的人才能完成初始化。

## 3. 添加证书来源

在“证书来源”页选择“添加证书来源”→“ACME 账号”，证书颁发机构选择 Let's Encrypt，填写用于接收证书颁发机构通知的邮箱，然后保存。

## 4. 添加验证方式

在“域名验证”页选择“添加验证方式”，搜索并选择域名所在的 DNS 服务商，填写其 API 密钥，然后选择“保存并测试”。测试通过时，界面列出该账号可管理的域名。

## 5. 添加部署目标（可选）

在“部署目标”页选择“添加部署目标”，选择证书要部署到的位置，例如阿里云 ESA 站点、Nginx Proxy Manager 或本地目录，填写所需设置后选择“保存并测试”。也可以暂不添加，证书仅保存在 JEC 中。

## 6. 添加证书

1. 在“证书”页选择“添加证书”，证书来源选择第 3 步添加的 ACME 账号。
2. 填写域名，每输入一个按回车；第一个为主域名，通配符写作 `*.example.com`。
3. 验证方式选择第 4 步添加的 DNS 服务商；如已添加部署目标，在“部署到”中勾选。
4. 提交后进入签发进度页，依次显示下单、添加验证记录、证书颁发机构验证与下载证书等步骤。

签发完成后，证书自动部署到所选目标。证书详情页显示有效期、部署结果与线上检查结果；此后 JEC 在到期前自动续期，详见[续期](../usage/renewal.zh-CN.md)。

## 后续步骤

- 了解各类对象及其关系：[核心概念](concepts.zh-CN.md)。
- 在证书事件发生时接收消息：[通知](../usage/notifications.zh-CN.md)。
- 备份数据目录与主密钥：[数据与备份](../operations/backup.zh-CN.md)。
- 部署在反向代理之后：[配置参考](../operations/configuration.zh-CN.md#反向代理)。
