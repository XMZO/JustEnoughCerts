# Domain validation

[中文](validation.zh-CN.md)

Before issuing a certificate, the certificate authority requires proof of
control of the domains. Validation methods are managed on the Domain
validation page and fall into two groups, DNS and HTTP. When a certificate
is added, only the methods its source can use are offered.

| Method | How it works | Applies to |
|---|---|---|
| DNS | A TXT record is added to the domain's DNS | Every certificate, wildcards included |
| HTTP | A validation file is served under `http://<domain>/.well-known/` | Certificates without wildcards, whose domains the certificate authority can reach over HTTP |

## DNS providers

Through [lego](https://github.com/go-acme/lego), JEC supports 221 DNS
providers, whose form fields match lego's environment variables. Tencent
Cloud DNSPod, Alibaba Cloud DNS, Cloudflare and Huawei Cloud DNS also
support credentials and connection tests (which list the domains the
account manages), and can add records of any type.

- **Arbitrary records**: the TXT and CNAME records that some certificate
  vendors require, such as Namecheap and Alibaba Cloud, are added
  automatically at the four providers above and removed after issuance.
- **CNAME delegation**: with ACME, `_acme-challenge.<domain>` can point
  permanently, through a CNAME, into another zone hosted at a supported
  provider; the validation record is then added under the name the CNAME
  points to.
- **Propagation check**: JEC queries the domain's authoritative DNS
  servers directly, and asks the certificate authority to validate only
  once the record is visible. If the machine's own resolver answers
  incorrectly, `JEC_DNS_RESOLVERS` sets other resolvers; see the
  [Configuration reference](../operations/configuration.md).

## DNS records added manually

For DNS providers without an API, or not supported. The issuance log lists
each record to add; once it is added at the DNS provider, JEC finds it on
the authoritative DNS servers and continues, without further confirmation.
JEC waits 20 minutes by default, at most 25. The records must be added
again at every renewal, and removed by hand.

## Website directories

| Type | Description |
|---|---|
| Website directory | Writes the validation files into the document root of a web server on this machine, or mounted into the container; all domains share one directory, or each has a subdirectory |
| Website directory over SSH | Writes the validation files into a website directory on a remote server over SSH; the connection settings are those of the SSH deploy target, and the host key must be verified |

Validation files are written under `/.well-known/` only, and removed after
the validation.

## Answered by JEC

JEC answers the validation requests under `/.well-known/` itself, without
a website directory. The website's reverse proxy or CDN must forward the
`/.well-known/` requests of these domains to JEC, without requiring a
sign-in. The connection test fetches a temporary file from the internet
through the test domain, to confirm that the forwarding works.

## Answered at ESA's edge

For websites on Alibaba Cloud ESA, an Edge Routine deployed by JEC answers
the validation requests from Edge KV at the edge, without reaching the
origin. On first use, JEC creates the Edge KV namespace `jec_validation`,
the Edge Routine `jec-validation` and the site's route for `/.well-known/`.
The ESA plan must include Functions and Pages, and Edge KV.

## Plugins

Plugins can provide new validation methods, both DNS methods, which may
declare support for arbitrary records, and HTTP methods; see
[Plugins](../development/plugins.md).
