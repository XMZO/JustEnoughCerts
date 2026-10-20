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

## Backups before upgrades

When a newer JEC starts on a SQLite database that its migrations are about
to change, it first copies the database to `backups/` as
`upgrade-<time>.db` (UTC) and keeps the latest three such copies. To go
back to the earlier version, stop JEC, copy the file into the data
directory as `restore.db`, and start the earlier version: it takes the
backup in place of the database and keeps the later one beside it. With
PostgreSQL, back the database up with `pg_dump` before upgrading.

## Offsite backups

The module "Offsite backups" copies each daily backup to S3 compatible
object storage (AWS S3, Cloudflare R2, MinIO, the S3 interfaces of Alibaba
Cloud OSS, Tencent Cloud COS and others) or to a WebDAV folder (Nextcloud,
Synology, Alist, Jianguoyun and others). It needs the daily backups turned
on.

1. Create a bucket and an access key that may write to it.
2. Under Settings, Data and backups, choose "Set up the copies" next to
   "Offsite backups", choose "S3 compatible", then fill in:

   | Field | Value |
   |---|---|
   | Endpoint | The address of the S3 interface, such as `https://s3.us-east-1.amazonaws.com` or `https://<account>.r2.cloudflarestorage.com` |
   | Bucket | The bucket's name |
   | Region | `us-east-1` when empty; `auto` for Cloudflare R2 |
   | Path prefix | Optional, such as `jec/` |
   | Path-style addresses | The bucket in the path of the address, as MinIO and other self-hosted services usually need |
   | Access key, secret key | The key from step 1 |

3. Save. JEC first writes and removes a small file, `jec-write-test.txt`,
   and refuses settings that cannot write.

For WebDAV, choose "WebDAV" and give the address of the folder for the
backups (such as `https://<host>/remote.php/dav/files/<user>/jec/` for
Nextcloud, or `https://dav.jianguoyun.com/dav/jec/` for Jianguoyun), an
optional subfolder, the user name and the password (an app password for
Jianguoyun); JEC makes subfolders that are not there.

JEC looks for new backups every 15 minutes and copies each once, under
its own name; the row in the settings shows how many were copied, when,
and the last error. JEC removes nothing from the bucket or folder: prune
old copies with the bucket's lifecycle rules, or by hand. The copies are like the backups
themselves, without the master key; keep the key somewhere else.

The module was tried against the S3 interface of SeaweedFS and the WebDAV
server of rclone only.

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
