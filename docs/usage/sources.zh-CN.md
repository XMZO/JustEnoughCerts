# 证书来源

[English](sources.md)

证书来源负责签发或取回证书，在“证书来源”页管理。添加证书时选择其一；手动上传的证书不需要证书来源。各类型的测试情况与厂商条件见[服务商支持与测试情况](../reference/providers.zh-CN.md)。

## ACME 账号

适用于任意支持 ACME 协议（RFC 8555）的证书颁发机构，是推荐的证书来源。

| 设置 | 说明 |
|---|---|
| 证书颁发机构 | 内置 Let's Encrypt、ZeroSSL、Google Trust Services、SSL.com、Sectigo、DigiCert、GlobalSign、Actalis、FreeSSL、LiteSSL、PeeringHub 等，部分提供测试环境；选择“自定义”时填写目录地址 |
| 联系邮箱 | 可选，用于接收证书颁发机构的通知 |
| EAB Key ID / EAB HMAC Key | 证书颁发机构要求 External Account Binding 时填写，例如 Google Trust Services；ZeroSSL 可以留空，JEC 注册时用填写的邮箱向 ZeroSSL 领取（尚未用真实账号验证） |
| 证书配置（高级设置） | 证书颁发机构提供的 ACME profile，例如 Let's Encrypt 的 `shortlived`（6 天短期证书）、`tlsserver`；留空使用默认配置。测试连接会列出可用的配置 |
| 代理 | 可选，所在网络无法直接访问证书颁发机构时填写 |

- 账号在首次签发或测试连接时注册，账号私钥加密保存在 JEC 中。
- 验证方式为 DNS 服务商时使用 DNS-01，为网站目录或 JEC 自己应答时使用 HTTP-01。
- 支持 ACME 续期信息（ARI，RFC 9773），按证书颁发机构的建议提前续期。

## 本机证书目录

读取同一台机器上由 certbot 或 acme.sh 续期的证书与私钥，只读，不改动其文件。目录填写 certbot 的 `/etc/letsencrypt`，或 acme.sh 的主目录（如 `/root/.acme.sh`），JEC 按域名查找证书。目录中仍为旧证书时，6 小时后再次检查。在容器中使用时须挂载该目录，参见[安装与升级](../guide/installation.zh-CN.md#可选挂载)。

## 私有 CA

用自己的证书颁发机构签发证书，适用于 `nas.home.arpa` 这类内网名称和 IPv4 地址，无需验证。只有信任该 CA 的设备才信任它签发的证书，因此须在这些设备上安装 CA 证书。

| 设置 | 说明 |
|---|---|
| CA 证书 | PEM 格式；中间 CA 须连同其上级证书一并填写，这些证书（根证书除外）放入证书链 |
| CA 私钥 | PEM 格式，不得加密；在 JEC 中加密保存 |
| 有效期（天） | 每张证书的有效期，默认 90 天，最多 825 天，且不超过 CA 本身的到期时间 |

可用 OpenSSL 创建 CA，例如：

```sh
openssl req -x509 -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
  -keyout ca.key -out ca.crt -days 3650 -subj "/CN=Home CA" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign"
```

`ca.key` 须妥善保管；`ca.crt` 作为受信任的根证书安装到各设备上。

## 云平台证书服务

| 类型 | 说明 |
|---|---|
| 阿里云签发的证书 | 取回阿里云数字证书管理服务签发的证书（含私钥），上传至该服务的证书除外 |
| 腾讯云签发的证书 | 取回腾讯云 SSL 证书服务签发的证书（含私钥），上传至该服务的证书除外 |
| 向阿里云申请证书 | 使用资源包申请证书，默认为个人测试证书；私钥与 CSR 由 JEC 生成，支持 DNS 验证或文件验证 |
| 腾讯云免费证书 | 申请腾讯云免费证书（TrustAsia DV）；域名须托管于同一账号的 DNSPod，由腾讯云自动完成验证 |

## Namecheap SSL Manager

实验性功能，须先在“模块”页启用。JEC 作为 Namecheap SSL Manager 的客户端，自动获取在 Namecheap 购买的付费证书（如 PositiveSSL）：

1. 在 Namecheap 控制台激活或重签证书时选择 Server-side automation，复制一次性令牌（有效期 24 小时，仅可使用一次）。
2. 添加该类型的证书来源并填写令牌，保存后立即测试连接以完成注册。
3. 添加证书时选择该来源。签发进入“等待来源”状态，按 Namecheap 的订单状态逐步完成提交 CSR、放置验证文件与下载证书。

仅支持单域名的 RSA 证书。完整的验证步骤见[真实账号测试指南](../development/testing.zh-CN.md)。

## 插件

插件可提供新的证书来源，包括依据 CSR 签发证书的来源与提供现成证书的来源，参见[插件](../development/plugins.zh-CN.md)。
