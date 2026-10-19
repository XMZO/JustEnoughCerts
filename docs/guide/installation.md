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

- **Image**: `ghcr.io/xmzo/justenoughcerts`, for linux/amd64,
  linux/arm64 and, from 0.1.0-alpha.6, linux/arm/v7
  (32-bit ARM, as on a Raspberry Pi with a 32-bit system). The tag
  `latest` is the newest stable release and `beta` the newest test
  release; a version, such as `0.1.0-alpha.6`, can be given as well.
  `docker-compose.yml` uses `beta` until the first stable release.
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
arm64, and for Linux on 32-bit ARM (`linux_armv7`, from 0.1.0-alpha.6), on
the [releases page](https://github.com/XMZO/JustEnoughCerts/releases).
There are no Windows builds; on Windows, use Docker or WSL.

```sh
tar -xzf jec_<version>_linux_amd64.tar.gz
./jec serve -data-dir /var/lib/jec
```

`jec <command> -h` lists the flags of a command. The other commands are
`jec version` (version information), `jec healthcheck` (a health check),
`jec reset-password` (resets the password) and `jec service` (below).

### systemd service

On Linux with systemd, `jec service install` runs JEC as a service:

```sh
tar -xzf jec_<version>_linux_amd64.tar.gz
sudo ./jec service install
journalctl -u jec | grep setup_token
```

The command copies the binary to `/usr/local/bin/jec`, adds the system
user `jec`, writes `/etc/systemd/system/jec.service`, and enables and
starts the service, with its data in `/var/lib/jec`. The last command
prints the setup token. `jec service unit` prints the unit without
installing anything; the same unit is in
[deploy/systemd/jec.service](../../deploy/systemd/jec.service) for
installing by hand.

| Flag | Default | Description |
|---|---|---|
| `-bin` | `/usr/local/bin/jec` | Where the binary is copied to, and the service runs it from |
| `-data-dir` | `/var/lib/jec` | The data directory |
| `-user` | `jec` | The user the service runs as, added if missing; `root` to write anywhere |
| `-start` | `true` | `-start=false` enables the service without starting it |

Settings go in `/var/lib/jec/config.yaml`; see the
[Configuration reference](../operations/configuration.md). Further
changes to the service, such as environment variables, go in
`sudo systemctl edit jec`, which keeps them apart from the unit, so that
installing again preserves them. `sudo jec service uninstall` stops the
service and removes the unit, and keeps the data, the user and the binary.

The service runs in a sandbox: it cannot write to `/usr`, `/boot` or
`/etc`, and elsewhere the user `jec` writes only where file permissions
allow. Features that reach outside the data directory need access, added
with `sudo systemctl edit jec` under `[Service]`:

| Feature | Access |
|---|---|
| A local directory or website directory under `/etc`, such as `/etc/nginx/ssl` | `ReadWritePaths=/etc/nginx/ssl`, and write permission for `jec` on the directory |
| A local directory or website directory elsewhere | Write permission for `jec` on the directory |
| The Docker container target | `SupplementaryGroups=docker` |
| Certificates on this machine, from certbot or acme.sh | Their keys are readable by root only: install with `-user root`, or have certbot's `--deploy-hook` copy them where `jec` can read them |
| A command after writing that reloads a service, such as `systemctl reload nginx` | A polkit rule, as below, or `-user root` |

A polkit rule that lets `jec` reload nginx, in
`/etc/polkit-1/rules.d/50-jec.rules`:

```js
polkit.addRule(function (action, subject) {
  if (action.id == "org.freedesktop.systemd1.manage-units" &&
      action.lookup("unit") == "nginx.service" &&
      action.lookup("verb") == "reload" &&
      subject.user == "jec") {
    return polkit.Result.YES;
  }
});
```

## Upgrades

- **Docker Compose**: pull the new image and recreate the container. The
  database is migrated automatically at startup.

  ```sh
  docker compose pull
  docker compose up -d
  ```

- **systemd service**: run `sudo ./jec service install` again with the
  new binary; it replaces the binary and restarts the service.
- **Other binaries**: stop JEC, replace the binary and start it again.

Back up before upgrading; see [Data and backups](../operations/backup.md).
To verify the signatures of a release, see
[Verifying releases](../operations/releases.md).
