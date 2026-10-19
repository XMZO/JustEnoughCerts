# Site watch

[中文](sites.zh-CN.md)

The Site watch page follows the certificates of sites that JEC does not
manage, such as a partner's or another team's: when each ends, and
whether browsers trust it. JEC only connects to them; it changes nothing
there.

## Watching a site

1. On the Site watch page, choose Watch a site.
2. Enter a host name, with a port when it is not 443, or a URL, such as
   `https://example.com`. A name is optional.
3. JEC checks the site at once, then every 6 hours, or an hour after it did
   not answer. Check now on a row checks it again.

| State | Meaning |
|---|---|
| Healthy | Trusted, and more than three weeks left |
| Ending soon | Three weeks or less left |
| Expired | Past its end |
| Not trusted | Not signed by a trusted CA, or not for the host; the reason shows below |
| No answer | No TLS on the port; the certificate seen last is kept |

## Notes

The channels with the event "A watched site's certificate ends soon or
ended", on by default, are told three weeks before an end, a week before,
and when it has ended; a new certificate on the site starts over. The
watched sites are also in the [Prometheus metrics](../operations/monitoring.md).
The module "Site watch" provides the page and can be switched off on the
Modules page.
