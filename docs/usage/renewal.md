# Renewal

[中文](renewal.zh-CN.md)

Certificates issued by a certificate source are renewed automatically
before they expire; certificates uploaded manually are not renewed, but
reminders are sent before they expire.

## When certificates are renewed

| Rule | Description |
|---|---|
| Renewal window | A certificate enters its renewal window a number of days before it expires, 30 by default; the default is set under Renewal defaults on the Settings page, and each certificate can have its own under Renewal settings on its page |
| Daily check time | Within the window, renewal starts at the daily check time, 02:00 by default, in the panel's time zone |
| Urgent renewal | With seven days or fewer left, renewal starts at once, without waiting for the check time |
| CA advice | When the certificate authority advises an earlier renewal through ACME Renewal Information (ARI, RFC 9773), for example before a revocation, renewal starts at the advised time |

The certificate list and each certificate's page show the next renewal.
The panel's time zone is chosen under Appearance on the Settings page, and
follows the server's (the `TZ` environment variable) by default.

## Failures and retries

- Failed issuances and deployments are retried with exponential backoff:
  the delay starts at ten minutes and doubles up to twelve hours; when a
  source asks for a wait (`Retry-After`), it is honored, up to 24 hours.
- The number of retries is set under Renewal defaults on the Settings
  page: 7 by default, from 0 to 20.
- Errors that retries cannot fix, such as a missing EAB or a refusal by the
  certificate authority, are not retried.
- After an issuance fails for good, renewal pauses for 24 hours before the
  next attempt; an issuance can be started by hand at any time on the
  certificate's page.
- Operations that cost money or cannot be undone, such as purchases,
  renewals of orders and revocations, are never retried automatically.

## Expiry reminders

A reminder is sent when a certificate enters its renewal window, when
seven days are left and when it has expired, once each (the notification
event for certificates that are expiring or expired). Certificates
uploaded manually receive them as well.

## Online checks

The online check module connects to port 443 of each domain as a browser
does, and verifies the certificate actually served:

- every six hours, and two minutes after each deployment;
- if the old certificate is still served 30 minutes after the current
  version took effect, for example because a CDN has not applied it, a
  notification is sent;
- the Online check section of the certificate's page shows the result for
  each domain, and can run a check at once.

Where the DNS of a domain answers by region, sending visitors from
mainland China to Alibaba Cloud ESA and others to Cloudflare, say, JEC
connects as each of them would and shows the two results apart, as one
line when both sides serve the same certificate. A certificate from
another CA, such as a CDN's own, shows as not deployed by JEC, without an
alert; only an older certificate from the same CA raises one.
