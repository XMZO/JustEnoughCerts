# Installation and upgrades

[中文](installation.zh-CN.md)

JEC is released as a container image and as static binaries. Docker
Compose is the recommended way to deploy it.

## Docker Compose

```sh
mkdir jec && cd jec
curl -fsSLO https://raw.githubusercontent.com/XMZO/JustEnoughCerts/main/deploy/docker-compose.yml
mkdir -p data && sudo chown 65532:65532 data
docker compose up -d
```

- **Image**: `ghcr.io/xmzo/justenoughcerts`, for linux/amd64 and
  linux/arm64. The tag `latest` is the newest stable release and `beta`
  the newest test release; a version, such as `0.1.0-alpha.5`, can be
  given as well.
- **User**: the container runs as 65532:65532 by default, so `./data` must
  be writable by that user. To run as the owner of an existing directory
  instead, set `user:` in `docker-compose.yml`; if the directory is not
  writable, JEC reports it clearly at startup.
- **Port**: JEC listens on 8080 by default; `JEC_LISTEN` changes it (see
  the [Configuration reference](../operations/configuration.md)).
- **Time zone**: `docker-compose.yml` sets `TZ: Asia/Shanghai` by default;
  the panel's own time zone can also be chosen on the Settings page.

### Optional mounts

`docker-compose.yml` contains the following mounts as comments; enable
them as needed:

| Mount | Purpose |
|---|---|
| `/etc/letsencrypt:/etc/letsencrypt:ro` | The certificate source "certificates on this machine" reads the certificates that certbot, or acme.sh, renews. Their keys usually belong to root, so JEC must then run as `user: "0:0"` |
| `/var/run/docker.sock` | The deploy target "Docker container" writes certificates into other containers and has them reload. The socket grants full control of Docker; a socket proxy that allows only `CONTAINERS=1` and `POST=1` is safer |

## Binaries

Each version provides static binaries for Linux and macOS, on amd64 and
arm64, on the [releases page](https://github.com/XMZO/JustEnoughCerts/releases).
There are no Windows builds; on Windows, use Docker or WSL.

```sh
tar -xzf jec_<version>_linux_amd64.tar.gz
./jec serve -data-dir /var/lib/jec
```

`jec <command> -h` lists the flags of a command. The other commands are
`jec version` (version information), `jec healthcheck` (a health check)
and `jec reset-password` (resets the password).

## Upgrades

- **Docker Compose**: pull the new image and recreate the container. The
  database is migrated automatically at startup.

  ```sh
  docker compose pull
  docker compose up -d
  ```

- **Binaries**: stop JEC, replace the binary and start it again.

Back up before upgrading; see [Data and backups](../operations/backup.md).
To verify the signatures of a release, see
[Verifying releases](../operations/releases.md).
