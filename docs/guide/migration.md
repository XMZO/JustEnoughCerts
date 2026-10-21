# Moving from certbot or acme.sh

[中文](migration.zh-CN.md)

JEC can take over from certbot or acme.sh at once, or step by step: it
can read the certificates they renew while it deploys them and watches
their expiry, and issue them itself once you are ready.

## Choosing how

| Way | When |
|---|---|
| Keep the old client renewing, and let JEC read its files | Its renewals work; you want deployment to more places, notifications, online checks and one overview first |
| Let JEC issue | You want JEC to renew as well, and to stop the old client |

## Keeping the old client

1. Let JEC read the files. The private keys belong to root and only root
   reads them. Rather than running JEC as root, give them JEC's group with
   group read (for the Docker image, group 65532; for the systemd service,
   `jec`). certbot keeps the group and group read of a key for the keys of
   later renewals (tried with certbot 5):

   ```sh
   sudo chgrp -R 65532 /etc/letsencrypt/live /etc/letsencrypt/archive
   sudo chmod -R g+rX /etc/letsencrypt/live /etc/letsencrypt/archive
   ```

   acme.sh keeps a domain's key across renewals: do the same to its home
   directory, such as `/root/.acme.sh`, and let JEC through `/root` with
   `sudo chmod o+x /root`. In Docker, also enable the mount of the
   directory in `docker-compose.yml`; see
   [Optional mounts](installation.md#optional-mounts).
2. Under Certificate sources, add **Certificates on this machine** with the
   directory: `/etc/letsencrypt`, or the home directory of acme.sh.
3. Add the certificates with that source: the page lists those the
   directory holds, to pick from; under Several, Add all takes them all,
   with the deploy targets they need.
4. When the old client renews, JEC takes the new certificate in at its next
   look and deploys it.

## Letting JEC issue

1. Add an ACME account at the same CA (a new account is fine), and the
   validation the old client used: the same DNS provider, with the same
   API keys, or the website directory of its webroot.
2. Add the certificate with the same domains. JEC issues a new one at once;
   the old one stays valid until it expires.
3. Deploy it where the server reads it: a local directory target with the
   server's paths, and the command that reloads it. JEC's default file
   names are certbot's, so paths into `/etc/letsencrypt/live/<domain>` keep
   working when that is the directory written to.
4. Stop the old client's renewals once JEC has deployed:

   ```sh
   sudo systemctl disable --now certbot.timer   # certbot from packages
   acme.sh --uninstall-cronjob                   # acme.sh
   ```

   Check also for a certbot line in `/etc/cron.d` or the crontab.

## What carries over

| Old client | In JEC |
|---|---|
| A certbot deploy hook | The command after writing of a local directory target, with certbot's `RENEWED_LINEAGE` and `RENEWED_DOMAINS` set |
| `acme.sh --install-cert ... --reloadcmd` | A local directory target, with the file names and the command |
| A certbot DNS plugin, such as certbot-dns-cloudflare | The DNS provider of the same name, with its API key |
| An acme.sh DNS API, such as `dns_dp`, `dns_ali` or `dns_cf` | Tencent Cloud DNSPod (with Tencent Cloud API keys), Alibaba Cloud DNS, Cloudflare |
| `--webroot` | A website directory validation, with the same directory |

See [Domain validation](../usage/validation.md) and
[Deploy targets](../usage/targets.md) for the settings of each.
