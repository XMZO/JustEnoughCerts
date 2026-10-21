# Certificates

[中文](certificates.zh-CN.md)

The Certificates page lists every certificate with its state, remaining
validity, source, deploy targets and next renewal; on phones, certificates
that need attention are listed separately with the reason. The states are
described in [Concepts](../guide/concepts.md#certificate-states).

## Adding a certificate

On the Certificates page, choose Add certificate. How a certificate is
added depends on its source:

| Method | Description |
|---|---|
| Issued by a source | Choose a certificate source, enter the domains, and choose a validation method and a key type. Issuance starts on submission, and the certificate is renewed automatically from then on |
| Manual upload | Choose manual upload as the source, then paste PEM text (the certificate, its intermediates and the private key, in any order) or upload a PFX/P12 file with its password. Such certificates are not renewed automatically; reminders are sent before they expire |
| Import a certificate | Choose Import a certificate; this works as a manual upload and suits the migration of existing certificates |
| Make a CSR | For a certificate bought from a CA by hand; see below |
| Several at once | With a source, choose Several above the domains and enter a certificate a line, its domains apart by spaces or commas; the rest of the form applies to each. Lines that cannot be added stay, with the reason, for another try |

Domains are handled as follows:

- The first domain is the primary domain; wildcards are written as
  `*.example.com`.
- Domains are converted to lowercase, internationalized domain names to
  Punycode, and duplicates are removed.
- Wildcard certificates require DNS validation; with `*.example.com` alone,
  the page offers to add `example.com` itself.
- IP addresses, such as `192.0.2.1` and `2001:db8::1`, are taken too, and
  only a validation through files on the web server proves them; Let's
  Encrypt certifies addresses with its `shortlived` profile only (see
  [Certificate sources](sources.md)).
- The page starts from the source, DNS provider and targets the last
  certificate was added with, as this browser remembers them. Add one like
  this, in the list's menu and the certificate's information, starts from
  that certificate's source, validation, targets, renewal window and key
  type instead.
- With Tencent Cloud DNSPod, Alibaba Cloud DNS, Cloudflare or Huawei Cloud
  DNS, the page checks that the account holds each domain, and says so
  when one is missing, before an issuance fails over it. The check was
  tried only with a fake: take it as a hint.
- With a DNS provider whose name servers are well known, such as
  Cloudflare, DNSPod, Alibaba Cloud, Route 53, GoDaddy or Namecheap, the
  page looks up the name servers of each domain's zone, following a CNAME
  of `_acme-challenge`, and says so when they are not that provider's.
- With a source that holds certificates already, as Certificates on this
  machine (certbot, acme.sh) or those fetched from Alibaba Cloud or
  Tencent Cloud, the page lists those not yet added with it: one fills in
  its domains, and under Several, Add all fills in a line each.
- The same certificate, by fingerprint, can be added only once.

The key type is RSA 2048, 3072 or 4096, or ECDSA P-256 or P-384; the
default is set under Renewal defaults on the Settings page. Every renewal
generates a new private key.

When a certificate is uploaded without its intermediates, as when copied
from a CA's email, JEC fetches them from the issuer address the
certificate names (AIA) to complete the chain, which some clients, such
as Android and curl, need once it is deployed.

Download all, on the certificate list, packs the version in use of every
certificate with its private key into one ZIP, a folder each, to keep
offline or to move elsewhere; it asks for the password first, and the
audit log records each certificate.

To move from another tool, choose Several files under Manual upload and
pick all the certificate and key files it exported at once (PEM, CRT, CER,
KEY and so on, apart or mixed): JEC pairs each key with its certificate by
public key, completes the chain and adds each; certificates it has already
stay as they are, and certificates without a key and files it cannot read
are listed.

A note, under the certificate's information on its page, says what the
certificate is for or whose it is; the list shows it, searches it and
exports it.

A reminder, in the same place, takes a day and what of, such as "Renew
the PositiveSSL subscription": on that day at 9:00, in the panel's time
zone, the notification channels remind you once (the event "When a
reminder set on a certificate comes due"), and the calendar feed shows it;
`reminder` (`day`, `note`) in the configuration file. It suits a
certificate bought, which cannot be reissued once its subscription ends.

On wider screens, certificates checked in the list (the box in the header
checks all those shown) can be renewed, deployed again or deleted
together, after a confirmation, their renewal paused or resumed, and a
deploy target added to them or taken off them, as when moving to another
CDN, and their validation changed, as when their DNS moves.

The list's search finds certificates by domain and by note. A host name,
or a URL pasted in, also finds the wildcard certificate that covers it:
`api.shop.com` finds `*.shop.com`.

### Certificates bought from a CA

For a certificate bought from a seller that issues it for a CSR, such as
a PositiveSSL certificate activated by hand, JEC can make the private key
and the CSR; the key is kept encrypted in JEC and goes nowhere else:

1. When adding the certificate, choose Make a CSR as the source, enter
   the domains and choose the key type (commercial certificates generally
   take RSA 2048). The page then shows the CSR, to copy or to download as
   a `.csr` file.
2. Hand the CSR to the seller where the certificate is activated or
   reissued, and validate the domains as it asks.
3. Once the certificate is issued, choose Upload the certificate on its
   page and paste the certificate alone, intermediates included if you
   wish, leaving the private key empty: JEC pairs it with the key it made
   for the CSR. Uploading it on the Add certificate page finds the right
   certificate too.

While it waits, the certificate shows as Awaiting certificate and is among
those that need attention. To renew it, choose Make a CSR on its page for
a new key and CSR; a reissue for the same CSR pairs with the key of the
version in use.

## The certificate's page

The certificate's page shows:

- the remaining validity, the issue and expiry dates, the validity period
  and the next renewal;
- the deploy targets, with the result of the latest deployment to each;
- the steps and duration of the last renewal, or of the latest issuance;
- the result of the online check (see [Renewal](renewal.md#online-checks));
- the versions and the certificate chain;
- the certificate details (domains, issuer, source, key, serial number
  and SHA-256 fingerprint) and the renewal settings.

## Actions

| Action | Description |
|---|---|
| Issue now / Renew now | Starts an issuance at once, without waiting for the renewal time |
| Change the domains | The pencil beside Domains in the certificate's information: changes the domains of a certificate with a source, which is issued again at once; not while an issuance is under way. An uploaded certificate takes its domains from its file |
| Upload new certificate | Adds a new version. The new certificate must include the primary domain and expire later than the current version |
| Choose targets | Adds or removes deploy targets. A new target receives the current version at once |
| Deploy now / Deploy again | Deploys the current version again, to every target or to one |
| Download | Downloads a ZIP file with `cert.pem`, `chain.pem`, `fullchain.pem` and `privkey.pem`, and optionally `certificate.pfx` for Windows, IIS, Azure and Java, with a password of its own and, for older systems, 3DES. As it contains the private key, the password is required first |
| Change the validation or the key type | In the renewal settings, from the next issuance on, as when the domain's DNS moves to another provider |
| Change the source | In the renewal settings, from the next issuance on; the versions so far stay. An uploaded certificate is renewed from then on, and one certbot renewed can be issued by an ACME account instead. Not while an issuance is under way; a configuration file does not change it |
| Deploy window | In the renewal settings, the hours new versions go to the targets in, as 01:00 to 05:00 in the panel's time zone, to reload no server by day; the online check counts from then. Deploying by hand starts at once; `deploy_window` in the configuration file |
| Pause the renewal | In the renewal settings: the certificate is no longer issued by itself, only when you choose Issue now, and its end is still told. For a domain whose DNS moves, or that is going away; `paused` in the configuration file |
| Go back to an earlier version | From the version history, makes an earlier version current again, if it has not expired or been revoked and is for the same domains, and deploys it to every target: for when a new version causes trouble, as a chain some clients do not take. The renewal then goes by its end |
| Revoke | Has an earlier version revoked at its CA, from the version history, with a reason; the password is required first. See below |
| Pull address | Under the certificate's information: a server JEC cannot reach, as one on a LAN or behind NAT, fetches the version in use with its private key itself, with curl; see below |
| Delete certificate | Deletes every version and private key of the certificate. Copies already deployed elsewhere are not affected |

Each row of the certificate list also offers the common actions in a menu.

## Pulled by servers

A server JEC cannot reach can fetch the certificate itself, from time to
time:

1. Under the certificate's information on its page, choose Set up beside
   Pull address, enter the password and make a key. The key shows once.
2. On the server, run the commands the page gives from time to time, as
   hourly from cron:

   ```sh
   curl -fsS -H "Authorization: Bearer jecpull_..." --etag-compare fullchain.pem.etag --etag-save fullchain.pem.etag \
     -o fullchain.pem https://jec.example.com/api/v1/certs/pull/fullchain.pem
   ```

   While the certificate is unchanged, JEC answers 304 and the file stays
   as it is. `fullchain.pem`, `privkey.pem`, `cert.pem` and `chain.pem` can
   be fetched; reload what uses the certificate afterwards.

The key goes in a header, not in the address, so access logs do not keep
it. A new key ends the old at once, and pulling can be stopped at any
time. The audit log records each fetch.

## Revoking

A version can be revoked when an ACME account, or a plugin's source that
supports it, issued it, it has not expired, and it is not the version in
use. The CA then lists it as
revoked, and clients that check revocation stop trusting it; this cannot
be undone. To revoke the version in use, for example when its private key
has leaked, choose **Issue now** first, and revoke the old version once
the new one is deployed: the sites never serve a revoked certificate.

| Reason | When |
|---|---|
| A newer certificate replaces it | The usual reason for an earlier version |
| Its private key has leaked | The private key was exposed; JEC uses a new key for every version |
| Its domains are no longer in use | The domains were given up |
| No particular reason | None of the above |

When a site is no more, **Also revoke it at the CA** in the delete dialog,
after the password, revokes every version still valid, the one in use
last, as no longer in use, then deletes the certificate. Should the CA
refuse one, nothing is deleted and the version in use stays valid.

Certificates from other sources, such as uploads or vendors' services,
are revoked where they were obtained.
