# FAQ

[中文](faq.zh-CN.md)

## Which certificate source should I use?

ACME, wherever the certificate authority offers it: certificates from
Let's Encrypt and other free CAs are issued and renewed without any manual
step. For paid certificates, use the CA's ACME service with External
Account Binding where it has one, or a vendor's service that JEC supports;
otherwise upload the certificates. See [Certificate sources](../usage/sources.md).

## The panel does not open at `http://<server>:8080`

`docker-compose.yml` publishes JEC on `127.0.0.1:8080`, which only the
server itself reaches. Open the panel through a reverse proxy with HTTPS,
or through an SSH tunnel; see [Reverse proxy and CDN](../operations/reverse-proxy.md).

## Checking the server

`jec doctor` checks the data directory, the database, the master key, the
panel's own HTTPS certificate when it has one, the network to a CA and the
server's clock, a line each, and exits with 1 when something is wrong:

```sh
docker exec jec /jec doctor                                   # Docker
sudo -u jec /usr/local/bin/jec doctor -data-dir /var/lib/jec  # systemd service
```

`-ca` names another ACME directory to reach than Let's Encrypt's;
`-offline` skips the network and the clock.

## An issuance failed. How do I find out why?

Open the certificate, then the latest issuance: its page lists each step
with its log, and **Show raw details** adds the raw errors. Common causes are
credentials of the DNS provider without the permission to add records, a
DNS provider that does not serve the domain, CAA records that exclude the
CA, and the CA's rate limits. JEC retries failures on its own; see
[Failures and retries](../usage/renewal.md#failures-and-retries). To ask
for help, the copy button next to Download log copies the whole log with
the version of JEC, ready to paste into a report.

## The validation records never become visible

JEC asks the authoritative DNS servers of the domain whether the records
are there. The DNS provider set for the certificate must be the one the
domain's NS records point to. Where the server resolves names differently
from the internet, set public resolvers in `JEC_DNS_RESOLVERS` (see
[DNS resolvers](../operations/configuration.md#dns-resolvers)). For a DNS
provider without an API, delegate `_acme-challenge` with a CNAME record
to a zone at a supported provider (see [Domain validation](../usage/validation.md)).

## The CA refuses because of CAA records

CAA records in a domain's DNS name the CAs that may issue its
certificates. Before ordering from a well-known CA, such as Let's
Encrypt, ZeroSSL, Google Trust Services, SSL.com, Sectigo or DigiCert,
JEC reads them and stops with the record to add, such as
`0 issue "letsencrypt.org"`, when they leave the CA out. The records of a
parent domain count for its subdomains that have none. When adding a
certificate, the page tells of the domains whose CAA records leave out the
CA of the ACME account chosen, before any issuance fails.

## A site still serves the old certificate after the deployment

The online check connects to each domain and reports this, by
notification too, when the new certificate has not shown 30 minutes
after it took effect (see [Online checks](../usage/renewal.md#online-checks)).
Check the deploy target's settings, such as the site or domain it updates,
and its latest deployment log; CDNs can take some minutes to apply a
certificate. **Deploy again** on the certificate's page repeats the
deployment.

## The server cannot reach a CA or a service abroad

ACME accounts and the providers that reach services abroad, such as
Telegram, Cloudflare and AWS, have a proxy setting of their own among
their advanced settings (see [Proxies](../operations/configuration.md#proxies)).

## I forgot the password, or lost the authenticator

Reset the password on the server; this also turns two-factor
authentication off, ends every session and deletes every API token:

```sh
docker exec -it jec /jec reset-password                               # Docker
sudo -u jec /usr/local/bin/jec reset-password -data-dir /var/lib/jec  # systemd service
```

## How do I move JEC to another server?

Stop JEC, copy the whole data directory, `secret.key` included, to the new
server, and start the same version of JEC or a newer one there. A backup
downloaded from the Settings page works as well, together with
`secret.key`. See [Data and backups](../operations/backup.md).

## The master key is lost

JEC refuses to start with a database that another master key encrypted,
as the private keys and credentials in it cannot be decrypted. Restore
`secret.key` from a backup of the data directory. Without it, start with
a new data directory and add the certificates and providers again.

## How do I upgrade?

Pull the new image and recreate the container, or run
`jec service install` again with the new binary; the database is migrated
at startup. See [Upgrades](installation.md#upgrades).
