# Introduction

[中文](introduction.zh-CN.md)

Just Enough Certs (JEC) is a self-hosted SSL/TLS certificate manager. It
obtains certificates from a range of sources, completes domain validation
automatically, deploys the certificates to CDNs, load balancers, panels
and servers, and renews them before they expire.

## Use cases

- Certificates spread across several certificate authorities and cloud
  platforms, whose expiry dates should be tracked and renewals handled in
  one place.
- Websites behind CDNs, such as Alibaba Cloud ESA, Tencent Cloud EdgeOne
  or Cloudflare, where each renewed certificate must otherwise be uploaded
  by hand.
- Shorter certificate lifetimes, such as 200 days, which make manual
  replacement more frequent and more error-prone.

## How it works

1. **Obtain**: a certificate comes from a certificate source, issued by an
   ACME certificate authority, issued or retrieved through a vendor's API,
   or uploaded manually.
2. **Validate**: a validation method proves control of the domains, by
   adding DNS records or by placing validation files on the website.
3. **Deploy**: each new version is deployed to every deploy target of the
   certificate, and the replaced certificate is removed where needed.
4. **Renew**: once a certificate enters its renewal window, it is issued
   and deployed again automatically; failures are retried and reported.
5. **Check**: JEC connects to each domain to verify the certificate it
   serves, and sends a notification if it still serves an old one.

[Concepts](concepts.md) describes the objects involved and how they relate.

## Features

- [Certificate sources](../usage/sources.md): ACME, including External
  Account Binding; manual upload; certbot and acme.sh on the same machine;
  Alibaba Cloud and Tencent Cloud certificate services; a private CA;
  Namecheap SSL Manager (experimental).
- [Domain validation](../usage/validation.md): the 221 DNS providers of
  lego, arbitrary records, manual records, website directories, answers by
  JEC itself and answers at the edge of Alibaba Cloud ESA or Cloudflare.
- [Deploy targets](../usage/targets.md): 31 types, covering clouds such as
  Alibaba Cloud, Tencent Cloud, Huawei Cloud, Cloudflare, AWS and Gcore, panels
  such as BaoTa and 1Panel, and local directories, SSH servers, Docker
  containers and Kubernetes.
- [Renewal](../usage/renewal.md): renewal windows and a daily check time,
  ACME Renewal Information (ARI), automatic retries and online checks.
- [Notifications](../usage/notifications.md): 14 notification channels,
  each with its own language and events, and a weekly digest; a calendar
  feed of the ends and renewals; [Prometheus metrics](../operations/monitoring.md).
- [Accounts and security](../operations/security.md): encrypted secrets,
  two-factor authentication, single sign-on, API tokens and an audit log;
  issued certificates can be [revoked](../usage/certificates.md#revoking).
- Modules and [plugins](../development/plugins.md): everything outside the
  kernel is a module, which can be enabled, disabled or excluded at build
  time; plugins can be written in any language.
- Lightweight deployment: a single static binary or a small container
  image, with data in SQLite (or [PostgreSQL](../operations/postgresql.md)),
  and an interface in English and Chinese
  that can be installed on a phone's home screen as a progressive web app
  (PWA).

## Project status

JEC is in early development and has no stable release yet. The test
releases are 0.1.0-alpha.x, with the image tag `beta`. Deployment to
Alibaba Cloud ESA, Namecheap SSL Manager and Cloudflare edge answers have
been verified with a real account; types tested only with a fake are
tagged "Untested" in the web UI and may not work. See
[Providers and test coverage](../reference/providers.md) for the test
status of every provider.

JEC is released under the MIT license. Its name and visual style pay
homage to the Minecraft mod JEI.
