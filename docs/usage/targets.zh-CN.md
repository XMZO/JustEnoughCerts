# 部署目标

[English](targets.md)

部署目标是证书的安装位置，在“部署目标”页管理。证书可选择任意多个部署目标；每个新版本（无论签发还是上传）都会自动部署到其全部目标，失败时按指数退避自动重试。可测试的目标类型提供“测试连接”，用于检查设置而不做任何修改。

## 支持的目标

| 类别 | 目标 |
|---|---|
| 阿里云 | ESA、CDN、全站加速（DCDN）、数字证书管理服务、OSS 自定义域名、传统型负载均衡（CLB）、应用型负载均衡（ALB） |
| 其他云服务 | 腾讯云 SSL 证书服务（更新 CDN、EdgeOne、CLB 等资源）、直接部署到腾讯云 CDN 和 EdgeOne、华为云 CDN 和 ELB、七牛云 CDN、火山引擎 CDN、百度智能云 CDN、又拍云 CDN（实验性）、Cloudflare 自定义证书、AWS Certificate Manager、AWS CloudFront、Gcore CDN |
| 面板与服务器软件 | 宝塔面板（aaPanel）、1Panel、雷池 WAF、Nginx Proxy Manager、群晖 DSM、Proxmox VE、Portainer、Caddy（经管理接口）、Apache APISIX 与 Kong Gateway（经 Admin API） |
| 文件与容器 | 本地目录、SSH 服务器、Docker 容器、Kubernetes TLS Secret |
| 自定义 | Webhook 部署、插件 |

各目标的测试情况与厂商条件见[服务商支持与测试情况](../reference/providers.zh-CN.md)。

## 旧证书的处理

上传到云平台的证书统一命名为 `jec-<证书 ID>-<指纹前 12 位>`。部署新版本后，支持删除的目标会删除 JEC 此前部署的版本，不影响其他证书；例如阿里云 ESA 免费版每个站点最多 2 张自定义证书，删除旧证书可避免占满。部分目标（如 Cloudflare、Nginx Proxy Manager、Caddy）原地替换同一张证书。雷池 WAF 替换其中域名相同（或指定 ID）的证书；没有时上传一张，需在雷池里为站点选用一次。Apache APISIX 把每张证书放进单独的 SSL 对象（默认 `jec-<证书 ID>`），按证书域名生效，不用绑定；Kong Gateway 同样为每张证书维护一个证书对象，以域名作为 SNI，须使用数据库（无数据库模式不能写入）。

## 写入文件的目标

本地目录、SSH 服务器与 Docker 容器将证书写成文件，默认采用 certbot 的文件名。高级设置中可调整：

| 设置 | 说明 |
|---|---|
| 放法 | 每张证书一个以主域名命名的子目录（`*` 写作 `star`），或直接放在目录中 |
| 文件名 | `cert.pem`、`chain.pem`、`fullchain.pem`、`privkey.pem` 均可改名，可用 `{domain}` 代表主域名，例如 `{domain}.crt` |
| 合并文件 | 私钥加完整证书链的单个文件，供 HAProxy 等程序使用；填写文件名后生成 |
| PKCS #12 文件 | 供 IIS、Windows 与 Java 程序使用；填写文件名后生成，可设密码，加密方式可选 AES-256 或 3DES |
| 权限 | 证书文件默认 644，含私钥的文件默认 600；含私钥的文件不得允许其他用户读取 |
| 属主 | 本地目录与 SSH 服务器可设置“用户:组”；Docker 容器使用 `uid:gid` |

文件先写入临时文件再改名，读取方不会读到写了一半的文件。

### 写入后执行命令

- **本地目录**：由内置 shell（兼容 POSIX sh 与 Bash）在证书目录中执行，最长 5 分钟，输出记入部署日志。环境变量包括 `JEC_CERT_DIR`、`JEC_CERT_FILE`、`JEC_CHAIN_FILE`、`JEC_FULLCHAIN_FILE`、`JEC_KEY_FILE`（生成时另有 `JEC_COMBINED_FILE`、`JEC_PFX_FILE`）、`JEC_DOMAIN`、`JEC_DOMAINS`、`JEC_CERTIFICATE_ID`、`JEC_NOT_AFTER`，以及 certbot 部署钩子使用的 `RENEWED_LINEAGE`、`RENEWED_DOMAINS`。文件名未修改时，现有的 certbot 部署钩子可直接使用。
- **SSH 服务器**：在服务器上执行所设置的命令，例如 `systemctl reload nginx`。服务器须提供 POSIX shell，主机公钥必须核对。
- **Docker 容器**：写入后向容器发送信号（默认 `HUP`）、重启容器或不做操作。JEC 须能访问 Docker 套接字或套接字代理。

命令退出码非 0、语法错误或超时时，部署记为失败且不自动重试。

### 常见配置

| 服务器 | 文件 | 写入后执行的命令 |
|---|---|---|
| Nginx | `ssl_certificate` 指向 `fullchain.pem`，`ssl_certificate_key` 指向 `privkey.pem` | `nginx -s reload` 或 `systemctl reload nginx` |
| Apache | `SSLCertificateFile` 指向 `fullchain.pem`，`SSLCertificateKeyFile` 指向 `privkey.pem` | `systemctl reload apache2`（部分系统为 `httpd`） |
| HAProxy | 合并文件（如 `{domain}.pem`），放在 `crt` 指定的目录 | `systemctl reload haproxy` |
| Traefik | 在文件提供者（file provider）目录的配置文件中，于 `tls.certificates` 下写 `certFile` 与 `keyFile` | `touch` 该配置文件：Traefik 在配置文件变化时重新加载，证书文件变化不会触发（以 Traefik 3.5 实测） |

## 其他说明

- **面板的自签名证书**：宝塔面板、1Panel、雷池 WAF、Proxmox VE 与 Portainer 使用 `http://` 地址时仅允许连接内网；使用自签名证书的 HTTPS 地址时按证书指纹信任，测试连接会给出指纹。Proxmox VE 与 Portainer 之后会使用 JEC 部署的证书：JEC 按它们的域名信任该证书，无需修改指纹；地址宜填写该域名而非 IP。
- **Webhook 部署**：以 JSON（或自定义模板）将证书与私钥 POST 至指定地址；地址须使用 HTTPS，可设置签名密钥（请求头 `X-JEC-Signature`）与额外的请求头。
- **代理**：Cloudflare、AWS Certificate Manager 与 Webhook 部署可在高级设置中单独设置代理。
- **插件**：插件可提供新的部署目标，参见[插件](../development/plugins.zh-CN.md)。
