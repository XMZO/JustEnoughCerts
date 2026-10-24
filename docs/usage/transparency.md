# Certificate transparency

[中文](transparency.zh-CN.md)

CAs log every certificate they issue in public certificate transparency
logs. JEC searches them through [Cert Spotter](https://sslmate.com/certspotter/)
for the registered domains it holds certificates under, with the names
under them, and tells which certificates JEC does not hold: those with a
public key that none of JEC's certificates has. Off by default.

## Switching it on

1. In the sidebar, open Certificate transparency (on phones, under More),
   and choose "Switch on". This sends those domains to Cert Spotter, of
   SSLMate.
2. Optionally, under "API key", enter a Cert Spotter API key. Without
   one, Cert Spotter answers few searches an hour, and JEC searches the
   rest later.

JEC searches each domain every 6 hours; "Search now" searches at once.

## The certificates found

A certificate issued (by its start date) before the search was first
switched on was there before: it is listed as "There before" and not told
of. One issued after, also under a domain JEC holds certificates under
only later, that JEC still does not recognize 2 hours on (time for JEC to
store its own) is told of and listed as "To look at".

Certificates of the same names from the same issuer, as those a CDN or
another tool renews again and again, make one row, with how many there
are and the dates of the latest. The rows come a page at a time, as many
as the window holds. For each row:

| Action | Effect |
|---|---|
| Trust such | The certificates the same issuer makes for the same names are not told of, as those a CDN such as Cloudflare makes for itself. Listed under Trusted, where "Stop trusting" takes it back |
| Ignore | The certificates of the row go from the list |
| Watch the site (menu) | Watches one of its names among the [sites](sites.md) |
| Hand to JEC (menu) | Adds a certificate for its names that JEC issues and renews |
| View (menu) | Opens the latest certificate of the row on crt.sh |

A certificate that nobody you know asked for may mean someone else
controls the domain's DNS or website: find out how, and have the CA revoke
the certificate. Certificate transparency was tried against the real Cert
Spotter, without an API key.

## Notes

| Event (in a channel's settings) | Default | When |
|---|---|---|
| A certificate JEC does not hold shows up in certificate transparency logs | On | Once for each certificate, 2 hours after it is found |

The certificates to look at show in the overview, in the number that leads
the browser tab's title, in the weekly digest, in the
[Prometheus metrics](../operations/monitoring.md) and in the Telegram bot's
`/sites`. The module "Expiry watch" provides the page, with that of the
[sites](sites.md), and can be switched off on the Modules page.
