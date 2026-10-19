# Data and backups

[中文](backup.zh-CN.md)

## The data directory

JEC keeps all of its data in the data directory (`/data` in the container,
which is `./data` next to `docker-compose.yml`):

| File | Contents |
|---|---|
| `jec.db` | The SQLite database: certificates, private keys, providers and credentials (sensitive values encrypted), issuance and deployment records, settings; absent when the data is in [PostgreSQL](postgresql.md) |
| `secret.key` | The master key, which decrypts the sensitive values of the database; not created when the master key comes from `JEC_SECRET_KEY` |
| `config.yaml` | The optional configuration of the server; see the [Configuration reference](configuration.md) |
| `plugins/` | Optional plugin programs; see [Plugins](../development/plugins.md) |
| `hooks/` | The scripts of the script notification channel |

> [!WARNING]
> Without the master key, stored private keys and credentials cannot be
> decrypted. Back up the master key together with the database, or keep it
> safely elsewhere.

## Backing up

Either of the following:

- Back up the whole data directory, `secret.key` included.
- On the Settings page, under Data and backups, choose Download a backup
  and enter the password to download a consistent copy of the database.
  JEC keeps running meanwhile. The backup does not contain the master key.

To keep the configuration in Git or move it to another instance, use the
YAML configuration file; see [Configuration file](config-file.md).

## Daily backups

Under Settings, Data and backups, choose how many daily backups JEC keeps:
off (the default), 3, 7, 14 or 30. JEC then makes one a day in `backups/`
of the data directory, named after the time it was made in UTC, such as
`jec-20261020-020000.db`, and removes the oldest beyond the number chosen.
They are like a downloaded backup: without the master key, and restored
the same way. Turned off, JEC leaves the backups it made. Copy them off
the machine too: a backup in the data directory does not survive the loss
of the disk.

## Restoring

**In the interface**: on the Settings page, choose Restore, upload the
backup and enter the password. JEC first checks the backup: the file must
be intact, be a JEC database, be encrypted with the current master key,
and not come from a newer version of JEC. JEC then stops, Docker or the
systemd service starts it again with the backup, and the previous
database, with its WAL file, is kept in the data directory as
`jec.db.before-restore-<time>`. Otherwise, start JEC again yourself.

**By hand**: stop JEC, place the backup in the data directory as `jec.db`,
and start JEC.
