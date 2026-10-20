# Expiry watch

[中文](sites.zh-CN.md)

The Expiry watch page follows two things JEC does not renew, but whose end
breaks a website all the same:

- **Site certificates**: the certificates of sites that JEC does not
  manage, such as a partner's or another team's: when each ends, and
  whether browsers trust it. JEC only connects to them; it changes nothing
  there.
- **Domain registrations**: when the registrations of the domains of your
  certificates and watched sites end, as their registries tell through
  RDAP, or WHOIS where there is none.

## Watching a site

1. On the Expiry watch page, choose Watch a site.
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

## Domain registrations

The registered domains of your certificates and watched sites (`shop.co.uk`
for `www.shop.co.uk`) join the list by themselves, and leave it with the
certificate. Addresses, names of private networks and domains for examples,
such as `example.com`, do not count. Add a domain adds any other.

1. JEC asks each registry, through the RDAP servers IANA lists, when the
   registration ends and with which registrar, or through the WHOIS server
   IANA names where the TLD has no RDAP (as `.cn`, `.io` and `.co.uk`):
   once a day, and every 12 hours while it ends soon, to see a renewal
   early.
2. On a certificate's page, the row Domain registration shows when the
   domains it is under end.

| State | Meaning |
|---|---|
| OK | More than 30 days left |
| Ends soon | 30 days or less left |
| Ended | Past the end the registry told |
| End not told | The registry of the TLD has neither RDAP nor WHOIS, or does not publish the end (as for `.de`); looked up again in 7 days |
| Lookup failed | The registry did not answer, or answered an error; the last result is kept, and the lookup tried again in 2 hours |

Lookups go only to IANA and to the public RDAP and WHOIS servers of the
registries, and carry only the domain.

## Notes

| Event (in a channel's settings) | Default | When |
|---|---|---|
| A watched site's certificate ends soon or ended | On | Three weeks before the end, a week before, and when it has ended; a new certificate on the site starts over |
| A domain registration ends soon or ended | On | 30 days before the end, 7 days before, and when it has ended; a renewal starts over |

The sites and domains to look at show in the overview, and in the number
that leads the browser tab's title. Their ends are also in the
[Prometheus metrics](../operations/monitoring.md), the calendar feed and the
Telegram bot's `/sites`. The module "Expiry watch" provides the page and
can be switched off on the Modules page.
