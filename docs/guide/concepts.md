# Concepts

[中文](concepts.zh-CN.md)

This page describes the main objects of JEC and how they relate. The pages
of the interface are organized around them.

## Certificates and versions

- **Certificate**: a set of domains, the first being the primary domain,
  with the way the certificate is obtained, its deploy targets and its
  renewal settings. A certificate is a long-lived, logical object.
- **Version**: one concrete result of an issuance or an upload, with the
  certificate chain, the private key and the validity period. Each renewal
  produces a new version with a new private key; the Versions section of
  the certificate's page lists them all.

## Providers

A provider is a configuration of an external service. Providers fall into
four kinds, each with its own page:

| Kind | Role | Examples |
|---|---|---|
| Certificate source | Issues or retrieves certificates | ACME account, Namecheap SSL Manager, certificates issued at Alibaba Cloud |
| Validation method | Proves control of the domains | DNS providers, website directories, answers by JEC |
| Deploy target | Installs certificates | Alibaba Cloud ESA site, Nginx Proxy Manager, local directory |
| Notification channel | Sends messages | Telegram, email, DingTalk, webhooks |

Each kind has several **types**, provided by modules or plugins; a type can
have several **instances**, such as two ACME accounts of different
accounts. A certificate uploaded manually has no source and needs no
validation method.

Each instance shows as a card on its page, with its main settings, how its
last test or deployment went and how many certificates use it. The card
tests, edits and deletes it; on phones, these actions are in the ⋯ menu at
the card's top right.

## Credentials

A credential is a set of keys that can be shared, such as an Alibaba Cloud
AccessKey or an SSH private key. Several providers can refer to one
credential, so that a new key is entered in one place. The Credentials page
lists the users of each credential and when it was last used.

## Issuance, renewal and deployment

- **Issuance**: the certificate source produces a new version of a
  certificate. Adding a certificate, choosing Issue now, or a renewal
  starts one; each step and its log are recorded on the issuance page.
- **Renewal**: an issuance started automatically once a certificate enters
  its renewal window; see [Renewal](../usage/renewal.md).
- **Deployment**: a new version is installed to each deploy target of the
  certificate. Every new version, issued or uploaded, is deployed
  automatically; a deployment can also be repeated by hand on the
  certificate's page.

Failed issuances and deployments are retried with exponential backoff and
can be reported by notifications. The History page lists every issuance,
renewal and deployment.

## Certificate states

| State | Meaning |
|---|---|
| Healthy | The current version is valid and outside its renewal window |
| Expiring soon | The certificate is in its renewal window |
| Expired | The current version has expired |
| Issuing | An issuance is under way or waiting on its source |
| Issuance failed | The latest issuance failed, and no newer version exists |
| Deploying | A deployment is under way |
| Deployment failed | The latest deployment to one of the targets failed |
| Revoked | The certificate authority revoked the current version (see [Revocation checks](../usage/renewal.md#revocation-checks)) |

On any page, Ctrl+K (⌘K on a Mac), or Search in the sidebar (under More on
phones), finds certificates by domain or note, pages and providers by name,
and goes to the one chosen.

## Modules and plugins

Everything outside the kernel is a module, including all provider types and
features such as the audit log, online checks and single sign-on. Modules
are enabled or disabled on the Modules page, or excluded at build time;
experimental modules are disabled by default. Plugins are external
programs that add provider types without rebuilding JEC; see
[Plugins](../development/plugins.md).
