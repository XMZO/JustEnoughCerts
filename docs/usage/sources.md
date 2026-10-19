# Certificate sources

[中文](sources.zh-CN.md)

A certificate source issues or retrieves certificates; sources are managed
on the Certificate sources page, and a certificate chooses one when it is
added. Certificates uploaded manually need no source. The test status and
vendor requirements of each type are listed in
[Providers and test coverage](../reference/providers.md).

## ACME account

The recommended source, for any certificate authority that supports the
ACME protocol (RFC 8555).

| Setting | Description |
|---|---|
| Certificate authority | Built in: Let's Encrypt, ZeroSSL, Google Trust Services, SSL.com, Sectigo, DigiCert, GlobalSign, Actalis, FreeSSL, LiteSSL, PeeringHub and more, some with a staging environment; choose Custom to enter a directory URL |
| Contact email | Optional, for notices from the certificate authority |
| EAB Key ID / EAB HMAC key | Required by certificate authorities that use External Account Binding, such as ZeroSSL and Google Trust Services |
| Profile (advanced) | An ACME profile the certificate authority offers, such as Let's Encrypt's `shortlived` (six-day certificates) or `tlsserver`; empty for the default. A test lists those offered |
| Proxy | Optional, for networks that cannot reach the certificate authority directly |

- The account is registered at the first issuance or test, and its private
  key is stored encrypted in JEC.
- DNS-01 is used with DNS providers, and HTTP-01 with website directories
  or answers by JEC.
- ACME Renewal Information (ARI, RFC 9773) is supported, so certificates
  are renewed earlier when the certificate authority advises it.

## Certificates on this machine

Reads the certificates and keys that certbot or acme.sh renews on the same
machine, read-only, without changing their files. Enter certbot's
`/etc/letsencrypt`, or the home directory of acme.sh (such as
`/root/.acme.sh`); JEC finds the certificate by its domains. While the
directory still holds the old certificate, JEC checks again six hours
later. In a container, the directory must be mounted; see
[Installation and upgrades](../guide/installation.md#optional-mounts).

## Private CA

Signs certificates with a certificate authority of your own, for internal
names such as `nas.home.arpa` and for IPv4 addresses, without validation.
Only devices that trust the CA trust its certificates, so its certificate
must be installed on them.

| Setting | Description |
|---|---|
| CA certificate | In PEM form; for an intermediate CA, add the certificates above it, which go into the chain, the root excepted |
| CA private key | In PEM form, not encrypted; stored encrypted in JEC |
| Validity (days) | Of each certificate, 90 by default and 825 at most; never past the CA's own end |

A CA can be made with OpenSSL, for example:

```sh
openssl req -x509 -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
  -keyout ca.key -out ca.crt -days 3650 -subj "/CN=Home CA" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign"
```

Keep `ca.key` private; install `ca.crt` as a trusted root on the devices.

## Cloud certificate services

| Type | Description |
|---|---|
| Certificates issued at Alibaba Cloud | Retrieves certificates, with their keys, issued by Alibaba Cloud Certificate Management Service; certificates uploaded to it are excluded |
| Certificates issued at Tencent Cloud | Retrieves certificates, with their keys, issued by Tencent Cloud SSL Certificates; certificates uploaded to it are excluded |
| Certificates applied for at Alibaba Cloud | Applies for certificates from a resource plan, personal test certificates by default; JEC generates the key and the CSR, and validates by DNS or by a file |
| Tencent Cloud free certificates | Applies for Tencent Cloud's free certificates (TrustAsia DV); the domain must be hosted in the DNSPod of the same account, which completes the validation itself |

## Namecheap SSL Manager

Experimental; enable it on the Modules page first. JEC acts as a client of
Namecheap SSL Manager to obtain paid certificates bought from Namecheap,
such as PositiveSSL:

1. In the Namecheap dashboard, activate or reissue the certificate with
   Server-side automation, and copy the one-time token (valid for 24 hours
   and for a single use).
2. Add a source of this type with the token, save, and test it at once to
   complete the registration.
3. Choose this source when adding the certificate. The issuance waits on
   the source and follows the state of the Namecheap order: it submits the
   CSR, places the validation file and downloads the certificate.

Single-domain RSA certificates only. The complete steps for verifying it
are in [Testing with real accounts](../development/testing.md).

## Plugins

Plugins can provide new certificate sources, both sources that issue from
a CSR and sources that provide ready certificates; see
[Plugins](../development/plugins.md).
