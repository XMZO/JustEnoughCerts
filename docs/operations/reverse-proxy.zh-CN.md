# 反向代理与 CDN

[English](reverse-proxy.md)

`docker-compose.yml` 仅在 `127.0.0.1:8080` 上发布 JEC，面板只能从服务器本机访问。由同一台服务器上的 Web 服务器（如 Caddy 或 nginx）以 HTTPS 对外提供面板，并将请求转发至 JEC。本页说明 JEC 的相应设置、Caddy 与 nginx 的完整配置，以及前面再加一层 CDN（如 Cloudflare、Amazon CloudFront）时需要补充的设置。

## JEC 的设置

在 `docker-compose.yml` 的 `environment` 中设置，然后执行 `docker compose up -d`：

| 设置 | 取值 | 作用 |
|---|---|---|
| `JEC_BASE_URL` | 用户访问的地址，例如 `https://certs.example.com`；子路径如 `https://example.com/certs/` | 会话 Cookie 仅经 HTTPS 发送；通知中的链接使用该地址；含路径时，JEC 在该路径下应答 |
| `JEC_TRUSTED_PROXIES` | `172.16.0.0/12`，即 `docker-compose.yml` 的默认值 | 反向代理经容器所在 Docker 网络的网关访问 JEC；对来自这些地址的请求，JEC 从 `X-Forwarded-For` 读取客户端地址 |
| `JEC_HSTS` | `true` | 可选；仅当面板始终经 HTTPS 访问时设置 |

核对客户端地址：经反向代理登录后打开“审计日志”页，登录记录的“来源地址”应为自己电脑的地址。如所有记录都显示同一个内网地址（例如 `192.168.16.1`），该地址即容器网络的网关，说明 Docker 被设置为使用其他网段：执行 `docker network inspect jec_default` 查看子网，填入 `JEC_TRUSTED_PROXIES`。设置不正确时，所有用户都显示为代理的地址，任何人连续输错 5 次密码都会导致所有人被锁定。

如 `docker-compose.yml` 仍为早期版本中的 `"8080:8080"`，请在反向代理配置完成后，将该行改为 `"127.0.0.1:8080:8080"`，并添加 `JEC_TRUSTED_PROXIES`。

systemd 服务的设置位于 `/var/lib/jec/config.yaml`，修改后重启服务：

```yaml
listen: 127.0.0.1:8080
base_url: https://certs.example.com
trusted_proxies: 127.0.0.1
```

## 基本要求

无论使用哪种反向代理，均须做到：

1. 转发面板域名（或其子路径）下的全部请求，不限请求方法，路径保持不变。
2. 将 `X-Forwarded-For` 设为客户端的地址，且仅含这一个地址，覆盖客户端自带的值。
3. 允许最大 1 GiB 的请求体，等待应答最长 10 分钟：恢复备份时将上传整个数据库。
4. 缓存遵循 JEC 的响应头：API 的应答为 `no-store`，`/assets/` 下的文件可缓存一年，页面每次重新验证。
5. 原样传递页面：JEC 的内容安全策略会拦截注入页面的脚本。

## Caddy

域名解析到服务器、80 与 443 端口可访问时，Caddy 自动申请并续期面板域名的证书。完整的 Caddyfile：

```caddyfile
certs.example.com {
	reverse_proxy 127.0.0.1:8080
}
```

Caddy 默认将 `X-Forwarded-For` 设为客户端的地址，且不限制请求大小与应答时间。执行 `sudo systemctl reload caddy` 使修改生效。

使用子路径时（`JEC_BASE_URL=https://example.com/certs/`），JEC 自行在该路径下应答，因此转发时保持路径不变：

```caddyfile
example.com {
	@jec path /certs /certs/*
	handle @jec {
		reverse_proxy 127.0.0.1:8080
	}
	handle {
		# 网站的其他部分，保持原样。
		root * /var/www/example.com
		file_server
	}
}
```

## nginx

证书由 ACME 客户端获取，例如执行 `sudo certbot --nginx -d certs.example.com` 获取证书，并将证书路径写入下面的配置。完整配置（如 `/etc/nginx/conf.d/jec.conf`）：

```nginx
server {
    listen 80;
    listen [::]:80;
    server_name certs.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    server_name certs.example.com;

    ssl_certificate     /etc/letsencrypt/live/certs.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/certs.example.com/privkey.pem;

    # 恢复备份时将上传整个数据库。
    client_max_body_size 1g;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        # 仅含客户端地址，覆盖其自带的值。
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 10m;
    }
}
```

未设置 `client_max_body_size` 时，nginx 以 413 拒绝大于 1 MiB 的上传。执行 `sudo nginx -t && sudo systemctl reload nginx` 检查并应用修改。

使用子路径时，`location` 写该路径，`proxy_pass` 不带路径，使路径原样转发；nginx 会自动将 `/certs` 重定向至 `/certs/`：

```nginx
    location /certs/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 10m;
    }
```

## 由 JEC 应答验证文件

使用验证方式“JEC 自己应答”时（参见[域名验证](../usage/validation.zh-CN.md)），各网站的 Web 服务器须将验证文件所在的路径转发至 JEC：`/.well-known/acme-challenge/`、`/.well-known/pki-validation/` 与 `/.well-known/ssl-manager/`，与 `JEC_BASE_URL` 无关。转发须在 80 端口上进行，不重定向至 HTTPS，因为并非所有证书颁发机构都跟随重定向；同时保留 `Host` 请求头，JEC 据此区分不同域名的文件。

Caddy：在网站原有站点之外，为网站域名另加一个 `http://` 站点，转发上述路径，其余请求重定向至 HTTPS。Caddy 仍会优先应答其自身证书的验证，并放行不是由它发起的验证：

```caddyfile
http://www.example.org {
	@validation path /.well-known/acme-challenge/* /.well-known/pki-validation/* /.well-known/ssl-manager/*
	handle @validation {
		reverse_proxy 127.0.0.1:8080
	}
	handle {
		redir https://{host}{uri} permanent
	}
}
```

nginx：在网站 80 端口的 `server` 块中、重定向之前添加：

```nginx
    location ~ ^/\.well-known/(acme-challenge|pki-validation|ssl-manager)/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
    }
```

## 前置 CDN

反向代理前面再加一层 CDN 时，浏览器连接 CDN，CDN 再连接反向代理（即源站）。除上述设置外，还须满足：

| 项目 | 要求 |
|---|---|
| 客户端地址 | 反向代理仅对来自 CDN 地址段的请求，从 CDN 的请求头读取客户端地址，再以 `X-Forwarded-For` 传给 JEC；`JEC_TRUSTED_PROXIES` 保持不变 |
| 缓存 | 不对面板设置超出 JEC 响应头的缓存规则，例如“缓存所有内容”或覆盖源站的边缘缓存时间 |
| 转发 | 全部请求方法（GET、HEAD、POST、PUT、PATCH、DELETE、OPTIONS）、Cookie、查询字符串与请求头，包括 API 令牌使用的 `Authorization` |
| 回源 | 使用 HTTPS，源站证书须能通过 CDN 的校验 |
| 页面内容 | 对面板域名关闭会注入脚本或改写页面的功能 |
| 限制 | CDN 对请求大小与应答时间的限制同样适用；超出限制的备份请经 SSH 隧道恢复（见最后一节） |

### CDN 的地址段

脚本 [deploy/reverse-proxy/cdn-ranges.sh](../../deploy/reverse-proxy/cdn-ranges.sh) 下载 Cloudflare 或 CloudFront 公布的地址段，并连同记录客户端地址的请求头一起，写成 Caddy 或 nginx 的设置。脚本需要 `curl`（CloudFront 另需 `jq`），仅在下载成功时替换文件：

```sh
sudo curl -fsSLo /usr/local/bin/cdn-ranges \
  https://raw.githubusercontent.com/XMZO/JustEnoughCerts/main/deploy/reverse-proxy/cdn-ranges.sh
sudo chmod +x /usr/local/bin/cdn-ranges
sudo cdn-ranges cloudflare caddy /etc/caddy/cdn-ranges.caddy    # 或 cloudfront、nginx
```

地址段会不定期变化，可用每周任务保持更新，例如可执行文件 `/etc/cron.weekly/cdn-ranges`：

```sh
#!/bin/sh
/usr/local/bin/cdn-ranges cloudflare caddy /etc/caddy/cdn-ranges.caddy && systemctl reload caddy
```

Caddy：在全局选项 `servers` 中导入该文件，并让 `X-Forwarded-For` 只携带 CDN 指明的客户端，而不是整条地址链：

```caddyfile
{
	servers {
		import /etc/caddy/cdn-ranges.caddy
	}
}

certs.example.com {
	reverse_proxy 127.0.0.1:8080 {
		header_up X-Forwarded-For {client_ip}
	}
}
```

缺少 `header_up` 时，JEC 看到的所有用户都是 CDN 的地址。

nginx：在面板 443 端口的 `server` 块中引入该文件，例如 `include /etc/nginx/cdn-ranges.conf;`；上文配置中的 `X-Forwarded-For $remote_addr` 随即携带 CDN 指明的客户端。

### Cloudflare

1. DNS 中，面板域名的记录开启代理。
2. “SSL/TLS”的加密模式选择 **Full (strict)**。Flexible 模式下 Cloudflare 以明文 HTTP 回源，与源站的 HTTPS 重定向形成循环。
3. 源站证书使用 Cloudflare Origin CA 证书（“SSL/TLS”→“Origin Server”→“Create Certificate”），Cloudflare 信任该证书：Caddy 在面板站点中写 `tls /etc/caddy/origin.pem /etc/caddy/origin.key`，nginx 写入 `ssl_certificate` 与 `ssl_certificate_key`。也可使用公开信任的证书。
4. 客户端地址：`cdn-ranges cloudflare ...`，读取 `CF-Connecting-IP` 请求头。
5. Cloudflare 默认不缓存页面与 API 的应答；“缓存所有内容”类的缓存规则须排除面板域名。
6. 对面板域名关闭 Rocket Loader 等会注入脚本的功能（“Rules”→“Configuration Rules”）。
7. Bot Fight Mode 可能拦截脚本使用 API 令牌发出的请求。
8. Free 与 Pro 套餐的请求体上限为 100 MB，源站须在 100 秒内应答。

### Amazon CloudFront

CloudFront 按源站的域名校验源站证书。最简单的做法是为源站另设一个域名，例如 `origin.example.com`，直接解析到服务器，由 Caddy 照常申请证书；面板域名 `certs.example.com` 指向分配（distribution）。

1. 源（Origin）：域名 `origin.example.com`，协议 **HTTPS only**。
2. 行为（Behavior）：查看器协议策略 **Redirect HTTP to HTTPS**；允许的方法 **GET, HEAD, OPTIONS, PUT, POST, PATCH, DELETE**；缓存策略 **CachingDisabled**；源请求策略 **AllViewerExceptHostHeader**，转发全部 Cookie、查询字符串与请求头（包括 `Authorization`），但以源站自己的域名作为 `Host`。
3. 设置：备用域名 `certs.example.com`，其证书位于 AWS Certificate Manager（us-east-1）。
4. 反向代理：站点改为 `origin.example.com`；`JEC_BASE_URL` 仍为 `https://certs.example.com`。
5. 客户端地址：`cdn-ranges cloudfront ...`，使用 CloudFront 回源的地址段，从右向左读取 `X-Forwarded-For`。
6. 源站响应超时默认为 30 秒，恢复较大的备份时须调大（源站设置）。

如需禁止绕过 CloudFront 直接访问源站，可在服务器防火墙上仅允许 CloudFront 的地址段访问 443 端口，例如 AWS 托管前缀列表 `com.amazonaws.global.cloudfront.origin-facing`；80 端口须保持开放，用于验证源站证书。

### 其他 CDN

阿里云 ESA、腾讯云 EdgeOne、Fastly、Akamai 等 CDN 适用相同的要求。各家文档列出其回源地址段与记录客户端地址的请求头；例如阿里云 ESA 开启“托管转换”后添加 `ali-real-client-ip` 请求头，回源地址在“源站防护”中列出。nginx 在面板的 `server` 块中添加：

```nginx
    set_real_ip_from 192.0.2.0/24;    # CDN 的每个地址段各一行
    real_ip_header ali-real-client-ip;
```

Caddy 在全局选项 `servers` 中添加：

```caddyfile
	servers {
		trusted_proxies static 192.0.2.0/24
		client_ip_headers ali-real-client-ip
	}
```

将客户端追加到 `X-Forwarded-For` 的 CDN，nginx 改用 `real_ip_header X-Forwarded-For;` 与 `real_ip_recursive on;`，Caddy 改用 `trusted_proxies_strict`。

## 不使用反向代理

- **SSH 隧道**：在自己的电脑上执行 `ssh -N -L 8080:127.0.0.1:8080 <用户>@<服务器>`，然后打开 `http://localhost:8080`。适合首次初始化与临时使用。
- **直接访问**：将端口一行改为 `"8080:8080"` 后，面板以明文 HTTP 对网络开放，密码与会话 Cookie 均不加密传输。仅限可信网络使用。
- **由 JEC 提供 HTTPS**：设置 `JEC_TLS_CERT` 与 `JEC_TLS_KEY` 后，JEC 直接提供 HTTPS，证书也可以由 JEC 自己签发和续期。

### 由 JEC 提供 HTTPS

1. 在 `docker-compose.yml` 的 `environment` 中加入以下两行，并将端口一行改为 `"443:8080"`：

   ```yaml
   JEC_TLS_CERT: /data/tls/fullchain.pem
   JEC_TLS_KEY: /data/tls/privkey.pem
   ```

   文件尚不存在，JEC 先以自签名证书启动，浏览器会提示证书不受信任，确认后即可登录。设置页“运行环境”中的“面板 HTTPS”显示当前状态。
2. 在“部署目标”中添加“本地目录”：目录填 `/data/tls`，“文件放在哪里”选“直接放在这个目录里”。
3. 为面板的域名添加证书，部署目标选第 2 步添加的目录。证书签发并写入后，JEC 约 10 秒内换用该证书；此后每次续期同样自动生效，无需重启。
4. 确认浏览器不再提示后，可按需设置 `JEC_HSTS: "true"`。

需要普通 HTTP 时（例如让 JEC 自己应答证书颁发机构的文件验证），再加入 `JEC_HTTP_LISTEN: ":8081"`，并发布 `"80:8081"`：`/.well-known/` 下的请求由 JEC 应答，其余请求跳转到 HTTPS。这样第 3 步的验证方式可以选“JEC 自己应答”，不需要 DNS 服务商的 API。

不使用容器时同理：以 `-tls-cert`、`-tls-key` 参数或配置文件中的 `tls_cert`、`tls_key` 指定文件，目录须是 JEC 能写入的位置。`jec healthcheck` 会自动改用 HTTPS。
