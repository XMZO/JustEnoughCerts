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
account manages), and can add records of any type; with one of them
chosen, adding a certificate offers the account's domains (the first 20
of up to 50) to pick, a list not yet tried with real accounts.

- **Arbitrary records**: the TXT and CNAME records that some certificate
  vendors require, such as Namecheap and Alibaba Cloud, are added
  automatically at the four providers above and removed after issuance.
- **CNAME delegation**: with ACME, `_acme-challenge.<domain>` can point
  permanently, through a CNAME, into another zone hosted at a supported
  provider; the validation record is then added under the name the CNAME
  points to. The certificate's DNS provider must then host the zone the
  CNAME points into; Try shows where the record went, and the account
  check while adding a certificate looks at that zone. A CNAME that
  `_acme-challenge` only gets from a wildcard (such as `*.example.com`
  pointing to a CDN) is no delegation: JEC tells the two apart and adds
  the record in the domain's own zone.
- **`_acme-challenge` taken by a CDN**: some CDNs have `_acme-challenge`
  point to a name of theirs, for certificates of their own, as Cloudflare's
  DCV delegation for custom hostnames does (to
  `<domain>.<ID>.dcv.cloudflare.com`). Only the CDN can add records
  there, so while that CNAME stays, the domain cannot be validated through
  DNS: use HTTP validation, such as Answered at Cloudflare's edge below,
  which rules out wildcards. The account check while adding a certificate, Try
  and the issuance log say where the CNAME points.
- **Try**: Try, on each provider's card on the DNS page, does what an
  issuance would: it adds a TXT record at `_acme-challenge` under the
  domain given, waits up to 40 seconds for the authoritative DNS to answer
  it, and removes it, without a CA, to make sure of the keys and the
  domain's access before issuing. A provider's error shows as it gave it.
  Tried against RFC 2136 dynamic updates (BIND). For validation through
  files on the website, Try puts a test file under
  `/.well-known/acme-challenge/`, fetches it from the domain over
  `http://` as a CA would, following redirects, and tells what the
  address answered when it is not the file, as a wrong directory's 404.
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
sign-in; [Reverse proxy and CDN](../operations/reverse-proxy.md#validation-files-answered-by-jec)
has the configurations for Caddy and nginx. The connection test fetches a
temporary file from the internet through the test domain, to confirm that
the forwarding works.

## Answered at ESA's edge

For websites on Alibaba Cloud ESA, an Edge Routine deployed by JEC answers
the validation requests from Edge KV at the edge, without reaching the
origin. On first use, JEC creates the Edge KV namespace `jec_validation`,
the Edge Routine `jec-validation` and the site's route for `/.well-known/`.
The ESA plan must include Functions and Pages, and Edge KV.

## Answered at Cloudflare's edge

For websites Cloudflare proxies, including custom hostnames of Cloudflare
for SaaS, a Worker deployed by JEC answers the validation requests from
Workers KV at the edge, without reaching the origin; the website's server
needs no change. On first use, JEC creates the Workers KV namespace
`jec_validation`, the Worker `jec-validation`, a route of the domain's
`/.well-known/` to it (for a custom hostname, of `/.well-known/` on every
host of the SaaS zone, which passes what it has no file for on to the
origin), and a custom rule, first in the zone, that lets the requests for
these files past the zone's security, which certificate authorities and
vendors such as Namecheap would otherwise be challenged by.

The API token needs Workers Scripts and Workers KV Storage of the account
and Workers Routes and Zone of the zone, to edit, and Zone WAF of the zone,
to edit, for the rule. The account's email with its Global API Key works
too, though it can do anything the account can. The zone and its
credentials are those of the account that serves the website: for a
custom hostname, the account of the SaaS zone. Bot Fight Mode cannot be skipped by a rule; turn it
off if the zone has it on. For a custom hostname, enter the SaaS zone as
the zone.

## Plugins

Plugins can provide new validation methods, both DNS methods, which may
declare support for arbitrary records, and HTTP methods; see
[Plugins](../development/plugins.md).
