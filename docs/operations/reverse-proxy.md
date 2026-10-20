# Reverse proxy and CDN

[中文](reverse-proxy.zh-CN.md)

`docker-compose.yml` publishes JEC on `127.0.0.1:8080` only, so that the
panel is reachable from the server itself. A web server on the same
machine, such as Caddy or nginx, serves it over HTTPS and forwards the
requests to JEC. This page describes JEC's settings for it, complete
configurations for Caddy and nginx, and what to add when a CDN such as
Cloudflare or Amazon CloudFront sits in front of them.

## JEC's settings

Set them under `environment` in `docker-compose.yml`, then run
`docker compose up -d`:

| Setting | Value | Purpose |
|---|---|---|
| `JEC_BASE_URL` | The address users open, such as `https://certs.example.com`, or `https://example.com/certs/` for a sub-path | Sends the session cookie over HTTPS only; notifications link to it; with a path, JEC answers under that path |
| `JEC_TRUSTED_PROXIES` | `172.16.0.0/12`, the default of `docker-compose.yml` | The reverse proxy reaches the container from the gateway of its Docker network; JEC takes the client's address from the `X-Forwarded-For` header of such requests |
| `JEC_HSTS` | `true` | Optional; only when the panel is reached over HTTPS alone |

To check the client's address, sign in through the reverse proxy and open
the Audit log page: the From column of the sign-in must show the address
of your own computer. If every entry shows the same private address
instead, such as `192.168.16.1`, it is the gateway of the container's
network, in a range Docker was set up to use:
`docker network inspect jec_default` shows the subnet to put in
`JEC_TRUSTED_PROXIES`. Without the right setting, all users appear with
the proxy's address, and five wrong passwords lock every one of them out.

If `docker-compose.yml` still publishes `"8080:8080"`, as earlier versions
of the file did, change the line to `"127.0.0.1:8080:8080"` and add
`JEC_TRUSTED_PROXIES` once the reverse proxy works.

The systemd service takes the settings from `/var/lib/jec/config.yaml`;
restart it after the change:

```yaml
listen: 127.0.0.1:8080
base_url: https://certs.example.com
trusted_proxies: 127.0.0.1
```

## Requirements

Whatever the reverse proxy, it has to:

1. Forward every request for the panel's host name, or under its sub-path,
   with any method, and keep the path unchanged.
2. Set `X-Forwarded-For` to the client's address alone, replacing whatever
   the client sent.
3. Accept request bodies of up to 1 GiB and wait up to 10 minutes for an
   answer: restoring a backup uploads the whole database.
4. Leave caching to JEC's headers: answers of the API are `no-store`, the
   files under `/assets/` may be kept for a year, and the page is
   revalidated each time.
5. Pass the pages on unchanged: JEC's Content Security Policy blocks scripts
   injected into them.

## Caddy

Caddy obtains and renews the certificate of the panel's host name itself,
once the name resolves to the server and ports 80 and 443 are open. The
complete Caddyfile:

```caddyfile
certs.example.com {
	reverse_proxy 127.0.0.1:8080
}
```

By default, Caddy replaces `X-Forwarded-For` with the client's address,
and limits neither the size of requests nor the time an answer takes.
Apply the change with `sudo systemctl reload caddy`.

Under a sub-path (`JEC_BASE_URL=https://example.com/certs/`), JEC answers
under that path itself, so the path is forwarded unchanged:

```caddyfile
example.com {
	@jec path /certs /certs/*
	handle @jec {
		reverse_proxy 127.0.0.1:8080
	}
	handle {
		# The rest of the site, as before.
		root * /var/www/example.com
		file_server
	}
}
```

## nginx

The certificate comes from an ACME client: for example,
`sudo certbot --nginx -d certs.example.com` obtains it and sets its paths
in the configuration below. A complete configuration, such as
`/etc/nginx/conf.d/jec.conf`:

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

    # Restoring a backup uploads the whole database.
    client_max_body_size 1g;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        # The client alone, replacing what it sent.
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 10m;
    }
}
```

Without `client_max_body_size`, nginx refuses uploads larger than 1 MiB
with 413. Check and apply the change with
`sudo nginx -t && sudo systemctl reload nginx`.

Under a sub-path, the location takes the path, and `proxy_pass` has no
path of its own, so that the path is forwarded unchanged; nginx redirects
`/certs` to `/certs/` itself:

```nginx
    location /certs/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 10m;
    }
```

## Validation files answered by JEC

With the validation method "Answered by JEC" ([Domain validation](../usage/validation.md)),
the web server of each website forwards the paths of validation files to
JEC: `/.well-known/acme-challenge/`, `/.well-known/pki-validation/` and
`/.well-known/ssl-manager/`, whatever `JEC_BASE_URL` is. It does so on
port 80 without redirecting them to HTTPS, as not every certificate
authority follows redirects, and keeps the `Host` header, by which JEC
tells the files of different domains apart.

In Caddy, an `http://` site for the website's name, beside its existing
site, forwards these paths and redirects the rest to HTTPS. Caddy still
answers the challenges of its own certificates first, and passes on those
it did not start:

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

In nginx, a location in the website's `server` block for port 80, before
its redirect:

```nginx
    location ~ ^/\.well-known/(acme-challenge|pki-validation|ssl-manager)/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
    }
```

## Behind a CDN

With a CDN in front of the reverse proxy, the browser connects to the
CDN, and the CDN to the reverse proxy, the origin. In addition to the
above:

| Item | Requirement |
|---|---|
| Client address | The reverse proxy takes it from the CDN's header, for requests from the CDN's address ranges only, and passes it on as `X-Forwarded-For`; `JEC_TRUSTED_PROXIES` stays as it is |
| Caching | No rules that cache the panel's answers beyond JEC's headers, such as "cache everything" or an edge TTL that overrides the origin |
| Forwarding | All methods (GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS), cookies, query strings and headers, including `Authorization` for API tokens |
| To the origin | HTTPS, with a certificate the CDN verifies |
| Page content | Features that inject scripts into pages or rewrite them are off for the panel's host name |
| Limits | The CDN's limits on the size of requests and the time an answer may take apply; a backup too large for them is restored over an SSH tunnel (see the last section) |

### Address ranges of the CDN

The script [deploy/reverse-proxy/cdn-ranges.sh](../../deploy/reverse-proxy/cdn-ranges.sh)
downloads the published address ranges of Cloudflare or CloudFront and
writes them as settings for Caddy or nginx, together with the header that
names the client. It needs `curl`, and `jq` for CloudFront, and replaces
the file only when the download succeeds:

```sh
sudo curl -fsSLo /usr/local/bin/cdn-ranges \
  https://raw.githubusercontent.com/XMZO/JustEnoughCerts/main/deploy/reverse-proxy/cdn-ranges.sh
sudo chmod +x /usr/local/bin/cdn-ranges
sudo cdn-ranges cloudflare caddy /etc/caddy/cdn-ranges.caddy    # or: cloudfront, nginx
```

The ranges change from time to time. A weekly job, such as
`/etc/cron.weekly/cdn-ranges` (executable), keeps them current:

```sh
#!/bin/sh
/usr/local/bin/cdn-ranges cloudflare caddy /etc/caddy/cdn-ranges.caddy && systemctl reload caddy
```

In Caddy, the file goes into the global `servers` option, and
`X-Forwarded-For` carries the client the CDN names, instead of the chain
of addresses:

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

Without `header_up`, JEC sees the CDN's address for every user.

In nginx, the file is included in the panel's `server` block for
port 443, such as `include /etc/nginx/cdn-ranges.conf;`; the
`X-Forwarded-For $remote_addr` of the configuration above then carries
the client the CDN names.

### Cloudflare

1. In DNS, the record of the panel's host name is proxied.
2. Under SSL/TLS, the encryption mode is **Full (strict)**. In Flexible
   mode, Cloudflare reaches the origin over plain HTTP, and the redirect
   to HTTPS loops.
3. The origin's certificate is a Cloudflare Origin CA certificate (SSL/TLS
   → Origin Server → Create Certificate), which Cloudflare trusts: in
   Caddy, `tls /etc/caddy/origin.pem /etc/caddy/origin.key` in the
   panel's site; in nginx, `ssl_certificate` and `ssl_certificate_key`. A
   publicly trusted certificate works as well.
4. The client's address: `cdn-ranges cloudflare ...`, which reads the
   `CF-Connecting-IP` header.
5. Cloudflare caches neither pages nor API answers by default; Cache Rules
   that cache everything leave out the panel's host name.
6. Rocket Loader and other features that inject scripts are off for the
   panel's host name (Rules → Configuration Rules).
7. Bot Fight Mode can challenge requests that scripts send with API
   tokens.
8. Request bodies are limited to 100 MB on the Free and Pro plans, and
   the origin has to answer within 100 seconds.

### Amazon CloudFront

CloudFront verifies the origin's certificate against the origin's domain
name. The simplest setup gives the origin a host name of its own, such as
`origin.example.com`, which resolves to the server directly and whose
certificate Caddy obtains as usual; the panel's name, `certs.example.com`,
points to the distribution.

1. Origin: domain `origin.example.com`, protocol **HTTPS only**.
2. Behavior: viewer protocol policy **Redirect HTTP to HTTPS**; allowed
   methods **GET, HEAD, OPTIONS, PUT, POST, PATCH, DELETE**; cache policy
   **CachingDisabled**; origin request policy
   **AllViewerExceptHostHeader**, which forwards every cookie, query string
   and header, `Authorization` included, but sends the origin's own name as
   `Host`.
3. Settings: the alternate domain name `certs.example.com`, with its
   certificate in AWS Certificate Manager (us-east-1).
4. Reverse proxy: the site is `origin.example.com` instead of
   `certs.example.com`; `JEC_BASE_URL` stays `https://certs.example.com`.
5. The client's address: `cdn-ranges cloudfront ...`, with the ranges
   CloudFront connects to origins from, which reads `X-Forwarded-For` from
   the right.
6. The origin response timeout is 30 seconds by default; a large backup
   needs more (Origin settings).

To keep others from reaching the origin directly, the firewall of the
server allows port 443 from CloudFront's ranges only, such as the AWS
managed prefix list `com.amazonaws.global.cloudfront.origin-facing`; port
80 stays open for validating the origin's certificate.

### Other CDNs

Other CDNs, such as Alibaba Cloud ESA, Tencent Cloud EdgeOne, Fastly or
Akamai, follow the same requirements. Their documentation lists the
address ranges they reach origins from and the header that names the
client; for example, ESA adds `ali-real-client-ip` when Managed
Transforms are on, and lists its addresses under Origin Protection. In
nginx, in the panel's `server` block:

```nginx
    set_real_ip_from 192.0.2.0/24;    # each range of the CDN
    real_ip_header ali-real-client-ip;
```

In Caddy, the same in the global `servers` option:

```caddyfile
	servers {
		trusted_proxies static 192.0.2.0/24
		client_ip_headers ali-real-client-ip
	}
```

A CDN that appends the client to `X-Forwarded-For` instead takes
`real_ip_header X-Forwarded-For;` and `real_ip_recursive on;` in nginx,
and `trusted_proxies_strict` in Caddy.

## Without a reverse proxy

- **SSH tunnel**: on your own computer, run
  `ssh -N -L 8080:127.0.0.1:8080 <user>@<server>`, then open
  `http://localhost:8080`. This suits the first setup and occasional use.
- **Directly**: with the ports line `"8080:8080"`, the panel is reachable
  from the network over plain HTTP, so passwords and session cookies cross
  it unencrypted. Use this on a trusted network only.
- **HTTPS served by JEC**: with `JEC_TLS_CERT` and `JEC_TLS_KEY` set, JEC
  serves HTTPS itself, and can issue and renew that certificate too.

### HTTPS served by JEC

1. Add these two lines to `environment` in `docker-compose.yml`, and change
   the ports line to `"443:8080"`:

   ```yaml
   JEC_TLS_CERT: /data/tls/fullchain.pem
   JEC_TLS_KEY: /data/tls/privkey.pem
   ```

   The files are not there yet, so JEC starts with a self-signed
   certificate; the browser warns that it is not trusted, and once you
   accept, you can sign in. "Panel HTTPS" under "About" on the Settings
   page tells how it stands.
2. Under Deploy targets, add a "Local directory": directory `/data/tls`,
   "Where the files go" set to "Right in this directory".
3. Add a certificate for the panel's domain, deployed to the target of
   step 2. Once it is issued and written, JEC serves it within about 10
   seconds, and every renewal after that takes effect the same way, with
   no restart.
4. Once the browser no longer warns, set `JEC_HSTS: "true"` if you wish.

For plain HTTP as well, as for JEC to answer a CA's file validation
itself, add `JEC_HTTP_LISTEN: ":8081"` and publish `"80:8081"`: JEC
answers the requests under `/.well-known/`, and sends the rest to HTTPS.
The validation of step 3 can then be JEC answering itself, with no DNS
provider's API.

Without containers it is the same: name the files with `-tls-cert` and
`-tls-key`, or `tls_cert` and `tls_key` in the configuration file, in a
directory JEC can write to. `jec healthcheck` turns to HTTPS by itself.
