# Deploy targets

[中文](targets.zh-CN.md)

A deploy target is a place where certificates are installed; targets are
managed on the Deploy targets page. A certificate can have any number of
targets. Every new version, issued or uploaded, is deployed to all of them
automatically, and failures are retried with exponential backoff. Types
that support it offer Test, which checks the settings without changing
anything.

## Supported targets

| Category | Targets |
|---|---|
| Alibaba Cloud | ESA, CDN, DCDN, Certificate Management Service, OSS custom domains, CLB and ALB load balancers |
| Other clouds | Tencent Cloud SSL Certificates (updating CDN, EdgeOne, CLB and other resources), Tencent Cloud CDN and EdgeOne directly, Huawei Cloud CDN and ELB, Qiniu Cloud CDN, Volcengine CDN, Baidu AI Cloud CDN, UPYUN CDN (experimental), Cloudflare custom certificates, AWS Certificate Manager, AWS CloudFront, Gcore CDN |
| Panels and server software | BaoTa (aaPanel), 1Panel, SafeLine WAF, Nginx Proxy Manager, Synology DSM, Proxmox VE, Portainer, Caddy (through its admin API), Apache APISIX and Kong Gateway (through their Admin APIs) |
| Files and containers | Local directory, SSH server, Docker container, Kubernetes TLS Secret |
| Custom | Webhook deployment, plugins |

The test status and vendor requirements of each target are listed in
[Providers and test coverage](../reference/providers.md).

## Replaced certificates

Certificates uploaded to cloud platforms are named
`jec-<certificate ID>-<first 12 characters of the fingerprint>`. After
deploying a new version, targets that support it remove the versions JEC
deployed there before, leaving other certificates untouched; the free plan
of Alibaba Cloud ESA, for example, holds at most two custom certificates
per site. Some targets, such as Cloudflare, Nginx Proxy Manager and Caddy,
replace the same certificate in place. SafeLine WAF replaces its
certificate for the same domains, or the one set by its ID; with none, it
uploads one, for the sites to be given in SafeLine once. Apache APISIX
keeps each certificate in an SSL object of its own, `jec-<certificate ID>`
unless set, which serves the certificate's domains without binding; Kong
Gateway likewise keeps a certificate object with the domains as its SNIs,
and needs a database, as its DB-less mode is read-only.

## Targets that write files

The local directory, the SSH server and the Docker container write
certificates as files, with certbot's file names by default. The advanced
settings adjust them:

| Setting | Description |
|---|---|
| Layout | A subdirectory per certificate named after its primary domain (`*` written as `star`), or the files directly in the directory |
| File names | `cert.pem`, `chain.pem`, `fullchain.pem` and `privkey.pem` can be renamed; `{domain}` stands for the primary domain, as in `{domain}.crt` |
| Combined file | The private key and the full chain in one file, as HAProxy expects; written when named |
| PKCS #12 file | For IIS, Windows and Java programs; written when named, optionally with a password, encrypted with AES-256 or 3DES |
| Permissions | 644 for certificates and 600 for files with the private key by default; files with the private key may not be readable by others |
| Owner | `user:group` for the local directory and the SSH server; `uid:gid` for the Docker container |

Each file is written to a temporary file and then renamed, so a reader
never sees a partly written file.

### Commands after writing

- **Local directory**: the built-in shell, compatible with POSIX sh and
  Bash, runs the command in the certificate's directory, for at most five
  minutes, and its output goes to the deployment log. The environment
  includes `JEC_CERT_DIR`, `JEC_CERT_FILE`, `JEC_CHAIN_FILE`,
  `JEC_FULLCHAIN_FILE` and `JEC_KEY_FILE` (and `JEC_COMBINED_FILE` and
  `JEC_PFX_FILE` when written), `JEC_DOMAIN`, `JEC_DOMAINS`,
  `JEC_CERTIFICATE_ID`, `JEC_NOT_AFTER`, and certbot's `RENEWED_LINEAGE`
  and `RENEWED_DOMAINS`. With the default file names, existing certbot
  deploy hooks work unchanged.
- **SSH server**: the command, such as `systemctl reload nginx`, runs on
  the server. The server needs a POSIX shell, and its host key must be
  verified.
- **Docker container**: after writing, JEC sends the container a signal
  (`HUP` by default), restarts it, or does nothing. JEC needs access to the
  Docker socket or to a socket proxy.

A non-zero exit status, a syntax error or a timeout fails the deployment,
which is then not retried automatically.

### Common setups

| Server | Files | Command after writing |
|---|---|---|
| Nginx | `ssl_certificate` to `fullchain.pem`, `ssl_certificate_key` to `privkey.pem` | `nginx -s reload` or `systemctl reload nginx` |
| Apache | `SSLCertificateFile` to `fullchain.pem`, `SSLCertificateKeyFile` to `privkey.pem` | `systemctl reload apache2` (`httpd` on some systems) |
| HAProxy | A combined file, such as `{domain}.pem`, in the directory of `crt` | `systemctl reload haproxy` |
| Traefik | `certFile` and `keyFile` in a file of the file provider's directory, under `tls.certificates` | `touch` that file: Traefik reloads on a change of its configuration, not of the certificate files (tried with Traefik 3.5) |

## Other notes

- **Self-signed panels**: BaoTa, 1Panel, SafeLine WAF, Proxmox VE and
  Portainer accept `http://` addresses only on private networks; for HTTPS
  with a self-signed certificate, they trust the certificate's
  fingerprint, which the connection test reports. Proxmox VE and Portainer
  then serve the certificate JEC deploys: JEC trusts it for their domain
  with no change to the fingerprint, and the address is best given by
  that domain rather than an IP address.
- **Webhook deployment**: posts the certificate and its private key as
  JSON, or in a custom template, to an HTTPS address, optionally with a
  signing key (header `X-JEC-Signature`) and extra headers.
- **Proxy**: Cloudflare, AWS Certificate Manager and the webhook
  deployment accept a proxy of their own in the advanced settings.
- **Plugins**: plugins can provide new deploy targets; see
  [Plugins](../development/plugins.md).
