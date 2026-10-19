# Getting started

[中文](getting-started.zh-CN.md)

This page deploys JEC with Docker Compose, then issues and deploys a first
certificate. For other ways to install JEC, see
[Installation and upgrades](installation.md).

## Prerequisites

- A Linux host with Docker and Docker Compose.
- A domain hosted at a supported DNS provider, such as Tencent Cloud
  DNSPod, Alibaba Cloud DNS or Cloudflare, and an API key of that
  provider.

> [!NOTE]
> Until the first stable release, `docker-compose.yml` runs the newest
> test release, the image tag `beta`.

## 1. Start JEC

```sh
mkdir jec && cd jec
curl -fsSLO https://raw.githubusercontent.com/XMZO/JustEnoughCerts/main/deploy/docker-compose.yml
mkdir -p data && sudo chown 65532:65532 data
docker compose up -d
docker logs jec 2>&1 | grep setup_token
```

The last command prints the setup token from the log, the part after
`setup_token=`.

## 2. Create the administrator account

JEC listens on `127.0.0.1:8080` of the server only. Set up HTTPS access as
[Reverse proxy and CDN](../operations/reverse-proxy.md) describes and open
the panel's address, or, for a first look, keep an SSH tunnel open from
your own computer and open `http://localhost:8080`:

```sh
ssh -N -L 8080:127.0.0.1:8080 <user>@<host>
```

Enter the setup token, a user name and a password, and choose Create
account. Until the account exists, the
server generates a new token at each start, so only someone with access to
the logs can complete the setup.

## 3. Add a certificate source

On the Certificate sources page, choose Add a source → ACME account,
select Let's Encrypt as the certificate authority, enter the email address
for the certificate authority's notices, and save.

## 4. Add a validation method

On the Domain validation page, choose Add a validation method, search for
the DNS provider that hosts the domain, enter its API key, and choose
Save and test. When the test passes, the domains the account manages are
listed.

## 5. Add a deploy target (optional)

On the Deploy targets page, choose Add a target and select where the
certificate goes, such as an Alibaba Cloud ESA site, Nginx Proxy Manager or
a local directory; fill in its settings and choose Save and test. Without
a target, the certificate is kept in JEC only.

## 6. Add the certificate

1. On the Certificates page, choose Add certificate and select the ACME
   account of step 3 as the source.
2. Enter the domains, pressing Enter after each; the first is the primary
   domain, and wildcards are written as `*.example.com`.
3. Select the DNS provider of step 4 as the validation method, and the
   deploy targets, if any, under Deploy to.
4. After submitting, the issuance page shows each step: placing the order,
   adding the validation records, the validation by the certificate
   authority and downloading the certificate.

Once issued, the certificate is deployed to its targets automatically. The
certificate's page shows its validity, the results of its deployments and
of the online check; from then on, JEC renews it before it expires, as
[Renewal](../usage/renewal.md) describes.

## Next steps

- The objects of JEC and how they relate: [Concepts](concepts.md).
- Messages when something happens to a certificate:
  [Notifications](../usage/notifications.md).
- Backing up the data directory and the master key:
  [Data and backups](../operations/backup.md).
- Running behind a reverse proxy:
  [Configuration reference](../operations/configuration.md#behind-a-reverse-proxy).
